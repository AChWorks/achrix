// SPDX-License-Identifier: MPL-2.0

// Package remote provides optional, explicit HTTP and MCP read adapters for a
// product-composed Multi-Site Service. The Service remains the sole owner of
// current collection and exact-site authorization, traversal and lifecycle.
// Importing multisite without this package does not compile Bridge or MCP.
package remote

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/multisite"
)

const (
	InventoryOperation = "achrix.multisite.inventory.read"
	SiteOperation      = "achrix.multisite.site.read"
	DefaultLimit       = 20
)

// InventoryInput omits Limit to select DefaultLimit. An explicit zero is
// invalid. A cursor selects traversal position; it never retains a permission.
type InventoryInput struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

// SiteInput selects an exact, case-sensitive ID for the owning Service to
// authorize. It is not a claim of identity, membership or site authority.
type SiteInput struct {
	SiteID string `json:"site_id"`
}

// SiteInfo contains only the owning Service's authorized public metadata.
// Revision remains an opaque configuration precondition, not a permission.
type SiteInfo struct {
	ID       string   `json:"id"`
	Disabled bool     `json:"disabled"`
	Modules  []string `json:"modules"`
	Revision string   `json:"revision"`
}

// InventoryPage never includes a total or identities of denied sites. An absent
// NextCursor means this traversal is exhausted under the permissions evaluated
// for the page; a later request always evaluates current permissions again.
type InventoryPage struct {
	Sites      []SiteInfo `json:"sites"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func caller(ctx context.Context) (achrix.Principal, error) {
	request, ok := bridge.RequestFromContext(ctx)
	if !ok || request.Caller.Principal == "" {
		return "", &bridge.Fault{
			Code: "unauthenticated", Message: "A verified product account is required.",
			Action:  "Use the configured authenticated HTTP or MCP endpoint.",
			Outcome: bridge.NotExecuted, HTTPStatus: http.StatusUnauthorized,
			Cause: errors.New("multisite remote operation lacks verified request context"),
		}
	}
	return request.Caller.Principal, nil
}

func inventory(service *multisite.Service) func(context.Context, InventoryInput) (InventoryPage, error) {
	return func(ctx context.Context, input InventoryInput) (InventoryPage, error) {
		actor, err := caller(ctx)
		if err != nil {
			return InventoryPage{}, err
		}
		limit := DefaultLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		// Do not filter, seek, or accumulate pages here. The Service owns sparse
		// traversal, all current permission checks and cursor privacy.
		page, err := service.Inventory(ctx, actor, input.Cursor, limit)
		if err != nil {
			return InventoryPage{}, serviceFault(err)
		}
		output := InventoryPage{Sites: make([]SiteInfo, len(page.Sites)), NextCursor: page.NextCursor}
		for i, info := range page.Sites {
			output.Sites[i] = siteInfo(info)
		}
		return output, nil
	}
}

func site(service *multisite.Service) func(context.Context, SiteInput) (SiteInfo, error) {
	return func(ctx context.Context, input SiteInput) (SiteInfo, error) {
		actor, err := caller(ctx)
		if err != nil {
			return SiteInfo{}, err
		}
		info, err := service.Site(ctx, actor, multisite.SiteID(input.SiteID))
		if err != nil {
			return SiteInfo{}, serviceFault(err)
		}
		// Only an authorized successful result becomes a trusted reference.
		// Merely requesting an ID never establishes site authority.
		bridge.Confirm(ctx, string(info.ID))
		return siteInfo(info), nil
	}
}

func siteInfo(info multisite.SiteInfo) SiteInfo {
	return SiteInfo{
		ID: string(info.ID), Disabled: info.Disabled,
		Modules: append([]string{}, info.Modules...), Revision: info.Revision,
	}
}

func serviceFault(err error) *bridge.Fault {
	// Both bound Services are queries that attempt no state-changing effect.
	// NotExecuted records that owning evidence, while Bridge's Dispatched flag
	// still records whether this query was invoked. It does not classify a
	// different operation or an earlier request as safe to replay.
	fault := &bridge.Fault{
		Code: "multisite_unavailable", Message: "The site inventory could not be read.",
		Action:  "Provide the request ID to an authorized operator to inspect service diagnostics.",
		Outcome: bridge.NotExecuted, HTTPStatus: http.StatusServiceUnavailable, Cause: err,
	}
	switch {
	case errors.Is(err, achrix.ErrDenied):
		fault.Code, fault.Message = "denied", "Your product account cannot read this site inventory or target."
		fault.Action = "Ask the product administrator to review your current collection and exact-site permissions."
		fault.HTTPStatus, fault.Outcome = http.StatusForbidden, bridge.NotExecuted
	case errors.Is(err, achrix.ErrAuthorizationUnavailable):
		fault.Code, fault.Message = "authorization_unavailable", "Current site permissions could not be evaluated."
		fault.Action = "Provide the request ID to an authorized operator; no partial inventory is returned."
		fault.Outcome = bridge.NotExecuted
	case errors.Is(err, achrix.ErrNotReady), errors.Is(err, multisite.ErrUnavailable):
		fault.Message = "The application or managed site inventory is not ready."
		fault.Action, fault.Outcome = "Check application readiness and managed Multi-Site composition before trying again.", bridge.NotExecuted
	case errors.Is(err, multisite.ErrNotFound):
		fault.Code, fault.Message = "site_not_found", "No configured site was found for this authorized target."
		fault.Action = "Check the exact case-sensitive site ID or refresh the authorized inventory."
		fault.HTTPStatus = http.StatusNotFound
	case errors.Is(err, multisite.ErrInput):
		fault.Code, fault.Message = "invalid_arguments", "The site request does not satisfy its input contract."
		fault.Action = "Correct the indicated field using the published HTTP or MCP schema."
		fault.HTTPStatus, fault.Outcome = http.StatusBadRequest, bridge.NotExecuted
		// Match only field names owned by Multi-Site. Never expose the original
		// error string or caller-supplied values as safe field guidance.
		switch {
		case strings.Contains(err.Error(), "site_id:"):
			fault.Fields = map[string]string{"site_id": "Use 1..64 ASCII letters, digits, underscores or hyphens with an alphanumeric first character."}
		case strings.Contains(err.Error(), "cursor:"):
			fault.Fields = map[string]string{"cursor": "Use an unchanged cursor previously returned by Inventory, or omit it for the first page."}
		case strings.Contains(err.Error(), "limit:"):
			fault.Fields = map[string]string{"limit": "Choose an integer from 1 through 100; omit it to select 20."}
		default:
			fault.Fields = map[string]string{"arguments": "Match the operation's published input fields and constraints."}
		}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		fault.Code, fault.Message = "deadline_exceeded", "The site read ended before a complete result was available."
		fault.Action = "Check service availability and the request deadline; this read did not change any site."
	}
	return fault
}
