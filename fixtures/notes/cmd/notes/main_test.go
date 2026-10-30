// SPDX-License-Identifier: MPL-2.0
package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotesCommandHelper(t *testing.T) {
	if os.Getenv("NOTES_TEST_COMMAND_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("notes", flag.ContinueOnError)
	os.Args = []string{"notes", "-mode", os.Getenv("NOTES_TEST_MODE")}
	if os.Getenv("NOTES_TEST_CLOSE_STDOUT") == "1" {
		_ = os.Stdout.Close()
	}
	os.Exit(run())
}

func TestCLIExplainsConfigurationMigrationAndOutputFailure(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		extra      []string
		want       []string
	}{
		{"mode", "unsupported", nil, []string{`"phase":"mode"`, "choose serve, migrate, or identity"}},
		{"setting", "serve", []string{"NOTES_LISTEN=PRIVATE_INVALID_SETTING"}, []string{"NOTES_LISTEN", `"phase":"validate"`}},
		{"database", "migrate", nil, []string{`"phase":"check_environment"`, `"reason":"file_missing"`, `"outcome":"not_executed"`}},
		{"output", "identity", []string{"NOTES_TEST_CLOSE_STDOUT=1"}, []string{`"phase":"write_stdout"`, `"reason":"closed"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestNotesCommandHelper$")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "NOTES_") {
					cmd.Env = append(cmd.Env, value)
				}
			}
			// A task-owned nonexistent Unix socket cannot reach another database/service.
			socketRoot, err := os.MkdirTemp("", "notes-cli-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
			cmd.Env = append(cmd.Env, "NOTES_TEST_COMMAND_HELPER=1", "NOTES_TEST_MODE="+tc.mode, "NOTES_DATABASE_URL=host="+filepath.Join(socketRoot, "missing-socket")+" user=fixture dbname=fixture sslmode=disable", "NOTES_WRITER_TOKEN="+strings.Repeat("PRIVATE_WRITER", 3), "NOTES_READER_TOKEN="+strings.Repeat("PRIVATE_READER", 3))
			cmd.Env = append(cmd.Env, tc.extra...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 1 {
				t.Fatalf("expected meaningful exit1: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("missing actionable diagnostic %q: %s", want, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), "PRIVATE_") {
				t.Fatal("runtime value leaked to CLI diagnostics", stderr.String())
			}
		})
	}
}
