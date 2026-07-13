package webindex

import (
	"reflect"
	"strings"
	"testing"
)

// TestProjectOpenAPIBindBodyDefaultsRemainOptionalAndMediaHonest verifies inference does not invent either requiredness or an exclusive JSON contract.
func TestProjectOpenAPIBindBodyDefaultsRemainOptionalAndMediaHonest(t *testing.T) {
	manifest := Manifest{
		Schemas: []Schema{{
			Identity: "example.com/items.CreateInput",
			Name:     "ItemsCreateInput",
			Definition: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		}},
		Operations: []Operation{{
			Method:  "POST",
			Path:    "/items",
			Handler: HandlerRef{Package: "items", Receiver: "Controller", Function: "Create"},
			Inputs: InputShape{Body: &BodyShape{
				TypeName: "CreateInput",
				Schema:   map[string]any{"$ref": "#/components/schemas/ItemsCreateInput"},
				Source:   "web.Bind",
			}},
		}},
	}

	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{})
	if err != nil {
		t.Fatalf("ProjectOpenAPI failed: %v", err)
	}
	body := document.Paths["/items"]["post"].RequestBody
	if _, required := body["required"]; required {
		t.Fatalf("Bind inference must not make a request body required: %+v", body)
	}
	content := body["content"].(map[string]any)
	if len(content) != 2 || content["*/*"] == nil || content["application/json"] == nil {
		t.Fatalf("typed Bind must retain typed JSON and an unconstrained media fallback: %+v", content)
	}
	jsonSchema := content["application/json"].(map[string]any)["schema"]
	if !reflect.DeepEqual(jsonSchema, map[string]any{"$ref": "#/components/schemas/ItemsCreateInput"}) {
		t.Fatalf("typed JSON evidence was lost: %+v", jsonSchema)
	}

	required := true
	refined, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match: OpenAPIOperationSelector{Function: "Create", Method: "POST", Path: "/items"},
		RequestBody: &OpenAPIRequestBodyOverride{
			Required:  &required,
			MediaType: "application/json",
			Schema:    map[string]any{"type": "object", "required": []string{"name"}},
		},
	}}})
	if err != nil {
		t.Fatalf("refine request body: %v", err)
	}
	refinedBody := refined.Paths["/items"]["post"].RequestBody
	if refinedBody["required"] != true {
		t.Fatalf("explicit requiredness was not applied: %+v", refinedBody)
	}
	refinedContent := refinedBody["content"].(map[string]any)
	if refinedContent["*/*"] == nil || refinedContent["application/json"] == nil {
		t.Fatalf("refinement discarded honest fallback media: %+v", refinedContent)
	}

	removed, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match:       OpenAPIOperationSelector{Function: "Create"},
		RequestBody: &OpenAPIRequestBodyOverride{Remove: true},
	}}})
	if err != nil {
		t.Fatalf("remove request body: %v", err)
	}
	if removed.Paths["/items"]["post"].RequestBody != nil {
		t.Fatalf("request body removal was not applied: %+v", removed.Paths["/items"]["post"].RequestBody)
	}
	if removed.Components != nil {
		t.Fatalf("request body removal left an orphan schema component: %+v", removed.Components)
	}

	unknownManifest := Manifest{Operations: []Operation{{
		Method:  "POST",
		Path:    "/opaque",
		Handler: HandlerRef{Package: "items", Function: "Opaque"},
		Inputs:  InputShape{Body: &BodyShape{TypeName: "Opaque", Source: "web.Bind"}},
	}}}
	unknown, err := ProjectOpenAPI(unknownManifest, OpenAPIOptions{})
	if err != nil {
		t.Fatalf("project unresolved Bind body: %v", err)
	}
	unknownContent := unknown.Paths["/opaque"]["post"].RequestBody["content"].(map[string]any)
	if len(unknownContent) != 1 || unknownContent["*/*"] == nil {
		t.Fatalf("unresolved Bind body must remain media-unconstrained: %+v", unknownContent)
	}
}

// TestMergeOpenAPIContentBodyFlattensOnlyPureAnyOfWrappers verifies repeated alternatives stay flat without dropping sibling constraints.
func TestMergeOpenAPIContentBodyFlattensOnlyPureAnyOfWrappers(t *testing.T) {
	existing := map[string]any{"schema": map[string]any{"anyOf": []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "integer"},
	}}}
	incoming := map[string]any{"schema": map[string]any{"anyOf": []any{
		map[string]any{"type": "boolean"},
		map[string]any{"type": "object"},
	}}}
	merged := mergeOpenAPIContentBody(existing, incoming)
	anyOf := merged["schema"].(map[string]any)["anyOf"].([]any)
	if len(anyOf) != 4 {
		t.Fatalf("pure anyOf wrappers must flatten into four alternatives: %+v", anyOf)
	}

	constrainedUnion := map[string]any{
		"anyOf":    []any{map[string]any{"type": "string"}},
		"nullable": true,
	}
	withSibling := mergeOpenAPIContentBody(
		map[string]any{"schema": constrainedUnion},
		map[string]any{"schema": map[string]any{"type": "integer"}},
	)
	siblingAlternatives := withSibling["schema"].(map[string]any)["anyOf"].([]any)
	if len(siblingAlternatives) != 2 || !reflect.DeepEqual(siblingAlternatives[0], constrainedUnion) {
		t.Fatalf("an anyOf schema with meaningful siblings must remain one constrained alternative: %+v", siblingAlternatives)
	}
}

// TestProjectOpenAPIRejectsUnsafeSecurityPolicies verifies invalid configuration cannot erase an inferred middleware policy or publish ambiguous public access.
func TestProjectOpenAPIRejectsUnsafeSecurityPolicies(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:     "GET",
		Path:       "/account",
		Handler:    HandlerRef{Package: "account", Function: "Show"},
		Middleware: []string{"auth.Require"},
	}}}
	schemes := map[string]OpenAPISecurityScheme{
		"session": {Type: "apiKey", In: "cookie", Name: "session_id"},
	}
	validMapping := map[string][]OpenAPISecurityRequirement{
		"auth.Require": {{"session": {}}},
	}

	cases := []struct {
		name    string
		options OpenAPIOptions
		problem string
	}{
		{
			name: "empty middleware policy",
			options: OpenAPIOptions{
				SecuritySchemes:    schemes,
				MiddlewareSecurity: map[string][]OpenAPISecurityRequirement{"auth.Require": {}},
			},
			problem: "security policy cannot be empty",
		},
		{
			name: "empty requirement object",
			options: OpenAPIOptions{
				SecuritySchemes:    schemes,
				MiddlewareSecurity: map[string][]OpenAPISecurityRequirement{"auth.Require": {{}}},
			},
			problem: "cannot be an empty object",
		},
		{
			name: "empty selector",
			options: OpenAPIOptions{
				Operations: []OpenAPIOperationOverride{{Summary: "unsafe global override"}},
			},
			problem: "empty OpenAPI operation selector",
		},
		{
			name: "invalid scheme fields",
			options: OpenAPIOptions{SecuritySchemes: map[string]OpenAPISecurityScheme{
				"basic": {Type: "http", Scheme: "basic", BearerFormat: "JWT"},
			}},
			problem: "bearerFormat only when scheme is bearer",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ProjectOpenAPI(manifest, testCase.options)
			if err == nil || !strings.Contains(err.Error(), testCase.problem) {
				t.Fatalf("expected %q, got %v", testCase.problem, err)
			}
		})
	}

	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{
		SecuritySchemes:    schemes,
		MiddlewareSecurity: validMapping,
		Operations: []OpenAPIOperationOverride{{
			Match:    OpenAPIOperationSelector{Function: "Show"},
			Security: &OpenAPISecurityPolicy{Requirements: []OpenAPISecurityRequirement{{}}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "empty requirement object") {
		t.Fatalf("expected invalid operation security error, got %v", err)
	}
	security := document.Paths["/account"]["get"].Security
	if security == nil || !reflect.DeepEqual(*security, validMapping["auth.Require"]) {
		t.Fatalf("invalid override erased middleware security: %+v", security)
	}
}

// TestProjectOpenAPIValidatesOAuthFlowsAndScopes verifies conditional OAuth flow fields and declared scopes reach the final scheme intact.
func TestProjectOpenAPIValidatesOAuthFlowsAndScopes(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:     "GET",
		Path:       "/reports",
		Handler:    HandlerRef{Package: "reports", Function: "List"},
		Middleware: []string{"oauth.Require"},
	}}}
	flows := map[string]any{
		"authorizationCode": map[string]any{
			"authorizationUrl": "/oauth/authorize",
			"tokenUrl":         "https://identity.example/token",
			"scopes":           map[string]string{"reports:read": "Read reports"},
		},
	}
	options := OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"oauth": {Type: "oauth2", Flows: flows},
		},
		MiddlewareSecurity: map[string][]OpenAPISecurityRequirement{
			"oauth.Require": {{"oauth": {"reports:read"}}},
		},
	}
	document, err := ProjectOpenAPI(manifest, options)
	if err != nil {
		t.Fatalf("valid OAuth configuration failed: %v", err)
	}
	published := document.Components["securitySchemes"].(map[string]OpenAPISecurityScheme)["oauth"]
	if _, ok := published.Flows.(map[string]any); !ok {
		t.Fatalf("OAuth flows were not normalized into a JSON object: %+v", published.Flows)
	}

	options.MiddlewareSecurity["oauth.Require"] = []OpenAPISecurityRequirement{{"oauth": {"reports:write"}}}
	if _, err := ProjectOpenAPI(manifest, options); err == nil || !strings.Contains(err.Error(), "undeclared OAuth scope") {
		t.Fatalf("expected undeclared scope error, got %v", err)
	}

	options.MiddlewareSecurity["oauth.Require"] = []OpenAPISecurityRequirement{{"oauth": {"reports:read"}}}
	options.SecuritySchemes["oauth"] = OpenAPISecurityScheme{Type: "oauth2", Flows: map[string]any{
		"authorizationCode": map[string]any{
			"authorizationUrl": "/oauth/authorize",
			"scopes":           map[string]string{"reports:read": "Read reports"},
		},
	}}
	if _, err := ProjectOpenAPI(manifest, options); err == nil || !strings.Contains(err.Error(), "requires tokenUrl") {
		t.Fatalf("expected missing tokenUrl error, got %v", err)
	}
}

// TestOpenAPISourcePolicyWarningsProduceSafeLenientProjection verifies unsafe source evidence is diagnosed and omitted or downgraded without invalidating the document.
func TestOpenAPISourcePolicyWarningsProduceSafeLenientProjection(t *testing.T) {
	operation := Operation{
		ID:      "GET:/records",
		Method:  "GET",
		Path:    "/records",
		Handler: HandlerRef{Package: "records", Function: "List", File: "records.go", Line: 12},
		Inputs: InputShape{Headers: []Parameter{
			{Name: "Accept", In: "header"},
			{Name: "X-Request-ID", In: "header"},
			{Name: "x-request-id", In: "header"},
			{Name: "Bad Header", In: "header"},
		}},
		Outputs: OutputShape{Responses: []ResponseShape{
			{StatusCode: 200, ContentType: "Application/JSON", Schema: map[string]any{"type": "string"}},
			{StatusCode: 204, Schema: map[string]any{"type": "string"}, Source: "web.JSON"},
			{StatusCode: 600, ContentType: "not a media type", Schema: map[string]any{"type": "object"}},
		}},
	}
	diagnostics := openAPISourcePolicyDiagnostics(operation)
	codes := map[string]bool{}
	for _, diagnostic := range diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, code := range []string{
		"duplicate_header_parameter",
		"invalid_header_parameter",
		"invalid_response_content_type",
		"invalid_response_status",
		"reserved_openapi_header",
		"response_content_not_allowed",
	} {
		if !codes[code] {
			t.Fatalf("missing %s diagnostic: %+v", code, diagnostics)
		}
	}

	document, err := ProjectOpenAPI(Manifest{Operations: []Operation{operation}}, OpenAPIOptions{})
	if err != nil {
		t.Fatalf("lenient projection failed instead of sanitizing source evidence: %v", err)
	}
	projected := document.Paths["/records"]["get"]
	if len(projected.Parameters) != 1 || projected.Parameters[0].Name != "X-Request-ID" {
		t.Fatalf("reserved, duplicate, or invalid headers leaked into OpenAPI: %+v", projected.Parameters)
	}
	if _, content := projected.Responses["204"]["content"]; content {
		t.Fatalf("204 response retained forbidden content: %+v", projected.Responses["204"])
	}
	if _, content := projected.Responses["default"]["content"]; content {
		t.Fatalf("invalid source media type leaked into default response: %+v", projected.Responses["default"])
	}
	responseContent := projected.Responses["200"]["content"].(map[string]any)
	if responseContent["application/json"] == nil {
		t.Fatalf("valid media type was not canonicalized: %+v", responseContent)
	}
}

// TestProjectOpenAPIOmitsUnrepresentableRoutesBeforeIdentityAndConfiguration verifies omitted source routes cannot perturb projected IDs or satisfy stale explicit mappings.
func TestProjectOpenAPIOmitsUnrepresentableRoutesBeforeIdentityAndConfiguration(t *testing.T) {
	handler := HandlerRef{Package: "items", Receiver: "Controller", Function: "Show"}
	manifest := Manifest{Operations: []Operation{
		{Method: "GET", Path: "/items", Handler: handler},
		{Method: "CONNECT", Path: "/items", Handler: handler},
		{Method: "GET", Path: "/files/*path", Handler: HandlerRef{Package: "files", Function: "Read"}, Middleware: []string{"auth.Require"}},
		{Method: "GET", Path: "/bad path", Handler: HandlerRef{Package: "items", Function: "Bad"}},
		{Method: "GET", Path: "/v1//health", Handler: HandlerRef{Package: "health", Function: "Show"}},
	}}
	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{})
	if err != nil {
		t.Fatalf("project representable routes: %v", err)
	}
	if len(document.Paths) != 2 || document.Paths["/items"] == nil || document.Paths["/v1//health"] == nil {
		t.Fatalf("projection did not omit only unrepresentable routes: %+v", document.Paths)
	}
	if operationID := document.Paths["/items"]["get"].OperationID; operationID != "itemsControllerShow" {
		t.Fatalf("omitted CONNECT route perturbed operationId allocation: %q", operationID)
	}

	omittedOnly := Manifest{Operations: []Operation{{
		Method:     "GET",
		Path:       "/files/*path",
		Handler:    HandlerRef{Package: "files", Function: "Read"},
		Middleware: []string{"auth.Require"},
	}}}
	omittedDocument, err := ProjectOpenAPI(omittedOnly, OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"session": {Type: "apiKey", In: "cookie", Name: "session_id"},
		},
		MiddlewareSecurity: map[string][]OpenAPISecurityRequirement{
			"auth.Require": {{"session": {}}},
		},
		Operations: []OpenAPIOperationOverride{{
			Match:   OpenAPIOperationSelector{Function: "Read", Path: "/files/*path"},
			Summary: "stale override",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "did not match an indexed operation") || !strings.Contains(err.Error(), "not used by an indexed operation") {
		t.Fatalf("expected actionable omitted-operation configuration errors, got %v", err)
	}
	if len(omittedDocument.Paths) != 0 || omittedDocument.Components != nil {
		t.Fatalf("omitted-only operation left paths or security schemes: paths=%+v components=%+v", omittedDocument.Paths, omittedDocument.Components)
	}
}

// TestProjectOpenAPIPrunesSchemasTransitivelyAfterOverrides verifies only schemas reachable from the final projected operations survive.
func TestProjectOpenAPIPrunesSchemasTransitivelyAfterOverrides(t *testing.T) {
	manifest := Manifest{
		Schemas: []Schema{
			{
				Identity: "example.com/items.Input",
				Name:     "Input",
				Definition: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"child": map[string]any{"$ref": "#/components/schemas/Child"},
					},
				},
			},
			{Identity: "example.com/items.Child", Name: "Child", Definition: map[string]any{"type": "string"}},
			{Identity: "example.com/items.Unused", Name: "Unused", Definition: map[string]any{"const": "invalid-in-3.0"}},
		},
		Operations: []Operation{
			{
				Method:  "POST",
				Path:    "/items",
				Handler: HandlerRef{Package: "items", Function: "Create"},
				Inputs: InputShape{Body: &BodyShape{
					Schema: map[string]any{"$ref": "#/components/schemas/Input"},
				}},
				Outputs: OutputShape{Responses: []ResponseShape{
					{StatusCode: 200, Schema: map[string]any{"$ref": "#/components/schemas/Child"}},
					{StatusCode: 204, Source: "web.NoContent"},
				}},
			},
			{
				Method:  "GET",
				Path:    "/unused/*path",
				Handler: HandlerRef{Package: "items", Function: "Unused"},
				Inputs:  InputShape{Body: &BodyShape{Schema: map[string]any{"$ref": "#/components/schemas/Unused"}}},
			},
		},
	}
	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{})
	if err != nil {
		t.Fatalf("project reachable schemas: %v", err)
	}
	schemas := document.Components["schemas"].(map[string]any)
	if len(schemas) != 2 || schemas["Input"] == nil || schemas["Child"] == nil || schemas["Unused"] != nil {
		t.Fatalf("transitive schema pruning retained the wrong components: %+v", schemas)
	}

	removed, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match:       OpenAPIOperationSelector{Function: "Create"},
		RequestBody: &OpenAPIRequestBodyOverride{Remove: true},
		Responses: map[string]OpenAPIResponseOverride{
			"200": {Remove: true},
		},
	}}})
	if err != nil {
		t.Fatalf("project after removing schema-bearing contracts: %v", err)
	}
	if removed.Components != nil {
		t.Fatalf("removed contracts left orphan schema components: %+v", removed.Components)
	}
}

// TestProjectOpenAPIValidatesSchemasAndLocalPointersRecursively verifies nested 3.0 constraints and escaped local targets are checked against the final document.
func TestProjectOpenAPIValidatesSchemasAndLocalPointersRecursively(t *testing.T) {
	manifest := Manifest{
		Schemas: []Schema{{
			Identity: "example.com/items.Envelope",
			Name:     "Envelope",
			Definition: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"item/name": map[string]any{"type": "string"},
				},
			},
		}},
		Operations: []Operation{{
			Method:  "GET",
			Path:    "/item",
			Handler: HandlerRef{Package: "items", Function: "Get"},
			Outputs: OutputShape{Responses: []ResponseShape{{
				StatusCode: 200,
				Schema:     map[string]any{"$ref": "#/components/schemas/Envelope/properties/item~1name"},
			}}},
		}},
	}
	if _, err := ProjectOpenAPI(manifest, OpenAPIOptions{}); err != nil {
		t.Fatalf("valid escaped nested JSON Pointer failed: %v", err)
	}

	invalidSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{
				"type":  "string",
				"items": map[string]any{"type": "integer"},
			},
		},
	}
	_, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match: OpenAPIOperationSelector{Function: "Get"},
		Responses: map[string]OpenAPIResponseOverride{
			"200": {MediaType: "application/json", Schema: invalidSchema},
		},
	}}})
	if err == nil || !strings.Contains(err.Error(), "items cannot constrain schema type \"string\"") {
		t.Fatalf("expected recursive type-keyword compatibility error, got %v", err)
	}

	missingPointer := manifest
	missingPointer.Operations = append([]Operation(nil), manifest.Operations...)
	missingPointer.Operations[0].Outputs.Responses = []ResponseShape{{
		StatusCode: 200,
		Schema:     map[string]any{"$ref": "#/components/schemas/Envelope/properties/missing"},
	}}
	if _, err := ProjectOpenAPI(missingPointer, OpenAPIOptions{}); err == nil || !strings.Contains(err.Error(), "cannot be resolved") {
		t.Fatalf("expected unresolved nested JSON Pointer error, got %v", err)
	}

	permissiveComposition := Manifest{Operations: []Operation{{
		Method:  "GET",
		Path:    "/composed",
		Handler: HandlerRef{Package: "items", Function: "Composed"},
		Outputs: OutputShape{Responses: []ResponseShape{{
			StatusCode: 200,
			Schema: map[string]any{
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"maxLength":  10,
			},
		}}},
	}}}
	if _, err := ProjectOpenAPI(permissiveComposition, OpenAPIOptions{}); err != nil {
		t.Fatalf("type-omitted composition schema should remain permissive: %v", err)
	}
}

// TestProjectOpenAPIRejectsExplicitInvalidMediaAndBodylessContent verifies explicit configuration cannot manufacture invalid response contracts.
func TestProjectOpenAPIRejectsExplicitInvalidMediaAndBodylessContent(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:  "GET",
		Path:    "/health",
		Handler: HandlerRef{Package: "health", Function: "Show"},
		Outputs: OutputShape{Responses: []ResponseShape{{StatusCode: 204, Source: "web.NoContent"}}},
	}}}
	cases := []struct {
		name     string
		response map[string]OpenAPIResponseOverride
		problem  string
	}{
		{
			name: "bodyless content",
			response: map[string]OpenAPIResponseOverride{
				"204": {MediaType: "application/json", Schema: map[string]any{"type": "object"}},
			},
			problem: "cannot define content for status 204",
		},
		{
			name: "invalid media",
			response: map[string]OpenAPIResponseOverride{
				"200": {MediaType: "application json", Schema: map[string]any{"type": "object"}},
			},
			problem: "invalid media type",
		},
		{
			name: "invalid status",
			response: map[string]OpenAPIResponseOverride{
				"600": {Description: "Impossible"},
			},
			problem: "three-digit HTTP status pattern",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
				Match:     OpenAPIOperationSelector{Function: "Show"},
				Responses: testCase.response,
			}}})
			if err == nil || !strings.Contains(err.Error(), testCase.problem) {
				t.Fatalf("expected %q, got %v", testCase.problem, err)
			}
		})
	}
}

// TestOpenAPIExamplesTreatReferenceLikeKeysAsData verifies example payloads are not traversed as Reference Objects.
func TestOpenAPIExamplesTreatReferenceLikeKeysAsData(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:  "GET",
		Path:    "/example",
		Handler: HandlerRef{Package: "examples", Function: "Show"},
	}}}
	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match: OpenAPIOperationSelector{Function: "Show"},
		Responses: map[string]OpenAPIResponseOverride{
			"default": {
				MediaType: "application/json",
				Schema:    map[string]any{"type": "object"},
				Example:   map[string]any{"$ref": "#/components/schemas/NotAReference"},
			},
		},
	}}})
	if err != nil {
		t.Fatalf("example data containing $ref failed projection: %v", err)
	}
	example := document.Paths["/example"]["get"].Responses["default"]["content"].(map[string]any)["application/json"].(map[string]any)["example"]
	if !reflect.DeepEqual(example, map[string]any{"$ref": "#/components/schemas/NotAReference"}) {
		t.Fatalf("example data changed during projection: %+v", example)
	}
}
