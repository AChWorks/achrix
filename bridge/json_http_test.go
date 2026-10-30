// SPDX-License-Identifier: MPL-2.0
package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
)

func jsonDecode(r *http.Request) (contractInput, error) {
	var input contractInput
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		return input, &bridge.Fault{Code: "invalid_input", Message: "The body must contain the declared text field.", Action: "Send one JSON object matching the published input schema.", Fields: map[string]string{"body": "Unknown fields and malformed JSON are not accepted."}, Cause: err, HTTPStatus: 400}
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return input, errors.New("unexpected trailing JSON body")
	}
	return input, nil
}

func newJSONFixture[Out any](t *testing.T, configure func(*bridge.Config), invoke func(context.Context, contractInput) (Out, error)) *contractFixture {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	capture := &contractCapture{}
	config := bridge.Config{
		ResourceURL: server.URL + "/api", AuthorizationServers: []string{"https://issuer.example.invalid"}, Scopes: []string{"notes"},
		Verify: func(_ context.Context, token string) (bridge.Credential, error) {
			if token != contractToken {
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
			return contractCredential(), nil
		},
		Admit: func(context.Context, bridge.Caller) error { return nil }, Diagnostic: capture.record,
		Limits: bridge.Limits{RequestBytes: 1024, ResponseBytes: 4096, Concurrent: 4, Timeout: time.Second},
	}
	if configure != nil {
		configure(&config)
	}
	route, err := bridge.JSONHandler("contract.echo", jsonDecode, invoke)
	if err != nil {
		t.Fatal(err)
	}
	operations := http.NewServeMux()
	operations.Handle("POST /api/echo", route)
	endpoint, err := bridge.NewJSONHTTP(config, operations)
	if err != nil {
		t.Fatal(err)
	}
	// The endpoint itself, not an outer redirecting ServeMux, rejects paths.
	mux.Handle("/", endpoint)
	client := server.Client()
	client.Timeout = 3 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return &contractFixture{endpoint, server, client, capture}
}

func jsonRequest(f *contractFixture, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, f.server.URL+"/api/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+contractToken)
	return req
}

func jsonCause(t *testing.T, f *contractFixture, id string, cause error, dispatched bool, outcome bridge.Outcome, reference string) bridge.Diagnostic {
	t.Helper()
	for _, captured := range f.capture.snapshot() {
		e := captured.Event
		if e.Request.ID != id || !errors.Is(e.Cause, cause) {
			continue
		}
		if !captured.Present || captured.Context.ID != id || captured.Context.Channel != "http" || e.Dispatched != dispatched || e.Outcome != outcome || e.Reference != reference || e.Phase == "" {
			t.Fatalf("incorrect HTTP diagnostic evidence: %+v", captured)
		}
		return e
	}
	t.Fatalf("missing protected HTTP cause for %s: %v", id, cause)
	return bridge.Diagnostic{}
}

func TestJSONHandlerRequiresVerifiedComposition(t *testing.T) {
	var decoded, invoked atomic.Int32
	h, err := bridge.JSONHandler("contract.echo", func(*http.Request) (contractInput, error) { decoded.Add(1); return contractInput{}, nil }, func(context.Context, contractInput) (contractOutput, error) {
		invoked.Add(1)
		return contractOutput{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "https://example.invalid/api/echo", strings.NewReader(`{}`)))
	if w.Code != 500 || decoded.Load() != 0 || invoked.Load() != 0 {
		t.Fatal("unverified invocation reached callbacks")
	}
	if _, err = bridge.JSONHandler("untrusted operation!", jsonDecode, contractEcho(&invoked)); err == nil {
		t.Fatal("accepted invalid fixed operation name")
	}
}

func TestJSONHTTPTrustedIdentityCurrentAdmissionAndGate(t *testing.T) {
	var calls atomic.Int32
	var disabled, denied atomic.Bool
	f := newJSONFixture(t, func(c *bridge.Config) {
		c.Admit = func(context.Context, bridge.Caller) error {
			if disabled.Load() {
				return achrix.ErrDenied
			}
			return nil
		}
		c.Gate = func(_ context.Context, caller bridge.Caller, operation string) error {
			if caller.Principal != "product-user-one" || operation != "contract.echo" {
				t.Error("gate received untrusted attribution")
			}
			if denied.Load() {
				return achrix.ErrDenied
			}
			return nil
		}
	}, contractEcho(&calls))
	req := jsonRequest(f, `{"text":"normal"}`)
	req.Header.Set("X-Request-ID", "forged-request")
	req.Header.Set("X-Actor", "administrator")
	req.Header.Set("X-Client-ID", "forged-client")
	response := f.send(t, req)
	var out contractOutput
	if response.Status != 200 || json.Unmarshal(response.Body, &out) != nil {
		t.Fatalf("HTTP result: %s", response.Body)
	}
	if out.Principal != "product-user-one" || out.ClientID != "registered-client" || out.RequestID == "forged-request" || out.RequestID != response.Header.Get("X-Request-ID") {
		t.Fatalf("untrusted attribution: %+v", out)
	}
	for _, captured := range f.capture.snapshot() {
		if captured.Event.Request.ID == out.RequestID && (captured.Event.Request.Channel != "http" || captured.Event.Request.Operation != "contract.echo" || !captured.Event.Dispatched || captured.Event.Outcome != bridge.Succeeded) {
			t.Fatal("lost trusted success context")
		}
	}
	denied.Store(true)
	failure := contractHTTPFailure(t, f.send(t, jsonRequest(f, `{"text":"blocked"}`)), 403)
	if failure.Outcome != bridge.NotExecuted || calls.Load() != 1 {
		t.Fatal("gate denial dispatched")
	}
	denied.Store(false)
	disabled.Store(true)
	failure = contractHTTPFailure(t, f.send(t, jsonRequest(f, `{"text":"disabled"}`)), 403)
	if failure.Outcome != bridge.NotExecuted || calls.Load() != 1 {
		t.Fatal("stale connection admission")
	}
	disabled.Store(false)
	if f.send(t, jsonRequest(f, `{"text":"current"}`)).Status != 200 || calls.Load() != 2 {
		t.Fatal("current admission was not recovered")
	}
}

func TestJSONHTTPAuthenticationAndProtectedVerifierFailure(t *testing.T) {
	var calls atomic.Int32
	private := errors.New("PRIVATE_VERIFIER_ORIGINAL_CAUSE")
	f := newJSONFixture(t, func(c *bridge.Config) {
		c.Verify = func(_ context.Context, token string) (bridge.Credential, error) {
			switch token {
			case contractToken:
				return contractCredential(), nil
			case "provider-down":
				return bridge.Credential{}, private
			case "expired":
				v := contractCredential()
				v.ExpiresAt = time.Now().Add(-time.Second)
				return v, nil
			case "no-scope":
				v := contractCredential()
				v.Scopes = nil
				return v, nil
			default:
				return bridge.Credential{}, bridge.ErrInvalidCredential
			}
		}
	}, contractEcho(&calls))
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {"wrong", 401}, {"expired", 401}, {"no-scope", 403}, {"provider-down", 503}} {
		req := jsonRequest(f, `{"text":"request"}`)
		if test.token == "" {
			req.Header.Del("Authorization")
		} else {
			req.Header.Set("Authorization", "Bearer "+test.token)
		}
		response := f.send(t, req)
		failure := contractHTTPFailure(t, response, test.status)
		if failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
			t.Fatal("invalid credential invoked service")
		}
		contractNoPrivate(t, response, private.Error())
		if test.token == "provider-down" {
			jsonCause(t, f, failure.RequestID, private, false, bridge.NotExecuted, "")
		}
	}
}

func TestJSONHTTPBoundaryAndDecodeRejections(t *testing.T) {
	var calls atomic.Int32
	f := newJSONFixture(t, nil, contractEcho(&calls))
	for _, test := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"host", func(r *http.Request) { r.Host = "untrusted.invalid" }, 403},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.invalid") }, 403},
		{"duplicate-auth", func(r *http.Request) { r.Header.Add("Authorization", "Bearer extra") }, 400},
		{"prefix-sibling", func(r *http.Request) { r.URL.Path = "/api-other/echo" }, 400},
		{"encoded-separator", func(r *http.Request) { r.URL.Path = "/api/echo"; r.URL.RawPath = "/api%2Fecho" }, 400},
		{"query-bound", func(r *http.Request) { r.URL.RawQuery = strings.Repeat("x", 4097) }, 400},
		{"unknown-route", func(r *http.Request) { r.URL.Path = "/api/unknown" }, 404},
		{"method", func(r *http.Request) { r.Method = http.MethodDelete }, 405},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := jsonRequest(f, `{"text":"request"}`)
			test.change(req)
			response := f.send(t, req)
			failure := contractHTTPFailure(t, response, test.status)
			if failure.Outcome != bridge.NotExecuted {
				t.Fatal("boundary claimed dispatch")
			}
			if test.status == 405 && response.Header.Get("Allow") != "POST" {
				t.Fatal("lost route Allow")
			}
		})
	}
	response := f.send(t, jsonRequest(f, `{"text":"safe","actor":"PRIVATE_CLAIM"}`))
	failure := contractHTTPFailure(t, response, 400)
	if failure.Code != "invalid_input" || failure.Fields["body"] == "" || failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
		t.Fatal("malformed input lacked actionable pre-dispatch failure")
	}
	contractNoPrivate(t, response, "PRIVATE_CLAIM")
}

func TestJSONHTTPBoundedChunkedInput(t *testing.T) {
	var calls atomic.Int32
	f := newJSONFixture(t, nil, contractEcho(&calls))
	for _, chunked := range []bool{false, true} {
		req := jsonRequest(f, `{"text":"`+strings.Repeat("x", 2048)+`"}`)
		if chunked {
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
		}
		failure := contractHTTPFailure(t, f.send(t, req), 413)
		if failure.Code != "request_too_large" || failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
			t.Fatal("oversized input reached service")
		}
	}
}

func TestJSONHTTPOperationOutcomesAndSafeStatus(t *testing.T) {
	for _, outcome := range []bridge.Outcome{bridge.NotExecuted, bridge.NotCommitted, bridge.Unknown} {
		t.Run(string(outcome), func(t *testing.T) {
			private := errors.New("PRIVATE_OPERATION_CAUSE")
			var calls atomic.Int32
			f := newJSONFixture(t, nil, func(context.Context, contractInput) (contractOutput, error) {
				calls.Add(1)
				return contractOutput{}, &bridge.Fault{Code: "precondition_failed", Message: "The expected revision cannot be confirmed.", Action: "Read the authorized resource and reconcile its revision before another write.", Outcome: outcome, Reference: "resource-one", Cause: private, HTTPStatus: 409}
			})
			response := f.send(t, jsonRequest(f, `{"text":"write once"}`))
			failure := contractHTTPFailure(t, response, 409)
			if failure.Outcome != outcome || failure.Reference != "resource-one" || calls.Load() != 1 {
				t.Fatal("lost owning operation outcome")
			}
			jsonCause(t, f, failure.RequestID, private, true, outcome, "resource-one")
			contractNoPrivate(t, response, private.Error())
		})
	}
}

func TestJSONHTTPPostSuccessFailuresRetainReference(t *testing.T) {
	for _, mode := range []string{"serialization", "capacity", "delivery"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			f := newJSONFixture(t, nil, func(ctx context.Context, _ contractInput) (any, error) {
				calls.Add(1)
				bridge.Confirm(ctx, "confirmed-resource")
				if mode == "serialization" {
					return func() {}, nil
				}
				if mode == "capacity" {
					return strings.Repeat("x", 8192), nil
				}
				return contractOutput{Text: "saved"}, nil
			})
			req := jsonRequest(f, `{"text":"write once"}`)
			if mode == "delivery" {
				private := errors.New("PRIVATE_DELIVERY_CAUSE")
				w := &contractFailingWriter{header: make(http.Header), cause: private}
				f.endpoint.ServeHTTP(w, req)
				jsonCause(t, f, w.Header().Get("X-Request-ID"), private, true, bridge.Succeeded, "confirmed-resource")
			} else {
				response := f.send(t, req)
				failure := contractHTTPFailure(t, response, 500)
				if failure.Outcome != bridge.Succeeded || failure.Reference != "confirmed-resource" || len(response.Body) > 4096 {
					t.Fatal("output failure erased known success or capacity")
				}
				found := false
				for _, d := range f.capture.snapshot() {
					if d.Event.Request.ID == failure.RequestID && d.Event.Phase == "output" && d.Event.Cause != nil && d.Event.Outcome == bridge.Succeeded {
						found = true
					}
				}
				if !found {
					t.Fatal("missing protected output cause")
				}
			}
			if calls.Load() != 1 {
				t.Fatal("operation was replayed")
			}
		})
	}
}

func TestJSONHTTPConcurrencyAndCooperativeDeadline(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f := newJSONFixture(t, func(c *bridge.Config) { c.Limits.Concurrent = 1 }, func(ctx context.Context, in contractInput) (contractOutput, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return contractOutput{}, ctx.Err()
			}
		}
		return contractOutput{Text: in.Text}, nil
	})
	type result struct {
		r contractHTTPResponse
		e error
	}
	done := make(chan result, 1)
	go func() { r, e := f.roundTrip(jsonRequest(f, `{"text":"first"}`)); done <- result{r, e} }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first invocation never started")
	}
	failure := contractHTTPFailure(t, f.send(t, jsonRequest(f, `{"text":"second"}`)), 503)
	close(release)
	first := <-done
	if first.e != nil || first.r.Status != 200 || failure.Code != "busy" || failure.Outcome != bridge.NotExecuted || calls.Load() != 1 {
		t.Fatal("concurrency limit or completion failed")
	}
	if f.send(t, jsonRequest(f, `{"text":"third"}`)).Status != 200 {
		t.Fatal("slot not released")
	}
	deadline := newJSONFixture(t, func(c *bridge.Config) { c.Limits.Timeout = 20 * time.Millisecond }, func(ctx context.Context, _ contractInput) (contractOutput, error) {
		<-ctx.Done()
		return contractOutput{}, ctx.Err()
	})
	response := deadline.send(t, jsonRequest(deadline, `{"text":"wait"}`))
	failure = contractHTTPFailure(t, response, 503)
	if failure.Outcome != bridge.Unknown {
		t.Fatal("post-dispatch deadline inferred non-execution")
	}
	jsonCause(t, deadline, failure.RequestID, context.DeadlineExceeded, true, bridge.Unknown, "")
}

func TestJSONHTTPConstructorIsIdleAndInvalidStatusCannotSucceed(t *testing.T) {
	var callbacks atomic.Int32
	configuration := bridge.Config{ResourceURL: "https://example.invalid/api", AuthorizationServers: []string{"https://issuer.example.invalid"},
		Verify: func(context.Context, string) (bridge.Credential, error) {
			callbacks.Add(1)
			return contractCredential(), nil
		},
		Admit:      func(context.Context, bridge.Caller) error { callbacks.Add(1); return nil },
		Diagnostic: func(context.Context, bridge.Diagnostic) { callbacks.Add(1) },
	}
	for range 32 {
		if _, err := bridge.NewJSONHTTP(configuration, http.NotFoundHandler()); err != nil {
			t.Fatal(err)
		}
	}
	if callbacks.Load() != 0 {
		t.Fatal("idle registration performed external/authentication work")
	}
	if _, err := bridge.NewJSONHTTP(configuration, nil); err == nil {
		t.Fatal("accepted missing router")
	}
	private := errors.New("PRIVATE_INVALID_STATUS_CAUSE")
	f := newJSONFixture(t, nil, func(context.Context, contractInput) (contractOutput, error) {
		return contractOutput{}, &bridge.Fault{Code: "cannot_be_success", Message: "This is an error.", Action: "Reconcile the operation.", HTTPStatus: 200, Cause: private}
	})
	response := f.send(t, jsonRequest(f, `{"text":"request"}`))
	failure := contractHTTPFailure(t, response, 503)
	if failure.Code != "operation_unavailable" || failure.Outcome != bridge.Unknown {
		t.Fatal("invalid status produced a trusted success or false outcome")
	}
	jsonCause(t, f, failure.RequestID, private, true, bridge.Unknown, "")
	contractNoPrivate(t, response, private.Error())
}

func TestJSONHTTPIncompleteBodyRetainsDeadlineCause(t *testing.T) {
	var calls atomic.Int32
	f := newJSONFixture(t, func(c *bridge.Config) { c.Limits.Timeout = 80 * time.Millisecond }, contractEcho(&calls))
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req := jsonRequest(f, "")
	req.Body, req.GetBody, req.ContentLength = reader, nil, -1
	req.TransferEncoding = []string{"chunked"}
	type result struct {
		r contractHTTPResponse
		e error
	}
	done := make(chan result, 1)
	go func() { r, e := f.roundTrip(req); done <- result{r, e} }()
	if _, err := writer.Write([]byte(`{"text":`)); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-done:
		if completed.e != nil {
			t.Fatal(completed.e)
		}
		failure := contractHTTPFailure(t, completed.r, 408)
		if failure.Code != "body_timeout" || failure.Outcome != bridge.NotExecuted || calls.Load() != 0 {
			t.Fatal("input timeout became a domain invocation or generic validation error")
		}
		found := false
		for _, capture := range f.capture.snapshot() {
			e := capture.Event
			if e.Request.ID == failure.RequestID && e.Phase == "body_read" && e.Cause != nil && !e.Dispatched {
				found = true
			}
		}
		if !found {
			t.Fatal("original body timeout was not retained")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("body read outlived its deadline")
	}
}
