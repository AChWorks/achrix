// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// MetadataPath follows RFC 8414 issuer-path insertion.
func (s *Server) MetadataPath() string  { return "/.well-known/oauth-authorization-server" + s.path }
func (s *Server) AuthorizePath() string { return s.path + "/authorize" }
func (s *Server) TokenPath() string     { return s.path + "/token" }
func (s *Server) RevokePath() string    { return s.path + "/revoke" }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if r.TLS == nil || r.Host != s.issuer.Host || r.URL.Host != "" || r.URL.RawPath != "" || r.URL.Opaque != "" {
		http.Error(w, "invalid issuer host", http.StatusForbidden)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "temporarily busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	requestID, randomError := token()
	if randomError != nil {
		s.report(ctx, "request_id", "random_unavailable", randomError)
		http.Error(w, "authorization temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx = context.WithValue(ctx, requestIDKey{}, requestID)
	w.Header().Set("X-Request-ID", requestID)
	r = r.WithContext(ctx)
	switch r.URL.Path {
	case s.MetadataPath():
		if r.Method != http.MethodGet || r.URL.RawQuery != "" {
			http.Error(w, "unsupported metadata request", http.StatusBadRequest)
			return
		}
		s.metadata(w)
	case s.AuthorizePath():
		if r.Method == http.MethodGet {
			s.beginAuthorize(w, r)
		} else if r.Method == http.MethodPost {
			s.finishAuthorize(w, r)
		} else {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	case s.TokenPath():
		if r.Method != http.MethodPost {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
			return
		}
		s.tokenHTTP(w, r)
	case s.RevokePath():
		if r.Method != http.MethodPost {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
			return
		}
		s.revokeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) metadata(w http.ResponseWriter) {
	scopes, methods := []string{}, []string{}
	for _, c := range s.clients {
		if !c.Enabled {
			continue
		}
		for _, scope := range c.Scopes {
			if !slices.Contains(scopes, scope) {
				scopes = append(scopes, scope)
			}
		}
		method := "none"
		if c.Confidential {
			method = "client_secret_basic"
		}
		if !slices.Contains(methods, method) {
			methods = append(methods, method)
		}
	}
	slices.Sort(scopes)
	slices.Sort(methods)
	metadata := map[string]any{
		"issuer":                                         s.config.Issuer,
		"authorization_endpoint":                         s.config.Issuer + "/authorize",
		"token_endpoint":                                 s.config.Issuer + "/token",
		"revocation_endpoint":                            s.config.Issuer + "/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"response_modes_supported":                       []string{"query"},
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
		"token_endpoint_auth_methods_supported":          methods,
		"revocation_endpoint_auth_methods_supported":     methods,
		"scopes_supported":                               scopes,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

func single(v url.Values, key string, required bool) (string, error) {
	values := v[key]
	if len(values) > 1 || required && (len(values) != 1 || values[0] == "") {
		return "", ErrInvalidGrant
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return "", nil
}
func requestForm(r *http.Request) (url.Values, error) {
	if r.URL.RawQuery != "" || len(r.Header.Values("Content-Type")) != 1 {
		return nil, ErrInvalidGrant
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		return nil, ErrInvalidGrant
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(body) > 8192 {
		return nil, ErrInvalidGrant
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, ErrInvalidGrant
	}
	for _, value := range values {
		if len(value) != 1 {
			return nil, ErrInvalidGrant
		}
	}
	return values, nil
}
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, registered string, parameters url.Values, hasState bool) {
	u, _ := url.Parse(registered) // constructor-validated registration only
	q := u.Query()
	for key, v := range parameters {
		if key == "state" && !hasState {
			continue
		}
		q.Set(key, v[0])
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}
func (s *Server) problem(w http.ResponseWriter, r *http.Request, phase string, err error) {
	s.report(r.Context(), phase, "unavailable", err)
	http.Error(w, "authorization temporarily unavailable", http.StatusServiceUnavailable)
}
func (s *Server) oauthError(w http.ResponseWriter, code string, status int) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"oauth\"")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// requestIDKey is deliberately private. Callbacks receive only a generated
// non-secret correlation value; request-controlled IDs are not accepted.
type requestIDKey struct{}

// RequestID lets a protected product diagnostic sink associate an operation
// with its public response without accepting untrusted correlation headers.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
