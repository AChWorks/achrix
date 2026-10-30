// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
)

func TestRevisionUsesCanonicalInt64DecimalStrings(t *testing.T) {
	for raw, expected := range map[string]int64{"1": 1, "9007199254740993": 9007199254740993, "9223372036854775807": 9223372036854775807} {
		actual, err := parseRevision(raw)
		if err != nil || actual != expected {
			t.Fatal("exact revision changed", raw, actual, err)
		}
	}
	for _, raw := range []string{"", "0", "01", "+1", "-1", " 1", "1 ", "1.0", "1e3", "۹", "9223372036854775808", "18446744073709551616"} {
		_, err := parseRevision(raw)
		if f := expectFault(t, err, 400); f.Fields["expected_revision"] == "" || f.Outcome != bridge.NotExecuted {
			t.Fatal("invalid revision lacks correction guidance", f)
		}
	}
}

func TestCompanionReferenceUsesOnlyCanonicalTrustedResourceAndGeneratedID(t *testing.T) {
	for _, raw := range []string{"https://product.example/api/media", "https://product.example/api/media/", "https://product.example", "http://localhost:8080/media", "http://127.0.0.1:8080/media", "http://[::1]:8080/media"} {
		u, err := referenceResource(raw)
		if err != nil || u.String() != raw {
			t.Fatal("valid trusted resource changed", raw, err)
		}
	}
	for _, raw := range []string{"", "/api/media", "http://product.example/api/media", "file:///private/file", "https://user:secret@product.example/media", "https://product.example/media?", "https://product.example/media?token=secret", "https://product.example/media#", "https://product.example/media#part", "https://product.example/a/../media", "https://product.example//media", "https://product.example/%6dedia", "https://product.example/" + strings.Repeat("a", 2048)} {
		if _, err := referenceResource(raw); err == nil {
			t.Fatal("uncanonical companion resource accepted", raw)
		}
	}
	resource, err := referenceResource("https://product.example/api/media/")
	if err != nil {
		t.Fatal(err)
	}
	asset := sampleAsset()
	view := present(asset, resource, true)
	if view.Content == nil || view.Content.URL != "https://product.example/api/media/assets/"+testAssetID+"/content" || view.Content.Resource != resource.String() {
		t.Fatal("reference identity or canonical path changed", view.Content)
	}
	asset.ID = "../outside"
	if present(asset, resource, true).Content != nil {
		t.Fatal("nongenerated identity became a content URL")
	}
}

func sampleAsset() media.Asset {
	return media.Asset{ID: testAssetID, State: "ready", Revision: 9007199254740993, CreatedAt: time.Date(2026, 10, 30, 9, 0, 0, 123456000, time.FixedZone("fixture", 3600)), Filename: "report.txt", MIME: "text/plain", Size: 3, SHA256: strings.Repeat("a", 64)}
}

func TestPresentationKeepsOpaqueRevisionsAndMinimalDeletedState(t *testing.T) {
	resource, err := referenceResource("https://product.example/api/media")
	if err != nil {
		t.Fatal(err)
	}
	asset := sampleAsset()
	view := present(asset, resource, true)
	if view.Revision != "9007199254740993" || view.CreatedAt != "2026-10-30T08:00:00.123456Z" {
		t.Fatal("revision or UTC precision changed", view)
	}
	for _, state := range []string{"pending", "deleting"} {
		asset.State = state
		if result := present(asset, resource, true); result.Content != nil || result.State != state {
			t.Fatal("nonready state became byte-readable", result)
		}
	}
	asset.State = "ready"
	if present(asset, nil, false).Content != nil {
		t.Fatal("List metadata manufactured a content grant")
	}
	asset.State = "deleted"
	// Even a stale nonzero source field cannot re-expose a tombstone's cleared
	// private metadata through this explicit public representation.
	data, err := json.Marshal(present(asset, resource, true))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields["id"] != testAssetID || fields["state"] != "deleted" || fields["revision"] != "9007199254740993" {
		t.Fatal("deleted output retained private metadata", string(data))
	}
}

func TestOwningInputAndUnknownOutcomeRemainDistinct(t *testing.T) {
	// Invoke the actual Service's early validation path. No PostgreSQL module
	// is started and no policy/persistence behavior is substituted in this test.
	var service media.Service
	_, invalid := service.Create(context.Background(), "reader", "../PRIVATE_FILENAME", strings.NewReader("unused"))
	var source *media.InputError
	if !errors.As(invalid, &source) || source.Field() != "filename" {
		t.Fatal("owning early validation did not return a safe field", invalid)
	}
	before := operationFault(invalid, CreateOperation, "", nil)
	if before.Code != "invalid_arguments" || before.Outcome != bridge.NotExecuted || before.Reference != "" || before.Fields["reason"] != source.Reason() {
		t.Fatal("early validation meaning changed", before)
	}
	after := operationFault(invalid, CreateOperation, testAssetID, nil)
	if after.Code != "invalid_arguments" || after.HTTPStatus != 400 || after.Outcome != bridge.Unknown || after.Reference != testAssetID {
		t.Fatal("late rejected input erased already-created intent", after)
	}
	private := errors.New("PRIVATE_PROVIDER_ORIGINAL")
	joined := errors.Join(invalid, media.ErrUnknownOutcome, private)
	unknown := operationFault(joined, CreateOperation, testAssetID, nil)
	if unknown.Code != "outcome_unknown" || unknown.HTTPStatus != 503 || unknown.Outcome != bridge.Unknown || unknown.Reference != testAssetID || unknown.Fields["reason"] != source.Reason() || !errors.Is(unknown.Cause, private) {
		t.Fatal("joined input error hid uncertain durable effects", unknown)
	}
	data, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_") || strings.Contains(string(data), "Cause") {
		t.Fatal("protected source input/provider detail escaped", string(data))
	}
	revisionErr := service.Delete(context.Background(), "reader", testAssetID, 0)
	revision := operationFault(revisionErr, DeleteOperation, testAssetID, nil)
	if revision.Fields["expected_revision"] == "" || revision.Fields["revision"] != "" || revision.Outcome != bridge.NotExecuted || revision.Reference != "" {
		t.Fatal("source revision validation did not map to the wire precondition", revision)
	}
}

func TestReadAndMutationFailureEvidenceIsConservative(t *testing.T) {
	for _, operation := range []string{ListOperation, StatusOperation, ReadOperation} {
		for _, err := range []error{media.ErrUnavailable, media.ErrNotFound, media.ErrConflict, media.ErrUnknownOutcome, context.DeadlineExceeded} {
			if f := operationFault(err, operation, "", nil); f.Outcome != bridge.NotExecuted {
				t.Fatal("query failure implied a mutation attempt", operation, f)
			}
		}
	}
	for _, err := range []error{media.ErrUnavailable, media.ErrConflict, media.ErrNotFound, media.ErrLimited} {
		f := operationFault(err, DeleteOperation, testAssetID, nil)
		if f.Outcome != bridge.Unknown || f.Reference != testAssetID {
			t.Fatal("coarse dispatched mutation failure invented no effects", f)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		plain := operationFault(err, DeleteOperation, testAssetID, nil)
		if plain.Outcome != bridge.Unknown || plain.Reference != "" || plain.Code != "deadline_exceeded" || !errors.Is(plain.Cause, err) {
			t.Fatal("ambiguous authorization cancellation verified a caller-selected target", plain)
		}
		joined := operationFault(errors.Join(media.ErrUnknownOutcome, err), DeleteOperation, testAssetID, nil)
		if joined.Outcome != bridge.Unknown || joined.Reference != testAssetID || joined.Code != "outcome_unknown" {
			t.Fatal("owning post-intent uncertainty lost its known target", joined)
		}
		created := operationFault(err, CreateOperation, testAssetID, nil)
		if created.Outcome != bridge.Unknown || created.Reference != testAssetID {
			t.Fatal("Service-generated Create identity was lost on cancellation", created)
		}
	}
	for _, err := range []error{achrix.ErrDenied, achrix.ErrAuthorizationUnavailable, achrix.ErrNotReady} {
		f := operationFault(err, DeleteOperation, testAssetID, nil)
		if f.Outcome != bridge.NotExecuted || f.Reference != "" {
			t.Fatal("pre-effect authorization/readiness failure claimed verified target effects", f)
		}
	}
	partial := media.ReconcileResult{Processed: 2, Busy: 1}
	f := operationFault(errors.Join(media.ErrUnknownOutcome, media.ErrUnavailable), ReconcileOperation, "", &partial)
	if f.Outcome != bridge.Unknown || f.Code != "outcome_unknown" || f.Fields["processed"] != "2" || f.Fields["busy"] != "1" {
		t.Fatal("partial reconciliation evidence was dropped", f)
	}
	if f := operationFault(achrix.ErrDenied, ReconcileOperation, "", &partial); f.Outcome != bridge.Unknown {
		t.Fatal("later denial erased observed partial effects", f)
	}
}

func TestDirectHandlerUseWithoutVerifiedContextFailsClosed(t *testing.T) {
	service := new(media.Service)
	resource, err := referenceResource("https://product.example/api/media")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, listErr := list(service)(ctx, ListInput{})
	_, statusErr := status(service, resource)(ctx, StatusInput{testAssetID})
	_, deleteErr := deleteAsset(service)(ctx, DeleteInput{testAssetID, "1"})
	_, reconcileErr := reconcile(service)(ctx, ReconcileInput{})
	_, createErr := create(service, resource)(ctx, uploadInput{"file.txt", strings.NewReader("private")})
	readErr := download(service, media.MaxUploadBytes)(ctx, StatusInput{testAssetID}, httptest.NewRecorder())
	for _, failure := range []error{listErr, statusErr, deleteErr, reconcileErr, createErr, readErr} {
		if f := expectFault(t, failure, 401); f.Outcome != bridge.NotExecuted || f.Reference != "" {
			t.Fatal("unverified direct call established authority", f)
		}
	}
}

type failingDestination struct {
	*httptest.ResponseRecorder
	cause error
}

func (w *failingDestination) Write(p []byte) (int, error) {
	n := len(p) / 2
	_, _ = w.ResponseRecorder.Write(p[:n])
	return n, w.cause
}

func TestAttachmentWriterDelaysHeadersAndRetainsOriginalDestinationFailure(t *testing.T) {
	private := errors.New("PRIVATE_DESTINATION_CAUSE")
	destination := &failingDestination{httptest.NewRecorder(), private}
	asset := sampleAsset()
	asset.Filename = "گزارش.txt"
	w := &attachmentWriter{writer: destination, asset: asset}
	if len(destination.Header()) != 0 {
		t.Fatal("fixture unexpectedly has headers")
	}
	if _, err := w.Write(nil); err != nil || len(destination.Header()) != 0 || w.started {
		t.Fatal("empty write committed attachment metadata", err)
	}
	n, err := w.Write([]byte("abc"))
	kind, parameters, parseErr := mime.ParseMediaType(destination.Header().Get("Content-Disposition"))
	if n != 1 || !errors.Is(err, private) || !errors.Is(w.cause, private) || !w.started || kind != "attachment" || parameters["filename"] != asset.Filename || parseErr != nil {
		t.Fatal("delivery failure or attachment filename changed", n, err, destination.Header())
	}
	if destination.Header().Get("Content-Type") != "text/plain" || destination.Header().Get("Content-Length") != "3" || destination.Header().Get("X-Content-Type-Options") != "nosniff" || destination.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("private original response lost its bounded safe headers", destination.Header())
	}
	destination = &failingDestination{httptest.NewRecorder(), nil}
	w = &attachmentWriter{writer: destination, asset: asset}
	if _, err := w.Write([]byte("abc")); !errors.Is(err, io.ErrShortWrite) || !errors.Is(w.cause, io.ErrShortWrite) {
		t.Fatal("silent partial destination write was treated as success", err)
	}
}
