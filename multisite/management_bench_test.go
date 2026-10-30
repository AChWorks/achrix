// SPDX-License-Identifier: MPL-2.0
package multisite_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/multisite"
)

// Keep this benchmark self-contained and on the public API so the same file can
// measure the previous implementation. Construction, startup, cursor preparation
// and shutdown are outside timed calls. Inventory's own one-second budget,
// authorization, permitted lookahead and result copying remain measured.
func BenchmarkInventory(b *testing.B) {
	for _, size := range []int{100, 10_000, 100_000} {
		b.Run(fmt.Sprintf("sites=%d", size), func(b *testing.B) {
			config := multisite.Config{Sites: make([]multisite.Site, size)}
			ids := make([]multisite.SiteID, size)
			for i := range ids {
				ids[i] = multisite.SiteID(fmt.Sprintf("site-%06d", i))
				config.Sites[i] = multisite.Site{ID: ids[i], Authorities: []string{string(ids[i]) + ".example"}}
			}
			late := inventoryBenchmarkCursor(b, ids[size-21])
			final := inventoryBenchmarkCursor(b, ids[size-11])
			exhausted := inventoryBenchmarkCursor(b, ids[size-1])
			var simulatedDigest atomic.Uint64
			service := inventoryBenchmarkService(b, config, achrix.PolicyFunc(func(ctx context.Context, actor achrix.Principal, capability, target string) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if capability != multisite.InventoryRead {
					return achrix.ErrDenied
				}
				if target == multisite.InventoryTarget {
					return nil
				}
				switch actor {
				case "cheap":
					return nil
				case "sparse":
					if strings.HasSuffix(target, "00") {
						return nil
					}
				case "denied-suffix":
					if target == string(ids[0]) {
						return nil
					}
				case "simulated-sha256-64":
					// A bounded synthetic CPU cost: exactly 64 SHA-256 rounds and
					// one atomic sink per exact-site check, not a production estimate.
					var digest [sha256.Size]byte
					copy(digest[:], target)
					for range 64 {
						digest = sha256.Sum256(digest[:])
					}
					simulatedDigest.Store(binary.LittleEndian.Uint64(digest[:8]))
					return nil
				}
				return achrix.ErrDenied
			}))
			type scenario struct {
				name   string
				actor  achrix.Principal
				cursor string
				limit  int
				want   []multisite.SiteID
				next   bool
			}
			cases := []scenario{
				{"cheap/first", "cheap", "", 10, ids[:10], true},
				{"cheap/late", "cheap", late, 10, ids[size-20 : size-10], true},
				{"cheap/final", "cheap", final, 10, ids[size-10:], false},
				{"cheap/exhausted", "cheap", exhausted, 10, nil, false},
			}
			// Sparse permission and a denied tail measure the work that seeking
			// cannot remove. Keep the matrix bounded instead of crossing every
			// policy cost, page position, size and page limit.
			if size >= 10_000 {
				cursor := inventoryBenchmarkCursor(b, ids[size-1101])
				want := make([]multisite.SiteID, 10)
				for i := range want {
					want[i] = ids[size-1100+i*100]
				}
				cases = append(cases, scenario{"sparse_1_in_100/late", "sparse", cursor, 10, want, true})
			}
			if size == 100_000 {
				cases = append(cases, scenario{"denied_suffix/first", "denied-suffix", "", 1, ids[:1], false})
			}
			if size == 10_000 {
				cases = append(cases, scenario{"simulated_sha256_64/late", "simulated-sha256-64", late, 10, ids[size-20 : size-10], true})
			}
			for _, tc := range cases {
				b.Run(tc.name, func(b *testing.B) {
					b.StopTimer()
					check := func(page multisite.InventoryPage, err error) {
						b.Helper()
						if err != nil || len(page.Sites) != len(tc.want) || (page.NextCursor != "") != tc.next {
							b.Fatalf("inventory shape: sites=%d next=%t err=%v", len(page.Sites), page.NextCursor != "", err)
						}
						for i, want := range tc.want {
							if page.Sites[i].ID != want {
								b.Fatalf("inventory[%d]=%s, want %s", i, page.Sites[i].ID, want)
							}
						}
					}
					ctx := context.Background()
					page, err := service.Inventory(ctx, tc.actor, tc.cursor, tc.limit)
					check(page, err)
					b.ReportAllocs()
					b.ResetTimer()
					b.StartTimer()
					for i := 0; i < b.N; i++ {
						page, err = service.Inventory(ctx, tc.actor, tc.cursor, tc.limit)
						if err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					check(page, err)
				})
			}
		})
	}
}

func inventoryBenchmarkService(b *testing.B, config multisite.Config, policy achrix.Policy) *multisite.Service {
	b.Helper()
	module, err := multisite.NewManaged(config, multisite.ManagementConfig{})
	if err != nil {
		b.Fatal(err)
	}
	app, err := achrix.New(achrix.Config{
		StartupTimeout: time.Second, ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, policy, module)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			b.Error(err)
		}
	})
	service, err := multisite.NewService(app, module)
	if err != nil {
		b.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	return service
}

func inventoryBenchmarkCursor(b *testing.B, after multisite.SiteID) string {
	b.Helper()
	// Obtain an opaque cursor through an authorized public operation. A small
	// temporary inventory supplies lookahead even for the measured inventory's
	// final ID; cursors are ordering inputs, not grants or inventory snapshots.
	service := inventoryBenchmarkService(b, multisite.Config{Sites: []multisite.Site{
		{ID: after, Authorities: []string{"cursor.example"}},
		{ID: "zz-cursor-lookahead", Authorities: []string{"lookahead.example"}},
	}}, achrix.PolicyFunc(func(ctx context.Context, _ achrix.Principal, _, _ string) error { return ctx.Err() }))
	page, err := service.Inventory(context.Background(), "cursor-setup", "", 1)
	if err != nil || len(page.Sites) != 1 || page.Sites[0].ID != after || page.NextCursor == "" {
		b.Fatalf("cursor setup: %+v %v", page, err)
	}
	return page.NextCursor
}
