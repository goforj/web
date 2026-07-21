package webindex

import (
	"context"
	"encoding/json"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestTypedSchemaIncrementalStateChecksOnlyChangedSelectedPackage verifies persisted types survive a round trip while one changed root is checked directly.
func TestTypedSchemaIncrementalStateChecksOnlyChangedSelectedPackage(t *testing.T) {
	root, handlerFiles := writeMultiPackageTypedSchemaFixture(t, 2)
	loaded := loadTypedSchemaIncrementalFixturePackages(t, root, handlerFiles)
	state, supported, err := buildTypedSchemaIncrementalState(context.Background(), root, "fixture-dependencies-v1", nil, loaded)
	if err != nil {
		t.Fatalf("capture incremental typed state: %v", err)
	}
	if !supported {
		t.Fatal("initial package graph did not support incremental typed state")
	}
	state = roundTripTypedSchemaIncrementalState(t, state)

	changedPath := handlerFiles[0]
	contents, err := os.ReadFile(changedPath)
	if err != nil {
		t.Fatalf("read changed handler: %v", err)
	}
	changed := strings.Replace(string(contents), "Value string", "Value int", 1)
	if changed == string(contents) {
		t.Fatal("fixture handler did not contain the expected contract field")
	}
	if err := os.WriteFile(changedPath, []byte(changed), 0o644); err != nil {
		t.Fatalf("change selected handler contract: %v", err)
	}

	parsed, fset, diagnostics, err := parseGoFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("parse changed fixture: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("parse changed fixture diagnostics: %+v", diagnostics)
	}
	result, hit, err := loadTypedSchemaRegistryIncremental(context.Background(), typedSchemaIncrementalRequest{
		Root:               root,
		DependencyIdentity: "fixture-dependencies-v1",
		GoVersion:          typedSchemaIncrementalGoVersion(root),
		ParsedFiles:        parsed,
		FileSet:            fset,
		HandlerFiles:       handlerFiles,
	}, state)
	if err != nil {
		t.Fatalf("load incremental typed registry: %v", err)
	}
	if !hit {
		t.Fatal("changed selected package unexpectedly fell back to package loading")
	}
	if !reflect.DeepEqual(result.CheckedPackages, []string{"example.com/typedmulti/handlers/h00"}) {
		t.Fatalf("directly checked packages = %v, want only h00", result.CheckedPackages)
	}
	if !reflect.DeepEqual(result.RestoredPackages, []string{"example.com/typedmulti/handlers/h01"}) {
		t.Fatalf("restored packages = %v, want only h01", result.RestoredPackages)
	}
	if result.State == nil {
		t.Fatal("changed package did not produce refreshed incremental state")
	}
	resolveTypedSchemaIncrementalPayloads(t, result.Registry, handlerFiles)
	assertTypedSchemaIncrementalPayloadType(t, result.Registry, "example.com/typedmulti/handlers/h00.Payload", "integer")
	assertTypedSchemaIncrementalPayloadType(t, result.Registry, "example.com/typedmulti/handlers/h01.Payload", "string")

	cold, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{Root: root, HandlerFiles: handlerFiles})
	if err != nil {
		t.Fatalf("load cold registry for parity: %v", err)
	}
	resolveTypedSchemaIncrementalPayloads(t, cold, handlerFiles)
	if !reflect.DeepEqual(result.Registry.componentSnapshot(), cold.componentSnapshot()) {
		t.Fatalf("incremental components differ from cold load\nincremental: %+v\ncold: %+v", result.Registry.componentSnapshot(), cold.componentSnapshot())
	}
	if !reflect.DeepEqual(result.Registry.diagnosticsSnapshot(), cold.diagnosticsSnapshot()) {
		t.Fatalf("incremental diagnostics differ from cold load: incremental=%+v cold=%+v", result.Registry.diagnosticsSnapshot(), cold.diagnosticsSnapshot())
	}

	refreshed := roundTripTypedSchemaIncrementalState(t, result.State)
	second, secondHit, err := loadTypedSchemaRegistryIncremental(context.Background(), typedSchemaIncrementalRequest{
		Root:               root,
		DependencyIdentity: "fixture-dependencies-v1",
		GoVersion:          typedSchemaIncrementalGoVersion(root),
		ParsedFiles:        parsed,
		FileSet:            token.NewFileSet(),
		HandlerFiles:       handlerFiles,
	}, refreshed)
	if err != nil {
		t.Fatalf("restore refreshed incremental state: %v", err)
	}
	if !secondHit || len(second.CheckedPackages) != 0 || len(second.RestoredPackages) != 2 {
		t.Fatalf("refreshed state did not restore both roots: hit=%t checked=%v restored=%v", secondHit, second.CheckedPackages, second.RestoredPackages)
	}
}

// TestTypedSchemaIncrementalStateRestoresLiteralSchemas verifies persisted expression artifacts retain constants needed to project fixed map and array shapes.
func TestTypedSchemaIncrementalStateRestoresLiteralSchemas(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/incrementalliterals\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"handler.go": `package handler

import "github.com/goforj/web"

func Handle(ctx web.Context) error {
	return ctx.JSON(200, map[string]any{
		"fixed": [2]any{"text", 1},
		"mixed": []any{"text", 1, true},
	})
}
`,
	})
	handlerFile := filepath.Join(root, "handler.go")
	loaded := loadTypedSchemaIncrementalFixturePackages(t, root, []string{handlerFile})
	state, supported, err := buildTypedSchemaIncrementalState(context.Background(), root, "fixture-dependencies-v1", nil, loaded)
	if err != nil || !supported {
		t.Fatalf("capture incremental literal state: supported=%t err=%v", supported, err)
	}
	state = roundTripTypedSchemaIncrementalState(t, state)
	parsed, fset, diagnostics, err := parseGoFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("parse incremental literal fixture: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("parse incremental literal fixture diagnostics: %+v", diagnostics)
	}
	result, hit, err := loadTypedSchemaRegistryIncremental(context.Background(), typedSchemaIncrementalRequest{
		Root:               root,
		DependencyIdentity: "fixture-dependencies-v1",
		GoVersion:          typedSchemaIncrementalGoVersion(root),
		ParsedFiles:        parsed,
		FileSet:            fset,
		HandlerFiles:       []string{handlerFile},
	}, state)
	if err != nil {
		t.Fatalf("restore incremental literal state: %v", err)
	}
	if !hit || len(result.CheckedPackages) != 0 || len(result.RestoredPackages) != 1 {
		t.Fatalf("literal state was not restored without checking: hit=%t checked=%v restored=%v", hit, result.CheckedPackages, result.RestoredPackages)
	}
	cold, err := loadTypedSchemaRegistry(context.Background(), typedSchemaLoadOptions{Root: root, HandlerFiles: []string{handlerFile}})
	if err != nil {
		t.Fatalf("load cold literal registry: %v", err)
	}
	lookupSet, _, response := parseTypedHandlerExpressions(t, handlerFile, "Handle")
	incrementalSchema, incrementalOK := result.Registry.resolveJSONExpression(lookupSet, response)
	coldSchema, coldOK := cold.resolveJSONExpression(lookupSet, response)
	if !incrementalOK || !coldOK {
		t.Fatalf("resolve literal response: incremental=%t cold=%t", incrementalOK, coldOK)
	}
	if !reflect.DeepEqual(incrementalSchema, coldSchema) {
		t.Fatalf("restored literal schema differs from cold load\nincremental: %+v\ncold: %+v", incrementalSchema, coldSchema)
	}
}

// TestTypedSchemaIncrementalStateFallsBackOnUntrustedInputs verifies unsupported or stale state never becomes partial type information.
func TestTypedSchemaIncrementalStateFallsBackOnUntrustedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(testing.TB, string, []string, *typedSchemaIncrementalState)
	}{
		{
			name: "dependency identity",
			mutate: func(_ testing.TB, _ string, _ []string, state *typedSchemaIncrementalState) {
				state.DependencyIdentity = "different-dependencies"
			},
		},
		{
			name: "corrupt package artifact",
			mutate: func(t testing.TB, _ string, handlers []string, state *typedSchemaIncrementalState) {
				t.Helper()
				entry := typedSchemaIncrementalStateForDirectory(t, state, filepath.Dir(handlers[0]))
				entry.Artifact = []byte("not export data")
			},
		},
		{
			name: "import header",
			mutate: func(t testing.TB, _ string, handlers []string, _ *typedSchemaIncrementalState) {
				t.Helper()
				contents, err := os.ReadFile(handlers[0])
				if err != nil {
					t.Fatalf("read handler for import mutation: %v", err)
				}
				changed := strings.Replace(string(contents), `import "github.com/goforj/web"`, "import (\n\t\"fmt\"\n\t\"github.com/goforj/web\"\n)\n\nvar _ = fmt.Sprintf", 1)
				if err := os.WriteFile(handlers[0], []byte(changed), 0o644); err != nil {
					t.Fatalf("change handler imports: %v", err)
				}
			},
		},
		{
			name: "new package file",
			mutate: func(t testing.TB, _ string, handlers []string, _ *typedSchemaIncrementalState) {
				t.Helper()
				path := filepath.Join(filepath.Dir(handlers[0]), "added.go")
				if err := os.WriteFile(path, []byte("package h00\n"), 0o644); err != nil {
					t.Fatalf("add handler package file: %v", err)
				}
			},
		},
		{
			name: "changed package type error",
			mutate: func(t testing.TB, _ string, handlers []string, _ *typedSchemaIncrementalState) {
				t.Helper()
				contents, err := os.ReadFile(handlers[0])
				if err != nil {
					t.Fatalf("read handler for type-error mutation: %v", err)
				}
				changed := strings.Replace(string(contents), "Value string", "Value Missing", 1)
				if changed == string(contents) {
					t.Fatal("handler did not contain the expected contract field")
				}
				if err := os.WriteFile(handlers[0], []byte(changed), 0o644); err != nil {
					t.Fatalf("change handler to an undefined contract type: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, handlerFiles := writeMultiPackageTypedSchemaFixture(t, 2)
			loaded := loadTypedSchemaIncrementalFixturePackages(t, root, handlerFiles)
			state, supported, err := buildTypedSchemaIncrementalState(context.Background(), root, "fixture-dependencies-v1", nil, loaded)
			if err != nil || !supported {
				t.Fatalf("capture incremental state: supported=%t err=%v", supported, err)
			}
			state = roundTripTypedSchemaIncrementalState(t, state)
			test.mutate(t, root, handlerFiles, state)

			parsed, fset, _, err := parseGoFiles(context.Background(), root, nil)
			if err != nil {
				t.Fatalf("parse mutated fixture: %v", err)
			}
			_, hit, err := loadTypedSchemaRegistryIncremental(context.Background(), typedSchemaIncrementalRequest{
				Root:               root,
				DependencyIdentity: "fixture-dependencies-v1",
				GoVersion:          typedSchemaIncrementalGoVersion(root),
				ParsedFiles:        parsed,
				FileSet:            fset,
				HandlerFiles:       handlerFiles,
			}, state)
			if err != nil {
				t.Fatalf("unsupported state returned an error instead of a fallback: %v", err)
			}
			if hit {
				t.Fatal("unsupported state was accepted")
			}
		})
	}
}

// TestTypedSchemaIncrementalStateFallsBackOnLocalDependencyChange protects restored package exports from stale local types.
func TestTypedSchemaIncrementalStateFallsBackOnLocalDependencyChange(t *testing.T) {
	root, handlerFile := writeTypedSchemaFixture(t)
	loaded := loadTypedSchemaIncrementalFixturePackages(t, root, []string{handlerFile})
	state, supported, err := buildTypedSchemaIncrementalState(context.Background(), root, "fixture-dependencies-v1", nil, loaded)
	if err != nil || !supported {
		t.Fatalf("capture incremental state: supported=%t err=%v", supported, err)
	}
	contractPath := filepath.Join(root, "contracts", "types.go")
	contents, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read local contract dependency: %v", err)
	}
	changed := strings.Replace(string(contents), `json:"alias"`, `json:"renamed_alias"`, 1)
	if err := os.WriteFile(contractPath, []byte(changed), 0o644); err != nil {
		t.Fatalf("change local contract dependency: %v", err)
	}
	parsed, fset, _, err := parseGoFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("parse dependency change: %v", err)
	}
	_, hit, err := loadTypedSchemaRegistryIncremental(context.Background(), typedSchemaIncrementalRequest{
		Root:               root,
		DependencyIdentity: "fixture-dependencies-v1",
		GoVersion:          typedSchemaIncrementalGoVersion(root),
		ParsedFiles:        parsed,
		FileSet:            fset,
		HandlerFiles:       []string{handlerFile},
	}, state)
	if err != nil {
		t.Fatalf("local dependency change returned an error instead of a fallback: %v", err)
	}
	if hit {
		t.Fatal("local dependency change reused stale package exports")
	}
}

// TestTypedSchemaIncrementalStateFallsBackAcrossUnselectedDependent verifies cached intermediates cannot hide a changed selected export.
func TestTypedSchemaIncrementalStateFallsBackAcrossUnselectedDependent(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/transitive\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"a/handler.go": `package a

import "github.com/goforj/web"

const N = 1
type Direct [N]string
func Handle(ctx web.Context) error { return ctx.JSON(200, Direct{}) }
`,
		"mid/payload.go": `package mid

import "example.com/transitive/a"

type Payload [a.N]string
`,
		"c/handler.go": `package c

import (
	"example.com/transitive/mid"
	"github.com/goforj/web"
)

func Handle(ctx web.Context) error { return ctx.JSON(200, mid.Payload{}) }
`,
	})
	handlerFiles := []string{filepath.Join(root, "a", "handler.go"), filepath.Join(root, "c", "handler.go")}
	loaded := loadTypedSchemaIncrementalFixturePackages(t, root, handlerFiles)
	state, supported, err := buildTypedSchemaIncrementalState(context.Background(), root, "fixture-dependencies-v1", nil, loaded)
	if err != nil || !supported {
		t.Fatalf("capture transitive incremental state: supported=%t err=%v", supported, err)
	}
	contents, err := os.ReadFile(handlerFiles[0])
	if err != nil {
		t.Fatalf("read changed selected dependency: %v", err)
	}
	changed := strings.Replace(string(contents), "const N = 1", "const N = 2", 1)
	if err := os.WriteFile(handlerFiles[0], []byte(changed), 0o644); err != nil {
		t.Fatalf("change selected dependency export: %v", err)
	}
	parsed, fset, _, err := parseGoFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("parse transitive dependency fixture: %v", err)
	}
	_, hit, err := loadTypedSchemaRegistryIncremental(context.Background(), typedSchemaIncrementalRequest{
		Root:               root,
		DependencyIdentity: "fixture-dependencies-v1",
		GoVersion:          typedSchemaIncrementalGoVersion(root),
		ParsedFiles:        parsed,
		FileSet:            fset,
		HandlerFiles:       handlerFiles,
	}, state)
	if err != nil {
		t.Fatalf("transitive selected dependency returned an error instead of fallback: %v", err)
	}
	if hit {
		t.Fatal("selected dependency change crossed an unselected cached package")
	}
}

// loadTypedSchemaIncrementalFixturePackages obtains the cold graph used to build the private incremental state.
func loadTypedSchemaIncrementalFixturePackages(t testing.TB, root string, handlerFiles []string) []*packages.Package {
	t.Helper()
	loaded, err := packages.Load(&packages.Config{
		Dir: root,
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedExportFile |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedTypesSizes |
			packages.NeedImports |
			packages.NeedModule,
	}, typedPackagePatterns(root, handlerFiles)...)
	if err != nil {
		t.Fatalf("load incremental fixture packages: %v", err)
	}
	return uniqueTypedPackages(loaded)
}

// roundTripTypedSchemaIncrementalState proves the core state contains no process-only values.
func roundTripTypedSchemaIncrementalState(t testing.TB, state *typedSchemaIncrementalState) *typedSchemaIncrementalState {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encode incremental typed state: %v", err)
	}
	var decoded typedSchemaIncrementalState
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode incremental typed state: %v", err)
	}
	return &decoded
}

// typedSchemaIncrementalStateForDirectory returns the selected state entry for one fixture directory.
func typedSchemaIncrementalStateForDirectory(t testing.TB, state *typedSchemaIncrementalState, directory string) *typedSchemaIncrementalPackageState {
	t.Helper()
	directory = filepath.Clean(directory)
	for index := range state.Packages {
		if state.Packages[index].Source.Directory == directory {
			return &state.Packages[index]
		}
	}
	t.Fatalf("incremental state has no package for %s", directory)
	return nil
}

// assertTypedSchemaIncrementalPayloadType verifies a restored or directly checked component retains its field schema.
func assertTypedSchemaIncrementalPayloadType(t testing.TB, registry *typedSchemaRegistry, identity string, expectedType string) {
	t.Helper()
	component := requireTypedComponent(t, componentsByIdentity(registry.componentSnapshot()), identity)
	properties := schemaProperties(t, component.Schema)
	value, ok := properties["value"].(map[string]any)
	if !ok || value["type"] != expectedType {
		t.Fatalf("component %s value schema = %#v, want type %q", identity, properties["value"], expectedType)
	}
}

// resolveTypedSchemaIncrementalPayloads exercises the same expression lookup that normalization uses before comparing component graphs.
func resolveTypedSchemaIncrementalPayloads(t testing.TB, registry *typedSchemaRegistry, handlerFiles []string) {
	t.Helper()
	for _, handlerFile := range handlerFiles {
		fset, _, response := parseTypedHandlerExpressions(t, handlerFile, "Handle")
		if _, ok := registry.resolveExpression(fset, response); !ok {
			t.Fatalf("resolve incremental payload in %s", handlerFile)
		}
	}
}
