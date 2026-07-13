package webindex

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// TestRunAnalyzesNativeWebContext verifies that the canonical context contract, rather than Echo compatibility names, drives request and response inference.
func TestRunAnalyzesNativeWebContext(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type createInput struct { Name string ` + "`json:\"name\"`" + ` }
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{web.NewRoute(http.MethodPost, "/reports/:id", c.Create)}
}
func (c *Controller) Create(r web.Context) error {
	_ = r.Param("id")
	_ = r.Query("format")
	_ = r.Header("X-Request-ID")
	_, _ = r.Cookie("session")
	var input createInput
	if err := r.Bind(&input); err != nil {
		return r.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if r.Query("format") == "blob" {
		return r.Blob(http.StatusOK, "text/csv; charset=utf-8", []byte("id"))
	}
	if r.Query("format") == "text" {
		return r.Text(http.StatusLocked, "locked")
	}
	if r.Query("format") == "html" {
		return r.HTML(http.StatusAccepted, "<p>queued</p>")
	}
	if r.Query("format") == "redirect" {
		return r.Redirect(http.StatusSeeOther, "/reports")
	}
	if r.Query("format") == "file" {
		return r.File("report.csv")
	}
	if r.Query("format") == "json" {
		return r.JSON(http.StatusCreated + 1, map[string]any{"ok": true})
	}
	return r.NoContent(http.StatusNoContent)
}`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected one operation, got %d", len(manifest.Operations))
	}
	op := manifest.Operations[0]
	assertParameterNames(t, op.Inputs.PathParams, []string{"id"})
	assertParameterNames(t, op.Inputs.QueryParams, []string{"format"})
	assertParameterNames(t, op.Inputs.Headers, []string{"X-Request-ID"})
	assertParameterNames(t, op.Inputs.Cookies, []string{"session"})
	if op.Inputs.Body == nil || op.Inputs.Body.TypeName != "createInput" || op.Inputs.Body.Source != "web.Bind" {
		t.Fatalf("unexpected request body: %+v", op.Inputs.Body)
	}

	responses := map[string]ResponseShape{}
	for _, response := range op.Outputs.Responses {
		responses[response.Source+"|"+strconv.Itoa(response.StatusCode)] = response
	}
	assertResponseShape(t, responses, "web.Blob", 200, "text/csv; charset=utf-8")
	assertResponseShape(t, responses, "web.HTML", 202, "")
	assertResponseShape(t, responses, "web.JSON", 202, "")
	assertResponseShape(t, responses, "web.NoContent", 204, "")
	assertResponseShape(t, responses, "web.Redirect", 303, "")
	assertResponseShape(t, responses, "web.Text", 423, "")
	assertResponseShape(t, responses, "web.File", 0, "application/octet-stream")

	document := toOpenAPI(manifest)
	operation := document.Paths["/reports/{id}"]["post"]
	parameterLocations := map[string]bool{}
	for _, parameter := range operation.Parameters {
		parameterLocations[parameter.In+"|"+parameter.Name] = true
	}
	for _, expected := range []string{"path|id", "query|format", "header|X-Request-ID", "cookie|session"} {
		if !parameterLocations[expected] {
			t.Fatalf("missing OpenAPI parameter %q in %+v", expected, operation.Parameters)
		}
	}
	assertResponseMediaType(t, operation.Responses["200"], "text/csv; charset=utf-8")
	assertResponseMediaType(t, operation.Responses["423"], "text/plain")
	assertResponseMediaType(t, operation.Responses["default"], "application/octet-stream")
	if _, hasContent := operation.Responses["303"]["content"]; hasContent {
		t.Fatalf("redirect response must not invent a response body: %+v", operation.Responses["303"])
	}
}

// TestRunRetainsEchoContextCompatibility verifies that legacy accessors remain supported without being presented as native web.Context provenance.
func TestRunRetainsEchoContextCompatibility(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/legacy/controller.go": `package legacy
import (
	"net/http"
	"github.com/labstack/echo/v5"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{web.NewRoute(http.MethodGet, "/legacy", c.Show)}
}
func (c *Controller) Show(r *echo.Context) error {
	_ = r.QueryParam("format")
	_ = r.QueryParams().Get("page")
	_ = r.Request().Header.Get("X-Request-ID")
	if r.QueryParam("format") == "xml" {
		return r.XML(http.StatusOK, map[string]string{"ok": "true"})
	}
	return r.String(http.StatusAccepted, "queued")
}`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected one operation, got %d", len(manifest.Operations))
	}
	op := manifest.Operations[0]
	assertParameterNames(t, op.Inputs.QueryParams, []string{"format", "page"})
	assertParameterNames(t, op.Inputs.Headers, []string{"X-Request-ID"})
	responses := map[string]ResponseShape{}
	for _, response := range op.Outputs.Responses {
		responses[response.Source+"|"+strconv.Itoa(response.StatusCode)] = response
	}
	assertResponseShape(t, responses, "echo.XML", 200, "")
	assertResponseShape(t, responses, "echo.String", 202, "")
	document, err := ProjectOpenAPI(manifest, OpenAPIOptions{})
	if err != nil {
		t.Fatalf("project Echo compatibility manifest: %v", err)
	}
	legacy := document.Paths["/legacy"]["get"]
	assertResponseMediaType(t, legacy.Responses["200"], "application/xml")
	assertResponseMediaType(t, legacy.Responses["202"], "text/plain")
}

// TestRunRetainsEchoV4ContextCompatibility verifies exact import identity includes the still-common v4 compatibility surface.
func TestRunRetainsEchoV4ContextCompatibility(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/echov4\n\ngo 1.24\n",
		"internal/legacy/routes.go": `package legacy
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/legacy-v4", c.Show)}
}
`,
		"internal/legacy/handler.go": `package legacy
import (
	"net/http"
	"github.com/labstack/echo/v4"
)
func (c *Controller) Show(ctx echo.Context) error {
	_ = ctx.QueryParam("format")
	return ctx.NoContent(http.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("strict index Echo v4 compatibility: %v", err)
	}
	operation := operationByPath(t, manifest, "/legacy-v4")
	assertParameterNames(t, operation.Inputs.QueryParams, []string{"format"})
	if len(operation.Outputs.Responses) != 1 || operation.Outputs.Responses[0].Source != "echo.NoContent" {
		t.Fatalf("Echo v4 response was not analyzed: %+v", operation.Outputs.Responses)
	}
}

// TestRunReportsUnresolvedResponseExpressions verifies that unknown status and Blob media-type expressions remain visible and diagnostic.
func TestRunReportsUnresolvedResponseExpressions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/dynamic/controller.go": `package dynamic
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/dynamic", c.Dynamic),
		web.NewRoute(http.MethodGet, "/blob", c.Blob),
	}
}
func (c *Controller) Dynamic(r web.Context) error {
	status := selectStatus()
	return r.JSON(status, map[string]any{"roles": loadRoles()})
}
func (c *Controller) Blob(r web.Context) error {
	return r.Blob(http.StatusOK, selectContentType(), []byte("body"))
}`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	dynamic := operationByPath(t, manifest, "/dynamic")
	if len(dynamic.Outputs.Responses) != 1 || dynamic.Outputs.Responses[0].StatusCode != 0 {
		t.Fatalf("unknown status response was dropped or fabricated: %+v", dynamic.Outputs.Responses)
	}
	schema, ok := dynamic.Outputs.Responses[0].Schema.(map[string]any)
	if !ok {
		t.Fatalf("expected inline response schema, got %+v", dynamic.Outputs.Responses[0].Schema)
	}
	properties := schema["properties"].(map[string]any)
	if !reflect.DeepEqual(properties["roles"], map[string]any{}) {
		t.Fatalf("unknown roles expression must remain unconstrained: %+v", properties["roles"])
	}

	codes := map[string]bool{}
	for _, diagnostic := range manifest.Diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, expected := range []string{"unresolved_status_code", "unresolved_response_content_type"} {
		if !codes[expected] {
			t.Fatalf("missing diagnostic %q in %+v", expected, manifest.Diagnostics)
		}
	}
	blob := operationByPath(t, manifest, "/blob")
	if blob.Outputs.Responses[0].ContentType != "" {
		t.Fatalf("dynamic content type must not be guessed: %+v", blob.Outputs.Responses[0])
	}
	document := toOpenAPI(manifest)
	if response := document.Paths["/blob"]["get"].Responses["200"]; response["content"] != nil {
		t.Fatalf("dynamic Blob content type must not become application/octet-stream: %+v", response)
	}
}

// TestRunResolvesImportedStatusAliasesAndConstantParameterKeys verifies static evidence follows import identity and local string constants.
func TestRunResolvesImportedStatusAliasesAndConstantParameterKeys(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/constants\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	stdhttp "net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute("GET", "/reports/:id", c.Show)}
}
func (c *Controller) Show(ctx web.Context) error {
	const idKey = "id"
	const queryKey = "for" + "mat"
	_ = ctx.Param(idKey)
	_ = ctx.Query(queryKey)
	return ctx.NoContent(stdhttp.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("strict index imported constants: %v", err)
	}
	operation := operationByPath(t, manifest, "/reports/:id")
	assertParameterNames(t, operation.Inputs.PathParams, []string{"id"})
	assertParameterNames(t, operation.Inputs.QueryParams, []string{"format"})
	if len(operation.Outputs.Responses) != 1 || operation.Outputs.Responses[0].StatusCode != 204 {
		t.Fatalf("net/http alias status was not resolved: %+v", operation.Outputs.Responses)
	}
}

// TestRunRejectsDecoyHTTPStatusQualifier verifies a same-named package cannot impersonate net/http constants.
func TestRunRejectsDecoyHTTPStatusQualifier(t *testing.T) {
	root := t.TempDir()
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/statusdecoy\n\ngo 1.24\n",
		"internal/report/controller.go": `package report
import (
	http "example.com/not-net-http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute("GET", "/status", c.Show)}
}
func (c *Controller) Show(ctx web.Context) error {
	return ctx.NoContent(http.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index decoy status fixture: %v", err)
	}
	operation := operationByPath(t, manifest, "/status")
	if len(operation.Outputs.Responses) != 1 || operation.Outputs.Responses[0].StatusCode != 0 {
		t.Fatalf("decoy status qualifier was accepted: %+v", operation.Outputs.Responses)
	}
	if !manifestHasDiagnostic(manifest, "unresolved_status_code") {
		t.Fatalf("missing unresolved status diagnostic: %+v", manifest.Diagnostics)
	}
}

// TestRunReportsMultipleBindContracts verifies branch-dependent request DTOs become an explicit union and a strict-mode finding.
func TestRunReportsMultipleBindContracts(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/multibind\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"internal/report/controller.go": `package report
import (
	"net/http"
	"github.com/goforj/web"
)
type First struct { Name string ` + "`json:\"name\"`" + ` }
type Second struct { Count int ` + "`json:\"count\"`" + ` }
type Controller struct{}
func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodPost, "/reports", c.Create)}
}
func (c *Controller) Create(ctx web.Context) error {
	if ctx.Query("kind") == "first" {
		var input First
		if err := ctx.Bind(&input); err != nil { return err }
	} else {
		var input Second
		if err := ctx.Bind(&input); err != nil { return err }
	}
	return ctx.NoContent(http.StatusNoContent)
}
`,
	})

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index multiple Bind fixture: %v", err)
	}
	operation := operationByPath(t, manifest, "/reports")
	if operation.Inputs.Body == nil || operation.Inputs.Body.Confidence != "low" {
		t.Fatalf("multiple Bind targets did not produce an ambiguous body: %+v", operation.Inputs.Body)
	}
	schema, ok := operation.Inputs.Body.Schema.(map[string]any)
	if !ok {
		t.Fatalf("multiple Bind schema has unexpected type: %#v", operation.Inputs.Body.Schema)
	}
	anyOf, ok := schema["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Fatalf("multiple Bind schemas were not retained: %+v", schema)
	}
	if !manifestHasDiagnostic(manifest, "ambiguous_request_body") {
		t.Fatalf("missing multiple Bind diagnostic: %+v", manifest.Diagnostics)
	}
	if _, strictErr := Run(context.Background(), IndexOptions{Root: root, Strict: true}); strictErr == nil {
		t.Fatal("strict indexing accepted multiple Bind contracts without an overrideable manifest contract")
	}
}

// TestParseStatusCodeConstantExpressions verifies the complete status table and safe local arithmetic used by response analysis.
func TestParseStatusCodeConstantExpressions(t *testing.T) {
	for expression, expected := range map[string]int{
		"http.StatusLocked":        423,
		"http.StatusEarlyHints":    103,
		"http.StatusLoopDetected":  508,
		"(http.StatusCreated + 1)": 202,
		"0x190 + 23":               423,
	} {
		parsed, err := parser.ParseExpr(expression)
		if err != nil {
			t.Fatalf("parse expression %q: %v", expression, err)
		}
		if got := parseStatusCode(parsed); got != expected {
			t.Fatalf("unexpected status for %q: got=%d want=%d", expression, got, expected)
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", `package p
func handler() {
	const base = 420
	const locked = base + 3
}`, 0)
	if err != nil {
		t.Fatalf("parse constants: %v", err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		if candidate, ok := declaration.(*ast.FuncDecl); ok && candidate.Name.Name == "handler" {
			function = candidate
			break
		}
	}
	if function == nil {
		t.Fatal("handler declaration not found")
	}
	constants := collectLocalConstants(function.Body)
	expression, err := parser.ParseExpr("locked")
	if err != nil {
		t.Fatalf("parse local status: %v", err)
	}
	if got, ok := parseStatusCodeWithConstants(expression, constants); !ok || got != 423 {
		t.Fatalf("unexpected local constant status: got=%d resolved=%t", got, ok)
	}
}

// TestCollectLocalConstantsRejectsNestedShadowing prevents one flattened name map from fabricating values across lexical scopes.
func TestCollectLocalConstantsRejectsNestedShadowing(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", `package p
import http "net/http"
func handler() {
	const status = 201
	if enabled {
		const status = 500
		const nestedOnly = 418
		_, _ = status, nestedOnly
	}
	http := struct{ StatusOK int }{StatusOK: 599}
	const shadowedHTTP = http.StatusOK
	_ = status
}`, 0)
	if err != nil {
		t.Fatalf("parse shadowed constants: %v", err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		if candidate, ok := declaration.(*ast.FuncDecl); ok && candidate.Name.Name == "handler" {
			function = candidate
			break
		}
	}
	if function == nil {
		t.Fatal("handler declaration not found")
	}
	constants := collectLocalConstants(function.Body)
	for _, name := range []string{"status", "nestedOnly", "shadowedHTTP"} {
		if _, resolved := constants[name]; resolved {
			t.Fatalf("lexically unsafe constant %q was flattened into function scope: %+v", name, constants)
		}
	}
}

// assertParameterNames keeps native and compatibility assertions focused on semantic parameter discovery.
func assertParameterNames(t *testing.T, parameters []Parameter, expected []string) {
	t.Helper()
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		names = append(names, parameter.Name)
	}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("unexpected parameters: got=%v want=%v", names, expected)
	}
}

// assertResponseShape makes provenance regressions explicit because adapter names should never leak into native contracts.
func assertResponseShape(t *testing.T, responses map[string]ResponseShape, source string, status int, contentType string) {
	t.Helper()
	response, ok := responses[source+"|"+strconv.Itoa(status)]
	if !ok {
		t.Fatalf("missing response source %q in %+v", source, responses)
	}
	if response.StatusCode != status || response.ContentType != contentType {
		t.Fatalf("unexpected response %q: %+v", source, response)
	}
}

// assertResponseMediaType verifies that the analyzer's media-type evidence survives the OpenAPI projection.
func assertResponseMediaType(t *testing.T, response map[string]any, mediaType string) {
	t.Helper()
	content, ok := response["content"].(map[string]any)
	if !ok {
		t.Fatalf("response has no content: %+v", response)
	}
	if _, ok := content[mediaType]; !ok {
		t.Fatalf("response missing media type %q: %+v", mediaType, content)
	}
}

// operationByPath locates a fixture operation without coupling tests to deterministic operation ordering.
func operationByPath(t *testing.T, manifest Manifest, path string) Operation {
	t.Helper()
	for _, operation := range manifest.Operations {
		if operation.Path == path {
			return operation
		}
	}
	t.Fatalf("operation %q not found in %+v", path, manifest.Operations)
	return Operation{}
}
