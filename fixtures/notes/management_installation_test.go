// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/domain"
	"example.com/achrix-notes/internal/infrastructure"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
	"github.com/AChWorks/achrix/installation"
	"github.com/AChWorks/achrix/multisite"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This consumer imports only public Foundation contracts. These databases are
// created solely by validate.sh inside its private Unix-socket test cluster.
func managementProofPool(t *testing.T, variable, database string) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv(variable)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || dsn == "" || cfg.ConnConfig.Database != database {
		t.Fatal("explicit isolated " + variable + " required")
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.ConnectTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("private proof database unavailable")
	}
	t.Cleanup(pool.Close)
	var actual string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != database {
		t.Fatal("private proof database identity mismatch")
	}
	if err := infrastructure.CheckEnvironment(ctx, pool); err != nil {
		t.Fatal("private proof database environment unsupported")
	}
	return pool, dsn
}

func proofStart(t *testing.T, policy achrix.Policy, modules ...achrix.Module) *achrix.Application {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second, Logger: logger}, policy, modules...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proofStop(t, app) })
	return app
}

func proofStop(t *testing.T, app *achrix.Application) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func proofDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// Real Migration and Identity bootstrap have distinct replay semantics. The
// product owns this private progress table and persists intent before effects;
// the shared installation package neither opens the DB nor executes a command.
func TestPublicInstallationReentry(t *testing.T) {
	pool, dsn := managementProofPool(t, "NOTES_INSTALL_DATABASE_URL", "achrix_install_consumer")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, "CREATE TABLE public.installation_fixture_progress(plan_id text NOT NULL, requirement text NOT NULL, state text NOT NULL, PRIMARY KEY(plan_id,requirement))"); err != nil {
		t.Fatal(err)
	}
	profile := installation.Profile{
		Binding: installation.Binding{
			ProfileID: "notes.local-postgres", OperationID: "install-" + rand.Text(),
			CandidateSHA256:     proofDigest("public-consumer-candidate"),
			PreconditionsSHA256: proofDigest("isolated-local-postgresql-profile"),
		},
		Requirements: []installation.Requirement{
			{ID: "identity.schema", Required: true},
			{ID: "identity.administrator", Required: true},
			{ID: "host.root"},
		},
	}
	support := []installation.Support{
		{RequirementID: "identity.schema", AdapterID: "notes.migrate", Authorized: true},
		{RequirementID: "identity.administrator", AdapterID: "notes.bootstrap", Authorized: true},
	}
	observation := func(schema, admin installation.ObservedState) []installation.Observation {
		return []installation.Observation{
			{RequirementID: "identity.schema", State: schema},
			{RequirementID: "identity.administrator", State: admin},
			{RequirementID: "host.root", State: installation.Missing},
		}
	}
	initial, err := installation.Assess(profile, observation(installation.Missing, installation.Missing), support, installation.Progress{})
	if err != nil || initial.Status() != installation.ChangesRequired || len(initial.Actions()) != 2 {
		t.Fatal("initial public installation plan", err)
	}
	record := func(requirement string, state installation.AttemptState) {
		t.Helper()
		if _, err := pool.Exec(ctx, "INSERT INTO public.installation_fixture_progress VALUES($1,$2,$3) ON CONFLICT(plan_id,requirement) DO UPDATE SET state=excluded.state", initial.ID(), requirement, string(state)); err != nil {
			t.Fatal(err)
		}
	}
	reload := func() installation.Progress {
		t.Helper()
		rows, err := pool.Query(ctx, "SELECT requirement,state FROM public.installation_fixture_progress WHERE plan_id=$1 ORDER BY requirement", initial.ID())
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		progress := installation.Progress{PlanID: initial.ID()}
		for rows.Next() {
			var attempt installation.Attempt
			if err := rows.Scan(&attempt.RequirementID, &attempt.State); err != nil {
				t.Fatal(err)
			}
			progress.Attempts = append(progress.Attempts, attempt)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return progress
	}
	record("identity.schema", installation.Attempted)
	for repeat := 0; repeat < 2; repeat++ {
		if err := audit.Migrate(ctx, dsn); err != nil {
			t.Fatal(err)
		}
		if err := identity.Migrate(ctx, dsn); err != nil {
			t.Fatal(err)
		}
	}
	record("identity.schema", installation.Verified)

	auditModule, err := audit.NewPostgres(dsn, audit.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	identityModule, err := identity.NewPostgres(dsn, identity.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const actor achrix.Principal = "installation-fixture"
	policy := achrix.PolicyFunc(func(_ context.Context, p achrix.Principal, capability, resource string) error {
		if p == actor && (capability == identity.AccountCreate || capability == identity.AccountLookup || capability == audit.Append) {
			return nil
		}
		return achrix.ErrDenied
	})
	app := proofStart(t, policy, auditModule, identityModule)
	accountability, err := audit.NewService(app, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	service, err := identity.NewService(app, identityModule, accountability)
	if err != nil {
		t.Fatal(err)
	}
	const login = "installation-admin"
	// Synthetic public fixture password, never deployment credentials.
	const password = "Synthetic installation administrator password 2026!"
	record("identity.administrator", installation.Attempted)
	account, err := service.CreateAccount(ctx, actor, login, password)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate losing that successful acknowledgement: reload the committed
	// attempt and pass inconclusive evidence, without invoking Create again.
	uncertain, err := initial.Recheck(profile, observation(installation.Satisfied, installation.Unknown), support, reload())
	if err != nil || uncertain.Status() != installation.ReconciliationRequired || len(uncertain.Actions()) != 0 {
		t.Fatal("unknown bootstrap acknowledgement became replayable", err)
	}
	found, err := service.LookupAccount(ctx, actor, login)
	if err != nil || found.ID != account.ID {
		t.Fatal("exact-login reconciliation failed", err)
	}
	record("identity.administrator", installation.Verified)
	ready, err := initial.Recheck(profile, observation(installation.Satisfied, installation.Satisfied), support, reload())
	if err != nil || ready.Status() != installation.Ready || len(ready.Actions()) != 0 {
		t.Fatal("verified installation did not converge", err)
	}
	var accounts, events int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM identity.accounts WHERE login=$1", login).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit.records WHERE target=$1", account.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || events != 1 {
		t.Fatal("bootstrap was duplicated", accounts, events)
	}
	stale := profile
	stale.Binding.CandidateSHA256 = proofDigest("different-candidate")
	if _, err := initial.Recheck(stale, observation(installation.Satisfied, installation.Satisfied), support, reload()); !errors.Is(err, installation.ErrStale) {
		t.Fatal("changed candidate accepted by old installation plan", err)
	}
}

func TestPublicManagedSitesRetainPostgresState(t *testing.T) {
	poolA, dsnA := managementProofPool(t, "NOTES_MANAGEMENT_A_DATABASE_URL", "achrix_test_management_a")
	poolB, dsnB := managementProofPool(t, "NOTES_MANAGEMENT_B_DATABASE_URL", "achrix_test_management_b")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, pool := range []*pgxpool.Pool{poolA, poolB} {
		if err := infrastructure.Migrate(ctx, pool, infrastructure.Migrations()); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := achrix.Descriptor{
		ID: "example.notes", Version: infrastructure.ModuleVersion,
		Provides: []achrix.Capability{{ID: application.Create, Version: 1}, {ID: application.Read, Version: 1}},
		Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 2}},
	}
	config := multisite.Config{Sites: []multisite.Site{
		{ID: "site-a", Authorities: []string{"a.example"}},
		{ID: "site-b", Authorities: []string{"b.example"}},
	}}
	settings := multisite.ManagementConfig{Available: []achrix.Descriptor{catalog}, Sites: []multisite.SiteSelection{
		{ID: "site-a", Modules: []string{"example.notes"}},
		{ID: "site-b"},
	}}
	controlPolicy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, resource string) error {
		if actor != "operator" {
			return achrix.ErrDenied
		}
		if capability == multisite.InventoryRead && (resource == "" || resource == "site-a" || resource == "site-b") {
			return nil
		}
		if capability == multisite.PlanActivation && resource == "site-a" {
			return nil
		}
		return achrix.ErrDenied
	})
	control := func(input multisite.ManagementConfig) (*achrix.Application, *multisite.Service) {
		module, err := multisite.NewManaged(config, input)
		if err != nil {
			t.Fatal(err)
		}
		app := proofStart(t, controlPolicy, module)
		service, err := multisite.NewService(app, module)
		if err != nil {
			t.Fatal(err)
		}
		return app, service
	}
	controlApp, management := control(settings)
	siteA, err := management.Site(ctx, "operator", "site-a")
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := management.Site(ctx, "operator", "site-b")
	if err != nil || len(siteB.Modules) != 0 {
		t.Fatal("inactive site's selection", err)
	}
	constructed := map[multisite.SiteID]int{}
	buildSite := func(selection multisite.SiteInfo) (*achrix.Application, *application.Service) {
		modules := []achrix.Module{}
		var store *infrastructure.Store
		for _, id := range selection.Modules {
			if id != catalog.ID {
				t.Fatal("unexpected selected product Module")
			}
			constructed[selection.ID]++
			var err error
			store, err = infrastructure.New(map[multisite.SiteID]string{"site-a": dsnA, "site-b": dsnB}[selection.ID], logger)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(store.Descriptor(), catalog) {
				t.Fatal("product catalogue drift")
			}
			modules = append(modules, store)
		}
		policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, resource string) error {
			if actor == achrix.Principal(string(selection.ID)+"-writer") && (capability == application.Create || capability == application.Read) {
				return nil
			}
			return achrix.ErrDenied
		})
		app := proofStart(t, policy, modules...)
		if store == nil {
			return app, nil
		}
		return app, application.New(app, store, logger)
	}
	appA, notes := buildSite(siteA)
	appB, absentB := buildSite(siteB)
	if absentB != nil || constructed["site-b"] != 0 {
		t.Fatal("inactive site B constructed its optional feature")
	}
	if err := appB.Authorize(ctx, "site-b-writer", application.Read, rand.Text()); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("inactive site B advertised the absent capability", err)
	}
	note, err := notes.Create(ctx, "site-a-writer", "یادداشت ماندگارِ سایت A")
	if err != nil {
		t.Fatal(err)
	}
	if constructed["site-a"] != 1 {
		t.Fatal("inactive site acquired a feature constructor")
	}
	var countB int
	if err := poolB.QueryRow(ctx, "SELECT count(*) FROM notes.entries").Scan(&countB); err != nil || countB != 0 {
		t.Fatal("site A state crossed into site B", err)
	}
	off, err := management.PlanActivation(ctx, "operator", multisite.ActivationRequest{SiteID: "site-a", ExpectedRevision: siteA.Revision})
	if err != nil {
		t.Fatal(err)
	}
	next, err := off.Replacement(config, settings)
	if err != nil {
		t.Fatal(err)
	}
	// The proposal has not changed the running site or control composition.
	if _, err := notes.Read(ctx, "site-a-writer", note.ID); err != nil {
		t.Fatal("plan mutated live state", err)
	}
	if _, err := management.PlanActivation(ctx, "stranger", multisite.ActivationRequest{SiteID: "site-a", ExpectedRevision: siteA.Revision}); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("ungranted actor planned activation", err)
	}
	proofStop(t, appA)
	proofStop(t, controlApp)
	offControl, offManagement := control(next)
	offSite, err := offManagement.Site(ctx, "operator", "site-a")
	if err != nil || offSite.Revision == siteA.Revision {
		t.Fatal("replacement revision did not advance", err)
	}
	inactive, absent := buildSite(offSite)
	if absent != nil || constructed["site-a"] != 1 {
		t.Fatal("inactive feature acquired resources")
	}
	if err := inactive.Authorize(ctx, "site-a-writer", application.Read, note.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("omitted feature retained executable authority", err)
	}
	var retained string
	if err := poolA.QueryRow(ctx, "SELECT body FROM notes.entries WHERE id=$1", note.ID).Scan(&retained); err != nil || retained != note.Text {
		t.Fatal("disable removed persisted state", err)
	}
	on, err := offManagement.PlanActivation(ctx, "operator", multisite.ActivationRequest{
		SiteID: "site-a", ExpectedRevision: offSite.Revision, Modules: []string{"example.notes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := on.Replacement(config, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.Replacement(config, enabled); !errors.Is(err, multisite.ErrConflict) {
		t.Fatal("old plan survived disable/re-enable ABA", err)
	}
	proofStop(t, inactive)
	proofStop(t, offControl)
	_, enabledManagement := control(enabled)
	enabledSite, err := enabledManagement.Site(ctx, "operator", "site-a")
	if err != nil {
		t.Fatal(err)
	}
	_, restored := buildSite(enabledSite)
	read, err := restored.Read(ctx, "site-a-writer", note.ID)
	if err != nil || read.Text != note.Text || read.ID != note.ID || constructed["site-a"] != 2 {
		t.Fatal("re-enabled public consumer lost retained state", err)
	}
	if _, err := restored.Read(ctx, "site-b-writer", note.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("site B principal obtained site A authority", err)
	}
	if _, err := restored.Read(ctx, "site-a-writer", rand.Text()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing owned row error changed", err)
	}
}
