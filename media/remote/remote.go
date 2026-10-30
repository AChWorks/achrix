// SPDX-License-Identifier: MPL-2.0

// Package remote provides deliberate optional HTTP and MCP adapters for the
// product-composed private Media Service. The owning Service retains all domain
// permissions, byte validation, preconditions and durable lifecycle behavior.
package remote

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
)

const (
	CreateOperation       = media.Create
	ListOperation         = media.List
	StatusOperation       = "achrix.media.status"
	ReadOperation         = media.Read
	DeleteOperation       = media.Delete
	ReconcileOperation    = media.Reconcile
	DefaultListLimit      = 20
	DefaultReconcileLimit = 10
)

type ListInput struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}
type StatusInput struct {
	AssetID string `json:"asset_id"`
}
type DeleteInput struct {
	AssetID          string `json:"asset_id"`
	ExpectedRevision string `json:"expected_revision"`
}
type ReconcileInput struct {
	Limit *int `json:"limit,omitempty"`
}

// ContentReference is a canonical companion HTTP location, never a bearer URL.
// Its resource needs its own credential; every fetch rechecks exact Read rights.
type ContentReference struct {
	URL      string `json:"url"`
	Resource string `json:"resource"`
}

// AssetInfo preserves opaque revisions as decimal strings. Cleared/zero
// metadata is omitted, including the owning Service's minimal deleted tombstone.
// A ready Status/Create may include a reference; List never implies byte rights.
type AssetInfo struct {
	ID        string            `json:"id"`
	State     string            `json:"state"`
	Revision  string            `json:"revision"`
	CreatedAt string            `json:"created_at"`
	Filename  string            `json:"filename,omitempty"`
	MIME      string            `json:"mime,omitempty"`
	Size      int64             `json:"size,omitempty"`
	Width     int               `json:"width,omitempty"`
	Height    int               `json:"height,omitempty"`
	SHA256    string            `json:"sha256,omitempty"`
	Content   *ContentReference `json:"content,omitempty"`
}
type Page struct {
	Assets     []AssetInfo `json:"assets"`
	NextCursor string      `json:"next_cursor,omitempty"`
}
type DeleteResult struct {
	AssetID string `json:"asset_id"`
	Outcome string `json:"outcome"`
}
type ReconcileResult struct {
	Processed int `json:"processed"`
	Busy      int `json:"busy"`
}

var generatedID = regexp.MustCompile(`^[A-Z2-7]{26}$`)
var decimalRevision = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func referenceResource(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 2048 {
		return nil, errors.New("media remote: a bounded canonical HTTP resource URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != raw {
		return nil, errors.New("media remote: HTTP resource must be absolute without credentials, query, fragment or encoded path")
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost") || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("media remote: HTTPS is required except for loopback development")
	}
	if strings.ContainsAny(u.Host, "\r\n\t ") || strings.Contains(u.Path, "//") || u.Path != "" && u.Path != path.Clean(u.Path) && u.Path != path.Clean(u.Path)+"/" {
		return nil, errors.New("media remote: HTTP resource must have a canonical host and path")
	}
	return u, nil
}

func present(asset media.Asset, resource *url.URL, withContent bool) AssetInfo {
	result := AssetInfo{
		ID: asset.ID, State: asset.State, Revision: strconv.FormatInt(asset.Revision, 10),
		CreatedAt: asset.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if asset.State == "deleted" {
		return result
	}
	result.Filename, result.MIME, result.Size = asset.Filename, asset.MIME, asset.Size
	result.Width, result.Height, result.SHA256 = asset.Width, asset.Height, asset.SHA256
	if withContent && asset.State == "ready" && generatedID.MatchString(asset.ID) {
		u := *resource
		u.Path = strings.TrimSuffix(u.Path, "/") + "/assets/" + asset.ID + "/content"
		result.Content = &ContentReference{URL: u.String(), Resource: resource.String()}
	}
	return result
}

func principal(ctx context.Context) (achrix.Principal, error) {
	request, ok := bridge.RequestFromContext(ctx)
	if !ok || request.Caller.Principal == "" {
		return "", &bridge.Fault{
			Code: "unauthenticated", Message: "A verified product account is required.",
			Action:  "Use the configured authenticated HTTP or MCP endpoint.",
			Outcome: bridge.NotExecuted, HTTPStatus: http.StatusUnauthorized,
			Cause: errors.New("media remote invocation lacks verified request context"),
		}
	}
	return request.Caller.Principal, nil
}

func list(service *media.Service) func(context.Context, ListInput) (Page, error) {
	return func(ctx context.Context, input ListInput) (Page, error) {
		actor, err := principal(ctx)
		if err != nil {
			return Page{}, err
		}
		limit := DefaultListLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		page, err := service.List(ctx, actor, input.Cursor, limit)
		if err != nil {
			return Page{}, operationFault(err, ListOperation, "", nil)
		}
		output := Page{Assets: make([]AssetInfo, len(page.Assets)), NextCursor: page.NextCursor}
		for i, asset := range page.Assets {
			// The existing List grant covers whole-library metadata, not each
			// asset's byte access. Do not filter it or manufacture Read grants.
			output.Assets[i] = present(asset, nil, false)
		}
		return output, nil
	}
}

func status(service *media.Service, resource *url.URL) func(context.Context, StatusInput) (AssetInfo, error) {
	return func(ctx context.Context, input StatusInput) (AssetInfo, error) {
		actor, err := principal(ctx)
		if err != nil {
			return AssetInfo{}, err
		}
		asset, err := service.Status(ctx, actor, input.AssetID)
		if err != nil {
			return AssetInfo{}, operationFault(err, StatusOperation, "", nil)
		}
		bridge.Confirm(ctx, asset.ID)
		return present(asset, resource, true), nil
	}
}

func deleteAsset(service *media.Service) func(context.Context, DeleteInput) (DeleteResult, error) {
	return func(ctx context.Context, input DeleteInput) (DeleteResult, error) {
		actor, err := principal(ctx)
		if err != nil {
			return DeleteResult{}, err
		}
		revision, err := parseRevision(input.ExpectedRevision)
		if err != nil {
			return DeleteResult{}, err
		}
		if err := service.Delete(ctx, actor, input.AssetID, revision); err != nil {
			return DeleteResult{}, operationFault(err, DeleteOperation, input.AssetID, nil)
		}
		bridge.Confirm(ctx, input.AssetID)
		return DeleteResult{AssetID: input.AssetID, Outcome: "succeeded"}, nil
	}
}

func reconcile(service *media.Service) func(context.Context, ReconcileInput) (ReconcileResult, error) {
	return func(ctx context.Context, input ReconcileInput) (ReconcileResult, error) {
		actor, err := principal(ctx)
		if err != nil {
			return ReconcileResult{}, err
		}
		limit := DefaultReconcileLimit
		if input.Limit != nil {
			limit = *input.Limit
		}
		result, err := service.Reconcile(ctx, actor, limit)
		if err != nil {
			return ReconcileResult{}, operationFault(err, ReconcileOperation, "", &result)
		}
		bridge.Confirm(ctx, "")
		return ReconcileResult{Processed: result.Processed, Busy: result.Busy}, nil
	}
}

func parseRevision(raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if !decimalRevision.MatchString(raw) || err != nil || value < 1 {
		return 0, inputFault("expected_revision", "Use the unchanged positive decimal-string revision from Status (1..9223372036854775807).", errors.New("invalid canonical Media revision"))
	}
	return value, nil
}

func operationFault(err error, operation, id string, partial *media.ReconcileResult) *bridge.Fault {
	mutation := operation == CreateOperation || operation == DeleteOperation || operation == ReconcileOperation
	outcome := bridge.NotExecuted // Queries attempt no state-changing effect.
	if mutation {
		outcome = bridge.Unknown
	}
	f := &bridge.Fault{
		Code: "media_unavailable", Message: "The Media operation could not produce a confirmed result.",
		Action:  "Inspect authorized Status and protected diagnostics before repeating a mutation; use explicit Reconcile for unfinished work.",
		Outcome: outcome, HTTPStatus: http.StatusServiceUnavailable, Cause: err,
	}
	if generatedID.MatchString(id) {
		f.Reference = id
	}
	var input *media.InputError
	if errors.As(err, &input) {
		field := input.Field()
		if field == "revision" {
			field = "expected_revision"
		}
		f.Fields = map[string]string{field: inputGuidance(field), "reason": input.Reason()}
	}
	partialEffects := partial != nil && partial.Processed > 0
	createIntent := operation == CreateOperation && id != ""
	switch {
	case mutation && errors.Is(err, media.ErrUnknownOutcome):
		// A joined input/cleanup error cannot erase the owning unknown result.
		f.Code, f.Message, f.Outcome = "outcome_unknown", "The Media mutation may have changed durable state and requires reconciliation.", bridge.Unknown
	case errors.Is(err, achrix.ErrDenied):
		f.Code, f.Message, f.HTTPStatus = "denied", "Your product account cannot perform this Media operation.", http.StatusForbidden
		f.Action = "Ask the product administrator to review the current operation, library or exact-asset permission."
		if !createIntent && !partialEffects {
			f.Outcome, f.Reference = bridge.NotExecuted, ""
		}
	case errors.Is(err, achrix.ErrAuthorizationUnavailable):
		f.Code, f.Message = "authorization_unavailable", "Current Media permissions could not be evaluated."
		f.Action = "Provide the request ID to an authorized operator to inspect the correlated policy failure."
		if !createIntent && !partialEffects {
			f.Outcome, f.Reference = bridge.NotExecuted, ""
		}
	case errors.Is(err, media.ErrInput):
		f.Code, f.Message, f.HTTPStatus = "invalid_arguments", "The Media request or file does not satisfy the supported input contract.", http.StatusBadRequest
		f.Action = "Correct the indicated field or enabled file-format rule; retain any returned asset ID and inspect its state before another upload."
		if !createIntent && !partialEffects {
			f.Outcome, f.Reference = bridge.NotExecuted, ""
		}
	case errors.Is(err, media.ErrNotFound):
		f.Code, f.Message, f.HTTPStatus = "asset_not_found", "No readable asset was found for this authorized target or state.", http.StatusNotFound
		f.Action = "Check the exact asset ID and authorized Status; absence does not prove an earlier mutation was never executed."
	case errors.Is(err, media.ErrConflict):
		f.Code, f.Message, f.HTTPStatus = "conflict", "The asset revision, lifecycle state or current storage activity conflicts with this request.", http.StatusConflict
		f.Action = "Inspect authorized Status and active transfers; keep the original precondition while reconciling an uncertain mutation."
	case errors.Is(err, media.ErrLimited):
		f.Code, f.Message, f.HTTPStatus = "busy", "The Media service has reached its bounded active-operation capacity.", http.StatusTooManyRequests
		f.Action = "Wait for capacity and inspect any known asset state before choosing another mutation."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		f.Code, f.Message = "deadline_exceeded", "The Media operation ended before a complete response was available."
		// Delete's argument is caller-selected. A bare cancellation can occur
		// before Core grants that target, so it is not an authorized reference.
		// The owning unknown-outcome case above and Create's generated ID retain
		// stronger post-intent evidence without requiring a new Read permission.
		if operation == DeleteOperation {
			f.Reference = ""
		}
	case errors.Is(err, achrix.ErrNotReady):
		f.Message, f.Action = "The application is not ready for Media operations.", "Check application readiness before trying a new operation."
		if !createIntent && !partialEffects {
			f.Outcome, f.Reference = bridge.NotExecuted, ""
		}
	}
	if partial != nil {
		if f.Fields == nil {
			f.Fields = make(map[string]string)
		}
		f.Fields["processed"] = strconv.Itoa(partial.Processed)
		f.Fields["busy"] = strconv.Itoa(partial.Busy)
	}
	return f
}

func inputGuidance(field string) string {
	switch field {
	case "filename":
		return "Use bounded UTF-8 text without paths, outer whitespace, control or bidirectional override characters."
	case "file":
		return "Send 1 byte..10 MiB matching an enabled format and its documented content/image bounds."
	case "asset_id":
		return "Use the exact 26-character generated uppercase base32 asset ID."
	case "expected_revision":
		return "Use the unchanged positive decimal-string revision returned by Status."
	case "cursor":
		return "Use an unchanged List cursor, or omit it to restart traversal."
	case "limit":
		return "Use 1..100 for List or 1..40 for Reconcile."
	default:
		return "Match this operation's published input contract; inspect the source-owned reason."
	}
}

func inputFault(field, guidance string, cause error) *bridge.Fault {
	return &bridge.Fault{
		Code: "invalid_arguments", Message: "The request does not satisfy this Media operation's input contract.",
		Action: "Correct the indicated field using the published HTTP or MCP schema.",
		Fields: map[string]string{field: guidance}, Outcome: bridge.NotExecuted,
		HTTPStatus: http.StatusBadRequest, Cause: cause,
	}
}
