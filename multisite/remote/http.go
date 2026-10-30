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

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/multisite"
)

// NewHTTP binds GET /sites and GET /sites/{site_id} below ResourceURL. Configure
// a distinct resource/audience and verifier from the MCP endpoint. The product
// mounts the returned endpoint and its MetadataHandler explicitly; construction
// does not start a listener, worker, session, polling loop or background timer.
func NewHTTP(service *multisite.Service, configuration bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("multisite remote: service is required")
	}
	resource, err := url.Parse(configuration.ResourceURL)
	if err != nil {
		return nil, fmt.Errorf("multisite remote: resource URL: %w", err)
	}
	// Bridge validates the canonical URL before admitting any request. Routing
	// deliberately does not use ServeMux's redirect/cleaning behavior.
	prefix := strings.TrimSuffix(resource.Path, "/")
	collection := prefix + "/sites"
	inventoryHandler, err := bridge.JSONHandler(InventoryOperation, func(r *http.Request) (InventoryInput, error) {
		if err := readRequest(r); err != nil {
			return InventoryInput{}, err
		}
		query, err := queryFields(r, "cursor", "limit")
		if err != nil {
			return InventoryInput{}, err
		}
		input := InventoryInput{Cursor: query.Get("cursor")}
		if raw, exists := query["limit"]; exists {
			limit, err := strconv.Atoi(raw[0])
			if err != nil {
				return InventoryInput{}, inputFault("limit", "Use an integer from 1 through 100, or omit limit to select 20.", err)
			}
			input.Limit = &limit
		}
		return input, nil
	}, inventory(service))
	if err != nil {
		return nil, err
	}
	siteHandler, err := bridge.JSONHandler(SiteOperation, func(r *http.Request) (SiteInput, error) {
		if err := readRequest(r); err != nil {
			return SiteInput{}, err
		}
		if _, err := queryFields(r); err != nil {
			return SiteInput{}, err
		}
		return SiteInput{SiteID: strings.TrimPrefix(r.URL.Path, collection+"/")}, nil
	}, site(service))
	if err != nil {
		return nil, err
	}
	missing, err := bridge.JSONHandler("achrix.multisite.http.route", func(*http.Request) (struct{}, error) {
		return struct{}{}, &bridge.Fault{
			Code: "route_not_found", Message: "This Multi-Site HTTP route is not supported.",
			Action:  "Use GET /sites or GET /sites/{site_id} below the configured resource URL.",
			Outcome: bridge.NotExecuted, HTTPStatus: http.StatusNotFound,
			Cause: errors.New("request does not match an explicit Multi-Site route"),
		}
	}, func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if err != nil {
		return nil, err
	}
	return bridge.NewJSONHTTP(configuration, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		canonical := r.URL.RawPath == "" && r.URL.Path == path.Clean(r.URL.Path)
		if canonical && r.URL.Path == collection {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
			}
			inventoryHandler.ServeHTTP(w, r)
			return
		}
		if canonical && strings.HasPrefix(r.URL.Path, collection+"/") {
			id := strings.TrimPrefix(r.URL.Path, collection+"/")
			if id != "" && !strings.Contains(id, "/") {
				if r.Method != http.MethodGet {
					w.Header().Set("Allow", http.MethodGet)
				}
				siteHandler.ServeHTTP(w, r)
				return
			}
		}
		missing.ServeHTTP(w, r)
	}))
}

func readRequest(r *http.Request) error {
	if r.Method != http.MethodGet {
		return &bridge.Fault{
			Code: "method_not_allowed", Message: "This site read requires GET.",
			Action:  "Send GET without a request body using the published route and query parameters.",
			Outcome: bridge.NotExecuted, HTTPStatus: http.StatusMethodNotAllowed,
			Cause: errors.New("non-GET request to a Multi-Site read route"),
		}
	}
	if r.ContentLength > 0 {
		return inputFault("body", "Send no body for a site read; use the path and supported query parameters.", errors.New("nonempty Multi-Site read request body"))
	}
	if r.Body != nil && r.Body != http.NoBody {
		// Inspect only enough bytes to reject a nonempty unknown-length body.
		// Bridge supplies the request deadline; this never buffers an upload.
		n, err := io.CopyN(io.Discard, r.Body, 1)
		if n != 0 {
			return inputFault("body", "Send no body for a site read; use the path and supported query parameters.", errors.New("nonempty Multi-Site read request body"))
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return &bridge.Fault{
				Code: "body_unavailable", Message: "The read request body could not be checked.",
				Action:  "Send GET with an empty body and a complete request within the deadline.",
				Outcome: bridge.NotExecuted, HTTPStatus: http.StatusBadRequest, Cause: err,
			}
		}
	}
	return nil
}

func queryFields(r *http.Request, allowed ...string) (url.Values, error) {
	if len(r.URL.RawQuery) > 512 {
		return nil, inputFault("query", "Use at most 512 encoded query bytes for the bounded cursor and limit.", errors.New("Multi-Site query exceeds 512 bytes"))
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, inputFault("query", "Use correctly encoded query parameters matching this route's schema.", err)
	}
	for name, values := range query {
		known := false
		for _, field := range allowed {
			if name == field {
				known = true
				break
			}
		}
		if !known {
			return nil, inputFault("query", "Use only the query parameters declared for this route; identity and authority claims are not accepted.", errors.New("unsupported Multi-Site query parameter"))
		}
		if len(values) != 1 {
			return nil, inputFault(name, "Send this query parameter at most once.", errors.New("duplicate Multi-Site query parameter"))
		}
	}
	return query, nil
}

func inputFault(field, guidance string, cause error) *bridge.Fault {
	return &bridge.Fault{
		Code: "invalid_arguments", Message: "The site request does not satisfy its input contract.",
		Action:  "Correct the indicated field using the published HTTP or MCP schema.",
		Fields:  map[string]string{field: guidance},
		Outcome: bridge.NotExecuted, HTTPStatus: http.StatusBadRequest, Cause: cause,
	}
}
