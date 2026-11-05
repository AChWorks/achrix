// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Separate, ordered fixture for scripts/validate.sh: leave retained grant,
// hashed token and revocation state in the task-owned database for pg_dump.
func TestSeedOAuthRetainedStateForRestore(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("public.example", "alice")
	a := f.tokens("public.example", "", "authorization_code", code)
	g, _, err := f.srv.VerifyAccess(context.Background(), testResource, a.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.srv.Disconnect(context.Background(), "alice", g.ID); err != nil {
		t.Fatal(err)
	}
	code, _ = f.consent("public.example", "bob")
	b := f.tokens("public.example", "", "authorization_code", code)
	_ = f.tokens("public.example", "", "refresh_token", b.RefreshToken)
	var active, revoked int
	err = f.pool.QueryRow(context.Background(), "SELECT count(*) FILTER(WHERE revoked_at IS NULL),count(*) FILTER(WHERE revoked_at IS NOT NULL) FROM oauth.grants").Scan(&active, &revoked)
	if err != nil || active != 1 || revoked != 1 {
		t.Fatalf("restore seed grants active=%d revoked=%d err=%v", active, revoked, err)
	}
}

// Verify a native pg_dump/pg_restore retained-state and migration-checksum
// roundtrip. This never restores plaintext credentials from a database dump.
func TestVerifiedOAuthRestore(t *testing.T) {
	dsn := os.Getenv("ACHRIX_OAUTH_RESTORE_DSN")
	if dsn == "" {
		t.Skip("ACHRIX_OAUTH_RESTORE_DSN absent: restore verification not run")
	}
	config := taskTestConfig(t, dsn, "achrix_oauth_restore")
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	checkTaskTestDatabase(t, context.Background(), pool, "achrix_oauth_restore")
	if err = Migrate(context.Background(), pool); err != nil {
		t.Fatal("restored immutable ledger invalid:", err)
	}
	var active, revoked int
	err = pool.QueryRow(context.Background(), "SELECT count(*) FILTER(WHERE revoked_at IS NULL),count(*) FILTER(WHERE revoked_at IS NOT NULL) FROM oauth.grants").Scan(&active, &revoked)
	if err != nil || active != 1 || revoked != 1 {
		t.Fatalf("restored active=%d revoked=%d err=%v", active, revoked, err)
	}
	var tokens, used int
	err = pool.QueryRow(context.Background(), "SELECT count(*),count(*) FILTER(WHERE used_at IS NOT NULL) FROM oauth.refresh_tokens").Scan(&tokens, &used)
	if err != nil || tokens != 3 || used != 1 {
		t.Fatalf("restored refresh tokens count=%d used=%d err=%v", tokens, used, err)
	}
	var unmasked int
	err = pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.access_tokens WHERE octet_length(token_hash)<>32").Scan(&unmasked)
	if err != nil || unmasked != 0 {
		t.Fatalf("invalid hashed token storage: count=%d err=%v", unmasked, err)
	}
}
