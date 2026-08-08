package webindex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/goforj/str"
	"golang.org/x/tools/go/packages"
)

// IndexOptions controls API index generation behavior.
type IndexOptions struct {
	Root                 string
	OutPath              string
	DiagnosticsPath      string
	OpenAPIPath          string
	OpenAPI              OpenAPIOptions
	RouteCompositionPath string
	// BuildTags selects the same conditional source for syntax discovery and focused Go type loading.
	BuildTags []string
	Strict    bool
	SkipDir   func(path string, name string) bool
}

// DiagnosticsError reports findings that prevent publishing a trustworthy index.
type DiagnosticsError struct {
	Diagnostics []Diagnostic
}

// Error summarizes the diagnostic failure without making callers parse individual messages.
func (e *DiagnosticsError) Error() string {
	count := len(e.Diagnostics)
	if count == 0 {
		return "API index validation failed"
	}
	first := e.Diagnostics[0]
	location := first.File
	if location != "" && first.Line > 0 {
		location += ":" + intToString(first.Line)
	} else if first.Line > 0 {
		location = "line " + intToString(first.Line)
	}
	if location != "" {
		location = " at " + location
	}
	noun := "diagnostics"
	if count == 1 {
		noun = "diagnostic"
	}
	return fmt.Sprintf("API index validation failed with %d %s; %s%s: %s", count, noun, first.Code, location, first.Message)
}

// parsedFile couples syntax with its repository-relative identity so diagnostics and import resolution remain reproducible across checkout roots.
type parsedFile struct {
	Path        string
	PackageName string
	File        *ast.File
}

// sourceParseResult retains each worker result at its walk-order position so concurrent parsing cannot perturb artifact order.
type sourceParseResult struct {
	parsed      *parsedFile
	diagnostics []Diagnostic
	tokenFile   *token.File
}

// sourceParseInput keeps immutable bytes and a deterministic token base together across parser workers.
type sourceParseInput struct {
	path   string
	data   []byte
	base   int
	active bool
}

// maxSourceParserWorkers uses cached immutable source snapshots in parallel while bounding uncached filesystem work on large hosts.
const maxSourceParserWorkers = 16

// Run indexes API metadata from source and writes artifacts.
// @group Indexing
// Example:
//
//	manifest, err := webindex.Run(context.Background(), webindex.IndexOptions{
//		Root:    ".",
//		OutPath: "webindex.json",
//	})
//
// fmt.Println(err == nil, manifest.Version != "")
//
//	// true true
func Run(ctx context.Context, opts IndexOptions) (Manifest, error) {
	return run(ctx, opts, "", nil)
}

// RunCached indexes API metadata while reusing a content-validated analysis cache at cachePath.
// Relative cache paths resolve from opts.Root. An empty path behaves like Run.
// When the active build cannot be fingerprinted safely, RunCached falls back to a full run without persisting state.
// @group Indexing
func RunCached(ctx context.Context, opts IndexOptions, cachePath string) (Manifest, error) {
	return runCachedWithRetry(ctx, opts, cachePath, nil)
}

// runCachedWithRetry restarts analysis once when a concurrent edit invalidates the source snapshot before publication.
func runCachedWithRetry(ctx context.Context, opts IndexOptions, cachePath string, loadPackages typedPackageLoader) (Manifest, error) {
	manifest, err := run(ctx, opts, cachePath, loadPackages)
	if !errors.Is(err, errIndexCacheInputsChanged) {
		return manifest, err
	}
	return run(ctx, opts, cachePath, loadPackages)
}

// run keeps cache selection and package-loading observation scoped to one invocation so parallel callers cannot affect each other.
func run(ctx context.Context, opts IndexOptions, cachePath string, loadPackages typedPackageLoader) (Manifest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err := sourceBuildEnvironmentError(os.Getenv("GOFLAGS")); err != nil {
		return Manifest{}, err
	}
	root := opts.Root
	if root == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Manifest{}, err
	}
	cache, err := newIndexCacheSession(ctx, root, opts, cachePath)
	if err != nil {
		return Manifest{}, err
	}
	if manifest, artifacts, hit := cache.load(ctx); hit {
		if invalid := publicationBlockingDiagnostics(manifest.Diagnostics, opts.Strict); len(invalid) > 0 {
			return manifest, &DiagnosticsError{Diagnostics: invalid}
		}
		if _, err := publishIndexCacheArtifacts(ctx, cache, artifacts); err != nil {
			return Manifest{}, fmt.Errorf("publish cached API index artifacts: %w", err)
		}
		return manifest, nil
	}

	parsed, fset, parseDiagnostics, err := parseGoFilesFromSnapshotWithEnvironment(ctx, root, opts.SkipDir, cacheSourceFiles(cache), cacheGoEnvironment(cache), opts.BuildTags...)
	if err != nil {
		return Manifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if selectedDiagnostics := selectedCompositionParseDiagnostics(root, opts.RouteCompositionPath, parseDiagnostics, cache); len(selectedDiagnostics) > 0 {
		manifest := Manifest{Version: ManifestVersion, Diagnostics: append([]Diagnostic(nil), parseDiagnostics...)}
		selectedByLocation := map[string]Diagnostic{}
		for _, diagnostic := range selectedDiagnostics {
			selectedByLocation[diagnostic.File+"|"+intToString(diagnostic.Line)+"|"+diagnostic.Message] = diagnostic
		}
		for index, diagnostic := range manifest.Diagnostics {
			key := diagnostic.File + "|" + intToString(diagnostic.Line) + "|" + diagnostic.Message
			if selected, exists := selectedByLocation[key]; exists {
				manifest.Diagnostics[index] = selected
			}
		}
		sortDiagnostics(manifest.Diagnostics)
		return manifest, &DiagnosticsError{Diagnostics: selectedDiagnostics}
	}

	scope, err := newRouteScopeWithModulePath(root, opts.RouteCompositionPath, parsed, cacheModulePath(cache, root))
	if err != nil {
		return Manifest{}, err
	}
	routes, handlers, prefixes, mapping := discoverRoutesAndHandlers(fset, parsed, scope)
	handlersByName := indexHandlersByName(handlers)
	handlerFiles := selectedHandlerFiles(routes, handlersByName)
	contractExpressions := selectedHandlerContractExpressions(routes, handlersByName, fset)
	sourceOverlay := cachePackageOverlay(cache)
	var typedSchemas *typedSchemaRegistry
	var typedState *typedSchemaIncrementalState
	if cache != nil && cache.priorTypedState != nil {
		incremental, handled, incrementalErr := loadTypedSchemaRegistryIncremental(ctx, typedSchemaIncrementalRequest{
			Root:                root,
			DependencyIdentity:  cache.dependencyIdentity,
			BuildTags:           opts.BuildTags,
			GoEnvironment:       cache.goEnvironment,
			GoVersion:           cacheGoVersion(cache, root),
			ParsedFiles:         parsed,
			FileSet:             fset,
			HandlerFiles:        handlerFiles,
			ContractExpressions: contractExpressions,
			SourceSnapshot:      sourceOverlay,
		}, cache.priorTypedState)
		if incrementalErr != nil {
			return Manifest{}, incrementalErr
		}
		if handled {
			typedSchemas = incremental.Registry
			typedState = incremental.State
		}
	}
	if typedSchemas == nil {
		var loadedPackages []*packages.Package
		typedSchemas, err = loadTypedSchemaRegistry(ctx, typedSchemaLoadOptions{
			Root:                root,
			HandlerFiles:        handlerFiles,
			ContractExpressions: contractExpressions,
			BuildTags:           opts.BuildTags,
			loadPackages:        loadPackages,
			capturePackages: func(loaded []*packages.Package) {
				loadedPackages = loaded
			},
			sourceOverlay: sourceOverlay,
			goEnvironment: cacheGoEnvironment(cache),
		})
		if err != nil {
			return Manifest{}, err
		}
		if cache != nil {
			moduleSnapshots, snapshotsValid := typedSchemaModuleSnapshotsFromInputs(root, cache.inputFiles)
			if !snapshotsValid || !typedSchemaLoadedPackagesMatchSnapshot(root, loadedPackages, sourceOverlay, moduleSnapshots) {
				return Manifest{}, errIndexCacheInputsChanged
			}
		}
		if cache != nil && len(loadedPackages) > 0 {
			capturedState, supported, captureErr := buildTypedSchemaIncrementalStateWithEnvironment(ctx, root, cache.dependencyIdentity, opts.BuildTags, loadedPackages, sourceOverlay, cache.goEnvironment, cacheGoVersion(cache, root))
			if captureErr != nil {
				return Manifest{}, captureErr
			}
			if supported {
				typedState = capturedState
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	ops, diagnostics := normalize(routes, handlersByName, prefixes, mapping, fset, typedSchemas)
	diagnostics = append(diagnostics, mapping.Diagnostics...)
	diagnostics = append(diagnostics, scope.diagnostics...)
	diagnostics = append(parseDiagnostics, diagnostics...)
	diagnostics = append(diagnostics, typedSchemas.diagnosticsSnapshot()...)
	diagnostics = append(diagnostics, duplicateOperationDiagnostics(ops)...)
	manifest := Manifest{
		Version:     ManifestVersion,
		Operations:  ops,
		Schemas:     collectSchemas(typedSchemas),
		Diagnostics: diagnostics,
	}
	relativizeManifestPaths(root, &manifest)
	sortDiagnostics(manifest.Diagnostics)
	if invalid := publicationBlockingDiagnostics(manifest.Diagnostics, opts.Strict); len(invalid) > 0 {
		return manifest, &DiagnosticsError{Diagnostics: invalid}
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}

	artifacts := []jsonArtifact{
		{path: opts.OutPath, value: manifest},
		{path: opts.DiagnosticsPath, value: manifest.Diagnostics},
	}
	if opts.OpenAPIPath != "" {
		openAPIOptions := opts.OpenAPI
		if strings.TrimSpace(openAPIOptions.Info.Title) == "" {
			openAPIOptions.Info.Title = cacheOpenAPITitle(cache, root)
		}
		document, projectionErr := ProjectOpenAPI(manifest, openAPIOptions)
		if projectionErr != nil {
			return manifest, projectionErr
		}
		artifacts = append(artifacts, jsonArtifact{path: opts.OpenAPIPath, value: document})
	}
	encodedArtifacts, err := encodeJSONArtifacts(artifacts)
	if err != nil {
		return Manifest{}, fmt.Errorf("publish API index artifacts: %w", err)
	}
	publicationArtifacts := encodedArtifacts
	// Parse-diagnostic generations stay uncached because source metadata intentionally skips lexing files that cannot contain imports or embed directives.
	if len(parseDiagnostics) == 0 {
		if cacheArtifact, ok := cache.encodedArtifact(manifest, encodedArtifacts, typedState); ok {
			publicationArtifacts = append(append([]encodedJSONArtifact(nil), encodedArtifacts...), cacheArtifact)
		}
	}
	if _, err := publishIndexCacheArtifacts(ctx, cache, publicationArtifacts); err != nil {
		return Manifest{}, fmt.Errorf("publish API index artifacts: %w", err)
	}

	return manifest, nil
}

// selectedCompositionParseDiagnostics promotes parse failures in the requested composition file because indexing cannot safely fall back to an unscoped API.
func selectedCompositionParseDiagnostics(root string, compositionPath string, diagnostics []Diagnostic, cache *indexCacheSession) []Diagnostic {
	if strings.TrimSpace(compositionPath) == "" {
		return nil
	}
	candidate := compositionPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if cache != nil {
		if !indexCacheInputFilePresent(cache.inputFiles, candidate) {
			return nil
		}
	} else {
		if _, err := os.Stat(candidate); err != nil {
			return nil
		}
	}
	relative := relativeSourcePath(root, candidate)
	selected := make([]Diagnostic, 0)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != "parse_error" || filepath.ToSlash(filepath.Clean(diagnostic.File)) != relative {
			continue
		}
		diagnostic.Severity = "error"
		selected = append(selected, diagnostic)
	}
	sortDiagnostics(selected)
	return selected
}

// parseGoFilesWithSet preserves the syntax-only helper used by focused tests.
func parseGoFilesWithSet(root string, skipDirs ...func(path string, name string) bool) ([]*parsedFile, *token.FileSet, error) {
	var skipDir func(path string, name string) bool
	if len(skipDirs) > 0 {
		skipDir = skipDirs[0]
	}
	parsed, fset, _, err := parseGoFiles(context.Background(), root, skipDir)
	return parsed, fset, err
}

// parseGoFiles records recoverable syntax failures instead of silently dropping source files from the index.
func parseGoFiles(ctx context.Context, root string, skipDir func(path string, name string) bool, buildTags ...string) ([]*parsedFile, *token.FileSet, []Diagnostic, error) {
	return parseGoFilesFromSnapshot(ctx, root, skipDir, nil, buildTags...)
}

// parseGoFilesFromSnapshot reuses bytes already read for the cache fingerprint so one miss observes a single source snapshot.
func parseGoFilesFromSnapshot(ctx context.Context, root string, skipDir func(path string, name string) bool, sourceFiles []indexCacheFileDigest, buildTags ...string) ([]*parsedFile, *token.FileSet, []Diagnostic, error) {
	return parseGoFilesFromSnapshotWithEnvironment(ctx, root, skipDir, sourceFiles, nil, buildTags...)
}

// parseGoFilesFromSnapshotWithEnvironment keeps source selection on the captured Go environment used by cache-backed package loading.
func parseGoFilesFromSnapshotWithEnvironment(ctx context.Context, root string, skipDir func(path string, name string) bool, sourceFiles []indexCacheFileDigest, environment map[string]string, buildTags ...string) ([]*parsedFile, *token.FileSet, []Diagnostic, error) {
	fset := token.NewFileSet()
	paths := make([]string, 0, 128)
	sourceData := make(map[string][]byte, len(sourceFiles))
	if sourceFiles == nil {
		err := walkSourceTree(ctx, root, skipDir, func(path string, d fs.DirEntry) error {
			base := d.Name()
			if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return nil, nil, nil, err
		}
	} else {
		for _, sourceFile := range sourceFiles {
			base := filepath.Base(sourceFile.path)
			if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
				continue
			}
			paths = append(paths, sourceFile.path)
			sourceData[sourceFile.path] = sourceFile.data
		}
	}
	results := parseSourcePaths(ctx, root, paths, fset, activeSourceBuildContextWithEnvironment(environment, buildTags...), sourceData)
	parsed := make([]*parsedFile, 0, len(results))
	diagnostics := make([]Diagnostic, 0)
	for _, result := range results {
		if result.parsed != nil {
			parsed = append(parsed, result.parsed)
		}
		diagnostics = append(diagnostics, result.diagnostics...)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	return parsed, fset, diagnostics, nil
}

// cacheSourceFiles returns the initial content snapshot only when persistence is active for this run.
func cacheSourceFiles(cache *indexCacheSession) []indexCacheFileDigest {
	if cache == nil {
		return nil
	}
	return cache.sourceFiles
}

// cacheGoEnvironment returns the immutable effective Go configuration only when persistence is active.
func cacheGoEnvironment(cache *indexCacheSession) map[string]string {
	if cache == nil {
		return nil
	}
	return cache.goEnvironment
}

// cachePackageOverlay binds focused package loading to the same bytes used for the initial cache fingerprint.
func cachePackageOverlay(cache *indexCacheSession) map[string][]byte {
	if cache == nil {
		return nil
	}
	overlay := make(map[string][]byte, len(cache.packageFiles)+len(cache.inputFiles))
	for _, sourceFile := range cache.packageFiles {
		if filepath.Ext(sourceFile.path) == ".go" {
			overlay[sourceFile.path] = sourceFile.data
		}
	}
	for _, input := range cache.inputFiles {
		name := filepath.Base(input.path)
		if name != "go.mod" && name != "go.sum" {
			continue
		}
		overlay[input.path] = input.data
	}
	return overlay
}

// cacheModulePath derives route identity from the same main-module bytes used by the cache fingerprint.
func cacheModulePath(cache *indexCacheSession, root string) string {
	if cache == nil {
		return modulePathFromRoot(root)
	}
	data, ok := indexCacheInputFileData(cache.inputFiles, filepath.Join(root, "go.mod"))
	if !ok {
		return ""
	}
	return modulePathFromData(data)
}

// cacheGoVersion derives go/types language semantics from the same main-module bytes used by the cache fingerprint.
func cacheGoVersion(cache *indexCacheSession, root string) string {
	if cache == nil {
		return typedSchemaIncrementalGoVersion(root)
	}
	data, ok := indexCacheInputFileData(cache.inputFiles, filepath.Join(root, "go.mod"))
	if !ok {
		return ""
	}
	return typedSchemaIncrementalGoVersionFromData(data)
}

// walkSourceTree applies the indexer's directory and nested-module boundaries once so parsing and cache validation see the same project surface.
func walkSourceTree(ctx context.Context, root string, skipDir func(path string, name string) bool, visit func(path string, entry fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if walkErr != nil {
			return walkErr
		}
		base := entry.Name()
		if entry.IsDir() {
			if path != root && skipDir != nil && skipDir(path, base) {
				return filepath.SkipDir
			}
			if path != root && sourceDirectoryIgnored(base) {
				return filepath.SkipDir
			}
			if path != root {
				_, moduleErr := os.Stat(filepath.Join(path, "go.mod"))
				if moduleErr == nil {
					return filepath.SkipDir
				}
				if !os.IsNotExist(moduleErr) {
					return fmt.Errorf("inspect nested module boundary %q: %w", path, moduleErr)
				}
			}
			return nil
		}
		return visit(path, entry)
	})
}

// parseSourcePaths bounds parallelism by available CPUs because parsing is CPU-heavy after the filesystem cache is warm.
func parseSourcePaths(ctx context.Context, root string, paths []string, fset *token.FileSet, buildContext build.Context, snapshots ...map[string][]byte) []sourceParseResult {
	results := make([]sourceParseResult, len(paths))
	inputs := make([]sourceParseInput, len(paths))
	var sourceData map[string][]byte
	if len(snapshots) > 0 {
		sourceData = snapshots[0]
	}
	workerCount := min(runtime.GOMAXPROCS(0), maxSourceParserWorkers, len(paths))
	if workerCount == 0 {
		return results
	}

	loadJobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range loadJobs {
				if ctx.Err() != nil {
					continue
				}
				path := paths[index]
				data, found := sourceData[path]
				matchContext := buildContext
				if found {
					matchContext.OpenFile = func(name string) (io.ReadCloser, error) {
						if filepath.Clean(name) != filepath.Clean(path) {
							return nil, os.ErrNotExist
						}
						return io.NopCloser(bytes.NewReader(data)), nil
					}
				}
				matchesBuild, matchErr := matchContext.MatchFile(filepath.Dir(path), filepath.Base(path))
				if matchErr != nil {
					results[index].diagnostics = []Diagnostic{{
						Severity: "warn",
						Code:     "build_constraint_error",
						Message:  matchErr.Error(),
						File:     relativeSourcePath(root, path),
					}}
					continue
				}
				if !matchesBuild {
					continue
				}
				if !found {
					var readErr error
					data, readErr = os.ReadFile(path)
					if readErr != nil {
						results[index].diagnostics = parseErrorDiagnostics(root, path, readErr)
						continue
					}
				}
				inputs[index] = sourceParseInput{path: path, data: data, active: true}
			}
		}()
	}
	for index := range paths {
		if ctx.Err() != nil {
			break
		}
		loadJobs <- index
	}
	close(loadJobs)
	workers.Wait()

	nextBase := 1
	for index := range inputs {
		if !inputs[index].active {
			continue
		}
		inputs[index].base = nextBase
		nextBase += len(inputs[index].data) + 1
	}

	parseJobs := make(chan int)
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range parseJobs {
				if ctx.Err() != nil || !inputs[index].active {
					continue
				}
				input := inputs[index]
				workerFileSet := token.NewFileSet()
				if input.base > 1 {
					workerFileSet.AddFile("", 1, input.base-2)
				}
				file, parseErr := parser.ParseFile(workerFileSet, input.path, input.data, parser.ParseComments)
				if parseErr != nil {
					results[index].diagnostics = parseErrorDiagnostics(root, input.path, parseErr)
					continue
				}
				results[index].parsed = &parsedFile{
					Path:        input.path,
					PackageName: file.Name.Name,
					File:        file,
				}
				results[index].tokenFile = workerFileSet.File(file.Pos())
			}
		}()
	}
	for index := range inputs {
		if ctx.Err() != nil {
			break
		}
		parseJobs <- index
	}
	close(parseJobs)
	workers.Wait()
	for _, result := range results {
		if result.tokenFile != nil {
			fset.AddExistingFiles(result.tokenFile)
		}
	}
	return results
}

// parseErrorDiagnostics retains each parser location while removing machine-specific root paths from output.
func parseErrorDiagnostics(root string, path string, parseErr error) []Diagnostic {
	relativePath := relativeSourcePath(root, path)
	var errorList scanner.ErrorList
	if errors.As(parseErr, &errorList) && len(errorList) > 0 {
		diagnostics := make([]Diagnostic, 0, len(errorList))
		for _, scanErr := range errorList {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: "warn",
				Code:     "parse_error",
				Message:  scanErr.Msg,
				File:     relativePath,
				Line:     scanErr.Pos.Line,
			})
		}
		return diagnostics
	}
	message := strings.TrimSpace(parseErr.Error())
	message = strings.ReplaceAll(filepath.ToSlash(message), filepath.ToSlash(filepath.Clean(root))+"/", "")
	return []Diagnostic{{
		Severity: "warn",
		Code:     "parse_error",
		Message:  message,
		File:     relativePath,
	}}
}

// publicationBlockingDiagnostics rejects hard errors in every mode and warnings when strict indexing is requested.
func publicationBlockingDiagnostics(diagnostics []Diagnostic, strict bool) []Diagnostic {
	blocked := make([]Diagnostic, 0)
	for _, diagnostic := range diagnostics {
		severity := strings.ToLower(strings.TrimSpace(diagnostic.Severity))
		if severity == "error" || strict && (severity == "warn" || severity == "warning") {
			blocked = append(blocked, diagnostic)
		}
	}
	return blocked
}

// normalizeMethodExpr accepts generated constants and literal spellings while returning one comparison form.
func normalizeMethodExpr(expr string) string {
	s := str.Of(expr).TrimSpace().String()
	if strings.HasPrefix(s, "http.Method") {
		s = strings.TrimPrefix(s, "http.Method")
		return str.Of(s).ToLower().String()
	}
	switch str.Of(s).ToUpper().String() {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD":
		return str.Of(s).ToLower().String()
	default:
		return str.Of(s).Trim(`"`).ToLower().String()
	}
}

// methodNameFromHandlerExpr supplies a conservative fallback when exact AST handler identity is unavailable.
func methodNameFromHandlerExpr(expr string) string {
	e := str.Of(expr).TrimSpace().String()
	parts := strings.Split(e, ".")
	return parts[len(parts)-1]
}

// typeNameFromExpr retains enough syntax identity for handler disambiguation and the no-types fallback path.
func typeNameFromExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.StarExpr:
		return strings.TrimPrefix(typeNameFromExpr(e.X), "*")
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		left := typeNameFromExpr(e.X)
		if left == "" {
			return e.Sel.Name
		}
		return left + "." + e.Sel.Name
	case *ast.IndexExpr:
		return typeNameFromExpr(e.X)
	case *ast.IndexListExpr:
		return typeNameFromExpr(e.X)
	case *ast.ArrayType:
		return "[]" + typeNameFromExpr(e.Elt)
	case *ast.MapType:
		return "map[" + typeNameFromExpr(e.Key) + "]" + typeNameFromExpr(e.Value)
	default:
		return exprString(expr)
	}
}

// exprString preserves author-facing evidence for diagnostics and exact middleware mappings.
func exprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var b strings.Builder
	_ = printer.Fprint(&b, token.NewFileSet(), expr)
	return b.String()
}

// intToString centralizes stable integer formatting used in diagnostic sort keys and messages.
func intToString(n int) string { return fmt.Sprintf("%d", n) }

// openAPITitleFromRoot retains legacy .env metadata when a caller does not supply explicit OpenAPI info.
func openAPITitleFromRoot(root string) string {
	if root == "" {
		return "Forj Generated API"
	}
	if name := appNameFromDotEnv(root); name != "" {
		return name
	}
	return "Forj Generated API"
}

// cacheOpenAPITitle prevents an APP_NAME edit during indexing from changing a generation formed from an earlier fingerprint.
func cacheOpenAPITitle(cache *indexCacheSession, root string) string {
	if cache == nil {
		return openAPITitleFromRoot(root)
	}
	data, present := indexCacheInputFileData(cache.inputFiles, filepath.Join(root, ".env"))
	if present {
		if name := appNameFromDotEnvData(data); name != "" {
			return name
		}
	}
	return "Forj Generated API"
}

// appNameFromDotEnv reads only APP_NAME so indexing never takes ownership of general runtime environment loading.
func appNameFromDotEnv(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return ""
	}
	return appNameFromDotEnvData(data)
}

// appNameFromDotEnvData reads only APP_NAME from immutable environment-file bytes.
func appNameFromDotEnvData(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "APP_NAME=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "APP_NAME="))
		value = strings.Trim(value, `"'`)
		if value == "" {
			return ""
		}
		return value
	}
	return ""
}
