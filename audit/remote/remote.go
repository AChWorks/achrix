// SPDX-License-Identifier: MPL-2.0

// Package remote supplies optional explicit HTTP and MCP pages for Audit Query
// and Export. The owning Service retains exact-target authorization, cursor
// semantics and committed-record reads. Importing audit alone does not compile
// Bridge or the MCP SDK. Attribution is output, never a public identity claim.
package remote

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
)

const (
	QueryOperation  = audit.Query
	ExportOperation = audit.Export
	DefaultLimit    = 20
)

type PageInput struct {
	Target string `json:"target"`
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

// AttributionInfo is retained trusted provenance. It never grants authority and
// contains no credential. Missing connection identity is represented by omission.
type AttributionInfo struct {
	ClientID     string `json:"client_id"`
	ConnectionID string `json:"connection_id,omitempty"`
	Channel      string `json:"channel"`
	RequestID    string `json:"request_id"`
	Operation    string `json:"operation"`
}

type RecordInfo struct {
	ID          string           `json:"id"`
	Actor       string           `json:"actor"`
	Action      string           `json:"action"`
	Target      string           `json:"target"`
	Authority   string           `json:"authority"`
	Outcome     string           `json:"outcome"`
	OccurredAt  string           `json:"occurred_at"`
	Attribution *AttributionInfo `json:"attribution,omitempty"`
}

type Page struct {
	Records    []RecordInfo `json:"records"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

func present(record audit.Record) RecordInfo {
	info := RecordInfo{ID: record.ID, Actor: string(record.Actor), Action: record.Action, Target: record.Target, Authority: record.Authority, Outcome: record.Outcome, OccurredAt: record.OccurredAt.UTC().Format(time.RFC3339Nano)}
	if a := record.Attribution; a != nil {
		info.Attribution = &AttributionInfo{ClientID: a.ClientID, ConnectionID: a.ConnectionID, Channel: a.Channel, RequestID: a.RequestID, Operation: a.Operation}
	}
	return info
}

func readPage(invoke func(context.Context, achrix.Principal, string, string, int) (audit.Page, error)) func(context.Context, PageInput) (Page, error) {
	return func(ctx context.Context, input PageInput) (Page, error) {
		request, ok := bridge.RequestFromContext(ctx)
		if !ok || request.Caller.Principal == "" {
			return Page{}, &bridge.Fault{Code: "unauthenticated", Message: "A verified product account is required.", Action: "Use the configured authenticated HTTP or MCP endpoint.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusUnauthorized, Cause: errors.New("audit read lacks verified request context")}
		}
		limit := DefaultLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		// Exactly one owning Query or Export call per request. Never accumulate
		// pages, mix their distinct permissions, or claim a cross-page snapshot.
		page, err := invoke(ctx, request.Caller.Principal, input.Target, input.Cursor, limit)
		if err != nil {
			return Page{}, serviceFault(err)
		}
		result := Page{Records: make([]RecordInfo, len(page.Records)), NextCursor: page.NextCursor}
		for i, record := range page.Records {
			result.Records[i] = present(record)
		}
		return result, nil
	}
}

func serviceFault(err error) *bridge.Fault {
	// These operations attempt no state-changing effect. Dispatched separately
	// records actual invocation; NotExecuted is not a transaction rollback claim.
	f := &bridge.Fault{Code: "audit_unavailable", Message: "The retained Audit page could not be read.", Action: "Provide the request ID to an authorized operator to inspect protected service diagnostics.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusServiceUnavailable, Cause: err}
	switch {
	case errors.Is(err, achrix.ErrDenied):
		f.Code, f.Message, f.HTTPStatus = "denied", "Your product account cannot perform this Audit read for the selected target.", http.StatusForbidden
		f.Action = "Ask the product administrator to review the current exact-target Query or Export permission."
	case errors.Is(err, achrix.ErrAuthorizationUnavailable):
		f.Code, f.Message = "authorization_unavailable", "Current Audit permissions could not be evaluated."
		f.Action = "Provide the request ID to an authorized operator; no partial page is returned."
	case errors.Is(err, audit.ErrInput):
		f.Code, f.Message, f.HTTPStatus = "invalid_arguments", "The Audit page request does not satisfy its input contract.", http.StatusBadRequest
		f.Action = "Correct the indicated field using the published HTTP or MCP schema."
		var input *audit.InputError
		if errors.As(err, &input) {
			guidance := map[string]string{
				"target": "Use an exact 1..128-character opaque target reference; no wildcard or account listing is provided.",
				"cursor": "Use an unchanged cursor returned for this same target, or omit it to restart.",
				"limit":  "Use an integer from 1 through 100; omit it to select 20.",
				"actor":  "The product verifier must supply a valid internal Audit principal reference.",
			}[input.Field]
			if guidance != "" {
				f.Fields = map[string]string{input.Field: guidance, "reason": input.Reason}
			}
		}
		if len(f.Fields) == 0 {
			f.Fields = map[string]string{"arguments": "Use only the published fields and bounded values."}
		}
	case errors.Is(err, audit.ErrLimited):
		f.Code, f.Message, f.HTTPStatus = "busy", "The Audit service has reached its active-operation capacity.", http.StatusTooManyRequests
		f.Action = "Wait for capacity before requesting another bounded page."
	case errors.Is(err, achrix.ErrNotReady):
		f.Message, f.Action = "The application is not ready for Audit reads.", "Check application readiness and the explicit Audit migration before trying again."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		f.Code, f.Message = "deadline_exceeded", "The Audit read ended before a complete page was available."
		f.Action = "Check service availability and request duration; this read changed no retained records."
	}
	return f
}
