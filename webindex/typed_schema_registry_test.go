package webindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestTypedSchemaRegistryBuildsCanonicalContracts verifies semantic identities and the checked shapes used by request and response expressions.
func TestTypedSchemaRegistryBuildsCanonicalContracts(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("load typed schema registry: %v", err)
	}

	fset, bindArgument, responseArgument := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	request, ok := registry.resolveExpression(fset, bindSchemaExpression(bindArgument))
	if !ok {
		t.Fatal("expected checked request expression")
	}
	if request.TypeIdentity != "example.com/typed/contracts.User" {
		t.Fatalf("unexpected request type identity: %q", request.TypeIdentity)
	}
	requestRef := directSchemaReference(t, request.Schema)
	if !strings.HasPrefix(requestRef, "#/components/schemas/ContractsUser") {
		t.Fatalf("unexpected request reference: %q", requestRef)
	}

	response, ok := registry.resolveExpression(fset, responseArgument)
	if !ok {
		t.Fatal("expected checked response expression")
	}
	if response.Schema["type"] != "object" {
		t.Fatalf("expected anonymous response to remain inline, got %+v", response.Schema)
	}
	responseProperties := schemaProperties(t, response.Schema)
	firstRef := directSchemaReference(t, responseProperties["first"])
	secondRef := directSchemaReference(t, responseProperties["second"])
	if firstRef == secondRef {
		t.Fatalf("generic instantiations from colliding imported package names must remain distinct: %q", firstRef)
	}
	if strings.Contains(firstRef+secondRef, ".") {
		t.Fatalf("component names should be codegen-safe rather than Go-qualified: %q %q", firstRef, secondRef)
	}

	components := componentsByIdentity(registry.componentSnapshot())
	user := requireTypedComponent(t, components, "example.com/typed/contracts.User")
	userProperties := schemaProperties(t, user.Schema)
	for _, name := range []string{"alias", "bytes", "created_at", "id", "maybe", "note", "project_id", "required_even", "scores", "status", "uuid"} {
		if _, exists := userProperties[name]; !exists {
			t.Fatalf("expected User property %q in %+v", name, userProperties)
		}
	}
	if _, exists := userProperties["private"]; exists {
		t.Fatalf("unexported fields must not enter schemas: %+v", userProperties)
	}
	if got := user.Schema["required"]; !reflect.DeepEqual(got, []string{"id", "required_even"}) {
		t.Fatalf("only validator-required fields should be required, got %#v", got)
	}
	assertSchemaValues(t, userProperties["bytes"], map[string]any{"type": "string", "format": "byte", "nullable": true})
	assertSchemaValues(t, userProperties["created_at"], map[string]any{"type": "string", "format": "date-time"})
	assertSchemaValues(t, userProperties["uuid"], map[string]any{"type": "string", "format": "uuid"})
	if userProperties["maybe"].(map[string]any)["nullable"] != true {
		t.Fatalf("expected pointer field to be nullable: %+v", userProperties["maybe"])
	}
	scores := userProperties["scores"].(map[string]any)
	if scores["type"] != "object" || scores["nullable"] != true || scores["additionalProperties"].(map[string]any)["type"] != "integer" {
		t.Fatalf("expected typed additionalProperties for map: %+v", scores)
	}

	status := requireTypedComponent(t, components, "example.com/typed/contracts.Status")
	if got := status.Schema["enum"]; !reflect.DeepEqual(got, []any{"active", "disabled"}) {
		t.Fatalf("unexpected typed enum values: %#v", got)
	}
	projectUUID := requireTypedComponent(t, components, "example.com/typed/contracts.ProjectUUID")
	if projectUUID.Schema["type"] != "array" || projectUUID.Schema["format"] == "uuid" {
		t.Fatalf("project types named UUID must follow their checked underlying type: %+v", projectUUID.Schema)
	}
	if _, exists := components["example.com/typed/contracts.TextAlias"]; exists {
		t.Fatal("true aliases should not become independent semantic components")
	}
	page := requireTypedComponent(t, components, "example.com/typed/contracts.Page[example.com/typed/alpha.User]")
	if items := schemaProperties(t, page.Schema)["items"].(map[string]any); items["nullable"] != true {
		t.Fatalf("nil-capable slices must remain nullable: %+v", items)
	}

	alpha := requireTypedComponent(t, components, "example.com/typed/alpha.User")
	beta := requireTypedComponent(t, components, "example.com/typed/beta.User")
	if alpha.Name == beta.Name || !strings.HasPrefix(alpha.Name, "ModelsUser_") || !strings.HasPrefix(beta.Name, "ModelsUser_") {
		t.Fatalf("same package/type labels need deterministic collision suffixes: %q %q", alpha.Name, beta.Name)
	}
	for _, component := range registry.componentSnapshot() {
		if strings.Contains(component.Name, ".") || strings.HasPrefix(component.Name, "Schema") || strings.Contains(component.Name, "struct") {
			t.Fatalf("unclean component name %q for %q", component.Name, component.Identity)
		}
	}

	if diagnostics := registry.diagnosticsSnapshot(); len(diagnostics) != 0 {
		t.Fatalf("expected fully checked fixture without diagnostics, got %+v", diagnostics)
	}
}

// TestTypedSchemaRegistryResolvesCallResultsAndUnknowns verifies call expressions receive result schemas while unsupported values stay unconstrained.
func TestTypedSchemaRegistryResolvesCallResultsAndUnknowns(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("load typed schema registry: %v", err)
	}

	fset, _, callResult := parseTypedHandlerExpressions(t, handlerFile, "CallResult")
	resolved, ok := registry.resolveExpression(fset, callResult)
	if !ok {
		t.Fatal("expected checked call result")
	}
	if got := directSchemaReference(t, resolved.Schema); !strings.Contains(got, "ContractsUser") {
		t.Fatalf("unexpected call-result schema: %+v", resolved.Schema)
	}

	fset, _, unknownArgument := parseTypedHandlerExpressions(t, handlerFile, "Unknown")
	unknown, ok := registry.resolveExpression(fset, unknownArgument)
	if !ok {
		t.Fatal("expected checked unsupported expression")
	}
	if len(unknown.Schema) != 0 {
		t.Fatalf("unsupported values must remain unconstrained, got %+v", unknown.Schema)
	}
	diagnostics := registry.diagnosticsSnapshot()
	if !hasTypedDiagnostic(diagnostics, "unsupported_schema_type", "warn") {
		t.Fatalf("expected warning for unsupported contract, got %+v", diagnostics)
	}
}

// TestTypedSchemaRegistryRejectsCustomMarshalerUnderlyingShapes verifies runtime serialization hooks remain unconstrained instead of exposing dishonest DTO internals.
func TestTypedSchemaRegistryRejectsCustomMarshalerUnderlyingShapes(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/marshalers\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"contracts/contracts.go": `package contracts
type JSONDTO struct { Internal string ` + "`json:\"internal\"`" + ` }
func (JSONDTO) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type TextDTO int
func (*TextDTO) MarshalText() ([]byte, error) { return []byte("text"), nil }

type OrdinaryDTO struct { Visible string ` + "`json:\"visible\"`" + ` }
func (OrdinaryDTO) MarshalJSON(prefix string) ([]byte, error) { return []byte(prefix), nil }

type DecodeDTO struct { Internal string ` + "`json:\"internal\"`" + ` }
func (*DecodeDTO) UnmarshalJSON(data []byte) error { return nil }
`,
		"handler.go": `package handler
import (
	"github.com/goforj/web"
	"example.com/marshalers/contracts"
)
func JSONValue(ctx web.Context) error { return ctx.JSON(200, contracts.JSONDTO{}) }
func TextValue(ctx web.Context) error { return ctx.JSON(200, contracts.TextDTO(0)) }
func OrdinaryValue(ctx web.Context) error { return ctx.JSON(200, contracts.OrdinaryDTO{}) }
func RequestValue(ctx web.Context) error {
	var request contracts.DecodeDTO
	if err := ctx.Bind(&request); err != nil { return err }
	return ctx.JSON(200, map[string]bool{"ok": true})
}
`,
	})
	handlerFile := filepath.Join(root, "handler.go")
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("load custom marshaler fixture: %v", err)
	}

	for _, functionName := range []string{"JSONValue", "TextValue"} {
		fset, _, response := parseTypedHandlerExpressions(t, handlerFile, functionName)
		resolved, ok := registry.resolveExpression(fset, response)
		if !ok {
			t.Fatalf("resolve %s response", functionName)
		}
		if len(resolved.Schema) != 0 || resolved.ComponentName != "" || resolved.Confidence != "low" {
			t.Fatalf("custom marshaler %s exposed an underlying schema: %+v", functionName, resolved)
		}
	}
	fset, _, ordinaryResponse := parseTypedHandlerExpressions(t, handlerFile, "OrdinaryValue")
	ordinary, ok := registry.resolveExpression(fset, ordinaryResponse)
	if !ok {
		t.Fatal("resolve ordinary response")
	}
	if directSchemaReference(t, ordinary.Schema) == "" {
		t.Fatalf("same-named non-marshaler method suppressed an ordinary DTO schema: %+v", ordinary)
	}
	requestSet, bindArgument, _ := parseTypedHandlerExpressions(t, handlerFile, "RequestValue")
	request, ok := registry.resolveExpression(requestSet, bindSchemaExpression(bindArgument))
	if !ok {
		t.Fatal("resolve custom unmarshaler request")
	}
	if len(request.Schema) != 0 || request.ComponentName != "" || request.Confidence != "low" {
		t.Fatalf("custom unmarshaler exposed an underlying request schema: %+v", request)
	}

	diagnostics := registry.diagnosticsSnapshot()
	customMessages := make([]string, 0)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "custom_marshaler_schema" {
			customMessages = append(customMessages, diagnostic.Message)
		}
	}
	if !reflect.DeepEqual(customMessages, []string{
		"Go type contracts.JSONDTO implements json.Marshaler; its runtime JSON contract requires an explicit OpenAPI schema",
		"Go type contracts.TextDTO implements encoding.TextMarshaler; its runtime JSON contract requires an explicit OpenAPI schema",
	}) {
		t.Fatalf("unexpected custom marshaler diagnostics: %+v", diagnostics)
	}
	if !hasTypedDiagnostic(diagnostics, "custom_unmarshaler_schema", "warn") {
		t.Fatalf("missing custom unmarshaler diagnostic: %+v", diagnostics)
	}
	components := componentsByIdentity(registry.componentSnapshot())
	if _, exists := components["example.com/marshalers/contracts.JSONDTO"]; exists {
		t.Fatal("custom JSON marshaler must not publish an underlying component")
	}
	if _, exists := components["example.com/marshalers/contracts.TextDTO"]; exists {
		t.Fatal("custom text marshaler must not publish an underlying component")
	}
	if _, exists := components["example.com/marshalers/contracts.DecodeDTO"]; exists {
		t.Fatal("custom JSON unmarshaler must not publish an underlying component")
	}
}

// TestTypedSchemaRegistryProjectsWireTagsAndTypedValidation verifies tags describe emitted JSON values rather than Go storage types.
func TestTypedSchemaRegistryProjectsWireTagsAndTypedValidation(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/wiretypes\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"contracts/contracts.go": `package contracts
import "encoding/json"

type embedded struct {
	Promoted string ` + "`json:\"promoted\"`" + `
	hidden string
}


type WireDTO struct {
	embedded
	ordinaryHidden string
	Count int ` + "`json:\"count,string\" validate:\"oneof=1 2\"`" + `
	Maybe *int ` + "`json:\"maybe,string\"`" + `
	Enabled bool ` + "`json:\"enabled\" validate:\"oneof=true false\"`" + `
	Ratio float64 ` + "`json:\"ratio\" validate:\"oneof=1.5 2\"`" + `
	Email string ` + "`json:\"email\" validate:\"email\"`" + `
	BadFormat int ` + "`json:\"bad_format\" validate:\"uuid\"`" + `
	BadEnum uint8 ` + "`json:\"bad_enum\" validate:\"oneof=1 nope\"`" + `
	Choice string ` + "`json:\"choice\" validate:\"oneof='hello world' plain plain\" binding:\"oneof=plain 'hello world'\"`" + `
	Optional string ` + "`json:\"optional\" validate:\"omitempty,required,oneof=x\"`" + `
	Elements []string ` + "`json:\"elements\" validate:\"dive,required,oneof=a b\"`" + `
	Top []string ` + "`json:\"top\" validate:\"required,dive,required\"`" + `
	Number json.Number ` + "`json:\"number\"`" + `
	U8 uint8 ` + "`json:\"u8\"`" + `
	U16 uint16 ` + "`json:\"u16\"`" + `
	U32 uint32 ` + "`json:\"u32\"`" + `
	U uint ` + "`json:\"u\"`" + `
	U64 uint64 ` + "`json:\"u64\"`" + `
	UP uintptr ` + "`json:\"up\"`" + `
}
`,
		"handler.go": `package handler
import (
	"github.com/goforj/web"
	"example.com/wiretypes/contracts"
)
func Handle(ctx web.Context) error { return ctx.JSON(200, contracts.WireDTO{}) }
`,
	})
	handlerFile := filepath.Join(root, "handler.go")
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{Root: root, HandlerFiles: []string{handlerFile}})
	if err != nil {
		t.Fatalf("load wire-tag fixture: %v", err)
	}
	fset, _, response := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	if _, ok := registry.resolveExpression(fset, response); !ok {
		t.Fatal("resolve wire-tag response")
	}
	component := requireTypedComponent(t, componentsByIdentity(registry.componentSnapshot()), "example.com/wiretypes/contracts.WireDTO")
	properties := schemaProperties(t, component.Schema)
	if _, exists := properties["promoted"]; !exists {
		t.Fatalf("exported fields from an unexported anonymous struct were not promoted: %+v", properties)
	}
	for _, omitted := range []string{"hidden", "ordinaryHidden"} {
		if _, exists := properties[omitted]; exists {
			t.Fatalf("unexported field %q leaked into schema: %+v", omitted, properties)
		}
	}
	assertSchemaValues(t, properties["count"], map[string]any{"type": "string", "enum": []any{"1", "2"}})
	assertSchemaValues(t, properties["maybe"], map[string]any{"type": "string", "nullable": true})
	assertSchemaValues(t, properties["enabled"], map[string]any{"type": "boolean", "enum": []any{true, false}})
	assertSchemaValues(t, properties["ratio"], map[string]any{"type": "number", "enum": []any{1.5, 2.0}})
	assertSchemaValues(t, properties["email"], map[string]any{"type": "string", "format": "email"})
	assertSchemaValues(t, properties["bad_format"], map[string]any{"type": "integer"})
	assertSchemaValues(t, properties["bad_enum"], map[string]any{"type": "integer", "format": "int32", "minimum": 0})
	assertSchemaValues(t, properties["choice"], map[string]any{"type": "string", "enum": []any{"hello world", "plain"}})
	assertSchemaValues(t, properties["optional"], map[string]any{"type": "string", "enum": []any{"x"}})
	assertSchemaValues(t, properties["elements"], map[string]any{"type": "array", "nullable": true})
	assertSchemaValues(t, properties["top"], map[string]any{"type": "array", "nullable": true})
	if got := component.Schema["required"]; !reflect.DeepEqual(got, []string{"top"}) {
		t.Fatalf("omitempty and dive must scope required to the containing field: %#v", got)
	}
	assertSchemaValues(t, properties["number"], map[string]any{"type": "number"})
	assertSchemaValues(t, properties["u8"], map[string]any{"type": "integer", "format": "int32", "minimum": 0})
	for _, field := range []string{"u16", "u32"} {
		assertSchemaValues(t, properties[field], map[string]any{"type": "integer", "format": "int64", "minimum": 0})
	}
	for _, field := range []string{"u", "u64", "up"} {
		assertSchemaValues(t, properties[field], map[string]any{"type": "integer", "minimum": 0})
	}
	diagnostics := registry.diagnosticsSnapshot()
	if !hasTypedDiagnostic(diagnostics, "incompatible_validation_hint", "warn") {
		t.Fatalf("invalid validation hints must remain diagnostic: %+v", diagnostics)
	}
	for _, fragment := range []string{`JSON field "bad_format"`, `JSON field "bad_enum"`} {
		found := false
		for _, diagnostic := range diagnostics {
			if strings.Contains(diagnostic.Message, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing validation diagnostic for %s: %+v", fragment, diagnostics)
		}
	}
}

// TestTypedSchemaRegistryIsDeterministic verifies component refs and definitions do not depend on repeated package-loader traversal.
func TestTypedSchemaRegistryIsDeterministic(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	var previous string
	for iteration := 0; iteration < 2; iteration++ {
		registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
			Root:         root,
			HandlerFiles: []string{handlerFile},
		})
		if err != nil {
			t.Fatalf("load typed schema registry: %v", err)
		}
		fset, bindArgument, responseArgument := parseTypedHandlerExpressions(t, handlerFile, "Handle")
		registry.resolveExpression(fset, bindSchemaExpression(bindArgument))
		registry.resolveExpression(fset, responseArgument)
		encoded, err := json.Marshal(registry.componentSnapshot())
		if err != nil {
			t.Fatalf("marshal component snapshot: %v", err)
		}
		current := string(encoded)
		if iteration > 0 && current != previous {
			t.Fatalf("typed components changed across identical runs\nfirst: %s\nsecond: %s", previous, current)
		}
		previous = current
	}
}

// TestTypedSchemaRegistrySkipsPackagesWithoutContracts verifies route-only indexing keeps the original fast AST path.
func TestTypedSchemaRegistrySkipsPackagesWithoutContracts(t *testing.T) {
	root := t.TempDir()
	handlerFile := filepath.Join(root, "handler.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"handler.go": "package handler\nfunc Handle() error { return nil }\n",
	})
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("route-only handler should not invoke package loading: %v", err)
	}
	if len(registry.expressions) != 0 || len(registry.diagnosticsSnapshot()) != 0 {
		t.Fatalf("expected an empty fast-path registry, got expressions=%d diagnostics=%+v", len(registry.expressions), registry.diagnosticsSnapshot())
	}
}

// TestRunPublishesCanonicalTypedSchemas verifies the focused registry is the authoritative manifest schema graph for scoped operations.
func TestRunPublishesCanonicalTypedSchemas(t *testing.T) {
	root, _ := writeTypedSchemaFixture(t)
	manifest, err := Run(context.Background(), IndexOptions{Root: root, Strict: true})
	if err != nil {
		t.Fatalf("run typed fixture index: %v", err)
	}
	if manifest.Version != "2" {
		t.Fatalf("unexpected manifest version: %q", manifest.Version)
	}
	if len(manifest.Operations) != 1 {
		t.Fatalf("expected one scoped operation, got %d", len(manifest.Operations))
	}
	operation := manifest.Operations[0]
	if operation.Handler.ImportPath != "example.com/typed/handler" {
		t.Fatalf("unexpected handler import identity: %+v", operation.Handler)
	}
	if operation.Inputs.Body == nil || !strings.Contains(directSchemaReference(t, operation.Inputs.Body.Schema), "ContractsUser") {
		t.Fatalf("expected canonical request ref, got %+v", operation.Inputs.Body)
	}
	if len(operation.Outputs.Responses) != 1 || operation.Outputs.Responses[0].Schema == nil {
		t.Fatalf("expected typed response schema, got %+v", operation.Outputs.Responses)
	}
	components := make([]typedSchemaComponent, 0, len(manifest.Schemas))
	for _, schema := range manifest.Schemas {
		definition, ok := schema.Definition.(map[string]any)
		if !ok {
			t.Fatalf("schema %s has unexpected definition %#v", schema.Name, schema.Definition)
		}
		components = append(components, typedSchemaComponent{
			Identity:   schema.Identity,
			Name:       schema.Name,
			Package:    schema.Package,
			TypeName:   schema.TypeName,
			Schema:     definition,
			Confidence: schema.Confidence,
		})
	}
	byIdentity := componentsByIdentity(components)
	requireTypedComponent(t, byIdentity, "example.com/typed/contracts.User")
	requireTypedComponent(t, byIdentity, "example.com/typed/contracts.Page[example.com/typed/alpha.User]")
	requireTypedComponent(t, byIdentity, "example.com/typed/contracts.Page[example.com/typed/beta.User]")
}

// TestTypedSchemaRegistryIgnoresUnrelatedTypeNameCollisions verifies helper-only types cannot rename an established API component.
func TestTypedSchemaRegistryIgnoresUnrelatedTypeNameCollisions(t *testing.T) {
	withoutHelperRoot, withoutHelperFile := writeTypedCollisionFixture(t, false)
	withoutHelper := resolveTypedFixtureResponse(t, withoutHelperRoot, withoutHelperFile)
	withHelperRoot, withHelperFile := writeTypedCollisionFixture(t, true)
	withHelper := resolveTypedFixtureResponse(t, withHelperRoot, withHelperFile)
	if withoutHelper != withHelper {
		t.Fatalf("unrelated helper changed API component name: without=%q with=%q", withoutHelper, withHelper)
	}
	if withoutHelper != "#/components/schemas/ModelsUser" {
		t.Fatalf("expected collision-free semantic name, got %q", withoutHelper)
	}
}

// TestTypedSchemaRegistryReportsPartialPackageErrors verifies lenient indexing can retain partial types while strict mode sees a warning.
func TestTypedSchemaRegistryReportsPartialPackageErrors(t *testing.T) {
	root := t.TempDir()
	handlerFile := filepath.Join(root, "handler.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/partial\n\ngo 1.25.0\n",
		"handler.go": `package partial
import missing "example.com/not-required"
type Input struct { Name string ` + "`json:\"name\"`" + ` }
func Handle(ctx missing.Context) error {
	input := Input{}
	if err := ctx.Bind(&input); err != nil { return err }
	return ctx.JSON(200, input)
}
`,
	})
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("partial package load should remain usable: %v", err)
	}
	if !hasTypedDiagnostic(registry.diagnosticsSnapshot(), "typed_package_error", "warn") {
		t.Fatalf("expected package warning, got %+v", registry.diagnosticsSnapshot())
	}
}

// TestTypedSchemaRegistrySanitizesDiagnosticRoots verifies package-loader messages are stable across equivalent checkout locations.
func TestTypedSchemaRegistrySanitizesDiagnosticRoots(t *testing.T) {
	first := (&typedSchemaRegistry{root: "/tmp/checkout-one"}).sanitizeDiagnosticMessage("load /tmp/checkout-one/internal/contracts/types.go: missing type")
	second := (&typedSchemaRegistry{root: "/tmp/checkout-two"}).sanitizeDiagnosticMessage("load /tmp/checkout-two/internal/contracts/types.go: missing type")
	if first != second || first != "load internal/contracts/types.go: missing type" {
		t.Fatalf("typed diagnostic retained checkout identity: first=%q second=%q", first, second)
	}
}

// TestTypedSchemaRegistryHonorsCancellation verifies focused package loading does not continue after its build context is canceled.
func TestTypedSchemaRegistryHonorsCancellation(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loadTypedSchemaRegistry(ctx, typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

// TestTypedSchemaRegistryLoadsMultipleHandlerPackages verifies a single registry load retains checked contracts from every selected package directory.
func TestTypedSchemaRegistryLoadsMultipleHandlerPackages(t *testing.T) {
	const packageCount = 6
	root, handlerFiles := writeMultiPackageTypedSchemaFixture(t, packageCount)
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: handlerFiles,
	})
	if err != nil {
		t.Fatalf("load multi-package typed schema registry: %v", err)
	}
	if diagnostics := registry.diagnosticsSnapshot(); len(diagnostics) != 0 {
		t.Fatalf("expected fully checked multi-package fixture, got %+v", diagnostics)
	}

	identities := make([]string, 0, len(handlerFiles))
	for index, handlerFile := range handlerFiles {
		fset, bindArgument, responseArgument := parseTypedHandlerExpressions(t, handlerFile, "Handle")
		request, ok := registry.resolveExpression(fset, bindSchemaExpression(bindArgument))
		if !ok {
			t.Fatalf("resolve request expression for handler package %d", index)
		}
		response, ok := registry.resolveExpression(fset, responseArgument)
		if !ok {
			t.Fatalf("resolve response expression for handler package %d", index)
		}
		identity := fmt.Sprintf("example.com/typedmulti/handlers/h%02d.Payload", index)
		if request.TypeIdentity != identity || response.TypeIdentity != identity {
			t.Fatalf("handler package %d resolved identities request=%q response=%q, want %q", index, request.TypeIdentity, response.TypeIdentity, identity)
		}
		identities = append(identities, identity)
	}
	components := componentsByIdentity(registry.componentSnapshot())
	for _, identity := range identities {
		if _, exists := components[identity]; !exists {
			t.Fatalf("missing component %q from %d loaded handler packages", identity, packageCount)
		}
	}
}

// BenchmarkTypedSchemaRegistryFocusedPackage tracks the cost of focused type loading separately from the fast route-discovery benchmark.
func BenchmarkTypedSchemaRegistryFocusedPackage(b *testing.B) {
	root, handlerFile := writeTypedSchemaFixture(b)
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
			Root:         root,
			HandlerFiles: []string{handlerFile},
		}); err != nil {
			b.Fatalf("load typed schema registry: %v", err)
		}
	}
}

// BenchmarkTypedSchemaRegistryMultiplePackages tracks the package-loader cost for an application with handlers distributed like a service-oriented project.
func BenchmarkTypedSchemaRegistryMultiplePackages(b *testing.B) {
	const packageCount = 14
	root, handlerFiles := writeMultiPackageTypedSchemaFixture(b, packageCount)
	b.ReportMetric(packageCount, "handler_pkgs")
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
			Root:         root,
			HandlerFiles: handlerFiles,
		}); err != nil {
			b.Fatalf("load multi-package typed schema registry: %v", err)
		}
	}
}

// writeMultiPackageTypedSchemaFixture creates independent handler roots so tests exercise the multi-pattern packages.Load path.
func writeMultiPackageTypedSchemaFixture(t testing.TB, packageCount int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	files := map[string]string{
		"go.mod": "module example.com/typedmulti\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
	}
	handlerFiles := make([]string, 0, packageCount)
	for index := 0; index < packageCount; index++ {
		directory := fmt.Sprintf("handlers/h%02d", index)
		relative := filepath.Join(directory, "handler.go")
		files[relative] = fmt.Sprintf(`package h%02d

import "github.com/goforj/web"

// Payload is the package-local contract used by this fixture's handler.
type Payload struct {
	Value string `+"`json:\"value\" validate:\"required\"`"+`
}

// Handle binds and emits the package-local contract.
func Handle(ctx web.Context) error {
	request := Payload{}
	if err := ctx.Bind(&request); err != nil {
		return err
	}
	return ctx.JSON(200, request)
}
`, index)
		handlerFiles = append(handlerFiles, filepath.Join(root, relative))
	}
	writeTypedFixtureFiles(t, root, files)
	return root, handlerFiles
}

// writeTypedSchemaFixture creates a real temporary module with local replaces so go/packages sees the same module boundaries as generated Apps.
func writeTypedSchemaFixture(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	files := map[string]string{
		"go.mod":        "module example.com/typed\n\ngo 1.25.0\n\nrequire (\n\tgithub.com/goforj/web v0.0.0\n\tgithub.com/google/uuid v0.0.0\n)\n\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\nreplace github.com/google/uuid => ./uuid\n",
		"uuid/go.mod":   "module github.com/google/uuid\n\ngo 1.25.0\n",
		"uuid/uuid.go":  "package uuid\ntype UUID [16]byte\n",
		"alpha/user.go": "package models\ntype User struct { Alpha string `json:\"alpha\"` }\n",
		"beta/user.go":  "package models\ntype User struct { Beta int `json:\"beta\"` }\n",
		"contracts/types.go": `package contracts

import (
	"time"
	"github.com/google/uuid"
)

type TextAlias = string

type Status string

const (
	statusUnknown Status = "unknown"
	StatusActive Status = "active"
	StatusDisabled Status = "disabled"
)

type Embedded struct {
	Identifier string ` + "`json:\"id\" validate:\"required\"`" + `
	private string
}

type OptionalFields struct {
	Note string ` + "`json:\"note,omitempty\"`" + `
}

type ProjectUUID [16]byte

type User struct {
	Embedded
	*OptionalFields
	Alias TextAlias ` + "`json:\"alias\"`" + `
	Bytes []byte ` + "`json:\"bytes\"`" + `
	CreatedAt time.Time ` + "`json:\"created_at\"`" + `
	Maybe *string ` + "`json:\"maybe,omitempty\"`" + `
	ProjectID ProjectUUID ` + "`json:\"project_id\"`" + `
	RequiredEvenOptional string ` + "`json:\"required_even,omitempty\" binding:\"required\"`" + `
	Scores map[string]int ` + "`json:\"scores\"`" + `
	State Status ` + "`json:\"status\"`" + `
	UUID uuid.UUID ` + "`json:\"uuid\"`" + `
}

type Page[T any] struct {
	Items []T ` + "`json:\"items\"`" + `
	Next *Page[T] ` + "`json:\"next,omitempty\"`" + `
}
`,
		"handler/handler.go": `package handler

import (
	"net/http"
	"github.com/goforj/web"
	alpha "example.com/typed/alpha"
	beta "example.com/typed/beta"
	"example.com/typed/contracts"
)

type Controller struct{}

func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodPost, "/typed", c.Handle)}
}

func (c *Controller) Handle(ctx web.Context) error {
	request := contracts.User{}
	if err := ctx.Bind(&request); err != nil {
		return err
	}
	first := contracts.Page[alpha.User]{}
	second := contracts.Page[beta.User]{}
	return ctx.JSON(200, struct {
		First contracts.Page[alpha.User] ` + "`json:\"first\"`" + `
		Second contracts.Page[beta.User] ` + "`json:\"second\"`" + `
	}{First: first, Second: second})
}

func payload() contracts.User { return contracts.User{} }

func CallResult(ctx web.Context) error {
	return ctx.JSON(200, payload())
}

func Unknown(ctx web.Context) error {
	var stream chan string
	return ctx.JSON(200, stream)
}
`,
	}
	writeTypedFixtureFiles(t, root, files)
	return root, filepath.Join(root, "handler", "handler.go")
}

// writeTypedCollisionFixture creates two same-named packages while keeping only one reachable from an API response.
func writeTypedCollisionFixture(t testing.TB, includeUnrelatedHelper bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	imports := "\t\"github.com/goforj/web\"\n\talpha \"example.com/collision/alpha\"\n"
	helper := ""
	if includeUnrelatedHelper {
		imports += "\tbeta \"example.com/collision/beta\"\n"
		helper = "\nfunc helperOnly() beta.User { return beta.User{} }\n"
	}
	handler := "package handler\n\nimport (\n" + imports + ")\n\n" +
		"func Handle(ctx web.Context) error { return ctx.JSON(200, alpha.User{}) }\n" + helper
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":        "module example.com/collision\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"alpha/user.go": "package models\ntype User struct { Alpha string `json:\"alpha\"` }\n",
		"beta/user.go":  "package models\ntype User struct { Beta string `json:\"beta\"` }\n",
		"handler.go":    handler,
	})
	return root, filepath.Join(root, "handler.go")
}

// resolveTypedFixtureResponse loads and resolves Handle's response for component-stability comparisons.
func resolveTypedFixtureResponse(t testing.TB, root, handlerFile string) string {
	t.Helper()
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{
		Root:         root,
		HandlerFiles: []string{handlerFile},
	})
	if err != nil {
		t.Fatalf("load collision fixture: %v", err)
	}
	fset, _, responseArgument := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	resolved, ok := registry.resolveExpression(fset, responseArgument)
	if !ok {
		t.Fatal("resolve collision response")
	}
	return directSchemaReference(t, resolved.Schema)
}

// writeTypedFixtureFiles writes module fixtures for tests and benchmarks without relying on helpers restricted to *testing.T.
func writeTypedFixtureFiles(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for relative, contents := range files {
		absolute := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatalf("create fixture directory %s: %v", relative, err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", relative, err)
		}
	}
}

// typedFixtureWebRoot locates this checkout without relying on the test process working directory.
func typedFixtureWebRoot(t testing.TB) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate typed schema fixture source")
	}
	return filepath.Dir(filepath.Dir(currentFile))
}

// parseTypedHandlerExpressions reparses the handler independently and returns its Bind and JSON contract expressions.
func parseTypedHandlerExpressions(t testing.TB, handlerFile, functionName string) (*token.FileSet, ast.Expr, ast.Expr) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, handlerFile, nil, 0)
	if err != nil {
		t.Fatalf("parse handler fixture: %v", err)
	}
	var bindArgument ast.Expr
	var responseArgument ast.Expr
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "Bind":
				if len(call.Args) == 1 {
					bindArgument = call.Args[0]
				}
			case "JSON":
				if len(call.Args) == 2 {
					responseArgument = call.Args[1]
				}
			}
			return true
		})
	}
	if responseArgument == nil {
		t.Fatalf("function %s did not contain a JSON response", functionName)
	}
	return fset, bindArgument, responseArgument
}

// componentsByIdentity indexes a deterministic snapshot by its canonical Go identity for focused assertions.
func componentsByIdentity(components []typedSchemaComponent) map[string]typedSchemaComponent {
	out := make(map[string]typedSchemaComponent, len(components))
	for _, component := range components {
		out[component.Identity] = component
	}
	return out
}

// requireTypedComponent fails with the full identity set when a canonical component is absent.
func requireTypedComponent(t testing.TB, components map[string]typedSchemaComponent, identity string) typedSchemaComponent {
	t.Helper()
	component, ok := components[identity]
	if ok {
		return component
	}
	identities := make([]string, 0, len(components))
	for current := range components {
		identities = append(identities, current)
	}
	sort.Strings(identities)
	t.Fatalf("missing component %q in %v", identity, identities)
	return typedSchemaComponent{}
}

// schemaProperties unwraps one object schema's properties with a useful fixture failure.
func schemaProperties(t testing.TB, schema map[string]any) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no object properties: %+v", schema)
	}
	return properties
}

// directSchemaReference extracts a direct component ref from a schema value.
func directSchemaReference(t testing.TB, raw any) string {
	t.Helper()
	schema, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected schema object, got %#v", raw)
	}
	reference, ok := schema["$ref"].(string)
	if !ok || reference == "" {
		t.Fatalf("expected direct schema reference, got %+v", schema)
	}
	return reference
}

// assertSchemaValues verifies the compact primitive schema keywords relevant to a fixture property.
func assertSchemaValues(t testing.TB, raw any, expected map[string]any) {
	t.Helper()
	schema, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected schema object, got %#v", raw)
	}
	for key, value := range expected {
		if !reflect.DeepEqual(schema[key], value) {
			t.Fatalf("schema keyword %s = %#v, want %#v in %+v", key, schema[key], value, schema)
		}
	}
}

// assertSchemaAlternativeTypes verifies a literal array keeps every distinct checked primitive alternative.
func assertSchemaAlternativeTypes(t testing.TB, raw any, expected []string) {
	t.Helper()
	schema, ok := raw.(map[string]any)
	if !ok || schema["type"] != "array" {
		t.Fatalf("expected array schema, got %#v", raw)
	}
	items, ok := schema["items"].(map[string]any)
	if !ok {
		t.Fatalf("expected array items schema, got %+v", schema)
	}
	alternatives, ok := items["anyOf"].([]any)
	if !ok {
		t.Fatalf("expected heterogeneous anyOf items, got %+v", items)
	}
	typesFound := make([]string, 0, len(alternatives))
	for _, alternative := range alternatives {
		candidate, ok := alternative.(map[string]any)
		if !ok {
			t.Fatalf("expected schema alternative, got %#v", alternative)
		}
		primitive, _ := candidate["type"].(string)
		typesFound = append(typesFound, primitive)
	}
	sort.Strings(typesFound)
	if !reflect.DeepEqual(typesFound, expected) {
		t.Fatalf("array alternative types = %v, want %v", typesFound, expected)
	}
}

// hasTypedDiagnostic reports whether a registry finding has the expected strictness boundary.
func hasTypedDiagnostic(diagnostics []Diagnostic, code, severity string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Severity == severity {
			return true
		}
	}
	return false
}

// TestTypedSchemaComponentMapReturnsDetachedSchemas verifies projection skips invalid components and cannot mutate registry state.
func TestTypedSchemaComponentMapReturnsDetachedSchemas(t *testing.T) {
	var empty *typedSchemaRegistry
	if components := empty.componentSchemaMap(); components != nil {
		t.Fatalf("nil registry component map = %#v", components)
	}

	registry := &typedSchemaRegistry{componentsByID: map[string]*typedSchemaComponent{
		"valid": {
			Identity: "example.com/sample.Record",
			Name:     "SampleRecord",
			Schema:   map[string]any{"type": "object", "required": []string{"id"}},
		},
		"nil component": nil,
		"nil schema":    {Name: "Ignored"},
	}}
	components := registry.componentSchemaMap()
	want := map[string]any{
		"SampleRecord": map[string]any{"type": "object", "required": []string{"id"}},
	}
	if !reflect.DeepEqual(components, want) {
		t.Fatalf("component schema map = %#v, want %#v", components, want)
	}
	components["SampleRecord"].(map[string]any)["type"] = "changed"
	if registry.componentsByID["valid"].Schema["type"] != "object" {
		t.Fatal("component schema map retained mutable registry data")
	}
}
