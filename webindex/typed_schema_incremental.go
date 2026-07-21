package webindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/go/packages"
)

const (
	// typedSchemaIncrementalStateVersion invalidates private type artifacts whenever their validation or representation changes.
	typedSchemaIncrementalStateVersion = 1
	// typedSchemaIncrementalArtifactLimit prevents a corrupt cache record from feeding an unbounded package artifact to the decoder.
	typedSchemaIncrementalArtifactLimit = 16 << 20
)

// typedSchemaIncrementalState is the deterministic, serializable state needed to avoid package discovery on a warm changed run.
type typedSchemaIncrementalState struct {
	Version            int                                  `json:"version"`
	Root               string                               `json:"root"`
	DependencyIdentity string                               `json:"dependency_identity"`
	BuildTags          []string                             `json:"build_tags,omitempty"`
	GOARCH             string                               `json:"goarch"`
	GoVersion          string                               `json:"go_version,omitempty"`
	Packages           []typedSchemaIncrementalPackageState `json:"packages"`
}

// typedSchemaIncrementalPackageState stores one package export and the source evidence required to trust it.
type typedSchemaIncrementalPackageState struct {
	Path               string                                  `json:"path"`
	Name               string                                  `json:"name"`
	Selected           bool                                    `json:"selected,omitempty"`
	Local              bool                                    `json:"local,omitempty"`
	Imports            []typedSchemaIncrementalImportState     `json:"imports,omitempty"`
	Source             typedSchemaIncrementalSourceState       `json:"source,omitempty"`
	Artifact           []byte                                  `json:"artifact"`
	ExpressionPath     string                                  `json:"expression_path,omitempty"`
	ExpressionArtifact []byte                                  `json:"expression_artifact,omitempty"`
	Expressions        []typedSchemaIncrementalExpressionState `json:"expressions,omitempty"`
}

// typedSchemaIncrementalImportState retains the source spelling and canonical package target used by go/types imports.
type typedSchemaIncrementalImportState struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// typedSchemaIncrementalSourceState separates exact content changes from package-structure changes.
type typedSchemaIncrementalSourceState struct {
	Directory      string                              `json:"directory,omitempty"`
	Files          []typedSchemaIncrementalFileState   `json:"files,omitempty"`
	DirectoryFiles []typedSchemaIncrementalHeaderState `json:"directory_files,omitempty"`
}

// typedSchemaIncrementalFileState records one active compiled Go file.
type typedSchemaIncrementalFileState struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	HeaderHash  string `json:"header_hash"`
}

// typedSchemaIncrementalHeaderState detects additions, removals, imports, package clauses, and build-constraint changes.
type typedSchemaIncrementalHeaderState struct {
	Path       string `json:"path"`
	HeaderHash string `json:"header_hash"`
}

// typedSchemaIncrementalExpressionState maps a stable source range to one exported synthetic variable.
type typedSchemaIncrementalExpressionState struct {
	Name     string                               `json:"name"`
	Source   typedSourceRange                     `json:"source"`
	Constant *typedSchemaIncrementalConstantState `json:"constant,omitempty"`
}

// typedSchemaIncrementalConstantState retains only constant kinds used by literal-aware schema mapping.
type typedSchemaIncrementalConstantState struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// typedSchemaIncrementalRequest supplies current syntax and the dependency identity validated by the enclosing cache.
type typedSchemaIncrementalRequest struct {
	Root                string
	DependencyIdentity  string
	BuildTags           []string
	GoEnvironment       map[string]string
	GoVersion           string
	ParsedFiles         []*parsedFile
	FileSet             *token.FileSet
	HandlerFiles        []string
	ContractExpressions []typedSourceRange
	SourceSnapshot      map[string][]byte
}

// typedSchemaModuleSnapshot binds one immutable local module root to every import path that can select it.
type typedSchemaModuleSnapshot struct {
	directory string
	paths     []string
}

// typedSchemaIncrementalResult reports both the registry and refreshed cache state for instrumentation and persistence.
type typedSchemaIncrementalResult struct {
	Registry         *typedSchemaRegistry
	State            *typedSchemaIncrementalState
	CheckedPackages  []string
	RestoredPackages []string
}

// typedSchemaIncrementalUnsupported distinguishes safe cache misses from source type errors and cancellation.
type typedSchemaIncrementalUnsupported struct {
	reason string
}

// Error returns the internal reason used by focused tests and diagnostics during development.
func (e typedSchemaIncrementalUnsupported) Error() string {
	return e.reason
}

// typedSchemaIncrementalLoader owns one restore transaction so package identity remains consistent across decoded artifacts.
type typedSchemaIncrementalLoader struct {
	ctx             context.Context
	fset            *token.FileSet
	sizes           types.Sizes
	state           *typedSchemaIncrementalState
	packages        map[string]*typedSchemaIncrementalPackageState
	selected        map[string]bool
	changed         map[string]bool
	parsedByPackage map[string][]*parsedFile
	typesPackages   map[string]*types.Package
	loaded          map[string]*packages.Package
	loading         map[string]bool
}

// buildTypedSchemaIncrementalState captures a successful package load in a private format that can be validated independently of a manifest cache hit.
func buildTypedSchemaIncrementalState(ctx context.Context, root string, dependencyIdentity string, buildTags []string, loaded []*packages.Package, snapshots ...map[string][]byte) (*typedSchemaIncrementalState, bool, error) {
	var sourceSnapshot map[string][]byte
	if len(snapshots) > 0 {
		sourceSnapshot = snapshots[0]
	}
	return buildTypedSchemaIncrementalStateWithEnvironment(ctx, root, dependencyIdentity, buildTags, loaded, sourceSnapshot, nil, typedSchemaIncrementalGoVersion(root))
}

// buildTypedSchemaIncrementalStateWithEnvironment captures types against the immutable environment and module snapshot used for package loading.
func buildTypedSchemaIncrementalStateWithEnvironment(ctx context.Context, root string, dependencyIdentity string, buildTags []string, loaded []*packages.Package, sourceSnapshot map[string][]byte, environment map[string]string, goVersion string) (*typedSchemaIncrementalState, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, false, nil
	}
	absRoot = filepath.Clean(absRoot)
	if strings.TrimSpace(dependencyIdentity) == "" || len(loaded) == 0 {
		return nil, false, nil
	}

	rootPaths := make(map[string]struct{}, len(loaded))
	for _, pkg := range loaded {
		if pkg == nil || pkg.PkgPath == "" {
			return nil, false, nil
		}
		rootPaths[pkg.PkgPath] = struct{}{}
	}
	graph, ok := typedSchemaIncrementalPackageGraph(loaded)
	if !ok {
		return nil, false, nil
	}
	paths := make([]string, 0, len(graph))
	for path := range graph {
		if path != "unsafe" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	state := &typedSchemaIncrementalState{
		Version:            typedSchemaIncrementalStateVersion,
		Root:               absRoot,
		DependencyIdentity: dependencyIdentity,
		BuildTags:          normalizeSourceBuildTags(buildTags),
		GOARCH:             activeSourceBuildContextWithEnvironment(environment, buildTags...).GOARCH,
		GoVersion:          goVersion,
		Packages:           make([]typedSchemaIncrementalPackageState, 0, len(paths)),
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		pkg := graph[path]
		_, selected := rootPaths[path]
		entry, supported := buildTypedSchemaIncrementalPackageState(absRoot, pkg, selected, sourceSnapshot)
		if !supported {
			return nil, false, nil
		}
		state.Packages = append(state.Packages, entry)
	}
	return state, true, nil
}

// loadTypedSchemaRegistryIncremental restores unchanged selected packages and directly checks selected packages with body changes.
func loadTypedSchemaRegistryIncremental(ctx context.Context, request typedSchemaIncrementalRequest, prior *typedSchemaIncrementalState) (typedSchemaIncrementalResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return typedSchemaIncrementalResult{}, false, err
	}
	root, err := filepath.Abs(request.Root)
	if err != nil {
		return typedSchemaIncrementalResult{}, false, nil
	}
	root = filepath.Clean(root)
	if !typedSchemaIncrementalStateMatches(prior, root, request.DependencyIdentity, request.BuildTags, request.GoEnvironment, request.GoVersion) || request.FileSet == nil {
		return typedSchemaIncrementalResult{}, false, nil
	}
	sizes := types.SizesFor("gc", prior.GOARCH)
	if sizes == nil {
		return typedSchemaIncrementalResult{}, false, nil
	}

	entries := make(map[string]*typedSchemaIncrementalPackageState, len(prior.Packages))
	selectedByDirectory := map[string]string{}
	for index := range prior.Packages {
		entry := &prior.Packages[index]
		if !typedSchemaIncrementalPackageStateValid(entry) || entries[entry.Path] != nil {
			return typedSchemaIncrementalResult{}, false, nil
		}
		entries[entry.Path] = entry
		if entry.Selected {
			if entry.Source.Directory == "" || selectedByDirectory[entry.Source.Directory] != "" {
				return typedSchemaIncrementalResult{}, false, nil
			}
			selectedByDirectory[entry.Source.Directory] = entry.Path
		}
	}

	currentSelected := map[string]bool{}
	for _, handlerFile := range request.HandlerFiles {
		canonical, canonicalErr := canonicalSourceFile(handlerFile)
		if canonicalErr != nil {
			return typedSchemaIncrementalResult{}, false, nil
		}
		path := selectedByDirectory[filepath.Dir(canonical)]
		if path == "" {
			return typedSchemaIncrementalResult{}, false, nil
		}
		currentSelected[path] = true
	}
	if len(currentSelected) == 0 {
		return typedSchemaIncrementalResult{}, false, nil
	}

	parsedByDirectory := typedSchemaIncrementalParsedByDirectory(request.ParsedFiles)
	parsedByPackage := make(map[string][]*parsedFile, len(currentSelected))
	changed := make(map[string]bool, len(currentSelected))
	for path, entry := range entries {
		if err := ctx.Err(); err != nil {
			return typedSchemaIncrementalResult{}, false, err
		}
		if !entry.Local || currentSelected[path] {
			continue
		}
		if valid, contentChanged := typedSchemaIncrementalSourceMatches(entry.Source, nil, request.SourceSnapshot); !valid || contentChanged {
			return typedSchemaIncrementalResult{}, false, nil
		}
	}
	for path := range currentSelected {
		if err := ctx.Err(); err != nil {
			return typedSchemaIncrementalResult{}, false, err
		}
		entry := entries[path]
		files := parsedByDirectory[entry.Source.Directory]
		valid, contentChanged := typedSchemaIncrementalSourceMatches(entry.Source, files, request.SourceSnapshot)
		if !valid {
			return typedSchemaIncrementalResult{}, false, nil
		}
		parsedByPackage[path] = files
		changed[path] = contentChanged
	}
	if !typedSchemaIncrementalPropagateSelectedChanges(entries, currentSelected, changed) {
		return typedSchemaIncrementalResult{}, false, nil
	}

	loader := &typedSchemaIncrementalLoader{
		ctx:             ctx,
		fset:            request.FileSet,
		sizes:           sizes,
		state:           prior,
		packages:        entries,
		selected:        currentSelected,
		changed:         changed,
		parsedByPackage: parsedByPackage,
		typesPackages:   map[string]*types.Package{"unsafe": types.Unsafe},
		loaded:          map[string]*packages.Package{},
		loading:         map[string]bool{},
	}
	selection := newTypedSourceSelection(request.ContractExpressions)
	registry := &typedSchemaRegistry{
		root:           root,
		expressions:    map[string]typedExpression{},
		componentNames: map[string]string{},
		componentsByID: map[string]*typedSchemaComponent{},
		diagnosticKeys: map[string]struct{}{},
	}
	result := typedSchemaIncrementalResult{Registry: registry}
	selectedPaths := make([]string, 0, len(currentSelected))
	for path := range currentSelected {
		selectedPaths = append(selectedPaths, path)
	}
	sort.Strings(selectedPaths)
	for _, path := range selectedPaths {
		pkg, loadErr := loader.loadPackage(path)
		if loadErr != nil {
			if errors.Is(loadErr, context.Canceled) || errors.Is(loadErr, context.DeadlineExceeded) {
				return typedSchemaIncrementalResult{}, false, loadErr
			}
			return typedSchemaIncrementalResult{}, false, nil
		}
		if changed[path] {
			if len(pkg.Errors) != 0 {
				return typedSchemaIncrementalResult{}, false, nil
			}
			registry.recordPackageErrors(pkg)
			registry.indexPackageExpressions(pkg, selection)
			result.CheckedPackages = append(result.CheckedPackages, path)
			continue
		}
		if !restoreTypedSchemaIncrementalExpressions(registry, loader, entries[path], selection) {
			return typedSchemaIncrementalResult{}, false, nil
		}
		result.RestoredPackages = append(result.RestoredPackages, path)
	}
	componentSelection := selection
	if componentSelection == nil {
		ranges := make([]typedSourceRange, 0, len(registry.expressions))
		for _, expression := range registry.expressions {
			ranges = append(ranges, expression.Source)
		}
		componentSelection = newTypedSourceSelection(ranges)
	}
	registry.indexReachableComponents(nil, componentSelection)
	registry.sortDiagnostics()

	updated, updateOK := refreshTypedSchemaIncrementalState(prior, loader, result.CheckedPackages, request.SourceSnapshot)
	if updateOK {
		result.State = updated
	}
	return result, true, nil
}

// typedSchemaIncrementalPackageStateValid rejects malformed records before an artifact decoder or importer consumes them.
func typedSchemaIncrementalPackageStateValid(entry *typedSchemaIncrementalPackageState) bool {
	if entry == nil || entry.Path == "" || entry.Path == "unsafe" || entry.Path == "C" || strings.IndexByte(entry.Path, 0) >= 0 || !token.IsIdentifier(entry.Name) {
		return false
	}
	if len(entry.Artifact) == 0 || len(entry.Artifact) > typedSchemaIncrementalArtifactLimit || entry.Selected && !entry.Local {
		return false
	}
	previousImport := ""
	for _, imported := range entry.Imports {
		if imported.Path == "" || imported.Target == "" || strings.IndexByte(imported.Path, 0) >= 0 || strings.IndexByte(imported.Target, 0) >= 0 || previousImport >= imported.Path {
			return false
		}
		previousImport = imported.Path
	}
	if entry.Local && (entry.Source.Directory == "" || len(entry.Source.Files) == 0) {
		return false
	}
	if len(entry.Expressions) == 0 {
		return entry.ExpressionPath == "" && len(entry.ExpressionArtifact) == 0
	}
	if !entry.Selected || entry.ExpressionPath != typedSchemaIncrementalExpressionPackagePath(entry.Path) || len(entry.ExpressionArtifact) == 0 || len(entry.ExpressionArtifact) > typedSchemaIncrementalArtifactLimit {
		return false
	}
	seenSources := make(map[string]struct{}, len(entry.Expressions))
	for index, expression := range entry.Expressions {
		if expression.Name != fmt.Sprintf("E%06d", index) || expression.Source.File == "" || expression.Source.StartOffset < 0 || expression.Source.EndOffset <= expression.Source.StartOffset {
			return false
		}
		key := typedSourceKey(expression.Source)
		if _, exists := seenSources[key]; exists {
			return false
		}
		seenSources[key] = struct{}{}
	}
	return true
}

// typedSchemaIncrementalStateMatches validates stable configuration before inspecting package source.
func typedSchemaIncrementalStateMatches(state *typedSchemaIncrementalState, root string, dependencyIdentity string, buildTags []string, environment map[string]string, goVersion string) bool {
	if state == nil || state.Version != typedSchemaIncrementalStateVersion || state.Root != root || state.DependencyIdentity != dependencyIdentity {
		return false
	}
	if state.GOARCH != activeSourceBuildContextWithEnvironment(environment, buildTags...).GOARCH || state.GoVersion != goVersion {
		return false
	}
	return typedSchemaIncrementalStringSlicesEqual(state.BuildTags, normalizeSourceBuildTags(buildTags))
}

// typedSchemaIncrementalPackageGraph traverses the complete import graph while rejecting ambiguous package identities.
func typedSchemaIncrementalPackageGraph(roots []*packages.Package) (map[string]*packages.Package, bool) {
	graph := map[string]*packages.Package{}
	visiting := map[*packages.Package]bool{}
	var visit func(*packages.Package) bool
	visit = func(pkg *packages.Package) bool {
		if pkg == nil || pkg.PkgPath == "" || pkg.PkgPath == "C" || pkg.Types == nil {
			return false
		}
		if existing := graph[pkg.PkgPath]; existing != nil {
			return existing.Types == pkg.Types || existing.ID == pkg.ID
		}
		if visiting[pkg] {
			return false
		}
		visiting[pkg] = true
		defer delete(visiting, pkg)
		graph[pkg.PkgPath] = pkg
		for _, imported := range pkg.Imports {
			if !visit(imported) {
				return false
			}
		}
		return true
	}
	for _, root := range roots {
		if !visit(root) {
			return nil, false
		}
	}
	return graph, true
}

// typedSchemaModuleSnapshotsFromInputs reconstructs local module identities solely from captured configuration bytes.
func typedSchemaModuleSnapshotsFromInputs(root string, inputs []indexCacheInputFile) ([]typedSchemaModuleSnapshot, bool) {
	moduleData, ok := indexCacheInputFileData(inputs, filepath.Join(root, "go.mod"))
	if !ok {
		return nil, false
	}
	moduleFile, err := modfile.Parse(filepath.Join(root, "go.mod"), moduleData, nil)
	if err != nil {
		return nil, false
	}
	modules, ok := indexCacheLocalModules(root, moduleFile, inputs)
	if !ok {
		return nil, false
	}
	pathsByDirectory := make(map[string][]string, len(modules))
	for _, module := range modules {
		canonical, err := canonicalIndexCachePath(module.root)
		if err != nil {
			return nil, false
		}
		pathsByDirectory[canonical] = append(pathsByDirectory[canonical], module.importPath)
	}
	directories := make([]string, 0, len(pathsByDirectory))
	for directory := range pathsByDirectory {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	snapshots := make([]typedSchemaModuleSnapshot, 0, len(directories))
	for _, directory := range directories {
		paths := sortedUniqueIndexCacheStrings(pathsByDirectory[directory])
		snapshots = append(snapshots, typedSchemaModuleSnapshot{directory: directory, paths: paths})
	}
	return snapshots, len(snapshots) != 0
}

// typedSchemaLoadedPackagesMatchSnapshot rejects package files or module identities that were not part of the immutable cache fingerprint.
func typedSchemaLoadedPackagesMatchSnapshot(root string, loaded []*packages.Package, sourceSnapshot map[string][]byte, moduleSnapshots []typedSchemaModuleSnapshot) bool {
	if sourceSnapshot == nil {
		return true
	}
	selected := make(map[*packages.Package]bool, len(loaded))
	for _, pkg := range loaded {
		selected[pkg] = true
	}
	seen := map[*packages.Package]bool{}
	var visit func(*packages.Package) bool
	visit = func(pkg *packages.Package) bool {
		if pkg == nil {
			return false
		}
		if seen[pkg] {
			return true
		}
		seen[pkg] = true
		local := typedSchemaIncrementalPackageIsLocal(root, pkg)
		if selected[pkg] && len(pkg.CompiledGoFiles) == 0 {
			return false
		}
		if local || selected[pkg] {
			if !typedSchemaLoadedPackageModuleMatchesSnapshot(pkg, moduleSnapshots) {
				return false
			}
			if len(pkg.CompiledGoFiles) == 0 {
				return false
			}
			for _, name := range pkg.CompiledGoFiles {
				if _, exists := sourceSnapshot[filepath.Clean(name)]; !exists {
					return false
				}
			}
		}
		for _, imported := range pkg.Imports {
			if !visit(imported) {
				return false
			}
		}
		return true
	}
	for _, pkg := range loaded {
		if !visit(pkg) {
			return false
		}
	}
	return true
}

// typedSchemaLoadedPackageModuleMatchesSnapshot verifies the Go driver selected an initially captured main or path-replaced module.
func typedSchemaLoadedPackageModuleMatchesSnapshot(pkg *packages.Package, snapshots []typedSchemaModuleSnapshot) bool {
	if pkg == nil || pkg.Module == nil || pkg.Module.Path == "" {
		return false
	}
	directory := pkg.Module.Dir
	if !pkg.Module.Main {
		if pkg.Module.Replace == nil || pkg.Module.Replace.Version != "" || pkg.Module.Replace.Dir == "" {
			return false
		}
		directory = pkg.Module.Replace.Dir
	}
	if strings.TrimSpace(directory) == "" {
		return false
	}
	canonical, err := canonicalIndexCachePath(directory)
	if err != nil {
		return false
	}
	for _, snapshot := range snapshots {
		if !indexCachePathsEqual(canonical, snapshot.directory) {
			continue
		}
		for _, path := range snapshot.paths {
			if path == pkg.Module.Path {
				return true
			}
		}
	}
	return false
}

// buildTypedSchemaIncrementalPackageState serializes one checked package and its deterministic import graph edge list.
func buildTypedSchemaIncrementalPackageState(root string, pkg *packages.Package, selected bool, snapshots ...map[string][]byte) (typedSchemaIncrementalPackageState, bool) {
	if pkg == nil || pkg.Types == nil || pkg.PkgPath == "" || pkg.Name == "" || len(pkg.Errors) != 0 {
		return typedSchemaIncrementalPackageState{}, false
	}
	artifact, ok := writeTypedSchemaIncrementalArtifact(pkg.Fset, pkg.Types)
	if !ok {
		return typedSchemaIncrementalPackageState{}, false
	}
	entry := typedSchemaIncrementalPackageState{
		Path:     pkg.PkgPath,
		Name:     pkg.Name,
		Selected: selected,
		Local:    typedSchemaIncrementalPackageIsLocal(root, pkg),
		Artifact: artifact,
	}
	importPaths := make([]string, 0, len(pkg.Imports))
	for importPath := range pkg.Imports {
		importPaths = append(importPaths, importPath)
	}
	sort.Strings(importPaths)
	for _, importPath := range importPaths {
		imported := pkg.Imports[importPath]
		if imported == nil || imported.PkgPath == "" || imported.PkgPath == "C" {
			return typedSchemaIncrementalPackageState{}, false
		}
		entry.Imports = append(entry.Imports, typedSchemaIncrementalImportState{Path: importPath, Target: imported.PkgPath})
	}
	if entry.Local || selected {
		var sourceSnapshot map[string][]byte
		if len(snapshots) > 0 {
			sourceSnapshot = snapshots[0]
		}
		source, sourceOK := buildTypedSchemaIncrementalSourceState(pkg.CompiledGoFiles, sourceSnapshot)
		if !sourceOK {
			return typedSchemaIncrementalPackageState{}, false
		}
		entry.Source = source
	}
	if selected {
		expressionPath, expressionArtifact, expressions, expressionOK := buildTypedSchemaIncrementalExpressions(pkg)
		if !expressionOK {
			return typedSchemaIncrementalPackageState{}, false
		}
		entry.ExpressionPath = expressionPath
		entry.ExpressionArtifact = expressionArtifact
		entry.Expressions = expressions
	}
	return entry, true
}

// typedSchemaIncrementalPackageIsLocal identifies main and path-replaced modules whose source may change without a version update.
func typedSchemaIncrementalPackageIsLocal(root string, pkg *packages.Package) bool {
	if pkg == nil {
		return false
	}
	if pkg.Module != nil {
		if pkg.Module.Main {
			return true
		}
		if pkg.Module.Replace != nil && pkg.Module.Replace.Version == "" && pkg.Module.Replace.Dir != "" {
			return true
		}
	}
	for _, file := range pkg.CompiledGoFiles {
		if typedSchemaIncrementalPathWithin(root, file) {
			return true
		}
	}
	return false
}

// typedSchemaIncrementalPathWithin reports containment without relying on string-prefix path comparisons.
func typedSchemaIncrementalPathWithin(root string, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// buildTypedSchemaIncrementalSourceState records active file contents and all package-directory headers.
func buildTypedSchemaIncrementalSourceState(compiledFiles []string, snapshots ...map[string][]byte) (typedSchemaIncrementalSourceState, bool) {
	if len(compiledFiles) == 0 {
		return typedSchemaIncrementalSourceState{}, false
	}
	paths := make([]string, 0, len(compiledFiles))
	directory := ""
	for _, name := range compiledFiles {
		absolute, err := filepath.Abs(name)
		if err != nil || filepath.Ext(absolute) != ".go" {
			return typedSchemaIncrementalSourceState{}, false
		}
		absolute = filepath.Clean(absolute)
		if directory == "" {
			directory = filepath.Dir(absolute)
		} else if filepath.Dir(absolute) != directory {
			return typedSchemaIncrementalSourceState{}, false
		}
		paths = append(paths, absolute)
	}
	sort.Strings(paths)
	var sourceSnapshot map[string][]byte
	if len(snapshots) > 0 {
		sourceSnapshot = snapshots[0]
	}
	state := typedSchemaIncrementalSourceState{Directory: directory}
	for _, name := range paths {
		file, ok, _ := readTypedSchemaIncrementalFileStateFromSnapshot(name, sourceSnapshot)
		if !ok {
			return typedSchemaIncrementalSourceState{}, false
		}
		state.Files = append(state.Files, file)
	}
	headers, ok := readTypedSchemaIncrementalDirectoryHeadersFromSnapshot(directory, sourceSnapshot)
	if !ok {
		return typedSchemaIncrementalSourceState{}, false
	}
	state.DirectoryFiles = headers
	return state, true
}

// readTypedSchemaIncrementalFileState hashes exact bytes and the package-discovery header from one Go source file.
func readTypedSchemaIncrementalFileState(name string) (typedSchemaIncrementalFileState, bool, bool) {
	return readTypedSchemaIncrementalFileStateFromSnapshot(name, nil)
}

// readTypedSchemaIncrementalFileStateFromSnapshot uses captured bytes when a cold cache miss must remain generation-consistent.
func readTypedSchemaIncrementalFileStateFromSnapshot(name string, sourceSnapshot map[string][]byte) (typedSchemaIncrementalFileState, bool, bool) {
	canonical := filepath.Clean(name)
	data, exists := sourceSnapshot[canonical]
	if sourceSnapshot != nil && !exists {
		return typedSchemaIncrementalFileState{}, false, false
	}
	if sourceSnapshot == nil {
		var err error
		data, err = os.ReadFile(canonical)
		if err != nil {
			return typedSchemaIncrementalFileState{}, false, false
		}
	}
	headerHash, importsC, ok := typedSchemaIncrementalHeaderHash(name, data)
	if !ok || importsC {
		return typedSchemaIncrementalFileState{}, false, importsC
	}
	content := sha256.Sum256(data)
	return typedSchemaIncrementalFileState{
		Path:        canonical,
		ContentHash: hex.EncodeToString(content[:]),
		HeaderHash:  headerHash,
	}, true, false
}

// typedSchemaIncrementalHeaderHash fingerprints only inputs that can change package discovery or imports.
func typedSchemaIncrementalHeaderHash(name string, data []byte) (string, bool, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, data, parser.ImportsOnly|parser.ParseComments|parser.SkipObjectResolution)
	if err != nil || file == nil || file.Name == nil {
		return "", false, false
	}
	position := fset.PositionFor(file.Package, false)
	if position.Offset < 0 || position.Offset > len(data) {
		return "", false, false
	}
	digest := sha256.New()
	_, _ = digest.Write(data[:position.Offset])
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(file.Name.Name))
	importsC := false
	for _, imported := range file.Imports {
		if imported == nil || imported.Path == nil {
			return "", false, false
		}
		pathValue, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			return "", false, false
		}
		if pathValue == "C" {
			importsC = true
		}
		nameValue := ""
		if imported.Name != nil {
			nameValue = imported.Name.Name
		}
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(nameValue))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(pathValue))
	}
	return hex.EncodeToString(digest.Sum(nil)), importsC, true
}

// readTypedSchemaIncrementalDirectoryHeaders detects active-set changes without hashing bodies of inactive Go files.
func readTypedSchemaIncrementalDirectoryHeaders(directory string) ([]typedSchemaIncrementalHeaderState, bool) {
	return readTypedSchemaIncrementalDirectoryHeadersFromSnapshot(directory, nil)
}

// readTypedSchemaIncrementalDirectoryHeadersFromSnapshot validates directory membership against captured paths when available.
func readTypedSchemaIncrementalDirectoryHeadersFromSnapshot(directory string, sourceSnapshot map[string][]byte) ([]typedSchemaIncrementalHeaderState, bool) {
	if sourceSnapshot != nil {
		headers := make([]typedSchemaIncrementalHeaderState, 0)
		for path, data := range sourceSnapshot {
			name := filepath.Base(path)
			if filepath.Dir(path) != filepath.Clean(directory) || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				continue
			}
			headerHash, importsC, headerOK := typedSchemaIncrementalHeaderHash(path, data)
			if !headerOK || importsC {
				return nil, false
			}
			headers = append(headers, typedSchemaIncrementalHeaderState{Path: filepath.Clean(path), HeaderHash: headerHash})
		}
		sort.Slice(headers, func(left int, right int) bool { return headers[left].Path < headers[right].Path })
		return headers, true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, false
	}
	headers := make([]typedSchemaIncrementalHeaderState, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		path := filepath.Join(directory, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, false
		}
		headerHash, importsC, headerOK := typedSchemaIncrementalHeaderHash(path, data)
		if !headerOK || importsC {
			return nil, false
		}
		headers = append(headers, typedSchemaIncrementalHeaderState{Path: filepath.Clean(path), HeaderHash: headerHash})
	}
	sort.Slice(headers, func(left int, right int) bool { return headers[left].Path < headers[right].Path })
	return headers, true
}

// buildTypedSchemaIncrementalExpressions serializes every package contract expression so route-selection changes within a known package remain warm.
func buildTypedSchemaIncrementalExpressions(pkg *packages.Package) (string, []byte, []typedSchemaIncrementalExpressionState, bool) {
	if pkg == nil || pkg.Types == nil || pkg.TypesInfo == nil || pkg.Fset == nil {
		return "", nil, nil, false
	}
	roots := packageContractExpressions(pkg)
	ranges := make([]typedSourceRange, 0, len(roots))
	for _, expression := range roots {
		ranges = append(ranges, expression.Source)
	}
	registry := &typedSchemaRegistry{expressions: map[string]typedExpression{}}
	registry.indexPackageExpressions(pkg, newTypedSourceSelection(ranges))
	keys := make([]string, 0, len(registry.expressions))
	for key := range registry.expressions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", nil, nil, true
	}

	expressionPath := typedSchemaIncrementalExpressionPackagePath(pkg.PkgPath)
	expressionPackage := types.NewPackage(expressionPath, "expressions")
	expressions := make([]typedSchemaIncrementalExpressionState, 0, len(keys))
	for index, key := range keys {
		expression := registry.expressions[key]
		name := fmt.Sprintf("E%06d", index)
		if expressionPackage.Scope().Insert(types.NewVar(token.NoPos, expressionPackage, name, expression.Type)) != nil {
			return "", nil, nil, false
		}
		expressions = append(expressions, typedSchemaIncrementalExpressionState{
			Name:     name,
			Source:   expression.Source,
			Constant: encodeTypedSchemaIncrementalConstant(expression.Value),
		})
	}
	expressionPackage.MarkComplete()
	artifact, ok := writeTypedSchemaIncrementalArtifact(pkg.Fset, expressionPackage)
	if !ok {
		return "", nil, nil, false
	}
	return expressionPath, artifact, expressions, true
}

// typedSchemaIncrementalExpressionPackagePath gives each selected package a stable synthetic import identity.
func typedSchemaIncrementalExpressionPackagePath(packagePath string) string {
	pathDigest := sha256.Sum256([]byte(packagePath))
	return "goforj.invalid/webindex/expressions/" + hex.EncodeToString(pathDigest[:8])
}

// encodeTypedSchemaIncrementalConstant retains only values consulted by literal map and array analysis.
func encodeTypedSchemaIncrementalConstant(value constant.Value) *typedSchemaIncrementalConstantState {
	if value == nil {
		return nil
	}
	switch value.Kind() {
	case constant.Int:
		return &typedSchemaIncrementalConstantState{Kind: "int", Value: value.ExactString()}
	case constant.String:
		return &typedSchemaIncrementalConstantState{Kind: "string", Value: constant.StringVal(value)}
	default:
		return nil
	}
}

// decodeTypedSchemaIncrementalConstant reconstructs the exact constant kinds used by schema mapping.
func decodeTypedSchemaIncrementalConstant(state *typedSchemaIncrementalConstantState) (constant.Value, bool) {
	if state == nil {
		return nil, true
	}
	switch state.Kind {
	case "int":
		value := constant.MakeFromLiteral(state.Value, token.INT, 0)
		return value, value.Kind() != constant.Unknown
	case "string":
		return constant.MakeString(state.Value), true
	default:
		return nil, false
	}
}

// writeTypedSchemaIncrementalArtifact wraps the exporter because malformed type graphs must become cache misses rather than process panics.
func writeTypedSchemaIncrementalArtifact(fset *token.FileSet, pkg *types.Package) (artifact []byte, ok bool) {
	if fset == nil || pkg == nil || pkg == types.Unsafe {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			artifact = nil
			ok = false
		}
	}()
	var buffer bytes.Buffer
	if err := gcexportdata.Write(&buffer, fset, pkg); err != nil || buffer.Len() == 0 || buffer.Len() > typedSchemaIncrementalArtifactLimit {
		return nil, false
	}
	return append([]byte(nil), buffer.Bytes()...), true
}

// readTypedSchemaIncrementalArtifact bounds and recovers around the private export decoder.
func readTypedSchemaIncrementalArtifact(artifact []byte, fset *token.FileSet, imports map[string]*types.Package, path string) (pkg *types.Package, ok bool) {
	if len(artifact) == 0 || len(artifact) > typedSchemaIncrementalArtifactLimit || fset == nil || path == "" {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			pkg = nil
			ok = false
		}
	}()
	decoded, err := gcexportdata.Read(bytes.NewReader(artifact), fset, imports, path)
	if err != nil || decoded == nil || decoded.Path() != path {
		return nil, false
	}
	return decoded, true
}

// typedSchemaIncrementalParsedByDirectory groups current active syntax for selected package validation and direct checking.
func typedSchemaIncrementalParsedByDirectory(parsed []*parsedFile) map[string][]*parsedFile {
	byDirectory := map[string][]*parsedFile{}
	for _, file := range parsed {
		if file == nil || file.File == nil || file.Path == "" {
			continue
		}
		canonical := filepath.Clean(file.Path)
		byDirectory[filepath.Dir(canonical)] = append(byDirectory[filepath.Dir(canonical)], file)
	}
	for directory := range byDirectory {
		sort.Slice(byDirectory[directory], func(left int, right int) bool {
			return byDirectory[directory][left].Path < byDirectory[directory][right].Path
		})
	}
	return byDirectory
}

// typedSchemaIncrementalSourceMatches verifies package structure and reports exact active-source changes separately.
func typedSchemaIncrementalSourceMatches(prior typedSchemaIncrementalSourceState, parsed []*parsedFile, snapshots ...map[string][]byte) (bool, bool) {
	if prior.Directory == "" || len(prior.Files) == 0 {
		return false, false
	}
	var sourceSnapshot map[string][]byte
	if len(snapshots) > 0 {
		sourceSnapshot = snapshots[0]
	}
	headers, ok := readTypedSchemaIncrementalDirectoryHeadersFromSnapshot(prior.Directory, sourceSnapshot)
	if !ok || !typedSchemaIncrementalHeadersEqual(prior.DirectoryFiles, headers) {
		return false, false
	}
	if parsed != nil {
		if len(parsed) != len(prior.Files) {
			return false, false
		}
		for index := range parsed {
			if filepath.Clean(parsed[index].Path) != prior.Files[index].Path {
				return false, false
			}
		}
	}
	changed := false
	for _, expected := range prior.Files {
		current, currentOK, _ := readTypedSchemaIncrementalFileStateFromSnapshot(expected.Path, sourceSnapshot)
		if !currentOK || current.HeaderHash != expected.HeaderHash {
			return false, false
		}
		if current.ContentHash != expected.ContentHash {
			changed = true
		}
	}
	return true, changed
}

// typedSchemaIncrementalHeadersEqual compares deterministic directory evidence.
func typedSchemaIncrementalHeadersEqual(left []typedSchemaIncrementalHeaderState, right []typedSchemaIncrementalHeaderState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// typedSchemaIncrementalPropagateSelectedChanges invalidates selected dependents and rejects paths that require rechecking an unselected intermediate.
func typedSchemaIncrementalPropagateSelectedChanges(entries map[string]*typedSchemaIncrementalPackageState, selected map[string]bool, changed map[string]bool) bool {
	directChanges := make(map[string]bool, len(changed))
	for path, contentChanged := range changed {
		if contentChanged {
			directChanges[path] = true
		}
	}
	for path := range selected {
		if typedSchemaIncrementalCrossesUnselectedDependency(path, entries, selected, directChanges, false, map[string]bool{}) {
			return false
		}
	}
	for updated := true; updated; {
		updated = false
		for path := range selected {
			if changed[path] {
				continue
			}
			for _, imported := range entries[path].Imports {
				if selected[imported.Target] && changed[imported.Target] {
					changed[path] = true
					updated = true
					break
				}
			}
		}
	}
	return true
}

// typedSchemaIncrementalCrossesUnselectedDependency finds changed exports that cannot reach a selected root through cached packages safely.
func typedSchemaIncrementalCrossesUnselectedDependency(path string, entries map[string]*typedSchemaIncrementalPackageState, selected map[string]bool, directChanges map[string]bool, crossedUnselected bool, visiting map[string]bool) bool {
	if visiting[path] {
		return false
	}
	visiting[path] = true
	defer delete(visiting, path)
	entry := entries[path]
	if entry == nil {
		return false
	}
	if !selected[path] && !entry.Local {
		return false
	}
	for _, imported := range entry.Imports {
		crossed := crossedUnselected || !selected[imported.Target]
		if crossed && directChanges[imported.Target] {
			return true
		}
		if typedSchemaIncrementalCrossesUnselectedDependency(imported.Target, entries, selected, directChanges, crossed, visiting) {
			return true
		}
	}
	return false
}

// loadPackage restores a cached package or directly checks an impacted selected package.
func (loader *typedSchemaIncrementalLoader) loadPackage(path string) (*packages.Package, error) {
	if err := loader.ctx.Err(); err != nil {
		return nil, err
	}
	if pkg := loader.loaded[path]; pkg != nil {
		return pkg, nil
	}
	if path == "unsafe" {
		return &packages.Package{ID: "unsafe", Name: "unsafe", PkgPath: "unsafe", Fset: loader.fset, Types: types.Unsafe}, nil
	}
	entry := loader.packages[path]
	if entry == nil {
		return nil, typedSchemaIncrementalUnsupported{reason: "missing package state for " + path}
	}
	if loader.loading[path] {
		return nil, typedSchemaIncrementalUnsupported{reason: "package import cycle at " + path}
	}
	loader.loading[path] = true
	defer delete(loader.loading, path)
	if loader.selected[path] && loader.changed[path] {
		return loader.checkSelectedPackage(entry)
	}
	for _, imported := range entry.Imports {
		if _, err := loader.loadPackage(imported.Target); err != nil {
			return nil, err
		}
	}
	decoded, ok := readTypedSchemaIncrementalArtifact(entry.Artifact, loader.fset, loader.typesPackages, entry.Path)
	if !ok || decoded.Name() != entry.Name {
		return nil, typedSchemaIncrementalUnsupported{reason: "decode package artifact for " + path}
	}
	loader.typesPackages[path] = decoded
	pkg := &packages.Package{
		ID:         path,
		Name:       entry.Name,
		PkgPath:    path,
		Fset:       loader.fset,
		Types:      decoded,
		TypesSizes: loader.sizes,
		Imports:    map[string]*packages.Package{},
	}
	loader.loaded[path] = pkg
	return pkg, nil
}

// checkSelectedPackage type-checks current shared syntax against restored dependency packages without invoking the Go command.
func (loader *typedSchemaIncrementalLoader) checkSelectedPackage(entry *typedSchemaIncrementalPackageState) (*packages.Package, error) {
	files := loader.parsedByPackage[entry.Path]
	if len(files) == 0 {
		return nil, typedSchemaIncrementalUnsupported{reason: "missing current syntax for " + entry.Path}
	}
	astFiles := make([]*ast.File, 0, len(files))
	for _, file := range files {
		if file == nil || file.File == nil || file.PackageName != entry.Name {
			return nil, typedSchemaIncrementalUnsupported{reason: "package syntax mismatch for " + entry.Path}
		}
		astFiles = append(astFiles, file.File)
	}
	typesPackage := types.NewPackage(entry.Path, entry.Name)
	loader.typesPackages[entry.Path] = typesPackage
	info := newTypedSchemaIncrementalTypesInfo()
	pkg := &packages.Package{
		ID:              entry.Path,
		Name:            entry.Name,
		PkgPath:         entry.Path,
		Fset:            loader.fset,
		CompiledGoFiles: typedSchemaIncrementalSourcePaths(entry.Source),
		GoFiles:         typedSchemaIncrementalSourcePaths(entry.Source),
		Syntax:          astFiles,
		Types:           typesPackage,
		TypesInfo:       info,
		TypesSizes:      loader.sizes,
		Imports:         map[string]*packages.Package{},
	}
	importTargets := make(map[string]string, len(entry.Imports))
	for _, imported := range entry.Imports {
		importTargets[imported.Path] = imported.Target
	}
	var infrastructureErr error
	var typeErrors []packages.Error
	config := &types.Config{
		GoVersion: loader.state.GoVersion,
		Sizes:     pkg.TypesSizes,
		Importer: typedSchemaIncrementalImporter(func(importPath string) (*types.Package, error) {
			if err := loader.ctx.Err(); err != nil {
				infrastructureErr = err
				return nil, err
			}
			target := importTargets[importPath]
			if target == "" {
				infrastructureErr = typedSchemaIncrementalUnsupported{reason: "new import " + importPath}
				return nil, infrastructureErr
			}
			dependency, err := loader.loadPackage(target)
			if err != nil {
				infrastructureErr = err
				return nil, err
			}
			pkg.Imports[importPath] = dependency
			return dependency.Types, nil
		}),
		Error: func(err error) {
			typeErrors = append(typeErrors, typedSchemaIncrementalPackagesError(loader.fset, err))
		},
	}
	checker := types.NewChecker(config, loader.fset, typesPackage, info)
	if err := checker.Files(astFiles); err != nil && len(typeErrors) == 0 {
		typeErrors = append(typeErrors, typedSchemaIncrementalPackagesError(loader.fset, err))
	}
	if infrastructureErr != nil {
		delete(loader.typesPackages, entry.Path)
		return nil, infrastructureErr
	}
	pkg.Errors = typeErrors
	loader.loaded[entry.Path] = pkg
	return pkg, nil
}

// typedSchemaIncrementalImporter adapts a closure to the standard go/types importer contract.
type typedSchemaIncrementalImporter func(path string) (*types.Package, error)

// Import loads one package through the incremental transaction.
func (importer typedSchemaIncrementalImporter) Import(path string) (*types.Package, error) {
	return importer(path)
}

// newTypedSchemaIncrementalTypesInfo allocates every map required by expression indexing and generic type resolution.
func newTypedSchemaIncrementalTypesInfo() *types.Info {
	return &types.Info{
		Types:        map[ast.Expr]types.TypeAndValue{},
		Instances:    map[*ast.Ident]types.Instance{},
		Defs:         map[*ast.Ident]types.Object{},
		Uses:         map[*ast.Ident]types.Object{},
		Implicits:    map[ast.Node]types.Object{},
		Selections:   map[*ast.SelectorExpr]*types.Selection{},
		Scopes:       map[ast.Node]*types.Scope{},
		FileVersions: map[*ast.File]string{},
	}
}

// typedSchemaIncrementalPackagesError preserves stable position and message fields consumed by registry diagnostics.
func typedSchemaIncrementalPackagesError(fset *token.FileSet, err error) packages.Error {
	var typedError types.Error
	if errors.As(err, &typedError) {
		position := "-"
		if typedError.Fset != nil {
			position = typedError.Fset.PositionFor(typedError.Pos, false).String()
		} else if fset != nil {
			position = fset.PositionFor(typedError.Pos, false).String()
		}
		return packages.Error{Pos: position, Msg: typedError.Msg, Kind: packages.TypeError}
	}
	return packages.Error{Pos: "-", Msg: err.Error(), Kind: packages.UnknownError}
}

// restoreTypedSchemaIncrementalExpressions rebuilds source-keyed expression types from a selected package's synthetic artifact.
func restoreTypedSchemaIncrementalExpressions(registry *typedSchemaRegistry, loader *typedSchemaIncrementalLoader, entry *typedSchemaIncrementalPackageState, selection typedSourceSelection) bool {
	if len(entry.Expressions) == 0 {
		return len(entry.ExpressionArtifact) == 0 && entry.ExpressionPath == ""
	}
	decoded, ok := readTypedSchemaIncrementalArtifact(entry.ExpressionArtifact, loader.fset, loader.typesPackages, entry.ExpressionPath)
	if !ok {
		return false
	}
	for _, state := range entry.Expressions {
		object, objectOK := decoded.Scope().Lookup(state.Name).(*types.Var)
		value, valueOK := decodeTypedSchemaIncrementalConstant(state.Constant)
		if !objectOK || !valueOK || state.Source.File == "" || !selection.contains(state.Source) {
			if !objectOK || !valueOK || state.Source.File == "" {
				return false
			}
			continue
		}
		registry.expressions[typedSourceKey(state.Source)] = typedExpression{Type: object.Type(), Value: value, Source: state.Source}
	}
	return true
}

// refreshTypedSchemaIncrementalState replaces directly checked roots while preserving validated dependency artifacts.
func refreshTypedSchemaIncrementalState(prior *typedSchemaIncrementalState, loader *typedSchemaIncrementalLoader, checked []string, snapshots ...map[string][]byte) (*typedSchemaIncrementalState, bool) {
	copyOf := *prior
	copyOf.BuildTags = append([]string(nil), prior.BuildTags...)
	copyOf.Packages = append([]typedSchemaIncrementalPackageState(nil), prior.Packages...)
	byPath := make(map[string]int, len(copyOf.Packages))
	var sourceSnapshot map[string][]byte
	if len(snapshots) > 0 {
		sourceSnapshot = snapshots[0]
	}
	for index := range copyOf.Packages {
		byPath[copyOf.Packages[index].Path] = index
	}
	for _, path := range checked {
		pkg := loader.loaded[path]
		if pkg == nil || len(pkg.Errors) != 0 {
			return nil, false
		}
		index, exists := byPath[path]
		if !exists {
			return nil, false
		}
		priorEntry := copyOf.Packages[index]
		artifact, ok := writeTypedSchemaIncrementalArtifact(pkg.Fset, pkg.Types)
		if !ok {
			return nil, false
		}
		source, sourceOK := buildTypedSchemaIncrementalSourceState(pkg.CompiledGoFiles, sourceSnapshot)
		if !sourceOK {
			return nil, false
		}
		expressionPath, expressionArtifact, expressions, expressionOK := buildTypedSchemaIncrementalExpressions(pkg)
		if !expressionOK {
			return nil, false
		}
		priorEntry.Artifact = artifact
		priorEntry.Source = source
		priorEntry.ExpressionPath = expressionPath
		priorEntry.ExpressionArtifact = expressionArtifact
		priorEntry.Expressions = expressions
		copyOf.Packages[index] = priorEntry
	}
	return &copyOf, true
}

// typedSchemaIncrementalSourcePaths projects deterministic active source paths into packages.Package fields.
func typedSchemaIncrementalSourcePaths(source typedSchemaIncrementalSourceState) []string {
	paths := make([]string, 0, len(source.Files))
	for _, file := range source.Files {
		paths = append(paths, file.Path)
	}
	return paths
}

// typedSchemaIncrementalGoVersion returns the go/types spelling of the main module language version.
func typedSchemaIncrementalGoVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	return typedSchemaIncrementalGoVersionFromData(data)
}

// typedSchemaIncrementalGoVersionFromData returns the go/types spelling from an immutable main module snapshot.
func typedSchemaIncrementalGoVersionFromData(data []byte) string {
	moduleFile, err := modfile.Parse("go.mod", data, nil)
	if err != nil || moduleFile.Go == nil || strings.TrimSpace(moduleFile.Go.Version) == "" {
		return ""
	}
	return "go" + strings.TrimPrefix(strings.TrimSpace(moduleFile.Go.Version), "go")
}

// typedSchemaIncrementalStringSlicesEqual compares normalized option identities without allocating a temporary encoding.
func typedSchemaIncrementalStringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
