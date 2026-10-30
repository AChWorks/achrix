// SPDX-License-Identifier: MPL-2.0
package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type inputField struct {
	name   string
	schema *jsonschema.Schema
}
type inputDiagnostic struct {
	resolved *jsonschema.Resolved
	paths    map[string]inputField
}

// SDK v1.8.0 uses this same inference/validator, but its input failure path
// formats the validation cause with %v, erasing the unwrap chain. Snapshot the
// schema once, supply it to SDK, and retain only diagnostic metadata. Successful
// requests do not validate twice. There is no additional schema implementation
// or operation dispatcher here.
func prepareInput[In any](tool *mcp.Tool) (*inputDiagnostic, error) {
	var schema *jsonschema.Schema
	if tool.InputSchema == nil {
		if reflect.TypeFor[In]() == reflect.TypeFor[any]() {
			schema = &jsonschema.Schema{Type: "object"}
		} else {
			t := reflect.TypeFor[In]()
			if t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			var err error
			schema, err = jsonschema.ForType(t, &jsonschema.ForOptions{})
			if err != nil {
				return nil, err
			}
		}
	} else {
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &schema); err != nil {
			return nil, err
		}
	}
	if schema == nil {
		return nil, errors.New("input schema is nil")
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, err
	}
	tool.InputSchema = schema
	d := &inputDiagnostic{resolved: resolved, paths: make(map[string]inputField)}
	var collect func(*jsonschema.Schema, string, string, int)
	collect = func(s *jsonschema.Schema, path, name string, depth int) {
		if s == nil || depth > 8 || len(d.paths) >= 128 {
			return
		}
		key := path
		if s.ID != "" {
			key = s.ID
		}
		d.paths[key] = inputField{name: name, schema: s}
		for _, property := range sortedInputNames(s.Properties) {
			child := s.Properties[property]
			if !safeCode(property) {
				continue
			}
			prefix := path
			if prefix == "root" {
				prefix = ""
			}
			field := property
			if name != "arguments" {
				field = name + "." + property
			}
			if len(field) > 64 {
				continue
			}
			collect(child, prefix+"/properties/"+property, field, depth+1)
		}
	}
	collect(schema, "root", "arguments", 0)
	return d, nil
}

// details runs only after SDK rejection. The maintained validator determines
// whether and where validation failed. We project recognized constraint names
// and bounded field names, never its Error string, which can contain raw values.
// Unrecognized complex schema paths retain a schema-level constraint hint.
func (d *inputDiagnostic) details(arguments json.RawMessage) (map[string]string, error) {
	value := make(map[string]any)
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &value); err != nil {
			return map[string]string{"arguments": "Send a JSON object matching the tool input schema."}, err
		}
	}
	var instance any = value
	if err := d.resolved.ApplyDefaults(&instance); err != nil {
		return map[string]string{"arguments": "The declared input defaults could not be applied."}, err
	}
	err := d.resolved.Validate(&instance)
	if err == nil {
		return nil, nil
	}
	field := d.paths["root"]
	if field.schema == nil {
		field = inputField{name: "arguments", schema: d.resolved.Schema()}
	}
	leaf := err
	for child := errors.Unwrap(leaf); child != nil; child = errors.Unwrap(leaf) {
		prefix := strings.TrimSuffix(leaf.Error(), ": "+child.Error())
		if path, ok := strings.CutPrefix(prefix, "validating "); ok {
			if known, ok := d.paths[path]; ok {
				field = known
			}
		}
		leaf = child
	}
	message := leaf.Error()
	constraint, _, _ := strings.Cut(message, ":")
	fields := map[string]string{}
	if strings.HasPrefix(message, "unexpected additional properties ") {
		// The recognized upstream leaf contains property names only, not
		// values. Return bounded simple names actually named by that leaf.
		for name := range value {
			if len(fields) >= 16 {
				break
			}
			if safeCode(name) && strings.Contains(message, strconv.Quote(name)) {
				fields[name] = "Remove this unrecognized field (additionalProperties)."
			}
		}
		if len(fields) == 0 {
			fields[field.name] = "Remove fields not permitted by the schema (additionalProperties)."
		}
		return fields, err
	}
	if constraint == "required" {
		for _, name := range field.schema.Required {
			if len(fields) >= 16 {
				break
			}
			if safeCode(name) && strings.Contains(message, strconv.Quote(name)) {
				path := name
				if field.name != "arguments" {
					path = field.name + "." + name
				}
				if len(path) <= 64 {
					fields[path] = "This field is required."
				}
			}
		}
		if len(fields) == 0 {
			fields[field.name] = "Provide the required schema fields."
		}
		return fields, err
	}
	guidance := "Correct this field to satisfy its published schema."
	switch constraint {
	case "type":
		if field.schema.Type != "" {
			guidance = "Expected JSON type " + field.schema.Type + "."
		} else {
			guidance = "Use one of the declared JSON types."
		}
	case "minLength":
		guidance = "The value is shorter than the declared minLength."
		if field.schema.MinLength != nil {
			guidance = fmt.Sprintf("Use at least %d Unicode characters (minLength).", *field.schema.MinLength)
		}
	case "maxLength":
		guidance = "The value is longer than the declared maxLength."
		if field.schema.MaxLength != nil {
			guidance = fmt.Sprintf("Use at most %d Unicode characters (maxLength).", *field.schema.MaxLength)
		}
	case "enum", "const", "pattern", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minItems", "maxItems", "uniqueItems", "contains", "minContains", "maxContains", "minProperties", "maxProperties", "anyOf", "oneOf", "allOf", "not":
		guidance = "Satisfy the published " + constraint + " constraint for this field."
	}
	fields[field.name] = guidance
	return fields, err
}

// Stable order is useful for constructor tests and bounded field selection.
func sortedInputNames(m map[string]*jsonschema.Schema) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
