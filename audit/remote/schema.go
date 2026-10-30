// SPDX-License-Identifier: MPL-2.0
package remote

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed openapi.json
var openAPI []byte

// OpenAPI returns a defensive copy of the published OpenAPI 3.1.1 contract.
// Replace its example server with the product's HTTP resource when publishing.
func OpenAPI() []byte { return append([]byte(nil), openAPI...) }

func publishedSchemas() (map[string]json.RawMessage, error) {
	var document struct {
		Components struct{ Schemas map[string]json.RawMessage }
	}
	if err := json.Unmarshal(openAPI, &document); err != nil {
		return nil, fmt.Errorf("audit remote: published schema: %w", err)
	}
	for _, name := range []string{"PageInput", "RecordInfo", "AttributionInfo", "Page"} {
		if len(document.Components.Schemas[name]) == 0 {
			return nil, fmt.Errorf("audit remote: published %s schema is absent", name)
		}
	}
	return document.Components.Schemas, nil
}
