// SPDX-License-Identifier: MPL-2.0
package bridge_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
)

func newStreamFixture(t *testing.T, configure func(*bridge.Config), route http.Handler) *contractFixture {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	capture := &contractCapture{}
	config := bridge.Config{
		ResourceURL: server.URL + "/files", AuthorizationServers: []string{"https://issuer.example.invalid"}, Scopes: []string{"notes"},
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			if token != contractToken {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return contractCredential(), nil
		},
		Admit: func(context.Context, bridge.Caller) error { return nil }, Diagnostic: capture.record,
		Limits: bridge.Limits{RequestBytes: 1024, ResponseBytes: 4 << 20, Concurrent: 4, Timeout: time.Second},
	}
	if configure != nil {
		configure(&config)
	}
	endpoint, err := bridge.NewStreamingHTTP(config, route)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", endpoint)
	client := server.Client()
	client.Timeout = 3 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return &contractFixture{endpoint, server, client, capture}
}

func streamRequest(f *contractFixture) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, f.server.URL+"/files/original", nil)
	r.Header.Set("Authorization", "Bearer "+contractToken)
	return r
}

func streamRoute(t *testing.T, invoke func(context.Context, struct{}, http.ResponseWriter) error) http.Handler {
	t.Helper()
	h, err := bridge.StreamHandler("contract.file.read", func(*http.Request) (struct{}, error) { return struct{}{}, nil }, invoke)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestStreamHTTPVerifiedCompositionAndSocketDeadlines(t *testing.T) {
	var decoded, invoked, verified atomic.Int32
	h, err := bridge.StreamHandler("contract.file.read", func(*http.Request) (struct{}, error) {
		decoded.Add(1)
		return struct{}{}, nil
	}, func(context.Context, struct{}, http.ResponseWriter) error { invoked.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://example.invalid/files/original", nil))
	if w.Code != 500 || decoded.Load() != 0 || invoked.Load() != 0 {
		t.Fatal("standalone streaming handler reached application callbacks")
	}
	f := newStreamFixture(t, func(c *bridge.Config) {
		original := c.Verify
		c.Verify = func(ctx context.Context, token string) (bridge.Credential, error) {
			verified.Add(1)
			return original(ctx, token)
		}
	}, h)
	if verified.Load() != 0 || decoded.Load() != 0 || invoked.Load() != 0 {
		t.Fatal("idle construction performed work")
	}
	w = httptest.NewRecorder() // It cannot enforce socket deadlines.
	f.endpoint.ServeHTTP(w, streamRequest(f))
	if w.Code != 503 || verified.Load() != 0 || decoded.Load() != 0 || invoked.Load() != 0 {
		t.Fatal("unsupported socket deadline reached authentication or invocation")
	}
	jsonCause(t, f, w.Header().Get("X-Request-ID"), http.ErrNotSupported, false, bridge.NotExecuted, "")
	if _, err := bridge.StreamHandler[struct{}]("invalid operation", nil, nil); err == nil {
		t.Fatal("accepted incomplete binding")
	}
}

func TestStreamHTTPDeliversBeforeCompletionAndPreservesBoundaryHeaders(t *testing.T) {
	payload := bytes.Repeat([]byte("private original bytes\n"), 70000)
	release := make(chan struct{})
	completed := make(chan bridge.Diagnostic, 1)
	var calls atomic.Int32
	route := streamRoute(t, func(ctx context.Context, _ struct{}, w http.ResponseWriter) error {
		calls.Add(1)
		request, ok := bridge.RequestFromContext(ctx)
		if !ok || request.Caller != contractCredential().Caller || request.Channel != "http" || request.Operation != "contract.file.read" {
			return errors.New("incorrect verified stream request")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Header().Set("Content-Disposition", "attachment; filename=sample.txt")
		w.Header().Set("X-Request-ID", "forged")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "disabled")
		bridge.Confirm(ctx, "known-original")
		if _, err := w.Write(payload[:64<<10]); err != nil {
			return err
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, err := w.Write(payload[64<<10:])
		return err
	})
	f := newStreamFixture(t, func(c *bridge.Config) {
		original := c.Diagnostic
		c.Diagnostic = func(ctx context.Context, d bridge.Diagnostic) {
			original(ctx, d)
			if d.Phase == "complete" {
				completed <- d
			}
		}
	}, route)
	r, err := f.client.Do(streamRequest(f))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer r.Body.Close()
	prefix := make([]byte, 4096)
	_, err = io.ReadFull(r.Body, prefix)
	close(release) // A whole-response buffer would deadlock until the deadline.
	if err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(r.Body)
	if err != nil || !bytes.Equal(append(prefix, rest...), payload) {
		t.Fatal("streamed payload did not arrive exactly", err)
	}
	if r.StatusCode != 200 || r.ContentLength != int64(len(payload)) || r.Header.Get("X-Request-ID") == "forged" || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("stream boundary headers or exact length changed")
	}
	select {
	case d := <-completed:
		if d.Outcome != bridge.Succeeded || d.Reference != "known-original" || !d.Dispatched || calls.Load() != 1 {
			t.Fatal("stream success/reference was lost or replayed")
		}
	case <-time.After(time.Second):
		t.Fatal("missing terminal stream diagnostic")
	}
}

func TestStreamHTTPCurrentAdmissionAndGateBeforeBytes(t *testing.T) {
	var calls atomic.Int32
	denied := errors.New("PRIVATE_STREAM_GATE")
	route := streamRoute(t, func(_ context.Context, _ struct{}, w http.ResponseWriter) error {
		calls.Add(1)
		_, err := w.Write([]byte("content"))
		return err
	})
	f := newStreamFixture(t, func(c *bridge.Config) {
		c.Gate = func(context.Context, bridge.Caller, string) error { return errors.Join(achrix.ErrDenied, denied) }
	}, route)
	response := f.send(t, streamRequest(f))
	failure := contractHTTPFailure(t, response, 403)
	if calls.Load() != 0 || failure.Outcome != bridge.NotExecuted {
		t.Fatal("denied stream reached byte callback")
	}
	jsonCause(t, f, failure.RequestID, denied, false, bridge.NotExecuted, "")
	contractNoPrivate(t, response, denied.Error())
}

func TestStreamHTTPBeforeBytesUsesProblemAndAfterHeadersAborts(t *testing.T) {
	private := errors.New("PRIVATE_STORED_FILE_FAILURE")
	early := newStreamFixture(t, nil, streamRoute(t, func(_ context.Context, _ struct{}, w http.ResponseWriter) error {
		w.Header().Set("Content-Disposition", "attachment; filename=private.txt")
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "99")
		w.WriteHeader(200)
		if n, err := w.Write(nil); n != 0 || err != nil {
			return errors.New("zero write should not commit content")
		}
		return &bridge.Fault{Code: "file_unavailable", Message: "The original could not be read.", Action: "Check its authorized status.", Outcome: bridge.NotExecuted, HTTPStatus: 409, Cause: private}
	}))
	response := early.send(t, streamRequest(early))
	failure := contractHTTPFailure(t, response, 409)
	if response.Header.Get("Content-Disposition") != "" || response.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatal("unverified attachment headers escaped")
	}
	jsonCause(t, early, failure.RequestID, private, true, bridge.NotExecuted, "")
	contractNoPrivate(t, response, private.Error())
	for _, kind := range []string{"source_failure", "short_success"} {
		t.Run(kind, func(t *testing.T) {
			prefix := bytes.Repeat([]byte("p"), 64<<10)
			f := newStreamFixture(t, nil, streamRoute(t, func(ctx context.Context, _ struct{}, w http.ResponseWriter) error {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", strconv.Itoa(2*len(prefix)))
				bridge.Confirm(ctx, "confirmed-reference")
				if _, err := w.Write(prefix); err != nil {
					return err
				}
				if kind == "short_success" {
					return nil
				}
				return &bridge.Fault{Code: "file_unavailable", Message: "The original could not be read completely.", Action: "Check its authorized status.", Outcome: bridge.Unknown, Cause: private}
			}))
			r, err := f.client.Do(streamRequest(f))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			data, err := io.ReadAll(r.Body)
			if err == nil || !bytes.Equal(data, prefix) || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatal("partial response was not aborted or had appended bytes", len(data), err)
			}
			cause := private
			if kind == "short_success" {
				cause = io.ErrUnexpectedEOF
			}
			jsonCause(t, f, r.Header.Get("X-Request-ID"), cause, true, bridge.Succeeded, "confirmed-reference")
		})
	}
}

func TestStreamHTTPByteBudgetAbortsInsteadOfTruncatingSuccess(t *testing.T) {
	prefix := bytes.Repeat([]byte("x"), 8192)
	writerFailure := make(chan error, 1)
	f := newStreamFixture(t, func(c *bridge.Config) { c.Limits.ResponseBytes = 8192 }, streamRoute(t, func(_ context.Context, _ struct{}, w http.ResponseWriter) error {
		if _, err := w.Write(prefix); err != nil {
			return err
		}
		_, err := w.Write([]byte("one-byte-too-many"))
		writerFailure <- err
		return &bridge.Fault{Code: "file_limit", Message: "The file exceeds this response budget.", Action: "Use an endpoint configured for the published file profile.", Outcome: bridge.NotExecuted, Cause: err}
	}))
	r, err := f.client.Do(streamRequest(f))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err == nil || !bytes.Equal(data, prefix) {
		t.Fatal("oversize file became a successful truncated response", err)
	}
	cause := <-writerFailure
	if cause == nil {
		t.Fatal("writer did not see byte limit")
	}
	jsonCause(t, f, r.Header.Get("X-Request-ID"), cause, true, bridge.NotExecuted, "")
}

type streamFailWriter struct {
	*httptest.ResponseRecorder
	cause error
}

func (w *streamFailWriter) SetReadDeadline(time.Time) error  { return nil }
func (w *streamFailWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *streamFailWriter) Write([]byte) (int, error)        { return 0, w.cause }

func TestStreamHTTPDestinationCauseKeepsConfirmedOutcome(t *testing.T) {
	var calls atomic.Int32
	f := newStreamFixture(t, nil, streamRoute(t, func(ctx context.Context, _ struct{}, w http.ResponseWriter) error {
		calls.Add(1)
		bridge.Confirm(ctx, "already-confirmed")
		_, err := w.Write([]byte("original"))
		return err
	}))
	w := &streamFailWriter{httptest.NewRecorder(), io.ErrClosedPipe}
	func() {
		defer func() {
			if p := recover(); p != http.ErrAbortHandler {
				t.Errorf("expected partial-transfer abort, got %v", p)
			}
		}()
		f.endpoint.ServeHTTP(w, streamRequest(f))
	}()
	if calls.Load() != 1 || w.Body.Len() != 0 {
		t.Fatal("failed delivery was replayed or appended a problem")
	}
	jsonCause(t, f, w.Header().Get("X-Request-ID"), io.ErrClosedPipe, true, bridge.Succeeded, "already-confirmed")
}

type streamFinalWriter struct {
	*httptest.ResponseRecorder
	readDeadline, writeDeadline time.Time
	flushes                     int
	cause                       error
}

func (w *streamFinalWriter) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	return nil
}
func (w *streamFinalWriter) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	return nil
}
func (w *streamFinalWriter) FlushError() error {
	w.flushes++
	if w.readDeadline.IsZero() || w.writeDeadline.IsZero() {
		return errors.New("final file flush has no transport deadline")
	}
	w.ResponseRecorder.Flush()
	return w.cause
}

func TestStreamHTTPFinalFlushKeepsDeadlineCauseAndConfirmedOutcome(t *testing.T) {
	private := errors.New("PRIVATE_FINAL_BUFFERED_WRITE")
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			var calls atomic.Int32
			f := newStreamFixture(t, nil, streamRoute(t, func(ctx context.Context, _ struct{}, w http.ResponseWriter) error {
				calls.Add(1)
				// A small final write may remain in the real server's buffer;
				// no Content-Length also exercises server-owned final framing.
				if _, err := w.Write([]byte("tail")); err != nil {
					return err
				}
				bridge.Confirm(ctx, "confirmed-tail")
				return nil
			}))
			w := &streamFinalWriter{ResponseRecorder: httptest.NewRecorder()}
			if failed {
				w.cause = private
			}
			var recovered any
			func() { defer func() { recovered = recover() }(); f.endpoint.ServeHTTP(w, streamRequest(f)) }()
			if failed && recovered != http.ErrAbortHandler || !failed && recovered != nil {
				t.Fatalf("unexpected final flush abort: %v", recovered)
			}
			if calls.Load() != 1 || w.flushes != 1 || w.Body.String() != "tail" {
				t.Fatal("final delivery was not flushed once or was replayed/concatenated")
			}
			if w.readDeadline.IsZero() || w.writeDeadline.IsZero() || !w.writeDeadline.After(w.readDeadline) {
				t.Fatal("handler return cleared deadlines before server finalization")
			}
			if failed {
				jsonCause(t, f, w.Header().Get("X-Request-ID"), private, true, bridge.Succeeded, "confirmed-tail")
			}
		})
	}
}

func TestStreamHTTPRawUploadFailureKeepsPostDispatchOutcomeAndBodyCause(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(strconv.FormatBool(slow), func(t *testing.T) {
			private := errors.New("PRIVATE_OWNER_UPLOAD_CAUSE")
			var calls atomic.Int32
			route, err := bridge.JSONHandler("contract.file.create", func(r *http.Request) (io.Reader, error) { return r.Body, nil }, func(_ context.Context, input io.Reader) (contractOutput, error) {
				calls.Add(1)
				_, _ = io.ReadAll(input) // The Service may project its private cause.
				return contractOutput{}, &bridge.Fault{Code: "upload_incomplete", Message: "Upload needs reconciliation.", Action: "Inspect the known asset before considering another upload.", Outcome: bridge.Unknown, Reference: "known-upload-id", Cause: private, HTTPStatus: 503}
			})
			if err != nil {
				t.Fatal(err)
			}
			f := newStreamFixture(t, func(c *bridge.Config) {
				c.Limits.RequestBytes = 256
				if slow {
					c.Limits.Timeout = 80 * time.Millisecond
				}
			}, route)
			req := streamRequest(f)
			req.Method = http.MethodPost
			req.Header.Set("Content-Type", "application/octet-stream")
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
			var response contractHTTPResponse
			if !slow {
				req.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 600)))
				response = f.send(t, req)
			} else {
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				req.Body = reader
				type result struct {
					response contractHTTPResponse
					err      error
				}
				done := make(chan result, 1)
				go func() { r, e := f.roundTrip(req); done <- result{r, e} }()
				if _, err := writer.Write([]byte("partial")); err != nil {
					t.Fatal(err)
				}
				select {
				case r := <-done:
					if r.err != nil {
						t.Fatal(r.err)
					}
					response = r.response
				case <-time.After(2 * time.Second):
					t.Fatal("post-dispatch raw body exceeded socket deadline")
				}
			}
			failure := contractHTTPFailure(t, response, 503)
			if calls.Load() != 1 || failure.Code != "upload_incomplete" || failure.Outcome != bridge.Unknown || failure.Reference != "known-upload-id" {
				t.Fatal("raw upload failure erased known intent or became pre-dispatch")
			}
			d := jsonCause(t, f, failure.RequestID, private, true, bridge.Unknown, "known-upload-id")
			var tooLarge *http.MaxBytesError
			var timeout net.Error
			if slow && (!errors.As(d.Cause, &timeout) || !timeout.Timeout()) || !slow && !errors.As(d.Cause, &tooLarge) {
				t.Fatal("original raw body failure was not retained")
			}
			contractNoPrivate(t, response, private.Error())
		})
	}
}
