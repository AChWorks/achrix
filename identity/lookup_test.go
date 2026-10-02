// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/jackc/pgx/v5"
)

type lookupCapability struct{}

func (lookupCapability) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "test.lookup", Version: "1", Provides: []achrix.Capability{{ID: AccountLookup, Version: 1}}}
}
func (lookupCapability) Start(context.Context) error { return nil }
func (lookupCapability) Ready(context.Context) error { return nil }
func (lookupCapability) Stop(context.Context) error  { return nil }

func TestLookupAccountAuthorizesExactLoginBeforeStorage(t *testing.T) {
	calls := 0
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, achrix.PolicyFunc(func(ctx context.Context, p achrix.Principal, cap, target string) error {
		calls++
		if p != "reader" || cap != AccountLookup || target != "known.login" {
			t.Fatal("lookup authorization target changed")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
			t.Fatal("unbounded lookup authorization")
		}
		return achrix.ErrDenied
	}), lookupCapability{})
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	// A nil storage Module would panic if denial were evaluated after the query.
	service := &Service{app: app}
	if _, err = service.LookupAccount(context.Background(), "reader", "known.login"); !errors.Is(err, achrix.ErrDenied) || calls != 1 {
		t.Fatal("lookup denial must precede any storage access", err, calls)
	}
	for _, login := range []string{"", "Known.login", "known.login%", "known login", strings.Repeat("a", 65)} {
		if _, err = service.LookupAccount(context.Background(), "reader", login); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid lookup target accepted", login, err)
		}
	}
	if calls != 1 {
		t.Fatal("invalid targets reached authorization")
	}
}

// This standalone proof deliberately resets only the named task-owned Admin DB.
// It uses the public Service for create/lookup and real PostgreSQL UNIQUE(login).
func TestLookupAccountPostgresExactMetadata(t *testing.T) {
	dsn := os.Getenv("ACHRIX_ADMIN_TEST_DSN")
	if dsn == "" {
		t.Skip("ACHRIX_ADMIN_TEST_DSN absent: exact-login PostgreSQL proof not run")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || cfg.Database != "achrix_admin_test" {
		t.Fatal("requires private task-owned achrix_admin_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err = db.Exec(ctx, "DROP SCHEMA IF EXISTS identity CASCADE; DROP SCHEMA IF EXISTS audit CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err = audit.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	am, err := audit.NewPostgres(dsn, audit.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	im, err := NewPostgres(dsn, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lookupTargets := []string{}
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, cap, target string) error {
		if p == "creator" && (cap == AccountCreate || cap == audit.Append) {
			return nil
		}
		if p == "lookup-reader" && cap == AccountLookup {
			lookupTargets = append(lookupTargets, target)
			return nil
		}
		if p == "id-reader" && cap == AccountRead {
			return nil
		}
		if p == PublicPrincipal && cap == Authentication {
			return nil
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second}, policy, im, am)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	as, err := audit.NewService(app, am)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(app, im, as)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.CreateAccount(ctx, "creator", "exact.login", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateAccount(ctx, "creator", "exact.login-more", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	got, err := service.LookupAccount(ctx, "lookup-reader", "exact.login")
	if err != nil || got != first {
		t.Fatal("exact lookup must recover only original safe metadata", got, err)
	}
	if _, err = service.LookupAccount(ctx, "lookup-reader", "exact"); !errors.Is(err, ErrNotFound) {
		t.Fatal("prefix is not an exact match", err)
	}
	for _, p := range []achrix.Principal{"id-reader", PublicPrincipal, achrix.Principal(first.ID)} {
		if _, err = service.LookupAccount(ctx, p, "exact.login"); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal("other permissions/authentication implicitly allow lookup", p, err)
		}
	}
	if _, err = service.Account(ctx, "lookup-reader", first.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("lookup implicitly allows opaque-ID read", err)
	}
	if len(lookupTargets) != 2 || lookupTargets[0] != "exact.login" || lookupTargets[1] != "exact" {
		t.Fatal("lookup authorization must use exact input", lookupTargets)
	}
	// Prove an existing index can satisfy the exact runtime predicate; no migration
	// or unbounded scan is needed for the new metadata path.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, "EXPLAIN SELECT id,login,enabled,revision FROM identity.accounts WHERE login=$1", "exact.login")
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !strings.Contains(plan.String(), "Index Scan") || !strings.Contains(plan.String(), "accounts_login_key") {
		t.Fatal("exact predicate lacks existing login index", plan.String())
	}
	// Metadata reconciliation does not depend on a credential or session join.
	if _, err = tx.Exec(ctx, "DELETE FROM identity.credentials WHERE account_id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = service.LookupAccount(ctx, "lookup-reader", "exact.login")
	if err != nil || got != first {
		t.Fatal("lookup must remain a metadata-only read", err)
	}
	if _, err = db.Exec(ctx, "DROP TABLE identity.accounts CASCADE"); err != nil {
		t.Fatal(err)
	}
	before := im.FailureCount()
	if _, err = service.LookupAccount(ctx, "id-reader", "exact.login"); !errors.Is(err, achrix.ErrDenied) || im.FailureCount() != before {
		t.Fatal("denied lookup queried broken storage", err)
	}
	if _, err = service.LookupAccount(ctx, "lookup-reader", "exact.login"); !errors.Is(err, ErrUnavailable) || im.FailureCount() != before+1 {
		t.Fatal("authorized storage failure must be safe and observable", err)
	}
}
