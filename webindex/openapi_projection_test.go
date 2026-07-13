package webindex

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestProjectOpenAPIUsesCanonicalComponentsAndCleanInlineContracts verifies the projection never recreates anonymous SchemaN components.
func TestProjectOpenAPIUsesCanonicalComponentsAndCleanInlineContracts(t *testing.T) {
	manifest := Manifest{
		Version: ManifestVersion,
		Schemas: []Schema{{
			Identity: "example.com/app/users.CreateInput",
			Name:     "UsersCreateInput",
			Definition: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		}},
		Operations: []Operation{{
			ID:     "POST:/users",
			Method: "POST",
			Path:   "/users",
			Handler: HandlerRef{
				ImportPath: "example.com/app/users",
				Package:    "users",
				Receiver:   "*Controller",
				Function:   "Create",
			},
			Metadata: &OperationMetadata{
				Summary:     "Create provisions a user.",
				Description: "The account is immediately available.",
				Tags:        []string{"Users"},
			},
			Inputs: InputShape{Body: &BodyShape{
				TypeName: "CreateInput",
				Schema:   map[string]any{"$ref": "#/components/schemas/UsersCreateInput"},
			}},
			Outputs: OutputShape{Responses: []ResponseShape{{
				StatusCode: 201,
				Schema: map[string]any{
					"type":        "object",
					"x-forj-type": "struct { ID string }",
					"properties":  map[string]any{"id": map[string]any{"type": "string"}},
				},
				Source: "web.JSON",
			}}},
		}},
	}

	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{Info: OpenAPIInfoOptions{
		Title:       "Accounts API",
		Version:     "2026-07-13",
		Description: "Account operations.",
	}})
	if err != nil {
		t.Fatalf("ProjectOpenAPI failed: %v", err)
	}
	operation := document.Paths["/users"]["post"]
	if operation.OperationID != "usersControllerCreate" {
		t.Fatalf("unexpected semantic operationId: %q", operation.OperationID)
	}
	if _, exists := operation.Responses["200"]; exists {
		t.Fatalf("projection invented a 200 response: %+v", operation.Responses)
	}
	if operation.Responses["201"]["description"] != "Created" {
		t.Fatalf("expected standard response description, got %+v", operation.Responses["201"])
	}
	components := document.Components["schemas"].(map[string]any)
	if len(components) != 1 || components["UsersCreateInput"] == nil {
		t.Fatalf("expected only the canonical named component, got %+v", components)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	for _, garbage := range []string{"Schema2", "x-forj-type", "struct {", "POST:/users"} {
		if strings.Contains(string(raw), garbage) {
			t.Fatalf("projection leaked %q:\n%s", garbage, raw)
		}
	}
}

// TestSemanticOperationIDsUseStableReadableCollisionRoles verifies ordering and unrelated insertions cannot swap colliding IDs.
func TestSemanticOperationIDsUseStableReadableCollisionRoles(t *testing.T) {
	operations := []Operation{
		{Method: "GET", Path: "/users/:id", Handler: HandlerRef{Package: "users", Receiver: "Controller", Function: "Get"}},
		{Method: "GET", Path: "/users/:user_id", Handler: HandlerRef{Package: "users", Receiver: "Controller", Function: "Get"}},
	}
	first := semanticOperationIDs(operations)
	reversed := []Operation{operations[1], operations[0]}
	second := semanticOperationIDs(reversed)
	if first[0] != second[1] || first[1] != second[0] {
		t.Fatalf("collision IDs changed with input order: first=%+v second=%+v", first, second)
	}
	if first[0] == first[1] || !strings.Contains(first[0], "AtUsersByIdGet") || !strings.Contains(first[1], "AtUsersByUserIdGet") {
		t.Fatalf("expected readable route roles, got %+v", first)
	}

	residual := semanticOperationIDs([]Operation{
		{Method: "GET", Path: "/a-b", Handler: HandlerRef{ImportPath: "example.com/a/users", Package: "users", Receiver: "Controller", Function: "List"}},
		{Method: "GET", Path: "/a_b", Handler: HandlerRef{ImportPath: "example.com/b/users", Package: "users", Receiver: "Controller", Function: "List"}},
	})
	for _, identifier := range residual {
		lastUnderscore := strings.LastIndex(identifier, "_")
		if lastUnderscore < 0 || len(identifier[lastUnderscore+1:]) != 8 {
			t.Fatalf("expected short hash only for residual semantic collision, got %q", identifier)
		}
	}
}

// TestProjectOpenAPIRejectsAmbiguousOverrides verifies selectors must resolve exactly once.
func TestProjectOpenAPIRejectsAmbiguousOverrides(t *testing.T) {
	manifest := Manifest{Operations: []Operation{
		{Method: "GET", Path: "/users", Handler: HandlerRef{Package: "users", Receiver: "Controller", Function: "List"}},
		{Method: "GET", Path: "/admins", Handler: HandlerRef{Package: "users", Receiver: "Controller", Function: "List"}},
	}}
	_, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match: OpenAPIOperationSelector{Package: "users", Function: "List"},
	}}})
	var projectionError *OpenAPIProjectionError
	if !errors.As(err, &projectionError) || !strings.Contains(err.Error(), "matched 2 operations") {
		t.Fatalf("expected actionable ambiguous selector error, got %v", err)
	}
}

// TestProjectOpenAPIAppliesContractOverrides verifies requiredness, examples, and ambiguous schemas can be supplied explicitly.
func TestProjectOpenAPIAppliesContractOverrides(t *testing.T) {
	required := true
	manifest := Manifest{Operations: []Operation{{
		Method:  "POST",
		Path:    "/search",
		Handler: HandlerRef{Package: "search", Receiver: "Controller", Function: "Run"},
		Inputs: InputShape{QueryParams: []Parameter{{
			Name: "q",
			In:   "query",
		}}},
		Outputs: OutputShape{Responses: []ResponseShape{{StatusCode: 0, Source: "web.JSON", Schema: map[string]any{}}}},
	}}}
	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match:       OpenAPIOperationSelector{Function: "Run", Method: "POST", Path: "/search"},
		Summary:     "Search records",
		Description: "Uses the indexed query language.",
		Tags:        []string{"Search"},
		Parameters: []OpenAPIParameterOverride{{
			In:       "query",
			Name:     "q",
			Required: &required,
			Schema:   map[string]any{"type": "string", "minLength": 1},
			Example:  "status:active",
		}},
		RequestBody: &OpenAPIRequestBodyOverride{
			Required: &required,
			Schema:   map[string]any{"type": "object"},
			Example:  map[string]any{"limit": 10},
		},
		ReplaceResponses: true,
		Responses: map[string]OpenAPIResponseOverride{
			"202": {
				Description: "Search accepted",
				MediaType:   "application/json",
				Schema:      map[string]any{"type": "object"},
				Example:     map[string]any{"job_id": "job_1"},
			},
		},
	}}})
	if err != nil {
		t.Fatalf("ProjectOpenAPI failed: %v", err)
	}
	operation := document.Paths["/search"]["post"]
	if operation.Summary != "Search records" || operation.Description == "" || !reflect.DeepEqual(operation.Tags, []string{"Search"}) {
		t.Fatalf("metadata override was not applied: %+v", operation)
	}
	if !operation.Parameters[0].Required || operation.Parameters[0].Example != "status:active" {
		t.Fatalf("parameter override was not applied: %+v", operation.Parameters[0])
	}
	requestContent := operation.RequestBody["content"].(map[string]any)["application/json"].(map[string]any)
	if requestContent["example"].(map[string]any)["limit"] != 10 {
		t.Fatalf("request example override was not applied: %+v", requestContent)
	}
	responseContent := operation.Responses["202"]["content"].(map[string]any)["application/json"].(map[string]any)
	if responseContent["example"].(map[string]any)["job_id"] != "job_1" {
		t.Fatalf("response example override was not applied: %+v", responseContent)
	}
	if _, exists := operation.Responses["default"]; exists {
		t.Fatalf("ReplaceResponses must remove the superseded unresolved response: %+v", operation.Responses)
	}
}

// TestProjectOpenAPIRejectsAmbiguousResponseMediaOverride verifies an explicit media type is required for multi-content responses.
func TestProjectOpenAPIRejectsAmbiguousResponseMediaOverride(t *testing.T) {
	manifest := Manifest{Operations: []Operation{{
		Method:  "GET",
		Path:    "/download",
		Handler: HandlerRef{Package: "files", Function: "Download"},
		Outputs: OutputShape{Responses: []ResponseShape{
			{StatusCode: 200, Source: "web.Text"},
			{StatusCode: 200, Source: "web.Blob", ContentType: "application/octet-stream"},
		}},
	}}}
	_, err := ProjectOpenAPI(manifest, OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
		Match: OpenAPIOperationSelector{Function: "Download"},
		Responses: map[string]OpenAPIResponseOverride{
			"200": {Example: "ambiguous"},
		},
	}}})
	if err == nil || !strings.Contains(err.Error(), "media type is required") {
		t.Fatalf("expected multi-content override error, got %v", err)
	}
}

// TestProjectOpenAPIMapsCookieSecurityExplicitly verifies exact middleware policy without auth-name guessing.
func TestProjectOpenAPIMapsCookieSecurityExplicitly(t *testing.T) {
	manifest := Manifest{Operations: []Operation{
		{Method: "GET", Path: "/account", Handler: HandlerRef{Package: "account", Function: "Show"}, Middleware: []string{"authService.RequireAuth"}},
		{Method: "GET", Path: "/audit", Handler: HandlerRef{Package: "audit", Function: "List"}, Middleware: []string{"audit.RequireAuth"}},
	}}
	options := OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"forjSession": {Type: "apiKey", In: "cookie", Name: "goforj_session"},
		},
		MiddlewareSecurity: map[string][]OpenAPISecurityRequirement{
			"authService.RequireAuth": {{"forjSession": {}}},
		},
	}
	document, err := ProjectOpenAPI(manifest, options)
	if err != nil {
		t.Fatalf("ProjectOpenAPI failed: %v", err)
	}
	accountSecurity := document.Paths["/account"]["get"].Security
	if accountSecurity == nil || !reflect.DeepEqual(*accountSecurity, []OpenAPISecurityRequirement{{"forjSession": {}}}) {
		t.Fatalf("expected cookie security on exact middleware mapping: %+v", accountSecurity)
	}
	if document.Paths["/audit"]["get"].Security != nil {
		t.Fatalf("similarly named middleware must not imply security: %+v", document.Paths["/audit"]["get"].Security)
	}
	schemes := document.Components["securitySchemes"].(map[string]OpenAPISecurityScheme)
	if schemes["forjSession"].In != "cookie" || schemes["forjSession"].Name != "goforj_session" {
		t.Fatalf("unexpected cookie scheme: %+v", schemes["forjSession"])
	}

	options.Operations = []OpenAPIOperationOverride{{
		Match:    OpenAPIOperationSelector{Function: "Show", Path: "/account"},
		Security: &OpenAPISecurityPolicy{Requirements: []OpenAPISecurityRequirement{}},
	}}
	publicDocument, err := ProjectOpenAPI(manifest, options)
	if err != nil {
		t.Fatalf("ProjectOpenAPI explicit public override failed: %v", err)
	}
	publicRaw, err := json.Marshal(publicDocument.Paths["/account"]["get"])
	if err != nil {
		t.Fatalf("marshal explicit public operation: %v", err)
	}
	if !strings.Contains(string(publicRaw), `"security":[]`) {
		t.Fatalf("explicit public override must serialize security as an empty array: %s", publicRaw)
	}

	options.Operations = nil
	options.MiddlewareSecurity["authService.RequireAuth"] = []OpenAPISecurityRequirement{{"missing": {}}}
	if _, err := ProjectOpenAPI(manifest, options); err == nil || !strings.Contains(err.Error(), "unknown security scheme") {
		t.Fatalf("expected unknown scheme error, got %v", err)
	}
}

// TestSecurityForMiddlewaresRemovesRedundantAlternatives verifies repeated policies do not create cartesian garbage.
func TestSecurityForMiddlewaresRemovesRedundantAlternatives(t *testing.T) {
	mappings := map[string][]OpenAPISecurityRequirement{
		"auth.Require": {
			{"oauth": {"access"}},
			{"oauth": {"refresh"}},
		},
	}
	requirements := securityForMiddlewares([]string{"auth.Require", "auth.Require"}, mappings)
	if requirements == nil || !reflect.DeepEqual(*requirements, []OpenAPISecurityRequirement{
		{"oauth": {"access"}},
		{"oauth": {"refresh"}},
	}) {
		t.Fatalf("repeated middleware produced redundant security alternatives: %+v", requirements)
	}

	cleaned := canonicalSecurityRequirements([]OpenAPISecurityRequirement{
		{"oauth": {"access"}},
		{"oauth": {"access", "refresh"}},
	})
	if !reflect.DeepEqual(cleaned, []OpenAPISecurityRequirement{{"oauth": {"access"}}}) {
		t.Fatalf("strict-superset alternative was not removed: %+v", cleaned)
	}
}

// TestOperationMetadataFromHandlerExtractsProseAndDirectives verifies directives override prose without leaking into descriptions.
func TestOperationMetadataFromHandlerExtractsProseAndDirectives(t *testing.T) {
	source := `package accounts
// Create provisions a user. The account can sign in immediately.
//
// @openapi.summary Register an account
// @openapi.description Creates the primary account record.
// @openapi.tag Accounts
// @openapi.security forjSession
func Create() {}`
	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse handler: %v", err)
	}
	function := file.Decls[0].(*ast.FuncDecl)
	metadata, problems := operationMetadataFromHandler(function, "accounts")
	if len(problems) != 0 {
		t.Fatalf("unexpected metadata problems: %+v", problems)
	}
	if metadata.Summary != "Register an account" || metadata.Description != "Creates the primary account record." {
		t.Fatalf("explicit prose metadata was not applied: %+v", metadata)
	}
	if !reflect.DeepEqual(metadata.Tags, []string{"Accounts"}) {
		t.Fatalf("unexpected tags: %+v", metadata.Tags)
	}
	if !reflect.DeepEqual(metadata.Security.Requirements, []OpenAPISecurityRequirement{{"forjSession": {}}}) {
		t.Fatalf("unexpected security annotation: %+v", metadata.Security)
	}
	raw, _ := json.Marshal(metadata)
	if strings.Contains(string(raw), "@openapi") {
		t.Fatalf("directive leaked into operation prose: %s", raw)
	}

	contradictorySource := `package accounts
// Show returns the account.
// @openapi.security none
// @openapi.security forjSession
func Show() {}`
	contradictoryFile, err := parser.ParseFile(token.NewFileSet(), "contradictory.go", contradictorySource, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse contradictory security handler: %v", err)
	}
	contradictoryFunction := contradictoryFile.Decls[0].(*ast.FuncDecl)
	contradictoryMetadata, contradictoryProblems := operationMetadataFromHandler(contradictoryFunction, "accounts")
	if len(contradictoryProblems) != 1 || !strings.Contains(contradictoryProblems[0], "cannot be combined") {
		t.Fatalf("expected contradictory security diagnostic, got %+v", contradictoryProblems)
	}
	if contradictoryMetadata.Security != nil {
		t.Fatalf("contradictory annotation must not override middleware security: %+v", contradictoryMetadata.Security)
	}
}

// TestOpenAPIHandlerSummaryRemovesDeclarationPrefix keeps mechanical Go documentation syntax out of human-facing operation prose.
func TestOpenAPIHandlerSummaryRemovesDeclarationPrefix(t *testing.T) {
	for _, test := range []struct {
		name     string
		summary  string
		expected string
	}{
		{name: "Show", summary: "Show reports readiness.", expected: "Reports readiness."},
		{name: "Show", summary: "Showcase results.", expected: "Showcase results."},
		{name: "Show", summary: "Show", expected: "Show"},
		{name: "Über", summary: "Über prüft den Dienst.", expected: "Prüft den Dienst."},
	} {
		if actual := openAPIHandlerSummary(test.name, test.summary); actual != test.expected {
			t.Fatalf("openAPIHandlerSummary(%q, %q) = %q, want %q", test.name, test.summary, actual, test.expected)
		}
	}
}

// TestRunProjectsHandlerDocumentationAndInfoOptions verifies source prose and build-supplied document metadata reach the artifact.
func TestRunProjectsHandlerDocumentationAndInfoOptions(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/health/controller.go": `package health
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/health", c.Show)}
}
// Show reports service readiness. It does not mutate application state.
// @openapi.tag Operations
func (c *Controller) Show(r web.Context) error { return r.NoContent(http.StatusNoContent) }`,
	})
	openAPIPath := filepath.Join(root, "build", "openapi.json")
	_, err := Run(context.Background(), IndexOptions{
		Root:        root,
		OpenAPIPath: openAPIPath,
		OpenAPI: OpenAPIOptions{Info: OpenAPIInfoOptions{
			Title:       "Health API",
			Version:     "2026.7",
			Description: "Operational endpoints.",
		}},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	raw, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatalf("read OpenAPI artifact: %v", err)
	}
	var document OpenAPIDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("unmarshal OpenAPI artifact: %v", err)
	}
	operation := document.Paths["/health"]["get"]
	if operation.Summary != "Reports service readiness." || operation.Description != "It does not mutate application state." {
		t.Fatalf("unexpected projected handler prose: %+v", operation)
	}
	if !reflect.DeepEqual(operation.Tags, []string{"Operations"}) {
		t.Fatalf("unexpected projected tags: %+v", operation.Tags)
	}
	if !reflect.DeepEqual(document.Info, map[string]string{
		"title":       "Health API",
		"version":     "2026.7",
		"description": "Operational endpoints.",
	}) {
		t.Fatalf("unexpected OpenAPI info: %+v", document.Info)
	}
}

// TestRunDoesNotPublishWhenOpenAPIOptionsAreInvalid verifies projection validation happens before every artifact write.
func TestRunDoesNotPublishWhenOpenAPIOptionsAreInvalid(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/health/controller.go": `package health
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/health", c.Show)}
}
func (c *Controller) Show(r web.Context) error { return r.NoContent(http.StatusNoContent) }`,
	})
	paths := []string{
		filepath.Join(root, "build", "webindex.json"),
		filepath.Join(root, "build", "diagnostics.json"),
		filepath.Join(root, "build", "openapi.json"),
	}
	_, err := Run(context.Background(), IndexOptions{
		Root:            root,
		OutPath:         paths[0],
		DiagnosticsPath: paths[1],
		OpenAPIPath:     paths[2],
		OpenAPI: OpenAPIOptions{Operations: []OpenAPIOperationOverride{{
			Match: OpenAPIOperationSelector{Function: "Missing"},
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("expected projection selector error, got %v", err)
	}
	for _, path := range paths {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid projection published %s: %v", path, statErr)
		}
	}
}
