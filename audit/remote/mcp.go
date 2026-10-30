// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"errors"

	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCP composes two explicit target-scoped reads on the request-only Bridge
// profile. It adds no append, edit, deletion, export job or resource subscription.
func NewMCP(service *audit.Service, cfg bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("audit remote: service is required")
	}
	return bridge.New(cfg, func(registrar *bridge.Registrar) error { return Register(registrar, service) })
}

// Register is valid inside a fixed Bridge constructor callback. Discovery and
// annotations do not grant either Query or the separately authorized Export.
func Register(registrar *bridge.Registrar, service *audit.Service) error {
	if registrar == nil || service == nil {
		return errors.New("audit remote: registrar and service are required")
	}
	schemas, err := publishedSchemas()
	if err != nil {
		return err
	}
	nondestructive, closedWorld := false, false
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &nondestructive, OpenWorldHint: &closedWorld}
	if err := bridge.AddTool(registrar, &mcp.Tool{Name: QueryOperation, Description: "Read one bounded page of committed Audit records for an exact target using current Query permission. An omitted limit selects 20; maximum 100. Cursors are target-bound positions, not access grants. Historical/direct records may have no retained attribution. Every page reauthorizes and pages do not form a consistent snapshot.", InputSchema: schemas["PageInput"], OutputSchema: schemas["Page"], Annotations: annotations}, readPage(service.Query)); err != nil {
		return err
	}
	return bridge.AddTool(registrar, &mcp.Tool{Name: ExportOperation, Description: "Read one bounded export page for an exact Audit target using separate current Export permission. This is not an unbounded export job or a snapshot across requests: concurrent inserts can commit out of sequence. For a complete consistent export, use product-owned write quiescence or its supported database snapshot profile. Default limit 20, maximum 100.", InputSchema: schemas["PageInput"], OutputSchema: schemas["Page"], Annotations: annotations}, readPage(service.Export))
}
