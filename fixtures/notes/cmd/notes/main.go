// SPDX-License-Identifier: MPL-2.0
// Command notes is a local proving consumer, not a supported production product.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/config"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/infrastructure"
	"example.com/achrix-notes/internal/presentation"
	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5/pgxpool"
)

var buildIdentity = "development"

func main() { os.Exit(run()) }
func run() int {
	logger := diagnosis.New(os.Stderr)
	mode := flag.String("mode", "serve", "serve, migrate, or identity")
	flag.Parse()
	if *mode == "identity" {
		build := buildIdentity
		if build == "development" {
			if b, ok := debug.ReadBuildInfo(); ok {
				for _, s := range b.Settings {
					if s.Key == "vcs.revision" {
						build = s.Value
					}
				}
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"foundation": achrix.Version(), "module": infrastructure.ModuleVersion, "build": build, "migrations": map[string]string{infrastructure.Migrations()[0].ID: infrastructure.Digest(infrastructure.Migrations()[0])}})
		return 0
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logger.Error("configuration rejected", "component", "notes.consumer", "reason", "invalid_config")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if *mode == "migrate" {
		defer cancel()
		poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
		if err != nil {
			return 1
		}
		poolConfig.MaxConns = 2
		poolConfig.ConnConfig.ConnectTimeout = time.Second
		p, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			logger.Error("migration failed", "component", "notes.migrations", "reason", "connect_failed")
			return 1
		}
		defer p.Close()
		var version int
		if err := p.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil || version < 180000 || version >= 190000 {
			logger.Error("migration rejected", "component", "notes.migrations", "reason", "unsupported_database")
			return 1
		}
		if err := infrastructure.Migrate(ctx, p, infrastructure.Migrations()); err != nil {
			logger.Error("migration failed", "component", "notes.migrations", "reason", "migration_failed")
			return 1
		}
		return 0
	}
	if *mode != "serve" {
		cancel()
		logger.Error("unknown mode", "component", "notes.consumer")
		return 1
	}
	store, err := infrastructure.New(cfg.DSN, logger)
	if err != nil {
		cancel()
		return 1
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: 3 * time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), store)
	if err != nil {
		cancel()
		return 1
	}
	if err := app.Start(ctx); err != nil {
		cancel()
		logger.Error("startup failed", "component", "notes.consumer", "reason", "module_failed")
		return 1
	}
	cancel()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = app.Shutdown(ctx)
	}()
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logger.Error("listen failed", "component", "notes.consumer", "reason", "bind_failed")
		return 1
	}
	service := application.New(app, store, logger)
	server := &http.Server{Handler: presentation.New(app, service, cfg.WriterToken, cfg.ReaderToken, logger), ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	stop, cancelSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelSignal()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-stop.Done():
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP stopped", "component", "notes.http", "reason", "serve_failed")
			return 1
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		logger.Warn("HTTP shutdown deadline", "component", "notes.http")
	}
	return 0
}
