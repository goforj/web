package webindex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRunOpenAPIIncludesParameters verifies native context reads become correctly located OpenAPI parameters.
func TestRunOpenAPIIncludesParameters(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, "/monitoring/monitors/:id/check-now", c.CheckNow)} }
func (c *Controller) CheckNow(ctx web.Context) error {
	_ = ctx.Param("id")
	_ = ctx.Query("sync")
	_ = ctx.Header("X-Request-ID")
	return ctx.NoContent(http.StatusAccepted)
}`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/monitoring/monitors/{id}/check-now"]["post"]
	if len(post.Parameters) != 3 {
		t.Fatalf("expected 3 openapi params, got %+v", post.Parameters)
	}
	got := map[string]bool{}
	for _, p := range post.Parameters {
		got[p.In+"|"+p.Name] = true
	}
	for _, expected := range []string{"path|id", "query|sync", "header|X-Request-ID"} {
		if !got[expected] {
			t.Fatalf("missing parameter %s in %+v", expected, post.Parameters)
		}
	}
}

// TestRunOpenAPIIncludesJSONResponseSchema verifies concrete JSON return values project into reusable response schemas.
func TestRunOpenAPIIncludesJSONResponseSchema(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, "/monitors/:id/check-now", c.CheckNow)} }
func (c *Controller) CheckNow(ctx web.Context) error {
	id := ctx.Param("id")
	if id == "" { return ctx.JSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "missing id"}) }
	return ctx.JSON(http.StatusAccepted, map[string]any{"ok": true, "mode": "queued", "monitor_id": id})
}`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/monitors/{id}/check-now"]["post"]
	resp400, ok := post.Responses["400"]
	if !ok {
		t.Fatalf("expected 400 response in openapi")
	}
	content, ok := resp400["content"].(map[string]any)
	if !ok {
		t.Fatalf("expected content for 400 response: %+v", resp400)
	}
	appJSON, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("expected application/json schema for 400 response")
	}
	schema, ok := appJSON["schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected schema object for 400 response")
	}
	schema = derefSchema(t, doc, schema)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties in schema, got %+v", schema)
	}
	if _, ok := props["ok"]; !ok {
		t.Fatalf("expected ok property in schema: %+v", props)
	}
	if _, ok := props["error"]; !ok {
		t.Fatalf("expected error property in schema: %+v", props)
	}
}

// TestRunOpenAPIIncludesRequestBodyFromBind verifies a native Bind target defines the operation request body.
func TestRunOpenAPIIncludesRequestBodyFromBind(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type monitorInput struct { Name string }
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, "/monitors", c.Create)} }
func (c *Controller) Create(ctx web.Context) error {
	var in monitorInput
	if err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{"ok": false}) }
	return ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
}`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/monitors"]["post"]
	assertBodyHasProperties(t, doc, post, []string{"Name"})
}

// TestRunOpenAPIRequestBodyIgnoresPostBindReassignmentFunction verifies later transformations cannot replace the type bound from the request.
func TestRunOpenAPIRequestBodyIgnoresPostBindReassignmentFunction(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type monitorInput struct { Name string; Type string }
func normalizeMonitorInput(in monitorInput) monitorInput { return in }
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, "/monitors", c.Create)} }
func (c *Controller) Create(ctx web.Context) error {
	var in monitorInput
	if err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{"ok": false}) }
	in = normalizeMonitorInput(in)
	return ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
}`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/monitors"]["post"]
	assertBodyHasProperties(t, doc, post, []string{"Name", "Type"})
}

// TestRunOpenAPIRequestBodyUsesJSONTagsAndRequired verifies wire names and validation policy drive request-schema requiredness.
func TestRunOpenAPIRequestBodyUsesJSONTagsAndRequired(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": "package hello\n" +
			"import (\n" +
			"\t\"net/http\"\n" +
			"\t\"github.com/goforj/web\"\n" +
			")\n" +
			"type createInput struct {\n" +
			"\tName string `json:\"name\" validate:\"required\"`\n" +
			"\tEmail *string `json:\"email,omitempty\"`\n" +
			"}\n" +
			"type Controller struct{}\n" +
			"func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, \"/users\", c.Create)} }\n" +
			"func (c *Controller) Create(ctx web.Context) error {\n" +
			"\tvar in createInput\n" +
			"\tif err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{\"ok\": false}) }\n" +
			"\treturn ctx.JSON(http.StatusCreated, map[string]any{\"ok\": true})\n" +
			"}\n",
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/users"]["post"]
	if post.RequestBody == nil {
		t.Fatalf("expected requestBody")
	}
	content := post.RequestBody["content"].(map[string]any)
	appJSON := content["application/json"].(map[string]any)
	schema := appJSON["schema"].(map[string]any)
	schema = derefSchema(t, doc, schema)
	props := schema["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatalf("expected json-tagged name property, got %+v", props)
	}
	if _, ok := props["email"]; !ok {
		t.Fatalf("expected json-tagged email property, got %+v", props)
	}
	requiredAny, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("expected required list, got %+v", schema["required"])
	}
	required := make([]string, 0, len(requiredAny))
	for _, v := range requiredAny {
		required = append(required, v.(string))
	}
	if !reflect.DeepEqual(required, []string{"name"}) {
		t.Fatalf("expected required [name], got %+v", required)
	}
}

// TestRunOpenAPINativeNonJSONResponseContentTypes verifies native text, HTML, and binary responses preserve their media contracts.
func TestRunOpenAPINativeNonJSONResponseContentTypes(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{
	web.NewRoute(http.MethodGet, "/s", c.S),
	web.NewRoute(http.MethodGet, "/h", c.H),
	web.NewRoute(http.MethodGet, "/b", c.B),
} }
func (c *Controller) S(ctx web.Context) error { return ctx.Text(http.StatusOK, "ok") }
func (c *Controller) H(ctx web.Context) error { return ctx.HTML(http.StatusOK, "<p>ok</p>") }
func (c *Controller) B(ctx web.Context) error { return ctx.Blob(http.StatusOK, "application/octet-stream", []byte{1,2,3}) }`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	assertResponseContentType(t, doc, "/s", "get", "200", "text/plain")
	assertResponseContentType(t, doc, "/h", "get", "200", "text/html")
	assertResponseContentType(t, doc, "/b", "get", "200", "application/octet-stream")
}

// TestRunOpenAPIGoldenSnapshot protects the human-facing quality and deterministic shape of a representative API document.
func TestRunOpenAPIGoldenSnapshot(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": "package hello\n" +
			"import (\n" +
			"\t\"net/http\"\n" +
			"\t\"github.com/goforj/web\"\n" +
			")\n" +
			"type createInput struct {\n" +
			"\tName string `json:\"name\"`\n" +
			"\tEnabled bool `json:\"enabled,omitempty\"`\n" +
			"}\n" +
			"type Controller struct{}\n" +
			"func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, \"/users\", c.Create)} }\n" +
			"func (c *Controller) Create(ctx web.Context) error {\n" +
			"\tvar in createInput\n" +
			"\tif err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{\"ok\": false, \"error\": \"bad\"}) }\n" +
			"\treturn ctx.JSON(http.StatusCreated, map[string]any{\"ok\": true, \"id\": \"u_1\"})\n" +
			"}\n",
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "openapi_snapshot.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	gotNorm := normalizeJSON(t, got)
	wantNorm := normalizeJSON(t, want)
	if gotNorm != wantNorm {
		t.Fatalf("openapi snapshot mismatch\nwant:\n%s\n\ngot:\n%s", wantNorm, gotNorm)
	}
}

// TestRunOpenAPIUsesAppNameAsTitle verifies App-scoped metadata replaces generic generator branding.
func TestRunOpenAPIUsesAppNameAsTitle(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		".env":   "APP_NAME=Monitoring Control Plane\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodGet, "/health", c.Health)} }
func (c *Controller) Health(ctx web.Context) error { return ctx.NoContent(http.StatusOK) }`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	if got := doc.Info["title"]; got != "Monitoring Control Plane" {
		t.Fatalf("expected app title from .env, got %q", got)
	}
}

// TestRunOpenAPIUsesComponentRefs verifies named models remain stable and reusable across operation roles.
func TestRunOpenAPIUsesComponentRefs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": "package hello\n" +
			"import (\n" +
			"\t\"net/http\"\n" +
			"\t\"github.com/goforj/web\"\n" +
			")\n" +
			"type createInput struct {\n" +
			"\tName string `json:\"name\"`\n" +
			"}\n" +
			"type Controller struct{}\n" +
			"func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, \"/users\", c.Create)} }\n" +
			"func (c *Controller) Create(ctx web.Context) error {\n" +
			"\tvar in createInput\n" +
			"\tif err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{\"ok\": false}) }\n" +
			"\treturn ctx.JSON(http.StatusCreated, map[string]any{\"ok\": true, \"id\": \"u_1\"})\n" +
			"}\n",
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/users"]["post"]
	content := post.RequestBody["content"].(map[string]any)
	appJSON := content["application/json"].(map[string]any)
	schema := appJSON["schema"].(map[string]any)
	if _, ok := schema["$ref"]; !ok {
		t.Fatalf("expected requestBody schema to be a component ref, got %+v", schema)
	}
	components, ok := doc.Components["schemas"].(map[string]any)
	if !ok || len(components) == 0 {
		t.Fatalf("expected component schemas, got %+v", doc.Components)
	}
}

// TestRunOpenAPIMergesResponseSchemasWithAnyOf verifies overlapping inferred response shapes accept every observed payload.
func TestRunOpenAPIMergesResponseSchemasWithAnyOf(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodGet, "/items/:id", c.Get)} }
func (c *Controller) Get(ctx web.Context) error {
	if ctx.Query("verbose") == "1" {
		return ctx.JSON(http.StatusOK, map[string]any{"ok": true, "id": "x", "details": "full"})
	}
	return ctx.JSON(http.StatusOK, map[string]any{"ok": true, "id": "x"})
}`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	op := doc.Paths["/items/{id}"]["get"]
	resp := op.Responses["200"]
	content := resp["content"].(map[string]any)
	appJSON := content["application/json"].(map[string]any)
	schema := derefSchema(t, doc, appJSON["schema"].(map[string]any))
	anyOf, ok := schema["anyOf"].([]any)
	if !ok || len(anyOf) < 2 {
		t.Fatalf("expected anyOf with >=2 schemas, got %+v", schema)
	}
}

// TestRunOpenAPICrossPackageAliasBindSchema verifies checked import identity survives source aliases during request mapping.
func TestRunOpenAPICrossPackageAliasBindSchema(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/dto/input.go": "package dto\n" +
			"type CreateInput struct {\n" +
			"\tName string `json:\"name\"`\n" +
			"}\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	mdt "example.com/test/internal/dto"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodPost, "/users", c.Create)} }
func (c *Controller) Create(ctx web.Context) error {
	var in mdt.CreateInput
	if err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{"ok": false}) }
	return ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
}`,
	}
	writeFixtureFiles(t, root, files)

	doc := buildOpenAPI(t, root)
	post := doc.Paths["/users"]["post"]
	content := post.RequestBody["content"].(map[string]any)
	appJSON := content["application/json"].(map[string]any)
	schema := appJSON["schema"].(map[string]any)
	ref, ok := schema["$ref"].(string)
	if !ok || ref == "" {
		t.Fatalf("expected component ref for request body schema, got %+v", schema)
	}
	parts := strings.Split(ref, "/")
	componentName := parts[len(parts)-1]
	schemas := doc.Components["schemas"].(map[string]any)
	component := schemas[componentName].(map[string]any)
	props := component["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatalf("expected resolved cross-package schema to include name, got %+v", component)
	}
}

// TestRunOpenAPIStructuralValidation verifies emitted references, path parameters, and responses satisfy core document invariants.
func TestRunOpenAPIStructuralValidation(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		".env":   "APP_NAME=Validation Fixture\n",
		"internal/users/controller.go": "package users\n" +
			"import (\n" +
			"\t\"net/http\"\n" +
			"\t\"github.com/goforj/web\"\n" +
			")\n" +
			"type createUser struct { Name string `json:\"name\"` }\n" +
			"type Controller struct{}\n" +
			"func (c *Controller) Routes() []any { return []any{\n" +
			"\tweb.NewRoute(http.MethodGet, \"/users/:id\", c.Get),\n" +
			"\tweb.NewRoute(http.MethodPost, \"/users\", c.Create),\n" +
			"} }\n" +
			"func (c *Controller) Get(ctx web.Context) error { return ctx.JSON(http.StatusOK, map[string]any{\"ok\": true, \"id\": ctx.Param(\"id\")}) }\n" +
			"func (c *Controller) Create(ctx web.Context) error { var in createUser; if err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{\"ok\": false}) }; return ctx.JSON(http.StatusCreated, map[string]any{\"ok\": true}) }\n",
	}
	writeFixtureFiles(t, root, files)
	doc := buildOpenAPI(t, root)
	if errs := validateOpenAPIDocument(doc); len(errs) > 0 {
		t.Fatalf("openapi structural validation errors:\n%s", strings.Join(errs, "\n"))
	}
}

// TestRunOpenAPIMultiControllerGoldenSnapshot protects semantic naming when several providers contribute colliding model names.
func TestRunOpenAPIMultiControllerGoldenSnapshot(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		".env":   "APP_NAME=Multi Controller App\n",
		"internal/users/controller.go": "package users\n" +
			"import (\n" +
			"\t\"net/http\"\n" +
			"\t\"github.com/goforj/web\"\n" +
			")\n" +
			"type createUser struct { Name string `json:\"name\"`; Email *string `json:\"email,omitempty\"` }\n" +
			"type Controller struct{}\n" +
			"func (c *Controller) Routes() []any { return []any{\n" +
			"\tweb.NewRoute(http.MethodGet, \"/users/:id\", c.Get),\n" +
			"\tweb.NewRoute(http.MethodPost, \"/users\", c.Create),\n" +
			"} }\n" +
			"func (c *Controller) Get(ctx web.Context) error { return ctx.JSON(http.StatusOK, map[string]any{\"ok\": true, \"id\": ctx.Param(\"id\")}) }\n" +
			"func (c *Controller) Create(ctx web.Context) error { var in createUser; if err := ctx.Bind(&in); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{\"ok\": false, \"error\": \"bad\"}) }; return ctx.JSON(http.StatusCreated, map[string]any{\"ok\": true, \"id\": \"u_1\"}) }\n",
		"internal/files/controller.go": `package files
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodGet, "/files/:id/download", c.Download)} }
func (c *Controller) Download(ctx web.Context) error {
	if ctx.Query("raw") == "1" { return ctx.Blob(http.StatusOK, "application/octet-stream", []byte{1,2}) }
	return ctx.Text(http.StatusOK, "ok")
}`,
	}
	writeFixtureFiles(t, root, files)
	doc := buildOpenAPI(t, root)
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "openapi_multi_snapshot.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	gotNorm := normalizeJSON(t, got)
	wantNorm := normalizeJSON(t, want)
	if gotNorm != wantNorm {
		t.Fatalf("openapi multi snapshot mismatch\nwant:\n%s\n\ngot:\n%s", wantNorm, gotNorm)
	}
}

// TestRunOpenAPIDeterministicAcrossRuns verifies repeated indexing cannot perturb generated client inputs.
func TestRunOpenAPIDeterministicAcrossRuns(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		".env":   "APP_NAME=Deterministic App\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type in struct { Name string ` + "`json:\"name\"`" + ` }
type Controller struct{}
func (c *Controller) Routes() []any { return []any{
	web.NewRoute(http.MethodGet, "/items/:id", c.Get),
	web.NewRoute(http.MethodPost, "/items", c.Create),
} }
func (c *Controller) Get(ctx web.Context) error {
	if ctx.Query("full") == "1" { return ctx.JSON(http.StatusOK, map[string]any{"id": ctx.Param("id"), "ok": true, "mode": "full"}) }
	return ctx.JSON(http.StatusOK, map[string]any{"id": ctx.Param("id"), "ok": true})
}
func (c *Controller) Create(ctx web.Context) error {
	var payload in
	if err := ctx.Bind(&payload); err != nil { return ctx.JSON(http.StatusBadRequest, map[string]any{"ok": false}) }
	return ctx.JSON(http.StatusCreated, map[string]any{"ok": true})
}`,
	}
	writeFixtureFiles(t, root, files)

	last := ""
	for i := 0; i < 3; i++ {
		doc := buildOpenAPI(t, root)
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatalf("marshal run %d: %v", i, err)
		}
		current := normalizeJSON(t, raw)
		if i > 0 && current != last {
			t.Fatalf("openapi output not deterministic between runs\nprev:\n%s\n\ncurrent:\n%s", last, current)
		}
		last = current
	}
}

// writeFixtureFiles materializes isolated repositories so integration tests exercise real parsing without touching the source checkout.
func writeFixtureFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, contents := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// buildOpenAPI runs the public indexer and decodes its artifact so assertions cover the published representation.
func buildOpenAPI(t *testing.T, root string) OpenAPIDocument {
	t.Helper()
	openapi := filepath.Join(root, "build", "openapi.json")
	_, err := Run(context.Background(), IndexOptions{Root: root, OpenAPIPath: openapi})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	raw, err := os.ReadFile(openapi)
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc OpenAPIDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi: %v", err)
	}
	return doc
}

// assertBodyHasProperties resolves component references so request assertions remain stable as schemas move between inline and named forms.
func assertBodyHasProperties(t *testing.T, doc OpenAPIDocument, post OpenAPIOp, fields []string) {
	t.Helper()
	if post.RequestBody == nil {
		t.Fatalf("expected requestBody")
	}
	content, ok := post.RequestBody["content"].(map[string]any)
	if !ok {
		t.Fatalf("expected requestBody.content, got %+v", post.RequestBody)
	}
	appJSON, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("expected application/json requestBody content")
	}
	schema, ok := appJSON["schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected requestBody schema")
	}
	schema = derefSchema(t, doc, schema)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected requestBody properties, got %+v", schema)
	}
	for _, field := range fields {
		if _, ok := props[field]; !ok {
			t.Fatalf("expected %s property, got %+v", field, props)
		}
	}
}

// assertResponseContentType verifies response projection preserves the framework method's actual media type.
func assertResponseContentType(t *testing.T, doc OpenAPIDocument, path, method, code, want string) {
	t.Helper()
	op := doc.Paths[path][method]
	resp := op.Responses[code]
	content, ok := resp["content"].(map[string]any)
	if !ok {
		t.Fatalf("expected content for %s %s %s", method, path, code)
	}
	if _, ok := content[want]; !ok {
		t.Fatalf("expected content type %s, got %+v", want, content)
	}
}

// normalizeJSON compares semantic JSON formatting independently from incidental encoder whitespace.
func normalizeJSON(t *testing.T, in []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(in, &v); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// validateOpenAPIDocument performs focused structural checks that make golden failures actionable without duplicating the production validator.
func validateOpenAPIDocument(doc OpenAPIDocument) []string {
	errs := make([]string, 0)
	if strings.TrimSpace(doc.OpenAPI) == "" {
		errs = append(errs, "missing openapi version")
	}
	if strings.TrimSpace(doc.Info["title"]) == "" {
		errs = append(errs, "missing info.title")
	}
	schemas := map[string]any{}
	if doc.Components != nil {
		if c, ok := doc.Components["schemas"].(map[string]any); ok {
			schemas = c
		}
	}
	for path, methods := range doc.Paths {
		pathParams := extractPathParamNames(path)
		for method, op := range methods {
			if len(op.Responses) == 0 {
				errs = append(errs, "operation missing responses: "+method+" "+path)
			}
			for _, name := range pathParams {
				if !hasPathParameter(op, name) {
					errs = append(errs, "missing path parameter "+name+" for "+method+" "+path)
				}
			}
			for code, response := range op.Responses {
				content, ok := response["content"].(map[string]any)
				if !ok {
					continue
				}
				for _, media := range content {
					mediaObj, ok := media.(map[string]any)
					if !ok {
						continue
					}
					schema, ok := mediaObj["schema"].(map[string]any)
					if !ok {
						continue
					}
					if ref, ok := schema["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
						name := strings.TrimPrefix(ref, "#/components/schemas/")
						if _, ok := schemas[name]; !ok {
							errs = append(errs, "dangling schema ref "+ref+" in "+method+" "+path+" "+code)
						}
					}
				}
			}
		}
	}
	return errs
}

// extractPathParamNames derives the parameters every OpenAPI path operation is required to declare.
func extractPathParamNames(path string) []string {
	parts := strings.Split(path, "/")
	out := make([]string, 0)
	for _, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") && len(p) > 2 {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(p, "{"), "}"))
		}
	}
	return out
}

// hasPathParameter confirms a templated segment is represented with the required path location.
func hasPathParameter(op OpenAPIOp, name string) bool {
	for _, p := range op.Parameters {
		if p.In == "path" && p.Name == name {
			return true
		}
	}
	return false
}

// derefSchema lets tests assert schema meaning without depending on whether component reuse was selected.
func derefSchema(t *testing.T, doc OpenAPIDocument, schema map[string]any) map[string]any {
	t.Helper()
	ref, ok := schema["$ref"].(string)
	if !ok || ref == "" {
		return schema
	}
	parts := strings.Split(ref, "/")
	if len(parts) == 0 {
		t.Fatalf("invalid schema ref: %q", ref)
	}
	name := parts[len(parts)-1]
	schemas, ok := doc.Components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("missing components.schemas for ref %q", ref)
	}
	resolved, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("missing schema component %q", name)
	}
	return resolved
}
