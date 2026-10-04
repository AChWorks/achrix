// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/jackc/pgx/v5"
)

const testPassword = "correct horse battery staple"
const replacementPassword = "another long password for testing"
const testAdmin achrix.Principal = "identity-test-administrator"

type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type identityFixture struct {
	app      *achrix.Application
	service  *Service
	module   *Module
	audit    *audit.Service
	db       *pgx.Conn
	clock    atomic.Int64
	denySelf atomic.Bool
	logs     lockedLog
}

// This environment names a disposable task-owned PostgreSQL database. Requiring
// its fixed prefix makes a mistyped production DSN fail before schema mutation.
func newIdentityFixture(t *testing.T) *identityFixture {
	t.Helper()
	return newIdentityFixturePolicy(t, DefaultPasswordPolicy())
}
func newIdentityFixturePolicy(t *testing.T, passwordPolicy PasswordPolicy) *identityFixture {
	t.Helper()
	return newIdentityFixtureConfig(t, Config{Password: passwordPolicy}, audit.Config{})
}
func newIdentityFixtureConfig(t *testing.T, config Config, auditConfig audit.Config) *identityFixture {
	t.Helper()
	dsn := os.Getenv("ACHRIX_IDENTITY_TEST_DSN")
	if dsn == "" {
		t.Skip("ACHRIX_IDENTITY_TEST_DSN absent: real PostgreSQL Identity proof not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.Database, "achrix_identity_") {
		t.Fatal("Identity proof requires task-owned achrix_identity_ database")
	}
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot connect task-owned Identity test database")
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	if _, err = db.Exec(ctx, "DROP SCHEMA IF EXISTS identity CASCADE; DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err = audit.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	f := &identityFixture{db: db}
	f.clock.Store(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).UnixNano())
	now := func() time.Time { return time.Unix(0, f.clock.Load()).UTC() }
	logger := slog.New(slog.NewJSONHandler(&f.logs, nil))
	auditConfig.Now = now
	am, err := audit.NewPostgres(dsn, auditConfig, logger)
	if err != nil {
		t.Fatal(err)
	}
	config.Now, config.SessionLifetime = now, time.Minute
	f.module, err = NewPostgres(dsn, config, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, c, r string) error {
		if p == PublicPrincipal && c == Authentication && r == "" {
			return nil
		}
		if p == testAdmin {
			switch c {
			case AccountCreate, AccountRead, CredentialSet, AccountSetEnabled, SessionRevokeAll, audit.Append, audit.Query, audit.Export:
				return nil
			}
		}
		if !f.denySelf.Load() && validID(string(p)) && r == string(p) && (c == PasswordChange || c == audit.Append) {
			return nil
		}
		return achrix.ErrDenied
	})
	f.app, err = achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second, Logger: logger}, policy, f.module, am)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := f.app.Shutdown(c); err != nil {
			t.Error(err)
		}
	})
	f.audit, err = audit.NewService(f.app, am)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewService(f.app, f.module, f.audit)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *identityFixture) create(t *testing.T, login string) Account {
	t.Helper()
	a, err := f.service.CreateAccount(context.Background(), testAdmin, login, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func (f *identityFixture) login(t *testing.T, login, password string) Session {
	t.Helper()
	s, err := f.service.Login(context.Background(), login, password, "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func (f *identityFixture) account(t *testing.T, id string) Account {
	t.Helper()
	a, err := f.service.Account(context.Background(), testAdmin, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func (f *identityFixture) countAudit(t *testing.T, id string) int {
	t.Helper()
	ctx, cancel := deadline()
	defer cancel()
	page, err := f.audit.Query(ctx, testAdmin, id, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "" {
		t.Fatal("test audit unexpectedly exceeds bounded page")
	}
	for _, r := range page.Records {
		if r.Target != id || r.Actor == "" || r.Action == "" || r.Authority == "" || r.Outcome == "" || r.OccurredAt.Location() != time.UTC {
			t.Fatalf("invalid accountable metadata: %+v", r)
		}
	}
	return len(page.Records)
}
func requireAuthentication(t *testing.T, s *Service, value string) {
	t.Helper()
	if _, err := s.Authenticate(context.Background(), value); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected failed authentication, got %v", err)
	}
}
func deadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func TestPostgresAccountCredentialAndSessionLifecycle(t *testing.T) {
	f := newIdentityFixture(t)
	a := f.create(t, "alice")
	if a.Revision != 1 || !a.Enabled || !validID(a.ID) {
		t.Fatal(a)
	}
	if _, err := f.service.CreateAccount(context.Background(), testAdmin, "alice", testPassword); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate login: %v", err)
	}
	if _, err := f.service.CreateAccount(context.Background(), "outsider", "bob", testPassword); !errors.Is(err, achrix.ErrDenied) {
		t.Fatalf("unauthorized provisioning: %v", err)
	}
	var accountCount int
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM identity.accounts").Scan(&accountCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 {
		t.Fatal("duplicate or denied provisioning created an account")
	}
	before := f.countAudit(t, a.ID)
	if before != 1 {
		t.Fatalf("account provision audit count %d", before)
	}
	failures := f.module.FailureCount()
	for _, attempt := range [][2]string{{"missing", testPassword}, {"alice", "incorrect password"}} {
		if _, err := f.service.Login(context.Background(), attempt[0], attempt[1], ""); !errors.Is(err, ErrAuthentication) {
			t.Fatal(err)
		}
	}
	if f.countAudit(t, a.ID) != before || f.module.FailureCount() != failures {
		t.Fatal("ordinary authentication failures created audit/error volume")
	}
	s := f.login(t, "alice", testPassword)
	if len(s.Token) != 43 || len(s.CSRF) != 43 || s.Token == s.CSRF {
		t.Fatal("invalid independent session secrets")
	}
	p, err := f.service.Authenticate(context.Background(), s.Token)
	if err != nil || p != achrix.Principal(a.ID) {
		t.Fatal(p, err)
	}
	ctx, cancel := deadline()
	err = f.app.Authorize(ctx, p, CredentialSet, a.ID)
	cancel()
	if !errors.Is(err, achrix.ErrDenied) {
		t.Fatalf("authentication granted management authority: %v", err)
	}
	if _, err = f.service.Account(context.Background(), p, a.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatalf("authentication granted account read: %v", err)
	}
	if _, err = f.service.ValidateCSRF(context.Background(), s.Token, token()); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("unbound CSRF token accepted: %v", err)
	}
	if _, err = f.service.ValidateCSRF(context.Background(), s.Token, s.CSRF); err != nil {
		t.Fatal(err)
	}
	csrf, err := f.service.RefreshCSRF(context.Background(), s.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ValidateCSRF(context.Background(), s.Token, s.CSRF); !errors.Is(err, ErrAuthentication) {
		t.Fatal("old synchronizer remained valid", err)
	}
	if _, err = f.service.ValidateCSRF(context.Background(), s.Token, csrf); err != nil {
		t.Fatal(err)
	}
	rotated, err := f.service.Rotate(context.Background(), s.Token)
	if err != nil || rotated.Token == s.Token || !rotated.ExpiresAt.Equal(s.ExpiresAt) {
		t.Fatal("rotation/fixation/absolute expiry", err)
	}
	requireAuthentication(t, f.service, s.Token)
	if err = f.service.Logout(context.Background(), rotated.Token); err != nil {
		t.Fatal(err)
	}
	if err = f.service.Logout(context.Background(), rotated.Token); err != nil {
		t.Fatal("logout is not idempotent", err)
	}
	requireAuthentication(t, f.service, rotated.Token)
	s = f.login(t, "alice", testPassword)
	if err = f.service.SetEnabled(context.Background(), testAdmin, a.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := f.account(t, a.ID); got.Enabled || got.Revision != 2 {
		t.Fatal(got)
	}
	requireAuthentication(t, f.service, s.Token)
	if _, err = f.service.Login(context.Background(), "alice", testPassword, ""); !errors.Is(err, ErrAuthentication) {
		t.Fatal("disabled account authenticated", err)
	}
	if err = f.service.SetEnabled(context.Background(), testAdmin, a.ID, 1, true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale status edit accepted", err)
	}
	if err = f.service.SetEnabled(context.Background(), testAdmin, a.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	s = f.login(t, "alice", testPassword)
	if err = f.service.SetPassword(context.Background(), testAdmin, a.ID, 3, replacementPassword); err != nil {
		t.Fatal(err)
	}
	requireAuthentication(t, f.service, s.Token)
	if _, err = f.service.Login(context.Background(), "alice", testPassword, ""); !errors.Is(err, ErrAuthentication) {
		t.Fatal("old password remained valid", err)
	}
	s = f.login(t, "alice", replacementPassword)
	if err = f.service.RevokeAll(context.Background(), testAdmin, a.ID); err != nil {
		t.Fatal(err)
	}
	requireAuthentication(t, f.service, s.Token)
	if got := f.account(t, a.ID); got.Revision != 5 {
		t.Fatal(got)
	}
	s = f.login(t, "alice", replacementPassword)
	if err = f.service.ChangePassword(context.Background(), s.Token, "wrong current password", testPassword); !errors.Is(err, ErrAuthentication) {
		t.Fatal("self-change omitted reauthentication", err)
	}
	f.denySelf.Store(true)
	if err = f.service.ChangePassword(context.Background(), s.Token, replacementPassword, testPassword); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("denied self-change accepted", err)
	}
	f.denySelf.Store(false)
	if err = f.service.ChangePassword(context.Background(), s.Token, replacementPassword, testPassword); err != nil {
		t.Fatal(err)
	}
	requireAuthentication(t, f.service, s.Token)
	if got := f.account(t, a.ID); got.Revision != 6 {
		t.Fatal(got)
	}
	s = f.login(t, "alice", testPassword)
	f.clock.Add(int64(time.Minute))
	requireAuthentication(t, f.service, s.Token)
	if f.countAudit(t, a.ID) != 6 {
		t.Fatal("diagnostic sessions or rejected mutations polluted accountability")
	}
	if strings.Contains(f.logs.String(), testPassword) || strings.Contains(f.logs.String(), replacementPassword) || strings.Contains(f.logs.String(), s.Token) || strings.Contains(f.logs.String(), s.CSRF) {
		t.Fatal("identity secrets appeared in logs")
	}
}

func TestPostgresSessionAtRestRotationAndCrossAccountFixation(t *testing.T) {
	f := newIdentityFixture(t)
	a := f.create(t, "alice")
	b := f.create(t, "bob")
	f.clock.Add(123) // Exercise PostgreSQL microsecond precision at issuance.
	s := f.login(t, "alice", testPassword)
	var tokenStored, csrfStored []byte
	if err := f.db.QueryRow(context.Background(), "SELECT token_hash,csrf_hash FROM identity.sessions WHERE account_id=$1", a.ID).Scan(&tokenStored, &csrfStored); err != nil {
		t.Fatal(err)
	}
	expectedToken, _ := tokenHash(s.Token)
	expectedCSRF, _ := tokenHash(s.CSRF)
	if !bytes.Equal(tokenStored, expectedToken) || !bytes.Equal(csrfStored, expectedCSRF) || bytes.Contains(tokenStored, []byte(s.Token)) || bytes.Contains(csrfStored, []byte(s.CSRF)) {
		t.Fatal("session secrets are not independently protected at rest")
	}
	next, err := f.service.Login(context.Background(), "bob", testPassword, s.Token)
	if err != nil || next.Principal != achrix.Principal(b.ID) || next.Token == s.Token {
		t.Fatal("cross-account fresh login failed", err)
	}
	requireAuthentication(t, f.service, s.Token)
	var wg sync.WaitGroup
	var successes atomic.Int32
	var winner Session
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rotated, e := f.service.Rotate(context.Background(), next.Token)
			if e == nil {
				successes.Add(1)
				mu.Lock()
				winner = rotated
				mu.Unlock()
			} else if !errors.Is(e, ErrAuthentication) {
				t.Errorf("unexpected rotation result: %v", e)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("rotation committed %d winners", successes.Load())
	}
	requireAuthentication(t, f.service, next.Token)
	if _, err := f.service.Authenticate(context.Background(), winner.Token); err != nil {
		t.Fatal(err)
	}
	if !winner.ExpiresAt.Equal(next.ExpiresAt) || f.countAudit(t, a.ID) != 1 || f.countAudit(t, b.ID) != 1 {
		t.Fatal("rotation extended expiry or created routine audit")
	}
}

func TestPostgresStaleCredentialAndSelfChangeFences(t *testing.T) {
	f := newIdentityFixture(t)
	a := f.create(t, "alice")
	ctx, cancel := deadline()
	stale, err := f.module.findCredential(ctx, "id", a.ID)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.SetPassword(context.Background(), testAdmin, a.ID, a.Revision, replacementPassword); err != nil {
		t.Fatal(err)
	}
	_, record := newSession(a.ID, stale.Revision, f.module.now().Add(time.Minute))
	ctx, cancel = deadline()
	err = f.module.issue(ctx, stale, record, "", nil, f.module.now())
	cancel()
	if !errors.Is(err, ErrAuthentication) {
		t.Fatal("stale verified password issued a session", err)
	}
	var wg sync.WaitGroup
	var success, conflict atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := f.service.SetPassword(context.Background(), testAdmin, a.ID, 2, testPassword)
			if e == nil {
				success.Add(1)
			} else if errors.Is(e, ErrConflict) {
				conflict.Add(1)
			} else {
				t.Errorf("credential race: %v", e)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 || conflict.Load() != 1 || f.account(t, a.ID).Revision != 3 || f.countAudit(t, a.ID) != 3 {
		t.Fatal("concurrent expected revision did not fence replacement")
	}
	s := f.login(t, "alice", testPassword)
	ctx, cancel = deadline()
	c, err := f.module.findCredential(ctx, "id", a.ID)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := f.service.Rotate(context.Background(), s.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = deadline()
	prepared, err := f.audit.Prepare(ctx, achrix.Principal(a.ID), audit.Event{Action: PasswordChange, Target: a.ID, Authority: PasswordChange, Outcome: "succeeded"})
	if err == nil {
		err = f.module.changePassword(ctx, s.Token, s.Principal, c.Revision, c.hash, f.module.now(), prepared)
	}
	cancel()
	if !errors.Is(err, ErrAuthentication) {
		t.Fatal("rotated initiating token changed credential", err)
	}
	if _, err = f.service.Authenticate(context.Background(), rotated.Token); err != nil {
		t.Fatal("rejected mutation altered live session", err)
	}
	if f.account(t, a.ID).Revision != 3 || f.countAudit(t, a.ID) != 3 {
		t.Fatal("rejected stale token had persisted effects")
	}
	// Two already-verified self mutations serialize under the same account fence.
	var hash string
	ctx, cancel = deadline()
	hash, err = f.module.passwords.hash(ctx, replacementPassword)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	success.Store(0)
	var authentication atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := deadline()
			defer cancel()
			prepared, e := f.audit.Prepare(ctx, rotated.Principal, audit.Event{Action: PasswordChange, Target: a.ID, Authority: PasswordChange, Outcome: "succeeded"})
			if e == nil {
				e = f.module.changePassword(ctx, rotated.Token, rotated.Principal, 3, hash, f.module.now(), prepared)
			}
			if e == nil {
				success.Add(1)
			} else if errors.Is(e, ErrAuthentication) {
				authentication.Add(1)
			} else {
				t.Errorf("self-change race: %v", e)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 || authentication.Load() != 1 || f.account(t, a.ID).Revision != 4 || f.countAudit(t, a.ID) != 4 {
		t.Fatal("self-change race did not fence session and credential")
	}
	requireAuthentication(t, f.service, rotated.Token)
}

func TestPostgresAuditFailureRollsBackIdentityMutation(t *testing.T) {
	f := newIdentityFixture(t)
	a := f.create(t, "alice")
	s := f.login(t, "alice", testPassword)
	// The provider fails at the actual Audit write, after Identity has changed its
	// transaction-local rows. The error deliberately contains a secret-like marker.
	if _, err := f.db.Exec(context.Background(), "CREATE FUNCTION audit.identity_test_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'database secret IDENTITY_TEST_SECRET'; END $$; CREATE TRIGGER identity_test_reject BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION audit.identity_test_reject()"); err != nil {
		t.Fatal(err)
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"provision", func() error {
			_, err := f.service.CreateAccount(context.Background(), testAdmin, "bob", testPassword)
			return err
		}},
		{"replace", func() error {
			return f.service.SetPassword(context.Background(), testAdmin, a.ID, 1, replacementPassword)
		}},
		{"disable", func() error { return f.service.SetEnabled(context.Background(), testAdmin, a.ID, 1, false) }},
		{"revoke", func() error { return f.service.RevokeAll(context.Background(), testAdmin, a.ID) }},
		{"self-change", func() error {
			return f.service.ChangePassword(context.Background(), s.Token, testPassword, replacementPassword)
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if !errors.Is(err, ErrUnavailable) || strings.Contains(fmt.Sprint(err), "IDENTITY_TEST_SECRET") {
				t.Fatalf("unsafe provider failure: %v", err)
			}
			if got := f.account(t, a.ID); got.Revision != 1 || !got.Enabled {
				t.Fatal("audit failure committed domain mutation", got)
			}
			if p, e := f.service.Authenticate(context.Background(), s.Token); e != nil || p != achrix.Principal(a.ID) {
				t.Fatal("audit failure revoked session", e)
			}
			if f.countAudit(t, a.ID) != 1 {
				t.Fatal("failed mutation wrote audit")
			}
		})
	}
	var accounts, credentials int
	if err := f.db.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM identity.accounts),(SELECT count(*) FROM identity.credentials)").Scan(&accounts, &credentials); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || credentials != 1 {
		t.Fatal("failed provision committed partial account")
	}
	if strings.Contains(f.logs.String(), "IDENTITY_TEST_SECRET") || strings.Contains(f.logs.String(), testPassword) || strings.Contains(f.logs.String(), s.Token) {
		t.Fatal("provider failure exposed sensitive details")
	}
	if _, err := f.db.Exec(context.Background(), "DROP TRIGGER identity_test_reject ON audit.records; DROP FUNCTION audit.identity_test_reject()"); err != nil {
		t.Fatal(err)
	}
	f.login(t, "alice", testPassword)
	if err := f.service.SetPassword(context.Background(), testAdmin, a.ID, 1, replacementPassword); err != nil {
		t.Fatal("reconciled mutation did not recover", err)
	}
	if f.countAudit(t, a.ID) != 2 {
		t.Fatal("recovered mutation omitted audit")
	}
}

func TestPostgresRevisionOverflowAndMigrationIdentity(t *testing.T) {
	f := newIdentityFixture(t)
	a := f.create(t, "alice")
	if _, err := f.db.Exec(context.Background(), "UPDATE identity.accounts SET revision=$2 WHERE id=$1", a.ID, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() error{
		func() error {
			return f.service.SetPassword(context.Background(), testAdmin, a.ID, math.MaxInt64, replacementPassword)
		},
		func() error { return f.service.SetEnabled(context.Background(), testAdmin, a.ID, math.MaxInt64, false) },
		func() error { return f.service.RevokeAll(context.Background(), testAdmin, a.ID) },
	} {
		if err := run(); !errors.Is(err, ErrConflict) {
			t.Fatal("revision overflow accepted", err)
		}
	}
	if f.account(t, a.ID).Revision != math.MaxInt64 || f.countAudit(t, a.ID) != 1 {
		t.Fatal("overflow mutated durable state")
	}
	ctx, cancel := deadline()
	defer cancel()
	if err := Migrate(ctx, os.Getenv("ACHRIX_IDENTITY_TEST_DSN")); err != nil {
		t.Fatal("repeat immutable installation", err)
	}
	if f.account(t, a.ID).Revision != math.MaxInt64 {
		t.Fatal("repeat migration altered account")
	}
	if _, err := f.db.Exec(ctx, "UPDATE identity.schema_migrations SET checksum=repeat('0',64)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, os.Getenv("ACHRIX_IDENTITY_TEST_DSN")); !errors.Is(err, ErrUnavailable) {
		t.Fatal("changed migration identity admitted", err)
	}
}

func TestPostgresSuccessfulLoginPersistsDeliberateRehash(t *testing.T) {
	f := newIdentityFixturePolicy(t, PasswordPolicy{19 * 1024, 3, 1})
	a := f.create(t, "alice")
	// A retained older PHC record represents an account persisted before the
	// deliberate deployment policy increase. No credential migration rewrites it.
	previous, err := newPasswords(DefaultPasswordPolicy(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := deadline()
	oldHash, err := previous.hash(ctx, testPassword)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(context.Background(), "UPDATE identity.credentials SET password_hash=$2 WHERE account_id=$1", a.ID, oldHash); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Login(context.Background(), "alice", "incorrect password", ""); !errors.Is(err, ErrAuthentication) {
		t.Fatal(err)
	}
	var got string
	if err = f.db.QueryRow(context.Background(), "SELECT password_hash FROM identity.credentials WHERE account_id=$1", a.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != oldHash {
		t.Fatal("failed authentication changed password record")
	}
	s := f.login(t, "alice", testPassword)
	if err = f.db.QueryRow(context.Background(), "SELECT password_hash FROM identity.credentials WHERE account_id=$1", a.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	params, err := decodePassword(got)
	if err != nil || params.Iterations != 3 || got == oldHash {
		t.Fatal("successful authentication did not persist the deliberate PHC policy advance", err)
	}
	if f.account(t, a.ID).Revision != 1 || f.countAudit(t, a.ID) != 1 {
		t.Fatal("same-password rehash changed account authority or generated routine Audit")
	}
	if _, err = f.service.Authenticate(context.Background(), s.Token); err != nil {
		t.Fatal(err)
	}
}

// waitForAccountFence observes the real blocked PostgreSQL statement, rather
// than assuming a goroutine has reached its transaction after an arbitrary wait.
func waitForAccountFence(t *testing.T, observer *pgx.Conn, blocker uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := observer.QueryRow(ctx, `SELECT EXISTS (
   SELECT 1 FROM pg_stat_activity
   WHERE datname=current_database()
     AND wait_event_type='Lock'
     AND $1::integer = ANY(pg_blocking_pids(pid))
     AND query='SELECT revision,enabled FROM identity.accounts WHERE id=$1 FOR UPDATE'
  )`, int64(blocker)).Scan(&waiting)
		if err != nil {
			t.Fatal("could not observe account fence", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation did not reach the account row fence", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgresSessionExpiryIsRecheckedAfterAccountFence(t *testing.T) {
	operations := []struct {
		name string
		run  func(*identityFixture, Session) error
	}{
		{"self-password-change", func(f *identityFixture, s Session) error {
			return f.service.ChangePassword(context.Background(), s.Token, testPassword, replacementPassword)
		}},
		{"rotation", func(f *identityFixture, s Session) error {
			_, err := f.service.Rotate(context.Background(), s.Token)
			return err
		}},
		{"csrf-refresh", func(f *identityFixture, s Session) error {
			_, err := f.service.RefreshCSRF(context.Background(), s.Token)
			return err
		}},
		{"session-issue", func(f *identityFixture, _ Session) error {
			_, err := f.service.Login(context.Background(), "alice", testPassword, "")
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			f := newIdentityFixture(t)
			a := f.create(t, "alice")
			s := f.login(t, "alice", testPassword)
			var originalHash string
			if err := f.db.QueryRow(context.Background(), "SELECT password_hash FROM identity.credentials WHERE account_id=$1", a.ID).Scan(&originalHash); err != nil {
				t.Fatal(err)
			}
			originalToken, _ := tokenHash(s.Token)
			originalCSRF, _ := tokenHash(s.CSRF)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			holder, err := pgx.Connect(ctx, os.Getenv("ACHRIX_IDENTITY_TEST_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, done := context.WithTimeout(context.Background(), time.Second)
				defer done()
				_ = holder.Close(cleanup)
			}()
			tx, err := holder.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, done := context.WithTimeout(context.Background(), time.Second)
				defer done()
				_ = tx.Rollback(cleanup)
			}()
			var lockedRevision int64
			if err = tx.QueryRow(ctx, "SELECT revision FROM identity.accounts WHERE id=$1 FOR UPDATE", a.ID).Scan(&lockedRevision); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- operation.run(f, s) }()
			waitForAccountFence(t, f.db, holder.PgConn().PID())
			f.clock.Store(s.ExpiresAt.Add(time.Microsecond).UnixNano())
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-result:
			case <-ctx.Done():
				t.Fatal("blocked session operation did not finish", ctx.Err())
			}
			if !errors.Is(err, ErrAuthentication) {
				t.Fatalf("expired session operation crossed the fence: %v", err)
			}
			if got := f.account(t, a.ID); got.Revision != lockedRevision || !got.Enabled {
				t.Fatal("expired operation changed account", got)
			}
			var currentHash string
			if err = f.db.QueryRow(ctx, "SELECT password_hash FROM identity.credentials WHERE account_id=$1", a.ID).Scan(&currentHash); err != nil {
				t.Fatal(err)
			}
			if currentHash != originalHash || f.countAudit(t, a.ID) != 1 {
				t.Fatal("expired operation changed credential or Audit")
			}
			var tokenStored, csrfStored []byte
			var expiry time.Time
			var revision int64
			if err = f.db.QueryRow(ctx, "SELECT token_hash,csrf_hash,expires_at,account_revision FROM identity.sessions WHERE account_id=$1", a.ID).Scan(&tokenStored, &csrfStored, &expiry, &revision); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = f.db.QueryRow(ctx, "SELECT count(*) FROM identity.sessions WHERE account_id=$1", a.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 || !bytes.Equal(tokenStored, originalToken) || !bytes.Equal(csrfStored, originalCSRF) || !expiry.Equal(s.ExpiresAt) || revision != lockedRevision {
				t.Fatal("expired operation changed persisted sessions")
			}
		})
	}
}

func TestPostgresStaleRehashCannotDowngradeCommittedPHC(t *testing.T) {
	f := newIdentityFixture(t)
	a := f.create(t, "alice")
	ctx, cancel := deadline()
	defer cancel()
	snapshot, err := f.module.findCredential(ctx, "id", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	stronger, err := newPasswords(PasswordPolicy{19 * 1024, 4, 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	weaker, err := newPasswords(PasswordPolicy{19 * 1024, 3, 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	strongHash, err := stronger.hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	weakHash, err := weaker.hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	// Both completions were prepared from the same originally verified account
	// revision and PHC. The slower weaker completion must lose the PHC fence.
	strongSession, strongRecord := newSession(a.ID, snapshot.Revision, f.module.now().Add(time.Minute))
	weakSession, weakRecord := newSession(a.ID, snapshot.Revision, f.module.now().Add(time.Minute))
	if err = f.module.issue(ctx, snapshot, strongRecord, strongHash, nil, f.module.now()); err != nil {
		t.Fatal(err)
	}
	if err = f.module.issue(ctx, snapshot, weakRecord, weakHash, nil, f.module.now()); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = f.db.QueryRow(ctx, "SELECT password_hash FROM identity.credentials WHERE account_id=$1", a.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != strongHash {
		t.Fatal("a stale rehash completion overwrote the committed stronger PHC")
	}
	params, err := decodePassword(stored)
	if err != nil || params.Iterations != 4 {
		t.Fatal("committed PHC metadata lost the stronger policy", err)
	}
	for _, s := range []Session{strongSession, weakSession} {
		p, e := f.service.Authenticate(context.Background(), s.Token)
		if e != nil || p != achrix.Principal(a.ID) {
			t.Fatal("same-password rehash session became invalid", e)
		}
	}
	if f.account(t, a.ID).Revision != snapshot.Revision || f.countAudit(t, a.ID) != 1 {
		t.Fatal("rehash changed account revision or created routine Audit")
	}
	var count int
	if err = f.db.QueryRow(ctx, "SELECT count(*) FROM identity.sessions WHERE account_id=$1", a.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("one same-password rehash completion lost its session")
	}
}

func TestPostgresMinimumResourceCapsCreateAndLogin(t *testing.T) {
	f := newIdentityFixtureConfig(t, Config{MaxConns: 1, MaxOperations: 2}, audit.Config{MaxConns: 1, MaxOperations: 2})
	a := f.create(t, "minimum-caps")
	session := f.login(t, a.Login, testPassword)
	if string(session.Principal) != a.ID || f.countAudit(t, a.ID) != 1 {
		t.Fatal("isolated nested create/login lost supported minimum behavior")
	}
	if f.module.active != 0 || f.module.pool.Stat().MaxConns() != 1 {
		t.Fatal("minimum resource configuration or lease accounting lost")
	}
}

func TestPostgresConfiguredPoolAdmissionAndCanceledWait(t *testing.T) {
	f := newIdentityFixtureConfig(t, Config{MaxConns: 6, MaxOperations: 8}, audit.Config{})
	m := f.module
	if stat := m.pool.Stat(); stat.MaxConns() != 6 || stat.TotalConns() != 1 {
		t.Fatal("start filled the configured maximum instead of demand", stat)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var release []func()
	defer func() {
		for _, done := range release {
			done()
		}
	}()
	for range 6 {
		lease, pool, finish, err := m.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := pool.Acquire(lease)
		if err != nil {
			finish()
			t.Fatal(err)
		}
		release = append(release, func() { conn.Release(); finish() })
	}
	if stat := m.pool.Stat(); stat.TotalConns() != 6 || stat.AcquiredConns() != 6 {
		t.Fatal("explicit pool above four was ineffective", stat)
	}
	// Readiness owns a lease while the native pool waits. Cancellation/deadline
	// must return the context category and release only that waiting lease.
	for _, cancelEarly := range []bool{false, true} {
		wait, done := context.WithTimeout(ctx, 50*time.Millisecond)
		result := make(chan error, 1)
		go func() { result <- m.Ready(wait) }()
		until := time.After(time.Second)
		for {
			m.mu.Lock()
			active := m.active
			m.mu.Unlock()
			if active == 7 {
				break
			}
			select {
			case <-until:
				t.Fatal("native pool wait did not own a lease")
			case <-time.After(time.Millisecond):
			}
		}
		want := context.DeadlineExceeded
		if cancelEarly {
			done()
			want = context.Canceled
		}
		if err := <-result; !errors.Is(err, want) {
			t.Fatal("pool wait lost context category", err)
		}
		done()
		m.mu.Lock()
		active := m.active
		m.mu.Unlock()
		if active != 6 {
			t.Fatal("canceled pool wait leaked its lease", active)
		}
	}
	for range 2 {
		_, _, finish, err := m.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		release = append(release, finish)
	}
	if err := m.Ready(ctx); !errors.Is(err, ErrLimited) {
		t.Fatal("configured admission did not fail fast", err)
	}
	for _, done := range release {
		done()
	}
	release = nil
	if err := m.Ready(ctx); err != nil || m.active != 0 || m.pool.Stat().TotalConns() != 6 {
		t.Fatal("released resources were not reusable", err)
	}
	if err := f.app.Shutdown(ctx); err != nil || m.pool != nil || m.active != 0 {
		t.Fatal("configured resources failed to stop", err)
	}
}
