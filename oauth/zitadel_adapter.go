// SPDX-License-Identifier: MPL-2.0
// Package-private ZITADEL protocol boundary. The product-facing contracts in
// this module deliberately do not expose OIDC provider, client or storage types.
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"
	oidchttp "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"github.com/zitadel/schema"
)

// sdkClient exposes only the exact code/S256, Basic-or-none and approved scopes
// to ZITADEL. Client lookup always consults product-authoritative live state.
type sdkClient struct {
	Client
}

func (c sdkClient) GetID() string                    { return c.ID }
func (c sdkClient) RedirectURIs() []string           { return slices.Clone(c.Client.RedirectURIs) }
func (c sdkClient) PostLogoutRedirectURIs() []string { return nil }
func (c sdkClient) ApplicationType() op.ApplicationType {
	if c.Confidential {
		return op.ApplicationTypeWeb
	}
	return op.ApplicationTypeUserAgent
}
func (c sdkClient) AuthMethod() oidc.AuthMethod {
	if c.Confidential {
		return oidc.AuthMethodBasic
	}
	return oidc.AuthMethodNone
}
func (c sdkClient) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}
func (c sdkClient) GrantTypes() []oidc.GrantType {
	return []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken}
}
func (c sdkClient) LoginURL(string) string              { return "" } // The issuer owns the session-bound consent UI.
func (c sdkClient) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeBearer }
func (c sdkClient) IDTokenLifetime() time.Duration      { return 0 } // OIDC is unsupported by this profile.
func (c sdkClient) DevMode() bool                       { return false }
func (c sdkClient) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return slices.Clone[[]string]
}
func (c sdkClient) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return slices.Clone[[]string]
}
func (c sdkClient) IsScopeAllowed(scope string) bool     { return slices.Contains(c.Scopes, scope) }
func (c sdkClient) IDTokenUserinfoClaimsAssertion() bool { return false }
func (c sdkClient) ClockSkew() time.Duration             { return 0 }

// sdkCode implements the provider's code-request contract backed by AChrix's
// durable grant/code rows; no vendor schema or model crosses the public API.
type sdkCode struct {
	g                   Grant
	redirect, challenge string
}

func (c sdkCode) GetID() string          { return c.g.ID }
func (c sdkCode) GetACR() string         { return "" }
func (c sdkCode) GetAMR() []string       { return nil }
func (c sdkCode) GetAudience() []string  { return []string{c.g.Resource} }
func (c sdkCode) GetAuthTime() time.Time { return c.g.CreatedAt }
func (c sdkCode) GetClientID() string    { return c.g.ClientID }
func (c sdkCode) GetCodeChallenge() *oidc.CodeChallenge {
	return &oidc.CodeChallenge{Challenge: c.challenge, Method: oidc.CodeChallengeMethodS256}
}
func (c sdkCode) GetNonce() string                   { return "" }
func (c sdkCode) GetRedirectURI() string             { return c.redirect }
func (c sdkCode) GetResponseType() oidc.ResponseType { return oidc.ResponseTypeCode }
func (c sdkCode) GetResponseMode() oidc.ResponseMode { return oidc.ResponseModeQuery }
func (c sdkCode) GetScopes() []string                { return slices.Clone(c.g.Scopes) }
func (c sdkCode) GetState() string                   { return "" }
func (c sdkCode) GetSubject() string                 { return c.g.Subject }
func (c sdkCode) Done() bool                         { return true }

type sdkRefresh struct {
	g      Grant
	scopes []string
}

func (r *sdkRefresh) GetAMR() []string                 { return nil }
func (r *sdkRefresh) GetAudience() []string            { return []string{r.g.Resource} }
func (r *sdkRefresh) GetAuthTime() time.Time           { return r.g.CreatedAt }
func (r *sdkRefresh) GetClientID() string              { return r.g.ClientID }
func (r *sdkRefresh) GetScopes() []string              { return slices.Clone(r.scopes) }
func (r *sdkRefresh) GetSubject() string               { return r.g.Subject }
func (r *sdkRefresh) SetCurrentScopes(scopes []string) { r.scopes = slices.Clone(scopes) }

type sdkStorage struct{ s *Server }

var _ op.Storage = (*sdkStorage)(nil)

func (d *sdkStorage) GetClientByClientID(ctx context.Context, id string) (op.Client, error) {
	c, err := d.s.client(ctx, id)
	if err != nil {
		return nil, err
	}
	return sdkClient{Client: c}, nil
}
func (d *sdkStorage) AuthorizeClientIDSecret(ctx context.Context, id, secret string) error {
	c, err := d.s.client(ctx, id)
	if err != nil {
		return err
	}
	if !c.Confidential || len(secret) < 32 {
		return ErrInvalidGrant
	}
	actual := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(c.SecretDigest[:], actual[:]) != 1 {
		return ErrInvalidGrant
	}
	return nil
}
func (d *sdkStorage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	if !validToken(code) {
		return nil, ErrInvalidGrant
	}
	var g Grant
	var redirect, challenge string
	var revoked *time.Time
	var expires time.Time
	err := d.s.config.Pool.QueryRow(ctx, `SELECT g.id,g.subject,g.client_id,g.issuer,g.resource,g.scopes,g.created_at,g.revoked_at,c.expires_at,c.challenge,c.redirect_uri FROM oauth.codes c JOIN oauth.grants g ON g.id=c.grant_id WHERE c.code_hash=$1`, hash(code)).
		Scan(&g.ID, &g.Subject, &g.ClientID, &g.Issuer, &g.Resource, &g.Scopes, &g.CreatedAt, &revoked, &expires, &challenge, &redirect)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, fmt.Errorf("%w: SDK code lookup: %v", ErrUnavailable, err)
	}
	if revoked != nil || !d.s.now().Before(expires) || g.Issuer != d.s.config.Issuer || g.Resource != d.s.config.Resource {
		return nil, ErrInvalidGrant
	}
	return sdkCode{g: g, redirect: redirect, challenge: challenge}, nil
}
func (d *sdkStorage) TokenRequestByRefreshToken(ctx context.Context, value string) (op.RefreshTokenRequest, error) {
	if !validToken(value) {
		return nil, ErrInvalidGrant
	}
	var g Grant
	var revoked *time.Time
	var expires time.Time
	err := d.s.config.Pool.QueryRow(ctx, `SELECT g.id,g.subject,g.client_id,g.issuer,g.resource,g.scopes,g.created_at,g.revoked_at,r.expires_at FROM oauth.refresh_tokens r JOIN oauth.grants g ON g.id=r.grant_id WHERE r.token_hash=$1`, hash(value)).
		Scan(&g.ID, &g.Subject, &g.ClientID, &g.Issuer, &g.Resource, &g.Scopes, &g.CreatedAt, &revoked, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidGrant
	}
	if err != nil {
		return nil, fmt.Errorf("%w: SDK refresh lookup: %v", ErrUnavailable, err)
	}
	// Used refresh tokens intentionally reach the atomic replay handler.
	if revoked != nil || !d.s.now().Before(expires) || g.Issuer != d.s.config.Issuer || g.Resource != d.s.config.Resource {
		return nil, ErrInvalidGrant
	}
	return &sdkRefresh{g: g, scopes: slices.Clone(g.Scopes)}, nil
}

// Deliberately unreachable OIDC/provider paths fail closed. The application
// only invokes the verified code/refresh request validators in this package.
func (*sdkStorage) CreateAuthRequest(context.Context, *oidc.AuthRequest, string) (op.AuthRequest, error) {
	return nil, ErrConfiguration
}
func (*sdkStorage) AuthRequestByID(context.Context, string) (op.AuthRequest, error) {
	return nil, ErrConfiguration
}
func (*sdkStorage) SaveAuthCode(context.Context, string, string) error { return ErrConfiguration }
func (*sdkStorage) DeleteAuthRequest(context.Context, string) error    { return ErrConfiguration }
func (*sdkStorage) CreateAccessToken(context.Context, op.TokenRequest) (string, time.Time, error) {
	return "", time.Time{}, ErrConfiguration
}
func (*sdkStorage) CreateAccessAndRefreshTokens(context.Context, op.TokenRequest, string) (string, string, time.Time, error) {
	return "", "", time.Time{}, ErrConfiguration
}
func (*sdkStorage) TerminateSession(context.Context, string, string) error { return ErrConfiguration }
func (*sdkStorage) RevokeToken(context.Context, string, string, string) *oidc.Error {
	return oidc.ErrUnsupportedGrantType()
}
func (*sdkStorage) GetRefreshTokenInfo(context.Context, string, string) (string, string, error) {
	return "", "", ErrConfiguration
}
func (*sdkStorage) SigningKey(context.Context) (op.SigningKey, error) { return nil, ErrConfiguration }
func (*sdkStorage) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return nil, ErrConfiguration
}
func (*sdkStorage) KeySet(context.Context) ([]op.Key, error) { return nil, ErrConfiguration }
func (*sdkStorage) SetUserinfoFromScopes(context.Context, *oidc.UserInfo, string, string, []string) error {
	return ErrConfiguration
}
func (*sdkStorage) SetUserinfoFromToken(context.Context, *oidc.UserInfo, string, string, string) error {
	return ErrConfiguration
}
func (*sdkStorage) SetIntrospectionFromToken(context.Context, *oidc.IntrospectionResponse, string, string, string) error {
	return ErrConfiguration
}
func (*sdkStorage) GetPrivateClaimsFromScopes(context.Context, string, string, []string) (map[string]any, error) {
	return nil, ErrConfiguration
}
func (*sdkStorage) GetKeyByIDAndClientID(context.Context, string, string) (*jose.JSONWebKey, error) {
	return nil, ErrConfiguration
}
func (*sdkStorage) ValidateJWTProfileScopes(context.Context, string, []string) ([]string, error) {
	return nil, ErrConfiguration
}
func (d *sdkStorage) Health(ctx context.Context) error { return d.s.config.Pool.Ping(ctx) }

type sdkEngine struct {
	store   *sdkStorage
	decoder *schema.Decoder
}

func newSDKEngine(s *Server) *sdkEngine {
	decoder := schema.NewDecoder()
	decoder.IgnoreUnknownKeys(true) // Ingress first rejects unsupported wire parameters.
	return &sdkEngine{store: &sdkStorage{s: s}, decoder: decoder}
}

var _ op.Exchanger = (*sdkEngine)(nil)

func (e *sdkEngine) Storage() op.Storage                                       { return e.store }
func (e *sdkEngine) Decoder() oidchttp.Decoder                                 { return e.decoder }
func (*sdkEngine) Crypto() op.Crypto                                           { return nil }
func (*sdkEngine) AuthMethodPostSupported() bool                               { return false }
func (*sdkEngine) AuthMethodPrivateKeyJWTSupported() bool                      { return false }
func (*sdkEngine) GrantTypeRefreshTokenSupported() bool                        { return true }
func (*sdkEngine) GrantTypeTokenExchangeSupported() bool                       { return false }
func (*sdkEngine) GrantTypeJWTAuthorizationSupported() bool                    { return false }
func (*sdkEngine) GrantTypeClientCredentialsSupported() bool                   { return false }
func (*sdkEngine) GrantTypeDeviceCodeSupported() bool                          { return false }
func (*sdkEngine) AccessTokenVerifier(context.Context) *op.AccessTokenVerifier { return nil }
func (*sdkEngine) IDTokenHintVerifier(context.Context) *op.IDTokenHintVerifier { return nil }
func (*sdkEngine) Logger() *slog.Logger                                        { return nil }
