package webindex

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// indexCacheReadRange records one random-access read for section-isolation assertions.
type indexCacheReadRange struct {
	offset int64
	length int64
}

// indexCacheTrackingReader records which cache ranges a decoder actually consumes.
type indexCacheTrackingReader struct {
	data  []byte
	reads []indexCacheReadRange
}

// ReadAt records the requested range before delegating to an immutable byte reader.
func (reader *indexCacheTrackingReader) ReadAt(buffer []byte, offset int64) (int, error) {
	reader.reads = append(reader.reads, indexCacheReadRange{offset: offset, length: int64(len(buffer))})
	return bytes.NewReader(reader.data).ReadAt(buffer, offset)
}

// overlaps reports whether any observed read touched the requested half-open section.
func (reader *indexCacheTrackingReader) overlaps(offset int64, length int64) bool {
	end := offset + length
	for _, observed := range reader.reads {
		observedEnd := observed.offset + observed.length
		if observed.offset < end && offset < observedEnd {
			return true
		}
	}
	return false
}

// TestIndexCacheRecordRoundTripPreservesManifestContract verifies canonical JSON, exact numbers, and private projection evidence survive persistence.
func TestIndexCacheRecordRoundTripPreservesManifestContract(t *testing.T) {
	manifest := Manifest{
		Version: ManifestVersion,
		Operations: []Operation{{
			ID:         "get-cache",
			Method:     "get",
			Path:       "/cache",
			Middleware: []string{"auth.Require"},
			Inputs:     InputShape{Body: &BodyShape{Schema: map[string]any{}}},
			Outputs:    OutputShape{Responses: []ResponseShape{{StatusCode: 204, Schema: nil}}},
			middlewareProvenance: []middlewareProvenance{{
				Expression: "auth.Require",
				File:       "app/routes.go",
				Function:   "ProvideRoutes",
			}},
		}},
		Schemas: []Schema{{
			Name: "Payload",
			Definition: map[string]any{
				"":         map[string]any{},
				"required": []string{},
				"anyOf":    []any{map[string]any{"type": "integer", "minimum": int64(0)}},
				"enum":     []any{json.Number("9007199254740993"), float64(1.5), true, "value"},
				"signed":   int(-1),
				"unsigned": uint64(2),
			},
		}},
	}
	manifestData, err := encodeJSONArtifacts([]jsonArtifact{{path: "manifest", value: manifest}})
	if err != nil {
		t.Fatalf("encode manifest fixture: %v", err)
	}
	manifestShape, ok := captureIndexCacheManifestShape(manifest)
	if !ok {
		t.Fatal("capture manifest runtime shape")
	}
	record := indexCacheRecord{
		Version:      indexCacheFormatVersion,
		InputHash:    "input",
		ManifestData: manifestData[0].data,
		Manifest:     manifestShape,
		Artifacts:    []indexCacheArtifact{{Role: indexCacheManifestArtifact, Data: []byte("{}")}},
	}
	encoded, err := encodeIndexCacheRecord(record)
	if err != nil {
		t.Fatalf("encodeIndexCacheRecord failed: %v", err)
	}
	decoded, ok := decodeIndexCacheRecord(encoded)
	if !ok {
		t.Fatal("decodeIndexCacheRecord rejected a valid record")
	}
	reencoded, err := encodeIndexCacheRecord(decoded)
	if err != nil {
		t.Fatalf("re-encode decoded cache record: %v", err)
	}
	if !reflect.DeepEqual(reencoded, encoded) {
		t.Fatal("cache record encoding was not deterministic after a round trip")
	}
	restored, ok := restoreIndexCacheManifest(decoded)
	if !ok {
		t.Fatal("restoreIndexCacheManifest rejected a valid record")
	}
	restoredData, err := encodeJSONArtifacts([]jsonArtifact{{path: "manifest", value: restored}})
	if err != nil {
		t.Fatalf("encode restored manifest: %v", err)
	}
	if !bytes.Equal(restoredData[0].data, manifestData[0].data) {
		t.Fatal("cache round trip changed canonical manifest bytes")
	}
	if !reflect.DeepEqual(restored, manifest) {
		t.Fatal("cache round trip changed the manifest runtime contract")
	}
	options := OpenAPIOptions{
		SecuritySchemes: map[string]OpenAPISecurityScheme{"session": {Type: "apiKey", In: "cookie", Name: "session"}},
		MiddlewareSecurityRules: []OpenAPIMiddlewareSecurityRule{{
			Expression:   "auth.Require",
			SourceFile:   "app/routes.go",
			Function:     "ProvideRoutes",
			Requirements: []OpenAPISecurityRequirement{{"session": {}}},
		}},
	}
	freshDocument, err := ProjectOpenAPI(manifest, options)
	if err != nil {
		t.Fatalf("project fresh manifest: %v", err)
	}
	restoredDocument, err := ProjectOpenAPI(restored, options)
	if err != nil {
		t.Fatalf("project restored manifest: %v", err)
	}
	if !reflect.DeepEqual(restoredDocument, freshDocument) {
		t.Fatal("cache round trip changed source-scoped OpenAPI projection")
	}
}

// TestIndexCacheValueShapeRoundTrip verifies every generated schema runtime type survives canonical JSON persistence.
func TestIndexCacheValueShapeRoundTrip(t *testing.T) {
	value := map[string]any{
		"":                   int8(-8),
		"any_slice":          []any{int16(-16), uint16(16)},
		"any_slice_nil":      []any(nil),
		"float32":            float32(1.25),
		"float64":            float64(-2.5),
		"int":                int(-1),
		"int32":              int32(-32),
		"int64":              int64(-64),
		"map_nil":            map[string]any(nil),
		"map_slice":          []map[string]any{{"nested": uint8(8)}},
		"map_slice_nil":      []map[string]any(nil),
		"number":             json.Number("9007199254740993"),
		"string_slice":       []string{"alpha", "beta"},
		"string_slice_empty": []string{},
		"string_slice_nil":   []string(nil),
		"uint":               uint(1),
		"uint32":             uint32(32),
		"uint64":             uint64(64),
		"uintptr":            uintptr(128),
	}
	shapes, ok := captureIndexCacheValueShape(value)
	if !ok {
		t.Fatal("capture generated schema runtime shape")
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode generated schema fixture: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode generated schema fixture: %v", err)
	}
	restored, ok := restoreIndexCacheValueShape(decoded, shapes)
	if !ok {
		t.Fatal("restore generated schema runtime shape")
	}
	if !reflect.DeepEqual(restored, value) {
		t.Fatalf("schema runtime shape changed:\nrestored: %#v\nwant:     %#v", restored, value)
	}
}

// TestIndexCacheValueShapeRejectsUnsupportedAndMalformedState verifies unsafe runtime shapes become cache misses.
func TestIndexCacheValueShapeRejectsUnsupportedAndMalformedState(t *testing.T) {
	for name, value := range map[string]any{
		"complex":      complex64(1),
		"infinite":     math.Inf(1),
		"not a number": math.NaN(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := captureIndexCacheValueShape(value); ok {
				t.Fatal("unsupported schema runtime value was accepted")
			}
		})
	}

	tests := []struct {
		name  string
		value any
		shape indexCacheValueShape
	}{
		{name: "unknown kind", value: json.Number("1"), shape: indexCacheValueShape{Kind: "unknown"}},
		{name: "missing map key", value: map[string]any{}, shape: indexCacheValueShape{Path: []indexCachePathSegment{{Key: "missing"}}, Kind: indexCacheShapeInt}},
		{name: "map path on array", value: []any{}, shape: indexCacheValueShape{Path: []indexCachePathSegment{{Key: "value"}}, Kind: indexCacheShapeInt}},
		{name: "array path on map", value: map[string]any{}, shape: indexCacheValueShape{Path: []indexCachePathSegment{{Index: 0, Array: true}}, Kind: indexCacheShapeInt}},
		{name: "array path out of range", value: []any{}, shape: indexCacheValueShape{Path: []indexCachePathSegment{{Index: 0, Array: true}}, Kind: indexCacheShapeInt}},
		{name: "numeric type", value: "1", shape: indexCacheValueShape{Kind: indexCacheShapeInt}},
		{name: "numeric overflow", value: json.Number("128"), shape: indexCacheValueShape{Kind: indexCacheShapeInt8}},
		{name: "string slice item", value: []any{json.Number("1")}, shape: indexCacheValueShape{Kind: indexCacheShapeStringSlice}},
		{name: "map slice item", value: []any{"value"}, shape: indexCacheValueShape{Kind: indexCacheShapeMapSlice}},
		{name: "nil slice source", value: []any{}, shape: indexCacheValueShape{Kind: indexCacheShapeAnySliceNil}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := restoreIndexCacheValueShape(test.value, []indexCacheValueShape{test.shape}); ok {
				t.Fatal("malformed schema runtime shape was accepted")
			}
		})
	}
}

// TestRunPersistentCacheRemapsArtifactsAndRecoversCorruption verifies GoForj staging paths reuse exact analysis while invalid entries safely rebuild.
func TestRunPersistentCacheRemapsArtifactsAndRecoversCorruption(t *testing.T) {
	root, handlerPath := writeTypedSchemaFixture(t)
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	uncachedOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "uncached"))
	uncachedManifest, err := Run(context.Background(), uncachedOptions)
	if err != nil {
		t.Fatalf("run without persistent cache: %v", err)
	}
	firstOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "first"))
	firstManifest, err := RunCached(context.Background(), firstOptions, cachePath)
	if err != nil {
		t.Fatalf("warm persistent cache: %v", err)
	}
	if !reflect.DeepEqual(firstManifest, uncachedManifest) {
		t.Fatal("cache-enabled cold run changed the uncached manifest")
	}
	firstArtifacts := readIndexCacheIntegrationArtifacts(t, firstOptions)
	cacheData, ok := readIndexCacheData(cachePath)
	if !ok || len(cacheData) == 0 {
		t.Fatal("warm run did not produce a readable cache entry")
	}

	typedPackageLoads := 0
	countingLoader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		typedPackageLoads++
		return packages.Load(config, patterns...)
	}

	secondOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "second"))
	secondManifest, err := run(context.Background(), secondOptions, cachePath, countingLoader)
	if err != nil {
		t.Fatalf("reuse persistent cache with remapped artifacts: %v", err)
	}
	if typedPackageLoads != 0 {
		t.Fatalf("cache hit loaded typed packages %d times", typedPackageLoads)
	}
	if !reflect.DeepEqual(secondManifest, firstManifest) {
		for index := range firstManifest.Operations {
			first := firstManifest.Operations[index]
			second := secondManifest.Operations[index]
			t.Logf("operation %d slice state: middleware=%t/%t path=%t/%t query=%t/%t headers=%t/%t cookies=%t/%t responses=%t/%t provenance=%t/%t", index, first.Middleware == nil, second.Middleware == nil, first.Inputs.PathParams == nil, second.Inputs.PathParams == nil, first.Inputs.QueryParams == nil, second.Inputs.QueryParams == nil, first.Inputs.Headers == nil, second.Inputs.Headers == nil, first.Inputs.Cookies == nil, second.Inputs.Cookies == nil, first.Outputs.Responses == nil, second.Outputs.Responses == nil, first.middlewareProvenance == nil, second.middlewareProvenance == nil)
		}
		t.Fatal("cache hit changed the returned manifest")
	}
	secondArtifacts := readIndexCacheIntegrationArtifacts(t, secondOptions)
	for role, firstData := range firstArtifacts {
		if !bytes.Equal(secondArtifacts[role], firstData) {
			t.Fatalf("cache hit changed %s artifact bytes", role)
		}
	}

	if err := os.WriteFile(cachePath, []byte("corrupt cache"), 0o644); err != nil {
		t.Fatalf("corrupt persistent cache fixture: %v", err)
	}
	thirdOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "third"))
	thirdManifest, err := run(context.Background(), thirdOptions, cachePath, countingLoader)
	if err != nil {
		t.Fatalf("rebuild corrupt persistent cache: %v", err)
	}
	if typedPackageLoads != 1 {
		t.Fatalf("corrupt cache loaded typed packages %d times, want 1", typedPackageLoads)
	}
	if !reflect.DeepEqual(thirdManifest, firstManifest) {
		t.Fatal("corrupt-cache rebuild changed the returned manifest")
	}
	rebuiltCache, ok := readIndexCacheData(cachePath)
	if !ok || bytes.Equal(rebuiltCache, []byte("corrupt cache")) {
		t.Fatal("corrupt cache was not replaced with a readable entry")
	}
	if _, valid := decodeIndexCacheRecord(rebuiltCache); !valid {
		t.Fatal("corrupt cache rebuild did not publish a valid record")
	}

	handlerData, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatalf("read typed handler fixture: %v", err)
	}
	if err := os.WriteFile(handlerPath, append(handlerData, []byte("\n// Cache invalidation must observe selected source edits.\n")...), 0o644); err != nil {
		t.Fatalf("change typed handler fixture: %v", err)
	}
	if _, err := run(context.Background(), thirdOptions, cachePath, countingLoader); err != nil {
		t.Fatalf("rebuild persistent cache after source change: %v", err)
	}
	if typedPackageLoads != 1 {
		t.Fatalf("incremental source change loaded typed packages %d additional times, want 0", typedPackageLoads-1)
	}
}

// TestRunCachedChangedControllerMatchesColdArtifacts verifies incremental checking preserves the complete public manifest and projection bytes.
func TestRunCachedChangedControllerMatchesColdArtifacts(t *testing.T) {
	root, handlerPath := writeTypedSchemaFixture(t)
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	warmOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "warm"))
	if _, err := RunCached(context.Background(), warmOptions, cachePath); err != nil {
		t.Fatalf("warm changed-controller cache: %v", err)
	}

	contents, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatalf("read changed-controller fixture: %v", err)
	}
	changed := bytes.Replace(contents, []byte(`json:"first"`), []byte(`json:"primary"`), 1)
	if bytes.Equal(changed, contents) {
		t.Fatal("changed-controller fixture did not contain the expected response field")
	}
	if err := os.WriteFile(handlerPath, changed, 0o644); err != nil {
		t.Fatalf("edit changed-controller fixture: %v", err)
	}

	packageLoads := 0
	countingLoader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		packageLoads++
		return packages.Load(config, patterns...)
	}
	incrementalOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "incremental"))
	incrementalManifest, err := run(context.Background(), incrementalOptions, cachePath, countingLoader)
	if err != nil {
		t.Fatalf("run changed controller incrementally: %v", err)
	}
	if packageLoads != 0 {
		t.Fatalf("changed controller loaded packages %d times, want 0", packageLoads)
	}

	coldOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "cold"))
	coldManifest, err := Run(context.Background(), coldOptions)
	if err != nil {
		t.Fatalf("run changed controller cold: %v", err)
	}
	if !reflect.DeepEqual(incrementalManifest, coldManifest) {
		t.Fatal("changed-controller incremental manifest differed from a cold index")
	}
	incrementalArtifacts := readIndexCacheIntegrationArtifacts(t, incrementalOptions)
	coldArtifacts := readIndexCacheIntegrationArtifacts(t, coldOptions)
	for role, coldData := range coldArtifacts {
		if !bytes.Equal(incrementalArtifacts[role], coldData) {
			t.Fatalf("changed-controller incremental %s artifact differed from a cold index", role)
		}
	}
}

// TestIndexCacheInputHashTracksRelevantChanges verifies cache identity follows source, configuration, module, and projection inputs.
func TestIndexCacheInputHashTracksRelevantChanges(t *testing.T) {
	t.Run("source add edit delete", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		baseline := requireIndexCacheInputHash(t, root, options)
		addedPath := filepath.Join(root, "internal", "added", "added.go")
		writeTypedFixtureFiles(t, root, map[string]string{"internal/added/added.go": "package added\nconst Value = 1\n"})
		added := requireIndexCacheInputHash(t, root, options)
		if added == baseline {
			t.Fatal("adding source did not invalidate cache identity")
		}
		if err := os.WriteFile(addedPath, []byte("package added\nconst Value = 2\n"), 0o644); err != nil {
			t.Fatalf("edit added source: %v", err)
		}
		if edited := requireIndexCacheInputHash(t, root, options); edited == added {
			t.Fatal("editing source did not invalidate cache identity")
		}
		if err := os.Remove(addedPath); err != nil {
			t.Fatalf("remove added source: %v", err)
		}
		if restored := requireIndexCacheInputHash(t, root, options); restored != baseline {
			t.Fatal("deleting added source did not restore the original cache identity")
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, root string, options *IndexOptions)
	}{
		{
			name: "go.mod",
			mutate: func(t *testing.T, root string, _ *IndexOptions) {
				appendIndexCacheTestFile(t, filepath.Join(root, "go.mod"), "\n// cache identity change\n")
			},
		},
		{
			name: "go.sum presence",
			mutate: func(t *testing.T, root string, _ *IndexOptions) {
				writeTypedFixtureFiles(t, root, map[string]string{"go.sum": "example.com/module v1.0.0 h1:fixture\n"})
			},
		},
		{
			name: ".env",
			mutate: func(t *testing.T, root string, _ *IndexOptions) {
				writeTypedFixtureFiles(t, root, map[string]string{".env": "APP_NAME=Changed\n"})
			},
		},
		{
			name: "composition",
			mutate: func(t *testing.T, root string, _ *IndexOptions) {
				appendIndexCacheTestFile(t, filepath.Join(root, "app", "routes.go"), "\nvar Changed = true\n")
			},
		},
		{
			name: "build tags",
			mutate: func(_ *testing.T, _ string, options *IndexOptions) {
				options.BuildTags = []string{"integration"}
			},
		},
		{
			name: "strictness",
			mutate: func(_ *testing.T, _ string, options *IndexOptions) {
				options.Strict = true
			},
		},
		{
			name: "OpenAPI options",
			mutate: func(_ *testing.T, _ string, options *IndexOptions) {
				options.OpenAPI.Info.Title = "Changed"
			},
		},
		{
			name: "artifact roles",
			mutate: func(_ *testing.T, _ string, options *IndexOptions) {
				options.DiagnosticsPath = ""
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, options := writeIndexCacheInputFixture(t)
			baseline := requireIndexCacheInputHash(t, root, options)
			test.mutate(t, root, &options)
			if changed := requireIndexCacheInputHash(t, root, options); changed == baseline {
				t.Fatalf("%s did not invalidate cache identity", test.name)
			}
		})
	}

	t.Run("staging paths", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		baseline := requireIndexCacheInputHash(t, root, options)
		options.OutPath = filepath.Join(t.TempDir(), "api.json")
		options.DiagnosticsPath = filepath.Join(t.TempDir(), "diagnostics.json")
		options.OpenAPIPath = filepath.Join(t.TempDir(), "openapi.json")
		if remapped := requireIndexCacheInputHash(t, root, options); remapped != baseline {
			t.Fatal("equivalent artifact roles changed cache identity when only staging paths changed")
		}
	})

	t.Run("Go build environment", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		baseline := requireIndexCacheInputHash(t, root, options)
		t.Setenv("GOFLAGS", "-tags=cache_environment")
		if changed := requireIndexCacheInputHash(t, root, options); changed == baseline {
			t.Fatal("effective Go build environment did not invalidate cache identity")
		}
	})

	t.Run("ignored source tree", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		baseline := requireIndexCacheInputHash(t, root, options)
		files := make(map[string]string, 64)
		for index := 0; index < 64; index++ {
			files[filepath.Join("frontend", "node_modules", "dependency", "pkg"+intToString(index), "source.go")] = "package dependency\n" + strings.Repeat("// ignored frontend source\n", 128)
		}
		writeTypedFixtureFiles(t, root, files)
		if changed := requireIndexCacheInputHash(t, root, options); changed != baseline {
			t.Fatal("ignored frontend dependency tree changed cache identity")
		}
	})
}

// TestIndexCacheInputHashesSeparateSourceAndDependencyChanges verifies ordinary edits preserve typed reuse while module selection invalidates it.
func TestIndexCacheInputHashesSeparateSourceAndDependencyChanges(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	baseline, cacheable, err := indexCacheInputHashes(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint dependency identity fixture: cacheable=%t err=%v", cacheable, err)
	}
	appendIndexCacheTestFile(t, filepath.Join(root, "main.go"), "\nconst SourceEdit = true\n")
	sourceEdit, cacheable, err := indexCacheInputHashes(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint source edit: cacheable=%t err=%v", cacheable, err)
	}
	if sourceEdit.exact == baseline.exact {
		t.Fatal("source edit did not invalidate the exact artifact identity")
	}
	if sourceEdit.dependency != baseline.dependency {
		t.Fatal("source edit invalidated the stable dependency identity")
	}

	appendIndexCacheTestFile(t, filepath.Join(root, "go.mod"), "\n// dependency graph selection changed\n")
	moduleEdit, cacheable, err := indexCacheInputHashes(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint module edit: cacheable=%t err=%v", cacheable, err)
	}
	if moduleEdit.dependency == sourceEdit.dependency {
		t.Fatal("module edit did not invalidate the dependency identity")
	}
}

// TestIndexCacheSemanticEpochsInvalidateAnalyzerChanges verifies executable analyzer semantics participate in both exact and incremental identities.
func TestIndexCacheSemanticEpochsInvalidateAnalyzerChanges(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	current := indexCacheSemanticEpochs{
		analyzer:        indexCacheAnalyzerEpoch,
		typedDependency: indexCacheTypedDependencyEpoch,
	}
	buildIdentity, buildIdentityAvailable := indexCacheAnalyzerBuildIdentity()
	if !buildIdentityAvailable {
		t.Fatal("executing analyzer build identity was unavailable")
	}
	var decodedBuildIdentity indexCacheBuildModuleIdentity
	if err := json.Unmarshal([]byte(buildIdentity), &decodedBuildIdentity); err != nil || decodedBuildIdentity.Path != indexCacheAnalyzerModulePath {
		t.Fatalf("executing analyzer build identity = %q, decoded=%#v err=%v", buildIdentity, decodedBuildIdentity, err)
	}
	baseline, cacheable, err := indexCacheInputHashesForIdentity(context.Background(), root, options, current, buildIdentity)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint current analyzer epochs: cacheable=%t err=%v", cacheable, err)
	}
	defaults, cacheable, err := indexCacheInputHashes(context.Background(), root, options)
	if err != nil || !cacheable || defaults.exact != baseline.exact || defaults.dependency != baseline.dependency {
		t.Fatalf("default analyzer identity diverged: cacheable=%t exact=%t dependency=%t err=%v", cacheable, defaults.exact == baseline.exact, defaults.dependency == baseline.dependency, err)
	}
	changedAnalyzer := current
	changedAnalyzer.analyzer += ".changed"
	analyzerResult, cacheable, err := indexCacheInputHashesForIdentity(context.Background(), root, options, changedAnalyzer, buildIdentity)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint changed analyzer epoch: cacheable=%t err=%v", cacheable, err)
	}
	if analyzerResult.exact == baseline.exact {
		t.Fatal("analyzer epoch change did not invalidate exact artifacts")
	}
	if analyzerResult.dependency == baseline.dependency {
		t.Fatal("analyzer epoch change did not invalidate incremental typed state")
	}

	changedDependencies := current
	changedDependencies.typedDependency += ".changed"
	dependencyResult, cacheable, err := indexCacheInputHashesForIdentity(context.Background(), root, options, changedDependencies, buildIdentity)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint changed dependency epoch: cacheable=%t err=%v", cacheable, err)
	}
	if dependencyResult.exact != baseline.exact {
		t.Fatal("dependency-only epoch change invalidated exact analyzer output")
	}
	if dependencyResult.dependency == baseline.dependency {
		t.Fatal("dependency-only epoch change reused incremental typed state")
	}

	buildResult, cacheable, err := indexCacheInputHashesForIdentity(context.Background(), root, options, current, buildIdentity+".changed")
	if err != nil || !cacheable {
		t.Fatalf("fingerprint changed analyzer build identity: cacheable=%t err=%v", cacheable, err)
	}
	if buildResult.exact == baseline.exact || buildResult.dependency == baseline.dependency {
		t.Fatal("executing Web module change did not invalidate exact and incremental identities")
	}
}

// TestIndexCacheInputHashTracksLocalReplacementSource verifies unpublished sibling modules cannot produce stale cache hits.
func TestIndexCacheInputHashTracksLocalReplacementSource(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	replacement := filepath.Join(parent, "contracts")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.25.0\n\nrequire example.com/contracts v0.0.0\nreplace example.com/contracts => ../contracts\n",
		"main.go": "package app\nimport _ \"example.com/contracts\"\n",
	})
	writeTypedFixtureFiles(t, replacement, map[string]string{
		"go.mod":       "module example.com/contracts\n\ngo 1.25.0\n",
		"contracts.go": "package contracts\ntype Request struct { Name string }\n",
	})
	options := IndexOptions{Root: root, OutPath: filepath.Join(parent, "artifacts", "api.json")}
	baseline := requireIndexCacheInputHash(t, root, options)
	writeTypedFixtureFiles(t, replacement, map[string]string{"contracts.go": "package contracts\ntype Request struct { Name string; Age int }\n"})
	if changed := requireIndexCacheInputHash(t, root, options); changed == baseline {
		t.Fatal("local replacement source change did not invalidate cache identity")
	}
}

// TestNewIndexCacheSessionDisablesUnsafeResolutionModes verifies opaque callbacks and package drivers retain the uncached correctness path.
func TestNewIndexCacheSessionDisablesUnsafeResolutionModes(t *testing.T) {
	t.Run("SkipDir callback", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		options.SkipDir = func(string, string) bool { return false }
		session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
		if err != nil || session != nil {
			t.Fatalf("SkipDir cache session = %#v, %v; want disabled without error", session, err)
		}
	})

	t.Run("custom package driver", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		t.Setenv("GOPACKAGESDRIVER", "/tmp/custom-driver")
		session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
		if err != nil || session != nil {
			t.Fatalf("custom-driver cache session = %#v, %v; want disabled without error", session, err)
		}
	})

	t.Run("standard driver sentinel", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		t.Setenv("GOPACKAGESDRIVER", "off")
		session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
		if err != nil || session == nil {
			t.Fatalf("standard-driver cache session = %#v, %v; want enabled", session, err)
		}
	})

	t.Run("vendor mode", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		if err := os.Mkdir(filepath.Join(root, "vendor"), 0o755); err != nil {
			t.Fatalf("create vendor fixture: %v", err)
		}
		session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
		if err != nil || session != nil {
			t.Fatalf("vendor cache session = %#v, %v; want disabled without error", session, err)
		}
	})
}

// TestValidateIndexCacheDestinationRejectsInputsAndArtifacts verifies an opt-in cache cannot overwrite application-owned files.
func TestValidateIndexCacheDestinationRejectsInputsAndArtifacts(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	compositionPath := filepath.Join(root, "app", "routes.go")
	directoryPath := filepath.Join(root, "cache-directory")
	if err := os.Mkdir(directoryPath, 0o755); err != nil {
		t.Fatalf("create cache directory fixture: %v", err)
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "go.mod", path: filepath.Join(root, "go.mod")},
		{name: "go.sum", path: filepath.Join(root, "go.sum")},
		{name: ".env", path: filepath.Join(root, ".env")},
		{name: "composition", path: compositionPath},
		{name: "source extension", path: filepath.Join(root, "cache.go")},
		{name: "artifact", path: options.OutPath},
		{name: "artifact lock", path: filepath.Join(root, ArtifactPublicationLockFilename)},
		{name: "directory", path: directoryPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newIndexCacheSession(context.Background(), root, options, test.path); err == nil {
				t.Fatalf("cache destination %q was accepted", test.path)
			}
		})
	}
	alias := filepath.Join(t.TempDir(), "artifact-alias")
	if err := os.Symlink(filepath.Dir(options.OutPath), alias); err == nil {
		cachePath := filepath.Join(alias, filepath.Base(options.OutPath))
		if _, err := newIndexCacheSession(context.Background(), root, options, cachePath); err == nil {
			t.Fatal("symlinked artifact path was accepted as a cache destination")
		}
	}
}

// TestValidateIndexCacheDestinationRejectsLocalModuleMetadata verifies sibling module inputs remain protected through replacements and path aliases.
func TestValidateIndexCacheDestinationRejectsLocalModuleMetadata(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	replacement := filepath.Join(parent, "contracts")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.25.0\n\nrequire example.com/contracts v0.0.0\nreplace example.com/contracts => ../contracts\n",
		"main.go": "package app\n",
	})
	writeTypedFixtureFiles(t, replacement, map[string]string{
		"go.mod":       "module example.com/contracts\n\ngo 1.25.0\n",
		"go.sum":       "example.com/dependency v1.0.0 h1:fixture\n",
		"contracts.go": "package contracts\n",
	})
	options := IndexOptions{Root: root, OutPath: filepath.Join(t.TempDir(), "api.json")}
	for _, name := range []string{"go.mod", "go.sum"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(replacement, name)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read replacement module input: %v", err)
			}
			if _, err := newIndexCacheSession(context.Background(), root, options, path); err == nil {
				t.Fatalf("replacement module input %q was accepted as a cache destination", path)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reread replacement module input: %v", err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("replacement module input %q changed during validation", path)
			}
		})
	}

	alias := filepath.Join(parent, "contracts-alias")
	if err := os.Symlink(replacement, alias); err != nil {
		t.Skipf("create replacement module path alias: %v", err)
	}
	cachePath := filepath.Join(alias, "go.mod")
	if _, err := newIndexCacheSession(context.Background(), root, options, cachePath); err == nil {
		t.Fatal("symlinked replacement module input was accepted as a cache destination")
	}
}

// TestValidateIndexCacheDestinationRejectsNonRegularFiles verifies cache reads cannot block on symlinks or special files.
func TestValidateIndexCacheDestinationRejectsNonRegularFiles(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	target := filepath.Join(t.TempDir(), "target.cache")
	if err := os.WriteFile(target, []byte("cache"), 0o644); err != nil {
		t.Fatalf("write cache symlink target: %v", err)
	}
	symlink := filepath.Join(t.TempDir(), "index.cache")
	if err := os.Symlink(target, symlink); err != nil {
		t.Skipf("create cache symlink fixture: %v", err)
	}
	if _, err := newIndexCacheSession(context.Background(), root, options, symlink); err == nil {
		t.Fatal("cache symlink was accepted as a regular destination")
	}
}

// TestReadIndexCacheDataRejectsOversizedAndNonRegularEntries verifies corrupt cache shapes remain bounded safe misses.
func TestReadIndexCacheDataRejectsOversizedAndNonRegularEntries(t *testing.T) {
	oversized := filepath.Join(t.TempDir(), "oversized.cache")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatalf("create oversized cache fixture: %v", err)
	}
	if err := file.Truncate(indexCacheMaximumSize + 1); err != nil {
		file.Close()
		t.Fatalf("truncate oversized cache fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close oversized cache fixture: %v", err)
	}
	if _, ok := readIndexCacheData(oversized); ok {
		t.Fatal("oversized cache entry was accepted")
	}
	if _, ok := readIndexCacheData(t.TempDir()); ok {
		t.Fatal("cache directory was accepted as a regular entry")
	}
}

// TestDecodeIndexCacheRecordRejectsInvalidFramingAndPayload verifies every malformed cache class becomes a safe miss.
func TestDecodeIndexCacheRecordRejectsInvalidFramingAndPayload(t *testing.T) {
	record, _ := writeIndexCacheRecordFixture(t)
	record.TypedState = typedSchemaIncrementalCodecFixture()
	encoded, err := encodeIndexCacheRecord(record)
	if err != nil {
		t.Fatalf("encode cache record fixture: %v", err)
	}
	envelope, ok := decodeIndexCacheEnvelope(encoded)
	if !ok {
		t.Fatal("decode cache envelope fixture")
	}
	payload := append([]byte(nil), envelope.record...)
	tests := []struct {
		name string
		data func() []byte
	}{
		{name: "truncated", data: func() []byte { return encoded[:indexCacheEnvelopeFixedHeaderBytes-1] }},
		{name: "magic", data: func() []byte {
			data := append([]byte(nil), encoded...)
			data[0] ^= 0xff
			return data
		}},
		{name: "empty input identity", data: func() []byte {
			data := append([]byte(nil), encoded...)
			binary.BigEndian.PutUint32(data[len(indexCacheMagic):len(indexCacheMagic)+4], 0)
			return data
		}},
		{name: "empty exact section", data: func() []byte {
			data := append([]byte(nil), encoded...)
			binary.BigEndian.PutUint32(data[len(indexCacheMagic)+4:len(indexCacheMagic)+8], 0)
			return data
		}},
		{name: "declared size mismatch", data: func() []byte {
			data := append([]byte(nil), encoded...)
			binary.BigEndian.PutUint32(data[len(indexCacheMagic)+8:len(indexCacheMagic)+12], uint32(envelope.typedLength+1))
			return data
		}},
		{name: "metadata checksum mismatch", data: func() []byte {
			data := append([]byte(nil), encoded...)
			data[indexCacheEnvelopeFixedHeaderBytes-1] ^= 1
			return data
		}},
		{name: "exact checksum mismatch", data: func() []byte {
			data := append([]byte(nil), encoded...)
			data[envelope.recordOffset] ^= 1
			return data
		}},
		{name: "typed checksum mismatch", data: func() []byte {
			data := append([]byte(nil), encoded...)
			data[envelope.typedOffset] ^= 1
			return data
		}},
		{name: "malformed JSON", data: func() []byte { return frameIndexCacheTestPayload([]byte("{")) }},
		{name: "unknown field", data: func() []byte {
			return frameIndexCacheTestPayload([]byte(`{"version":2,"unknown":true}`))
		}},
		{name: "noncanonical JSON", data: func() []byte { return frameIndexCacheTestPayload(append(payload, ' ')) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := decodeIndexCacheRecord(test.data()); ok {
				t.Fatal("invalid cache record was accepted")
			}
		})
	}
}

// TestDecodeIndexCacheReaderReadsOnlySelectedSection verifies fresh processes never consume an unrelated cache payload.
func TestDecodeIndexCacheReaderReadsOnlySelectedSection(t *testing.T) {
	record, _ := writeIndexCacheRecordFixture(t)
	wantTypedState := typedSchemaIncrementalCodecFixture()
	typedPayload, err := encodeTypedSchemaIncrementalState(wantTypedState)
	if err != nil {
		t.Fatalf("encode typed-state fixture: %v", err)
	}
	exactPayload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode exact-record fixture: %v", err)
	}
	encoded, err := frameIndexCacheEnvelope(record.InputHash, exactPayload, typedPayload)
	if err != nil {
		t.Fatalf("frame section-isolation fixture: %v", err)
	}

	exactReader := &indexCacheTrackingReader{data: encoded}
	exactRecord, exactLayout, exact, ok := decodeIndexCacheReader(exactReader, int64(len(encoded)), record.InputHash)
	if !ok || !exact || exactRecord.InputHash != record.InputHash {
		t.Fatalf("exact section read = valid %t, exact %t, input %q", ok, exact, exactRecord.InputHash)
	}
	if exactReader.overlaps(exactLayout.typedOffset, exactLayout.typedLength) {
		t.Fatal("exact cache hit read the incremental typed section")
	}

	typedReader := &indexCacheTrackingReader{data: encoded}
	changedRecord, changedLayout, exact, ok := decodeIndexCacheReader(typedReader, int64(len(encoded)), "changed-input")
	if !ok || exact || !reflect.DeepEqual(changedRecord.TypedState, wantTypedState) {
		t.Fatalf("changed-input section read = valid %t, exact %t, typed state %#v", ok, exact, changedRecord.TypedState)
	}
	if typedReader.overlaps(changedLayout.recordOffset, changedLayout.recordLength) {
		t.Fatal("changed-input cache read consumed the exact artifact section")
	}
}

// TestReadDecodedIndexCacheRecordRejectsExternalCorruption verifies each read validates current bytes from disk.
func TestReadDecodedIndexCacheRecordRejectsExternalCorruption(t *testing.T) {
	record, _ := writeIndexCacheRecordFixture(t)
	record.TypedState = typedSchemaIncrementalCodecFixture()
	encoded, err := encodeIndexCacheRecord(record)
	if err != nil {
		t.Fatalf("encode retained corruption fixture: %v", err)
	}
	layout, ok := decodeIndexCacheEnvelopeLayout(bytes.NewReader(encoded), int64(len(encoded)))
	if !ok {
		t.Fatal("decode retained corruption layout")
	}
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	if err := os.WriteFile(cachePath, encoded, 0o644); err != nil {
		t.Fatalf("write retained corruption fixture: %v", err)
	}
	if _, ok := readDecodedIndexCacheRecord(cachePath, record.InputHash); !ok {
		t.Fatal("prime decoded cache memory")
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[layout.recordOffset] ^= 1
	if err := os.WriteFile(cachePath, corrupt, 0o644); err != nil {
		t.Fatalf("corrupt retained cache record: %v", err)
	}
	if _, ok := readDecodedIndexCacheRecord(cachePath, record.InputHash); ok {
		t.Fatal("retained decoded state hid exact-section corruption")
	}
}

// TestDecodeIndexCacheReaderIsolatesSectionCorruption verifies corruption invalidates a section when that section is selected.
func TestDecodeIndexCacheReaderIsolatesSectionCorruption(t *testing.T) {
	record, _ := writeIndexCacheRecordFixture(t)
	record.TypedState = typedSchemaIncrementalCodecFixture()
	encoded, err := encodeIndexCacheRecord(record)
	if err != nil {
		t.Fatalf("encode corruption-isolation fixture: %v", err)
	}
	layout, ok := decodeIndexCacheEnvelopeLayout(bytes.NewReader(encoded), int64(len(encoded)))
	if !ok {
		t.Fatal("decode corruption-isolation layout")
	}

	exactCorrupt := append([]byte(nil), encoded...)
	exactCorrupt[layout.recordOffset] ^= 1
	if _, _, _, valid := decodeIndexCacheReader(bytes.NewReader(exactCorrupt), int64(len(exactCorrupt)), record.InputHash); valid {
		t.Fatal("exact hit accepted corrupt exact content")
	}
	if changed, _, exact, valid := decodeIndexCacheReader(bytes.NewReader(exactCorrupt), int64(len(exactCorrupt)), "changed-input"); !valid || exact || changed.TypedState == nil {
		t.Fatalf("exact corruption invalidated independent typed state: valid=%t exact=%t", valid, exact)
	}

	typedCorrupt := append([]byte(nil), encoded...)
	typedCorrupt[layout.typedOffset] ^= 1
	if exactRecord, _, exact, valid := decodeIndexCacheReader(bytes.NewReader(typedCorrupt), int64(len(typedCorrupt)), record.InputHash); !valid || !exact || exactRecord.InputHash != record.InputHash {
		t.Fatalf("typed corruption invalidated independent exact state: valid=%t exact=%t", valid, exact)
	}
	if _, _, _, valid := decodeIndexCacheReader(bytes.NewReader(typedCorrupt), int64(len(typedCorrupt)), "changed-input"); valid {
		t.Fatal("changed-input read accepted corrupt typed content")
	}
}

// TestReadDecodedIndexCacheRecordDefersTypedState verifies exact hits retain raw incremental state until a later source miss needs it.
func TestReadDecodedIndexCacheRecordDefersTypedState(t *testing.T) {
	record, _ := writeIndexCacheRecordFixture(t)
	wantTypedState := typedSchemaIncrementalCodecFixture()
	record.TypedState = wantTypedState
	encoded, err := encodeIndexCacheRecord(record)
	if err != nil {
		t.Fatalf("encode lazy typed-state fixture: %v", err)
	}
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	if err := os.WriteFile(cachePath, encoded, 0o644); err != nil {
		t.Fatalf("write lazy typed-state fixture: %v", err)
	}

	exact, ok := readDecodedIndexCacheRecord(cachePath, record.InputHash)
	if !ok || exact.InputHash != record.InputHash {
		t.Fatalf("read exact cache record: valid=%t input=%q", ok, exact.InputHash)
	}
	if exact.TypedState != nil {
		t.Fatal("exact cache hit eagerly decoded incremental type state")
	}

	changed, ok := readDecodedIndexCacheRecord(cachePath, "changed-input")
	if !ok {
		t.Fatal("changed cache input could not decode retained incremental type state")
	}
	if !reflect.DeepEqual(changed.TypedState, wantTypedState) {
		t.Fatalf("lazy incremental type state differed:\ngot:  %#v\nwant: %#v", changed.TypedState, wantTypedState)
	}
}

// TestReadDecodedIndexCacheRecordDefersTypedCorruption verifies malformed incremental state is isolated from exact artifacts and rejected when needed.
func TestReadDecodedIndexCacheRecordDefersTypedCorruption(t *testing.T) {
	record, _ := writeIndexCacheRecordFixture(t)
	exactPayload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode exact cache fixture: %v", err)
	}
	encoded, err := frameIndexCacheEnvelope(record.InputHash, exactPayload, []byte("malformed typed state"))
	if err != nil {
		t.Fatalf("frame corrupt typed-state fixture: %v", err)
	}
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	if err := os.WriteFile(cachePath, encoded, 0o644); err != nil {
		t.Fatalf("write corrupt typed-state fixture: %v", err)
	}

	exact, ok := readDecodedIndexCacheRecord(cachePath, record.InputHash)
	if !ok || exact.InputHash != record.InputHash || exact.TypedState != nil {
		t.Fatalf("corrupt incremental section invalidated exact artifacts: valid=%t record=%#v", ok, exact)
	}
	if _, ok := readDecodedIndexCacheRecord(cachePath, "changed-input"); ok {
		t.Fatal("changed cache input accepted malformed incremental type state")
	}
	if _, ok := decodeIndexCacheRecord(encoded); ok {
		t.Fatal("complete cache decoder accepted malformed incremental type state")
	}
}

// TestCachedIndexArtifactsRejectsRoleAndContentMismatch verifies exact artifact sets cannot be confused across cache records.
func TestCachedIndexArtifactsRejectsRoleAndContentMismatch(t *testing.T) {
	record, manifest := writeIndexCacheRecordFixture(t)
	options := IndexOptions{OutPath: "manifest.json", DiagnosticsPath: "diagnostics.json", OpenAPIPath: "openapi.json"}
	if artifacts, ok := cachedIndexArtifacts(record, manifest, options); !ok || len(artifacts) != 3 {
		t.Fatalf("valid cached artifacts were rejected: count=%d valid=%t", len(artifacts), ok)
	}
	tests := []struct {
		name   string
		mutate func(*indexCacheRecord)
	}{
		{name: "count", mutate: func(record *indexCacheRecord) { record.Artifacts = record.Artifacts[:2] }},
		{name: "empty role", mutate: func(record *indexCacheRecord) { record.Artifacts[0].Role = "" }},
		{name: "invalid JSON", mutate: func(record *indexCacheRecord) { record.Artifacts[0].Data = []byte("{") }},
		{name: "duplicate role", mutate: func(record *indexCacheRecord) { record.Artifacts[1].Role = record.Artifacts[0].Role }},
		{name: "unknown role", mutate: func(record *indexCacheRecord) { record.Artifacts[2].Role = "unknown" }},
		{name: "manifest bytes", mutate: func(record *indexCacheRecord) { record.Artifacts[0].Data = []byte("{}") }},
		{name: "diagnostic bytes", mutate: func(record *indexCacheRecord) { record.Artifacts[1].Data = []byte("[{}]") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, candidateManifest := writeIndexCacheRecordFixture(t)
			test.mutate(&candidate)
			if _, ok := cachedIndexArtifacts(candidate, candidateManifest, options); ok {
				t.Fatal("mismatched cached artifact set was accepted")
			}
		})
	}
}

// TestCacheRecordArtifactsRejectsInvalidPublicationSets verifies cache roles retain the same order and validity as generated artifacts.
func TestCacheRecordArtifactsRejectsInvalidPublicationSets(t *testing.T) {
	options := IndexOptions{OutPath: "manifest.json"}
	valid := []encodedJSONArtifact{{path: options.OutPath, data: []byte("{}")}}
	if artifacts, ok := cacheRecordArtifacts(options, valid); !ok || len(artifacts) != 1 {
		t.Fatal("valid publication set was rejected")
	}
	if _, ok := cacheRecordArtifacts(options, nil); ok {
		t.Fatal("publication count mismatch was accepted")
	}
	if _, ok := cacheRecordArtifacts(options, []encodedJSONArtifact{{path: "other.json", data: []byte("{}")}}); ok {
		t.Fatal("publication path mismatch was accepted")
	}
	if _, ok := cacheRecordArtifacts(options, []encodedJSONArtifact{{path: options.OutPath, data: []byte("{")}}); ok {
		t.Fatal("invalid publication JSON was accepted")
	}
}

// TestNewIndexCacheSessionFallsBackForUnavailableModuleState verifies unresolved module modes never make a stale cache authoritative.
func TestNewIndexCacheSessionFallsBackForUnavailableModuleState(t *testing.T) {
	t.Run("missing go.mod", func(t *testing.T) {
		root := t.TempDir()
		options := IndexOptions{Root: root}
		if session, err := newIndexCacheSession(context.Background(), root, options, filepath.Join(t.TempDir(), "index.cache")); err != nil || session != nil {
			t.Fatalf("missing-module cache session = %#v, %v; want disabled", session, err)
		}
	})

	t.Run("invalid go.mod", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		writeTypedFixtureFiles(t, root, map[string]string{"go.mod": "not a module"})
		if session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options)); err != nil || session != nil {
			t.Fatalf("invalid-module cache session = %#v, %v; want disabled", session, err)
		}
	})

	t.Run("missing local replacement", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		writeTypedFixtureFiles(t, root, map[string]string{"go.mod": "module example.com/cacheinput\n\ngo 1.25.0\n\nreplace example.com/missing => ./missing\n"})
		if session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options)); err != nil || session != nil {
			t.Fatalf("missing-replacement cache session = %#v, %v; want disabled", session, err)
		}
	})

	t.Run("workspace", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		workspacePath := filepath.Join(root, "go.work")
		writeTypedFixtureFiles(t, root, map[string]string{"go.work": "go 1.25.0\n\nuse .\n"})
		t.Setenv("GOWORK", workspacePath)
		if session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options)); err != nil || session != nil {
			t.Fatalf("workspace cache session = %#v, %v; want disabled", session, err)
		}
	})

	t.Run("go command unavailable", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		t.Setenv("PATH", "")
		if session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options)); err != nil || session != nil {
			t.Fatalf("unavailable-go cache session = %#v, %v; want disabled", session, err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		root, options := writeIndexCacheInputFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if session, err := newIndexCacheSession(ctx, root, options, indexCachePathForTest(options)); err == nil || session != nil {
			t.Fatalf("canceled cache session = %#v, %v; want cancellation error", session, err)
		}
	})
}

// indexCacheIntegrationOptions requests every artifact role while moving their physical staging directory between runs.
func indexCacheIntegrationOptions(root string, artifactDirectory string) IndexOptions {
	return IndexOptions{
		Root:            root,
		OutPath:         filepath.Join(artifactDirectory, "api_index.json"),
		DiagnosticsPath: filepath.Join(artifactDirectory, "api_index.diagnostics.json"),
		OpenAPIPath:     filepath.Join(artifactDirectory, "openapi.json"),
	}
}

// readIndexCacheIntegrationArtifacts returns exact role-bound bytes for staging-directory comparisons.
func readIndexCacheIntegrationArtifacts(t *testing.T, options IndexOptions) map[string][]byte {
	t.Helper()
	artifacts := map[string][]byte{}
	for role, path := range map[string]string{
		indexCacheManifestArtifact:    options.OutPath,
		indexCacheDiagnosticsArtifact: options.DiagnosticsPath,
		indexCacheOpenAPIArtifact:     options.OpenAPIPath,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s integration artifact: %v", role, err)
		}
		artifacts[role] = data
	}
	return artifacts
}

// writeIndexCacheInputFixture creates the smallest module that exercises every content-fingerprint input class.
func writeIndexCacheInputFixture(t *testing.T) (string, IndexOptions) {
	t.Helper()
	root := t.TempDir()
	artifactDirectory := t.TempDir()
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":        "module example.com/cacheinput\n\ngo 1.25.0\n",
		".env":          "APP_NAME=Cache Input\n",
		"main.go":       "package cacheinput\nconst Value = 1\n",
		"app/routes.go": "package app\nfunc Routes() {}\n",
	})
	return root, IndexOptions{
		Root:                 root,
		RouteCompositionPath: "app/routes.go",
		OutPath:              filepath.Join(artifactDirectory, "api.json"),
		DiagnosticsPath:      filepath.Join(artifactDirectory, "diagnostics.json"),
		OpenAPIPath:          filepath.Join(artifactDirectory, "openapi.json"),
	}
}

// requireIndexCacheInputHash returns one enabled cache identity or fails with its construction error.
func requireIndexCacheInputHash(t *testing.T, root string, options IndexOptions) string {
	t.Helper()
	session, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
	if err != nil {
		t.Fatalf("create cache session: %v", err)
	}
	if session == nil || session.inputHash == "" {
		t.Fatal("cache session was unexpectedly disabled")
	}
	return session.inputHash
}

// indexCachePathForTest gives each fixture a stable private cache path without changing the public options contract.
func indexCachePathForTest(options IndexOptions) string {
	return options.OutPath + ".cache"
}

// appendIndexCacheTestFile preserves existing fixture bytes while making one explicit content change.
func appendIndexCacheTestFile(t *testing.T, path string, suffix string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache input fixture %s: %v", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(data, suffix...), 0o644); err != nil {
		t.Fatalf("write cache input fixture %s: %v", filepath.Base(path), err)
	}
}

// writeIndexCacheRecordFixture creates a complete three-role record and its restored manifest.
func writeIndexCacheRecordFixture(t *testing.T) (indexCacheRecord, Manifest) {
	t.Helper()
	manifest := Manifest{Version: ManifestVersion, Operations: []Operation{}, Schemas: []Schema{}, Diagnostics: []Diagnostic{}}
	manifestArtifacts, err := encodeJSONArtifacts([]jsonArtifact{{path: "manifest", value: manifest}})
	if err != nil {
		t.Fatalf("encode cache manifest fixture: %v", err)
	}
	manifestShape, ok := captureIndexCacheManifestShape(manifest)
	if !ok {
		t.Fatal("capture cache manifest fixture shape")
	}
	record := indexCacheRecord{
		Version:      indexCacheFormatVersion,
		InputHash:    "input",
		ManifestData: manifestArtifacts[0].data,
		Manifest:     manifestShape,
		Artifacts: []indexCacheArtifact{
			{Role: indexCacheManifestArtifact, Data: manifestArtifacts[0].data},
			{Role: indexCacheDiagnosticsArtifact, Data: []byte("[]\n")},
			{Role: indexCacheOpenAPIArtifact, Data: []byte("{}\n")},
		},
	}
	restored, ok := restoreIndexCacheManifest(record)
	if !ok {
		t.Fatal("restore cache record fixture")
	}
	return record, restored
}

// frameIndexCacheTestPayload creates valid framing around an intentionally controlled payload.
func frameIndexCacheTestPayload(payload []byte) []byte {
	framed, _ := frameIndexCacheEnvelope("input", payload, nil)
	return framed
}
