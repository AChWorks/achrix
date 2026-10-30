// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func fixtureAttribution() Attribution {
	return Attribution{ClientID: "https://client.example/registered-client", ConnectionID: "connection:one", Channel: "mcp", RequestID: "REQUEST_123", Operation: "achrix.identity.account.set-enabled"}
}

func TestAttributionValueBindingBoundsAndCancellation(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value := fixtureAttribution()
	expected := value
	ctx, err := WithAttribution(parent, "operator", value)
	if err != nil {
		t.Fatal(err)
	}
	value.ClientID = "changed-after-binding"
	got, err := attributionFor(ctx, "operator")
	if err != nil || got == nil || *got != expected {
		t.Fatal("trusted context retained caller mutation", got, err)
	}
	got.ClientID = "changed-read-copy"
	got, err = attributionFor(ctx, "operator")
	if err != nil || *got != expected {
		t.Fatal("attribution extraction exposed shared state", got, err)
	}
	if _, err := attributionFor(ctx, "another-actor"); !errors.Is(err, ErrInput) {
		t.Fatal("attribution was reusable by another subject", err)
	}
	if got, err := attributionFor(parent, "operator"); err != nil || got != nil {
		t.Fatal("direct call acquired invented provenance", got, err)
	}
	if a, ok := ctx.Deadline(); !ok {
		t.Fatal("binding removed deadline")
	} else if b, _ := parent.Deadline(); a != b {
		t.Fatal("binding extended deadline")
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("binding detached cancellation")
	}
	if _, err := WithAttribution(context.Background(), "invalid actor", expected); !errors.Is(err, ErrInput) {
		t.Fatal("invalid subject accepted", err)
	}
	//lint:ignore SA1012 This intentional negative case proves the public helper rejects a nil parent instead of panicking.
	if _, err := WithAttribution(nil, "operator", expected); !errors.Is(err, ErrConfiguration) {
		t.Fatal("nil parent accepted", err)
	}
	for _, change := range []func(*Attribution){
		func(a *Attribution) { a.ClientID = "" },
		func(a *Attribution) { a.ClientID = strings.Repeat("é", 129) },
		func(a *Attribution) { a.ClientID = "PRIVATE_CLIENT\nHEADER" },
		func(a *Attribution) { a.ConnectionID = string([]byte{0xff}) },
		func(a *Attribution) { a.ConnectionID = "one\u0085two" },
		func(a *Attribution) { a.ConnectionID = strings.Repeat("x", 257) },
		func(a *Attribution) { a.RequestID = "" },
		func(a *Attribution) { a.RequestID = "request/id" },
		func(a *Attribution) { a.RequestID = strings.Repeat("x", 129) },
		func(a *Attribution) { a.Operation = "" },
		func(a *Attribution) { a.Operation = "tools/call" },
		func(a *Attribution) { a.Operation = strings.Repeat("x", 97) },
		func(a *Attribution) { a.Channel = "" },
		func(a *Attribution) { a.Channel = strings.Repeat("x", 33) },
	} {
		bad := expected
		change(&bad)
		_, err := WithAttribution(context.Background(), "operator", bad)
		var input *InputError
		if !errors.As(err, &input) || !strings.HasPrefix(input.Field, "attribution.") || input.Reason == "" || strings.Contains(err.Error(), "PRIVATE_CLIENT") {
			t.Fatal("invalid provenance lost safe owning guidance", err)
		}
	}
	for _, value := range []Attribution{
		expected,
		{ClientID: strings.Repeat("é", 128), Channel: strings.Repeat("C", 32), RequestID: strings.Repeat("R", 128), Operation: strings.Repeat("O", 96)},
	} {
		if _, err := WithAttribution(context.Background(), "operator", value); err != nil {
			t.Fatal("valid bounded/URI-like identifiers rejected", err)
		}
	}
}

func TestRetainedAttributionRejectsPartialStorageWithoutBlamingCaller(t *testing.T) {
	var columns [5]pgtype.Text
	if got, err := retainedAttribution(columns); err != nil || got != nil {
		t.Fatal("historical NULL tuple became a new identity", got, err)
	}
	columns[0] = pgtype.Text{String: "client", Valid: true}
	if _, err := retainedAttribution(columns); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrInput) {
		t.Fatal("partial retained tuple was exposed as caller input", err)
	}
	want := fixtureAttribution()
	for i, value := range []string{want.ClientID, want.ConnectionID, want.Channel, want.RequestID, want.Operation} {
		columns[i] = pgtype.Text{String: value, Valid: true}
	}
	got, err := retainedAttribution(columns)
	if err != nil || !reflect.DeepEqual(got, &want) {
		t.Fatal("retained attribution changed", got, err)
	}
}
