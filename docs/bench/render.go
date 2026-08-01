//go:build benchrender

package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	snapshotSchemaVersion           = 3
	defaultBenchmarkCount           = 7
	defaultBenchmarkTime            = "1s"
	minimumPublicationBenchmarkTime = time.Second
	allowDirtyBenchmarkEnvironment  = "BENCH_ALLOW_DIRTY"
	requireCleanSnapshotEnvironment = "BENCH_REQUIRE_CLEAN_SNAPSHOT"
	readmeStartMarker               = "<!-- bench:embed:start -->"
	readmeEndMarker                 = "<!-- bench:embed:end -->"
	benchmarkInputHashPrefix        = "sha256:"
)

var scenarioOrder = []string{
	"live_plain_text",
	"static_text",
	"path_param_json",
	"middleware_chain",
}

var frameworkOrder = []string{
	"goforj_web",
	"net_http",
	"echo",
	"gin",
	"chi",
	"gorilla_mux",
	"httprouter",
}

var frameworkLabels = map[string]string{
	"goforj_web":  "GoForj Web",
	"net_http":    "net/http",
	"echo":        "Echo",
	"gin":         "Gin",
	"chi":         "Chi",
	"gorilla_mux": "Gorilla Mux",
	"httprouter":  "httprouter",
}

var dependencyModules = []dependencyModule{
	{name: "GoForj Web", module: "github.com/goforj/web"},
	{name: "Echo", module: "github.com/labstack/echo/v5"},
	{name: "Gin", module: "github.com/gin-gonic/gin"},
	{name: "Chi", module: "github.com/go-chi/chi/v5"},
	{name: "Gorilla Mux", module: "github.com/gorilla/mux"},
	{name: "httprouter", module: "github.com/julienschmidt/httprouter"},
}

type dependencyModule struct {
	name   string
	module string
}

type benchmarkSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	Metadata      benchmarkMetadata    `json:"metadata"`
	Samples       []benchmarkSample    `json:"samples"`
	Aggregates    []benchmarkAggregate `json:"aggregates"`
}

type benchmarkMetadata struct {
	GoVersion                 string              `json:"go_version"`
	GOOS                      string              `json:"goos"`
	GOARCH                    string              `json:"goarch"`
	CPU                       string              `json:"cpu"`
	Kernel                    string              `json:"kernel"`
	GOMAXPROCS                int                 `json:"gomaxprocs"`
	SampleCount               int                 `json:"sample_count"`
	BenchmarkTime             string              `json:"benchmark_time"`
	RepositoryRevision        string              `json:"repository_revision"`
	RepositoryDirty           bool                `json:"repository_dirty"`
	BenchmarkInputFingerprint string              `json:"benchmark_input_fingerprint"`
	BuildSettings             []benchmarkSetting  `json:"build_settings"`
	Dependencies              []dependencyVersion `json:"dependencies"`
}

type benchmarkSetting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type dependencyVersion struct {
	Name    string `json:"name"`
	Module  string `json:"module"`
	Version string `json:"version"`
}

type benchmarkSample struct {
	Benchmark           string  `json:"benchmark"`
	Scenario            string  `json:"scenario"`
	Framework           string  `json:"framework"`
	Sample              int     `json:"sample"`
	Iterations          int64   `json:"iterations"`
	NanosecondsPerOp    float64 `json:"nanoseconds_per_op"`
	ThroughputPerSecond float64 `json:"throughput_per_second"`
	BytesPerOp          float64 `json:"bytes_per_op"`
	AllocsPerOp         float64 `json:"allocs_per_op"`
}

type benchmarkAggregate struct {
	Scenario                     string   `json:"scenario"`
	Framework                    string   `json:"framework"`
	Samples                      int      `json:"samples"`
	NanosecondsPerOpMedian       float64  `json:"nanoseconds_per_op_median"`
	ThroughputPerSecondMedian    float64  `json:"throughput_per_second_median"`
	BytesPerOpMedian             float64  `json:"bytes_per_op_median"`
	AllocsPerOpMedian            float64  `json:"allocs_per_op_median"`
	MiddlewareAddedLatencyMedian *float64 `json:"middleware_added_latency_ns_median,omitempty"`
}

type parsedBenchmarkOutput struct {
	GOOS    string
	GOARCH  string
	CPU     string
	Samples []benchmarkSample
}

type moduleRecord struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Main    bool          `json:"Main"`
	Dir     string        `json:"Dir"`
	GoMod   string        `json:"GoMod"`
	Replace *moduleRecord `json:"Replace"`
}

type packageRecord struct {
	ImportPath string        `json:"ImportPath"`
	Dir        string        `json:"Dir"`
	Module     *moduleRecord `json:"Module"`
	EmbedFiles []string      `json:"EmbedFiles"`
}

type panelDefinition struct {
	scenario string
	title    string
	subtitle string
	live     bool
}

type throughputRange struct {
	minimum float64
	maximum float64
}

// RenderBenchmarks measures the HTTP stacks or renders the checked-in snapshot.
// BENCH_RENDER_ONLY=1 skips measurement and leaves the snapshot untouched.
func RenderBenchmarks(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}

	snapshotPath := filepath.Join(root, "docs", "bench", "benchmarks_rows.json")
	var snapshot benchmarkSnapshot
	renderOnly := os.Getenv("BENCH_RENDER_ONLY") == "1"
	if renderOnly {
		snapshot, err = loadBenchmarkSnapshot(snapshotPath)
		if err != nil {
			return err
		}
	} else {
		snapshot, err = measureBenchmarks(root)
		if err != nil {
			return err
		}
	}

	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	if err := validateSnapshotProvenance(snapshot); err != nil {
		return err
	}
	if err := validateSnapshotInputFingerprint(root, snapshot); err != nil {
		return err
	}
	if !renderOnly {
		encoded, encodeErr := encodeBenchmarkSnapshot(snapshot)
		if encodeErr != nil {
			return encodeErr
		}
		if err := writeFileIfChanged(snapshotPath, encoded); err != nil {
			return fmt.Errorf("write benchmark snapshot: %w", err)
		}
	}
	svg, err := renderBenchmarkSVG(snapshot)
	if err != nil {
		return err
	}
	if err := writeFileIfChanged(filepath.Join(root, "docs", "bench", "framework_comparison.svg"), svg); err != nil {
		return fmt.Errorf("write benchmark SVG: %w", err)
	}

	readmePath := filepath.Join(root, "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		return fmt.Errorf("read README: %w", err)
	}
	updated, err := replaceREADMEBlock(readme, renderREADMEBlock(snapshot.Metadata))
	if err != nil {
		return err
	}
	if err := writeFileIfChanged(readmePath, updated); err != nil {
		return fmt.Errorf("write README: %w", err)
	}
	return nil
}

// measureBenchmarks runs the comparison suite and assembles its reproducibility metadata.
func measureBenchmarks(root string) (benchmarkSnapshot, error) {
	startRevision, startDirty := repositoryRevision(root)
	if startDirty && os.Getenv(allowDirtyBenchmarkEnvironment) != "1" {
		return benchmarkSnapshot{}, fmt.Errorf("refusing to measure a dirty working tree; commit or stash changes for a publishable snapshot, or set %s=1 for an explicitly marked local preview", allowDirtyBenchmarkEnvironment)
	}
	count, err := benchmarkCount()
	if err != nil {
		return benchmarkSnapshot{}, err
	}
	benchmarkTime, err := configuredBenchmarkTime()
	if err != nil {
		return benchmarkSnapshot{}, err
	}
	buildSettings, err := readBenchmarkBuildSettings(root, runtime.GOARCH)
	if err != nil {
		return benchmarkSnapshot{}, err
	}
	startFingerprint, err := benchmarkInputFingerprint(root, runtime.GOOS, runtime.GOARCH, buildSettings)
	if err != nil {
		return benchmarkSnapshot{}, err
	}
	generatorCache := strings.TrimSpace(os.Getenv("GOCACHE"))
	if generatorCache == "" {
		generatorCache = filepath.Join(os.TempDir(), "gocache")
	}

	parsed := parsedBenchmarkOutput{}
	for sample := 1; sample <= count; sample++ {
		rotation := (sample - 1) % len(frameworkOrder)
		output, runErr := runBenchmarkSample(root, benchmarkTime, generatorCache, rotation)
		if runErr != nil {
			return benchmarkSnapshot{}, fmt.Errorf("run HTTP stack benchmark sample %d/%d: %w\n%s", sample, count, runErr, output)
		}
		current, parseErr := parseBenchmarkOutput(output)
		if parseErr != nil {
			return benchmarkSnapshot{}, fmt.Errorf("parse HTTP stack benchmark sample %d/%d: %w", sample, count, parseErr)
		}
		if mergeErr := mergeBenchmarkSample(&parsed, current, sample); mergeErr != nil {
			return benchmarkSnapshot{}, fmt.Errorf("merge HTTP stack benchmark sample %d/%d: %w", sample, count, mergeErr)
		}
	}
	dependencies, err := readDependencyVersions(root, runtime.Version())
	if err != nil {
		return benchmarkSnapshot{}, err
	}
	endFingerprint, err := benchmarkInputFingerprint(root, parsed.GOOS, parsed.GOARCH, buildSettings)
	if err != nil {
		return benchmarkSnapshot{}, err
	}
	if endFingerprint != startFingerprint {
		return benchmarkSnapshot{}, errors.New("benchmark inputs changed while benchmarks were running; rerun from a stable source tree")
	}
	revision, dirty := repositoryRevision(root)
	if repositoryChangedDuringMeasurement(startRevision, startDirty, revision, dirty) {
		return benchmarkSnapshot{}, errors.New("repository changed while benchmarks were running; rerun from a stable working tree")
	}
	snapshot := benchmarkSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		Metadata: benchmarkMetadata{
			GoVersion:                 runtime.Version(),
			GOOS:                      parsed.GOOS,
			GOARCH:                    parsed.GOARCH,
			CPU:                       parsed.CPU,
			Kernel:                    readKernelVersion(),
			GOMAXPROCS:                1,
			SampleCount:               count,
			BenchmarkTime:             benchmarkTime,
			RepositoryRevision:        revision,
			RepositoryDirty:           dirty,
			BenchmarkInputFingerprint: startFingerprint,
			BuildSettings:             buildSettings,
			Dependencies:              dependencies,
		},
		Samples: parsed.Samples,
	}
	snapshot.Aggregates = aggregateSamples(snapshot.Samples)
	sortSnapshot(&snapshot)
	if err := validateSnapshot(snapshot); err != nil {
		return benchmarkSnapshot{}, err
	}
	return snapshot, nil
}

// validateSnapshotProvenance rejects preview data when publication checks are requested.
func validateSnapshotProvenance(snapshot benchmarkSnapshot) error {
	if os.Getenv(requireCleanSnapshotEnvironment) != "1" {
		return nil
	}
	if snapshot.Metadata.RepositoryDirty {
		return errors.New("benchmark snapshot was measured from a dirty working tree and is not publication-ready")
	}
	if snapshot.Metadata.RepositoryRevision == "" || snapshot.Metadata.RepositoryRevision == "unknown" {
		return errors.New("benchmark snapshot has no verifiable repository revision")
	}
	if snapshot.Metadata.SampleCount%len(frameworkOrder) != 0 {
		return fmt.Errorf("benchmark snapshot uses %d samples; publication requires a multiple of %d so every framework occupies every run-order position equally", snapshot.Metadata.SampleCount, len(frameworkOrder))
	}
	if err := validatePublicationBenchmarkTime(snapshot.Metadata.BenchmarkTime); err != nil {
		return err
	}
	return nil
}

// validateSnapshotInputFingerprint rejects publication data measured against different benchmark inputs.
func validateSnapshotInputFingerprint(root string, snapshot benchmarkSnapshot) error {
	if os.Getenv(requireCleanSnapshotEnvironment) != "1" {
		return nil
	}
	current, err := benchmarkInputFingerprint(root, snapshot.Metadata.GOOS, snapshot.Metadata.GOARCH, snapshot.Metadata.BuildSettings)
	if err != nil {
		return fmt.Errorf("fingerprint current benchmark inputs: %w", err)
	}
	if current != snapshot.Metadata.BenchmarkInputFingerprint {
		return fmt.Errorf("benchmark inputs differ from measured snapshot: current %s, recorded %s; rerun make bench-svg from a clean source commit", current, snapshot.Metadata.BenchmarkInputFingerprint)
	}
	return nil
}

// runBenchmarkSample measures one complete matrix with a rotated framework order.
func runBenchmarkSample(root, benchmarkTime, generatorCache string, rotation int) ([]byte, error) {
	command := exec.Command(
		"go", "test", ".",
		"-run=^$",
		"-bench=^BenchmarkHTTPStacks$",
		"-benchmem",
		"-benchtime="+benchmarkTime,
		"-count=1",
		"-cpu=1",
	)
	command.Dir = filepath.Join(root, "docs", "bench")
	// A separate build cache prevents the nested Go process from racing the generator's cache trim.
	command.Env = environmentWith(
		os.Environ(),
		"GOWORK", "off",
		"GOMAXPROCS", "1",
		"GOCACHE", generatorCache+"-benchmark",
		frameworkRotationEnvironment, strconv.Itoa(rotation),
	)
	return command.CombinedOutput()
}

// mergeBenchmarkSample appends one independently measured matrix and assigns its stable sample number.
func mergeBenchmarkSample(merged *parsedBenchmarkOutput, current parsedBenchmarkOutput, sample int) error {
	if merged == nil {
		return errors.New("merged benchmark output is nil")
	}
	if sample < 1 {
		return fmt.Errorf("sample number must be positive, got %d", sample)
	}
	if merged.GOOS == "" {
		merged.GOOS = current.GOOS
		merged.GOARCH = current.GOARCH
		merged.CPU = current.CPU
	} else if merged.GOOS != current.GOOS || merged.GOARCH != current.GOARCH || merged.CPU != current.CPU {
		return fmt.Errorf("benchmark environment changed from %s/%s %q to %s/%s %q",
			merged.GOOS, merged.GOARCH, merged.CPU, current.GOOS, current.GOARCH, current.CPU)
	}
	for _, row := range current.Samples {
		row.Sample = sample
		merged.Samples = append(merged.Samples, row)
	}
	return nil
}

// benchmarkCount returns the configured number of independent samples.
func benchmarkCount() (int, error) {
	value := strings.TrimSpace(os.Getenv("BENCH_COUNT"))
	if value == "" {
		return defaultBenchmarkCount, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 1 {
		return 0, fmt.Errorf("BENCH_COUNT must be a positive integer, got %q", value)
	}
	return count, nil
}

// configuredBenchmarkTime returns the requested harness duration and rejects weak publication measurements early.
func configuredBenchmarkTime() (string, error) {
	value := strings.TrimSpace(os.Getenv("BENCHTIME"))
	if value == "" {
		value = defaultBenchmarkTime
	}
	if os.Getenv(requireCleanSnapshotEnvironment) == "1" {
		if err := validatePublicationBenchmarkTime(value); err != nil {
			return "", err
		}
	}
	return value, nil
}

// validatePublicationBenchmarkTime requires time-based samples long enough for stable calibration.
func validatePublicationBenchmarkTime(value string) error {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("publication BENCHTIME must be a duration of at least %s, got %q", minimumPublicationBenchmarkTime, value)
	}
	if duration < minimumPublicationBenchmarkTime {
		return fmt.Errorf("publication BENCHTIME must be at least %s, got %q", minimumPublicationBenchmarkTime, value)
	}
	return nil
}

// parseBenchmarkOutput converts standard go test benchmark rows into samples.
func parseBenchmarkOutput(output []byte) (parsedBenchmarkOutput, error) {
	parsed := parsedBenchmarkOutput{}
	sampleIndexes := make(map[string]int)
	for _, rawLine := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "goos:"):
			parsed.GOOS = strings.TrimSpace(strings.TrimPrefix(line, "goos:"))
			continue
		case strings.HasPrefix(line, "goarch:"):
			parsed.GOARCH = strings.TrimSpace(strings.TrimPrefix(line, "goarch:"))
			continue
		case strings.HasPrefix(line, "cpu:"):
			parsed.CPU = strings.TrimSpace(strings.TrimPrefix(line, "cpu:"))
			continue
		case !strings.HasPrefix(line, "BenchmarkHTTPStacks/"):
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse benchmark row %q: expected name, iterations, and metrics", line)
		}
		benchmarkName := trimCPUSuffix(fields[0])
		nameParts := strings.Split(benchmarkName, "/")
		if len(nameParts) != 3 || nameParts[0] != "BenchmarkHTTPStacks" {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse benchmark name %q: expected BenchmarkHTTPStacks/scenario/framework", fields[0])
		}
		iterations, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse iterations in %q: %w", line, err)
		}
		metrics, err := parseMetricPairs(fields[2:])
		if err != nil {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse metrics in %q: %w", line, err)
		}
		nanoseconds, ok := metrics["ns/op"]
		if !ok || nanoseconds <= 0 || math.IsNaN(nanoseconds) || math.IsInf(nanoseconds, 0) {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse benchmark row %q: missing positive ns/op", line)
		}
		throughput := metrics["req/s"]
		if throughput <= 0 || math.IsNaN(throughput) || math.IsInf(throughput, 0) {
			throughput = 1e9 / nanoseconds
		}
		bytesPerOp, ok := metrics["B/op"]
		if !ok || bytesPerOp < 0 || math.IsNaN(bytesPerOp) || math.IsInf(bytesPerOp, 0) {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse benchmark row %q: missing nonnegative B/op", line)
		}
		allocsPerOp, ok := metrics["allocs/op"]
		if !ok || allocsPerOp < 0 || math.IsNaN(allocsPerOp) || math.IsInf(allocsPerOp, 0) {
			return parsedBenchmarkOutput{}, fmt.Errorf("parse benchmark row %q: missing nonnegative allocs/op", line)
		}
		key := nameParts[1] + "/" + nameParts[2]
		sampleIndexes[key]++
		parsed.Samples = append(parsed.Samples, benchmarkSample{
			Benchmark:           benchmarkName,
			Scenario:            nameParts[1],
			Framework:           nameParts[2],
			Sample:              sampleIndexes[key],
			Iterations:          iterations,
			NanosecondsPerOp:    nanoseconds,
			ThroughputPerSecond: throughput,
			BytesPerOp:          bytesPerOp,
			AllocsPerOp:         allocsPerOp,
		})
	}
	if len(parsed.Samples) == 0 {
		return parsedBenchmarkOutput{}, errors.New("benchmark output contained no BenchmarkHTTPStacks samples")
	}
	if parsed.GOOS == "" || parsed.GOARCH == "" {
		return parsedBenchmarkOutput{}, errors.New("benchmark output is missing goos or goarch metadata")
	}
	if parsed.CPU == "" {
		parsed.CPU = parsed.GOARCH + " (CPU model unavailable)"
	}
	return parsed, nil
}

// parseMetricPairs parses the value/unit pairs emitted by Go's benchmark harness.
func parseMetricPairs(fields []string) (map[string]float64, error) {
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("metric fields must be value/unit pairs: %q", strings.Join(fields, " "))
	}
	metrics := make(map[string]float64, len(fields)/2)
	for index := 0; index < len(fields); index += 2 {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err != nil {
			return nil, fmt.Errorf("parse metric %s: %w", fields[index+1], err)
		}
		metrics[fields[index+1]] = value
	}
	return metrics, nil
}

// trimCPUSuffix removes the -N suffix added by go test's -cpu flag.
func trimCPUSuffix(name string) string {
	separator := strings.LastIndexByte(name, '-')
	if separator < 0 {
		return name
	}
	if _, err := strconv.Atoi(name[separator+1:]); err != nil {
		return name
	}
	return name[:separator]
}

// aggregateSamples calculates medians without discarding the recorded sample rows.
func aggregateSamples(samples []benchmarkSample) []benchmarkAggregate {
	groups := make(map[string][]benchmarkSample)
	for _, sample := range samples {
		key := sample.Scenario + "\x00" + sample.Framework
		groups[key] = append(groups[key], sample)
	}

	aggregates := make([]benchmarkAggregate, 0, len(groups))
	for key, group := range groups {
		parts := strings.SplitN(key, "\x00", 2)
		nanoseconds := make([]float64, 0, len(group))
		throughput := make([]float64, 0, len(group))
		bytesPerOp := make([]float64, 0, len(group))
		allocsPerOp := make([]float64, 0, len(group))
		for _, sample := range group {
			nanoseconds = append(nanoseconds, sample.NanosecondsPerOp)
			throughput = append(throughput, sample.ThroughputPerSecond)
			bytesPerOp = append(bytesPerOp, sample.BytesPerOp)
			allocsPerOp = append(allocsPerOp, sample.AllocsPerOp)
		}
		aggregates = append(aggregates, benchmarkAggregate{
			Scenario:                  parts[0],
			Framework:                 parts[1],
			Samples:                   len(group),
			NanosecondsPerOpMedian:    median(nanoseconds),
			ThroughputPerSecondMedian: median(throughput),
			BytesPerOpMedian:          median(bytesPerOp),
			AllocsPerOpMedian:         median(allocsPerOp),
		})
	}

	for index := range aggregates {
		aggregate := &aggregates[index]
		if aggregate.Scenario != "middleware_chain" {
			continue
		}
		addedLatency := pairedMiddlewareAddedLatency(samples, aggregate.Framework)
		if len(addedLatency) > 0 {
			value := median(addedLatency)
			aggregate.MiddlewareAddedLatencyMedian = &value
		}
	}
	sortAggregates(aggregates)
	return aggregates
}

// pairedMiddlewareAddedLatency pairs each delta within one process so machine drift affects both sides of the comparison.
func pairedMiddlewareAddedLatency(samples []benchmarkSample, framework string) []float64 {
	baselineBySample := make(map[int]float64)
	for _, sample := range samples {
		if sample.Framework == framework && sample.Scenario == "static_text" {
			baselineBySample[sample.Sample] = sample.NanosecondsPerOp
		}
	}
	addedLatency := make([]float64, 0, len(baselineBySample))
	for _, sample := range samples {
		if sample.Framework != framework || sample.Scenario != "middleware_chain" {
			continue
		}
		baseline, ok := baselineBySample[sample.Sample]
		if !ok {
			continue
		}
		addedLatency = append(addedLatency, sample.NanosecondsPerOp-baseline)
	}
	return addedLatency
}

// median returns the midpoint of a sorted copy so callers retain their input order.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

// sortSnapshot gives JSON and SVG generation a stable scenario/framework/sample order.
func sortSnapshot(snapshot *benchmarkSnapshot) {
	sort.SliceStable(snapshot.Samples, func(left, right int) bool {
		a := snapshot.Samples[left]
		b := snapshot.Samples[right]
		if scenarioIndex(a.Scenario) != scenarioIndex(b.Scenario) {
			return scenarioIndex(a.Scenario) < scenarioIndex(b.Scenario)
		}
		if frameworkIndex(a.Framework) != frameworkIndex(b.Framework) {
			return frameworkIndex(a.Framework) < frameworkIndex(b.Framework)
		}
		return a.Sample < b.Sample
	})
	sortAggregates(snapshot.Aggregates)
}

// sortAggregates orders aggregate rows using the chart's fixed visual order.
func sortAggregates(aggregates []benchmarkAggregate) {
	sort.SliceStable(aggregates, func(left, right int) bool {
		a := aggregates[left]
		b := aggregates[right]
		if scenarioIndex(a.Scenario) != scenarioIndex(b.Scenario) {
			return scenarioIndex(a.Scenario) < scenarioIndex(b.Scenario)
		}
		return frameworkIndex(a.Framework) < frameworkIndex(b.Framework)
	})
}

// scenarioIndex returns the stable position of a benchmark scenario.
func scenarioIndex(scenario string) int {
	for index, candidate := range scenarioOrder {
		if candidate == scenario {
			return index
		}
	}
	return len(scenarioOrder)
}

// frameworkIndex returns the stable position of a benchmark framework.
func frameworkIndex(framework string) int {
	for index, candidate := range frameworkOrder {
		if candidate == framework {
			return index
		}
	}
	return len(frameworkOrder)
}

// validateSnapshot rejects partial or internally inconsistent comparisons.
func validateSnapshot(snapshot benchmarkSnapshot) error {
	if snapshot.SchemaVersion != snapshotSchemaVersion {
		return fmt.Errorf("benchmark snapshot schema is %d, want %d", snapshot.SchemaVersion, snapshotSchemaVersion)
	}
	metadata := snapshot.Metadata
	if metadata.GoVersion == "" || metadata.GOOS == "" || metadata.GOARCH == "" || metadata.CPU == "" || metadata.Kernel == "" {
		return errors.New("benchmark snapshot metadata is incomplete")
	}
	if err := validateBenchmarkInputFingerprint(metadata.BenchmarkInputFingerprint); err != nil {
		return err
	}
	if err := validateBenchmarkBuildSettings(metadata.GOARCH, metadata.BuildSettings); err != nil {
		return err
	}
	if metadata.GOMAXPROCS != 1 {
		return fmt.Errorf("benchmark snapshot GOMAXPROCS is %d, want 1", metadata.GOMAXPROCS)
	}
	if metadata.SampleCount < 1 || metadata.BenchmarkTime == "" {
		return errors.New("benchmark snapshot sampling metadata is incomplete")
	}

	counts := make(map[string]int)
	seenSamples := make(map[string]struct{})
	for _, sample := range snapshot.Samples {
		if scenarioIndex(sample.Scenario) == len(scenarioOrder) {
			return fmt.Errorf("benchmark snapshot contains unknown scenario %q", sample.Scenario)
		}
		if frameworkIndex(sample.Framework) == len(frameworkOrder) {
			return fmt.Errorf("benchmark snapshot contains unknown framework %q", sample.Framework)
		}
		if sample.Iterations <= 0 {
			return fmt.Errorf("benchmark sample %s/%s/%d has non-positive iterations", sample.Scenario, sample.Framework, sample.Sample)
		}
		if sample.NanosecondsPerOp <= 0 || sample.ThroughputPerSecond <= 0 || math.IsNaN(sample.NanosecondsPerOp) || math.IsInf(sample.NanosecondsPerOp, 0) || math.IsNaN(sample.ThroughputPerSecond) || math.IsInf(sample.ThroughputPerSecond, 0) {
			return fmt.Errorf("benchmark sample %s/%s/%d has non-positive timing", sample.Scenario, sample.Framework, sample.Sample)
		}
		if sample.BytesPerOp < 0 || sample.AllocsPerOp < 0 || math.IsNaN(sample.BytesPerOp) || math.IsInf(sample.BytesPerOp, 0) || math.IsNaN(sample.AllocsPerOp) || math.IsInf(sample.AllocsPerOp, 0) {
			return fmt.Errorf("benchmark sample %s/%s/%d has invalid allocation metrics", sample.Scenario, sample.Framework, sample.Sample)
		}
		if sample.Sample < 1 || sample.Sample > metadata.SampleCount {
			return fmt.Errorf("benchmark sample %s/%s has index %d outside 1..%d", sample.Scenario, sample.Framework, sample.Sample, metadata.SampleCount)
		}
		key := sample.Scenario + "\x00" + sample.Framework
		sampleKey := key + "\x00" + strconv.Itoa(sample.Sample)
		if _, exists := seenSamples[sampleKey]; exists {
			return fmt.Errorf("benchmark snapshot contains duplicate sample %s/%s/%d", sample.Scenario, sample.Framework, sample.Sample)
		}
		seenSamples[sampleKey] = struct{}{}
		counts[key]++
	}
	for _, scenario := range scenarioOrder {
		for _, framework := range frameworkOrder {
			count := counts[scenario+"\x00"+framework]
			if count != metadata.SampleCount {
				return fmt.Errorf("benchmark snapshot has %d samples for %s/%s, want %d", count, scenario, framework, metadata.SampleCount)
			}
		}
	}
	if len(snapshot.Aggregates) != len(scenarioOrder)*len(frameworkOrder) {
		return fmt.Errorf("benchmark snapshot has %d aggregate rows, want %d", len(snapshot.Aggregates), len(scenarioOrder)*len(frameworkOrder))
	}
	actualAggregates := append([]benchmarkAggregate(nil), snapshot.Aggregates...)
	sortAggregates(actualAggregates)
	expectedAggregates := aggregateSamples(snapshot.Samples)
	if !reflect.DeepEqual(actualAggregates, expectedAggregates) {
		return errors.New("benchmark snapshot aggregates do not match the recorded sample rows")
	}
	return nil
}

// encodeBenchmarkSnapshot formats the checked-in data with deterministic indentation.
func encodeBenchmarkSnapshot(snapshot benchmarkSnapshot) ([]byte, error) {
	sortSnapshot(&snapshot)
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode benchmark snapshot: %w", err)
	}
	return append(encoded, '\n'), nil
}

// loadBenchmarkSnapshot reads the authoritative rows used by render-only mode.
func loadBenchmarkSnapshot(path string) (benchmarkSnapshot, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return benchmarkSnapshot{}, fmt.Errorf("read benchmark snapshot: %w", err)
	}
	var snapshot benchmarkSnapshot
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return benchmarkSnapshot{}, fmt.Errorf("decode benchmark snapshot: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return benchmarkSnapshot{}, err
	}
	return snapshot, nil
}

// readDependencyVersions resolves the exact module versions used by the docs benchmark module.
func readDependencyVersions(root, goVersion string) ([]dependencyVersion, error) {
	command := exec.Command("go", "list", "-m", "-json", "all")
	command.Dir = filepath.Join(root, "docs")
	command.Env = environmentWith(os.Environ(), "GOWORK", "off")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list benchmark dependencies: %w", err)
	}
	modules := make(map[string]moduleRecord)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for decoder.More() {
		var module moduleRecord
		if err := decoder.Decode(&module); err != nil {
			return nil, fmt.Errorf("decode benchmark dependency: %w", err)
		}
		modules[module.Path] = module
	}

	dependencies := []dependencyVersion{{Name: "net/http", Module: "standard library", Version: goVersion}}
	for _, wanted := range dependencyModules {
		module, ok := modules[wanted.module]
		if !ok {
			return nil, fmt.Errorf("benchmark dependency %s is not in the module graph", wanted.module)
		}
		version := module.Version
		if module.Replace != nil {
			if module.Replace.Version != "" {
				version = module.Replace.Version
			} else {
				version = "local checkout"
			}
		}
		if version == "" {
			version = "local checkout"
		}
		dependencies = append(dependencies, dependencyVersion{Name: wanted.name, Module: wanted.module, Version: version})
	}
	return dependencies, nil
}

// readBenchmarkBuildSettings records compiler and runtime switches that can materially change benchmark results.
func readBenchmarkBuildSettings(root, goarch string) ([]benchmarkSetting, error) {
	names := requiredBenchmarkBuildSettingNames(goarch)
	arguments := append([]string{"env", "-json"}, names...)
	command := exec.Command("go", arguments...)
	command.Dir = filepath.Join(root, "docs")
	command.Env = environmentWith(os.Environ(), "GOWORK", "off")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read benchmark build settings: %w", err)
	}
	values := make(map[string]string, len(names))
	if err := json.Unmarshal(output, &values); err != nil {
		return nil, fmt.Errorf("decode benchmark build settings: %w", err)
	}
	settings := make([]benchmarkSetting, 0, len(names))
	for _, name := range names {
		value, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("go env omitted benchmark build setting %s", name)
		}
		settings = append(settings, benchmarkSetting{Name: name, Value: value})
	}
	return settings, nil
}

// requiredBenchmarkBuildSettingNames returns a stable, architecture-aware metadata key order.
func requiredBenchmarkBuildSettingNames(goarch string) []string {
	names := []string{"CGO_ENABLED", "GODEBUG", "GOEXPERIMENT", "GOFLAGS"}
	if tuning := architectureTuningVariable(goarch); tuning != "" {
		names = append(names, tuning)
	}
	sort.Strings(names)
	return names
}

// architectureTuningVariable identifies the Go build switch that changes code generation for an architecture.
func architectureTuningVariable(goarch string) string {
	switch goarch {
	case "386":
		return "GO386"
	case "amd64":
		return "GOAMD64"
	case "arm":
		return "GOARM"
	case "arm64":
		return "GOARM64"
	case "mips", "mipsle":
		return "GOMIPS"
	case "mips64", "mips64le":
		return "GOMIPS64"
	case "ppc64", "ppc64le":
		return "GOPPC64"
	case "riscv64":
		return "GORISCV64"
	case "wasm":
		return "GOWASM"
	default:
		return ""
	}
}

// validateBenchmarkBuildSettings rejects incomplete or ambiguous environment metadata.
func validateBenchmarkBuildSettings(goarch string, settings []benchmarkSetting) error {
	expected := requiredBenchmarkBuildSettingNames(goarch)
	if len(settings) != len(expected) {
		return fmt.Errorf("benchmark snapshot has %d build settings, want %d", len(settings), len(expected))
	}
	for index, name := range expected {
		if settings[index].Name != name {
			return fmt.Errorf("benchmark build setting %d is %q, want %q in stable order", index, settings[index].Name, name)
		}
	}
	return nil
}

// readKernelVersion records the host kernel used by the loopback measurement where the platform exposes it.
func readKernelVersion() string {
	command := exec.Command("uname", "-sr")
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return runtime.GOOS + " (kernel version unavailable)"
	}
	return strings.TrimSpace(string(output))
}

// benchmarkInputFingerprint hashes every local source and module pin that can change the measured handlers.
func benchmarkInputFingerprint(root, goos, goarch string, settings []benchmarkSetting) (string, error) {
	paths, err := benchmarkInputPaths(root, goos, goarch, settings)
	if err != nil {
		return "", err
	}
	return hashBenchmarkInputFiles(root, paths)
}

// hashBenchmarkInputFiles frames sorted paths and contents so names and file boundaries cannot collide.
func hashBenchmarkInputFiles(root string, paths []string) (string, error) {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	hash := sha256.New()
	_, _ = io.WriteString(hash, "goforj-web-benchmark-input-v1\x00")
	for _, path := range paths {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", fmt.Errorf("read benchmark input %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:", len(path), path, len(contents))
		_, _ = hash.Write(contents)
		_, _ = hash.Write([]byte{0})
	}
	return benchmarkInputHashPrefix + hex.EncodeToString(hash.Sum(nil)), nil
}

// benchmarkInputPaths inventories runtime packages, the benchmark harness, and both modules' dependency pins.
func benchmarkInputPaths(root, goos, goarch string, settings []benchmarkSetting) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve benchmark input root: %w", err)
	}
	packages, err := listBenchmarkRuntimePackages(root, goos, goarch, settings)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	for _, record := range packages {
		entries, err := os.ReadDir(record.Dir)
		if err != nil {
			return nil, fmt.Errorf("read benchmark runtime package %s: %w", record.ImportPath, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !isPackageSourceFile(entry.Name()) {
				continue
			}
			path, err := repositoryRelativePath(root, filepath.Join(record.Dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			paths[path] = struct{}{}
		}
		for _, embed := range record.EmbedFiles {
			path, err := repositoryRelativePath(root, filepath.Join(record.Dir, embed))
			if err != nil {
				return nil, err
			}
			paths[path] = struct{}{}
		}
	}
	for _, path := range []string{
		"go.mod",
		"go.sum",
		"docs/go.mod",
		"docs/go.sum",
		"docs/bench/bench_test.go",
		"docs/bench/doc.go",
		"docs/bench/render.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return nil, fmt.Errorf("stat benchmark input %s: %w", path, err)
		}
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	return ordered, nil
}

// listBenchmarkRuntimePackages discovers local GoForj packages compiled into the benchmark handlers.
func listBenchmarkRuntimePackages(root, goos, goarch string, settings []benchmarkSetting) ([]packageRecord, error) {
	command := exec.Command("go", "list", "-deps", "-test", "-json", "./bench")
	command.Dir = filepath.Join(root, "docs")
	command.Env = benchmarkToolEnvironment(os.Environ(), goos, goarch, settings)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list benchmark runtime packages: %w", err)
	}
	seen := make(map[string]packageRecord)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var record packageRecord
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode benchmark runtime package: %w", err)
		}
		if record.Module == nil || record.Module.Path != "github.com/goforj/web" || record.Dir == "" {
			continue
		}
		seen[record.Dir] = record
	}
	if len(seen) == 0 {
		return nil, errors.New("benchmark runtime package inventory is empty")
	}
	packages := make([]packageRecord, 0, len(seen))
	for _, record := range seen {
		packages = append(packages, record)
	}
	sort.Slice(packages, func(left, right int) bool {
		return packages[left].ImportPath < packages[right].ImportPath
	})
	return packages, nil
}

// benchmarkToolEnvironment recreates the recorded build selection without inheriting CI host tuning.
func benchmarkToolEnvironment(environment []string, goos, goarch string, settings []benchmarkSetting) []string {
	pairs := []string{"GOWORK", "off", "GOOS", goos, "GOARCH", goarch}
	for _, setting := range settings {
		pairs = append(pairs, setting.Name, setting.Value)
	}
	return environmentWith(environment, pairs...)
}

// isPackageSourceFile includes compiler inputs while excluding tests that do not run in the comparison.
func isPackageSourceFile(name string) bool {
	if strings.HasSuffix(name, "_test.go") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".s", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".f", ".for", ".f90", ".syso", ".swig", ".swigcxx":
		return true
	default:
		return false
	}
}

// repositoryRelativePath returns a portable path and rejects inputs outside the repository.
func repositoryRelativePath(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("make benchmark input path relative: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("benchmark input %s is outside repository root %s", path, root)
	}
	return filepath.ToSlash(relative), nil
}

// validateBenchmarkInputFingerprint requires a complete SHA-256 source identity.
func validateBenchmarkInputFingerprint(fingerprint string) error {
	digest := strings.TrimPrefix(fingerprint, benchmarkInputHashPrefix)
	if len(digest) != sha256.Size*2 || !strings.HasPrefix(fingerprint, benchmarkInputHashPrefix) {
		return fmt.Errorf("benchmark input fingerprint %q is not a SHA-256 digest", fingerprint)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("benchmark input fingerprint %q is not a SHA-256 digest", fingerprint)
	}
	return nil
}

// repositoryRevision identifies the source tree behind a measured snapshot.
func repositoryRevision(root string) (string, bool) {
	revisionCommand := exec.Command("git", "rev-parse", "--short=12", "HEAD")
	revisionCommand.Dir = root
	revisionOutput, err := revisionCommand.Output()
	if err != nil {
		return "unknown", true
	}
	statusCommand := exec.Command("git", "status", "--porcelain")
	statusCommand.Dir = root
	statusOutput, statusErr := statusCommand.Output()
	return strings.TrimSpace(string(revisionOutput)), statusErr != nil || len(bytes.TrimSpace(statusOutput)) > 0
}

// repositoryChangedDuringMeasurement rejects source or dirty-state transitions that could obscure benchmark provenance.
func repositoryChangedDuringMeasurement(startRevision string, startDirty bool, endRevision string, endDirty bool) bool {
	return startRevision != endRevision || startDirty != endDirty
}

// environmentWith replaces selected process environment variables without duplicates.
func environmentWith(environment []string, pairs ...string) []string {
	replacements := make(map[string]string, len(pairs)/2)
	for index := 0; index < len(pairs); index += 2 {
		replacements[pairs[index]] = pairs[index+1]
	}
	result := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replace := replacements[key]; replace {
				continue
			}
		}
		result = append(result, entry)
	}
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+replacements[key])
	}
	return result
}

// renderBenchmarkSVG builds a deterministic, self-contained comparison graphic.
func renderBenchmarkSVG(snapshot benchmarkSnapshot) ([]byte, error) {
	aggregates := make(map[string]benchmarkAggregate, len(snapshot.Aggregates))
	for _, aggregate := range snapshot.Aggregates {
		aggregates[aggregate.Scenario+"\x00"+aggregate.Framework] = aggregate
	}
	ranges := sampleThroughputRanges(snapshot.Samples)
	panels := []panelDefinition{
		{scenario: "live_plain_text", title: "HTTP/1.1 loopback", subtitle: "Single-core end-to-end requests/sec · reused keep-alive connection", live: true},
		{scenario: "static_text", title: "Plaintext route", subtitle: "In-process ServeHTTP operations/sec"},
		{scenario: "path_param_json", title: "Path parameter + JSON", subtitle: "In-process ServeHTTP operations/sec"},
		{scenario: "middleware_chain", title: "Middleware: state + header + validation", subtitle: "In-process ops/sec · paired added latency vs the same sample's plaintext route"},
	}

	metadata := snapshot.Metadata
	environmentItems := []string{"Kernel " + metadata.Kernel}
	for _, setting := range metadata.BuildSettings {
		environmentItems = append(environmentItems, formatBenchmarkSetting(setting))
	}
	environmentItems = append(environmentItems, "Inputs "+metadata.BenchmarkInputFingerprint)
	environmentLines := wrapFooterItems(environmentItems, 150)
	dependencyLines := wrapDependencyFooter(metadata.Dependencies, 150)
	footerLineCount := len(environmentLines) + len(dependencyLines)
	const width = 1440
	height := 1190
	if requiredHeight := 1146 + max(0, footerLineCount-1)*22; requiredHeight > height {
		height = requiredHeight
	}
	var svg strings.Builder
	svg.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title description">`, width, height, width, height)
	svg.WriteString("\n<title id=\"title\">Go HTTP stack performance comparison</title>\n")
	description := "Median HTTP/1.1 loopback requests per second and in-process handler operations per second, with observed sample ranges for GoForj Web, net/http, Echo, Gin, Chi, Gorilla Mux, and httprouter."
	if snapshot.Metadata.RepositoryDirty {
		description += " This is an unpublished local preview measured from a dirty working tree."
	}
	fmt.Fprintf(&svg, "<desc id=\"description\">%s</desc>\n", escapeSVG(description))
	fmt.Fprintf(&svg, `<rect width="%d" height="%d" rx="8" fill="#1A1620"/>`+"\n", width, height)
	svg.WriteString(`<text x="52" y="61" fill="#FFFFFF" font-family="Space Grotesk, Inter, system-ui, sans-serif" font-size="32" font-weight="700" letter-spacing="-1.2">Go HTTP stack performance</text>` + "\n")
	svg.WriteString(`<text x="52" y="94" fill="#A9A1B3" font-family="Inter, system-ui, sans-serif" font-size="16">Equivalent endpoint work using idiomatic APIs · medians with observed ranges · not a ranking</text>` + "\n")
	if snapshot.Metadata.RepositoryDirty {
		svg.WriteString(`<rect x="1040" y="35" width="348" height="36" rx="8" fill="#FF5E3A"/><text x="1214" y="58" fill="#08070A" text-anchor="middle" font-family="Inter, system-ui, sans-serif" font-size="13" font-weight="700">LOCAL DIRTY-TREE PREVIEW · DO NOT PUBLISH</text>` + "\n")
	}
	svg.WriteString(`<rect x="52" y="116" width="18" height="4" rx="2" fill="#FF5E3A"/><text x="80" y="123" fill="#A9A1B3" font-family="Inter, system-ui, sans-serif" font-size="13">GoForj Web</text>` + "\n")
	svg.WriteString(`<line x1="194" y1="118" x2="218" y2="118" stroke="#FFC24D" stroke-width="1.5"/><line x1="194" y1="114" x2="194" y2="122" stroke="#FFC24D" stroke-width="1.5"/><line x1="218" y1="114" x2="218" y2="122" stroke="#FFC24D" stroke-width="1.5"/><text x="230" y="123" fill="#A9A1B3" font-family="Inter, system-ui, sans-serif" font-size="13">Observed sample min–max</text>` + "\n")

	positions := [][2]int{{52, 154}, {730, 154}, {52, 584}, {730, 584}}
	for index, panel := range panels {
		position := positions[index]
		if err := renderPanel(&svg, position[0], position[1], 658, 402, panel, aggregates, ranges); err != nil {
			return nil, err
		}
	}

	revision := metadata.RepositoryRevision
	if metadata.RepositoryDirty {
		revision += " (dirty tree)"
	}
	footer := fmt.Sprintf("%s · %s/%s · %s · GOMAXPROCS=%d · median of %s · benchtime=%s · revision %s",
		metadata.GoVersion, metadata.GOOS, metadata.GOARCH, metadata.CPU, metadata.GOMAXPROCS, formatSampleCount(metadata.SampleCount), metadata.BenchmarkTime, revision)
	svg.WriteString(`<line x1="52" y1="1022" x2="1388" y2="1022" stroke="#2A2333"/>` + "\n")
	fmt.Fprintf(&svg, `<text x="52" y="1055" fill="#A9A1B3" font-family="Inter, system-ui, sans-serif" font-size="14">%s</text>`+"\n", escapeSVG(footer))
	svg.WriteString(`<text x="52" y="1083" fill="#746C80" font-family="Inter, system-ui, sans-serif" font-size="13">Loopback is HTTP/1.1 req/s; other panels are in-process ServeHTTP ops/s. Small differences and overlapping ranges are not rankings.</text>` + "\n")
	footerY := 1111
	for index, line := range environmentLines {
		prefix := ""
		if index == 0 {
			prefix = "Environment: "
		}
		fmt.Fprintf(&svg, `<text x="52" y="%d" fill="#746C80" font-family="JetBrains Mono, ui-monospace, monospace" font-size="13">%s</text>`+"\n", footerY+index*22, escapeSVG(prefix+line))
	}
	footerY += len(environmentLines) * 22
	for index, line := range dependencyLines {
		prefix := ""
		if index == 0 {
			prefix = "Dependencies: "
		}
		fmt.Fprintf(&svg, `<text x="52" y="%d" fill="#746C80" font-family="JetBrains Mono, ui-monospace, monospace" font-size="13">%s</text>`+"\n", footerY+index*22, escapeSVG(prefix+line))
	}
	svg.WriteString("</svg>\n")
	return []byte(svg.String()), nil
}

// sampleThroughputRanges returns the observed minimum and maximum throughput for each measured row.
func sampleThroughputRanges(samples []benchmarkSample) map[string]throughputRange {
	ranges := make(map[string]throughputRange)
	for _, sample := range samples {
		key := sample.Scenario + "\x00" + sample.Framework
		observed, exists := ranges[key]
		if !exists {
			ranges[key] = throughputRange{minimum: sample.ThroughputPerSecond, maximum: sample.ThroughputPerSecond}
			continue
		}
		observed.minimum = math.Min(observed.minimum, sample.ThroughputPerSecond)
		observed.maximum = math.Max(observed.maximum, sample.ThroughputPerSecond)
		ranges[key] = observed
	}
	return ranges
}

// renderPanel draws one throughput scenario using a scenario-local scale.
func renderPanel(svg *strings.Builder, x, y, width, height int, panel panelDefinition, aggregates map[string]benchmarkAggregate, ranges map[string]throughputRange) error {
	rows := make([]benchmarkAggregate, 0, len(frameworkOrder))
	maximum := float64(0)
	for _, framework := range frameworkOrder {
		key := panel.scenario + "\x00" + framework
		aggregate, ok := aggregates[key]
		if !ok {
			return fmt.Errorf("render benchmark SVG: missing aggregate for %s/%s", panel.scenario, framework)
		}
		rows = append(rows, aggregate)
		maximum = math.Max(maximum, aggregate.ThroughputPerSecondMedian)
		observed, rangeOK := ranges[key]
		if !rangeOK {
			return fmt.Errorf("render benchmark SVG: missing sample range for %s/%s", panel.scenario, framework)
		}
		maximum = math.Max(maximum, observed.maximum)
	}
	if maximum <= 0 {
		return fmt.Errorf("render benchmark SVG: %s has no positive throughput", panel.scenario)
	}

	fmt.Fprintf(svg, `<rect x="%d" y="%d" width="%d" height="%d" rx="8" fill="#1D1923" stroke="#3D3349"/>`+"\n", x, y, width, height)
	fmt.Fprintf(svg, `<text x="%d" y="%d" fill="#FFFFFF" font-family="Space Grotesk, Inter, system-ui, sans-serif" font-size="20" font-weight="700">%s</text>`+"\n", x+22, y+34, escapeSVG(panel.title))
	fmt.Fprintf(svg, `<text x="%d" y="%d" fill="#746C80" font-family="Inter, system-ui, sans-serif" font-size="12">%s</text>`+"\n", x+22, y+56, escapeSVG(panel.subtitle))
	for index, aggregate := range rows {
		rowY := y + 91 + index*43
		label := frameworkLabels[aggregate.Framework]
		color := "#746C80"
		if aggregate.Framework == "goforj_web" {
			color = "#FF5E3A"
		}
		barX := x + 133
		barWidth := int(math.Round((aggregate.ThroughputPerSecondMedian / maximum) * 270))
		if barWidth < 2 {
			barWidth = 2
		}
		fmt.Fprintf(svg, `<text x="%d" y="%d" fill="#A9A1B3" font-family="Inter, system-ui, sans-serif" font-size="13" font-weight="600">%s</text>`+"\n", x+22, rowY, escapeSVG(label))
		fmt.Fprintf(svg, `<rect x="%d" y="%d" width="270" height="9" rx="4.5" fill="#2C2734"/><rect x="%d" y="%d" width="%d" height="9" rx="4.5" fill="%s"/>`+"\n", barX, rowY-11, barX, rowY-11, barWidth, color)
		observed := ranges[panel.scenario+"\x00"+aggregate.Framework]
		minimumX := barX + int(math.Round((observed.minimum/maximum)*270))
		maximumX := barX + int(math.Round((observed.maximum/maximum)*270))
		whiskerColor := "#A9A1B3"
		if aggregate.Framework == "goforj_web" {
			whiskerColor = "#FFC24D"
		}
		fmt.Fprintf(svg, `<line data-sample-range="%s/%s" x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5" stroke-linecap="round"/><line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/><line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.5"/>`+"\n",
			escapeSVG(panel.scenario), escapeSVG(aggregate.Framework), minimumX, rowY-7, maximumX, rowY-7, whiskerColor,
			minimumX, rowY-11, minimumX, rowY-3, whiskerColor,
			maximumX, rowY-11, maximumX, rowY-3, whiskerColor)
		fmt.Fprintf(svg, `<text x="%d" y="%d" fill="#FFFFFF" text-anchor="end" font-family="JetBrains Mono, ui-monospace, monospace" font-size="13" font-weight="650">%s</text>`+"\n", x+width-22, rowY-2, escapeSVG(formatThroughput(aggregate.ThroughputPerSecondMedian, panel.live)))
		if !panel.live {
			detail := fmt.Sprintf("%s ns/op · %s B/op · %s allocs/op", formatNumber(aggregate.NanosecondsPerOpMedian), formatNumber(aggregate.BytesPerOpMedian), formatNumber(aggregate.AllocsPerOpMedian))
			if panel.scenario == "middleware_chain" {
				if aggregate.MiddlewareAddedLatencyMedian == nil {
					return fmt.Errorf("render benchmark SVG: missing paired middleware latency for %s", aggregate.Framework)
				}
				detail += " · " + formatAddedLatency(*aggregate.MiddlewareAddedLatencyMedian)
			}
			fmt.Fprintf(svg, `<text x="%d" y="%d" fill="#746C80" font-family="JetBrains Mono, ui-monospace, monospace" font-size="11">%s</text>`+"\n", x+133, rowY+11, escapeSVG(detail))
		}
	}
	return nil
}

// formatThroughput formats chart values while keeping their measurement unit explicit.
func formatThroughput(value float64, live bool) string {
	unit := "ops/s"
	if live {
		unit = "req/s"
	}
	switch {
	case value >= 1e6:
		return fmt.Sprintf("%.2fM %s", value/1e6, unit)
	case value >= 1e3:
		return fmt.Sprintf("%.1fk %s", value/1e3, unit)
	default:
		return fmt.Sprintf("%.0f %s", value, unit)
	}
}

// formatNumber formats integral metrics compactly and preserves meaningful decimals.
func formatNumber(value float64) string {
	if math.Abs(value-math.Round(value)) < 0.005 {
		return strconv.FormatInt(int64(math.Round(value)), 10)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

// formatAddedLatency describes middleware latency added above the stack's own plaintext route.
func formatAddedLatency(value float64) string {
	if value >= 0 {
		return "+" + formatNumber(value) + " ns added"
	}
	return "−" + formatNumber(math.Abs(value)) + " ns added"
}

// formatSampleCount keeps generated preview and publication prose grammatical.
func formatSampleCount(count int) string {
	unit := "samples"
	if count == 1 {
		unit = "sample"
	}
	return strconv.Itoa(count) + " " + unit
}

// escapeSVG protects generated text nodes from metadata and module values.
func escapeSVG(value string) string {
	return html.EscapeString(value)
}

// formatBenchmarkSetting makes unset values explicit instead of silently omitting material configuration.
func formatBenchmarkSetting(setting benchmarkSetting) string {
	value := setting.Value
	if value == "" {
		value = "(unset)"
	}
	return setting.Name + "=" + value
}

// wrapFooterItems keeps provenance metadata inside the fixed SVG width.
func wrapFooterItems(items []string, limit int) []string {
	var lines []string
	current := ""
	for _, item := range items {
		candidate := item
		if current != "" {
			candidate = current + " · " + item
		}
		if current != "" && len(candidate) > limit {
			lines = append(lines, current)
			current = item
			continue
		}
		current = candidate
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// wrapDependencyFooter keeps long module metadata inside the fixed SVG width.
func wrapDependencyFooter(dependencies []dependencyVersion, limit int) []string {
	items := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		items = append(items, dependency.Name+" "+dependency.Version)
	}
	return wrapFooterItems(items, limit)
}

// renderREADMEBlock describes exactly what the generated image measures and how to reproduce it.
func renderREADMEBlock(metadata benchmarkMetadata) []byte {
	revision := metadata.RepositoryRevision
	warning := ""
	if metadata.RepositoryDirty {
		revision += " (dirty tree)"
		warning = "> **Local benchmark preview:** This snapshot was measured from a dirty working tree and is not publication-ready.\n\n"
	}
	dependencies := make([]string, 0, len(metadata.Dependencies))
	for _, dependency := range metadata.Dependencies {
		dependencies = append(dependencies, dependency.Name+" "+dependency.Version)
	}
	settings := make([]string, 0, len(metadata.BuildSettings))
	for _, setting := range metadata.BuildSettings {
		settings = append(settings, "`"+formatBenchmarkSetting(setting)+"`")
	}
	block := fmt.Sprintf(`<p align="center">
  <img src="docs/bench/framework_comparison.svg" alt="Go HTTP stack loopback and in-process performance comparison">
</p>

%sWhiskers in every panel show the observed sample minimum and maximum. The first panel measures single-core HTTP/1.1 loopback requests per second over a reused connection. The other panels measure in-process `+"`ServeHTTP`"+` operations per second, and their allocation figures cover the complete route and handler dispatch. Middleware details show the median paired latency added above the plaintext route measured in the same benchmark process. Each primary value is the median of %s at `+"`%s`"+` with `+"`GOMAXPROCS=1`"+`.

Bars are scaled independently within each panel, and small differences should not be treated as rankings. These are microbenchmarks and loopback ceilings, not production capacity forecasts.

Measured with `+"`%s`"+` on `+"`%s/%s`"+` (%s), kernel `+"`%s`"+`, revision `+"`%s`"+`. Build settings: %s. Benchmark inputs: `+"`%s`"+`. Dependencies: %s.

Fiber is omitted because its `+"`fasthttp`"+` engine is not directly comparable in this shared `+"`net/http`"+` suite. See the [benchmark methodology](docs/bench/README.md) and [recorded sample rows](docs/bench/benchmarks_rows.json).

Regenerate the measurement and image with:

`+"```sh"+`
make bench-svg
`+"```"+``, warning, formatSampleCount(metadata.SampleCount), metadata.BenchmarkTime, metadata.GoVersion, metadata.GOOS, metadata.GOARCH, metadata.CPU, metadata.Kernel, revision, strings.Join(settings, ", "), metadata.BenchmarkInputFingerprint, strings.Join(dependencies, ", "))
	return []byte(block)
}

// replaceREADMEBlock updates only the content between the benchmark markers.
func replaceREADMEBlock(readme, block []byte) ([]byte, error) {
	start := bytes.Index(readme, []byte(readmeStartMarker))
	end := bytes.Index(readme, []byte(readmeEndMarker))
	if start < 0 || end < 0 {
		return nil, errors.New("README benchmark markers are missing")
	}
	if end < start {
		return nil, errors.New("README benchmark markers are out of order")
	}
	if bytes.Index(readme[start+len(readmeStartMarker):], []byte(readmeStartMarker)) >= 0 || bytes.Index(readme[end+len(readmeEndMarker):], []byte(readmeEndMarker)) >= 0 {
		return nil, errors.New("README benchmark markers must be unique")
	}
	var updated bytes.Buffer
	updated.Write(readme[:start+len(readmeStartMarker)])
	updated.WriteByte('\n')
	updated.Write(bytes.TrimSpace(block))
	updated.WriteByte('\n')
	updated.Write(readme[end:])
	return updated.Bytes(), nil
}

// writeFileIfChanged avoids rewriting deterministic artifacts whose bytes are unchanged.
func writeFileIfChanged(path string, contents []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, contents) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o644)
}
