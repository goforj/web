package webindex

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRunMergesGroupAndRouteMiddlewares verifies operation security evidence preserves composition order across both attachment levels.
func TestRunMergesGroupAndRouteMiddlewares(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any {
	return []any{
		web.NewRoute(http.MethodGet, "/m", c.Index, middleware.Gzip(), trace),
	}
}
func (c *Controller) Index(ctx any) error { return nil }`,
		"internal/router/routes_registry.go": `package router
import "github.com/goforj/web"
func ProvideAppRoutes(helloController *hello.Controller) *AppRoutes {
	var app []any
	app = append(app, helloController.Routes()...)
	return &AppRoutes{app: app}
}
type AppRoutes struct { app []any }
func ProvideRoutes(r *AppRoutes) []any {
	groups := []any{}
	groups = append(groups, web.NewRouteGroup("/api", r.app, middleware.Auth(), trace))
	return groups
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
	got := manifest.Operations[0].Middleware
	want := []string{"middleware.Auth()", "trace", "middleware.Gzip()", "trace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected middleware list: got=%v want=%v", got, want)
	}
}

// TestRunUsesDefaultMiddlewaresForSingleGroup verifies an unambiguous generated group can inherit its App-level middleware evidence.
func TestRunUsesDefaultMiddlewaresForSingleGroup(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodGet, "/m", c.Index)} }
func (c *Controller) Index(ctx any) error { return nil }`,
		"internal/router/routes_registry.go": `package router
import "github.com/goforj/web"
func ProvideRoutes(r *AppRoutes) []any {
	groups := []any{}
	groups = append(groups, web.NewRouteGroup("/api", r.app, middleware.RequireAuth()))
	return groups
}
type AppRoutes struct { app []any }`,
	}
	writeFixtureFiles(t, root, files)

	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected one operation, got %d", len(manifest.Operations))
	}
	got := manifest.Operations[0].Middleware
	want := []string{"middleware.RequireAuth()"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected middleware list: got=%v want=%v", got, want)
	}
}

// TestRouteProviderFromRoutesArg verifies provider identity retains the route method selected by composition.
func TestRouteProviderFromRoutesArg(t *testing.T) {
	paramProviders := map[string]routeProvider{
		"helloController": {Package: "hello", Receiver: "Controller"},
	}

	callExpr, err := parser.ParseExpr("helloController.PublicRoutes()")
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}
	got, ok := routeProviderFromRoutesArg(callExpr, paramProviders)
	if !ok || got.Package != "hello" || got.Receiver != "Controller" || got.Method != "PublicRoutes" {
		t.Fatalf("unexpected provider for call: %#v", got)
	}

	selExpr, err := parser.ParseExpr("helloController.Routes")
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}
	got, ok = routeProviderFromRoutesArg(selExpr, paramProviders)
	if !ok || got.Method != "Routes" {
		t.Fatalf("unexpected provider for selector: %#v", got)
	}

	invalidExpr, err := parser.ParseExpr("helloController.Other()")
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}
	got, ok = routeProviderFromRoutesArg(invalidExpr, paramProviders)
	if ok {
		t.Fatalf("expected non-route methods to be ignored, got %#v", got)
	}

	nonIdentSelector, err := parser.ParseExpr("pkg.helloController.Routes")
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}
	if got, ok := routeProviderFromRoutesArg(nonIdentSelector, paramProviders); ok {
		t.Fatalf("expected no provider for selector with non-ident receiver, got %#v", got)
	}

	nonSelectorCall, err := parser.ParseExpr("Routes()")
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}
	if got, ok := routeProviderFromRoutesArg(nonSelectorCall, paramProviders); ok {
		t.Fatalf("expected no provider for non-selector call, got %#v", got)
	}
}

// TestMiddlewareExprs verifies middleware source expressions retain stable diagnostic evidence without executing application code.
func TestMiddlewareExprs(t *testing.T) {
	parameterized, err := parser.ParseExpr(`middleware.RequireRole("admin", fallbackRole)`)
	if err != nil {
		t.Fatalf("parse parameterized middleware: %v", err)
	}
	args := []ast.Expr{
		&ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.Ident{Name: "middleware"},
				Sel: &ast.Ident{Name: "Auth"},
			},
		},
		&ast.Ident{Name: "trace"},
		parameterized,
	}
	got := middlewareExprs(args)
	want := []string{"middleware.Auth()", "trace", `middleware.RequireRole("admin", fallbackRole)`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected middleware expressions: got=%v want=%v", got, want)
	}
	if middlewareExprs(nil) != nil {
		t.Fatalf("expected nil for empty middleware args")
	}
}

// TestParseStatusCode verifies only statically supported HTTP status expressions become response contracts.
func TestParseStatusCode(t *testing.T) {
	if got := parseStatusCode(&ast.BasicLit{Kind: token.INT, Value: "201"}); got != 201 {
		t.Fatalf("expected 201, got %d", got)
	}
	if got := parseStatusCode(&ast.SelectorExpr{
		X:   &ast.Ident{Name: "http"},
		Sel: &ast.Ident{Name: "StatusBadRequest"},
	}); got != 400 {
		t.Fatalf("expected 400, got %d", got)
	}
	if got := parseStatusCode(&ast.SelectorExpr{
		X:   &ast.Ident{Name: "http"},
		Sel: &ast.Ident{Name: "StatusUnknown"},
	}); got != 0 {
		t.Fatalf("expected 0 for unknown status, got %d", got)
	}
}

// TestInferJSONSchemaExprHelpers verifies syntax fallback never upgrades unknown expressions into guessed schema types.
func TestInferJSONSchemaExprHelpers(t *testing.T) {
	expr, err := parser.ParseExpr(`map[string]any{"ok": true, "stats": map[string]any{"count": 1}, "items": []any{"a"}}`)
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}
	schema, ok := inferJSONSchemaExpr(expr).(map[string]any)
	if !ok {
		t.Fatalf("expected object schema, got %+v", schema)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["ok"]; !ok {
		t.Fatalf("expected literal map key ok in schema")
	}
	if _, ok := props["stats"]; !ok {
		t.Fatalf("expected literal map key stats in schema")
	}

	unaryExpr, err := parser.ParseExpr(`&map[string]any{"n": 1.5, "ok": true, "none": nil}`)
	if err != nil {
		t.Fatalf("parse unary expr: %v", err)
	}
	unarySchema := inferJSONSchemaExpr(unaryExpr).(map[string]any)
	unaryProps := unarySchema["properties"].(map[string]any)
	if unaryProps["n"].(map[string]any)["type"] != "number" {
		t.Fatalf("expected number type for float literal, got %+v", unaryProps["n"])
	}
	if unaryProps["ok"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("expected boolean type for bool literal, got %+v", unaryProps["ok"])
	}
	if _, ok := unaryProps["none"].(map[string]any)["nullable"]; !ok {
		t.Fatalf("expected nullable schema for nil literal, got %+v", unaryProps["none"])
	}

	arrayExpr, err := parser.ParseExpr(`[]any{map[string]any{"k": "v"}}`)
	if err != nil {
		t.Fatalf("parse array expr: %v", err)
	}
	arraySchema := inferJSONSchemaExpr(arrayExpr).(map[string]any)
	if arraySchema["type"] != "array" {
		t.Fatalf("expected array schema, got %+v", arraySchema)
	}
	itemSchema := arraySchema["items"].(map[string]any)
	if itemSchema["type"] != "object" {
		t.Fatalf("expected object item schema, got %+v", itemSchema)
	}
	heterogeneousExpr, err := parser.ParseExpr(`[]any{"value", 1}`)
	if err != nil {
		t.Fatalf("parse heterogeneous array: %v", err)
	}
	heterogeneous := inferJSONSchemaExpr(heterogeneousExpr).(map[string]any)
	if alternatives, ok := heterogeneous["items"].(map[string]any)["anyOf"].([]any); !ok || len(alternatives) != 2 {
		t.Fatalf("heterogeneous syntax literal must retain all alternatives: %+v", heterogeneous)
	}

	typedExpr, err := parser.ParseExpr(`Payload{}`)
	if err != nil {
		t.Fatalf("parse typed expr: %v", err)
	}
	if typedSchema := inferJSONSchemaExpr(typedExpr); typedSchema != nil {
		t.Fatalf("named structs require checked type information, got %+v", typedSchema)
	}
	decoyTime, err := parser.ParseExpr(`time.Time{}`)
	if err != nil {
		t.Fatalf("parse selector composite: %v", err)
	}
	if schema := inferJSONSchemaExpr(decoyTime); schema != nil {
		t.Fatalf("selector spelling cannot prove a well-known type, got %+v", schema)
	}
}

// TestToOpenAPIWrapper verifies manifest-to-document projection preserves the operation evidence required by OpenAPI consumers.
func TestToOpenAPIWrapper(t *testing.T) {
	m := Manifest{
		Operations: []Operation{
			{
				ID:     "GET:/x",
				Method: "GET",
				Path:   "/x",
				Outputs: OutputShape{
					Responses: []ResponseShape{{StatusCode: 200, Source: "web.NoContent"}},
				},
			},
		},
	}
	doc := toOpenAPI(m)
	if doc.Info["title"] != "Forj Generated API" {
		t.Fatalf("unexpected default title: %q", doc.Info["title"])
	}
}

// TestResponseContentAndOpenAPIMergeHelpers verifies multi-branch responses merge without discarding media types or inventing schemas.
func TestResponseContentAndOpenAPIMergeHelpers(t *testing.T) {
	if contentType, schema := responseContent(ResponseShape{Schema: map[string]any{"type": "object"}}); contentType != "application/json" || schema == nil {
		t.Fatalf("expected direct schema content for json response")
	}
	for _, tc := range []struct {
		resp        ResponseShape
		contentType string
	}{
		{resp: ResponseShape{Source: "web.Text"}, contentType: "text/plain"},
		{resp: ResponseShape{Source: "web.HTML"}, contentType: "text/html"},
	} {
		if got, _ := responseContent(tc.resp); got != tc.contentType {
			t.Fatalf("unexpected content type for %+v: got=%s want=%s", tc.resp, got, tc.contentType)
		}
	}
	if got, schema := responseContent(ResponseShape{Source: "web.Blob"}); got != "" || schema != nil {
		t.Fatalf("unresolved Blob content must remain untyped: media=%q schema=%+v", got, schema)
	}
	if got, _ := responseContent(ResponseShape{TypeName: "map[string]any"}); got != "application/json" {
		t.Fatalf("expected json content type for map response")
	}
	if got, _ := responseContent(ResponseShape{TypeName: "[]Thing"}); got != "application/json" {
		t.Fatalf("expected json content type for array response")
	}
	if got, _ := responseContent(ResponseShape{TypeName: "Thing"}); got != "application/json" {
		t.Fatalf("expected json content type for object response")
	}
	if got, schema := responseContent(ResponseShape{}); got != "" || schema != nil {
		t.Fatalf("expected empty content for empty response shape")
	}

	existing := map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}}
	incoming := map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string"}}}}
	merged := mergeOpenAPIContentBody(existing, incoming)
	schema, ok := merged["schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected merged schema body, got %+v", merged)
	}
	anyOf, ok := schema["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Fatalf("expected anyOf with 2 schemas, got %+v", schema)
	}

	if out := mergeOpenAPIContentBody(nil, incoming); !reflect.DeepEqual(out, incoming) {
		t.Fatalf("expected incoming when existing empty, got %+v", out)
	}
	if out := mergeOpenAPIContentBody(existing, nil); !reflect.DeepEqual(out, existing) {
		t.Fatalf("expected existing when incoming empty, got %+v", out)
	}
	if out := mergeOpenAPIContentBody(map[string]any{"description": "x"}, incoming); !reflect.DeepEqual(out, incoming) {
		t.Fatalf("expected incoming when existing has no schema, got %+v", out)
	}
	if out := mergeOpenAPIContentBody(existing, map[string]any{"description": "x"}); !reflect.DeepEqual(out, existing) {
		t.Fatalf("expected existing when incoming has no schema, got %+v", out)
	}
	sameSchema := map[string]any{"schema": map[string]any{"type": "string"}}
	if out := mergeOpenAPIContentBody(sameSchema, sameSchema); !reflect.DeepEqual(out, sameSchema) {
		t.Fatalf("expected identical schema merge to preserve existing, got %+v", out)
	}
	if !schemasEquivalent(map[string]any{"type": "string"}, map[string]any{"type": "string"}) {
		t.Fatalf("expected equivalent schemas")
	}
	if schemasEquivalent("x", map[string]any{"type": "string"}) {
		t.Fatalf("expected non-map schemas to be non-equivalent")
	}
	items := dedupeSchemas([]any{
		map[string]any{"type": "string"},
		map[string]any{"type": "string"},
		"non-map",
	})
	if len(items) != 2 {
		t.Fatalf("expected deduped items length 2, got %d (%+v)", len(items), items)
	}
}

// TestAnalyzeTypeInferenceHelpers verifies local evidence is sufficient before handler analysis emits a concrete contract.
func TestAnalyzeTypeInferenceHelpers(t *testing.T) {
	locals := map[string]string{"in": "Input"}
	if got := inferExprTypeName(&ast.Ident{Name: "in"}, locals); got != "Input" {
		t.Fatalf("expected local ident type, got %q", got)
	}
	if got := inferExprTypeName(&ast.CallExpr{Fun: &ast.Ident{Name: "new"}, Args: []ast.Expr{&ast.Ident{Name: "Payload"}}}, locals); got != "Payload" {
		t.Fatalf("expected new(T) type, got %q", got)
	}
	if got := inferExprTypeName(&ast.CallExpr{Fun: &ast.SelectorExpr{X: &ast.Ident{Name: "pkg"}, Sel: &ast.Ident{Name: "NewInput"}}}, locals); got != "pkg.NewInput" {
		t.Fatalf("expected constructor selector type, got %q", got)
	}
	if got := inferExprTypeName(&ast.SelectorExpr{X: &ast.Ident{Name: "dto"}, Sel: &ast.Ident{Name: "Input"}}, locals); got != "dto.Input" {
		t.Fatalf("expected selector type, got %q", got)
	}
	if got := inferArgTypeName(&ast.UnaryExpr{Op: token.AND, X: &ast.CompositeLit{Type: &ast.Ident{Name: "Request"}}}, locals); got != "Request" {
		t.Fatalf("expected unary arg type, got %q", got)
	}
}

// TestCollectLocalTypesAssignmentKeepsExistingTypedVar verifies later value assignments cannot erase an earlier declared request type.
func TestCollectLocalTypesAssignmentKeepsExistingTypedVar(t *testing.T) {
	src := `package p
func f() {
	var req Request
	req = normalize(req)
	payload := NewPayload()
	other := new(Other)
	_, _ = payload, other
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	got := collectLocalTypes(fn.Body)
	if got["req"] != "Request" {
		t.Fatalf("expected req to remain Request, got %q", got["req"])
	}
	if got["payload"] != "NewPayload" {
		t.Fatalf("expected inferred payload type NewPayload, got %q", got["payload"])
	}
	if got["other"] != "Other" {
		t.Fatalf("expected inferred other type Other, got %q", got["other"])
	}
}

// TestCollectLocalTypesInferredVarAndNonIdentAssignments verifies inference follows supported identifiers while ignoring unrelated assignment targets.
func TestCollectLocalTypesInferredVarAndNonIdentAssignments(t *testing.T) {
	src := `package p
func f() {
	var inferred = Payload{}
	var explicit Payload
	external.Field = factory()
	_, _ = inferred, explicit
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	got := collectLocalTypes(fn.Body)
	if got["inferred"] != "Payload" {
		t.Fatalf("expected inferred var type Payload, got %q", got["inferred"])
	}
	if got["explicit"] != "Payload" {
		t.Fatalf("expected explicit var type Payload, got %q", got["explicit"])
	}
}

// TestParseGoFilesWithSetSkipsTemplatesAndTests verifies generated templates and test-only routes cannot contaminate runtime API artifacts.
func TestParseGoFilesWithSetSkipsTemplatesAndTests(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates", "x"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a_test.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("write a_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "x", "ignored.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write ignored.go: %v", err)
	}
	parsed, _, err := parseGoFilesWithSet(root)
	if err != nil {
		t.Fatalf("parseGoFilesWithSet failed: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected only one parsed file, got %d", len(parsed))
	}
}

// TestParseGoFilesWithSetSkipsUnparseableGoFiles verifies lenient discovery reports isolated syntax failures while retaining usable source.
func TestParseGoFilesWithSetSkipsUnparseableGoFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "good.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("write good.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "bad.go"), []byte("package pkg\nfunc {"), 0o644); err != nil {
		t.Fatalf("write bad.go: %v", err)
	}
	parsed, _, err := parseGoFilesWithSet(root)
	if err != nil {
		t.Fatalf("parseGoFilesWithSet failed: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected parser to skip invalid go file and keep one valid file, got %d", len(parsed))
	}
}

// TestIndexerHelpers verifies deterministic source normalization shared by discovery and artifact identity.
func TestIndexerHelpers(t *testing.T) {
	if got := normalizeMethodExpr("http.MethodPost"); got != "post" {
		t.Fatalf("unexpected method normalization: %q", got)
	}
	if got := normalizeMethodExpr(`"GET"`); got != "get" {
		t.Fatalf("unexpected quoted method normalization: %q", got)
	}
	if got := methodNameFromHandlerExpr("controller.Handle"); got != "Handle" {
		t.Fatalf("unexpected handler method name: %q", got)
	}

	if got := typeNameFromExpr(nil); got != "" {
		t.Fatalf("expected empty type name for nil expr, got %q", got)
	}
	if got := typeNameFromExpr(&ast.StarExpr{X: &ast.Ident{Name: "Input"}}); got != "Input" {
		t.Fatalf("unexpected star type name: %q", got)
	}
	if got := typeNameFromExpr(&ast.SelectorExpr{X: &ast.Ident{Name: "dto"}, Sel: &ast.Ident{Name: "Input"}}); got != "dto.Input" {
		t.Fatalf("unexpected selector type name: %q", got)
	}
	if got := typeNameFromExpr(&ast.ArrayType{Elt: &ast.Ident{Name: "string"}}); got != "[]string" {
		t.Fatalf("unexpected array type name: %q", got)
	}
	if got := typeNameFromExpr(&ast.MapType{Key: &ast.Ident{Name: "string"}, Value: &ast.Ident{Name: "int"}}); got != "map[string]int" {
		t.Fatalf("unexpected map type name: %q", got)
	}
	if got := typeNameFromExpr(&ast.FuncType{}); got == "" {
		t.Fatalf("expected fallback type name for func type")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("APP_NAME=Indexer Helper App\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if got := openAPITitleFromRoot(root); got != "Indexer Helper App" {
		t.Fatalf("unexpected openapi title from root: %q", got)
	}
	if got := appNameFromDotEnv(filepath.Join(root, ".missing")); got != "" {
		t.Fatalf("expected empty app name when .env missing, got %q", got)
	}
}

// TestAdditionalIndexerHelpers covers conservative edge cases that protect stable operation and schema identity.
func TestAdditionalIndexerHelpers(t *testing.T) {
	t.Run("join path and string literal helpers", func(t *testing.T) {
		if got := joinPath("", "/users"); got != "/users" {
			t.Fatalf("joinPath(empty) = %q", got)
		}
		if got := joinPath("/api", "users"); got != "/apiusers" {
			t.Fatalf("joinPath(no leading slash) = %q", got)
		}
		if got := joinPath("/api/", "/users"); got != "/api//users" {
			t.Fatalf("joinPath(trim) = %q", got)
		}
		if got := extractStringLiteral(&ast.BasicLit{Kind: token.STRING, Value: `"ok"`}); got != "ok" {
			t.Fatalf("extractStringLiteral(string) = %q", got)
		}
		if got := extractStringLiteral(&ast.BasicLit{Kind: token.STRING, Value: `"unterminated`}); got != "" {
			t.Fatalf("extractStringLiteral(bad quote) = %q", got)
		}
		if got := extractStringLiteral(&ast.Ident{Name: "value"}); got != "" {
			t.Fatalf("extractStringLiteral(non-string) = %q", got)
		}
	})

	t.Run("expr string and title helpers", func(t *testing.T) {
		if got := exprString(nil); got != "" {
			t.Fatalf("exprString(nil) = %q", got)
		}
		if got := exprString(&ast.Ident{Name: "payload"}); got != "payload" {
			t.Fatalf("exprString(ident) = %q", got)
		}
		if got := openAPITitleFromRoot(""); got != "Forj Generated API" {
			t.Fatalf("openAPITitleFromRoot(empty) = %q", got)
		}
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte("# comment\nOTHER=1\nAPP_NAME=\"Quoted App\"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if got := appNameFromDotEnv(root); got != "Quoted App" {
			t.Fatalf("appNameFromDotEnv(quoted) = %q", got)
		}
	})

	t.Run("candidate helpers", func(t *testing.T) {
		only := []discoveredHandler{{Package: "api", Receiver: "*Controller"}}
		if got := pickBestCandidate(only, "", "", ""); !reflect.DeepEqual(got, only[0]) {
			t.Fatalf("pickBestCandidate(single) = %+v", got)
		}
		candidates := []discoveredHandler{
			{Package: "public", Receiver: "*Controller"},
			{Package: "admin", Receiver: "*AdminController"},
		}
		if got := pickBestCandidate(candidates, "", "admin", "AdminController"); got.Package != "admin" {
			t.Fatalf("pickBestCandidate(pkg+recv) = %+v", got)
		}
		if got := pickBestCandidate(candidates, "", "", "Controller"); got.Package != "public" {
			t.Fatalf("pickBestCandidate(recv) = %+v", got)
		}
	})
}

// TestRunReturnsErrorWhenOutputPathIsDirectory verifies publication fails visibly without replacing the prior artifact set.
func TestRunReturnsErrorWhenOutputPathIsDirectory(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24\n",
		"internal/hello/controller.go": `package hello
import (
	"net/http"
	"github.com/goforj/web"
)
type Controller struct{}
func (c *Controller) Routes() []any { return []any{web.NewRoute(http.MethodGet, "/x", c.X)} }
func (c *Controller) X(ctx any) error { return nil }`,
	}
	writeFixtureFiles(t, root, files)

	outDir := filepath.Join(root, "build")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}
	_, err := Run(context.Background(), IndexOptions{
		Root:    root,
		OutPath: outDir,
	})
	if err == nil {
		t.Fatal("expected Run to fail when OutPath is a directory")
	}
}
