// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Only the browser fixture writes this separate safe projection. Never recover
// trusted records from arbitrary provider text in fixture.log. Reasons must be
// source-owned explanations; private values and original errors stay in the
// ordinary private Go failure output. This is test tooling, not a product API.
type browserFixtureFailure struct {
	Test      string `json:"test"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Reason    string `json:"reason"`
	ErrorType string `json:"error_type,omitempty"`
	SQLState  string `json:"sqlstate,omitempty"`
	Errno     int    `json:"errno,omitempty"`
	Context   string `json:"context,omitempty"`
}

func browserFixtureFatal(t testing.TB, reason string, private ...any) {
	t.Helper()
	emitBrowserFixtureFailure(t, reason, 2, private...)
	t.Fatal(append([]any{reason}, private...)...)
}

func emitBrowserFixtureFailure(t testing.TB, reason string, caller int, private ...any) {
	t.Helper()
	root := os.Getenv("NOTES_ADMIN_BROWSER_ROOT")
	if root == "" {
		return // Shared helpers retain their ordinary non-browser test behavior.
	}
	info, err := os.Lstat(root)
	if err != nil || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Log("safe browser failure projection unavailable: private fixture root required")
		return
	}
	_, file, line, ok := runtime.Caller(caller)
	if !ok || len(reason) == 0 || len(reason) > 256 {
		t.Log("safe browser failure projection unavailable: source identity/reason invalid")
		return
	}
	record := browserFixtureFailure{Test: t.Name(), File: filepath.Base(file), Line: line, Reason: reason}
	for _, value := range private {
		cause, ok := value.(error)
		if !ok || cause == nil {
			continue
		}
		record.ErrorType = fmt.Sprintf("%T", cause)
		var database *pgconn.PgError
		if errors.As(cause, &database) && database != nil && regexp.MustCompile(`^[A-Z0-9]{5}$`).MatchString(database.Code) {
			record.SQLState = database.Code
		}
		var errno syscall.Errno
		if errors.As(cause, &errno) {
			record.Errno = int(errno)
		}
		if errors.Is(cause, context.DeadlineExceeded) {
			record.Context = "deadline_exceeded"
		} else if errors.Is(cause, context.Canceled) {
			record.Context = "cancelled"
		}
		break
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Log("safe browser failure projection encoding failed")
		return
	}
	output, err := os.OpenFile(filepath.Join(root, "browser-failures.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err == nil {
		_, err = output.Write(append(data, '\n'))
		err = errors.Join(err, output.Close())
	}
	if err != nil {
		t.Log("safe browser failure projection write failed", err)
	}
}

func TestBrowserFixtureFailureProjection(t *testing.T) {
	// Execute the actual fatal-emission path in a child: the original failure
	// must remain a failure and its private cause must not enter the projection.
	if scenario := os.Getenv("ACHRIX_BROWSER_FAILURE_TEST"); scenario != "" {
		switch scenario {
		case "direction":
			browserFixtureFatal(t, "real surface did not use installed locale direction", fmt.Errorf("PRIVATE_INPUT: %w", syscall.EIO))
		case "catalog":
			browserFixtureFatal(t, "safe browser catalog missing: identity", &pgconn.PgError{Code: "42501", Message: "PRIVATE_PASSWORD", Detail: "PRIVATE_SQL"})
		default:
			t.Fatal("unknown child fixture case")
		}
	}
	for _, scenario := range []string{"direction", "catalog"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBrowserFixtureFailureProjection$", "-test.v")
			command.Env = append(os.Environ(), "ACHRIX_BROWSER_FAILURE_TEST="+scenario, "NOTES_ADMIN_BROWSER_ROOT="+root)
			private, err := command.CombinedOutput()
			var failure *exec.ExitError
			if !errors.As(err, &failure) || failure.ExitCode() != 1 || !strings.Contains(string(private), "PRIVATE_") {
				t.Fatalf("original fixture failure was lost: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(root, "browser-failures.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var record browserFixtureFailure
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "PRIVATE_") || record.Test != "TestBrowserFixtureFailureProjection" || record.File != "admin_diagnostics_test.go" || record.Line == 0 || record.ErrorType == "" {
				t.Fatalf("unsafe or unhelpful failure projection: %+v", record)
			}
			if scenario == "direction" && (record.Reason != "real surface did not use installed locale direction" || record.Errno != int(syscall.EIO)) {
				t.Fatalf("direction/I-O cause lost: %+v", record)
			}
			if scenario == "catalog" && (record.Reason != "safe browser catalog missing: identity" || record.SQLState != "42501") {
				t.Fatalf("catalog/database cause lost: %+v", record)
			}
		})
	}
}
