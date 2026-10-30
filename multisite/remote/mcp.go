// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"errors"

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/multisite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCP composes the two deliberate read tools on Bridge's stateless request
// profile. Use a product verifier for this MCP resource/audience. This does not
// establish a production OAuth issuer, consent flow or hosted-client support.
func NewMCP(service *multisite.Service, configuration bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("multisite remote: service is required")
	}
	return bridge.New(configuration, func(registrar *bridge.Registrar) error {
		return Register(registrar, service)
	})
}

// Register adds Inventory and Site during an existing Bridge constructor's
// registration callback. Its fixed schemas disclose no sites, authorities or
// product configuration. Discovery never grants permission to invoke a tool.
func Register(registrar *bridge.Registrar, service *multisite.Service) error {
	if registrar == nil || service == nil {
		return errors.New("multisite remote: registrar and service are required")
	}
	schemas, err := publishedSchemas()
	if err != nil {
		return err
	}
	closedWorld, nondestructive := false, false
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &nondestructive, OpenWorldHint: &closedWorld}
	if err := bridge.AddTool(registrar, &mcp.Tool{
		Name:        InventoryOperation,
		Description: "List only sites readable by your current product account. The collection and each exact site are authorized on every page. A cursor is a traversal position, not a grant or snapshot; omit it to start over after permission changes. An omitted limit selects 20; the maximum is 100. No totals or denied-site identifiers are disclosed.",
		InputSchema: schemas["InventoryInput"], OutputSchema: schemas["InventoryPage"],
		Annotations: annotations,
	}, inventory(service)); err != nil {
		return err
	}
	return bridge.AddTool(registrar, &mcp.Tool{
		Name:        SiteOperation,
		Description: "Read one exact case-sensitive site ID using your current product account permissions. Authorization precedes existence lookup. The result includes its routing-disabled flag, selected module IDs and opaque configuration revision. Selecting a site grants no authority and performs no activation or provisioning.",
		InputSchema: schemas["SiteInput"], OutputSchema: schemas["SiteInfo"],
		Annotations: annotations,
	}, site(service))
}
