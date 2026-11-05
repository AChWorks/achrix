// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestOAuthExpiryCodeConsentAccessAndRefresh(t *testing.T) {
	f := newFixture(t)
	// Consent one-time state must expire without granting authority.
	id, csrf := f.ceremony("public.example", "alice")
	f.now = f.now.Add(6 * time.Minute)
	expired := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id, csrf, "allow"), "alice")
	if expired.StatusCode != http.StatusForbidden {
		t.Fatalf("expired consent status %d", expired.StatusCode)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM oauth.grants").Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired consent created grant %d err=%v", count, err)
	}
	// A fresh approved code cannot be redeemed after its own 90-second TTL.
	code, _ := f.consent("public.example", "alice")
	f.now = f.now.Add(91 * time.Second)
	if resp := f.tokenCall("public.example", "", "authorization_code", code); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expired code accepted %d", resp.StatusCode)
	}
	// Issued access and refresh expire independently: access lasts ten minutes,
	// refresh lifetime is absolute and never extended by rotation.
	fresh, _ := f.consent("public.example", "alice")
	access := f.tokens("public.example", "", "authorization_code", fresh)
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, access.AccessToken); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(10*time.Minute + time.Microsecond)
	if _, _, err := f.srv.VerifyAccess(context.Background(), testResource, access.AccessToken); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expired access accepted: %v", err)
	}
	refreshed := f.tokens("public.example", "", "refresh_token", access.RefreshToken)
	if refreshed.RefreshToken == access.RefreshToken {
		t.Fatal("refresh credential unchanged")
	}
	f.now = f.now.Add(14 * 24 * time.Hour)
	if resp := f.tokenCall("public.example", "", "refresh_token", refreshed.RefreshToken); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expired absolute refresh accepted: %d", resp.StatusCode)
	}
}
func TestNoClientStateDoesNotInventState(t *testing.T) {
	f := newFixture(t)
	target, _ := url.Parse(authURL(f, "public.example"))
	query := target.Query()
	query.Del("state")
	resp := f.mustReq(http.MethodGet, f.srv.AuthorizePath()+"?"+query.Encode(), "", "alice")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize without state returned %d", resp.StatusCode)
	}
	raw := mustBody(t, resp)
	id := hiddenID.FindStringSubmatch(raw)
	csrf := hiddenCSRF.FindStringSubmatch(raw)
	if len(id) != 2 || len(csrf) != 2 {
		t.Fatal("no consent transaction")
	}
	finish := f.mustReq(http.MethodPost, f.srv.AuthorizePath(), consentForm(id[1], csrf[1], "deny"), "alice")
	if finish.StatusCode != http.StatusSeeOther {
		t.Fatalf("no-state deny status %d", finish.StatusCode)
	}
	returned, err := url.Parse(finish.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if returned.Query().Has("state") {
		t.Fatal("AS invented client state where none was requested")
	}
	if returned.Query().Get("iss") != f.web.URL {
		t.Fatal("missing issuer on no-state denial")
	}
}
func TestTamperedMigrationLedgerFailsClosed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "UPDATE oauth.schema_migrations SET checksum='corrupted' WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, f.pool); err == nil {
		t.Fatal("modified historical schema checksum accepted")
	}
	if _, err := f.pool.Exec(ctx, "UPDATE oauth.schema_migrations SET checksum=$1 WHERE version=1", embeddedChecksum(t)); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, f.pool); err != nil {
		t.Fatalf("reconciled unchanged ledger rejected: %v", err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO oauth.schema_migrations(version,checksum) VALUES(2,'future')"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, f.pool); err == nil {
		t.Fatal("unrecognized migration version accepted")
	}
}
func embeddedChecksum(t *testing.T) string {
	t.Helper()
	source, err := migrations.ReadFile("migrations/001_oauth.sql")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	return hex.EncodeToString(digest[:])
}
