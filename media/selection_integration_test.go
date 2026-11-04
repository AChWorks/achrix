// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"errors"
	"testing"

	"github.com/AChWorks/achrix"
)

func TestPostgresSelectionIsReadyAndSeparatelyAuthorized(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := operationContext(t)
	defer cancel()
	ready, err := f.service.Create(ctx, testActor, "selected.png", bytes.NewReader(imageBytes(t, "png", 2, 3)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Select(ctx, testActor, ready.ID)
	if err != nil || got.ID != ready.ID || got.State != "ready" || got.Revision != ready.Revision || got.Filename != ready.Filename {
		t.Fatal("ready selection", got, err)
	}
	if _, err := f.service.Select(ctx, "other-user", ready.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("cross-user selection was not denied", err)
	}
	// Exact selection never implies List, Status/Read or original byte access.
	selected, err := f.service.Select(ctx, "media-selection-only", ready.ID)
	if err != nil || selected.ID != ready.ID {
		t.Fatal("select-only principal was denied its exact grant", err)
	}
	if _, err := f.service.List(ctx, "media-selection-only", "", 1); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("selection granted library List", err)
	}
	if _, err := f.service.Status(ctx, "media-selection-only", ready.ID); !errors.Is(err, achrix.ErrDenied) {
		t.Fatal("selection granted byte/status Read", err)
	}
	var original bytes.Buffer
	if _, err := f.service.Read(ctx, "media-selection-only", ready.ID, &original); !errors.Is(err, achrix.ErrDenied) || original.Len() != 0 {
		t.Fatal("selection disclosed original bytes", err)
	}
	if _, err := f.service.Select(ctx, testActor, "../bad-id"); !errors.Is(err, ErrInput) {
		t.Fatal("invalid selection ID accepted", err)
	}
	if _, err := f.service.Select(ctx, testActor, newID()); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown selection disclosed metadata", err)
	}
	if err := f.service.Delete(ctx, testActor, ready.ID, ready.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Select(ctx, testActor, ready.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted asset remains selectable", err)
	}
}
