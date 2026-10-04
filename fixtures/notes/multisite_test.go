// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/multisite"
)

// This product-local participant explicitly selects a single-site identity when
// the optional resolver is omitted. It owns no site stores, account grants or TLS.
type multisiteConsumer struct{ resolver *multisite.Service }

func (*multisiteConsumer) Descriptor() achrix.Descriptor {
	return achrix.Descriptor{ID: "example.notes.sites", Version: "test", Optional: []achrix.Capability{{ID: multisite.Resolve, Version: 1}}}
}
func (*multisiteConsumer) Start(ctx context.Context) error { return ctx.Err() }
func (*multisiteConsumer) Ready(ctx context.Context) error { return ctx.Err() }
func (*multisiteConsumer) Stop(ctx context.Context) error  { return ctx.Err() }
func (c *multisiteConsumer) site(ctx context.Context, actor achrix.Principal, authority string) (multisite.SiteID, error) {
	if c.resolver == nil {
		return "single-site", nil
	}
	return c.resolver.Resolve(ctx, actor, authority)
}

// The separately pinned Go module proves the public boundary without copying
// Foundation source. This is resolver composition proof, not product isolation.
func TestPublicMultiSiteComposition(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "omitted"
		if enabled {
			name = "two-sites"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			consumer := &multisiteConsumer{}
			modules := []achrix.Module{consumer}
			var resolver *multisite.Module
			if enabled {
				var err error
				resolver, err = multisite.New(multisite.Config{Sites: []multisite.Site{
					{ID: "site-a", Authorities: []string{"shared.example"}},
					{ID: "site-b", Authorities: []string{"shared.example:443"}},
				}})
				if err != nil {
					t.Fatal(err)
				}
				// The optional dependency orders this deliberately later provider
				// before its consumer; product code supplies the typed collaborator.
				modules = append(modules, resolver)
			}
			var authorized []string
			policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, authority string) error {
				if actor != "product-router" || capability != multisite.Resolve {
					return achrix.ErrDenied
				}
				switch authority {
				case "shared.example", "shared.example:443", "shared.example:8443":
					authorized = append(authorized, authority)
					return nil
				default:
					return achrix.ErrDenied
				}
			})
			app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, policy, modules...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
				defer cleanupCancel()
				if err := app.Shutdown(cleanup); err != nil {
					t.Error(err)
				}
			})
			if enabled {
				consumer.resolver, err = multisite.NewService(app, resolver)
				if err != nil {
					t.Fatal(err)
				}
				if app.Components()[0].ID != "achrix.multisite" {
					t.Fatal("optional provider did not start first")
				}
			}
			if err := app.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := app.Ready(ctx); err != nil {
				t.Fatal(err)
			}
			id, err := consumer.site(ctx, "product-router", "shared.example")
			want := multisite.SiteID("single-site")
			if enabled {
				want = "site-a"
			}
			if err != nil || id != want {
				t.Fatalf("first site: %q, %v", id, err)
			}
			if !enabled {
				if len(app.Components()) != 1 || len(authorized) != 0 {
					t.Fatal("omitted resolver unexpectedly participated")
				}
				return
			}
			if id, err := consumer.site(ctx, "product-router", "shared.example:443"); err != nil || id != "site-b" {
				t.Fatalf("explicit port: %q, %v", id, err)
			}
			if id, err := consumer.site(ctx, "product-router", "shared.example:8443"); id != "" || !errors.Is(err, multisite.ErrNotFound) {
				t.Fatalf("port fallback: %q, %v", id, err)
			}
			if len(authorized) != 3 || authorized[0] != "shared.example" || authorized[1] != "shared.example:443" || authorized[2] != "shared.example:8443" {
				t.Fatalf("authorization resources: %v", authorized)
			}
			if id, err := consumer.site(ctx, "ungranted-principal", "shared.example:443"); id != "" || !errors.Is(err, achrix.ErrDenied) {
				t.Fatalf("denied resolution: %q, %v", id, err)
			}
		})
	}
}
