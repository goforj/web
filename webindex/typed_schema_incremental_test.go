package webindex

import (
	"context"
	"encoding/json"
	"go/token"
	"go/types"
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

// TestTypedSchemaIncrementalPackageStateRejectsCorruptMetadata verifies every persisted package invariant is checked before artifact import.
func TestTypedSchemaIncrementalPackageStateRejectsCorruptMetadata(t *testing.T) {
	if !typedSchemaIncrementalPackageStateValid(validTypedSchemaIncrementalPackageState()) {
		t.Fatal("valid package state was rejected")
	}
	if typedSchemaIncrementalPackageStateValid(nil) {
		t.Fatal("nil package state was accepted")
	}

	tests := []struct {
		name   string
		mutate func(*typedSchemaIncrementalPackageState)
	}{
		{name: "empty path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Path = "" }},
		{name: "unsafe path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Path = "unsafe" }},
		{name: "cgo path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Path = "C" }},
		{name: "nul path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Path = "example.com/package\x00tail" }},
		{name: "invalid name", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Name = "invalid-name" }},
		{name: "empty artifact", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Artifact = nil }},
		{name: "oversized artifact", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Artifact = make([]byte, typedSchemaIncrementalArtifactLimit+1)
		}},
		{name: "selected external package", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Local = false }},
		{name: "empty import path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Imports[0].Path = "" }},
		{name: "empty import target", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Imports[0].Target = "" }},
		{name: "nul import path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Imports[0].Path += "\x00" }},
		{name: "nul import target", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Imports[0].Target += "\x00" }},
		{name: "unsorted imports", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Imports[0], entry.Imports[1] = entry.Imports[1], entry.Imports[0]
		}},
		{name: "duplicate imports", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Imports[1].Path = entry.Imports[0].Path
		}},
		{name: "local package without directory", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Source.Directory = "" }},
		{name: "local package without files", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Source.Files = nil }},
		{name: "stale expression path", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Expressions = nil
			entry.ExpressionArtifact = nil
		}},
		{name: "stale expression artifact", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Expressions = nil
			entry.ExpressionPath = ""
		}},
		{name: "expressions on unselected package", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Selected = false }},
		{name: "wrong expression path", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.ExpressionPath = "example.com/wrong" }},
		{name: "empty expression artifact", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.ExpressionArtifact = nil }},
		{name: "oversized expression artifact", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.ExpressionArtifact = make([]byte, typedSchemaIncrementalArtifactLimit+1)
		}},
		{name: "noncanonical expression name", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Expressions[0].Name = "E1" }},
		{name: "empty expression file", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Expressions[0].Source.File = "" }},
		{name: "negative expression offset", mutate: func(entry *typedSchemaIncrementalPackageState) { entry.Expressions[0].Source.StartOffset = -1 }},
		{name: "empty expression range", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Expressions[0].Source.EndOffset = entry.Expressions[0].Source.StartOffset
		}},
		{name: "duplicate expression range", mutate: func(entry *typedSchemaIncrementalPackageState) {
			entry.Expressions[1].Source = entry.Expressions[0].Source
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validTypedSchemaIncrementalPackageState()
			test.mutate(entry)
			if typedSchemaIncrementalPackageStateValid(entry) {
				t.Fatal("corrupt package state was accepted")
			}
		})
	}
}

// TestTypedSchemaIncrementalStateRejectsAmbiguousPersistedMetadata verifies graph-level ambiguity cannot select arbitrary cached packages.
func TestTypedSchemaIncrementalStateRejectsAmbiguousPersistedMetadata(t *testing.T) {
	root, handlerFiles := writeMultiPackageTypedSchemaFixture(t, 2)
	loaded := loadTypedSchemaIncrementalFixturePackages(t, root, handlerFiles)
	state, supported, err := buildTypedSchemaIncrementalState(context.Background(), root, "fixture-dependencies-v1", nil, loaded)
	if err != nil || !supported {
		t.Fatalf("capture incremental state: supported=%t err=%v", supported, err)
	}
	parsed, fset, diagnostics, err := parseGoFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("parse fixture diagnostics: %+v", diagnostics)
	}

	tests := []struct {
		name   string
		mutate func(testing.TB, *typedSchemaIncrementalState, *typedSchemaIncrementalRequest)
	}{
		{
			name: "duplicate package path",
			mutate: func(_ testing.TB, state *typedSchemaIncrementalState, _ *typedSchemaIncrementalRequest) {
				state.Packages = append(state.Packages, state.Packages[0])
			},
		},
		{
			name: "duplicate selected directory",
			mutate: func(t testing.TB, state *typedSchemaIncrementalState, _ *typedSchemaIncrementalRequest) {
				t.Helper()
				first := typedSchemaIncrementalStateForDirectory(t, state, filepath.Dir(handlerFiles[0]))
				second := typedSchemaIncrementalStateForDirectory(t, state, filepath.Dir(handlerFiles[1]))
				second.Source.Directory = first.Source.Directory
			},
		},
		{
			name: "handler outside selected packages",
			mutate: func(_ testing.TB, _ *typedSchemaIncrementalState, request *typedSchemaIncrementalRequest) {
				request.HandlerFiles = []string{filepath.Join(root, "unknown", "handler.go")}
			},
		},
		{
			name: "invalid handler path",
			mutate: func(_ testing.TB, _ *typedSchemaIncrementalState, request *typedSchemaIncrementalRequest) {
				request.HandlerFiles = []string{"handler\x00.go"}
			},
		},
		{
			name: "no selected handlers",
			mutate: func(_ testing.TB, _ *typedSchemaIncrementalState, request *typedSchemaIncrementalRequest) {
				request.HandlerFiles = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := roundTripTypedSchemaIncrementalState(t, state)
			request := typedSchemaIncrementalRequest{
				Root:               root,
				DependencyIdentity: "fixture-dependencies-v1",
				GoVersion:          typedSchemaIncrementalGoVersion(root),
				ParsedFiles:        parsed,
				FileSet:            fset,
				HandlerFiles:       append([]string(nil), handlerFiles...),
			}
			test.mutate(t, candidate, &request)
			_, hit, err := loadTypedSchemaRegistryIncremental(context.Background(), request, candidate)
			if err != nil {
				t.Fatalf("corrupt metadata returned an error instead of a cache miss: %v", err)
			}
			if hit {
				t.Fatal("ambiguous persisted metadata was accepted")
			}
		})
	}
}

// TestTypedSchemaIncrementalSourceSnapshotLifecycle verifies exact body changes stay incremental while package-structure changes invalidate state.
func TestTypedSchemaIncrementalSourceSnapshotLifecycle(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "active.go")
	activeSource := []byte("//go:build !windows\n\npackage sample\n\nimport \"fmt\"\n\nvar Value = fmt.Sprint(1)\n")
	baseSnapshot := map[string][]byte{
		activePath:                            activeSource,
		filepath.Join(root, "active_test.go"): []byte("package sample\n"),
		filepath.Join(root, "_ignored.go"):    []byte("package sample\n"),
		filepath.Join(root, ".ignored.go"):    []byte("package sample\n"),
		filepath.Join(root, "notes.txt"):      []byte("not Go source\n"),
	}
	state, ok := buildTypedSchemaIncrementalSourceState([]string{activePath}, baseSnapshot)
	if !ok {
		t.Fatal("build source state from valid snapshot")
	}

	tests := []struct {
		name        string
		mutate      func(testing.TB, map[string][]byte)
		wantValid   bool
		wantChanged bool
	}{
		{name: "unchanged", mutate: func(testing.TB, map[string][]byte) {}, wantValid: true},
		{
			name: "body changed",
			mutate: func(t testing.TB, snapshot map[string][]byte) {
				snapshot[activePath] = bytesReplace(t, snapshot[activePath], "fmt.Sprint(1)", "fmt.Sprint(2)")
			},
			wantValid:   true,
			wantChanged: true,
		},
		{
			name: "ignored files added",
			mutate: func(_ testing.TB, snapshot map[string][]byte) {
				snapshot[filepath.Join(root, "new_test.go")] = []byte("package sample\n")
				snapshot[filepath.Join(root, "_new.go")] = []byte("package sample\n")
				snapshot[filepath.Join(root, ".new.go")] = []byte("package sample\n")
			},
			wantValid: true,
		},
		{
			name: "import changed",
			mutate: func(t testing.TB, snapshot map[string][]byte) {
				snapshot[activePath] = bytesReplace(t, snapshot[activePath], "import \"fmt\"", "import \"strings\"")
			},
		},
		{
			name: "package changed",
			mutate: func(t testing.TB, snapshot map[string][]byte) {
				snapshot[activePath] = bytesReplace(t, snapshot[activePath], "package sample", "package changed")
			},
		},
		{
			name: "build constraint changed",
			mutate: func(t testing.TB, snapshot map[string][]byte) {
				snapshot[activePath] = bytesReplace(t, snapshot[activePath], "!windows", "linux")
			},
		},
		{
			name: "active file added",
			mutate: func(_ testing.TB, snapshot map[string][]byte) {
				snapshot[filepath.Join(root, "added.go")] = []byte("package sample\n")
			},
		},
		{name: "active file removed", mutate: func(_ testing.TB, snapshot map[string][]byte) { delete(snapshot, activePath) }},
		{name: "malformed source", mutate: func(_ testing.TB, snapshot map[string][]byte) { snapshot[activePath] = []byte("package") }},
		{
			name: "cgo source",
			mutate: func(_ testing.TB, snapshot map[string][]byte) {
				snapshot[activePath] = []byte("package sample\n\nimport \"C\"\n")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := cloneTypedSchemaSourceSnapshot(baseSnapshot)
			test.mutate(t, snapshot)
			valid, changed := typedSchemaIncrementalSourceMatches(state, nil, snapshot)
			if valid != test.wantValid || changed != test.wantChanged {
				t.Fatalf("source match = (%t, %t), want (%t, %t)", valid, changed, test.wantValid, test.wantChanged)
			}
		})
	}
}

// TestBuildTypedSchemaIncrementalSourceStateRejectsUnsafeSnapshots verifies captured package evidence must be complete and parseable.
func TestBuildTypedSchemaIncrementalSourceStateRejectsUnsafeSnapshots(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "active.go")
	otherPath := filepath.Join(root, "other", "other.go")
	tests := []struct {
		name     string
		files    []string
		snapshot map[string][]byte
	}{
		{name: "no compiled files"},
		{name: "non-go compiled file", files: []string{filepath.Join(root, "notes.txt")}},
		{
			name:  "multiple package directories",
			files: []string{activePath, otherPath},
			snapshot: map[string][]byte{
				activePath: []byte("package sample\n"),
				otherPath:  []byte("package other\n"),
			},
		},
		{name: "missing snapshot file", files: []string{activePath}, snapshot: map[string][]byte{}},
		{name: "malformed snapshot file", files: []string{activePath}, snapshot: map[string][]byte{activePath: []byte("package")}},
		{name: "cgo snapshot file", files: []string{activePath}, snapshot: map[string][]byte{activePath: []byte("package sample\n\nimport \"C\"\n")}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if state, ok := buildTypedSchemaIncrementalSourceState(test.files, test.snapshot); ok {
				t.Fatalf("unsafe source snapshot produced state: %#v", state)
			}
		})
	}
}

// TestTypedSchemaIncrementalPackageGraphRejectsAmbiguousIdentities verifies one import path cannot resolve to conflicting type universes.
func TestTypedSchemaIncrementalPackageGraphRejectsAmbiguousIdentities(t *testing.T) {
	newPackage := func(id string, path string) *packages.Package {
		return &packages.Package{
			ID:      id,
			Name:    "sample",
			PkgPath: path,
			Types:   types.NewPackage(path, "sample"),
			Imports: map[string]*packages.Package{},
		}
	}
	dependency := newPackage("dependency", "example.com/dependency")
	root := newPackage("application", "example.com/application")
	root.Imports[dependency.PkgPath] = dependency
	graph, ok := typedSchemaIncrementalPackageGraph([]*packages.Package{root})
	if !ok || len(graph) != 2 || graph[root.PkgPath] != root || graph[dependency.PkgPath] != dependency {
		t.Fatalf("valid package graph = %#v, ok=%t", graph, ok)
	}

	sameIdentity := newPackage("dependency", dependency.PkgPath)
	graph, ok = typedSchemaIncrementalPackageGraph([]*packages.Package{dependency, sameIdentity})
	if !ok || len(graph) != 1 {
		t.Fatalf("equivalent package identity was rejected: graph=%#v ok=%t", graph, ok)
	}
	conflict := newPackage("conflict", dependency.PkgPath)
	if graph, ok := typedSchemaIncrementalPackageGraph([]*packages.Package{dependency, conflict}); ok || graph != nil {
		t.Fatalf("conflicting package identities produced graph=%#v, ok=%t", graph, ok)
	}

	invalidPackages := []*packages.Package{
		nil,
		newPackage("empty", ""),
		newPackage("cgo", "C"),
		{ID: "missing-types", Name: "sample", PkgPath: "example.com/missing-types"},
	}
	for _, invalid := range invalidPackages {
		if graph, ok := typedSchemaIncrementalPackageGraph([]*packages.Package{invalid}); ok || graph != nil {
			t.Fatalf("invalid package %#v produced graph=%#v, ok=%t", invalid, graph, ok)
		}
	}
	rootWithMissingImport := newPackage("missing-import", "example.com/missing-import")
	rootWithMissingImport.Imports["example.com/missing"] = nil
	if graph, ok := typedSchemaIncrementalPackageGraph([]*packages.Package{rootWithMissingImport}); ok || graph != nil {
		t.Fatalf("nil imported package produced graph=%#v, ok=%t", graph, ok)
	}
}

// TestTypedSchemaLoadedPackageModuleMatchesSnapshot verifies only captured main and local-replacement module identities are trusted.
func TestTypedSchemaLoadedPackageModuleMatchesSnapshot(t *testing.T) {
	mainRoot := t.TempDir()
	replacementRoot := t.TempDir()
	snapshots := []typedSchemaModuleSnapshot{
		{directory: mainRoot, paths: []string{"example.com/main"}},
		{directory: replacementRoot, paths: []string{"example.com/replaced"}},
	}
	mainPackage := &packages.Package{Module: &packages.Module{Path: "example.com/main", Main: true, Dir: mainRoot}}
	if !typedSchemaLoadedPackageModuleMatchesSnapshot(mainPackage, snapshots) {
		t.Fatal("captured main module was rejected")
	}
	replacedPackage := &packages.Package{Module: &packages.Module{
		Path:    "example.com/replaced",
		Replace: &packages.Module{Dir: replacementRoot},
	}}
	if !typedSchemaLoadedPackageModuleMatchesSnapshot(replacedPackage, snapshots) {
		t.Fatal("captured local replacement was rejected")
	}

	invalid := []*packages.Package{
		nil,
		{},
		{Module: &packages.Module{}},
		{Module: &packages.Module{Path: "example.com/main", Main: true}},
		{Module: &packages.Module{Path: "example.com/main", Main: true, Dir: t.TempDir()}},
		{Module: &packages.Module{Path: "example.com/wrong", Main: true, Dir: mainRoot}},
		{Module: &packages.Module{Path: "example.com/replaced"}},
		{Module: &packages.Module{Path: "example.com/replaced", Replace: &packages.Module{Version: "v1.0.0", Dir: replacementRoot}}},
		{Module: &packages.Module{Path: "example.com/replaced", Replace: &packages.Module{}}},
	}
	for _, pkg := range invalid {
		if typedSchemaLoadedPackageModuleMatchesSnapshot(pkg, snapshots) {
			t.Fatalf("uncaptured module was accepted: %#v", pkg)
		}
	}
}

// TestTypedSchemaLoadedPackagesMatchSnapshot verifies package files and transitive local modules come from the immutable source capture.
func TestTypedSchemaLoadedPackagesMatchSnapshot(t *testing.T) {
	root := t.TempDir()
	rootFile := filepath.Join(root, "root.go")
	dependencyRoot := filepath.Join(root, "dependency")
	dependencyFile := filepath.Join(dependencyRoot, "dependency.go")
	snapshots := []typedSchemaModuleSnapshot{{directory: root, paths: []string{"example.com/application"}}}
	dependency := &packages.Package{
		ID:              "dependency",
		PkgPath:         "example.com/application/dependency",
		CompiledGoFiles: []string{dependencyFile},
		Module:          &packages.Module{Path: "example.com/application", Main: true, Dir: root},
		Imports:         map[string]*packages.Package{},
	}
	application := &packages.Package{
		ID:              "application",
		PkgPath:         "example.com/application",
		CompiledGoFiles: []string{rootFile},
		Module:          &packages.Module{Path: "example.com/application", Main: true, Dir: root},
		Imports:         map[string]*packages.Package{dependency.PkgPath: dependency},
	}
	sourceSnapshot := map[string][]byte{
		rootFile:       []byte("package application\n"),
		dependencyFile: []byte("package dependency\n"),
	}
	if !typedSchemaLoadedPackagesMatchSnapshot(root, []*packages.Package{application}, sourceSnapshot, snapshots) {
		t.Fatal("captured package graph was rejected")
	}
	if !typedSchemaLoadedPackagesMatchSnapshot(root, []*packages.Package{nil}, nil, nil) {
		t.Fatal("absent source snapshot should disable immutable-package validation")
	}
	if typedSchemaLoadedPackagesMatchSnapshot(root, []*packages.Package{nil}, sourceSnapshot, snapshots) {
		t.Fatal("nil loaded package was accepted")
	}

	withoutFiles := *application
	withoutFiles.CompiledGoFiles = nil
	if typedSchemaLoadedPackagesMatchSnapshot(root, []*packages.Package{&withoutFiles}, sourceSnapshot, snapshots) {
		t.Fatal("selected package without compiled files was accepted")
	}
	missingSource := cloneTypedSchemaSourceSnapshot(sourceSnapshot)
	delete(missingSource, rootFile)
	if typedSchemaLoadedPackagesMatchSnapshot(root, []*packages.Package{application}, missingSource, snapshots) {
		t.Fatal("package file missing from source snapshot was accepted")
	}
	wrongModule := *application
	wrongModule.Module = &packages.Module{Path: "example.com/other", Main: true, Dir: root}
	if typedSchemaLoadedPackagesMatchSnapshot(root, []*packages.Package{&wrongModule}, sourceSnapshot, snapshots) {
		t.Fatal("package from uncaptured module identity was accepted")
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

// validTypedSchemaIncrementalPackageState returns the smallest package entry satisfying every persisted-state invariant.
func validTypedSchemaIncrementalPackageState() *typedSchemaIncrementalPackageState {
	path := "example.com/application/handler"
	sourceFile := filepath.Join("testdata", "application", "handler.go")
	return &typedSchemaIncrementalPackageState{
		Path:     path,
		Name:     "handler",
		Selected: true,
		Local:    true,
		Imports: []typedSchemaIncrementalImportState{
			{Path: "encoding/json", Target: "encoding/json"},
			{Path: "net/http", Target: "net/http"},
		},
		Source: typedSchemaIncrementalSourceState{
			Directory: filepath.Dir(sourceFile),
			Files: []typedSchemaIncrementalFileState{
				{Path: sourceFile, ContentHash: "content", HeaderHash: "header"},
			},
			DirectoryFiles: []typedSchemaIncrementalHeaderState{
				{Path: sourceFile, HeaderHash: "header"},
			},
		},
		Artifact:           []byte("package artifact"),
		ExpressionPath:     typedSchemaIncrementalExpressionPackagePath(path),
		ExpressionArtifact: []byte("expression artifact"),
		Expressions: []typedSchemaIncrementalExpressionState{
			{Name: "E000000", Source: typedSourceRange{File: sourceFile, StartOffset: 10, EndOffset: 20, Line: 2}},
			{Name: "E000001", Source: typedSourceRange{File: sourceFile, StartOffset: 30, EndOffset: 40, Line: 3}},
		},
	}
}

// cloneTypedSchemaSourceSnapshot copies captured bytes so lifecycle mutations cannot leak between table cases.
func cloneTypedSchemaSourceSnapshot(snapshot map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(snapshot))
	for path, data := range snapshot {
		cloned[path] = append([]byte(nil), data...)
	}
	return cloned
}

// bytesReplace applies one required fixture mutation without hiding a stale test input.
func bytesReplace(t testing.TB, source []byte, old string, replacement string) []byte {
	t.Helper()
	changed := strings.Replace(string(source), old, replacement, 1)
	if changed == string(source) {
		t.Fatalf("fixture source did not contain %q", old)
	}
	return []byte(changed)
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

// TestTypedSchemaIncrementalUnsupportedReportsReason verifies safe cache misses retain their diagnostic reason.
func TestTypedSchemaIncrementalUnsupportedReportsReason(t *testing.T) {
	want := "unsupported package graph"
	if got := (typedSchemaIncrementalUnsupported{reason: want}).Error(); got != want {
		t.Fatalf("typedSchemaIncrementalUnsupported.Error() = %q, want %q", got, want)
	}
}

// TestTypedSchemaIncrementalFilesystemReaders verifies uncached reads fingerprint active Go files and fail closed on incomplete or cgo sources.
func TestTypedSchemaIncrementalFilesystemReaders(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "active.go")
	activeSource := []byte("package sample\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")
	if err := os.WriteFile(activePath, activeSource, 0o644); err != nil {
		t.Fatalf("write active source: %v", err)
	}
	for name, source := range map[string]string{
		"active_test.go": "package sample\n",
		"_ignored.go":    "package sample\n",
		".ignored.go":    "package sample\n",
		"notes.txt":      "not Go source\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
			t.Fatalf("write ignored fixture %q: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "nested.go"), 0o755); err != nil {
		t.Fatalf("create directory fixture: %v", err)
	}

	state, ok, importsC := readTypedSchemaIncrementalFileState(activePath)
	if !ok || importsC || state.Path != activePath || state.ContentHash == "" || state.HeaderHash == "" {
		t.Fatalf("active file state = %#v, ok=%t, imports_c=%t", state, ok, importsC)
	}
	if _, ok, importsC := readTypedSchemaIncrementalFileState(filepath.Join(root, "missing.go")); ok || importsC {
		t.Fatalf("missing file was accepted: ok=%t imports_c=%t", ok, importsC)
	}

	headers, ok := readTypedSchemaIncrementalDirectoryHeaders(root)
	if !ok || len(headers) != 1 || headers[0].Path != activePath || headers[0].HeaderHash != state.HeaderHash {
		t.Fatalf("directory headers = %#v, ok=%t", headers, ok)
	}

	cgoPath := filepath.Join(root, "cgo.go")
	if err := os.WriteFile(cgoPath, []byte("package sample\n\nimport \"C\"\n"), 0o644); err != nil {
		t.Fatalf("write cgo fixture: %v", err)
	}
	if _, ok, importsC := readTypedSchemaIncrementalFileState(cgoPath); ok || !importsC {
		t.Fatalf("cgo file state: ok=%t imports_c=%t", ok, importsC)
	}
	if headers, ok := readTypedSchemaIncrementalDirectoryHeaders(root); ok || headers != nil {
		t.Fatalf("directory containing cgo returned headers=%#v, ok=%t", headers, ok)
	}
	if headers, ok := readTypedSchemaIncrementalDirectoryHeaders(filepath.Join(root, "missing")); ok || headers != nil {
		t.Fatalf("missing directory returned headers=%#v, ok=%t", headers, ok)
	}
}
