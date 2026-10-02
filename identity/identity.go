// SPDX-License-Identifier: MPL-2.0
// Package identity owns product-local accounts, password credentials and opaque
// server-side sessions. An authenticated principal conveys identity, never rights.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
)

var (
	ErrAuthentication = errors.New("authentication failed")
	ErrInvalid        = errors.New("invalid identity input")
	ErrConflict       = errors.New("identity precondition conflict")
	ErrUnavailable    = errors.New("identity unavailable")
	ErrLimited        = errors.New("identity admission limited")
	ErrConfiguration  = errors.New("invalid identity configuration")
)

const (
	AccountCreate     = "achrix.identity.account.create"
	AccountRead       = "achrix.identity.account.read"
	CredentialSet     = "achrix.identity.credential.set"
	AccountSetEnabled = "achrix.identity.account.set-enabled"
	SessionRevokeAll  = "achrix.identity.session.revoke-all"
	Authentication    = "achrix.identity.authenticate"
	PasswordChange    = "achrix.identity.credential.change"
)

// PublicPrincipal is only the principal for attempting credential/session
// authentication. A product must explicitly permit Authentication for this
// principal; it conveys no account identity or product permission.
const PublicPrincipal achrix.Principal = "achrix.identity.public"
const ModuleVersion = "0.2.0-development"

// Config is instance-owned. No environment/global configuration is read by Identity.
// Now controls security time decisions and must be trusted, monotonic in use and
// concurrency-safe. Zero fields use explicit bounded defaults.
type Config struct {
	Password        PasswordPolicy
	HashConcurrency int
	SessionLifetime time.Duration
	Now             func() time.Time
}

func (c Config) defaults() (Config, error) {
	if c.Password == (PasswordPolicy{}) {
		c.Password = DefaultPasswordPolicy()
	}
	if c.HashConcurrency == 0 {
		c.HashConcurrency = 2
	}
	if c.SessionLifetime == 0 {
		c.SessionLifetime = 8 * time.Hour
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if !c.Password.valid() || c.HashConcurrency < 1 || c.HashConcurrency > 2 || c.SessionLifetime < time.Minute || c.SessionLifetime > 24*time.Hour {
		return c, ErrConfiguration
	}
	return c, nil
}

// Account exposes only the immutable opaque ID, login identifier and status.
// Login is a product-local authentication handle, never an email/profile contract.
type Account struct {
	ID, Login string
	Enabled   bool
	Revision  int64
}
type credential struct {
	Account
	hash string
}

// Session secrets are returned only to the trusted adapter on issue/rotation.
// Default formatting deliberately redacts secret fields.
type Session struct {
	Principal achrix.Principal
	Token     string `json:"-"`
	CSRF      string `json:"-"`
	ExpiresAt time.Time
}

func (s Session) String() string       { return "identity.Session[redacted]" }
func (s Session) GoString() string     { return s.String() }
func (s Session) LogValue() slog.Value { return slog.StringValue(s.String()) }

type sessionRecord struct {
	accountID           string
	tokenHash, csrfHash []byte
	expires             time.Time
	revision            int64
}

var loginSyntax = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var accountSyntax = regexp.MustCompile(`^[A-Z2-7]{26}$`)

func validID(id string) bool       { return len(id) == 26 && accountSyntax.MatchString(id) }
func validLogin(login string) bool { return len(login) <= 64 && loginSyntax.MatchString(login) }
func token() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func tokenHash(value string) ([]byte, error) {
	if len(value) != 43 {
		return nil, ErrAuthentication
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, ErrAuthentication
	}
	sum := sha256.Sum256(decoded)
	return sum[:], nil
}
func newSession(id string, revision int64, expires time.Time) (Session, sessionRecord) {
	s := Session{Principal: achrix.Principal(id), Token: token(), CSRF: token(), ExpiresAt: expires.UTC().Truncate(time.Microsecond)}
	th, _ := tokenHash(s.Token)
	ch, _ := tokenHash(s.CSRF)
	return s, sessionRecord{id, th, ch, s.ExpiresAt, revision}
}
func csrfMatches(expected []byte, value string) bool {
	got, err := tokenHash(value)
	return err == nil && subtle.ConstantTimeCompare(expected, got) == 1
}

// Service routes authorized account management through the normal Application.
// Public login/session authentication is credential-based; it never grants any
// product capability. Products use Principal with their own authorized Service.
type Service struct {
	app    *achrix.Application
	module *Module
	audit  *audit.Service
}

func NewService(app *achrix.Application, module *Module, accountability *audit.Service) (*Service, error) {
	if app == nil || module == nil || accountability == nil {
		return nil, ErrConfiguration
	}
	if err := accountability.CheckDatabase(module.dbConfig.ConnConfig.ConnString()); err != nil {
		return nil, ErrConfiguration
	}
	return &Service{app: app, module: module, audit: accountability}, nil
}
func (s *Service) authorize(ctx context.Context, p achrix.Principal, capability, id string) error {
	if id != "" && !validID(id) {
		return ErrInvalid
	}
	return s.app.Authorize(ctx, p, capability, id)
}
func (s *Service) admission(ctx context.Context) error {
	return s.app.Authorize(ctx, PublicPrincipal, Authentication, "")
}
func (s *Service) prepare(ctx context.Context, actor achrix.Principal, action, capability, id string) (audit.Prepared, error) {
	return s.audit.Prepare(ctx, actor, audit.Event{Action: action, Target: id, Authority: capability, Outcome: "succeeded"})
}

// Account is an authorized status/revision read for subsequent conditional
// management operations. It never returns credential or session material.
func (s *Service) Account(parent context.Context, actor achrix.Principal, id string) (Account, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if !validID(id) {
		return Account{}, ErrInvalid
	}
	if err := s.authorize(ctx, actor, AccountRead, id); err != nil {
		return Account{}, err
	}
	return s.module.account(ctx, id)
}
func (s *Service) CreateAccount(parent context.Context, actor achrix.Principal, login, password string) (Account, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if !validLogin(login) {
		return Account{}, ErrInvalid
	}
	if err := s.authorize(ctx, actor, AccountCreate, ""); err != nil {
		return Account{}, err
	}
	ctx, _, finish, err := s.module.acquire(ctx)
	if err != nil {
		return Account{}, err
	}
	defer finish()
	a := Account{ID: rand.Text(), Login: login, Enabled: true, Revision: 1}
	prepared, err := s.prepare(ctx, actor, "identity.account.create", AccountCreate, a.ID)
	if err != nil {
		return Account{}, err
	}
	hash, err := s.module.passwords.hash(ctx, password)
	if err != nil {
		return Account{}, err
	}
	err = s.module.create(ctx, a, hash, s.module.now(), prepared)
	if err != nil {
		return Account{}, err
	}
	return a, nil
}
func (s *Service) SetPassword(parent context.Context, actor achrix.Principal, id string, expectedRevision int64, password string) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if expectedRevision < 1 || !validID(id) {
		return ErrInvalid
	}
	if err := s.authorize(ctx, actor, CredentialSet, id); err != nil {
		return err
	}
	ctx, _, finish, err := s.module.acquire(ctx)
	if err != nil {
		return err
	}
	defer finish()
	prepared, err := s.prepare(ctx, actor, "identity.credential.set", CredentialSet, id)
	if err != nil {
		return err
	}
	hash, err := s.module.passwords.hash(ctx, password)
	if err != nil {
		return err
	}
	return s.module.setPassword(ctx, id, expectedRevision, hash, s.module.now(), prepared)
}
func (s *Service) SetEnabled(parent context.Context, actor achrix.Principal, id string, expectedRevision int64, enabled bool) error {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if expectedRevision < 1 || !validID(id) {
		return ErrInvalid
	}
	if err := s.authorize(ctx, actor, AccountSetEnabled, id); err != nil {
		return err
	}
	prepared, err := s.prepare(ctx, actor, "identity.account.set-enabled", AccountSetEnabled, id)
	if err != nil {
		return err
	}
	return s.module.setEnabled(ctx, id, expectedRevision, enabled, s.module.now(), prepared)
}
func (s *Service) RevokeAll(parent context.Context, actor achrix.Principal, id string) error {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if !validID(id) {
		return ErrInvalid
	}
	if err := s.authorize(ctx, actor, SessionRevokeAll, id); err != nil {
		return err
	}
	prepared, err := s.prepare(ctx, actor, "identity.session.revoke-all", SessionRevokeAll, id)
	if err != nil {
		return err
	}
	return s.module.revokeAll(ctx, id, s.module.now(), prepared)
}

// Login never adopts a client token. A supplied previous cookie is revoked in the
// same transaction as successful fresh issuance, including cross-account login.
// Unknown/disabled accounts undergo the bounded dummy hash; failures stay generic.
func (s *Service) Login(parent context.Context, login, password, previousToken string) (Session, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if !validLogin(login) || !validPassword(password, false) {
		return Session{}, ErrAuthentication
	}
	if err := s.admission(ctx); err != nil {
		return Session{}, err
	}
	ctx, _, finish, err := s.module.acquire(ctx)
	if err != nil {
		return Session{}, err
	}
	defer finish()
	c, err := s.module.findCredential(ctx, "login", login)
	if err != nil && !errors.Is(err, ErrAuthentication) {
		return Session{}, err
	}
	encoded := c.hash
	if errors.Is(err, ErrAuthentication) || !c.Enabled {
		encoded = s.module.passwords.dummy
	}
	ok, rehash, err := s.module.passwords.verify(ctx, password, encoded)
	if err != nil {
		return Session{}, err
	}
	if !ok || !c.Enabled {
		return Session{}, ErrAuthentication
	}
	replacement := ""
	if rehash {
		replacement, err = s.module.passwords.hash(ctx, password)
		if err != nil {
			return Session{}, err
		}
	}
	result, record := newSession(c.ID, c.Revision, s.module.now().Add(s.module.config.SessionLifetime))
	var previous []byte
	if previousToken != "" {
		previous, _ = tokenHash(previousToken)
	}
	if err = s.module.issue(ctx, c, record, replacement, previous, s.module.now()); err != nil {
		return Session{}, err
	}
	return result, nil
}
func (s *Service) Authenticate(parent context.Context, value string) (achrix.Principal, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	hash, err := tokenHash(value)
	if err != nil {
		return "", err
	}
	if err := s.admission(ctx); err != nil {
		return "", err
	}
	record, err := s.module.lookup(ctx, hash, s.module.now())
	if err != nil {
		return "", err
	}
	return achrix.Principal(record.accountID), nil
}
func (s *Service) ValidateCSRF(parent context.Context, value, csrf string) (achrix.Principal, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	hash, err := tokenHash(value)
	if err != nil {
		return "", err
	}
	if err := s.admission(ctx); err != nil {
		return "", err
	}
	record, err := s.module.lookup(ctx, hash, s.module.now())
	if err != nil {
		return "", err
	}
	if !csrfMatches(record.csrfHash, csrf) {
		return "", ErrAuthentication
	}
	return achrix.Principal(record.accountID), nil
}

// RefreshCSRF serves a freshly generated synchronizer token through an
// authenticated same-origin response. Only its hash is persisted; adapters must
// prevent cross-origin reads and must not put it in URL/cookie/Web Storage.
func (s *Service) RefreshCSRF(parent context.Context, value string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	hash, err := tokenHash(value)
	if err != nil {
		return "", err
	}
	if err := s.admission(ctx); err != nil {
		return "", err
	}
	csrf := token()
	ch, _ := tokenHash(csrf)
	if err = s.module.refreshCSRF(ctx, hash, ch, s.module.now()); err != nil {
		return "", err
	}
	return csrf, nil
}
func (s *Service) Rotate(parent context.Context, value string) (Session, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	hash, err := tokenHash(value)
	if err != nil {
		return Session{}, err
	}
	if err := s.admission(ctx); err != nil {
		return Session{}, err
	}
	record, err := s.module.lookup(ctx, hash, s.module.now())
	if err != nil {
		return Session{}, err
	}
	// Rotation keeps the original absolute expiry rather than extending forever.
	result, next := newSession(record.accountID, record.revision, record.expires)
	if err = s.module.rotate(ctx, hash, next, s.module.now()); err != nil {
		return Session{}, err
	}
	return result, nil
}
func (s *Service) Logout(parent context.Context, value string) error {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	hash, err := tokenHash(value)
	if err != nil {
		return err
	}
	if err := s.admission(ctx); err != nil {
		return err
	}
	return s.module.revoke(ctx, hash)
}

// ChangePassword reauthenticates the currently enabled session account, atomically
// replaces the expected credential and revokes all sessions. A new login is needed.
func (s *Service) ChangePassword(parent context.Context, value, current, next string) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	p, err := s.Authenticate(ctx, value)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, p, PasswordChange, string(p)); err != nil {
		return err
	}
	ctx, _, finish, err := s.module.acquire(ctx)
	if err != nil {
		return err
	}
	defer finish()
	prepared, err := s.prepare(ctx, p, "identity.credential.change", PasswordChange, string(p))
	if err != nil {
		return err
	}
	c, err := s.module.findCredential(ctx, "id", string(p))
	if err != nil {
		return err
	}
	ok, _, err := s.module.passwords.verify(ctx, current, c.hash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAuthentication
	}
	hash, err := s.module.passwords.hash(ctx, next)
	if err != nil {
		return err
	}
	return s.module.changePassword(ctx, value, p, c.Revision, hash, s.module.now(), prepared)
}
