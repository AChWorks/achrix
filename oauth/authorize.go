// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ceremony struct {
	Subject, ClientID, Redirect, Issuer, Resource, State, Challenge string
	SessionHash, CSRFHash                                           []byte
	HasState                                                        bool
	Scopes                                                          []string
	Expires                                                         time.Time
}

func validChallenge(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}
func requestedScopes(c Client, raw string) ([]string, error) {
	if raw == "" || len(raw) > 1024 {
		return nil, ErrInvalidGrant
	}
	parts := strings.Split(raw, " ")
	if len(parts) > len(c.Scopes) {
		return nil, ErrInvalidGrant
	}
	seen := map[string]bool{}
	for _, scope := range parts {
		if scope == "" || seen[scope] || !slices.Contains(c.Scopes, scope) {
			return nil, ErrInvalidGrant
		}
		seen[scope] = true
	}
	return parts, nil
}
func (s *Server) beginAuthorize(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) > 4096 {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}
	// No redirects until an unambiguous exact pre-registered callback is
	// established. RFC 9207 issuer error responses are safe only after this gate.
	id, idErr := single(v, "client_id", true)
	redirect, redirectErr := single(v, "redirect_uri", true)
	client, ok := s.client(id)
	if idErr != nil || redirectErr != nil || !ok || !slices.Contains(client.RedirectURIs, redirect) {
		http.Error(w, "invalid client or redirect", http.StatusBadRequest)
		return
	}
	state, stateErr := single(v, "state", false)
	if stateErr != nil || len(state) > 512 {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	hasState := v.Has("state")
	deny := func(code string) {
		s.redirect(w, r, redirect, url.Values{"error": {code}, "iss": {s.config.Issuer}, "state": {state}}, hasState)
	}
	fields := []string{"response_type", "client_id", "redirect_uri", "resource", "scope", "state", "code_challenge", "code_challenge_method"}
	for key := range v {
		if !slices.Contains(fields, key) {
			deny("invalid_request")
			return
		}
	}
	get := func(k string) string { x, _ := single(v, k, true); return x }
	for _, key := range []string{"response_type", "resource", "scope", "code_challenge", "code_challenge_method"} {
		if _, err = single(v, key, true); err != nil {
			deny("invalid_request")
			return
		}
	}
	if get("response_type") != "code" {
		deny("unsupported_response_type")
		return
	}
	if get("resource") != s.config.Resource {
		deny("invalid_target")
		return
	}
	if get("code_challenge_method") != "S256" || !validChallenge(get("code_challenge")) {
		deny("invalid_request")
		return
	}
	scopes, err := requestedScopes(client, get("scope"))
	if err != nil {
		deny("invalid_scope")
		return
	}
	session, err := s.config.Session(r.Context(), r)
	if err != nil || session.Subject == "" || session.Binding == ([32]byte{}) {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	candidate := Grant{Subject: session.Subject, ClientID: client.ID, Issuer: s.config.Issuer, Resource: s.config.Resource, Scopes: scopes}
	if err = s.active(r.Context(), candidate); err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			deny("access_denied")
			return
		}
		s.problem(w, r, "consent_policy", err)
		return
	}
	txID, err := token()
	if err != nil {
		s.problem(w, r, "random", err)
		return
	}
	csrf, err := token()
	if err != nil {
		s.problem(w, r, "random", err)
		return
	}
	now := s.now()
	_, err = s.config.Pool.Exec(r.Context(), "INSERT INTO oauth.ceremonies (id_hash,csrf_hash,session_hash,subject,client_id,redirect_uri,issuer,resource,scopes,client_state,challenge,expires_at,state_present) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(session_hash) DO UPDATE SET id_hash=EXCLUDED.id_hash,csrf_hash=EXCLUDED.csrf_hash,subject=EXCLUDED.subject,client_id=EXCLUDED.client_id,redirect_uri=EXCLUDED.redirect_uri,issuer=EXCLUDED.issuer,resource=EXCLUDED.resource,scopes=EXCLUDED.scopes,client_state=EXCLUDED.client_state,challenge=EXCLUDED.challenge,expires_at=EXCLUDED.expires_at,state_present=EXCLUDED.state_present",
		hash(txID), hash(csrf), session.Binding[:], session.Subject, client.ID, redirect, s.config.Issuer, s.config.Resource, scopes, state, get("code_challenge"), now.Add(5*time.Minute), hasState)
	if err != nil {
		s.problem(w, r, "consent_insert", err)
		return
	}
	name := client.Name
	if name == "" {
		name = client.ID
	}
	// Products own language, branding, accessibility and consent presentation.
	// The issuer retains transaction/CSRF verification and grant authority.
	s.config.RenderConsent(w, r, ConsentView{
		Subject:     session.Subject,
		ClientID:    client.ID,
		ClientName:  name,
		RedirectURI: redirect,
		Resource:    s.config.Resource,
		Scopes:      append([]string(nil), scopes...),
		FormAction:  s.AuthorizePath(),
		Transaction: txID,
		CSRF:        csrf,
		ExpiresAt:   now.Add(5 * time.Minute),
	})
}
func (s *Server) finishAuthorize(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin {
		http.Error(w, "invalid consent origin", http.StatusForbidden)
		return
	}
	v, err := requestForm(r)
	if err != nil {
		http.Error(w, "invalid consent", http.StatusBadRequest)
		return
	}
	for key := range v {
		if key != "transaction" && key != "csrf" && key != "decision" {
			http.Error(w, "invalid consent", http.StatusBadRequest)
			return
		}
	}
	id, _ := single(v, "transaction", true)
	csrf, _ := single(v, "csrf", true)
	decision, _ := single(v, "decision", true)
	if !validToken(id) || !validToken(csrf) || decision != "allow" && decision != "deny" {
		http.Error(w, "invalid consent", http.StatusBadRequest)
		return
	}
	session, err := s.config.Session(r.Context(), r)
	if err != nil || session.Subject == "" || session.Binding == ([32]byte{}) {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		s.problem(w, r, "consent_begin", err)
		return
	}
	defer tx.Rollback(r.Context())
	var c ceremony
	err = tx.QueryRow(r.Context(), "SELECT csrf_hash,session_hash,subject,client_id,redirect_uri,issuer,resource,scopes,client_state,challenge,expires_at,state_present FROM oauth.ceremonies WHERE id_hash=$1 FOR UPDATE", hash(id)).
		Scan(&c.CSRFHash, &c.SessionHash, &c.Subject, &c.ClientID, &c.Redirect, &c.Issuer, &c.Resource, &c.Scopes, &c.State, &c.Challenge, &c.Expires, &c.HasState)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "invalid or consumed consent", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.problem(w, r, "consent_read", err)
		return
	}
	now := s.now()
	if !now.Before(c.Expires) || subtle.ConstantTimeCompare(c.CSRFHash, hash(csrf)) != 1 ||
		subtle.ConstantTimeCompare(c.SessionHash, session.Binding[:]) != 1 || session.Subject != c.Subject ||
		c.Issuer != s.config.Issuer || c.Resource != s.config.Resource {
		http.Error(w, "invalid or expired consent", http.StatusForbidden)
		return
	}
	client, ok := s.client(c.ClientID)
	if !ok || !slices.Contains(client.RedirectURIs, c.Redirect) {
		http.Error(w, "invalid client", http.StatusForbidden)
		return
	}
	if _, err = tx.Exec(r.Context(), "DELETE FROM oauth.ceremonies WHERE id_hash=$1", hash(id)); err != nil {
		s.problem(w, r, "consent_consume", err)
		return
	}
	if decision == "deny" {
		if err = tx.Commit(r.Context()); err != nil {
			s.problem(w, r, "consent_commit", err)
			return
		}
		s.redirect(w, r, c.Redirect, url.Values{"error": {"access_denied"}, "iss": {s.config.Issuer}, "state": {c.State}}, c.HasState)
		return
	}
	grant, err := s.newGrant(session.Subject, c.ClientID, c.Scopes, now)
	if err != nil {
		s.problem(w, r, "random", err)
		return
	}
	if err = s.active(r.Context(), grant); err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			s.report(r.Context(), "consent", "policy_denied", err)
			http.Error(w, "authorization denied", http.StatusForbidden)
		} else {
			s.problem(w, r, "consent_policy", err)
		}
		return
	}
	code, err := token()
	if err != nil {
		s.problem(w, r, "random", err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO oauth.grants(id,subject,client_id,issuer,resource,scopes,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)", grant.ID, grant.Subject, grant.ClientID, grant.Issuer, grant.Resource, grant.Scopes, now); err != nil {
		s.problem(w, r, "grant_create", err)
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO oauth.codes(code_hash,grant_id,client_id,redirect_uri,challenge,expires_at) VALUES($1,$2,$3,$4,$5,$6)", hash(code), grant.ID, c.ClientID, c.Redirect, c.Challenge, now.Add(90*time.Second)); err != nil {
		s.problem(w, r, "code_create", err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.problem(w, r, "code_commit", err)
		return
	}
	s.redirect(w, r, c.Redirect, url.Values{"code": {code}, "iss": {s.config.Issuer}, "state": {c.State}}, c.HasState)
}
