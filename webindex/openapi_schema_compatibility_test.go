package webindex

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestProjectOpenAPIRejectsTypeIncompatibleOverrideSchemas verifies every public contract override reaches recursive schema-instance validation.
func TestProjectOpenAPIRejectsTypeIncompatibleOverrideSchemas(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:  "POST",
		Path:    "/widgets",
		Handler: HandlerRef{Package: "widgets", Function: "Create"},
		Inputs: InputShape{QueryParams: []Parameter{{
			Name: "mode",
			In:   "query",
		}}},
		Outputs: OutputShape{Responses: []ResponseShape{{
			StatusCode: 200,
			Schema:     map[string]any{"type": "object"},
		}}},
	}}}

	_, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match: OpenAPIOperationSelector{Function: "Create"},
		Parameters: []OpenAPIParameterOverride{{
			In:     "query",
			Name:   "mode",
			Schema: map[string]any{"type": "string", "default": 42},
		}},
		RequestBody: &OpenAPIRequestBodyOverride{
			MediaType: "application/json",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"state": map[string]any{"type": "string", "enum": []any{"ready", 42}},
				},
			},
		},
		Responses: map[string]OpenAPIResponseOverride{
			"200": {
				MediaType: "application/json",
				Schema:    map[string]any{"type": "string", "example": 42},
			},
		},
	}}})
	var projectionError *OpenAPIProjectionError
	if !errors.As(err, &projectionError) {
		t.Fatalf("expected OpenAPIProjectionError, got %v", err)
	}
	for _, problem := range []string{
		`parameter query "mode" default must conform to schema type "string"`,
		`property "state" enum[1] must conform to schema type "string"`,
		`response 200 media type "application/json" example must conform to schema type "string"`,
	} {
		if !strings.Contains(err.Error(), problem) {
			t.Errorf("projection error does not contain %q: %v", problem, err)
		}
	}
}

// TestProjectOpenAPIAcceptsCompatibleOverrideSchemaValues verifies nullable, numeric, and string-form example semantics remain usable through the public API.
func TestProjectOpenAPIAcceptsCompatibleOverrideSchemaValues(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:  "GET",
		Path:    "/widgets",
		Handler: HandlerRef{Package: "widgets", Function: "List"},
		Outputs: OutputShape{Responses: []ResponseShape{{StatusCode: 200}}},
	}}}
	tests := []struct {
		name   string
		schema map[string]any
	}{
		{
			name: "integer-valued JSON decimals",
			schema: map[string]any{
				"type":    "integer",
				"default": json.Number("42.0"),
				"enum":    []any{json.Number("1e2")},
				"example": json.Number("3.0"),
			},
		},
		{
			name: "nullable string values",
			schema: map[string]any{
				"type":     "string",
				"nullable": true,
				"default":  nil,
				"enum":     []any{"ready", nil},
				"example":  nil,
			},
		},
		{
			name: "string-form non-JSON example",
			schema: map[string]any{
				"type":    "object",
				"default": map[string]any{"ready": true},
				"enum":    []any{map[string]any{"ready": true}},
				"example": "<widget ready=\"true\" />",
			},
		},
		{
			name: "type-omitted composition",
			schema: map[string]any{
				"default": 42,
				"enum":    []any{"ready", 42},
				"example": true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
				Match: OpenAPIOperationSelector{Function: "List"},
				Responses: map[string]OpenAPIResponseOverride{
					"200": {MediaType: "application/json", Schema: test.schema},
				},
			}}})
			if err != nil {
				t.Fatalf("compatible explicit schema failed projection: %v", err)
			}
		})
	}
}
