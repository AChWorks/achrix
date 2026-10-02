// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIdentityConfigurationBounds(t *testing.T) {
	c, err := (Config{}).defaults()
	if err != nil || c.Password != DefaultPasswordPolicy() || c.HashConcurrency != 2 || c.SessionLifetime != 8*time.Hour || c.Now == nil {
		t.Fatal("explicit default configuration", err)
	}
	for _, bad := range []Config{
		{HashConcurrency: -1}, {HashConcurrency: 3},
		{SessionLifetime: time.Minute - time.Nanosecond}, {SessionLifetime: 24*time.Hour + time.Nanosecond},
		{Password: PasswordPolicy{Memory: 19 * 1024}},
	} {
		if _, err := bad.defaults(); !errors.Is(err, ErrConfiguration) {
			t.Fatal("unsafe configuration accepted")
		}
	}
	for _, lifetime := range []time.Duration{time.Minute, 24 * time.Hour} {
		clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.FixedZone("fixture", 3*3600))
		c, err := (Config{HashConcurrency: 1, SessionLifetime: lifetime, Now: func() time.Time { return clock }}).defaults()
		if err != nil {
			t.Fatal("supported boundary rejected", err)
		}
		m := &Module{config: c}
		if got := m.now(); !got.Equal(clock) || got.Location() != time.UTC {
			t.Fatal("trusted time source or UTC normalization lost")
		}
	}
}

func TestPostgresTransportValidatesEveryHost(t *testing.T) {
	for _, tt := range []struct {
		name, dsn string
		valid     bool
	}{
		{"unix development", "host=/tmp user=fixture dbname=fixture sslmode=disable", true},
		{"IPv4 development", "host=127.0.0.1 user=fixture dbname=fixture sslmode=disable", true},
		{"IPv6 development", "host=::1 user=fixture dbname=fixture sslmode=disable", true},
		{"localhost development", "host=localhost user=fixture dbname=fixture sslmode=disable", true},
		{"remote verified", "host=db.example.test user=fixture dbname=fixture sslmode=verify-full", true},
		{"mixed verified", "host=127.0.0.1,db.example.test user=fixture dbname=fixture sslmode=verify-full", true},
		{"remote and local verified", "host=db.example.test,127.0.0.1 user=fixture dbname=fixture sslmode=verify-full", true},
		{"remote plaintext", "host=db.example.test user=fixture dbname=fixture sslmode=disable", false},
		{"remote unverified require", "host=db.example.test user=fixture dbname=fixture sslmode=require", false},
		{"remote unverified prefer", "host=db.example.test user=fixture dbname=fixture sslmode=prefer", false},
		{"remote verify CA without host", "host=db.example.test user=fixture dbname=fixture sslmode=verify-ca", false},
		{"local primary remote plaintext fallback", "host=127.0.0.1,db.example.test user=fixture dbname=fixture sslmode=disable", false},
		{"local primary remote unverified fallback", "host=127.0.0.1,db.example.test user=fixture dbname=fixture sslmode=require", false},
		{"remote primary local plaintext", "host=db.example.test,127.0.0.1 user=fixture dbname=fixture sslmode=disable", false},
		{"localhost-looking suffix", "host=localhost.attacker.example.test user=fixture dbname=fixture sslmode=disable", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := databaseConfig(tt.dsn)
			if (err == nil) != tt.valid || (err != nil && !errors.Is(err, ErrConfiguration)) {
				t.Fatal("transport preflight result", err)
			}
		})
	}
}

func TestPostgresConfigurationHasFixedResourceBounds(t *testing.T) {
	c, err := databaseConfig("host=127.0.0.1 user=fixture dbname=fixture sslmode=disable connect_timeout=120 pool_max_conns=128 pool_min_conns=8 pool_min_idle_conns=8 pool_health_check_period=0s pool_max_conn_lifetime_jitter=30m pool_ping_timeout=1m pool_max_conn_idle_time=12h pool_max_conn_lifetime=48h")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxConns != 4 || c.MinConns != 0 || c.MinIdleConns != 0 || c.ConnConfig.ConnectTimeout != time.Second || c.MaxConnIdleTime != time.Minute || c.MaxConnLifetime != time.Hour || c.MaxConnLifetimeJitter != 0 || c.HealthCheckPeriod != time.Minute || c.PingTimeout != time.Second {
		t.Fatal("DSN resource options bypass fixed pool bounds")
	}
	for _, dsn := range []string{"", strings.Repeat("x", 4097), "postgres://fixture:fixture-secret@127.0.0.1:99999/fixture", "host=127.0.0.1 sslmode=invalid", "host=127.0.0.1 pool_max_conns=not-a-number", "host=127.0.0.1 pool_health_check_period=not-a-duration"} {
		_, err := databaseConfig(dsn)
		if !errors.Is(err, ErrConfiguration) || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("unsafe DSN failure result")
		}
	}
}

func TestPasswordUnicodeAndDirectInputPreflight(t *testing.T) {
	for _, tt := range []struct {
		password       string
		setting, valid bool
	}{
		{strings.Repeat("🙂", 15), true, true},
		{strings.Repeat("🙂", 14), true, false},
		{strings.Repeat("🙂", 14), false, true},
		{strings.Repeat("🙂", 256), true, true},
		{strings.Repeat("🙂", 257), false, false},
		{strings.Repeat("x", 256), true, true},
		{strings.Repeat("x", 257), false, false},
		{strings.Repeat(" ", 15), true, true},
		{"", false, false},
		{string([]byte{0xff}), false, false},
		{strings.Repeat("x", 1<<20), false, false},
	} {
		if validPassword(tt.password, tt.setting) != tt.valid {
			t.Fatal("password Unicode/length boundary changed")
		}
	}
	// Nil collaborators prove cheap invalid-input rejection before any policy,
	// hash or database work, including direct callers outside the HTTP body cap.
	s := &Service{}
	oversized := strings.Repeat("x", 1<<20)
	if _, err := s.CreateAccount(context.Background(), "actor", oversized, "a long test password"); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized login admitted to account work")
	}
	if _, err := s.Login(context.Background(), oversized, "a long test password", ""); !errors.Is(err, ErrAuthentication) {
		t.Fatal("oversized login admitted to authentication work")
	}
	if _, err := s.Login(context.Background(), "valid-handle", oversized, ""); !errors.Is(err, ErrAuthentication) {
		t.Fatal("oversized password admitted to authentication work")
	}
	if err := s.SetPassword(context.Background(), "actor", oversized, 1, "a long test password"); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized account reference reached authorization")
	}
}

func TestSessionTokenCanonicalBounds(t *testing.T) {
	for _, value := range []string{strings.Repeat("A", 42) + "B", strings.Repeat("A", 43) + "=", strings.Repeat("/", 43), strings.Repeat("x", 1<<20)} {
		if _, err := tokenHash(value); !errors.Is(err, ErrAuthentication) {
			t.Fatal("noncanonical or oversized session token accepted")
		}
	}
	if h, err := tokenHash(strings.Repeat("A", 43)); err != nil || len(h) != 32 {
		t.Fatal("canonical token rejected")
	}
}

func TestIdentityOwnedLeaseBoundsCancellationAndDrain(t *testing.T) {
	// pgx's real pool has zero minimum/idle resources and is never queried. This
	// exercises lifecycle ownership without a fake zero-value Pool.Close panic
	// or a database connection. PostgreSQL semantics have separate integration proof.
	c, err := databaseConfig("host=127.0.0.1 port=1 user=fixture dbname=fixture sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	drained := make(chan struct{})
	close(drained)
	m := &Module{pool: pool, state: "ready", work: work, cancel: cancelWork, drained: drained}
	if _, _, _, err := m.acquire(context.Background()); !errors.Is(err, ErrConfiguration) {
		t.Fatal("unbounded owned work admitted")
	}
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var finishes []func()
	defer func() {
		for _, finish := range finishes {
			finish()
		}
	}()
	var lease context.Context
	for range 16 {
		ctx, got, finish, err := m.acquire(parent)
		if err != nil || got != pool {
			t.Fatal("owned work admitted incorrectly", err)
		}
		lease = ctx
		finishes = append(finishes, finish)
	}
	if _, _, _, err := m.acquire(parent); !errors.Is(err, ErrLimited) {
		t.Fatal("owned work exceeded fixed admission")
	}
	if m.active != 16 {
		t.Fatal("owned work tracking lost lease")
	}
	stop, cancelStop := context.WithCancel(parent)
	cancelStop()
	if err := m.Stop(stop); !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown discarded an undrained lease")
	}
	select {
	case <-lease.Done():
	case <-parent.Done():
		t.Fatal("shutdown failed to cancel owned service work")
	}
	if m.pool != pool || m.state != "stopping" || m.active != 16 {
		t.Fatal("pool closed before owned work drained")
	}
	if _, _, _, err := m.acquire(parent); !errors.Is(err, ErrUnavailable) {
		t.Fatal("shutdown reopened owned admission")
	}
	for _, finish := range finishes {
		finish()
	}
	finishes = nil
	if err := m.Stop(parent); err != nil {
		t.Fatal("drained shutdown could not complete", err)
	}
	if m.pool != nil || m.state != "stopped" || m.active != 0 {
		t.Fatal("shutdown did not release owned resources")
	}
	if err := m.Stop(parent); err != nil {
		t.Fatal("completed shutdown was not idempotent", err)
	}
}
