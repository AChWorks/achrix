// SPDX-License-Identifier: MPL-2.0
package remote

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed openapi.json
var openAPI []byte

// OpenAPI returns a defensive copy of the published HTTP contract. Its example
// server URL must be replaced with the product's configured HTTP ResourceURL.
func OpenAPI() []byte { return append([]byte(nil), openAPI...) }

func publishedSchemas() (map[string]json.RawMessage, error) {
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPI, &document); err != nil {
		return nil, fmt.Errorf("media remote: published schema: %w", err)
	}
	for _, name := range []string{"ListInput", "StatusInput", "DeleteInput", "ReconcileInput", "Page", "AssetInfo", "DeleteResult", "ReconcileResult"} {
		if len(document.Components.Schemas[name]) == 0 {
			return nil, fmt.Errorf("media remote: published %s schema is absent", name)
		}
	}
	return document.Components.Schemas, nil
}
