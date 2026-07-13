package webindex

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestTypedSchemaRegistryProjectsLiteralValuesAndNilCapableContainers verifies exact map keys do not erase checked child contracts.
func TestTypedSchemaRegistryProjectsLiteralValuesAndNilCapableContainers(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/literals\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"contracts/contracts.go": `package contracts
type Tagged struct {
	Display string ` + "`json:\"display_name\"`" + `
	Ignored string ` + "`json:\"-\"`" + `
}
type EncodedByte byte
func (EncodedByte) MarshalJSON() ([]byte, error) { return []byte("0"), nil }
type EncodedSlice []int
func (EncodedSlice) MarshalJSON() ([]byte, error) { return []byte("[]"), nil }
type EncodedMap map[string]int
func (EncodedMap) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }
type BadNamedMap map[bool]string
type QuotedInt int
type QuotedLabel string
const (
	quotedHidden QuotedLabel = "hidden"
	QuotedVisible QuotedLabel = "visible"
)
type Container struct {
	Raw []byte ` + "`json:\"raw\"`" + `
	Coded []EncodedByte ` + "`json:\"coded\"`" + `
	Quoted EncodedByte ` + "`json:\"quoted,string\"`" + `
	Bad map[bool]string ` + "`json:\"bad\"`" + `
	NamedBad BadNamedMap ` + "`json:\"named_bad\"`" + `
	QuotedInt QuotedInt ` + "`json:\"quoted_int,string\"`" + `
	QuotedLabel QuotedLabel ` + "`json:\"quoted_label,string\"`" + `
	Values map[string]int ` + "`json:\"values\"`" + `
}
`,
		"handler.go": `package handler
import (
	"github.com/goforj/web"
	"example.com/literals/contracts"
)
func Handle(ctx web.Context) error {
	return ctx.JSON(200, map[string]any{
		"tagged": contracts.Tagged{Display: "shown", Ignored: "hidden"},
		"coded": []contracts.EncodedByte{1},
		"encoded_slice": contracts.EncodedSlice{1},
		"encoded_map": contracts.EncodedMap{"one": 1},
		"mixed": []any{"text", 1, true},
		"fixed": [2]any{"text", 1},
		"container": contracts.Container{},
	})
}
`,
	})
	handlerFile := filepath.Join(root, "handler.go")
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{Root: root, HandlerFiles: []string{handlerFile}})
	if err != nil {
		t.Fatalf("load literal fixture: %v", err)
	}
	fset, _, response := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	resolved, ok := registry.resolveJSONExpression(fset, response)
	if !ok {
		t.Fatal("resolve typed literal response")
	}
	if resolved.Schema["additionalProperties"] != false {
		t.Fatalf("literal map must expose fixed keys: %+v", resolved.Schema)
	}
	wantRequired := []string{"coded", "container", "encoded_map", "encoded_slice", "fixed", "mixed", "tagged"}
	if got := resolved.Schema["required"]; !reflect.DeepEqual(got, wantRequired) {
		t.Fatalf("literal map required keys = %#v, want %#v", got, wantRequired)
	}
	properties := schemaProperties(t, resolved.Schema)
	if reference := directSchemaReference(t, properties["tagged"]); !strings.HasPrefix(reference, "#/components/schemas/ContractsTagged") {
		t.Fatalf("tagged literal did not retain checked component: %q", reference)
	}
	assertSchemaAlternativeTypes(t, properties["mixed"], []string{"boolean", "integer", "string"})
	assertSchemaAlternativeTypes(t, properties["fixed"], []string{"integer", "string"})
	fixed := properties["fixed"].(map[string]any)
	if fixed["minItems"] != int64(2) || fixed["maxItems"] != int64(2) {
		t.Fatalf("fixed array bounds were lost: %+v", fixed)
	}
	coded := properties["coded"].(map[string]any)
	if coded["type"] != "array" || len(coded["items"].(map[string]any)) != 0 || coded["format"] == "byte" {
		t.Fatalf("custom-codec byte elements must bypass the base64 shortcut: %+v", coded)
	}
	for _, name := range []string{"encoded_map", "encoded_slice"} {
		if len(properties[name].(map[string]any)) != 0 {
			t.Fatalf("named container codec %q exposed its underlying literal: %+v", name, properties[name])
		}
	}

	components := componentsByIdentity(registry.componentSnapshot())
	tagged := requireTypedComponent(t, components, "example.com/literals/contracts.Tagged")
	taggedProperties := schemaProperties(t, tagged.Schema)
	if taggedProperties["display_name"] == nil || taggedProperties["Ignored"] != nil {
		t.Fatalf("typed map value ignored JSON tags: %+v", taggedProperties)
	}
	container := requireTypedComponent(t, components, "example.com/literals/contracts.Container")
	containerProperties := schemaProperties(t, container.Schema)
	assertSchemaValues(t, containerProperties["raw"], map[string]any{"type": "string", "format": "byte", "nullable": true})
	assertSchemaValues(t, containerProperties["coded"], map[string]any{"type": "array", "nullable": true})
	if len(containerProperties["coded"].(map[string]any)["items"].(map[string]any)) != 0 {
		t.Fatalf("custom-codec element schema must remain unconstrained: %+v", containerProperties["coded"])
	}
	if len(containerProperties["quoted"].(map[string]any)) != 0 {
		t.Fatalf("json string option must not hide a custom codec: %+v", containerProperties["quoted"])
	}
	if len(containerProperties["bad"].(map[string]any)) != 0 {
		t.Fatalf("unsupported map keys must remain unconstrained: %+v", containerProperties["bad"])
	}
	if len(containerProperties["named_bad"].(map[string]any)) != 0 {
		t.Fatalf("named maps cannot hide unsupported key types behind a component: %+v", containerProperties["named_bad"])
	}
	if _, exists := components["example.com/literals/contracts.BadNamedMap"]; exists {
		t.Fatal("unsupported named map must not publish a misleading component")
	}
	assertSchemaValues(t, containerProperties["quoted_int"], map[string]any{"type": "string"})
	if _, exists := components["example.com/literals/contracts.QuotedInt"]; exists {
		t.Fatal("string-encoded scalar must not publish an unused storage component")
	}
	assertSchemaValues(t, containerProperties["quoted_label"], map[string]any{"type": "string", "enum": []any{`"visible"`}})
	if _, exists := components["example.com/literals/contracts.QuotedLabel"]; exists {
		t.Fatal("string-encoded enum must remain inline instead of publishing an unused storage component")
	}
	assertSchemaValues(t, containerProperties["values"], map[string]any{"type": "object", "nullable": true})
	for _, code := range []string{"custom_marshaler_schema", "unsupported_map_key_type"} {
		if !hasTypedDiagnostic(registry.diagnosticsSnapshot(), code, "warn") {
			t.Fatalf("missing %s diagnostic: %+v", code, registry.diagnosticsSnapshot())
		}
	}
}

// TestTypedSchemaRegistryAllocatesGloballyUniqueComponentNames verifies every readable collision class is resolved from canonical identities.
func TestTypedSchemaRegistryAllocatesGloballyUniqueComponentNames(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":        "module example.com/names\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"lower/user.go": "package models\ntype User struct { Lower string `json:\"lower\"` }\n",
		"upper/user.go": "package Models\ntype User struct { Upper string `json:\"upper\"` }\n",
		"contracts/contracts.go": `package contracts
import (
	lower "example.com/names/lower"
	upper "example.com/names/upper"
)
type Alias = string
type Box[T any] struct { Value T ` + "`json:\"value\"`" + ` }
type BoxOfInt struct { Plain int ` + "`json:\"plain\"`" + ` }
type BoxOfListOfInt struct { Plain []int ` + "`json:\"plain\"`" + ` }
type 用户 struct { Value string ` + "`json:\"value\"`" + ` }
type 合同 struct { Value string ` + "`json:\"value\"`" + ` }
type Payload struct {
	Generic Box[int] ` + "`json:\"generic\"`" + `
	Plain BoxOfInt ` + "`json:\"plain\"`" + `
	Nested Box[[]int] ` + "`json:\"nested\"`" + `
	NestedPlain BoxOfListOfInt ` + "`json:\"nested_plain\"`" + `
	Pointer Box[*int] ` + "`json:\"pointer\"`" + `
	AliasBox Box[Alias] ` + "`json:\"alias_box\"`" + `
	StringBox Box[string] ` + "`json:\"string_box\"`" + `
	UnicodeA 用户 ` + "`json:\"unicode_a\"`" + `
	UnicodeB 合同 ` + "`json:\"unicode_b\"`" + `
	Lower lower.User ` + "`json:\"lower\"`" + `
	Upper upper.User ` + "`json:\"upper\"`" + `
}
`,
		"handler.go": `package handler
import (
	"github.com/goforj/web"
	"example.com/names/contracts"
)
func Handle(ctx web.Context) error { return ctx.JSON(200, contracts.Payload{}) }
`,
	})
	handlerFile := filepath.Join(root, "handler.go")
	registry, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{Root: root, HandlerFiles: []string{handlerFile}})
	if err != nil {
		t.Fatalf("load component-name fixture: %v", err)
	}
	fset, _, response := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	if _, ok := registry.resolveExpression(fset, response); !ok {
		t.Fatal("resolve component-name fixture")
	}
	components := componentsByIdentity(registry.componentSnapshot())
	generic := requireTypedComponent(t, components, "example.com/names/contracts.Box[int]")
	plain := requireTypedComponent(t, components, "example.com/names/contracts.BoxOfInt")
	if generic.Name == plain.Name || !strings.HasPrefix(generic.Name, "ContractsBoxOfInt_") || !strings.HasPrefix(plain.Name, "ContractsBoxOfInt_") {
		t.Fatalf("generic/non-generic collision was not deterministic: %q %q", generic.Name, plain.Name)
	}
	nested := requireTypedComponent(t, components, "example.com/names/contracts.Box[[]int]")
	nestedPlain := requireTypedComponent(t, components, "example.com/names/contracts.BoxOfListOfInt")
	if nested.Name == nestedPlain.Name || !strings.HasPrefix(nested.Name, "ContractsBoxOfListOfInt_") || !strings.HasPrefix(nestedPlain.Name, "ContractsBoxOfListOfInt_") {
		t.Fatalf("nested generic collision was not deterministic: %q %q", nested.Name, nestedPlain.Name)
	}
	pointer := requireTypedComponent(t, components, "example.com/names/contracts.Box[*int]")
	if !strings.Contains(pointer.Name, "PointerToInt") {
		t.Fatalf("pointer generic argument collapsed into its value form: %q", pointer.Name)
	}
	aliasBox := requireTypedComponent(t, components, "example.com/names/contracts.Box[string]")
	if strings.Contains(aliasBox.Identity, "Alias") {
		t.Fatalf("true aliases must collapse into their semantic target: %q", aliasBox.Identity)
	}
	unicodeA := requireTypedComponent(t, components, "example.com/names/contracts.用户")
	unicodeB := requireTypedComponent(t, components, "example.com/names/contracts.合同")
	if !strings.HasPrefix(unicodeA.Name, "ContractsContract_") || !strings.HasPrefix(unicodeB.Name, "ContractsContract_") || unicodeA.Name == unicodeB.Name {
		t.Fatalf("Unicode sanitization collision produced unsafe names: %q %q", unicodeA.Name, unicodeB.Name)
	}
	lower := requireTypedComponent(t, components, "example.com/names/lower.User")
	upper := requireTypedComponent(t, components, "example.com/names/upper.User")
	if lower.Name == upper.Name || !strings.HasPrefix(lower.Name, "ModelsUser_") || !strings.HasPrefix(upper.Name, "ModelsUser_") {
		t.Fatalf("case-folded package collision was not resolved: %q %q", lower.Name, upper.Name)
	}
	seen := map[string]string{}
	for _, component := range registry.componentSnapshot() {
		if !isCodegenSafeIdentifier(component.Name) {
			t.Fatalf("component name is not codegen-safe: %q", component.Name)
		}
		key := strings.ToLower(component.Name)
		if previous := seen[key]; previous != "" {
			t.Fatalf("case-insensitive component collision: %q and %q", previous, component.Name)
		}
		seen[key] = component.Name
	}
}

// TestRunNamesComponentsFromReachableContractsOnly verifies an unrouted JSON call in the same loaded file cannot rename an active schema.
func TestRunNamesComponentsFromReachableContractsOnly(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":        "module example.com/reachable\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"alpha/user.go": "package models\ntype User struct { Active string `json:\"active\"` }\n",
		"beta/user.go":  "package models\ntype User struct { Inactive string `json:\"inactive\"` }\n",
		"handler.go": `package handler
import (
	"net/http"
	"github.com/goforj/web"
	alpha "example.com/reachable/alpha"
	beta "example.com/reachable/beta"
)
type Controller struct{}
func (c *Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/active", c.Handle)} }
func (c *Controller) Handle(ctx web.Context) error { return ctx.JSON(200, alpha.User{}) }
func (c *Controller) Inactive(ctx web.Context) error { return ctx.JSON(200, beta.User{}) }
`,
	})
	manifest, err := Run(context.Background(), IndexOptions{Root: root})
	if err != nil {
		t.Fatalf("index reachable fixture: %v", err)
	}
	if len(manifest.Operations) != 1 || len(manifest.Operations[0].Outputs.Responses) != 1 {
		t.Fatalf("unexpected reachable operations: %+v", manifest.Operations)
	}
	reference := directSchemaReference(t, manifest.Operations[0].Outputs.Responses[0].Schema)
	if reference != "#/components/schemas/ModelsUser" {
		t.Fatalf("unreachable contract renamed active component: %q", reference)
	}
	if len(manifest.Schemas) != 1 || manifest.Schemas[0].Identity != "example.com/reachable/alpha.User" {
		t.Fatalf("unreachable component was published: %+v", manifest.Schemas)
	}
}
