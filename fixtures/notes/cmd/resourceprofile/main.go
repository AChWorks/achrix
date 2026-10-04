// SPDX-License-Identifier: MPL-2.0
// Opt-in raised-profile Foundation composition observation for issue76.
// Uses only public services and a normally resolved Foundation dependency.
// Derived from the accepted default observation in issuecomment-5976870760.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
	"github.com/AChWorks/achrix/media"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type composition struct {
	app      *achrix.Application
	identity *identity.Service
	audit    *audit.Service
	media    *media.Service
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 145*time.Second)
	defer cancel()
	root := os.Getenv("ACHRIX76_ROOT")
	if !filepath.IsAbs(root) || os.Getenv("ACHRIX76_PGUSER") == "" {
		panic("absolute owned root and explicit task PostgreSQL user required")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		panic("owned root must be an existing private 0700 directory")
	}
	dsn := func(db string) string {
		return fmt.Sprintf("host=%s port=5432 user=%s dbname=%s sslmode=disable application_name=achrix76-measure", filepath.Join(root, "socket"), os.Getenv("ACHRIX76_PGUSER"), db)
	}
	observer, err := pgx.Connect(ctx, dsn("postgres")+" application_name=achrix76-observer")
	must(err)
	defer observer.Close(context.Background())
	emit := func(phase string, elapsed time.Duration) int {
		rows, err := observer.Query(ctx, "SELECT datname,count(*) FROM pg_stat_activity WHERE application_name='achrix76-measure' GROUP BY datname ORDER BY datname")
		must(err)
		counts := map[string]int{}
		total := 0
		for rows.Next() {
			var db string
			var n int
			must(rows.Scan(&db, &n))
			counts[db] = n
			total += n
		}
		must(rows.Err())
		rows.Close()
		must(json.NewEncoder(os.Stdout).Encode(map[string]any{"phase": phase, "elapsed_ms": elapsed.Milliseconds(), "connections": counts, "total": total}))
		return total
	}
	pids := func() []int {
		rows, err := observer.Query(ctx, "SELECT pid FROM pg_stat_activity WHERE application_name='achrix76-measure' ORDER BY pid")
		must(err)
		defer rows.Close()
		var result []int
		for rows.Next() {
			var pid int
			must(rows.Scan(&pid))
			result = append(result, pid)
		}
		must(rows.Err())
		return result
	}
	emit("before-migrations", 0)
	for _, db := range []string{"achrix76_control", "achrix76_site_a", "achrix76_site_b"} {
		for _, migrate := range []func(context.Context, string) error{audit.Migrate, identity.Migrate} {
			must(migrate(ctx, dsn(db)))
		}
		if db != "achrix76_control" {
			must(media.Migrate(ctx, dsn(db)))
		}
	}
	emit("after-explicit-migrations", 0)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	all := []composition{}
	constructorStart := time.Now()
	for _, db := range []string{"achrix76_control", "achrix76_site_a", "achrix76_site_b"} {
		a, e := audit.NewPostgres(dsn(db), audit.Config{MaxConns: 6, MaxOperations: 24}, logger)
		must(e)
		i, e := identity.NewPostgres(dsn(db), identity.Config{MaxConns: 6, MaxOperations: 24}, logger)
		must(e)
		modules := []achrix.Module{i, a}
		var m *media.Module
		if db != "achrix76_control" {
			m, e = media.NewPostgres(dsn(db), media.Config{MaxConns: 6, MaxOperations: 8, StorageRoot: filepath.Join(root, db+"-media")}, logger)
			must(e)
			modules = append(modules, m)
		}
		policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, target string) error {
			if actor == "measure" && ((capability == identity.AccountLookup && target == "unused") || (capability == audit.Query && target == "measure") || (capability == media.List && target == media.LibraryTarget)) {
				return nil
			}
			return achrix.ErrDenied
		})
		app, e := achrix.New(achrix.Config{StartupTimeout: 10 * time.Second, ShutdownTimeout: 5 * time.Second, Logger: logger}, policy, modules...)
		must(e)
		as, e := audit.NewService(app, a)
		must(e)
		is, e := identity.NewService(app, i, as)
		must(e)
		c := composition{app: app, identity: is, audit: as}
		if m != nil {
			c.media, e = media.NewService(app, m)
			must(e)
		}
		all = append(all, c)
	}
	emit("constructed-eight-pools-not-started", time.Since(constructorStart))
	defer func() {
		for _, c := range all {
			must(c.app.Shutdown(context.Background()))
		}
	}()
	start := time.Now()
	for _, c := range all {
		must(c.app.Start(ctx))
	}
	emit("started", time.Since(start))
	time.Sleep(500 * time.Millisecond)
	emit("idle-after-start", 500*time.Millisecond)
	demandStart := time.Now()
	var wg sync.WaitGroup
	results := make(chan error, 32)
	for _, c := range all {
		for range 4 {
			wg.Go(func() {
				_, e := c.identity.LookupAccount(ctx, "measure", "unused")
				if errors.Is(e, identity.ErrNotFound) {
					e = nil
				}
				results <- e
			})
			wg.Go(func() { _, e := c.audit.Query(ctx, "measure", "measure", "", 1); results <- e })
			if c.media != nil {
				wg.Go(func() { _, e := c.media.List(ctx, "measure", "", 1); results <- e })
			}
		}
	}
	wg.Wait()
	close(results)
	for e := range results {
		must(e)
	}
	emit("after-one-32-request-public-read-burst", time.Since(demandStart))
	beforeReuse := pids()
	// A bounded second pass proves reuse of the same native sessions, without
	// trying to fill the higher 48-connection configured aggregate maximum.
	for _, c := range all {
		_, err := c.identity.LookupAccount(ctx, "measure", "unused")
		if !errors.Is(err, identity.ErrNotFound) {
			must(err)
			panic("unexpected account")
		}
		_, err = c.audit.Query(ctx, "measure", "measure", "", 1)
		must(err)
		if c.media != nil {
			_, err = c.media.List(ctx, "measure", "", 1)
			must(err)
		}
	}
	if !slices.Equal(beforeReuse, pids()) {
		panic("native sessions changed during bounded reuse pass")
	}
	emit("after-eight-public-reads-session-reuse", 0)
	idleStart := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			panic(ctx.Err())
		case <-ticker.C:
			if emit("idle-native-reclamation-observation", time.Since(idleStart)) == 0 {
				goto reclaimed
			}
		}
	}
reclaimed:
	shutdownStart := time.Now()
	for _, c := range all {
		must(c.app.Shutdown(ctx))
	}
	emit("after-shutdown", time.Since(shutdownStart))
}
