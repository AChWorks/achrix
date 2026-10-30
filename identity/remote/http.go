// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
)

// NewHTTP binds exact-account GET and conditional enabled-state POST below the
// canonical ResourceURL. It starts no listener, worker or authentication flow.
func NewHTTP(service *identity.Service, cfg bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("identity remote: service is required")
	}
	u, err := url.Parse(cfg.ResourceURL)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(u.Path, "/") + "/accounts/"
	read, err := bridge.JSONHandler(AccountOperation, func(r *http.Request) (AccountInput, error) {
		if err := readRequest(r); err != nil {
			return AccountInput{}, err
		}
		return AccountInput{AccountID: strings.TrimPrefix(r.URL.Path, prefix)}, nil
	}, account(service))
	if err != nil {
		return nil, err
	}
	change, err := bridge.JSONHandler(SetEnabledOperation, func(r *http.Request) (SetEnabledInput, error) {
		return decodeEnabled(r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/enabled"))
	}, setEnabled(service))
	if err != nil {
		return nil, err
	}
	missing, err := bridge.JSONHandler("achrix.identity.http.route", func(*http.Request) (struct{}, error) {
		return struct{}{}, &bridge.Fault{Code: "route_not_found", Message: "This Identity HTTP route is not supported.", Action: "Use the published exact-account status or conditional enabled-state route.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusNotFound, Cause: errors.New("request outside explicit identity routes")}
	}, func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if err != nil {
		return nil, err
	}
	return bridge.NewJSONHTTP(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath == "" && r.URL.Path == path.Clean(r.URL.Path) && strings.HasPrefix(r.URL.Path, prefix) {
			rest := strings.TrimPrefix(r.URL.Path, prefix)
			id, suffix, slash := strings.Cut(rest, "/")
			if id != "" && !slash {
				w.Header().Set("Allow", http.MethodGet)
				read.ServeHTTP(w, r)
				return
			}
			if id != "" && slash && suffix == "enabled" {
				w.Header().Set("Allow", http.MethodPost)
				change.ServeHTTP(w, r)
				return
			}
		}
		missing.ServeHTTP(w, r)
	}))
}

func method(r *http.Request, want string) error {
	if r.Method != want {
		return &bridge.Fault{Code: "method_not_allowed", Message: "This account route requires its published HTTP method.", Action: "Use GET for account metadata or POST with JSON for a conditional enabled-state change.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusMethodNotAllowed, Cause: errors.New("unsupported identity HTTP method")}
	}
	if r.URL.RawQuery != "" {
		return inputFault("query", "This exact-account route accepts no query parameters or identity claims.", errors.New("unsupported identity query"))
	}
	return nil
}
func readRequest(r *http.Request) error {
	if err := method(r, http.MethodGet); err != nil {
		return err
	}
	if r.ContentLength > 0 {
		return inputFault("body", "Send GET without a body.", errors.New("unexpected account read body"))
	}
	if r.Body != nil && r.Body != http.NoBody {
		n, err := io.CopyN(io.Discard, r.Body, 1)
		if n != 0 {
			return inputFault("body", "Send GET without a body.", errors.New("unexpected account read body"))
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return inputFault("body", "Send a complete empty read request within the deadline.", err)
		}
	}
	return nil
}

func decodeEnabled(r *http.Request, id string) (SetEnabledInput, error) {
	input := SetEnabledInput{AccountID: id}
	if err := method(r, http.MethodPost); err != nil {
		return input, err
	}
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return input, inputFault("content_type", "Send one application/json Content-Type.", errors.New("missing or ambiguous identity content type"))
	}
	kind, parameters, err := mime.ParseMediaType(values[0])
	encodings := r.Header.Values("Content-Encoding")
	if err != nil || kind != "application/json" || len(parameters) > 1 || len(parameters) == 1 && !strings.EqualFold(parameters["charset"], "utf-8") || len(encodings) > 1 || len(encodings) == 1 && encodings[0] != "" {
		return input, inputFault("content_type", "Send uncompressed application/json encoded as UTF-8.", errors.New("unsupported identity JSON representation"))
	}
	tooLarge := func() error {
		return &bridge.Fault{Code: "request_too_large", Message: "The account JSON exceeds its 4096-byte input bound.", Action: "Send only expected_revision and enabled within 4096 JSON bytes.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusRequestEntityTooLarge, Cause: errors.New("identity JSON exceeds 4096 bytes")}
	}
	if r.ContentLength > 4096 {
		return input, tooLarge()
	}
	if r.Body == nil {
		return input, inputFault("body", "Send one complete JSON object.", errors.New("missing identity JSON body"))
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil {
		return input, inputFault("body", "Send one complete bounded JSON object within the deadline.", err)
	}
	if len(data) > 4096 {
		return input, tooLarge()
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return input, inputFault("body", "Send one JSON object with expected_revision and enabled.", errors.New("identity JSON is not an object"))
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return input, inputFault("body", "Use valid JSON object fields.", err)
		}
		name, ok := key.(string)
		if !ok || seen[name] || name != "expected_revision" && name != "enabled" {
			return input, inputFault("body", "Use each declared field exactly once; identity and attribution claims are not accepted.", errors.New("unknown or duplicate identity JSON field"))
		}
		seen[name] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return input, inputFault(name, "Use the published JSON field type.", err)
		}
		if name == "expected_revision" {
			if err := json.Unmarshal(raw, &input.ExpectedRevision); err != nil || len(raw) == 0 || raw[0] != '"' {
				return input, inputFault(name, "Use a positive decimal string, never a JSON number.", errors.New("account revision is not a JSON string"))
			}
		} else {
			if string(raw) != "true" && string(raw) != "false" {
				return input, inputFault(name, "Send the required JSON boolean true or false.", errors.New("enabled is not a JSON boolean"))
			}
			input.Enabled = string(raw) == "true"
		}
	}
	if _, err := d.Token(); err != nil {
		return input, inputFault("body", "Complete the JSON object.", err)
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return input, inputFault("body", "Send exactly one JSON object without trailing values.", errors.New("trailing identity JSON content"))
	}
	if !seen["expected_revision"] {
		return input, inputFault("expected_revision", "Send the unchanged positive decimal-string revision from Account.", errors.New("missing account revision"))
	}
	if !seen["enabled"] {
		return input, inputFault("enabled", "Send the required JSON boolean true or false.", errors.New("missing enabled field"))
	}
	if _, err := revision(input.ExpectedRevision); err != nil {
		return input, err
	}
	return input, nil
}
