// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
)

// NewHTTP binds GET /records and GET /export below the configured resource.
// Both return one bounded JSON page. Products configure an HTTP audience and
// verifier independently from MCP, then mount ingress and metadata explicitly.
// Construction starts no listeners, subscriptions, jobs or background work.
func NewHTTP(service *audit.Service, cfg bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("audit remote: service is required")
	}
	resource, err := url.Parse(cfg.ResourceURL)
	if err != nil {
		return nil, fmt.Errorf("audit remote: resource URL: %w", err)
	}
	prefix := strings.TrimSuffix(resource.Path, "/")
	query, err := bridge.JSONHandler(QueryOperation, decodePage, readPage(service.Query))
	if err != nil {
		return nil, err
	}
	export, err := bridge.JSONHandler(ExportOperation, decodePage, readPage(service.Export))
	if err != nil {
		return nil, err
	}
	missing, err := bridge.JSONHandler("achrix.audit.http.route", func(*http.Request) (struct{}, error) {
		return struct{}{}, &bridge.Fault{Code: "route_not_found", Message: "This Audit HTTP route is not supported.", Action: "Use GET /records or GET /export below the configured resource, with an exact target.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusNotFound, Cause: errors.New("request outside explicit audit page routes")}
	}, func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if err != nil {
		return nil, err
	}
	return bridge.NewJSONHTTP(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath == "" && r.URL.Path == path.Clean(r.URL.Path) {
			switch r.URL.Path {
			case prefix + "/records":
				if r.Method != http.MethodGet {
					w.Header().Set("Allow", http.MethodGet)
				}
				query.ServeHTTP(w, r)
				return
			case prefix + "/export":
				if r.Method != http.MethodGet {
					w.Header().Set("Allow", http.MethodGet)
				}
				export.ServeHTTP(w, r)
				return
			}
		}
		missing.ServeHTTP(w, r)
	}))
}

func decodePage(r *http.Request) (PageInput, error) {
	if r.Method != http.MethodGet {
		return PageInput{}, &bridge.Fault{Code: "method_not_allowed", Message: "This Audit page requires GET.", Action: "Send GET without a request body and use the published target, cursor and limit parameters.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusMethodNotAllowed, Cause: errors.New("non-GET audit page request")}
	}
	if r.ContentLength > 0 {
		return PageInput{}, inputFault("body", "Send no request body for this read.", errors.New("nonempty audit read body"))
	}
	if r.Body != nil && r.Body != http.NoBody {
		n, err := io.CopyN(io.Discard, r.Body, 1)
		if n != 0 {
			return PageInput{}, inputFault("body", "Send no request body for this read.", errors.New("nonempty unknown-length audit read body"))
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return PageInput{}, &bridge.Fault{Code: "body_unavailable", Message: "The read request body could not be checked.", Action: "Send a complete GET request with no body within the deadline.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusBadRequest, Cause: err}
		}
	}
	if len(r.URL.RawQuery) > 512 {
		return PageInput{}, inputFault("query", "Use at most 512 encoded query bytes.", errors.New("audit query exceeds its bound"))
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return PageInput{}, inputFault("query", "Use correctly encoded query parameters.", err)
	}
	for field, entries := range values {
		if field != "target" && field != "cursor" && field != "limit" {
			return PageInput{}, inputFault("query", "Use only target, cursor and limit; identity and attribution claims are not accepted.", errors.New("unknown audit query field"))
		}
		if len(entries) != 1 {
			return PageInput{}, inputFault(field, "Send this query field at most once.", errors.New("duplicate audit query field"))
		}
	}
	input := PageInput{Target: values.Get("target"), Cursor: values.Get("cursor")}
	if input.Target == "" {
		return PageInput{}, inputFault("target", "Provide the exact target reference for this Audit read.", errors.New("missing audit target"))
	}
	if raw, exists := values["limit"]; exists {
		limit, err := strconv.Atoi(raw[0])
		if err != nil {
			return PageInput{}, inputFault("limit", "Use an integer from 1 through 100, or omit it to select 20.", err)
		}
		input.Limit = &limit
	}
	return input, nil
}

func inputFault(field, guidance string, cause error) *bridge.Fault {
	return &bridge.Fault{Code: "invalid_arguments", Message: "The Audit page request does not satisfy its input contract.", Action: "Correct the indicated field using the published HTTP or MCP schema.", Fields: map[string]string{field: guidance}, Outcome: bridge.NotExecuted, HTTPStatus: http.StatusBadRequest, Cause: cause}
}
