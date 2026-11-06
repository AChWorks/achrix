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
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
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
	decoder.IgnoreUnknownKeys(true) // OAuth form ingress validates its recognized token parameters separately.
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

var errSDKClientMethod = errors.New("oauth registered client authentication method mismatch")

// sdkRequest supplies the already size-bounded, duplicate-free HTTP form to
// the upstream protocol parser. AChrix's RFC 8707 resource and request-shape
// guards run before these calls; no opaque client body is parsed twice.
func sdkRequest(r *http.Request, fields url.Values) *http.Request {
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(strings.NewReader(fields.Encode()))
	req.Form = nil
	req.PostForm = nil
	return req
}

// The upstream validator handles credential checking but permits client_secret
// form authentication and (for public clients) ignores stray Basic credentials.
// Our first profile only advertises client_secret_basic or none. Enforce that
// method boundary here without duplicating secret or PKCE verification.
func strictSDKClientMethod(r *http.Request, fields url.Values, client Client) error {
	auth := r.Header.Values("Authorization")
	if len(auth) > 1 || fields.Has("client_secret") {
		return errSDKClientMethod
	}
	hasBasic := false
	if len(auth) == 1 {
		_, _, hasBasic = r.BasicAuth()
		if !hasBasic {
			return errSDKClientMethod
		}
		id, err := single(fields, "client_id", false)
		if err != nil || id != "" && id != client.ID {
			return errSDKClientMethod
		}
	}
	if hasBasic != client.Confidential {
		return errSDKClientMethod
	}
	return nil
}

func isSDKClientAuthError(err error) bool {
	return errors.Is(err, errSDKClientMethod) || errors.Is(err, oidc.ErrInvalidClient())
}

// sdkValidateCode is the sole registered-client, redirect and S256 exchange
// authority. Its immutable code model is later compared to a locked row to
// defend against concurrent revocation/replacement before token issuance.
func (s *Server) sdkValidateCode(r *http.Request, fields url.Values) (Client, sdkCode, error) {
	req := sdkRequest(r, fields)
	parsed, err := op.ParseAccessTokenRequest(req, s.sdk.Decoder())
	if err != nil {
		return Client{}, sdkCode{}, err
	}
	checked, registered, err := op.ValidateAccessTokenRequest(req.Context(), parsed, s.sdk)
	if err != nil {
		return Client{}, sdkCode{}, err
	}
	client, ok := registered.(sdkClient)
	if !ok {
		return Client{}, sdkCode{}, fmt.Errorf("%w: unexpected upstream client contract", ErrUnavailable)
	}
	if err = strictSDKClientMethod(r, fields, client.Client); err != nil {
		return Client{}, sdkCode{}, err
	}
	code, ok := checked.(sdkCode)
	if !ok {
		return Client{}, sdkCode{}, fmt.Errorf("%w: unexpected upstream code contract", ErrUnavailable)
	}
	return client.Client, code, nil
}

func (s *Server) sdkValidateRefresh(r *http.Request, fields url.Values) (Client, *sdkRefresh, error) {
	req := sdkRequest(r, fields)
	parsed, err := op.ParseRefreshTokenRequest(req, s.sdk.Decoder())
	if err != nil {
		return Client{}, nil, err
	}
	checked, registered, err := op.ValidateRefreshTokenRequest(req.Context(), parsed, s.sdk)
	if err != nil {
		// Unlike its code validator, ZITADEL's refresh validator returns
		// client lookup failures directly. Distinguish an inactive current
		// client (invalid_client) from a bad/replayed refresh credential
		// (invalid_grant), without implementing a second auth algorithm.
		if errors.Is(err, ErrInvalidGrant) {
			if _, lookupErr := s.client(req.Context(), parsed.ClientID); lookupErr != nil {
				if errors.Is(lookupErr, ErrUnavailable) {
					return Client{}, nil, lookupErr
				}
				return Client{}, nil, errSDKClientMethod
			}
		}
		return Client{}, nil, err
	}
	client, ok := registered.(sdkClient)
	if !ok {
		return Client{}, nil, fmt.Errorf("%w: unexpected upstream client contract", ErrUnavailable)
	}
	if err = strictSDKClientMethod(r, fields, client.Client); err != nil {
		return Client{}, nil, err
	}
	refresh, ok := checked.(*sdkRefresh)
	if !ok {
		return Client{}, nil, fmt.Errorf("%w: unexpected upstream refresh contract", ErrUnavailable)
	}
	return client.Client, refresh, nil
}

// RFC 7009 client authentication is handled by ZITADEL's revocation parser.
// Opaque-token revocation itself is owned by the atomic PostgreSQL grant store.
func (s *Server) sdkRevocationClient(r *http.Request, fields url.Values) (Client, error) {
	req := sdkRequest(r, fields)
	_, _, clientID, err := op.ParseTokenRevocationRequest(req, s.sdk)
	if err != nil {
		return Client{}, err
	}
	client, err := s.client(r.Context(), clientID)
	if err != nil {
		return Client{}, err
	}
	if err := strictSDKClientMethod(r, fields, client); err != nil {
		return Client{}, err
	}
	return client, nil
}
