// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"errors"
	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/identity"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCP composes only Account and conditional SetEnabled. Passwords, tokens,
// login, account creation and permission management are not tool arguments.
func NewMCP(service *identity.Service, cfg bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("identity remote: service is required")
	}
	return bridge.New(cfg, func(r *bridge.Registrar) error { return Register(r, service) })
}

// Register adds the fixed tools during Bridge construction, without workers.
func Register(r *bridge.Registrar, service *identity.Service) error {
	if r == nil || service == nil {
		return errors.New("identity remote: registrar and service are required")
	}
	s, err := publishedSchemas()
	if err != nil {
		return err
	}
	closed, no, yes := false, false, true
	if err := bridge.AddTool(r, &mcp.Tool{Name: AccountOperation, Description: "Read metadata and the exact decimal-string revision of one account using current exact-target AccountRead permission. No credentials or sessions are returned. Denied targets are not looked up; an authorized missing account retains the owning conflict response. This is not account listing or proof that an earlier uncertain mutation did not occur.", InputSchema: s["AccountInput"], OutputSchema: s["AccountInfo"], Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &closed}}, account(service)); err != nil {
		return err
	}
	return bridge.AddTool(r, &mcp.Tool{Name: SetEnabledOperation, Description: "Conditionally set one account's enabled state using its unchanged decimal-string expected_revision. Current AccountSetEnabled and Audit append permissions are both required. Every successful call advances the revision once, revokes all sessions and atomically appends trusted accountability, even when enabled is unchanged. The call is not idempotent. After uncertainty inspect authorized Account/Audit state with the original precondition; never automatically retry or substitute a newer revision. Success does not require AccountRead permission.", InputSchema: s["SetEnabledInput"], OutputSchema: s["SetEnabledResult"], Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes, OpenWorldHint: &closed}}, setEnabled(service))
}
