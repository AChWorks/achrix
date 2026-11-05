// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"testing"
	"time"
)

func TestPruneBoundsRevokedGrantHistoryWithoutTouchingLiveCredential(t *testing.T) {
	f := newFixture(t)
	code, _ := f.consent("public.example", "alice")
	alice := f.tokens("public.example", "", "authorization_code", code)
	grant, _, err := f.srv.VerifyAccess(context.Background(), testResource, alice.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.srv.Disconnect(context.Background(), "alice", grant.ID); err != nil {
		t.Fatal(err)
	}
	// After twenty days Alice's family is expired, but retains the previous
	// explicit revocation for the full reconciliation window.
	f.now = f.now.Add(20 * 24 * time.Hour)
	if err = f.srv.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	var revoked int
	if err = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.grants WHERE revoked_at IS NOT NULL").Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("recent revocation erased too soon: %d %v", revoked, err)
	}
	code, _ = f.consent("public.example", "bob")
	bob := f.tokens("public.example", "", "authorization_code", code)
	// Alice's event is older than thirty days, but Bob's outstanding refresh
	// and grant must stay; only orphan grant history is eligible for pruning.
	f.now = f.now.Add(12 * 24 * time.Hour)
	if err = f.srv.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	var active int
	if err = f.pool.QueryRow(context.Background(), "SELECT count(*) FILTER(WHERE revoked_at IS NULL),count(*) FILTER(WHERE revoked_at IS NOT NULL) FROM oauth.grants").Scan(&active, &revoked); err != nil {
		t.Fatal(err)
	}
	if active != 1 || revoked != 0 {
		t.Fatalf("orphan retention/liveness wrong active=%d revoked=%d", active, revoked)
	}
	if status := f.tokenCall("public.example", "", "refresh_token", bob.RefreshToken).StatusCode; status != 200 {
		t.Fatalf("fresh grant unexpectedly pruned: %d", status)
	}
}
