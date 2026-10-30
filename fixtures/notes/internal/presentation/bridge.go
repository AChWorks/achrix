// SPDX-License-Identifier: MPL-2.0
package presentation

import (
	"context"
	"errors"

	"example.com/achrix-notes/internal/application"
	"example.com/achrix-notes/internal/diagnosis"
	"example.com/achrix-notes/internal/domain"
	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createNoteInput struct {
	Text string `json:"text"`
}

type readNoteInput struct {
	ID string `json:"id"`
}

// NewBridge deliberately exposes the same immutable Notes operations used by
// HTTP and direct callers. Configuration belongs to the product: its verifier
// supplies the account/client identity, and its current admission rules may
// narrow access without replacing the Service's ordinary authorization.
// This adapter neither issues credentials nor implements product login/consent.
func NewBridge(service *application.Service, configuration bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("notes service is required")
	}
	return bridge.New(configuration, func(registrar *bridge.Registrar) error {
		closedWorld, nondestructive := false, false
		if err := bridge.AddTool(registrar, &mcp.Tool{
			Name:        application.Create,
			Description: "Create one immutable note using your current product account permissions. Creation is not idempotent: reconcile an uncertain write with its returned note ID before considering another create.",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"text": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200,
					"description": "Non-blank UTF-8 note text, at most 200 Unicode characters, without NUL.",
				}},
				"required": []string{"text"},
			},
			Annotations: &mcp.ToolAnnotations{DestructiveHint: &nondestructive, OpenWorldHint: &closedWorld},
		}, func(ctx context.Context, input createNoteInput) (domain.Note, error) {
			ctx, request, err := notesBridgeContext(ctx)
			if err != nil {
				return domain.Note{}, err
			}
			note, err := service.Create(ctx, request.Caller.Principal, input.Text)
			if err != nil {
				// An error result's typed output is not an MCP success payload. Keep
				// the authorized reconciliation ID explicitly in the safe fault.
				return domain.Note{}, notesBridgeFault(err, note.ID, true)
			}
			bridge.Confirm(ctx, note.ID)
			return note, nil
		}); err != nil {
			return err
		}
		return bridge.AddTool(registrar, &mcp.Tool{
			Name:        application.Read,
			Description: "Read an immutable note by its exact ID using current product permissions. A missing read does not establish that an earlier uncertain create was never executed.",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"id": map[string]any{
					"type": "string", "minLength": 26, "maxLength": 64, "pattern": "^[A-Z2-7]{26,64}$",
					"description": "The opaque note identifier returned by this service.",
				}},
				"required": []string{"id"},
			},
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
		}, func(ctx context.Context, input readNoteInput) (domain.Note, error) {
			ctx, request, err := notesBridgeContext(ctx)
			if err != nil {
				return domain.Note{}, err
			}
			note, err := service.Read(ctx, request.Caller.Principal, input.ID)
			if err != nil {
				return domain.Note{}, notesBridgeFault(err, "", false)
			}
			bridge.Confirm(ctx, note.ID)
			return note, nil
		})
	})
}

func notesBridgeContext(ctx context.Context) (context.Context, bridge.Request, error) {
	request, ok := bridge.RequestFromContext(ctx)
	if !ok {
		return ctx, bridge.Request{}, &bridge.Fault{
			Code: "unauthenticated", Message: "A verified product account is required.",
			Action: "Reconnect using your own product account.", Outcome: bridge.NotExecuted,
		}
	}
	// Only the verified transport's generated identity enters the Notes logger.
	// No request header or tool argument is accepted as diagnostic authority.
	return diagnosis.WithRequestID(ctx, request.ID), request, nil
}

func notesBridgeFault(err error, reference string, mutation bool) *bridge.Fault {
	fault := &bridge.Fault{
		Code: "unavailable", Message: "The Notes service could not complete the operation.",
		Action:  "Check the service diagnostics before trying again.",
		Outcome: bridge.Unknown, Reference: reference, Cause: err,
	}
	switch {
	case errors.Is(err, achrix.ErrDenied):
		fault.Code, fault.Message = "permission_denied", "Your product account cannot perform this Notes operation."
		fault.Action, fault.Outcome = "Ask the product administrator to review your current account permissions.", bridge.NotExecuted
	case errors.Is(err, achrix.ErrAuthorizationUnavailable):
		fault.Code, fault.Message = "authorization_unavailable", "The current account permissions could not be checked."
		fault.Action, fault.Outcome = "Wait for authorization service recovery before trying again.", bridge.NotExecuted
	case errors.Is(err, achrix.ErrNotReady):
		fault.Message = "The Notes application is not ready."
		fault.Action, fault.Outcome = "Check service readiness before trying again.", bridge.NotExecuted
	case errors.Is(err, domain.ErrInvalid):
		fault.Code, fault.Message = "invalid_input", "Correct the invalid Notes input."
		fault.Action, fault.Outcome = "Use the field details to correct the input.", bridge.NotExecuted
		var input *domain.InputError
		if errors.As(err, &input) {
			fault.Fields = map[string]string{"field": input.Field, "detail": input.Problem}
		}
	case errors.Is(err, domain.ErrNotFound):
		fault.Code, fault.Message = "not_found", "No note was found for this identifier."
		fault.Action = "Check the exact note ID. A missing read does not prove an earlier create did not commit."
	case errors.Is(err, domain.ErrNotExecuted):
		fault.Message = "The note write was not executed."
		fault.Action, fault.Outcome = "Check service readiness before trying again.", bridge.NotExecuted
	case errors.Is(err, domain.ErrNotCommitted):
		fault.Message = "The note write was rejected without a commit."
		fault.Action, fault.Outcome = "Ask the operator to resolve the rejected write before trying again.", bridge.NotCommitted
	case errors.Is(err, domain.ErrUnknownOutcome):
		fault.Code, fault.Message = "write_outcome_unknown", "The note write may have committed."
		fault.Action = "Read the returned note ID to reconcile. Do not automatically repeat Create; ask the operator if the read remains inconclusive."
	case mutation && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)):
		// Create returns these directly only before Save. A persistence failure
		// is classified by the owning Service as one of the write outcomes above.
		fault.Message = "The request ended before the note write was invoked."
		fault.Action, fault.Outcome = "Check request and service availability before trying again.", bridge.NotExecuted
	}
	return fault
}
