// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Destructive tests accept only a deliberately identified, locally owned,
// private PostgreSQL Unix-socket cluster. A database NAME alone is insufficient.
func ownedTestConfig(dsn, root, database string) (*pgxpool.Config, error) {
	if root == "" || !filepath.IsAbs(root) || root != filepath.Clean(root) {
		return nil, errors.New("absolute canonical task-owned test root required")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, errors.New("test root symlinks or missing path are forbidden")
	}
	for _, dir := range []string{root, filepath.Join(root, "socket")} {
		info, e := os.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("private task-owned mode-0700 directory required: %s", dir)
		}
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid test DSN: %w", err)
	}
	if c.ConnConfig.Database != database ||
		c.ConnConfig.Host != filepath.Join(root, "socket") ||
		c.ConnConfig.Port == 0 || c.ConnConfig.TLSConfig != nil ||
		len(c.ConnConfig.Fallbacks) != 0 {
		return nil, errors.New("test database identity, local Unix socket, port or transport mismatch")
	}
	return c, nil
}
func taskTestConfig(t *testing.T, dsn, database string) *pgxpool.Config {
	t.Helper()
	config, err := ownedTestConfig(dsn, os.Getenv("ACHRIX_OAUTH_TEST_ROOT"), database)
	if err != nil {
		t.Fatal("unsafe OAuth test database target:", err)
	}
	return config
}
func checkTaskTestDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, database string) {
	t.Helper()
	var actual, encoding string
	var version int
	var unixSocket bool
	err := pool.QueryRow(ctx, "SELECT current_database(),current_setting('server_version_num')::integer,current_setting('server_encoding'),inet_server_addr() IS NULL").
		Scan(&actual, &version, &encoding, &unixSocket)
	if err != nil || actual != database || version != 180006 || encoding != "UTF8" || !unixSocket {
		t.Fatalf("unsafe or unsupported OAuth test database: database=%s version=%d encoding=%s unix=%t err=%v",
			actual, version, encoding, unixSocket, err)
	}
}
func TestOwnedTestDatabaseTargetGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "socket"), 0700); err != nil {
		t.Fatal(err)
	}
	local := "host=" + filepath.Join(root, "socket") + " port=55472 user=tester dbname=achrix_oauth_test sslmode=disable"
	if _, err := ownedTestConfig(local, root, "achrix_oauth_test"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, dsn, root, db string }{
		{"missing root", local, "", "achrix_oauth_test"},
		{"wrong DB", local, root, "achrix_oauth_restore"},
		{"network host", "host=127.0.0.1 port=55472 user=tester dbname=achrix_oauth_test sslmode=disable", root, "achrix_oauth_test"},
		{"remote TLS", "host=db.example.com port=5432 user=tester dbname=achrix_oauth_test sslmode=require", root, "achrix_oauth_test"},
		{"relative root", local, ".", "achrix_oauth_test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ownedTestConfig(tc.dsn, tc.root, tc.db); err == nil {
				t.Fatal("unsafe test target accepted")
			}
		})
	}
	if err := os.Chmod(filepath.Join(root, "socket"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedTestConfig(local, root, "achrix_oauth_test"); err == nil {
		t.Fatal("world-accessible socket root accepted")
	}
}
