// SPDX-License-Identifier: MPL-2.0
// Command componentidentity proves packaged public Module metadata without
// starting resources. It is a validation fixture, not product updater tooling.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
	"github.com/AChWorks/achrix/media"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "component identity validation failed")
		os.Exit(1)
	}
}

func run() error {
	// Constructors validate this synthetic local profile without connecting.
	const dsn = "host=/tmp user=fixture dbname=fixture sslmode=disable"
	auditModule, err := audit.NewPostgres(dsn, audit.Config{}, nil)
	if err != nil {
		return err
	}
	identityModule, err := identity.NewPostgres(dsn, identity.Config{}, nil)
	if err != nil {
		return err
	}
	mediaModule, err := media.NewPostgres(dsn, media.Config{StorageRoot: "/tmp/achrix-component-storage"}, nil)
	if err != nil {
		return err
	}
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
		return achrix.ErrDenied
	}), identityModule, auditModule, mediaModule)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Foundation string              `json:"foundation"`
		Components []achrix.Descriptor `json:"components"`
	}{achrix.Version(), app.Components()})
}
