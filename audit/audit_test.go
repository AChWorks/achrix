// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

func TestDatabasePreflightAndProfile(t *testing.T) {
	for _, dsn := range []string{"", strings.Repeat("x", 4097), "host=remote.example dbname=fixture sslmode=disable", "host=remote.example dbname=fixture sslmode=require", "host=remote.example dbname=fixture sslmode=verify-ca", "host=127.0.0.1,remote.example dbname=fixture sslmode=disable", "host=localhost,remote.example dbname=fixture sslmode=prefer"} {
		if _, err := NewPostgres(dsn, Config{}, nil); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("unsafe configuration admitted: %v", err)
		}
	}
	for _, dsn := range []string{"host=127.0.0.1 user=fixture dbname=fixture sslmode=disable", "host=/tmp user=fixture dbname=fixture sslmode=disable", "host=remote.example user=fixture dbname=fixture sslmode=verify-full", "host=127.0.0.1,remote.example user=fixture dbname=fixture sslmode=verify-full"} {
		m, err := NewPostgres(dsn, Config{}, nil)
		if err != nil {
			t.Fatalf("defensive profile preflight failed: %v", err)
		}
		c := m.dbConfig
		if c.MaxConns != 4 || c.MinConns != 0 || c.MinIdleConns != 0 || c.HealthCheckPeriod != time.Minute || c.MaxConnLifetime != time.Hour || c.MaxConnLifetimeJitter != 0 || c.MaxConnIdleTime != time.Minute || c.PingTimeout != time.Second || c.ConnConfig.ConnectTimeout != time.Second {
			t.Fatal("connection source overrode owned pool bounds")
		}
		if err = (&Service{module: m}).CheckDatabase(dsn); err != nil {
			t.Fatal(err)
		}
	}
	m, err := NewPostgres("host=127.0.0.1 port=5432 user=fixture password=fixture-secret dbname=fixture sslmode=disable", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{module: m}
	if err = s.CheckDatabase("postgres://fixture:fixture-secret@127.0.0.1:5432/fixture?sslmode=disable"); err != nil {
		t.Fatalf("equivalent normalized profile rejected: %v", err)
	}
	for _, dsn := range []string{"host=127.0.0.1 port=5432 user=fixture password=other dbname=fixture sslmode=disable", "host=127.0.0.1 port=5432 user=fixture password=fixture-secret dbname=other sslmode=disable", "host=127.0.0.1,localhost port=5432 user=fixture password=fixture-secret dbname=fixture sslmode=disable"} {
		if err = s.CheckDatabase(dsn); !errors.Is(err, ErrConfiguration) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("mismatched profile did not fail safely: %v", err)
		}
	}
}

func TestCursorScopeAndBounds(t *testing.T) {
	c := encodeCursor("TARGET", 42)
	seq, err := decodeCursor("TARGET", c)
	if err != nil || seq != 42 {
		t.Fatalf("round trip: %d %v", seq, err)
	}
	b, _ := base64.RawURLEncoding.DecodeString(c)
	b[0] = 2
	for _, v := range []string{strings.Repeat("x", 10000), "!", c + "=", encodeCursor("OTHER", 42), encodeCursor("TARGET", 0), encodeCursor("TARGET", -1), base64.RawURLEncoding.EncodeToString(b)} {
		if _, err = decodeCursor("TARGET", v); !errors.Is(err, ErrInput) {
			t.Fatal("invalid or cross-target cursor admitted")
		}
	}
	if seq, err = decodeCursor("TARGET", ""); err != nil || seq != 0 {
		t.Fatal("empty cursor failed")
	}
}

func TestMetadataBounds(t *testing.T) {
	for _, v := range []string{"", strings.Repeat("A", 129), "display name", "<script>", "x\ny", "\x00", "شناسه"} {
		if validReference(v) {
			t.Fatal("unbounded/display-text reference admitted")
		}
	}
	for _, v := range []string{"OPERATOR", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "achrix.identity.public", "operator:fixture"} {
		if !validReference(v) {
			t.Fatal("bounded opaque reference rejected")
		}
	}
	for _, v := range []string{"", strings.Repeat("a", 97), "UPPER", "a..b", "a/b", "a\nb"} {
		if validName(v) {
			t.Fatal("invalid action/authority admitted")
		}
	}
	if !validName("identity.account.set-enabled") {
		t.Fatal("domain classification rejected")
	}
}

type contractModule struct{}

func (contractModule) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "fixture.audit", Version: "test", Provides: []achrix.Capability{{ID: Append, Version: 1}, {ID: Query, Version: 1}, {ID: Export, Version: 1}}}
}
func (contractModule) Start(context.Context) error { return nil }
func (contractModule) Ready(context.Context) error { return nil }
func (contractModule) Stop(context.Context) error  { return nil }

func TestDenialAndInvalidInputDoNotReachStorage(t *testing.T) {
	var calls int
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, achrix.PolicyFunc(func(ctx context.Context, p achrix.Principal, c, r string) error { calls++; return achrix.ErrDenied }), contractModule{})
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	m, err := NewPostgres("host=127.0.0.1 user=fixture dbname=fixture sslmode=disable", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(app, m)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e := Event{Action: "identity.account.create", Target: "ACCOUNT", Authority: "achrix.identity.account-create", Outcome: "succeeded"}
	for i := 0; i < 2000; i++ {
		if _, err = s.Prepare(ctx, "operator", e); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
		if _, err = s.Query(ctx, "operator", "ACCOUNT", "", 1); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
		if _, err = s.Export(ctx, "operator", "ACCOUNT", "", 1); !errors.Is(err, achrix.ErrDenied) {
			t.Fatal(err)
		}
	}
	before := calls
	e.Target = strings.Repeat("x", 10000)
	if _, err = s.Prepare(ctx, "operator", e); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err = s.Query(ctx, "operator", "ACCOUNT", "", 101); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err = s.Export(ctx, "operator", "ACCOUNT", strings.Repeat("x", 10000), 1); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if calls != before || m.FailureCount() != 0 {
		t.Fatal("invalid/denied traffic reached policy/storage diagnostics")
	}
	if err = AppendInTx(ctx, nil, Prepared{}); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

func TestDiagnosticsAreBoundedAndSecretFree(t *testing.T) {
	var log bytes.Buffer
	m, err := NewPostgres("host=127.0.0.1 user=fixture dbname=fixture sslmode=disable", Config{}, slog.New(slog.NewJSONHandler(&log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		if err = m.failure(context.Background(), "query", errors.New("password=secret request payload")); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if m.FailureCount() != 10000 {
		t.Fatal("fixed failure counter incorrect")
	}
	if strings.Count(log.String(), "\n") > 2 || strings.Contains(log.String(), "secret") || strings.Contains(log.String(), "payload") {
		t.Fatal("unbounded/sensitive diagnostics")
	}
	before := m.FailureCount()
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, ErrLimited, ErrConflict, ErrInput} {
		_ = m.failure(context.Background(), "query", err)
	}
	if before != m.FailureCount() {
		t.Fatal("expected input/capacity traffic became operational evidence")
	}
}
