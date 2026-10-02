// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/infrastructure"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These credentials are synthetic, public test fixtures, never deployment input.
const stableFixtureLogin = "consumer-admin"
const fixtureInitialPassword = "Synthetic consumer initial password 123!"
const fixtureRetainedPassword = "Synthetic consumer retained password 456!"
const fixtureAdministrator achrix.Principal = "consumer-bootstrap"
const fixtureNoteText = "Identity authenticates; this product authorizes this note."

// This fixture deliberately runs sequentially: each source run resets only its
// explicitly named disposable database. The restore test never resets schema.
func identityAuditPool(t *testing.T, restore bool) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("NOTES_IDENTITY_DATABASE_URL")
	c, err := pgxpool.ParseConfig(dsn)
	want := "achrix_identity_consumer"
	if restore {
		want = "achrix_identity_restore"
	}
	if err != nil || dsn == "" || c.ConnConfig.Database != want {
		t.Fatal("explicit private NOTES_IDENTITY_DATABASE_URL required")
	}
	c.MaxConns = 2
	c.ConnConfig.ConnectTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		t.Fatal("private consumer database unavailable")
	}
	t.Cleanup(p.Close)
	var actual string
	if err := p.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != want {
		t.Fatal("private consumer database identity mismatch")
	}
	if err := infrastructure.CheckEnvironment(ctx, p); err != nil {
		t.Fatal(err)
	}
	return p, dsn
}

// Authorization remains product-owned and is independently adjustable. Identity's
// public principal can only attempt authentication; it cannot read/write Notes or
// administer accounts. An account's own password change still needs permission.
type identityConsumerPolicy struct {
	accountID atomic.Value
	notes     atomic.Bool
}

func newIdentityConsumerPolicy() *identityConsumerPolicy {
	p := &identityConsumerPolicy{}
	p.accountID.Store("")
	return p
}
func (p *identityConsumerPolicy) Authorize(_ context.Context, actor achrix.Principal, capability, resource string) error {
	if actor == identity.PublicPrincipal {
		if capability == identity.Authentication && resource == "" {
			return nil
		}
		return achrix.ErrDenied
	}
	if actor == fixtureAdministrator {
		switch capability {
		case identity.AccountCreate, identity.AccountRead, identity.CredentialSet, identity.AccountSetEnabled, identity.SessionRevokeAll, audit.Append, audit.Query, audit.Export:
			return nil
		}
	}
	id := p.accountID.Load().(string)
	if id != "" && string(actor) == id {
		if resource == id && (capability == identity.PasswordChange || capability == audit.Append) {
			return nil
		}
		if p.notes.Load() && (capability == application.Create || capability == application.Read) {
			return nil
		}
	}
	return achrix.ErrDenied
}

type identityConsumer struct {
	app      *achrix.Application
	identity *identity.Service
	audit    *audit.Service
	notes    *application.Service
	policy   *identityConsumerPolicy
	pool     *pgxpool.Pool
}

func composeIdentityConsumer(t *testing.T, restore bool) *identityConsumer {
	t.Helper()
	p, dsn := identityAuditPool(t, restore)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !restore {
		// A fixed, verified fixture database is the entire destructive target.
		if _, err := p.Exec(ctx, "DROP SCHEMA IF EXISTS identity CASCADE; DROP SCHEMA IF EXISTS audit CASCADE; DROP SCHEMA IF EXISTS notes CASCADE"); err != nil {
			t.Fatal("private consumer schema reset failed")
		}
	}
	// Installation is explicit. On restore these calls verify immutable ledgers;
	// no traffic-driven migration or source-copy/local-replace seam exists.
	if err := audit.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := identity.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := infrastructure.Migrate(ctx, p, infrastructure.Migrations()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	auditModule, err := audit.NewPostgres(dsn, audit.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	identityModule, err := identity.NewPostgres(dsn, identity.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	notesModule, err := infrastructure.New(dsn, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := newIdentityConsumerPolicy()
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 3 * time.Second, Logger: logger}, policy, identityModule, notesModule, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	auditService, err := audit.NewService(app, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(app, identityModule, auditService)
	if err != nil {
		t.Fatal(err)
	}
	return &identityConsumer{app, identityService, auditService, application.New(app, notesModule, logger), policy, p}
}

// This small product adapter forwards a principal through the existing typed
// Notes Application. Its input/policy/domain persistence remains consumer-owned.
func identityNotesHandler(web *identity.Web, notes *application.Service) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/identity/", http.StripPrefix("/identity", web.Handler()))
	mux.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p, err := web.AuthenticateRequest(r, true)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n, err := notes.Create(r.Context(), p, body.Text)
		if errors.Is(err, achrix.ErrDenied) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(n)
	})
	mux.HandleFunc("/notes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p, err := web.AuthenticateRequest(r, false)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n, err := notes.Read(r.Context(), p, strings.TrimPrefix(r.URL.Path, "/notes/"))
		if errors.Is(err, achrix.ErrDenied) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(n)
	})
	return mux
}

type identityHTTPResult struct {
	status  int
	headers http.Header
	cookies []*http.Cookie
	body    []byte
}

func identityRequest(t *testing.T, server *httptest.Server, method, path, origin, csrf, body string, cookie *http.Cookie) identityHTTPResult {
	t.Helper()
	r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal("fixture request construction failed")
	}
	r.Header.Set("Origin", origin)
	r.Header.Set("X-Identity-Request", "1")
	r.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	client := server.Client()
	client.Timeout = 6 * time.Second
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("fixture HTTPS request failed")
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(b) > 4096 {
		t.Fatal("fixture response bound failed")
	}
	return identityHTTPResult{response.StatusCode, response.Header.Clone(), response.Cookies(), b}
}

func fixtureSession(t *testing.T, result identityHTTPResult, principal achrix.Principal) (*http.Cookie, string) {
	t.Helper()
	if result.status != http.StatusOK || result.headers.Get("Cache-Control") != "no-store" || result.headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("unexpected authenticated session response", result.status)
	}
	if len(result.cookies) != 1 {
		t.Fatal("expected one session cookie")
	}
	cookie := result.cookies[0]
	if cookie.Name != identity.CookieName || cookie.Path != "/" || cookie.Domain != "" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 || !cookie.Expires.IsZero() || len(cookie.Value) != 43 {
		t.Fatal("session cookie scope differs from the explicit browser contract")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(result.body, &fields) != nil || len(fields) != 3 {
		t.Fatal("session JSON contract differs")
	}
	var got struct {
		Principal achrix.Principal `json:"principal"`
		CSRF      string           `json:"csrf"`
		ExpiresAt time.Time        `json:"expires_at"`
	}
	if json.Unmarshal(result.body, &got) != nil || got.Principal != principal || len(got.CSRF) != 43 || !got.ExpiresAt.After(time.Now()) || got.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
		t.Fatal("invalid public session fields")
	}
	for _, required := range []string{"principal", "csrf", "expires_at"} {
		if _, ok := fields[required]; !ok {
			t.Fatal("missing public session field")
		}
	}
	if strings.Contains(string(result.body), cookie.Value) {
		t.Fatal("session bearer entered JSON")
	}
	return cookie, got.CSRF
}

func fixtureCounts(t *testing.T, pool *pgxpool.Pool, notes, records, sessions int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var actualNotes, actualRecords, actualSessions int
	err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM notes.entries),(SELECT count(*) FROM audit.records),(SELECT count(*) FROM identity.sessions)").Scan(&actualNotes, &actualRecords, &actualSessions)
	if err != nil || actualNotes != notes || actualRecords != records || actualSessions != sessions {
		t.Fatal("private consumer side-effect counts differ", actualNotes, actualRecords, actualSessions)
	}
}

func fixtureAuditRecords(t *testing.T, service *audit.Service, target string) []audit.Record {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var all []audit.Record
	cursor := ""
	// Actual authorized keyset query/export is bounded even for this tiny history.
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		page, err := service.Query(ctx, fixtureAdministrator, target, cursor, 2)
		if err != nil || len(page.Records) > 2 {
			t.Fatal("bounded public Audit query failed")
		}
		exported, err := service.Export(ctx, fixtureAdministrator, target, cursor, 2)
		if err != nil {
			t.Fatal("authorized Audit export failed")
		}
		queryJSON, _ := json.Marshal(page)
		exportJSON, _ := json.Marshal(exported)
		if string(queryJSON) != string(exportJSON) {
			t.Fatal("query/export retained view differs")
		}
		all = append(all, page.Records...)
		if page.NextCursor == "" {
			return all
		}
		if page.NextCursor == cursor {
			t.Fatal("Audit keyset cursor made no progress")
		}
		cursor = page.NextCursor
	}
	t.Fatal("fixture history unexpectedly exceeded bounded pagination")
	return nil
}

func checkFixtureAudit(t *testing.T, records []audit.Record, principal achrix.Principal, secrets ...string) {
	t.Helper()
	if len(records) != 5 {
		t.Fatal("retained accountable event count differs", len(records))
	}
	expected := map[string]string{
		"identity.account.create":      identity.AccountCreate,
		"identity.credential.change":   identity.PasswordChange,
		"identity.account.set-enabled": identity.AccountSetEnabled,
		"identity.session.revoke-all":  identity.SessionRevokeAll,
	}
	counts := map[string]int{}
	ids := map[string]bool{}
	for _, record := range records {
		actor := fixtureAdministrator
		if record.Action == "identity.credential.change" {
			actor = principal
		}
		if record.Actor != actor || record.Target != string(principal) || record.Authority != expected[record.Action] || record.Outcome != "succeeded" || len(record.ID) != 26 || ids[record.ID] || record.OccurredAt.IsZero() || record.OccurredAt.Location() != time.UTC || record.OccurredAt.After(time.Now().Add(time.Minute)) {
			t.Fatal("retained who/action/target/authority/outcome/time differs")
		}
		ids[record.ID] = true
		counts[record.Action]++
	}
	if counts["identity.account.create"] != 1 || counts["identity.credential.change"] != 1 || counts["identity.account.set-enabled"] != 2 || counts["identity.session.revoke-all"] != 1 {
		t.Fatal("accountable event classifications differ")
	}
	b, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range append(secrets, fixtureInitialPassword, fixtureRetainedPassword, stableFixtureLogin) {
		if secret != "" && strings.Contains(string(b), secret) {
			t.Fatal("credential/session/sensitive fixture material entered Audit")
		}
	}
}

func TestIdentityAuditPublicConsumer(t *testing.T) {
	f := composeIdentityConsumer(t, false)
	account, err := f.identity.CreateAccount(context.Background(), fixtureAdministrator, stableFixtureLogin, fixtureInitialPassword)
	if err != nil || account.ID == "" || !account.Enabled {
		t.Fatal("public account provisioning failed")
	}
	f.policy.accountID.Store(account.ID)
	principal := achrix.Principal(account.ID)
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	web, err := identity.NewWeb(f.identity, origin)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = identityNotesHandler(web, f.notes)
	server.StartTLS()
	t.Cleanup(server.Close)
	loginBody, _ := json.Marshal(map[string]string{"login": stableFixtureLogin, "password": fixtureInitialPassword})
	cookie, csrf := fixtureSession(t, identityRequest(t, server, "POST", "/identity/login", origin, "", string(loginBody), nil), principal)
	if got, err := f.identity.Authenticate(context.Background(), cookie.Value); err != nil || got != principal {
		t.Fatal("cookie did not map to the stable public principal")
	}
	noteBody, _ := json.Marshal(map[string]string{"text": fixtureNoteText})
	if _, err := f.notes.Create(context.Background(), identity.PublicPrincipal, fixtureNoteText); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("authentication-attempt principal acquired product authority")
	}
	if _, err := f.notes.Create(context.Background(), principal, fixtureNoteText); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("authentication implicitly granted the product operation")
	}
	if got := identityRequest(t, server, "POST", "/notes", origin, csrf, string(noteBody), cookie); got.status != http.StatusForbidden {
		t.Fatal("HTTP authentication bypassed product authorization", got.status)
	}
	fixtureCounts(t, f.pool, 0, 1, 1)
	f.policy.notes.Store(true)
	for _, probe := range []struct{ method, path, origin, csrf string }{
		{"POST", "/notes", "https://attacker.invalid", csrf},
		{"POST", "/notes", origin, "wrong-synchronizer-token"},
		{"POST", "/notes?session=" + cookie.Value, origin, csrf},
		{"GET", "/notes/AAAAAAAAAAAAAAAAAAAAAAAAAA?session=" + cookie.Value, origin, ""},
	} {
		if got := identityRequest(t, server, probe.method, probe.path, probe.origin, probe.csrf, string(noteBody), cookie); got.status != http.StatusUnauthorized {
			t.Fatal("browser origin/CSRF/query credential boundary accepted a probe", got.status)
		}
	}
	if got := identityRequest(t, server, "GET", "/notes/AAAAAAAAAAAAAAAAAAAAAAAAAA?session="+cookie.Value, origin, "", "", nil); got.status != http.StatusUnauthorized {
		t.Fatal("URL session credential was accepted without a cookie")
	}
	fixtureCounts(t, f.pool, 0, 1, 1)
	written := identityRequest(t, server, "POST", "/notes", origin, csrf, string(noteBody), cookie)
	var note domain.Note
	if written.status != http.StatusCreated || json.Unmarshal(written.body, &note) != nil || note.Text != fixtureNoteText {
		t.Fatal("authorized product write failed", written.status)
	}
	if found, err := f.notes.Read(context.Background(), principal, note.ID); err != nil || found.Text != fixtureNoteText {
		t.Fatal("persisted product read failed")
	}
	read := identityRequest(t, server, "GET", "/notes/"+note.ID, origin, "", "", cookie)
	var readNote domain.Note
	if read.status != http.StatusOK || json.Unmarshal(read.body, &readNote) != nil || readNote.ID != note.ID || readNote.Text != fixtureNoteText {
		t.Fatal("authenticated HTTP product read failed", read.status)
	}
	fixtureCounts(t, f.pool, 1, 1, 1)
	rotated, nextCSRF := fixtureSession(t, identityRequest(t, server, "POST", "/identity/rotate", origin, csrf, "", cookie), principal)
	if rotated.Value == cookie.Value {
		t.Fatal("session rotation reused the bearer")
	}
	if _, err := f.identity.Authenticate(context.Background(), cookie.Value); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("rotation left the old session active")
	}
	f.policy.notes.Store(false)
	if got := identityRequest(t, server, "POST", "/notes", origin, nextCSRF, string(noteBody), rotated); got.status != http.StatusForbidden {
		t.Fatal("rotation changed product authority", got.status)
	}
	fixtureCounts(t, f.pool, 1, 1, 1)
	wrongPasswordBody, _ := json.Marshal(map[string]string{"current": "Wrong synthetic current password", "next": fixtureRetainedPassword})
	if got := identityRequest(t, server, "POST", "/identity/password", origin, nextCSRF, string(wrongPasswordBody), rotated); got.status != http.StatusUnauthorized {
		t.Fatal("own password change accepted failed reauthentication", got.status)
	}
	fixtureCounts(t, f.pool, 1, 1, 1)
	changeBody, _ := json.Marshal(map[string]string{"current": fixtureInitialPassword, "next": fixtureRetainedPassword})
	changed := identityRequest(t, server, "POST", "/identity/password", origin, nextCSRF, string(changeBody), rotated)
	if changed.status != http.StatusNoContent || len(changed.cookies) != 1 || changed.cookies[0].Name != identity.CookieName || changed.cookies[0].MaxAge != -1 {
		t.Fatal("own password change did not clear the browser session", changed.status)
	}
	if _, err := f.identity.Authenticate(context.Background(), rotated.Value); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("password change left an old session active")
	}
	if _, err := f.identity.Login(context.Background(), stableFixtureLogin, fixtureInitialPassword, ""); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("replaced password still authenticated")
	}
	retainedBody, _ := json.Marshal(map[string]string{"login": stableFixtureLogin, "password": fixtureRetainedPassword})
	retained, retainedCSRF := fixtureSession(t, identityRequest(t, server, "POST", "/identity/login", origin, "", string(retainedBody), nil), principal)
	if _, err := f.identity.Account(context.Background(), principal, account.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("account authentication granted administrative read")
	}
	managed, err := f.identity.Account(context.Background(), fixtureAdministrator, account.ID)
	if err != nil || managed.Revision != 2 {
		t.Fatal("credential revision did not advance atomically")
	}
	if err := f.identity.SetEnabled(context.Background(), fixtureAdministrator, account.ID, managed.Revision, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.identity.Authenticate(context.Background(), retained.Value); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("disabled account retained an authenticated session")
	}
	if _, err := f.identity.Login(context.Background(), stableFixtureLogin, fixtureRetainedPassword, ""); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("disabled account authenticated")
	}
	managed, err = f.identity.Account(context.Background(), fixtureAdministrator, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.identity.SetEnabled(context.Background(), fixtureAdministrator, account.ID, managed.Revision, true); err != nil {
		t.Fatal(err)
	}
	finalSession, err := f.identity.Login(context.Background(), stableFixtureLogin, fixtureRetainedPassword, "")
	if err != nil || finalSession.Principal != principal {
		t.Fatal("retained credential did not survive status lifecycle")
	}
	if err := f.identity.RevokeAll(context.Background(), fixtureAdministrator, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.identity.Authenticate(context.Background(), finalSession.Token); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("explicit revoke-all left a session active")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.audit.Query(ctx, principal, account.ID, "", 2); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("authenticated account acquired Audit query access")
	}
	if _, err := f.audit.Export(ctx, principal, account.ID, "", 2); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("authenticated account acquired Audit export access")
	}
	if _, err := f.audit.Query(ctx, fixtureAdministrator, account.ID, "", 101); !errors.Is(err, audit.ErrInput) {
		t.Fatal("Audit accepted an unbounded page")
	}
	records := fixtureAuditRecords(t, f.audit, account.ID)
	checkFixtureAudit(t, records, principal, cookie.Value, csrf, rotated.Value, nextCSRF, retained.Value, retainedCSRF, finalSession.Token, finalSession.CSRF, os.Getenv("NOTES_IDENTITY_DATABASE_URL"))
	fixtureCounts(t, f.pool, 1, 5, 0)
	// No bearer remains in the captured source database. Quiesce ingress and the
	// whole composition before validate.sh captures its native logical dump.
	server.Close()
	if err := f.app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityAuditTrustedRestore(t *testing.T) {
	if os.Getenv("NOTES_IDENTITY_RESTORE_VERIFY") != "1" {
		t.Skip("validate.sh invokes this after its trusted private logical restore")
	}
	f := composeIdentityConsumer(t, true)
	// The source was quiesced after revoke-all: restored old bearer material must
	// not become an active session. This is a test profile, not production recovery.
	fixtureCounts(t, f.pool, 1, 5, 0)
	session, err := f.identity.Login(context.Background(), stableFixtureLogin, fixtureRetainedPassword, "")
	if err != nil {
		t.Fatal("restored retained credential failed public authentication")
	}
	f.policy.accountID.Store(string(session.Principal))
	f.policy.notes.Store(true)
	account, err := f.identity.Account(context.Background(), fixtureAdministrator, string(session.Principal))
	if err != nil || account.Login != stableFixtureLogin || !account.Enabled || account.Revision != 5 || account.ID != string(session.Principal) {
		t.Fatal("restored stable account/status/revision differs")
	}
	records := fixtureAuditRecords(t, f.audit, account.ID)
	checkFixtureAudit(t, records, session.Principal, session.Token, session.CSRF, os.Getenv("NOTES_IDENTITY_DATABASE_URL"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.app.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	// Only the consumer's own Notes schema is inspected to discover a note. The
	// actual restored read still traverses product Application authorization.
	var noteID string
	if err := f.pool.QueryRow(ctx, "SELECT id FROM notes.entries").Scan(&noteID); err != nil {
		t.Fatal("restored consumer note missing")
	}
	if note, err := f.notes.Read(ctx, session.Principal, noteID); err != nil || note.Text != fixtureNoteText {
		t.Fatal("restored authorized product behavior differs")
	}
	// Revoke the newly issued proof session explicitly. No restored/preexisting
	// browser session is ever activated by this fixture; HTTP ingress stays closed.
	if err := f.identity.RevokeAll(ctx, fixtureAdministrator, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.identity.Authenticate(ctx, session.Token); !errors.Is(err, identity.ErrAuthentication) {
		t.Fatal("restore proof session survived revocation")
	}
	fixtureCounts(t, f.pool, 1, 6, 0)
	if err := f.app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
