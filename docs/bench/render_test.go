//go:build benchrender

package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestRenderBenchmarks is the generation entry point used by the benchmark Make targets.
func TestRenderBenchmarks(t *testing.T) {
	if os.Getenv("BENCH_RENDER") != "1" {
		t.Skip("set BENCH_RENDER=1 to update benchmark artifacts")
	}
	root, err := findRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderBenchmarks(root); err != nil {
		t.Fatal(err)
	}
}

// TestParseBenchmarkOutput verifies metadata, custom throughput, derived throughput, and sample indexes.
func TestParseBenchmarkOutput(t *testing.T) {
	output := []byte(`goos: linux
goarch: amd64
pkg: github.com/goforj/web/docs/bench
cpu: Example CPU & Friends
BenchmarkHTTPStacks/static_text/goforj_web-1  2000000  125.5 ns/op  7968127 req/s  32 B/op  2 allocs/op
BenchmarkHTTPStacks/static_text/goforj_web-1  1900000  1.30e+02 ns/op  30 B/op  1 allocs/op
PASS
`)
	parsed, err := parseBenchmarkOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.GOOS != "linux" || parsed.GOARCH != "amd64" || parsed.CPU != "Example CPU & Friends" {
		t.Fatalf("metadata = %#v", parsed)
	}
	if len(parsed.Samples) != 2 {
		t.Fatalf("sample count = %d, want 2", len(parsed.Samples))
	}
	first := parsed.Samples[0]
	if first.Benchmark != "BenchmarkHTTPStacks/static_text/goforj_web" || first.Scenario != "static_text" || first.Framework != "goforj_web" {
		t.Fatalf("first sample identity = %#v", first)
	}
	if first.Sample != 1 || first.Iterations != 2_000_000 || first.ThroughputPerSecond != 7_968_127 || first.BytesPerOp != 32 || first.AllocsPerOp != 2 {
		t.Fatalf("first sample metrics = %#v", first)
	}
	second := parsed.Samples[1]
	if second.Sample != 2 {
		t.Fatalf("second sample index = %d, want 2", second.Sample)
	}
	wantDerived := 1e9 / 130
	if math.Abs(second.ThroughputPerSecond-wantDerived) > 0.001 {
		t.Fatalf("derived throughput = %f, want %f", second.ThroughputPerSecond, wantDerived)
	}
}

// TestParseBenchmarkOutputErrors checks malformed target rows and missing environment metadata.
func TestParseBenchmarkOutputErrors(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "bad name", output: "goos: linux\ngoarch: amd64\ncpu: cpu\nBenchmarkHTTPStacks/static_text-1 10 12 ns/op\n", want: "expected BenchmarkHTTPStacks/scenario/framework"},
		{name: "bad pairs", output: "goos: linux\ngoarch: amd64\ncpu: cpu\nBenchmarkHTTPStacks/static_text/echo-1 10 12 ns/op 2\n", want: "value/unit pairs"},
		{name: "no samples", output: "goos: linux\ngoarch: amd64\ncpu: cpu\nPASS\n", want: "contained no"},
		{name: "no goarch", output: "goos: linux\ncpu: cpu\nBenchmarkHTTPStacks/static_text/echo-1 10 12 ns/op 0 B/op 0 allocs/op\n", want: "missing goos"},
		{name: "missing bytes", output: "goos: linux\ngoarch: amd64\ncpu: cpu\nBenchmarkHTTPStacks/static_text/echo-1 10 12 ns/op 0 allocs/op\n", want: "nonnegative B/op"},
		{name: "missing allocations", output: "goos: linux\ngoarch: amd64\ncpu: cpu\nBenchmarkHTTPStacks/static_text/echo-1 10 12 ns/op 0 B/op\n", want: "nonnegative allocs/op"},
		{name: "negative allocations", output: "goos: linux\ngoarch: amd64\ncpu: cpu\nBenchmarkHTTPStacks/static_text/echo-1 10 12 ns/op 0 B/op -1 allocs/op\n", want: "nonnegative allocs/op"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseBenchmarkOutput([]byte(test.output))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

// TestParseBenchmarkOutputCPUFallback verifies deterministic metadata on platforms that omit cpu headers.
func TestParseBenchmarkOutputCPUFallback(t *testing.T) {
	output := []byte("goos: linux\ngoarch: arm64\nBenchmarkHTTPStacks/static_text/echo-1 10 12 ns/op 0 B/op 0 allocs/op\n")
	parsed, err := parseBenchmarkOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CPU != "arm64 (CPU model unavailable)" {
		t.Fatalf("CPU = %q", parsed.CPU)
	}
}

// TestMergeBenchmarkSample verifies independent runs receive stable sample numbers and consistent metadata.
func TestMergeBenchmarkSample(t *testing.T) {
	merged := parsedBenchmarkOutput{}
	first := parsedBenchmarkOutput{
		GOOS: "linux", GOARCH: "amd64", CPU: "Example CPU",
		Samples: []benchmarkSample{{Scenario: "static_text", Framework: "echo", Sample: 1}},
	}
	second := parsedBenchmarkOutput{
		GOOS: "linux", GOARCH: "amd64", CPU: "Example CPU",
		Samples: []benchmarkSample{{Scenario: "static_text", Framework: "echo", Sample: 1}},
	}
	if err := mergeBenchmarkSample(&merged, first, 1); err != nil {
		t.Fatal(err)
	}
	if err := mergeBenchmarkSample(&merged, second, 2); err != nil {
		t.Fatal(err)
	}
	if len(merged.Samples) != 2 || merged.Samples[0].Sample != 1 || merged.Samples[1].Sample != 2 {
		t.Fatalf("merged samples = %#v", merged.Samples)
	}

	changed := parsedBenchmarkOutput{GOOS: "darwin", GOARCH: "arm64", CPU: "Other CPU"}
	if err := mergeBenchmarkSample(&merged, changed, 3); err == nil || !strings.Contains(err.Error(), "environment changed") {
		t.Fatalf("environment mismatch error = %v", err)
	}
}

// TestMedian verifies odd, even, singleton, empty, and non-mutating behavior.
func TestMedian(t *testing.T) {
	values := []float64{9, 1, 5, 3}
	before := append([]float64(nil), values...)
	if got := median(values); got != 4 {
		t.Fatalf("even median = %f, want 4", got)
	}
	if !reflect.DeepEqual(values, before) {
		t.Fatalf("median mutated input: got %v, want %v", values, before)
	}
	if got := median([]float64{8, 2, 5}); got != 5 {
		t.Fatalf("odd median = %f, want 5", got)
	}
	if got := median([]float64{7}); got != 7 {
		t.Fatalf("singleton median = %f, want 7", got)
	}
	if got := median(nil); got != 0 {
		t.Fatalf("empty median = %f, want 0", got)
	}
}

// TestAggregateSamples verifies per-metric medians and process-paired middleware latency.
func TestAggregateSamples(t *testing.T) {
	samples := []benchmarkSample{
		{Scenario: "static_text", Framework: "echo", Sample: 1, NanosecondsPerOp: 100, ThroughputPerSecond: 10_000_000, BytesPerOp: 20, AllocsPerOp: 2},
		{Scenario: "static_text", Framework: "echo", Sample: 2, NanosecondsPerOp: 200, ThroughputPerSecond: 5_000_000, BytesPerOp: 24, AllocsPerOp: 4},
		{Scenario: "static_text", Framework: "echo", Sample: 3, NanosecondsPerOp: 1000, ThroughputPerSecond: 1_000_000, BytesPerOp: 28, AllocsPerOp: 6},
		{Scenario: "middleware_chain", Framework: "echo", Sample: 1, NanosecondsPerOp: 150, ThroughputPerSecond: 6_666_667, BytesPerOp: 30, AllocsPerOp: 5},
		{Scenario: "middleware_chain", Framework: "echo", Sample: 2, NanosecondsPerOp: 500, ThroughputPerSecond: 2_000_000, BytesPerOp: 34, AllocsPerOp: 7},
		{Scenario: "middleware_chain", Framework: "echo", Sample: 3, NanosecondsPerOp: 1050, ThroughputPerSecond: 952_381, BytesPerOp: 38, AllocsPerOp: 9},
	}
	aggregates := aggregateSamples(samples)
	if len(aggregates) != 2 {
		t.Fatalf("aggregate count = %d, want 2", len(aggregates))
	}
	var middleware benchmarkAggregate
	for _, aggregate := range aggregates {
		if aggregate.Scenario == "middleware_chain" {
			middleware = aggregate
		}
	}
	if middleware.Samples != 3 || middleware.NanosecondsPerOpMedian != 500 || middleware.BytesPerOpMedian != 34 || middleware.AllocsPerOpMedian != 7 {
		t.Fatalf("middleware aggregate = %#v", middleware)
	}
	if middleware.MiddlewareAddedLatencyMedian == nil {
		t.Fatal("middleware added latency is nil")
	}
	if got := *middleware.MiddlewareAddedLatencyMedian; got != 50 {
		t.Fatalf("paired middleware added latency = %f, want 50", got)
	}
}

// TestBenchmarkCountDefaultBalancesFrameworkOrder verifies the published default completes one full rotation.
func TestBenchmarkCountDefaultBalancesFrameworkOrder(t *testing.T) {
	t.Setenv("BENCH_COUNT", "")
	count, err := benchmarkCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != len(frameworkOrder) || count%len(frameworkOrder) != 0 {
		t.Fatalf("default benchmark count = %d, want one balanced rotation of %d", count, len(frameworkOrder))
	}
}

// TestConfiguredBenchmarkTime separates fast previews from publication-quality measurements.
func TestConfiguredBenchmarkTime(t *testing.T) {
	t.Setenv(requireCleanSnapshotEnvironment, "1")
	for _, value := range []string{"1x", "500ms", "not-a-duration"} {
		t.Setenv("BENCHTIME", value)
		if _, err := configuredBenchmarkTime(); err == nil || !strings.Contains(err.Error(), "at least 1s") {
			t.Fatalf("publication BENCHTIME %q error = %v, want minimum-duration error", value, err)
		}
	}
	t.Setenv("BENCHTIME", "1s")
	if got, err := configuredBenchmarkTime(); err != nil || got != "1s" {
		t.Fatalf("publication BENCHTIME = %q, %v", got, err)
	}
	t.Setenv(requireCleanSnapshotEnvironment, "")
	t.Setenv("BENCHTIME", "1x")
	if got, err := configuredBenchmarkTime(); err != nil || got != "1x" {
		t.Fatalf("preview BENCHTIME = %q, %v", got, err)
	}
}

// TestReplaceREADMEBlock verifies that unrelated README content and both markers remain intact.
func TestReplaceREADMEBlock(t *testing.T) {
	readme := []byte("before\n" + readmeStartMarker + "\nstale\n" + readmeEndMarker + "\nafter\n")
	updated, err := replaceREADMEBlock(readme, []byte("fresh\ncontent\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := "before\n" + readmeStartMarker + "\nfresh\ncontent\n" + readmeEndMarker + "\nafter\n"
	if string(updated) != want {
		t.Fatalf("updated README:\n%s\nwant:\n%s", updated, want)
	}
	second, err := replaceREADMEBlock(updated, []byte("fresh\ncontent\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updated, second) {
		t.Fatal("README replacement is not deterministic")
	}
}

// TestReplaceREADMEBlockErrors verifies marker presence, order, and uniqueness checks.
func TestReplaceREADMEBlockErrors(t *testing.T) {
	tests := []struct {
		name   string
		readme string
		want   string
	}{
		{name: "missing", readme: "README", want: "missing"},
		{name: "reversed", readme: readmeEndMarker + "\n" + readmeStartMarker, want: "out of order"},
		{name: "duplicate start", readme: readmeStartMarker + "\n" + readmeStartMarker + "\n" + readmeEndMarker, want: "unique"},
		{name: "duplicate end", readme: readmeStartMarker + "\n" + readmeEndMarker + "\n" + readmeEndMarker, want: "unique"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := replaceREADMEBlock([]byte(test.readme), []byte("new"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

// TestRenderBenchmarkSVG verifies deterministic XML, labels, units, escaping, and allocation scope.
func TestRenderBenchmarkSVG(t *testing.T) {
	snapshot := testBenchmarkSnapshot(2)
	snapshot.Metadata.CPU = "Example <CPU> & Friends"
	first, err := renderBenchmarkSVG(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderBenchmarkSVG(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("SVG rendering is not deterministic")
	}
	decoder := xml.NewDecoder(bytes.NewReader(first))
	for {
		if _, err := decoder.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("SVG is not valid XML: %v", err)
		}
	}
	text := string(first)
	for _, expected := range []string{"Equivalent endpoint work using idiomatic APIs", "not a ranking", "Observed sample min–max", "HTTP/1.1 loopback", "Plaintext route", "Path parameter + JSON", "Middleware: state + header + validation", "paired added latency", "GoForj Web", "Gorilla Mux", "req/s", "ops/s", "allocs/op", "+90 ns added", "&lt;CPU&gt; &amp; Friends", "Environment:", "Kernel ExampleOS 1.0", "GOAMD64=v1", "Inputs sha256:"} {
		if !strings.Contains(text, expected) {
			t.Errorf("SVG does not contain %q", expected)
		}
	}
	if got := strings.Count(text, `data-sample-range=`); got != len(scenarioOrder)*len(frameworkOrder) {
		t.Fatalf("sample-range whiskers = %d, want %d", got, len(scenarioOrder)*len(frameworkOrder))
	}
	if strings.Contains(text, "vs static") {
		t.Fatal("SVG presents middleware overhead against an ambiguous static baseline")
	}
	liveStart := strings.Index(text, "HTTP/1.1 loopback")
	plaintextStart := strings.Index(text[liveStart+1:], "Plaintext route") + liveStart + 1
	if strings.Contains(text[liveStart:plaintextStart], "allocs/op") {
		t.Fatal("live-loopback panel presents allocations as framework allocations")
	}
	if strings.Contains(text, "timestamp") || strings.Contains(text, "cachebuster") {
		t.Fatal("SVG contains a non-deterministic timestamp or cachebuster")
	}
	if strings.Contains(text, "DO NOT PUBLISH") {
		t.Fatal("clean snapshot SVG contains the dirty-tree preview warning")
	}
}

// TestDirtySnapshotPresentation makes preview provenance prominent in both generated surfaces.
func TestDirtySnapshotPresentation(t *testing.T) {
	snapshot := testBenchmarkSnapshot(2)
	snapshot.Metadata.RepositoryDirty = true
	svg, err := renderBenchmarkSVG(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(svg, []byte("LOCAL DIRTY-TREE PREVIEW · DO NOT PUBLISH")) {
		t.Fatal("dirty snapshot SVG has no publication warning")
	}
	readme := renderREADMEBlock(snapshot.Metadata)
	if !bytes.Contains(readme, []byte("not publication-ready")) {
		t.Fatal("dirty snapshot README has no publication warning")
	}
	for _, expected := range []string{"kernel `ExampleOS 1.0`", "`GOAMD64=v1`", "Benchmark inputs: `sha256:"} {
		if !bytes.Contains(readme, []byte(expected)) {
			t.Errorf("benchmark README does not contain %q", expected)
		}
	}
}

// TestEncodeBenchmarkSnapshot verifies stable sorting and byte-for-byte JSON output.
func TestEncodeBenchmarkSnapshot(t *testing.T) {
	snapshot := testBenchmarkSnapshot(1)
	for left, right := 0, len(snapshot.Samples)-1; left < right; left, right = left+1, right-1 {
		snapshot.Samples[left], snapshot.Samples[right] = snapshot.Samples[right], snapshot.Samples[left]
	}
	first, err := encodeBenchmarkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeBenchmarkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("snapshot encoding is not deterministic")
	}
	if !bytes.HasSuffix(first, []byte("\n")) {
		t.Fatal("snapshot JSON does not end with a newline")
	}
	if bytes.Index(first, []byte(`"scenario": "live_plain_text"`)) > bytes.Index(first, []byte(`"scenario": "static_text"`)) {
		t.Fatal("snapshot rows are not in the stable scenario order")
	}
}

// TestValidateSnapshotRejectsPartialMatrix verifies that missing framework samples fail closed.
func TestValidateSnapshotRejectsPartialMatrix(t *testing.T) {
	snapshot := testBenchmarkSnapshot(1)
	snapshot.Samples = snapshot.Samples[:len(snapshot.Samples)-1]
	err := validateSnapshot(snapshot)
	if err == nil || !strings.Contains(err.Error(), "want 1") {
		t.Fatalf("error = %v, want missing-sample error", err)
	}
}

// TestValidateSnapshotRejectsDuplicateSample verifies sample indexes identify independent runs uniquely.
func TestValidateSnapshotRejectsDuplicateSample(t *testing.T) {
	snapshot := testBenchmarkSnapshot(2)
	snapshot.Samples[1].Sample = snapshot.Samples[0].Sample
	err := validateSnapshot(snapshot)
	if err == nil || !strings.Contains(err.Error(), "duplicate sample") {
		t.Fatalf("error = %v, want duplicate-sample error", err)
	}
}

// TestValidateSnapshotRejectsAggregateMismatch verifies that sample rows remain authoritative.
func TestValidateSnapshotRejectsAggregateMismatch(t *testing.T) {
	snapshot := testBenchmarkSnapshot(1)
	snapshot.Aggregates[0].ThroughputPerSecondMedian++
	err := validateSnapshot(snapshot)
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("error = %v, want aggregate-mismatch error", err)
	}
}

// TestValidateSnapshotRejectsInvalidAllocations keeps malformed rows from appearing allocation-free.
func TestValidateSnapshotRejectsInvalidAllocations(t *testing.T) {
	snapshot := testBenchmarkSnapshot(1)
	snapshot.Samples[0].BytesPerOp = -1
	snapshot.Aggregates = aggregateSamples(snapshot.Samples)
	err := validateSnapshot(snapshot)
	if err == nil || !strings.Contains(err.Error(), "invalid allocation metrics") {
		t.Fatalf("error = %v, want invalid-allocation error", err)
	}
}

// TestValidateSnapshotRejectsInvalidIterations keeps malformed harness rows out of aggregates.
func TestValidateSnapshotRejectsInvalidIterations(t *testing.T) {
	snapshot := testBenchmarkSnapshot(1)
	snapshot.Samples[0].Iterations = 0
	snapshot.Aggregates = aggregateSamples(snapshot.Samples)
	err := validateSnapshot(snapshot)
	if err == nil || !strings.Contains(err.Error(), "non-positive iterations") {
		t.Fatalf("error = %v, want invalid-iteration error", err)
	}
}

// TestValidateBenchmarkMetadata checks fingerprint format and required build-setting order.
func TestValidateBenchmarkMetadata(t *testing.T) {
	if err := validateBenchmarkInputFingerprint("sha256:not-a-digest"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("invalid fingerprint error = %v", err)
	}
	settings := testBenchmarkSnapshot(1).Metadata.BuildSettings
	if err := validateBenchmarkBuildSettings("amd64", settings[:len(settings)-1]); err == nil || !strings.Contains(err.Error(), "want 5") {
		t.Fatalf("missing build setting error = %v", err)
	}
	settings = append([]benchmarkSetting(nil), settings...)
	settings[0], settings[1] = settings[1], settings[0]
	if err := validateBenchmarkBuildSettings("amd64", settings); err == nil || !strings.Contains(err.Error(), "stable order") {
		t.Fatalf("unordered build setting error = %v", err)
	}
}

// TestValidateSnapshotProvenance requires clean, revisioned, fully rotated publication data.
func TestValidateSnapshotProvenance(t *testing.T) {
	t.Setenv(requireCleanSnapshotEnvironment, "1")

	dirty := testBenchmarkSnapshot(len(frameworkOrder))
	dirty.Metadata.RepositoryDirty = true
	if err := validateSnapshotProvenance(dirty); err == nil || !strings.Contains(err.Error(), "dirty working tree") {
		t.Fatalf("dirty snapshot error = %v", err)
	}

	unknown := testBenchmarkSnapshot(len(frameworkOrder))
	unknown.Metadata.RepositoryRevision = "unknown"
	if err := validateSnapshotProvenance(unknown); err == nil || !strings.Contains(err.Error(), "verifiable") {
		t.Fatalf("unknown revision error = %v", err)
	}

	unbalanced := testBenchmarkSnapshot(2)
	if err := validateSnapshotProvenance(unbalanced); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("unbalanced snapshot error = %v", err)
	}

	short := testBenchmarkSnapshot(len(frameworkOrder))
	short.Metadata.BenchmarkTime = "500ms"
	if err := validateSnapshotProvenance(short); err == nil || !strings.Contains(err.Error(), "at least 1s") {
		t.Fatalf("short benchmark time error = %v", err)
	}

	balanced := testBenchmarkSnapshot(len(frameworkOrder))
	if err := validateSnapshotProvenance(balanced); err != nil {
		t.Fatalf("balanced clean snapshot: %v", err)
	}
}

// TestHashBenchmarkInputFiles verifies deterministic ordering and sensitivity to names and contents.
func TestHashBenchmarkInputFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := hashBenchmarkInputFiles(root, []string{"go.mod", "a.go"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashBenchmarkInputFiles(root, []string{"a.go", "go.mod"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("input fingerprint depends on path order: %s != %s", first, second)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := hashBenchmarkInputFiles(root, []string{"a.go", "go.mod"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("input fingerprint did not change with source contents")
	}
}

// TestBenchmarkInputPaths covers local runtime sources, harness files, and module pins without generated artifacts.
func TestBenchmarkInputPaths(t *testing.T) {
	root, err := findRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := readBenchmarkBuildSettings(root, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := benchmarkInputPaths(root, runtime.GOOS, runtime.GOARCH, settings)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		seen[path] = true
	}
	for _, expected := range []string{"context.go", "adapter/echoweb/context.go", "go.mod", "go.sum", "docs/go.mod", "docs/go.sum", "docs/bench/bench_test.go", "docs/bench/render.go"} {
		if !seen[expected] {
			t.Errorf("benchmark input inventory is missing %s", expected)
		}
	}
	for _, generated := range []string{"README.md", "docs/bench/benchmarks_rows.json", "docs/bench/framework_comparison.svg", "adapter/echoweb/adapter_test.go"} {
		if seen[generated] {
			t.Errorf("benchmark input inventory includes non-measurement file %s", generated)
		}
	}
}

// TestValidateSnapshotInputFingerprint detects stale measurements without comparing machine results.
func TestValidateSnapshotInputFingerprint(t *testing.T) {
	root, err := findRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := readBenchmarkBuildSettings(root, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := benchmarkInputFingerprint(root, runtime.GOOS, runtime.GOARCH, settings)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testBenchmarkSnapshot(len(frameworkOrder))
	snapshot.Metadata.GOOS = runtime.GOOS
	snapshot.Metadata.GOARCH = runtime.GOARCH
	snapshot.Metadata.BuildSettings = settings
	snapshot.Metadata.BenchmarkInputFingerprint = fingerprint
	t.Setenv(requireCleanSnapshotEnvironment, "1")
	if err := validateSnapshotInputFingerprint(root, snapshot); err != nil {
		t.Fatalf("matching benchmark inputs: %v", err)
	}
	snapshot.Metadata.BenchmarkInputFingerprint = benchmarkInputHashPrefix + strings.Repeat("0", sha256.Size*2)
	if err := validateSnapshotInputFingerprint(root, snapshot); err == nil || !strings.Contains(err.Error(), "differ from measured snapshot") {
		t.Fatalf("stale benchmark input error = %v", err)
	}
}

// TestRepositoryChangedDuringMeasurement verifies that provenance changes fail closed in every direction.
func TestRepositoryChangedDuringMeasurement(t *testing.T) {
	tests := []struct {
		name          string
		startRevision string
		startDirty    bool
		endRevision   string
		endDirty      bool
		want          bool
	}{
		{name: "stable clean", startRevision: "abc", endRevision: "abc"},
		{name: "stable dirty", startRevision: "abc", startDirty: true, endRevision: "abc", endDirty: true},
		{name: "revision changed", startRevision: "abc", endRevision: "def", want: true},
		{name: "became dirty", startRevision: "abc", endRevision: "abc", endDirty: true, want: true},
		{name: "became clean", startRevision: "abc", startDirty: true, endRevision: "abc", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := repositoryChangedDuringMeasurement(test.startRevision, test.startDirty, test.endRevision, test.endDirty)
			if got != test.want {
				t.Fatalf("repositoryChangedDuringMeasurement() = %t, want %t", got, test.want)
			}
		})
	}
}

// TestRecordedSnapshotInputFingerprintMatchesCurrentSource keeps preview and publication rows tied to their inputs.
func TestRecordedSnapshotInputFingerprintMatchesCurrentSource(t *testing.T) {
	root, err := findRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadBenchmarkSnapshot(filepath.Join(root, "docs", "bench", "benchmarks_rows.json"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := benchmarkInputFingerprint(root, snapshot.Metadata.GOOS, snapshot.Metadata.GOARCH, snapshot.Metadata.BuildSettings)
	if err != nil {
		t.Fatal(err)
	}
	if current != snapshot.Metadata.BenchmarkInputFingerprint {
		t.Fatalf("recorded benchmark input fingerprint = %s, current = %s", snapshot.Metadata.BenchmarkInputFingerprint, current)
	}
}

// TestRenderOnlyRejectsDirtyPublicationSnapshot ensures the public renderer fails before writing preview data as final output.
func TestRenderOnlyRejectsDirtyPublicationSnapshot(t *testing.T) {
	root := t.TempDir()
	benchDirectory := filepath.Join(root, "docs", "bench")
	if err := os.MkdirAll(benchDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	readme := []byte("# Project\n\n" + readmeStartMarker + "\nold\n" + readmeEndMarker + "\n")
	readmePath := filepath.Join(root, "README.md")
	if err := os.WriteFile(readmePath, readme, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := testBenchmarkSnapshot(len(frameworkOrder))
	snapshot.Metadata.RepositoryDirty = true
	snapshotBytes, err := encodeBenchmarkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(benchDirectory, "benchmarks_rows.json"), snapshotBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_RENDER_ONLY", "1")
	t.Setenv(requireCleanSnapshotEnvironment, "1")
	if err := RenderBenchmarks(root); err == nil || !strings.Contains(err.Error(), "not publication-ready") {
		t.Fatalf("render error = %v", err)
	}
	unchangedREADME, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unchangedREADME, readme) {
		t.Fatal("failed publication check changed README")
	}
	if _, err := os.Stat(filepath.Join(benchDirectory, "framework_comparison.svg")); !os.IsNotExist(err) {
		t.Fatalf("failed publication check SVG error = %v, want not-exist", err)
	}
}

// TestRenderOnlyDeterministic exercises the public render-only path without running benchmarks.
func TestRenderOnlyDeterministic(t *testing.T) {
	root := t.TempDir()
	benchDirectory := filepath.Join(root, "docs", "bench")
	if err := os.MkdirAll(benchDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	readme := []byte("# Project\n\n" + readmeStartMarker + "\nold\n" + readmeEndMarker + "\n\n## API\n")
	if err := os.WriteFile(filepath.Join(root, "README.md"), readme, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshotBytes, err := encodeBenchmarkSnapshot(testBenchmarkSnapshot(2))
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(benchDirectory, "benchmarks_rows.json")
	if err := os.WriteFile(snapshotPath, snapshotBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_RENDER_ONLY", "1")
	if err := RenderBenchmarks(root); err != nil {
		t.Fatal(err)
	}
	firstSVG, err := os.ReadFile(filepath.Join(benchDirectory, "framework_comparison.svg"))
	if err != nil {
		t.Fatal(err)
	}
	firstREADME, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderBenchmarks(root); err != nil {
		t.Fatal(err)
	}
	secondSVG, _ := os.ReadFile(filepath.Join(benchDirectory, "framework_comparison.svg"))
	secondREADME, _ := os.ReadFile(filepath.Join(root, "README.md"))
	if !bytes.Equal(firstSVG, secondSVG) || !bytes.Equal(firstREADME, secondREADME) {
		t.Fatal("render-only output changed between identical runs")
	}
	unchangedSnapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshotBytes, unchangedSnapshot) {
		t.Fatal("render-only mode rewrote the benchmark snapshot")
	}
}

// testBenchmarkSnapshot returns a complete deterministic scenario/framework matrix.
func testBenchmarkSnapshot(sampleCount int) benchmarkSnapshot {
	snapshot := benchmarkSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		Metadata: benchmarkMetadata{
			GoVersion:                 "go1.25.0",
			GOOS:                      "linux",
			GOARCH:                    "amd64",
			CPU:                       "Example CPU",
			Kernel:                    "ExampleOS 1.0",
			GOMAXPROCS:                1,
			SampleCount:               sampleCount,
			BenchmarkTime:             "1s",
			RepositoryRevision:        "0123456789ab",
			BenchmarkInputFingerprint: benchmarkInputHashPrefix + strings.Repeat("a", sha256.Size*2),
			BuildSettings: []benchmarkSetting{
				{Name: "CGO_ENABLED", Value: "1"},
				{Name: "GOAMD64", Value: "v1"},
				{Name: "GODEBUG", Value: ""},
				{Name: "GOEXPERIMENT", Value: ""},
				{Name: "GOFLAGS", Value: ""},
			},
			Dependencies: []dependencyVersion{
				{Name: "net/http", Module: "standard library", Version: "go1.25.0"},
				{Name: "GoForj Web", Module: "github.com/goforj/web", Version: "local checkout"},
				{Name: "Echo", Module: "github.com/labstack/echo/v5", Version: "v5.1.0"},
			},
		},
	}
	for scenarioPosition, scenario := range scenarioOrder {
		for frameworkPosition, framework := range frameworkOrder {
			for sample := 1; sample <= sampleCount; sample++ {
				nanoseconds := float64(80 + scenarioPosition*45 + frameworkPosition*11 + sample)
				snapshot.Samples = append(snapshot.Samples, benchmarkSample{
					Benchmark:           "BenchmarkHTTPStacks/" + scenario + "/" + framework,
					Scenario:            scenario,
					Framework:           framework,
					Sample:              sample,
					Iterations:          1_000_000,
					NanosecondsPerOp:    nanoseconds,
					ThroughputPerSecond: 1e9 / nanoseconds,
					BytesPerOp:          float64(frameworkPosition * 8),
					AllocsPerOp:         float64(frameworkPosition),
				})
			}
		}
	}
	snapshot.Aggregates = aggregateSamples(snapshot.Samples)
	sortSnapshot(&snapshot)
	return snapshot
}

// findRepositoryRoot walks upward from the package working directory to the project README.
func findRepositoryRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, readmeErr := os.Stat(filepath.Join(directory, "README.md")); readmeErr == nil {
			if _, benchErr := os.Stat(filepath.Join(directory, "docs", "bench")); benchErr == nil {
				return directory, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", os.ErrNotExist
		}
		directory = parent
	}
}
