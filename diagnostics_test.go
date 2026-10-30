// SPDX-License-Identifier: MPL-2.0
package achrix_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
)

func TestCompositionExplainsFieldAndDependencyABI(t *testing.T) {
	for _, tc := range []struct {
		name    string
		modules []achrix.Module
		want    []string
	}{
		{"nil", []achrix.Module{nil}, []string{"modules[0]", "non-nil"}},
		{"missing", []achrix.Module{&module{d: achrix.Descriptor{ID: "consumer", Version: "1", Requires: []achrix.Capability{{ID: "store.read", Version: 2}}}}}, []string{"consumer", "missing", "store.read", "ABI 2"}},
		{"incompatible", []achrix.Module{&module{d: achrix.Descriptor{ID: "consumer", Version: "1", Requires: []achrix.Capability{{ID: "achrix.authorization", Version: 1}}}}}, []string{"consumer", "requires", "ABI 1", "incompatible ABI 2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := achrix.New(config(), achrix.PolicyFunc(allow), tc.modules...)
			if !errors.Is(err, achrix.ErrComposition) {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q in %v", want, err)
				}
			}
		})
	}
}

func TestPolicyDiagnosticsPreserveCauseAndCorrelationPrivately(t *testing.T) {
	type correlationKey struct{}
	var logs strings.Builder
	cfg := config()
	cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	failure := fmt.Errorf("policy store: %w: PRIVATE_TOKEN", context.DeadlineExceeded)
	var captured error
	cfg.Diagnostic = func(ctx context.Context, operation, phase string, cause error) {
		if operation != "authorize" || phase != "policy" || ctx.Value(correlationKey{}) != "server-generated-123" {
			t.Fatalf("lost diagnostic boundary: %s %s", operation, phase)
		}
		captured = cause
	}
	app, err := achrix.New(cfg, achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error { return failure }), &module{d: descriptor("notes")})
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown(context.Background())
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), correlationKey{}, "server-generated-123"), time.Second)
	defer cancel()
	outward := app.Authorize(ctx, "private-principal", "notes.read", "private-resource")
	if outward != achrix.ErrAuthorizationUnavailable || errors.Unwrap(outward) != nil {
		t.Fatal("private cause escaped public policy result", outward)
	}
	if captured != failure || !errors.Is(captured, context.DeadlineExceeded) {
		t.Fatal("original live-caller dependency cause was lost")
	}
	for _, want := range []string{`"phase":"policy"`, `"reason":"policy_evaluation_failed"`, `"cause_type":"*fmt.wrapError"`, `"level":"ERROR"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing useful evidence %q: %s", want, logs.String())
		}
	}
	for _, secret := range []string{"PRIVATE_TOKEN", "private-principal", "private-resource"} {
		if strings.Contains(logs.String(), secret) || strings.Contains(outward.Error(), secret) {
			t.Fatal("private diagnostic data escaped", secret)
		}
	}
}
