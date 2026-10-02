// SPDX-License-Identifier: MPL-2.0
package achrix_test

import (
	"context"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
)

func TestOfficialComponentSourceIdentity(t *testing.T) {
	// Construction/composition acquires no database resources. The standalone
	// consumer build proves normal tagged/pseudo-version dependency metadata.
	const dsn = "host=/tmp user=fixture dbname=fixture sslmode=disable"
	auditModule, err := audit.NewPostgres(dsn, audit.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	identityModule, err := identity.NewPostgres(dsn, identity.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		return achrix.ErrDenied
	}), identityModule, auditModule)
	if err != nil {
		t.Fatal(err)
	}
	version := achrix.Version()
	if version == "" || version == "0.2.0-development" {
		t.Fatalf("generic source-line label used as build identity: %q", version)
	}
	components := app.Components()
	if len(components) != 2 {
		t.Fatal("official component snapshot incomplete")
	}
	for _, d := range components {
		if d.Version != version {
			t.Errorf("%s: implementation version %q, Foundation build %q", d.ID, d.Version, version)
		}
		for _, c := range d.Provides {
			if c.Version != 1 {
				t.Errorf("%s: capability ABI changed: %+v", d.ID, c)
			}
		}
		for _, c := range d.Requires {
			want := uint32(1)
			if c.ID == "achrix.authorization" {
				want = 2
			}
			if c.Version != want {
				t.Errorf("%s: required capability ABI changed: %+v", d.ID, c)
			}
		}
	}
}
