// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"errors"

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCP composes explicit metadata and lifecycle tools. The trusted
// httpResourceURL identifies a separately authenticated companion for original
// bytes and uploads; it is never fetched or derived from caller arguments.
func NewMCP(service *media.Service, config bridge.Config, httpResourceURL string) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("media remote: service is required")
	}
	if _, err := referenceResource(httpResourceURL); err != nil {
		return nil, err
	}
	return bridge.New(config, func(registrar *bridge.Registrar) error { return Register(registrar, service, httpResourceURL) })
}

// Register adds only List, Status, Delete and Reconcile during Bridge's fixed
// registration callback. It adds no MCP attachment upload, resource fetch,
// subscription, public URL, permission registry or background work.
func Register(registrar *bridge.Registrar, service *media.Service, httpResourceURL string) error {
	if registrar == nil || service == nil {
		return errors.New("media remote: registrar and service are required")
	}
	resource, err := referenceResource(httpResourceURL)
	if err != nil {
		return err
	}
	schemas, err := publishedSchemas()
	if err != nil {
		return err
	}
	closedWorld, nondestructive, destructive := false, false, true
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &nondestructive, OpenWorldHint: &closedWorld}
	mutation := &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld}
	if err := bridge.AddTool(registrar, &mcp.Tool{
		Name:        ListOperation,
		Description: "List ready whole-library metadata using your current product List permission. Filenames are included, but listing does not grant per-asset Status, original-byte Read or Delete. Every page reauthorizes the library; cursors are positions, not a snapshot or grant. Omitted limit selects 20; maximum 100.",
		InputSchema: schemas["ListInput"], OutputSchema: schemas["Page"], Annotations: readOnly,
	}, list(service)); err != nil {
		return err
	}
	if err := bridge.AddTool(registrar, &mcp.Tool{
		Name:        StatusOperation,
		Description: "Read the durable pending/ready/deleting/deleted state of one exact asset using current Read permission. A ready result includes a canonical companion HTTP content reference requiring its own resource-specific credential and fresh authorization; it is not a bearer URL. Deleted state retains only ID, time, state and revision. Revision is an exact decimal string. Status is used to inspect uncertain mutations, not to justify automatic replay.",
		InputSchema: schemas["StatusInput"], OutputSchema: schemas["AssetInfo"], Annotations: readOnly,
	}, status(service, resource)); err != nil {
		return err
	}
	if err := bridge.AddTool(registrar, &mcp.Tool{
		Name:        DeleteOperation,
		Description: "Conditionally delete one exact asset using current Delete permission and its unchanged decimal-string revision from Status. A conflict may mean stale revision, lifecycle state or active storage work. Preserve the original precondition and inspect Status after an unknown result; never automatically repeat deletion or substitute a newer revision.",
		InputSchema: schemas["DeleteInput"], OutputSchema: schemas["DeleteResult"], Annotations: mutation,
	}, deleteAsset(service)); err != nil {
		return err
	}
	return bridge.AddTool(registrar, &mcp.Tool{
		Name:        ReconcileOperation,
		Description: "Run one explicitly authorized library-wide maintenance batch: abort interrupted pending uploads and finish deleting assets. It never publishes incomplete uploads. Omitted limit selects 10; maximum 40. Busy locked work is counted and untouched. A failure can follow partial effects; retain processed/busy counts and inspect state before deciding whether to run another bounded batch. This is not an automatic retry engine.",
		InputSchema: schemas["ReconcileInput"], OutputSchema: schemas["ReconcileResult"], Annotations: mutation,
	}, reconcile(service))
}
