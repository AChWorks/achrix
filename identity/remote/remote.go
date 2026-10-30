// SPDX-License-Identifier: MPL-2.0

// Package remote exposes selected account administration through optional typed
// HTTP and MCP bindings. Identity owns permissions, preconditions, session
// revocation and the atomic Audit append; this package owns only transport.
package remote

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
)

const (
	AccountOperation    = identity.AccountRead
	SetEnabledOperation = identity.AccountSetEnabled
)

type AccountInput struct {
	AccountID string `json:"account_id"`
}
type SetEnabledInput struct {
	AccountID        string `json:"account_id"`
	ExpectedRevision string `json:"expected_revision"`
	Enabled          bool   `json:"enabled"`
}
type AccountInfo struct {
	ID       string `json:"id"`
	Login    string `json:"login"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}
type SetEnabledResult struct {
	AccountID string `json:"account_id"`
	Outcome   string `json:"outcome"`
}

var decimalRevision = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var accountID = regexp.MustCompile(`^[A-Z2-7]{26}$`)

func revision(raw string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if !decimalRevision.MatchString(raw) || err != nil || n < 1 {
		return 0, inputFault("expected_revision", "Use the unchanged positive decimal-string revision from Account (1..9223372036854775807).", errors.New("invalid canonical account revision"))
	}
	return n, nil
}

func verified(ctx context.Context) (bridge.Request, error) {
	r, ok := bridge.RequestFromContext(ctx)
	if !ok || r.Caller.Principal == "" {
		return bridge.Request{}, &bridge.Fault{Code: "unauthenticated", Message: "A verified product account is required.", Action: "Use the configured authenticated Identity HTTP or MCP endpoint.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusUnauthorized, Cause: errors.New("identity remote invocation lacks verified request context")}
	}
	return r, nil
}

func account(service *identity.Service) func(context.Context, AccountInput) (AccountInfo, error) {
	return func(ctx context.Context, input AccountInput) (AccountInfo, error) {
		r, err := verified(ctx)
		if err != nil {
			return AccountInfo{}, err
		}
		a, err := service.Account(ctx, r.Caller.Principal, input.AccountID)
		if err != nil {
			return AccountInfo{}, operationFault(err, false, "")
		}
		bridge.Confirm(ctx, a.ID)
		return AccountInfo{ID: a.ID, Login: a.Login, Enabled: a.Enabled, Revision: strconv.FormatInt(a.Revision, 10)}, nil
	}
}

func setEnabled(service *identity.Service) func(context.Context, SetEnabledInput) (SetEnabledResult, error) {
	return func(ctx context.Context, input SetEnabledInput) (SetEnabledResult, error) {
		r, err := verified(ctx)
		if err != nil {
			return SetEnabledResult{}, err
		}
		expected, err := revision(input.ExpectedRevision)
		if err != nil {
			return SetEnabledResult{}, err
		}
		// Only verified, server-owned context enters durable attribution. Neither
		// the input DTO nor forwarded/client metadata supplies these fields.
		attributed, err := audit.WithAttribution(ctx, r.Caller.Principal, audit.Attribution{
			ClientID: r.Caller.ClientID, ConnectionID: r.Caller.ConnectionID, Channel: r.Channel, RequestID: r.ID, Operation: r.Operation,
		})
		if err != nil {
			return SetEnabledResult{}, &bridge.Fault{Code: "attribution_unavailable", Message: "Trusted accountability context could not be established.", Action: "Ask the product operator to check verified internal account and client mapping before submitting another change.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusServiceUnavailable, Cause: err}
		}
		if err := service.SetEnabled(attributed, r.Caller.Principal, input.AccountID, expected, input.Enabled); err != nil {
			return SetEnabledResult{}, operationFault(err, true, input.AccountID)
		}
		bridge.Confirm(ctx, input.AccountID)
		return SetEnabledResult{AccountID: input.AccountID, Outcome: "succeeded"}, nil
	}
}

func inputFault(field, guidance string, cause error) *bridge.Fault {
	return &bridge.Fault{Code: "invalid_arguments", Message: "The account request does not satisfy its input contract.", Action: "Correct the indicated field using the published Identity HTTP or MCP schema.", Fields: map[string]string{field: guidance}, Outcome: bridge.NotExecuted, HTTPStatus: http.StatusBadRequest, Cause: cause}
}

func operationFault(err error, mutation bool, id string) *bridge.Fault {
	f := &bridge.Fault{Code: "identity_unavailable", Message: "The Identity operation could not produce a confirmed result.", Action: "Inspect authorized Account and Audit records with the original precondition; do not automatically repeat the change or substitute a newer revision.", Outcome: bridge.NotExecuted, HTTPStatus: http.StatusServiceUnavailable, Cause: err}
	if mutation {
		f.Outcome = bridge.Unknown
		if accountID.MatchString(id) {
			f.Reference = id
		}
	}
	var field *identity.InputError
	if errors.As(err, &field) {
		name := field.Field
		if name == "id" {
			name = "account_id"
		}
		guidance := "Use the published source-owned field rule."
		switch name {
		case "account_id":
			guidance = "Use the exact 26-character generated account ID."
		case "expected_revision":
			guidance = "Use the unchanged positive decimal-string revision returned by Account."
		}
		f.Fields = map[string]string{name: guidance, "reason": field.Reason}
	}
	switch {
	case errors.Is(err, achrix.ErrDenied):
		f.Code, f.Message, f.Action, f.HTTPStatus = "denied", "Your product account cannot perform this account operation.", "Ask the product administrator to review the current exact-account operation and required Audit append permission.", http.StatusForbidden
		f.Outcome, f.Reference = bridge.NotExecuted, ""
	case errors.Is(err, achrix.ErrAuthorizationUnavailable):
		f.Code, f.Message, f.Action = "authorization_unavailable", "Current account permissions could not be evaluated.", "Provide the request ID to an authorized operator to inspect the correlated policy failure."
		f.Outcome, f.Reference = bridge.NotExecuted, ""
	case errors.Is(err, identity.ErrInvalid), errors.Is(err, audit.ErrInput):
		f.Code, f.Message, f.HTTPStatus = "invalid_arguments", "The account request does not satisfy the owning input contract.", http.StatusBadRequest
		f.Outcome, f.Reference = bridge.NotExecuted, ""
	case errors.Is(err, identity.ErrConflict):
		// Account intentionally uses the same owning conflict for an authorized
		// absent ID. Do not manufacture a public existence distinction.
		f.Code, f.Message, f.HTTPStatus = "conflict", "The account or its exact revision does not satisfy this operation.", http.StatusConflict
		f.Action = "Inspect authorized Account state; retain the original precondition while reconciling an uncertain change. Do not automatically substitute a newer revision."
	case errors.Is(err, identity.ErrLimited), errors.Is(err, audit.ErrLimited):
		f.Code, f.Message, f.HTTPStatus = "busy", "The account operation has reached its bounded active capacity.", http.StatusTooManyRequests
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		f.Code, f.Message = "deadline_exceeded", "The account operation ended before a complete response was available."
		// Cancellation can precede successful authorization. The supplied ID
		// alone is not evidence of a verified recovery reference.
		f.Reference = ""
	case errors.Is(err, achrix.ErrNotReady):
		f.Message, f.Action = "The application is not ready for account operations.", "Check application readiness before submitting another operation."
		f.Outcome, f.Reference = bridge.NotExecuted, ""
	}
	return f
}
