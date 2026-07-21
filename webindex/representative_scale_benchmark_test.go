package webindex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
)

// representative-scale fixture dimensions keep this benchmark comparable to the application that exposed the indexing regression.
const (
	representativeScaleHandlerPackages = 14
	representativeScaleOperations      = 161
	representativeScaleSchemas         = 98
	representativeScaleTypedFiles      = 168
	representativeScaleTotalFiles      = 326
	representativeScaleTypedLines      = 40_320
	representativeScaleTotalLines      = 82_032
	representativeScaleTypedFileLines  = 240
	representativeScaleServiceLines    = 264
	representativeScaleCacheHitSamples = 7
	representativeScaleChangedSamples  = 7
	representativeScaleCacheHitBudget  = 100 * time.Millisecond
	representativeScaleChangedBudget   = 100 * time.Millisecond
)

// representativeScaleFixture records the mutable selected source and all three artifact destinations.
type representativeScaleFixture struct {
	root            string
	mutableContract string
	indexPath       string
	diagnosticsPath string
	openAPIPath     string
	cachePath       string
}

// representativeScaleSourceShape describes the physical source workload independently of index output.
type representativeScaleSourceShape struct {
	typedPackages int
	typedFiles    int
	totalFiles    int
	typedLines    int
	totalLines    int
}

// representativeScaleArtifacts holds decoded outputs so changed and cached runs can verify every requested artifact.
type representativeScaleArtifacts struct {
	manifest        Manifest
	diagnostics     []Diagnostic
	openAPI         OpenAPIDocument
	indexBytes      []byte
	diagnosticBytes []byte
	openAPIBytes    []byte
}

// TestRunRepresentativeScaleChangedFixture exercises two full indexes around a selected typed-contract change.
func TestRunRepresentativeScaleChangedFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skip representative-scale indexing fixture in short mode")
	}
	fixture := writeRepresentativeScaleFixture(t)
	assertRepresentativeScaleSourceShape(t, measureRepresentativeScaleSources(t, fixture.root))

	firstManifest, err := Run(context.Background(), fixture.options())
	if err != nil {
		t.Fatalf("index initial representative-scale fixture: %v", err)
	}
	assertRepresentativeScaleManifest(t, firstManifest)
	firstArtifacts := readRepresentativeScaleArtifacts(t, fixture)

	writeRepresentativeScaleContract(t, fixture.mutableContract, 0, 1)
	secondManifest, err := Run(context.Background(), fixture.options())
	if err != nil {
		t.Fatalf("index changed representative-scale fixture: %v", err)
	}
	assertRepresentativeScaleManifest(t, secondManifest)
	secondArtifacts := readRepresentativeScaleArtifacts(t, fixture)
	if bytes.Equal(firstArtifacts.indexBytes, secondArtifacts.indexBytes) {
		t.Fatal("selected contract change did not update the API index artifact")
	}
	if bytes.Equal(firstArtifacts.openAPIBytes, secondArtifacts.openAPIBytes) {
		t.Fatal("selected contract change did not update the OpenAPI artifact")
	}
}

// TestRunRepresentativeScalePersistentCacheHitBudget protects the unchanged development path without hiding package loads behind a synthetic stub.
func TestRunRepresentativeScalePersistentCacheHitBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skip representative-scale cache performance budget in short mode")
	}
	if os.Getenv("CI") != "" {
		t.Skip("skip hardware-dependent cache performance budget in CI")
	}
	if representativeScaleRaceEnabled() {
		t.Skip("skip wall-clock cache performance budget under the race detector")
	}
	fixture := writeRepresentativeScaleFixture(t)
	assertRepresentativeScaleSourceShape(t, measureRepresentativeScaleSources(t, fixture.root))
	options := fixture.cachedOptions()
	warmManifest, err := RunCached(context.Background(), options, fixture.cachePath)
	if err != nil {
		t.Fatalf("warm representative-scale persistent cache: %v", err)
	}
	assertRepresentativeScaleManifest(t, warmManifest)
	warmArtifacts := readRepresentativeScaleArtifacts(t, fixture)
	warmCache := readRepresentativeScaleFile(t, fixture.cachePath)
	if len(warmCache) == 0 {
		t.Fatal("warm run did not publish a persistent cache entry")
	}
	cacheSession, err := newIndexCacheSession(context.Background(), fixture.root, options, fixture.cachePath)
	if err != nil {
		t.Fatalf("open warm representative-scale persistent cache: %v", err)
	}
	if _, _, hit := cacheSession.load(context.Background()); !hit {
		record, decoded := decodeIndexCacheRecord(warmCache)
		manifest, manifestValid := restoreIndexCacheManifest(record)
		_, artifactsValid := cachedIndexArtifacts(record, manifest, options)
		t.Fatalf("warm run did not produce an immediately reusable persistent cache entry: decoded=%t input_match=%t manifest_valid=%t artifacts_valid=%t", decoded, record.InputHash == cacheSession.inputHash, manifestValid, artifactsValid)
	}

	// A deliberately old timestamp distinguishes a rewritten cache even on filesystems with coarse timestamp precision.
	cacheSentinel := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(fixture.cachePath, cacheSentinel, cacheSentinel); err != nil {
		t.Fatalf("set persistent cache sentinel timestamp: %v", err)
	}
	warmCacheInfo, err := os.Stat(fixture.cachePath)
	if err != nil {
		t.Fatalf("stat persistent cache after warm run: %v", err)
	}

	durations := make([]time.Duration, 0, representativeScaleCacheHitSamples)
	for sample := 0; sample < representativeScaleCacheHitSamples; sample++ {
		clearRepresentativeScaleDecodedCache()
		started := time.Now()
		manifest, runErr := RunCached(context.Background(), options, fixture.cachePath)
		durations = append(durations, time.Since(started))
		if runErr != nil {
			t.Fatalf("run unchanged representative-scale cache sample %d: %v", sample, runErr)
		}
		assertRepresentativeScaleManifest(t, manifest)
		assertRepresentativeScaleArtifactsEqual(t, readRepresentativeScaleArtifacts(t, fixture), warmArtifacts)
		if cache := readRepresentativeScaleFile(t, fixture.cachePath); !bytes.Equal(cache, warmCache) {
			t.Fatalf("unchanged cache sample %d rewrote persistent cache bytes", sample)
		}
		cacheInfo, statErr := os.Stat(fixture.cachePath)
		if statErr != nil {
			t.Fatalf("stat persistent cache after sample %d: %v", sample, statErr)
		}
		if !cacheInfo.ModTime().Equal(warmCacheInfo.ModTime()) {
			t.Fatalf("unchanged cache sample %d rewrote persistent cache timestamp: got %s, want %s", sample, cacheInfo.ModTime(), warmCacheInfo.ModTime())
		}
	}
	slices.Sort(durations)
	median := durations[len(durations)/2]
	t.Logf("representative-scale persistent cache median: %s across %d samples", median, len(durations))
	if median >= representativeScaleCacheHitBudget {
		t.Fatalf("representative-scale persistent cache median was %s, budget is %s across %d samples: %v", median, representativeScaleCacheHitBudget, len(durations), durations)
	}
}

// TestRunRepresentativeScaleChangedControllerBudget protects the controller-edit loop through the public cached API.
func TestRunRepresentativeScaleChangedControllerBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skip representative-scale controller-edit performance budget in short mode")
	}
	if os.Getenv("CI") != "" {
		t.Skip("skip hardware-dependent controller-edit performance budget in CI")
	}
	if representativeScaleRaceEnabled() {
		t.Skip("skip wall-clock controller-edit performance budget under the race detector")
	}
	fixture := writeRepresentativeScaleFixture(t)
	assertRepresentativeScaleSourceShape(t, measureRepresentativeScaleSources(t, fixture.root))
	options := fixture.cachedOptions()
	manifest, err := RunCached(context.Background(), options, fixture.cachePath)
	if err != nil {
		t.Fatalf("warm representative-scale controller-edit cache: %v", err)
	}
	assertRepresentativeScaleManifest(t, manifest)
	previous := readRepresentativeScaleArtifacts(t, fixture)

	durations := make([]time.Duration, 0, representativeScaleChangedSamples)
	for sample := 0; sample < representativeScaleChangedSamples; sample++ {
		writeRepresentativeScaleContract(t, fixture.mutableContract, 0, sample+1)
		clearRepresentativeScaleDecodedCache()
		started := time.Now()
		manifest, runErr := RunCached(context.Background(), options, fixture.cachePath)
		durations = append(durations, time.Since(started))
		if runErr != nil {
			t.Fatalf("run representative-scale controller-edit sample %d: %v", sample, runErr)
		}
		assertRepresentativeScaleManifest(t, manifest)
		current := readRepresentativeScaleArtifacts(t, fixture)
		if bytes.Equal(current.indexBytes, previous.indexBytes) || bytes.Equal(current.openAPIBytes, previous.openAPIBytes) {
			t.Fatalf("controller-edit sample %d did not update every contract artifact", sample)
		}
		previous = current
	}
	slices.Sort(durations)
	median := durations[len(durations)/2]
	t.Logf("representative-scale controller-edit median: %s across %d samples", median, len(durations))
	if median >= representativeScaleChangedBudget {
		t.Fatalf("representative-scale controller-edit median was %s, budget is %s across %d samples: %v", median, representativeScaleChangedBudget, len(durations), durations)
	}
}

// clearRepresentativeScaleDecodedCache makes the hard edit budget include persisted-record decoding across build process boundaries.
func clearRepresentativeScaleDecodedCache() {
	decodedIndexCaches.Lock()
	decodedIndexCaches.entries = nil
	decodedIndexCaches.Unlock()
}

// BenchmarkRunRepresentativeScaleChangedRepo measures the cached development loop after one selected contract changes.
func BenchmarkRunRepresentativeScaleChangedRepo(b *testing.B) {
	fixture := writeRepresentativeScaleFixture(b)
	assertRepresentativeScaleSourceShape(b, measureRepresentativeScaleSources(b, fixture.root))
	manifest, err := RunCached(context.Background(), fixture.options(), fixture.cachePath)
	if err != nil {
		b.Fatalf("warm representative-scale fixture: %v", err)
	}
	assertRepresentativeScaleManifest(b, manifest)
	readRepresentativeScaleArtifacts(b, fixture)

	b.ReportAllocs()
	b.ResetTimer()
	b.StopTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		writeRepresentativeScaleContract(b, fixture.mutableContract, 0, iteration+1)
		clearRepresentativeScaleDecodedCache()
		b.StartTimer()
		manifest, runErr := RunCached(context.Background(), fixture.options(), fixture.cachePath)
		b.StopTimer()
		if runErr != nil {
			b.Fatalf("index changed representative-scale fixture: %v", runErr)
		}
		assertRepresentativeScaleManifest(b, manifest)
		readRepresentativeScaleArtifacts(b, fixture)
	}
	b.ReportMetric(representativeScaleHandlerPackages, "handler_pkgs")
	b.ReportMetric(representativeScaleOperations, "operations")
	b.ReportMetric(representativeScaleSchemas, "schemas")
	b.ReportMetric(representativeScaleTotalFiles, "go_files")
	b.ReportMetric(representativeScaleTotalLines, "go_lines")
}

// BenchmarkRunRepresentativeScalePersistentCacheHit measures a fresh-process-equivalent exact cache hit.
func BenchmarkRunRepresentativeScalePersistentCacheHit(b *testing.B) {
	fixture := writeRepresentativeScaleFixture(b)
	assertRepresentativeScaleSourceShape(b, measureRepresentativeScaleSources(b, fixture.root))
	manifest, err := RunCached(context.Background(), fixture.cachedOptions(), fixture.cachePath)
	if err != nil {
		b.Fatalf("warm representative-scale fixture: %v", err)
	}
	assertRepresentativeScaleManifest(b, manifest)
	readRepresentativeScaleArtifacts(b, fixture)
	cacheData := readRepresentativeScaleFile(b, fixture.cachePath)
	layout, ok := decodeIndexCacheEnvelopeLayout(bytes.NewReader(cacheData), int64(len(cacheData)))
	if !ok {
		b.Fatal("decode representative-scale cache layout")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		clearRepresentativeScaleDecodedCache()
		manifest, runErr := RunCached(context.Background(), fixture.cachedOptions(), fixture.cachePath)
		if runErr != nil {
			b.Fatalf("index unchanged representative-scale fixture: %v", runErr)
		}
		assertRepresentativeScaleManifest(b, manifest)
	}
	b.ReportMetric(representativeScaleHandlerPackages, "handler_pkgs")
	b.ReportMetric(representativeScaleOperations, "operations")
	b.ReportMetric(representativeScaleSchemas, "schemas")
	b.ReportMetric(representativeScaleTotalFiles, "go_files")
	b.ReportMetric(representativeScaleTotalLines, "go_lines")
	b.ReportMetric(float64(layout.recordLength), "exact_B")
	b.ReportMetric(float64(layout.typedLength), "typed_B")
}

// options requests the same three artifacts produced by application indexing.
func (fixture representativeScaleFixture) options() IndexOptions {
	return IndexOptions{
		Root:            fixture.root,
		OutPath:         fixture.indexPath,
		DiagnosticsPath: fixture.diagnosticsPath,
		OpenAPIPath:     fixture.openAPIPath,
		Strict:          true,
	}
}

// cachedOptions keeps the cached and uncached runs on identical indexing options.
func (fixture representativeScaleFixture) cachedOptions() IndexOptions {
	return fixture.options()
}

// writeRepresentativeScaleFixture creates typed handler and inactive service source in realistic proportions.
func writeRepresentativeScaleFixture(t testing.TB) representativeScaleFixture {
	t.Helper()
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeRepresentativeScaleFile(t, filepath.Join(root, "go.mod"), "module example.com/representativescale\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => "+filepath.ToSlash(webRoot)+"\n")
	writeRepresentativeScaleFile(t, filepath.Join(root, ".env"), "APP_NAME=Representative Scale Fixture\n")

	for packageIndex := 0; packageIndex < representativeScaleHandlerPackages; packageIndex++ {
		directory := filepath.Join(root, "internal", "handlers", fmt.Sprintf("h%02d", packageIndex))
		contractPath := filepath.Join(directory, "contracts.go")
		writeRepresentativeScaleContract(t, contractPath, packageIndex, 0)
		operationCount := 11
		if packageIndex < 7 {
			operationCount = 12
		}
		for fileIndex := 0; fileIndex < 11; fileIndex++ {
			source := representativeScaleHandlerSource(packageIndex, fileIndex, operationCount)
			source = padRepresentativeScaleSource(source, representativeScaleTypedFileLines, fmt.Sprintf("h%02df%02d", packageIndex, fileIndex))
			writeRepresentativeScaleFile(t, filepath.Join(directory, fmt.Sprintf("handler_%02d.go", fileIndex)), source)
		}
	}

	remainingFiles := representativeScaleTotalFiles - representativeScaleTypedFiles
	for packageIndex := 0; packageIndex < representativeScaleHandlerPackages; packageIndex++ {
		packageFiles := remainingFiles / representativeScaleHandlerPackages
		if packageIndex < remainingFiles%representativeScaleHandlerPackages {
			packageFiles++
		}
		directory := filepath.Join(root, "internal", "services", fmt.Sprintf("s%02d", packageIndex))
		for fileIndex := 0; fileIndex < packageFiles; fileIndex++ {
			source := fmt.Sprintf("package service%02d\n", packageIndex)
			source = padRepresentativeScaleSource(source, representativeScaleServiceLines, fmt.Sprintf("s%02df%02d", packageIndex, fileIndex))
			writeRepresentativeScaleFile(t, filepath.Join(directory, fmt.Sprintf("service_%02d.go", fileIndex)), source)
		}
	}

	return representativeScaleFixture{
		root:            root,
		mutableContract: filepath.Join(root, "internal", "handlers", "h00", "contracts.go"),
		indexPath:       filepath.Join(root, "build", "api_index.json"),
		diagnosticsPath: filepath.Join(root, "build", "api_index.diagnostics.json"),
		openAPIPath:     filepath.Join(root, "build", "openapi.json"),
		cachePath:       filepath.Join(root, "build", "api_index.cache"),
	}
}

// writeRepresentativeScaleContract keeps seven reachable named contracts in every selected handler package.
func writeRepresentativeScaleContract(t testing.TB, path string, packageIndex, revision int) {
	t.Helper()
	traceField := "trace_a"
	if revision%2 != 0 {
		traceField = "trace_b"
	}
	source := fmt.Sprintf(`package h%02d

// Identifier represents a stable application identifier.
type Identifier struct {
	Value string `+"`json:\"value\" validate:\"required\"`"+`
}

// Audit records the request metadata returned by the application.
type Audit struct {
	Actor string `+"`json:\"actor\"`"+`
	Trace string `+"`json:\"%s\"`"+`
}

// Detail combines identity and audit data shared by requests and responses.
type Detail struct {
	ID    Identifier `+"`json:\"id\"`"+`
	Audit Audit      `+"`json:\"audit\"`"+`
}

// Request is the typed payload accepted by each fixture operation.
type Request struct {
	Detail Detail `+"`json:\"detail\" validate:\"required\"`"+`
}

// Result is the typed result nested in successful responses.
type Result struct {
	Detail Detail `+"`json:\"detail\"`"+`
}

// Envelope is the successful response contract.
type Envelope struct {
	Result Result `+"`json:\"result\"`"+`
}

// Problem is the validation-error response contract.
type Problem struct {
	Message string `+"`json:\"message\"`"+`
}
`, packageIndex, traceField)
	source = padRepresentativeScaleSource(source, representativeScaleTypedFileLines, fmt.Sprintf("h%02dcontracts", packageIndex))
	writeRepresentativeScaleFile(t, path, source)
}

// representativeScaleHandlerSource distributes 161 typed operations across eleven handler files per package.
func representativeScaleHandlerSource(packageIndex, fileIndex, operationCount int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "package h%02d\n\n", packageIndex)
	builder.WriteString("import \"github.com/goforj/web\"\n\n")
	if fileIndex == 0 {
		builder.WriteString("// Controller groups the package's scale-fixture routes.\n")
		builder.WriteString("type Controller struct{}\n\n")
		builder.WriteString("// Routes returns every operation selected from this handler package.\n")
		builder.WriteString("func (c *Controller) Routes() []web.Route {\n")
		builder.WriteString("\treturn []web.Route{\n")
		for operationIndex := 0; operationIndex < operationCount; operationIndex++ {
			fmt.Fprintf(&builder, "\t\tweb.NewRoute(\"POST\", \"/h%02d/op%03d\", c.Handle%03d),\n", packageIndex, operationIndex, operationIndex)
		}
		builder.WriteString("\t}\n")
		builder.WriteString("}\n\n")
	}

	handlerIndexes := []int{fileIndex}
	if fileIndex == 0 && operationCount == 12 {
		handlerIndexes = append(handlerIndexes, 11)
	}
	for _, handlerIndex := range handlerIndexes {
		fmt.Fprintf(&builder, "// Handle%03d binds and emits package-local contracts for a selected route.\n", handlerIndex)
		fmt.Fprintf(&builder, "func (c *Controller) Handle%03d(ctx web.Context) error {\n", handlerIndex)
		builder.WriteString("\trequest := Request{}\n")
		builder.WriteString("\tif err := ctx.Bind(&request); err != nil {\n")
		builder.WriteString("\t\treturn ctx.JSON(400, Problem{Message: \"invalid request\"})\n")
		builder.WriteString("\t}\n")
		builder.WriteString("\treturn ctx.JSON(200, Envelope{Result: Result{Detail: request.Detail}})\n")
		builder.WriteString("}\n\n")
	}
	return builder.String()
}

// padRepresentativeScaleSource adds ordinary checked control flow so file count alone cannot understate parser and type-checker work.
func padRepresentativeScaleSource(source string, targetLines int, identity string) string {
	var builder strings.Builder
	builder.WriteString(source)
	for helperIndex := 0; ; helperIndex++ {
		name := fmt.Sprintf("scaleFiller%sN%02d", identity, helperIndex)
		var block strings.Builder
		fmt.Fprintf(&block, "\n// %s keeps fixture syntax representative of application implementation files.\n", name)
		fmt.Fprintf(&block, "func %s(seed int) int {\n", name)
		block.WriteString("\tvalue := seed\n")
		for statement := 0; statement < 14; statement++ {
			fmt.Fprintf(&block, "\tvalue += %d\n", statement+1)
		}
		block.WriteString("\treturn value\n")
		block.WriteString("}\n")
		candidate := block.String()
		if representativeScaleLineCount(builder.String())+representativeScaleLineCount(candidate) > targetLines {
			break
		}
		builder.WriteString(candidate)
	}
	for line := representativeScaleLineCount(builder.String()); line < targetLines; line++ {
		builder.WriteString("// Scale fixture padding retains the measured source volume.\n")
	}
	return builder.String()
}

// representativeScaleLineCount treats each terminating newline as one generated source line.
func representativeScaleLineCount(source string) int {
	return strings.Count(source, "\n")
}

// writeRepresentativeScaleFile writes one fixture file and creates its package directory when necessary.
func writeRepresentativeScaleFile(t testing.TB, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create representative-scale fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write representative-scale fixture %s: %v", filepath.Base(path), err)
	}
}

// measureRepresentativeScaleSources verifies the generated workload rather than trusting fixture loop constants.
func measureRepresentativeScaleSources(t testing.TB, root string) representativeScaleSourceShape {
	t.Helper()
	shape := representativeScaleSourceShape{}
	typedPackages := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lines := representativeScaleLineCount(string(contents))
		shape.totalFiles++
		shape.totalLines += lines
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		if strings.HasPrefix(filepath.ToSlash(relative), "internal/handlers/") {
			shape.typedFiles++
			shape.typedLines += lines
			typedPackages[filepath.Dir(relative)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("measure representative-scale fixture: %v", err)
	}
	shape.typedPackages = len(typedPackages)
	return shape
}

// assertRepresentativeScaleSourceShape prevents an accidentally smaller fixture from producing flattering benchmark numbers.
func assertRepresentativeScaleSourceShape(t testing.TB, shape representativeScaleSourceShape) {
	t.Helper()
	if shape.typedPackages != representativeScaleHandlerPackages || shape.typedFiles != representativeScaleTypedFiles || shape.totalFiles != representativeScaleTotalFiles || shape.typedLines != representativeScaleTypedLines || shape.totalLines != representativeScaleTotalLines {
		t.Fatalf("unexpected representative-scale source shape: got %+v, want typed_packages=%d typed_files=%d total_files=%d typed_lines=%d total_lines=%d", shape, representativeScaleHandlerPackages, representativeScaleTypedFiles, representativeScaleTotalFiles, representativeScaleTypedLines, representativeScaleTotalLines)
	}
}

// assertRepresentativeScaleManifest checks every measured run against the application-level operation and schema counts.
func assertRepresentativeScaleManifest(t testing.TB, manifest Manifest) {
	t.Helper()
	if len(manifest.Operations) != representativeScaleOperations || len(manifest.Schemas) != representativeScaleSchemas || len(manifest.Diagnostics) != 0 {
		t.Fatalf("unexpected representative-scale manifest shape: operations=%d schemas=%d diagnostics=%d", len(manifest.Operations), len(manifest.Schemas), len(manifest.Diagnostics))
	}
	handlerPackages := map[string]struct{}{}
	for _, operation := range manifest.Operations {
		handlerPackages[operation.Handler.ImportPath] = struct{}{}
	}
	if len(handlerPackages) != representativeScaleHandlerPackages {
		t.Fatalf("unexpected selected handler package count: got %d, want %d", len(handlerPackages), representativeScaleHandlerPackages)
	}
}

// readRepresentativeScaleArtifacts decodes all three requested outputs and verifies their published shape.
func readRepresentativeScaleArtifacts(t testing.TB, fixture representativeScaleFixture) representativeScaleArtifacts {
	t.Helper()
	artifacts := representativeScaleArtifacts{}
	artifacts.indexBytes = readRepresentativeScaleJSON(t, fixture.indexPath, &artifacts.manifest)
	artifacts.diagnosticBytes = readRepresentativeScaleJSON(t, fixture.diagnosticsPath, &artifacts.diagnostics)
	artifacts.openAPIBytes = readRepresentativeScaleJSON(t, fixture.openAPIPath, &artifacts.openAPI)
	assertRepresentativeScaleManifest(t, artifacts.manifest)
	if len(artifacts.diagnostics) != 0 {
		t.Fatalf("unexpected published representative-scale diagnostics: %+v", artifacts.diagnostics)
	}
	if len(artifacts.openAPI.Paths) != representativeScaleOperations {
		t.Fatalf("unexpected OpenAPI path count: got %d, want %d", len(artifacts.openAPI.Paths), representativeScaleOperations)
	}
	components, ok := artifacts.openAPI.Components["schemas"].(map[string]any)
	if !ok || len(components) != representativeScaleSchemas {
		t.Fatalf("unexpected OpenAPI schema count: got %d, want %d", len(components), representativeScaleSchemas)
	}
	return artifacts
}

// assertRepresentativeScaleArtifactsEqual requires cache hits to reproduce each published artifact byte for byte.
func assertRepresentativeScaleArtifactsEqual(t testing.TB, got, want representativeScaleArtifacts) {
	t.Helper()
	for _, artifact := range []struct {
		name string
		got  []byte
		want []byte
	}{
		{name: "API index", got: got.indexBytes, want: want.indexBytes},
		{name: "diagnostics", got: got.diagnosticBytes, want: want.diagnosticBytes},
		{name: "OpenAPI", got: got.openAPIBytes, want: want.openAPIBytes},
	} {
		if !bytes.Equal(artifact.got, artifact.want) {
			t.Fatalf("persistent cache changed %s artifact bytes", artifact.name)
		}
	}
}

// readRepresentativeScaleFile reads a required fixture output without interpreting its binary cache format.
func readRepresentativeScaleFile(t testing.TB, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read representative-scale file %s: %v", filepath.Base(path), err)
	}
	return contents
}

// representativeScaleRaceEnabled detects instrumentation from the test binary's recorded build settings.
func representativeScaleRaceEnabled() bool {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			return true
		}
	}
	return false
}

// readRepresentativeScaleJSON reads and decodes one required benchmark artifact.
func readRepresentativeScaleJSON(t testing.TB, path string, destination any) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read representative-scale artifact %s: %v", filepath.Base(path), err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		t.Fatalf("decode representative-scale artifact %s: %v", filepath.Base(path), err)
	}
	return contents
}
