package webindex

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

// unsetIndexCacheTestEnvironment lets a custom GOENV file supply values without process variables taking precedence.
func unsetIndexCacheTestEnvironment(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		value, present := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(name, value)
				return
			}
			_ = os.Unsetenv(name)
		})
	}
}

// indexCacheTestEnvironment projects an exec-style environment slice into the values asserted by focused tests.
func indexCacheTestEnvironment(entries []string) map[string]string {
	environment := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			environment[name] = value
		}
	}
	return environment
}

// TestIndexCachePathsEqual verifies cache destinations reject case-only aliases on every host and mount type.
func TestIndexCachePathsEqual(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "exact", left: "/build/API.json", right: "/build/API.json", want: true},
		{name: "case collision", left: "/build/API.json", right: "/BUILD/api.JSON", want: true},
		{name: "distinct", left: "/build/api.json", right: "/build/diagnostics.json", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := indexCachePathsEqual(test.left, test.right); got != test.want {
				t.Fatalf("indexCachePathsEqual(%q, %q) = %t, want %t", test.left, test.right, got, test.want)
			}
		})
	}
}

// TestValidateIndexCacheDestinationRejectsCaseOnlyAliases verifies cache data cannot replace artifacts, inputs, or the publication lock on case-folding filesystems.
func TestValidateIndexCacheDestinationRejectsCaseOnlyAliases(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	tests := []struct {
		name string
		path string
	}{
		{name: "artifact", path: filepath.Join(filepath.Dir(options.OutPath), strings.ToUpper(filepath.Base(options.OutPath)))},
		{name: "input", path: filepath.Join(root, "GO.MOD")},
		{name: "publication lock", path: filepath.Join(root, strings.ToUpper(ArtifactPublicationLockFilename))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newIndexCacheSession(context.Background(), root, options, test.path); err == nil {
				t.Fatalf("case-only cache destination alias %q was accepted", test.path)
			}
		})
	}
}

// TestAcquireArtifactPublicationLockDeduplicatesDirectoryAliases verifies one transaction never waits on its own physical lock file.
func TestAcquireArtifactPublicationLockDeduplicatesDirectoryAliases(t *testing.T) {
	parent := t.TempDir()
	realDirectory := filepath.Join(parent, "real")
	if err := os.Mkdir(realDirectory, 0o755); err != nil {
		t.Fatalf("create real artifact directory: %v", err)
	}
	aliasDirectory := filepath.Join(parent, "alias")
	if err := os.Symlink(realDirectory, aliasDirectory); err != nil {
		t.Skipf("create artifact directory alias: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock, err := AcquireArtifactPublicationLock(ctx, filepath.Join(realDirectory, "api.json"), filepath.Join(aliasDirectory, "cache.bin"))
	if err != nil {
		t.Fatalf("acquire aliased artifact directories: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release aliased artifact directories: %v", err)
	}
}

// TestRunCachedDoesNotPublishMissWhenSourceChangesDuringAnalysis verifies miss artifacts and cache entries share one validated source generation.
func TestRunCachedDoesNotPublishMissWhenSourceChangesDuringAnalysis(t *testing.T) {
	root, handlerPath := writeTypedSchemaFixture(t)
	artifactDirectory := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	options := indexCacheIntegrationOptions(root, artifactDirectory)
	loader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		loaded, err := packages.Load(config, patterns...)
		if err == nil {
			appendIndexCacheTestFile(t, handlerPath, "\n// The active build changed while typed analysis was in flight.\n")
		}
		return loaded, err
	}
	if _, err := run(context.Background(), options, cachePath, loader); !errors.Is(err, errIndexCacheInputsChanged) {
		t.Fatalf("run with concurrent source edit: %v", err)
	}
	for _, path := range []string{options.OutPath, options.DiagnosticsPath, options.OpenAPIPath, cachePath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source-race miss published %s: %v", filepath.Base(path), err)
		}
	}
}

// TestRunCachedColdMissUsesFingerprintOverlay verifies an ABA edit cannot mix initial syntax with transient package types.
func TestRunCachedColdMissUsesFingerprintOverlay(t *testing.T) {
	root, _ := writeTypedSchemaFixture(t)
	expectedOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "expected"))
	expected, err := Run(context.Background(), expectedOptions)
	if err != nil {
		t.Fatalf("run expected fingerprint generation: %v", err)
	}

	contractPath := filepath.Join(root, "contracts", "types.go")
	original, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read overlay contract fixture: %v", err)
	}
	transient := []byte(strings.Replace(string(original), "type TextAlias = string", "type TextAlias = int", 1))
	if bytes.Equal(transient, original) {
		t.Fatal("overlay contract fixture did not contain the expected alias")
	}
	loader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		if !bytes.Equal(config.Overlay[contractPath], original) {
			t.Fatalf("package loader overlay did not retain fingerprint bytes for %s", filepath.Base(contractPath))
		}
		if err := os.WriteFile(contractPath, transient, 0o644); err != nil {
			t.Fatalf("write transient package source: %v", err)
		}
		loaded, loadErr := packages.Load(config, patterns...)
		if err := os.WriteFile(contractPath, original, 0o644); err != nil {
			t.Fatalf("restore package source after transient edit: %v", err)
		}
		return loaded, loadErr
	}
	actualOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "actual"))
	actual, err := run(context.Background(), actualOptions, filepath.Join(t.TempDir(), "webindex.cache"), loader)
	if err != nil {
		t.Fatalf("run cache miss across transient edit: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("cache miss mixed transient package types with fingerprinted syntax")
	}
	actualArtifacts := readIndexCacheIntegrationArtifacts(t, actualOptions)
	expectedArtifacts := readIndexCacheIntegrationArtifacts(t, expectedOptions)
	for role, expectedData := range expectedArtifacts {
		if !bytes.Equal(actualArtifacts[role], expectedData) {
			t.Fatalf("cache miss mixed transient package types in %s artifact", role)
		}
	}
}

// TestRunCachedColdMissUsesModuleOverlay verifies dependency selection cannot cross module generations during focused package loading.
func TestRunCachedColdMissUsesModuleOverlay(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	stableModule := "module example.com/module-snapshot\n\ngo 1.25.0\n\nrequire (\n\texample.com/contracts v0.0.0\n\tgithub.com/goforj/web v0.0.0\n)\n\nreplace example.com/contracts => ./contracts-stable\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n"
	transientModule := strings.Replace(stableModule, "./contracts-stable", "./contracts-transient", 1)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":                         stableModule,
		"contracts-stable/go.mod":        "module example.com/contracts\n\ngo 1.25.0\n",
		"contracts-stable/payload.go":    "package contracts\ntype Payload struct { Stable string `json:\"stable\"` }\n",
		"contracts-transient/go.mod":     "module example.com/contracts\n\ngo 1.25.0\n",
		"contracts-transient/payload.go": "package contracts\ntype Payload struct { Transient int `json:\"transient\"` }\n",
		"handler/handler.go": `package handler

import (
	"net/http"
	"example.com/contracts"
	"github.com/goforj/web"
)

type Controller struct{}

func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/module-snapshot", c.Handle)}
}

func (c *Controller) Handle(ctx web.Context) error {
	return ctx.JSON(http.StatusOK, contracts.Payload{})
}
`,
	})
	expectedOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "expected"))
	expected, err := Run(context.Background(), expectedOptions)
	if err != nil {
		t.Fatalf("run expected module generation: %v", err)
	}

	modulePath := filepath.Join(root, "go.mod")
	loader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		if !bytes.Equal(config.Overlay[modulePath], []byte(stableModule)) {
			t.Fatal("package loader did not receive the fingerprinted main module")
		}
		if err := os.WriteFile(modulePath, []byte(transientModule), 0o644); err != nil {
			t.Fatalf("write transient module selection: %v", err)
		}
		loaded, loadErr := packages.Load(config, patterns...)
		if err := os.WriteFile(modulePath, []byte(stableModule), 0o644); err != nil {
			t.Fatalf("restore module selection: %v", err)
		}
		return loaded, loadErr
	}
	actualOptions := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "actual"))
	actual, err := run(context.Background(), actualOptions, filepath.Join(t.TempDir(), "webindex.cache"), loader)
	if err != nil {
		t.Fatalf("run cache miss across transient module edit: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("cache miss mixed transient dependency types with fingerprinted module selection")
	}
	for role, expectedData := range readIndexCacheIntegrationArtifacts(t, expectedOptions) {
		actualData := readIndexCacheIntegrationArtifacts(t, actualOptions)[role]
		if !bytes.Equal(actualData, expectedData) {
			t.Fatalf("cache miss mixed transient module selection in %s artifact", role)
		}
		if bytes.Contains(actualData, []byte("transient")) {
			t.Fatalf("%s artifact contains the transient dependency schema", role)
		}
	}
}

// TestRunCachedPinsEffectiveGoEnvironment verifies go env -w values drive syntax, package loading, and persisted type state as one snapshot.
func TestRunCachedPinsEffectiveGoEnvironment(t *testing.T) {
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/environment-snapshot\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"handler/handler_special.go": `//go:build special

package handler

import (
	"net/http"
	"github.com/goforj/web"
)

type Controller struct{}

func (c *Controller) Routes() []web.Route {
	return []web.Route{web.NewRoute(http.MethodGet, "/special", c.Handle)}
}

func (c *Controller) Handle(ctx web.Context) error {
	return ctx.JSON(http.StatusOK, struct { Value string ` + "`json:\"value\"`" + ` }{})
}
`,
	})
	goEnvironmentPath := filepath.Join(t.TempDir(), "go.env")
	stableEnvironment := []byte("GOARCH=386\nCGO_ENABLED=0\nGOFLAGS=-tags=special\n")
	transientEnvironment := []byte("GOARCH=amd64\nCGO_ENABLED=0\nGOFLAGS=\n")
	if err := os.WriteFile(goEnvironmentPath, stableEnvironment, 0o644); err != nil {
		t.Fatalf("write custom Go environment: %v", err)
	}
	unsetIndexCacheTestEnvironment(t, "GOARCH", "CGO_ENABLED", "GOFLAGS")
	t.Setenv("GOENV", goEnvironmentPath)
	t.Setenv("GOWORK", "off")

	loaderCalls := 0
	loader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		loaderCalls++
		environment := indexCacheTestEnvironment(config.Env)
		if environment["GOENV"] != "off" || environment["GOPACKAGESDRIVER"] != "off" || environment["GOWORK"] != "off" || environment["GOARCH"] != "386" || environment["CGO_ENABLED"] != "0" || environment["GOFLAGS"] != "-tags=special" {
			t.Fatalf("package loader environment was not pinned: GOENV=%q GOPACKAGESDRIVER=%q GOWORK=%q GOARCH=%q CGO_ENABLED=%q GOFLAGS=%q", environment["GOENV"], environment["GOPACKAGESDRIVER"], environment["GOWORK"], environment["GOARCH"], environment["CGO_ENABLED"], environment["GOFLAGS"])
		}
		if err := os.WriteFile(goEnvironmentPath, transientEnvironment, 0o644); err != nil {
			t.Fatalf("write transient Go environment: %v", err)
		}
		loaded, loadErr := packages.Load(config, patterns...)
		if err := os.WriteFile(goEnvironmentPath, stableEnvironment, 0o644); err != nil {
			t.Fatalf("restore Go environment: %v", err)
		}
		return loaded, loadErr
	}
	options := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "artifacts"))
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	manifest, err := run(context.Background(), options, cachePath, loader)
	if err != nil {
		t.Fatalf("run with custom Go environment: %v", err)
	}
	if loaderCalls != 1 {
		t.Fatalf("package loader calls = %d, want 1", loaderCalls)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Path != "/special" {
		t.Fatalf("tagged operations = %#v, want /special", manifest.Operations)
	}
	inputHash, cacheable, err := indexCacheInputHash(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint pinned environment: cacheable=%t err=%v", cacheable, err)
	}
	// Incremental state is intentionally decoded only after an exact source miss.
	record, ok := readDecodedIndexCacheRecord(cachePath, inputHash+"-source-edit")
	if !ok || record.TypedState == nil {
		t.Fatal("pinned environment run did not persist incremental type state")
	}
	if record.TypedState.GOARCH != "386" {
		t.Fatalf("persisted GOARCH = %q, want 386", record.TypedState.GOARCH)
	}
}

// TestIndexCacheSessionUsesConfigurationSnapshot verifies later module and APP_NAME edits cannot alter helper-derived generation metadata.
func TestIndexCacheSessionUsesConfigurationSnapshot(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	cache, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
	if err != nil || cache == nil {
		t.Fatalf("create configuration snapshot: cache=%#v err=%v", cache, err)
	}
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/transient\n\ngo 1.24.0\n",
		".env":   "APP_NAME=Transient Name\n",
	})
	if got := cacheModulePath(cache, root); got != "example.com/cacheinput" {
		t.Fatalf("snapshotted module path = %q, want example.com/cacheinput", got)
	}
	if got := cacheGoVersion(cache, root); got != "go1.25.0" {
		t.Fatalf("snapshotted Go version = %q, want go1.25.0", got)
	}
	if got := cacheOpenAPITitle(cache, root); got != "Cache Input" {
		t.Fatalf("snapshotted OpenAPI title = %q, want Cache Input", got)
	}
}

// TestSelectedCompositionParseDiagnosticsUsesSnapshotPresence verifies transient filesystem changes cannot demote a required composition parse failure.
func TestSelectedCompositionParseDiagnosticsUsesSnapshotPresence(t *testing.T) {
	root, options := writeIndexCacheInputFixture(t)
	cache, err := newIndexCacheSession(context.Background(), root, options, indexCachePathForTest(options))
	if err != nil || cache == nil {
		t.Fatalf("create composition snapshot: cache=%#v err=%v", cache, err)
	}
	compositionPath := filepath.Join(root, options.RouteCompositionPath)
	if err := os.Remove(compositionPath); err != nil {
		t.Fatalf("remove live composition after snapshot: %v", err)
	}
	diagnostics := []Diagnostic{{Severity: "warn", Code: "parse_error", File: filepath.ToSlash(options.RouteCompositionPath), Line: 3, Message: "expected declaration"}}
	selected := selectedCompositionParseDiagnostics(root, options.RouteCompositionPath, diagnostics, cache)
	if len(selected) != 1 || selected[0].Severity != "error" {
		t.Fatalf("selected composition diagnostics = %#v, want promoted snapshot diagnostic", selected)
	}
}

// TestRunCachedDoesNotPersistParseDiagnosticGeneration verifies metadata fast paths cannot make incomplete syntax reusable.
func TestRunCachedDoesNotPersistParseDiagnosticGeneration(t *testing.T) {
	root := t.TempDir()
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":    "module example.com/parse-diagnostic\n\ngo 1.25.0\n",
		"valid.go":  "package parsediagnostic\nconst Stable = true\n",
		"broken.go": "package parsediagnostic\nfunc (\n",
	})
	artifactPath := filepath.Join(t.TempDir(), "api_index.json")
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	manifest, err := RunCached(context.Background(), IndexOptions{Root: root, OutPath: artifactPath}, cachePath)
	if err != nil {
		t.Fatalf("run non-strict parse-diagnostic generation: %v", err)
	}
	foundParseDiagnostic := false
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Code == "parse_error" {
			foundParseDiagnostic = true
			break
		}
	}
	if !foundParseDiagnostic {
		t.Fatalf("manifest diagnostics = %#v, want parse_error", manifest.Diagnostics)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("parse-diagnostic generation did not publish its requested artifact: %v", err)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parse-diagnostic generation persisted a cache entry: %v", err)
	}
}

// TestRunCachedColdMissRejectsTransientPackageFile verifies a file absent from the fingerprint cannot enter focused package loading.
func TestRunCachedColdMissRejectsTransientPackageFile(t *testing.T) {
	root, handlerPath := writeTypedSchemaFixture(t)
	transientPath := filepath.Join(filepath.Dir(handlerPath), "transient.go")
	loader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		if err := os.WriteFile(transientPath, []byte("package handler\n\ntype transientContract struct { Value int }\n"), 0o644); err != nil {
			t.Fatalf("write transient package file: %v", err)
		}
		loaded, loadErr := packages.Load(config, patterns...)
		if err := os.Remove(transientPath); err != nil {
			t.Fatalf("remove transient package file: %v", err)
		}
		return loaded, loadErr
	}
	options := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "artifacts"))
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	if _, err := run(context.Background(), options, cachePath, loader); !errors.Is(err, errIndexCacheInputsChanged) {
		t.Fatalf("run cache miss with transient package file: %v", err)
	}
	for _, path := range []string{options.OutPath, options.DiagnosticsPath, options.OpenAPIPath, cachePath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transient package file published %s: %v", filepath.Base(path), err)
		}
	}
}

// TestRunCachedColdMissRejectsTransientNestedModule verifies an H1-H2-H1 module-boundary edit cannot publish types loaded from H2.
func TestRunCachedColdMissRejectsTransientNestedModule(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	webRoot := typedFixtureWebRoot(t)
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod": "module example.com/stable\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n",
		"handler/handler.go": `package handler

import (
	"net/http"

	"github.com/goforj/web"
)

type UUID [16]byte
type Controller struct{}
func (c *Controller) Routes() []web.Route { return []web.Route{web.NewRoute(http.MethodGet, "/value", c.Handle)} }
func (c *Controller) Handle(ctx web.Context) error { return ctx.JSON(200, UUID{}) }
`,
	})
	modulePath := filepath.Join(root, "handler", "go.mod")
	loader := func(config *packages.Config, patterns ...string) ([]*packages.Package, error) {
		transient := "module github.com/google/uuid\n\ngo 1.25.0\n\nrequire github.com/goforj/web v0.0.0\nreplace github.com/goforj/web => " + filepath.ToSlash(webRoot) + "\n"
		if err := os.WriteFile(modulePath, []byte(transient), 0o644); err != nil {
			t.Fatalf("write transient nested module: %v", err)
		}
		loaded, loadErr := packages.Load(config, patterns...)
		if err := os.Remove(modulePath); err != nil {
			t.Fatalf("remove transient nested module: %v", err)
		}
		return loaded, loadErr
	}
	options := indexCacheIntegrationOptions(root, filepath.Join(t.TempDir(), "artifacts"))
	cachePath := filepath.Join(t.TempDir(), "webindex.cache")
	if _, err := run(context.Background(), options, cachePath, loader); !errors.Is(err, errIndexCacheInputsChanged) {
		t.Fatalf("run cache miss across transient nested module: %v", err)
	}
	for _, path := range []string{options.OutPath, options.DiagnosticsPath, options.OpenAPIPath, cachePath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transient nested module published %s: %v", filepath.Base(path), err)
		}
	}
}

// TestValidateMissPublicationRejectsChangedInputs verifies a successful analysis cannot outlive the snapshot from which it started.
func TestValidateMissPublicationRejectsChangedInputs(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/cache-race\n\ngo 1.25.0\n",
		"main.go": "package cacherace\nconst Version = 1\n",
	})
	options := IndexOptions{Root: root}
	inputHash, cacheable, err := indexCacheInputHash(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint source-race fixture: cacheable=%t err=%v", cacheable, err)
	}
	cache := &indexCacheSession{root: root, inputHash: inputHash, options: options}
	appendIndexCacheTestFile(t, sourcePath, "const Changed = true\n")
	if err := cache.validatePublication(context.Background()); !errors.Is(err, errIndexCacheInputsChanged) {
		t.Fatalf("validate changed source snapshot: %v", err)
	}
}

// TestPublishIndexCacheArtifactsRevalidatesAfterLockWait verifies an older publisher cannot overwrite newer output after waiting behind its directory lock.
func TestPublishIndexCacheArtifactsRevalidatesAfterLockWait(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/locked-race\n\ngo 1.25.0\n",
		"main.go": "package lockedrace\nconst Version = 1\n",
	})
	options := IndexOptions{Root: root}
	inputHash, cacheable, err := indexCacheInputHash(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint lock-race fixture: cacheable=%t err=%v", cacheable, err)
	}
	cache := &indexCacheSession{root: root, inputHash: inputHash, options: options}
	artifactPath := filepath.Join(t.TempDir(), "api.json")
	artifacts := []encodedJSONArtifact{{path: artifactPath, data: []byte("{\"version\":1}\n")}}
	locks, err := acquireArtifactDirectoryLocks(context.Background(), artifacts)
	if err != nil {
		t.Fatalf("hold artifact lock: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, publishErr := publishIndexCacheArtifacts(context.Background(), cache, artifacts)
		result <- publishErr
	}()
	appendIndexCacheTestFile(t, sourcePath, "const VersionAfterWait = 2\n")
	if err := releaseArtifactDirectoryLocks(locks); err != nil {
		t.Fatalf("release artifact lock: %v", err)
	}
	if err := <-result; !errors.Is(err, errIndexCacheInputsChanged) {
		t.Fatalf("publish after source changed during lock wait: %v", err)
	}
	if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale waiter published artifact: %v", err)
	}
}

// TestPublishIndexCacheArtifactsRevalidatesAfterStaging verifies a source edit during candidate writes cannot publish stale output.
func TestPublishIndexCacheArtifactsRevalidatesAfterStaging(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.go")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/staging-race\n\ngo 1.25.0\n",
		"main.go": "package stagingrace\nconst Version = 1\n",
	})
	options := IndexOptions{Root: root}
	inputHash, cacheable, err := indexCacheInputHash(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint staging-race fixture: cacheable=%t err=%v", cacheable, err)
	}
	cache := &indexCacheSession{root: root, inputHash: inputHash, options: options}
	artifactDirectory := t.TempDir()
	artifactPath := filepath.Join(artifactDirectory, "api.json")
	artifacts := []encodedJSONArtifact{{path: artifactPath, data: []byte("{\"version\":1}\n")}}
	validations := 0
	validate := func(ctx context.Context) error {
		validations++
		candidates, globErr := filepath.Glob(filepath.Join(artifactDirectory, ".api.json.tmp-*"))
		if globErr != nil {
			t.Fatalf("find staged artifact: %v", globErr)
		}
		if len(candidates) != 1 {
			t.Fatalf("final validation ran before staging completed: candidates=%v", candidates)
		}
		appendIndexCacheTestFile(t, sourcePath, "const VersionDuringStaging = 2\n")
		return cache.validatePublication(ctx)
	}
	changed, err := publishIndexCacheArtifactsValidated(context.Background(), artifacts, os.Rename, validate)
	if !errors.Is(err, errIndexCacheInputsChanged) {
		t.Fatalf("publish after source changed during staging: changed=%t err=%v", changed, err)
	}
	if changed {
		t.Fatal("stale staged publication reported a visible change")
	}
	if validations != 1 {
		t.Fatalf("publication validations = %d, want 1", validations)
	}
	if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale staged publication created an artifact: %v", err)
	}
	assertNoArtifactCandidates(t, artifactDirectory)
}

// TestIndexCacheSourceEntryCoveredRejectsDirectorySymlinks verifies cache walking fails closed instead of silently omitting linked package trees.
func TestIndexCacheSourceEntryCoveredRejectsDirectorySymlinks(t *testing.T) {
	parent := t.TempDir()
	targetDirectory := filepath.Join(parent, "target")
	if err := os.Mkdir(targetDirectory, 0o755); err != nil {
		t.Fatalf("create symlink target directory: %v", err)
	}
	targetFile := filepath.Join(parent, "target.go")
	if err := os.WriteFile(targetFile, []byte("package target\n"), 0o644); err != nil {
		t.Fatalf("create symlink target file: %v", err)
	}
	if err := os.Symlink(targetDirectory, filepath.Join(parent, "linked-package")); err != nil {
		t.Skipf("create directory symlink fixture: %v", err)
	}
	if err := os.Symlink(targetFile, filepath.Join(parent, "linked.go")); err != nil {
		t.Skipf("create file symlink fixture: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read symlink fixture: %v", err)
	}
	covered := map[string]bool{}
	for _, entry := range entries {
		if entry.Name() != "linked-package" && entry.Name() != "linked.go" {
			continue
		}
		value, entryErr := indexCacheSourceEntryCovered(filepath.Join(parent, entry.Name()), entry)
		if entryErr != nil {
			t.Fatalf("inspect %s: %v", entry.Name(), entryErr)
		}
		covered[entry.Name()] = value
	}
	if covered["linked-package"] {
		t.Fatal("symlinked package directory was treated as covered by a non-following walk")
	}
	if !covered["linked.go"] {
		t.Fatal("symlinked source file was rejected even though hashing follows its bytes")
	}
}

// TestIndexCacheInputHashDisablesLinkedPackageTrees verifies a local import cannot remain cacheable when its package source is hidden behind a directory symlink.
func TestIndexCacheInputHashDisablesLinkedPackageTrees(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	linkedTarget := filepath.Join(parent, "contracts")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/linked-app\n\ngo 1.25.0\n",
		"main.go": "package linkedapp\nimport _ \"example.com/linked-app/contracts\"\n",
	})
	writeTypedFixtureFiles(t, linkedTarget, map[string]string{
		"contract.go": "package contracts\ntype Request struct { Name string }\n",
	})
	if err := os.Symlink(linkedTarget, filepath.Join(root, "contracts")); err != nil {
		t.Skipf("create linked package fixture: %v", err)
	}
	if _, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{Root: root}); err != nil || cacheable {
		t.Fatalf("linked source package cacheability = %t, %v; want disabled without error", cacheable, err)
	}
}

// TestIndexCacheInputHashIgnoresLinkedExcludedTrees verifies frontend dependency symlinks do not disable an otherwise complete source fingerprint.
func TestIndexCacheInputHashIgnoresLinkedExcludedTrees(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/ignored-link\n\ngo 1.25.0\n",
		"main.go": "package ignoredlink\n",
	})
	target := filepath.Join(parent, "frontend-dependencies")
	writeTypedFixtureFiles(t, target, map[string]string{"generated.go": "package generated\n"})
	if err := os.Symlink(target, filepath.Join(root, "node_modules")); err != nil {
		t.Skipf("create ignored directory symlink fixture: %v", err)
	}
	if _, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{Root: root}); err != nil || !cacheable {
		t.Fatalf("ignored linked tree cacheability = %t, %v; want enabled", cacheable, err)
	}
}

// TestIndexCacheInputHashDisablesExternalSourceGeneration verifies cgo and SWIG cannot reuse a fingerprint that omits external generator inputs.
func TestIndexCacheInputHashDisablesExternalSourceGeneration(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
	}{
		{name: "cgo", path: "main.go", content: "package external\nimport \"C\"\n"},
		{name: "SWIG", path: "contract.swig", content: "%module contract\n"},
		{name: "SWIG C++", path: "contract.swigcxx", content: "%module contract\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTypedFixtureFiles(t, root, map[string]string{
				"go.mod":  "module example.com/external\n\ngo 1.25.0\n",
				test.path: test.content,
			})
			if _, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{Root: root}); err != nil || cacheable {
				t.Fatalf("external source generation cacheability = %t, %v; want disabled without error", cacheable, err)
			}
		})
	}
}

// TestIndexCacheGoFlagsCoveredRejectsFileBackedInputs verifies stable flag strings cannot hide changing overlay or alternate-module bytes.
func TestIndexCacheGoFlagsCoveredRejectsFileBackedInputs(t *testing.T) {
	for _, goFlags := range []string{
		"-overlay overlay.json",
		"-overlay=overlay.json",
		"--overlay overlay.json",
		"--overlay=overlay.json",
		"-modfile alternate.mod",
		"-modfile=alternate.mod",
		"--modfile alternate.mod",
		"--modfile=alternate.mod",
		`-overlay="path with spaces.json"`,
	} {
		t.Run(goFlags, func(t *testing.T) {
			if indexCacheGoFlagsCovered(goFlags) {
				t.Fatalf("file-backed GOFLAGS %q remained cacheable", goFlags)
			}
		})
	}
	if !indexCacheGoFlagsCovered("-trimpath -tags=development") {
		t.Fatal("content-independent GOFLAGS unexpectedly disabled caching")
	}
}

// TestIndexCachePublicationContextGuards verifies optional cache state, nil contexts, and cancellation stop publication at deterministic boundaries.
func TestIndexCachePublicationContextGuards(t *testing.T) {
	var noCache *indexCacheSession
	if err := noCache.validatePublication(nil); err != nil {
		t.Fatalf("validate without cache: %v", err)
	}

	root := t.TempDir()
	writeTypedFixtureFiles(t, root, map[string]string{
		"go.mod":  "module example.com/publication-context\n\ngo 1.25.0\n",
		"main.go": "package publicationcontext\nconst Version = 1\n",
	})
	options := IndexOptions{Root: root}
	inputHash, cacheable, err := indexCacheInputHash(context.Background(), root, options)
	if err != nil || !cacheable {
		t.Fatalf("fingerprint publication-context fixture: cacheable=%t err=%v", cacheable, err)
	}
	cache := &indexCacheSession{root: root, inputHash: inputHash, options: options}
	if err := cache.validatePublication(nil); err != nil {
		t.Fatalf("validate current inputs with nil context: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cache.validatePublication(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("validate canceled cache: %v", err)
	}

	path := filepath.Join(t.TempDir(), "api.json")
	validateCalled := false
	changed, err := publishIndexCacheArtifactsValidated(ctx, []encodedJSONArtifact{{path: path, data: []byte("{}\n")}}, os.Rename, func(context.Context) error {
		validateCalled = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || changed {
		t.Fatalf("publish with canceled context: changed=%t err=%v", changed, err)
	}
	if validateCalled {
		t.Fatal("canceled publication reached final validation")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled publication created an artifact: %v", err)
	}

	duplicate := []encodedJSONArtifact{{path: path, data: []byte("one")}, {path: path, data: []byte("two")}}
	if changed, err := publishIndexCacheArtifactsValidated(context.Background(), duplicate, os.Rename, func(context.Context) error { return nil }); err == nil || changed {
		t.Fatalf("duplicate publication paths: changed=%t err=%v", changed, err)
	}
}

// TestIndexCacheSourceEntryCoveredRejectsDanglingSymlink verifies an unresolved link cannot be treated as a completely fingerprinted input.
func TestIndexCacheSourceEntryCoveredRejectsDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dangling.go")
	if err := os.Symlink(filepath.Join(root, "missing.go"), path); err != nil {
		t.Skipf("create dangling symlink: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read dangling symlink directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dangling symlink entries = %d, want 1", len(entries))
	}
	covered, err := indexCacheSourceEntryCovered(path, entries[0])
	if err == nil || covered {
		t.Fatalf("dangling source link: covered=%t err=%v", covered, err)
	}
}
