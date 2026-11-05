// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zitadel/oidc/v3/pkg/op"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var verifierSyntax = regexp.MustCompile("^[A-Za-z0-9._~-]{43,128}$")

// TokenResponse is a credential-bearing result; callers must not log it.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (t TokenResponse) String() string   { return "oauth.TokenResponse[redacted]" }
func (t TokenResponse) GoString() string { return t.String() }

func (s *Server) authenticateClient(r *http.Request, v url.Values) (Client, error) {
	basic := r.Header.Values("Authorization")
	if len(basic) > 1 {
		return Client{}, ErrInvalidGrant
	}
	id, err := single(v, "client_id", false)
	if err != nil {
		return Client{}, ErrInvalidGrant
	}
	if len(basic) == 1 {
		username, secret, ok := r.BasicAuth()
		username, nameErr := url.QueryUnescape(username)
		secret, secretErr := url.QueryUnescape(secret)
		if !ok || nameErr != nil || secretErr != nil || username == "" || id != "" && id != username {
			return Client{}, ErrInvalidGrant
		}
		c, lookupErr := s.client(r.Context(), username)
		if lookupErr != nil {
			return Client{}, lookupErr
		}
		if !c.Confidential || len(secret) < 32 {
			return Client{}, ErrInvalidGrant
		}
		digest := sha256.Sum256([]byte(secret))
		if subtle.ConstantTimeCompare(digest[:], c.SecretDigest[:]) != 1 {
			return Client{}, ErrInvalidGrant
		}
		return c, nil
	}
	c, lookupErr := s.client(r.Context(), id)
	if lookupErr != nil {
		return Client{}, lookupErr
	}
	if c.Confidential {
		return Client{}, ErrInvalidGrant
	}
	return c, nil
}
func (s *Server) tokenHTTP(w http.ResponseWriter, r *http.Request) {
	v, err := requestForm(r)
	if err != nil {
		if !errors.Is(err, ErrInvalidGrant) {
			s.report(r.Context(), "token_form_read", "read_failed", err)
		}
		s.oauthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	for k := range v {
		if !slices.Contains([]string{"grant_type", "client_id", "code", "redirect_uri", "code_verifier", "resource", "refresh_token"}, k) {
			s.oauthError(w, "invalid_request", http.StatusBadRequest)
			return
		}
	}
	c, clientErr := s.authenticateClient(r, v)
	if clientErr != nil {
		if errors.Is(clientErr, ErrUnavailable) {
			s.problem(w, r, "token_client_registry", clientErr)
		} else {
			s.oauthError(w, "invalid_client", http.StatusUnauthorized)
		}
		return
	}
	kind, err := single(v, "grant_type", true)
	if err != nil || kind != "authorization_code" && kind != "refresh_token" {
		s.oauthError(w, "unsupported_grant_type", http.StatusBadRequest)
		return
	}
	resource, err := single(v, "resource", true)
	if err != nil || resource != s.config.Resource {
		s.oauthError(w, "invalid_target", http.StatusBadRequest)
		return
	}
	var result TokenResponse
	if kind == "authorization_code" {
		if v.Has("refresh_token") {
			s.oauthError(w, "invalid_request", http.StatusBadRequest)
			return
		}
		code, e1 := single(v, "code", true)
		redirect, e2 := single(v, "redirect_uri", true)
		verifier, e3 := single(v, "code_verifier", true)
		if e1 != nil || e2 != nil || e3 != nil {
			s.oauthError(w, "invalid_request", http.StatusBadRequest)
			return
		}
		if err = s.sdkValidate(r, v, false); err == nil {
			result, err = s.exchange(r.Context(), c, code, redirect, verifier)
		}
	} else {
		if v.Has("code") || v.Has("redirect_uri") || v.Has("code_verifier") {
			s.oauthError(w, "invalid_request", http.StatusBadRequest)
			return
		}
		refresh, e := single(v, "refresh_token", true)
		if e != nil {
			s.oauthError(w, "invalid_request", http.StatusBadRequest)
			return
		}
		if err = s.sdkValidate(r, v, true); err == nil {
			result, err = s.refresh(r.Context(), c, refresh)
		}
	}
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			s.oauthError(w, "invalid_grant", http.StatusBadRequest)
		} else {
			s.problem(w, r, "token_exchange", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func (s *Server) issue(ctx context.Context, tx pgx.Tx, g Grant, absoluteExpiry, timeNow time.Time) (TokenResponse, error) {
	access, e := token()
	if e != nil {
		return TokenResponse{}, e
	}
	refresh, e := token()
	if e != nil {
		return TokenResponse{}, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO oauth.access_tokens(token_hash,grant_id,expires_at) VALUES($1,$2,$3)", hash(access), g.ID, timeNow.Add(10*time.Minute)); e != nil {
		return TokenResponse{}, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO oauth.refresh_tokens(token_hash,grant_id,expires_at) VALUES($1,$2,$3)", hash(refresh), g.ID, absoluteExpiry); e != nil {
		return TokenResponse{}, e
	}
	return TokenResponse{AccessToken: access, RefreshToken: refresh, ExpiresIn: 600, TokenType: "Bearer", Scope: joinScopes(g.Scopes)}, nil
}
func joinScopes(scopes []string) string {
	if len(scopes) == 0 {
		return ""
	}
	result := scopes[0]
	for _, scope := range scopes[1:] {
		result += " " + scope
	}
	return result
}
func (s *Server) exchange(ctx context.Context, client Client, code, redirect, verifier string) (TokenResponse, error) {
	if !validToken(code) || !verifierSyntax.MatchString(verifier) || !slices.Contains(client.RedirectURIs, redirect) {
		return TokenResponse{}, ErrInvalidGrant
	}
	tx, err := s.config.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: code begin: %v", ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	var g Grant
	var expires time.Time
	var challenge, storedRedirect, storedClient string
	var revoked *time.Time
	err = tx.QueryRow(ctx, "SELECT g.id,g.subject,g.client_id,g.issuer,g.resource,g.scopes,g.created_at,g.revoked_at,c.expires_at,c.challenge,c.redirect_uri,c.client_id FROM oauth.codes c JOIN oauth.grants g ON g.id=c.grant_id WHERE c.code_hash=$1 FOR UPDATE OF c,g", hash(code)).
		Scan(&g.ID, &g.Subject, &g.ClientID, &g.Issuer, &g.Resource, &g.Scopes, &g.CreatedAt, &revoked, &expires, &challenge, &storedRedirect, &storedClient)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenResponse{}, ErrInvalidGrant
	}
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: code read: %v", ErrUnavailable, err)
	}
	now := s.now()
	actual := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(actual[:])
	if !now.Before(expires) || revoked != nil || g.ClientID != client.ID || storedClient != client.ID || storedRedirect != redirect ||
		subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) != 1 {
		return TokenResponse{}, ErrInvalidGrant
	}
	if err = s.active(ctx, g); err != nil {
		return TokenResponse{}, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM oauth.codes WHERE code_hash=$1", hash(code)); err != nil {
		return TokenResponse{}, fmt.Errorf("%w: consume code: %v", ErrUnavailable, err)
	}
	result, err := s.issue(ctx, tx, g, now.Add(14*24*time.Hour), now)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: issue: %v", ErrUnavailable, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return TokenResponse{}, fmt.Errorf("%w: code commit outcome uncertain: %v", ErrUnavailable, err)
	}
	return result, nil
}
func (s *Server) refresh(ctx context.Context, client Client, old string) (TokenResponse, error) {
	if !validToken(old) {
		return TokenResponse{}, ErrInvalidGrant
	}
	tx, err := s.config.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: refresh begin: %v", ErrUnavailable, err)
	}
	defer tx.Rollback(ctx)
	var g Grant
	var revoked, used *time.Time
	var expires time.Time
	err = tx.QueryRow(ctx, "SELECT g.id,g.subject,g.client_id,g.issuer,g.resource,g.scopes,g.created_at,g.revoked_at,r.expires_at,r.used_at FROM oauth.refresh_tokens r JOIN oauth.grants g ON g.id=r.grant_id WHERE r.token_hash=$1 FOR UPDATE OF r,g", hash(old)).
		Scan(&g.ID, &g.Subject, &g.ClientID, &g.Issuer, &g.Resource, &g.Scopes, &g.CreatedAt, &revoked, &expires, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenResponse{}, ErrInvalidGrant
	}
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: refresh read: %v", ErrUnavailable, err)
	}
	now := s.now()
	if g.ClientID != client.ID || g.Resource != s.config.Resource || g.Issuer != s.config.Issuer {
		return TokenResponse{}, ErrInvalidGrant
	}
	if used != nil {
		// One-use refresh replay revokes this grant family, even when a newer
		// rotated refresh token exists. The whole mutation commits on replay.
		if _, err = tx.Exec(ctx, "UPDATE oauth.grants SET revoked_at=COALESCE(revoked_at,$2) WHERE id=$1", g.ID, now); err != nil {
			return TokenResponse{}, fmt.Errorf("%w: replay revoke: %v", ErrUnavailable, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return TokenResponse{}, fmt.Errorf("%w: replay commit: %v", ErrUnavailable, err)
		}
		s.report(ctx, "refresh", "replay_family_revoked", ErrInvalidGrant)
		return TokenResponse{}, ErrInvalidGrant
	}
	if revoked != nil || !now.Before(expires) {
		return TokenResponse{}, ErrInvalidGrant
	}
	if err = s.active(ctx, g); err != nil {
		return TokenResponse{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE oauth.refresh_tokens SET used_at=$2 WHERE token_hash=$1 AND used_at IS NULL", hash(old), now); err != nil {
		return TokenResponse{}, fmt.Errorf("%w: rotate: %v", ErrUnavailable, err)
	}
	result, err := s.issue(ctx, tx, g, expires, now)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("%w: refresh issue: %v", ErrUnavailable, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return TokenResponse{}, fmt.Errorf("%w: refresh commit uncertain: %v", ErrUnavailable, err)
	}
	return result, nil
}

// VerifyAccess returns only a verified product subject/client/resource binding.
// Bridge must also run its own Admit/Gate and the ordinary product Service.
func (s *Server) VerifyAccess(ctx context.Context, resource, access string) (Grant, time.Time, error) {
	if resource != s.config.Resource || !validToken(access) {
		return Grant{}, time.Time{}, ErrInvalidGrant
	}
	var g Grant
	var revoked *time.Time
	var expires time.Time
	err := s.config.Pool.QueryRow(ctx, "SELECT g.id,g.subject,g.client_id,g.issuer,g.resource,g.scopes,g.created_at,g.revoked_at,a.expires_at FROM oauth.access_tokens a JOIN oauth.grants g ON g.id=a.grant_id WHERE a.token_hash=$1", hash(access)).
		Scan(&g.ID, &g.Subject, &g.ClientID, &g.Issuer, &g.Resource, &g.Scopes, &g.CreatedAt, &revoked, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, time.Time{}, ErrInvalidGrant
	}
	if err != nil {
		return Grant{}, time.Time{}, fmt.Errorf("%w: verify access: %v", ErrUnavailable, err)
	}
	if revoked != nil || !s.now().Before(expires) {
		return Grant{}, time.Time{}, ErrInvalidGrant
	}
	if err = s.active(ctx, g); err != nil {
		return Grant{}, time.Time{}, err
	}
	return g, expires, nil
}

// Disconnect is a deliberate product-owned, authenticated operation. Products
// must supply the actual user subject from their normal authorization boundary.
// It is independent of interactive-session logout.
func (s *Server) Disconnect(ctx context.Context, subject, grantID string) error {
	if subject == "" || !validToken(grantID) {
		return ErrInvalidGrant
	}
	tag, err := s.config.Pool.Exec(ctx, "UPDATE oauth.grants SET revoked_at=COALESCE(revoked_at,$3) WHERE id=$1 AND subject=$2", grantID, subject, s.now())
	if err != nil {
		return fmt.Errorf("%w: disconnect: %v", ErrUnavailable, err)
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalidGrant
	}
	return nil
}
func (s *Server) revokeHTTP(w http.ResponseWriter, r *http.Request) {
	v, err := requestForm(r)
	if err != nil {
		if !errors.Is(err, ErrInvalidGrant) {
			s.report(r.Context(), "revoke_form_read", "read_failed", err)
		}
		s.oauthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	for key := range v {
		if key != "client_id" && key != "token" && key != "token_type_hint" {
			s.oauthError(w, "invalid_request", http.StatusBadRequest)
			return
		}
	}
	client, clientErr := s.authenticateClient(r, v)
	if clientErr != nil {
		if errors.Is(clientErr, ErrUnavailable) {
			s.problem(w, r, "revoke_client_registry", clientErr)
		} else {
			s.oauthError(w, "invalid_client", http.StatusUnauthorized)
		}
		return
	}
	value, err := single(v, "token", true)
	if err != nil {
		s.oauthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if hint, e := single(v, "token_type_hint", false); e != nil || hint != "" && hint != "access_token" && hint != "refresh_token" {
		s.oauthError(w, "unsupported_token_type", http.StatusBadRequest)
		return
	}
	if validToken(value) {
		query := "UPDATE oauth.grants SET revoked_at=COALESCE(revoked_at,$3) WHERE client_id=$1 AND (id IN (SELECT grant_id FROM oauth.access_tokens WHERE token_hash=$2) OR id IN (SELECT grant_id FROM oauth.refresh_tokens WHERE token_hash=$2))"
		if _, err = s.config.Pool.Exec(r.Context(), query, client.ID, hash(value), s.now()); err != nil {
			s.problem(w, r, "revoke", err)
			return
		}
	}
	w.WriteHeader(http.StatusOK) // RFC 7009: do not disclose token existence.
}

// sdkValidate uses the maintained in-process Go library for auth-code,
// registered redirect, confidential client authentication, PKCE S256,
// refresh client and scope validation. Resource binding and atomic grant
// state remain the explicit AChrix protocol profile/storage obligations.
func (s *Server) sdkValidate(r *http.Request, values url.Values, isRefresh bool) error {
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(strings.NewReader(values.Encode()))
	req.Form = nil
	req.PostForm = nil
	if isRefresh {
		parsed, err := op.ParseRefreshTokenRequest(req, s.sdk.Decoder())
		if err != nil {
			return ErrInvalidGrant
		}
		_, _, err = op.ValidateRefreshTokenRequest(req.Context(), parsed, s.sdk)
		if err != nil {
			if errors.Is(err, ErrUnavailable) {
				return err
			}
			return ErrInvalidGrant
		}
		return nil
	}
	parsed, err := op.ParseAccessTokenRequest(req, s.sdk.Decoder())
	if err != nil {
		return ErrInvalidGrant
	}
	_, _, err = op.ValidateAccessTokenRequest(req.Context(), parsed, s.sdk)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return err
		}
		return ErrInvalidGrant
	}
	return nil
}
