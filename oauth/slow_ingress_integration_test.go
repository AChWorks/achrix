// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercise real TLS streams that send an incomplete form and then stop making
// progress. An in-memory ResponseRecorder cannot prove socket read deadlines.
// The product's reverse proxy remains a separate operational acceptance gate.
func TestSlowStreamingPOSTTerminatesAndReleasesIngressHTTP1AndHTTP2(t *testing.T) {
	for _, tc := range []struct {
		name  string
		http2 bool
	}{
		{name: "http1"},
		{name: "http2", http2: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var issuerHandler http.Handler
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				issuerHandler.ServeHTTP(w, r)
			}))
			upstream.EnableHTTP2 = tc.http2
			upstream.Config.ReadHeaderTimeout = time.Second
			upstream.Config.MaxHeaderBytes = 8192
			upstream.StartTLS()
			t.Cleanup(upstream.Close)

			failure := make(chan string, 4)
			cfg := publicTestConfig()
			cfg.Issuer = upstream.URL
			cfg.Diagnostic = func(ctx context.Context, phase, status string, err error) {
				if phase == "token_form_read" && status == "read_failed" && err != nil {
					failure <- RequestID(ctx)
				}
			}
			srv, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			issuerHandler = srv

			reader, writer := io.Pipe()
			defer writer.Close()
			request, err := http.NewRequest(http.MethodPost, upstream.URL+srv.TokenPath(), reader)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			client := upstream.Client()
			client.Timeout = 8 * time.Second
			start := time.Now()
			result := make(chan struct {
				response *http.Response
				err      error
			}, 1)
			go func() {
				response, err := client.Do(request)
				result <- struct {
					response *http.Response
					err      error
				}{response, err}
			}()
			prefix := "grant_type=authorization_code&resource=" + upstream.URL + "&code="
			if _, err := io.WriteString(writer, prefix); err != nil {
				t.Fatalf("slow stream could not send initial body: %v", err)
			}
			select {
			case got := <-result:
				elapsed := time.Since(start)
				if got.err != nil {
					t.Fatalf("network deadline did not return a protected response: %v", got.err)
				}
				defer got.response.Body.Close()
				if tc.http2 && got.response.ProtoMajor != 2 || !tc.http2 && got.response.ProtoMajor != 1 {
					t.Fatalf("wrong protocol: %s", got.response.Proto)
				}
				if got.response.StatusCode != http.StatusBadRequest {
					t.Fatalf("wrong slow-body response: %s", got.response.Status)
				}
				if elapsed < 4*time.Second || elapsed > 8*time.Second {
					t.Fatalf("unexpected network deadline duration: %v", elapsed)
				}
				body, err := io.ReadAll(io.LimitReader(got.response.Body, 1024))
				if err != nil || !strings.Contains(string(body), "invalid_request") {
					t.Fatalf("non-disclosing OAuth failure missing: read error %v", err)
				}
				select {
				case id := <-failure:
					if id == "" || got.response.Header.Get("X-Request-ID") != id {
						t.Fatal("protected diagnostic correlation missing")
					}
				default:
					t.Fatal("request body transport failure not recorded")
				}
			case <-time.After(9 * time.Second):
				t.Fatal("stalled body escaped network deadline")
			}
			// The stuck request must not exhaust the issuer's bounded slots or
			// render its listener unusable after cancellation.
			metadata, err := client.Get(upstream.URL + srv.MetadataPath())
			if err != nil {
				t.Fatal("issuer failed after slow peer:", err)
			}
			defer metadata.Body.Close()
			if metadata.StatusCode != http.StatusOK {
				t.Fatalf("issuer failed to recover after slow peer: %s", metadata.Status)
			}
		})
	}
}

func TestSlowStreamFailureCannotBeMistakenForGrant(t *testing.T) {
	if errors.Is(io.ErrUnexpectedEOF, ErrInvalidGrant) {
		t.Fatal("network errors must remain distinguishable from rejected grants")
	}
}
