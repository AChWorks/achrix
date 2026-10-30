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
func run() (result int) {
	logger := diagnosis.New(os.Stderr)
	defer diagnosis.Flush(logger)
	mode := flag.String("mode", "serve", "serve, migrate, or identity")
	flag.Parse()
	if *mode != "identity" && *mode != "migrate" && *mode != "serve" {
		logger.Error("unknown mode", "component", "notes.consumer", "operation", "configure", "phase", "mode", "next_action", "choose serve, migrate, or identity")
		return 1
	}

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
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"foundation": achrix.Version(), "module": infrastructure.ModuleVersion, "build": build, "migrations": map[string]string{infrastructure.Migrations()[0].ID: infrastructure.Digest(infrastructure.Migrations()[0])}}); err != nil {
			logger.Error("identity output failed", "component", "notes.consumer", "operation", "identity", "phase", "write_stdout", "reason", diagnosis.IOReason(err))
			return 1
		}
		return 0
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logger.Error("configuration rejected", "component", "notes.consumer", "operation", "configure", "phase", "validate", "reason", "invalid_config", "detail", err.Error(), "next_action", "correct the named runtime setting before restarting")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if *mode == "migrate" {
		defer cancel()
		poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
		if err != nil {
			logger.ErrorContext(ctx, "migration configuration rejected", "component", "notes.migrations", "operation", "migrate", "phase", "parse_dsn", "reason", "invalid_database_configuration")
			return 1
		}
		poolConfig.MaxConns = 2
		poolConfig.ConnConfig.ConnectTimeout = time.Second
		p, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			logger.ErrorContext(ctx, "migration failed", "component", "notes.migrations", "operation", "initialize", "reason", infrastructure.FailureReason(err))
			return 1
		}
		defer p.Close()
		if err := infrastructure.MigrateWithDiagnostics(ctx, p, infrastructure.Migrations(), logger, nil); err != nil {
			return 1
		}
		logger.Info("migration complete", "component", "notes.migrations", "operation", "migrate", "outcome", "committed")
		return 0
	}
	store, err := infrastructure.New(cfg.DSN, logger)
	if err != nil {
		cancel()
		logger.Error("store configuration rejected", "component", "notes.consumer", "operation", "configure", "phase", "postgres", "detail", err.Error())
		return 1
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: 3 * time.Second, ShutdownTimeout: 2 * time.Second, Logger: logger}, achrix.PolicyFunc(application.Policy), store)
	if err != nil {
		cancel()
		logger.Error("composition rejected", "component", "notes.consumer", "operation", "configure", "phase", "compose", "detail", err.Error())
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
		if err := app.Shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "application shutdown incomplete", "component", "notes.consumer", "operation", "shutdown", "phase", "application", "reason", infrastructure.FailureReason(err))
			result = 1
		}
	}()
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logger.Error("listen failed", "component", "notes.consumer", "operation", "serve", "phase", "listen", "reason", diagnosis.IOReason(err), "listen", cfg.Listen, "next_action", "check the configured loopback address and port availability")
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
			logger.Error("HTTP stopped", "component", "notes.http", "operation", "serve", "phase", "accept", "reason", diagnosis.IOReason(err))
			return 1
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("HTTP forced close failed", "component", "notes.http", "operation", "shutdown", "phase", "force_close", "reason", diagnosis.IOReason(closeErr))
		}
		logger.Error("HTTP shutdown incomplete", "component", "notes.http", "operation", "shutdown", "phase", "drain", "reason", diagnosis.IOReason(err))
		return 1
	}
	return 0
}
