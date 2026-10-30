// SPDX-License-Identifier: MPL-2.0
package remote

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed openapi.json
var openAPI []byte

// OpenAPI returns an independent copy of the published OpenAPI 3.1.1 contract.
// Its example server URL must be replaced with the product's HTTP ResourceURL.
// Serving this static document does not grant access to any operation.
func OpenAPI() []byte { return append([]byte(nil), openAPI...) }

func publishedSchemas() (map[string]json.RawMessage, error) {
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPI, &document); err != nil {
		return nil, fmt.Errorf("multisite remote: published schema: %w", err)
	}
	for _, name := range []string{"InventoryInput", "SiteInput", "SiteInfo", "InventoryPage"} {
		if len(document.Components.Schemas[name]) == 0 {
			return nil, fmt.Errorf("multisite remote: published %s schema is absent", name)
		}
	}
	return document.Components.Schemas, nil
}
