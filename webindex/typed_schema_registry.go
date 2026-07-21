package webindex

import (
	"context"
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

	"golang.org/x/tools/go/packages"
)

// typedSchemaLoadOptions narrows type loading to the packages that own selected HTTP handlers.
type typedSchemaLoadOptions struct {
	Root                string
	HandlerFiles        []string
	ContractExpressions []typedSourceRange
	BuildTags           []string
}

// typedSourceRange identifies an expression across independently parsed syntax trees.
type typedSourceRange struct {
	File        string
	StartOffset int
	EndOffset   int
	Line        int
}

// typedSourceSelection indexes route-reachable contract ranges by physical source file.
type typedSourceSelection map[string][]typedSourceRange

// typedExpression records the checked type and provenance for one source expression.
type typedExpression struct {
	Type   types.Type
	Value  constant.Value
	Source typedSourceRange
}

// typedSchemaResolution carries a schema together with the Go identity and source that justified it.
type typedSchemaResolution struct {
	Schema        map[string]any
	TypeIdentity  string
	TypeName      string
	ComponentName string
	Source        typedSourceRange
	Confidence    string
}

// typedSchemaComponent keeps semantic identity separate from its readable OpenAPI component name.
type typedSchemaComponent struct {
	Identity   string
	Name       string
	Package    string
	TypeName   string
	Schema     map[string]any
	Confidence string
}

// typedSchemaRegistry owns focused type information and the canonical component graph derived from it.
type typedSchemaRegistry struct {
	root           string
	expressions    map[string]typedExpression
	componentNames map[string]string
	componentsByID map[string]*typedSchemaComponent
	diagnostics    []Diagnostic
	diagnosticKeys map[string]struct{}
}

// loadTypedSchemaRegistry loads only packages containing selected handlers so indexing does not type-check an unrelated repository wholesale.
func loadTypedSchemaRegistry(ctx context.Context, opts typedSchemaLoadOptions) (*typedSchemaRegistry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := opts.Root
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve typed schema root: %w", err)
	}

	registry := &typedSchemaRegistry{
		root:           filepath.Clean(absRoot),
		expressions:    map[string]typedExpression{},
		componentNames: map[string]string{},
		componentsByID: map[string]*typedSchemaComponent{},
		diagnosticKeys: map[string]struct{}{},
	}
	patterns := typedPackagePatterns(registry.root, opts.HandlerFiles)
	if len(patterns) == 0 {
		return registry, nil
	}
	if opts.ContractExpressions != nil && len(opts.ContractExpressions) == 0 {
		return registry, nil
	}
	if opts.ContractExpressions == nil && !handlerFilesNeedTypedSchemas(opts.HandlerFiles) {
		return registry, nil
	}

	config := &packages.Config{
		Context:    ctx,
		Dir:        registry.root,
		BuildFlags: sourceBuildFlags(opts.BuildTags),
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedTypesSizes |
			packages.NeedImports |
			packages.NeedModule,
		Tests: false,
	}
	loaded, err := packages.Load(config, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load handler packages for typed schemas: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loaded = uniqueTypedPackages(loaded)
	selection := newTypedSourceSelection(opts.ContractExpressions)
	for _, pkg := range loaded {
		registry.recordPackageErrors(pkg)
		registry.indexPackageExpressions(pkg, selection)
	}
	registry.indexReachableComponents(loaded, selection)
	registry.sortDiagnostics()
	return registry, nil
}

// handlerFilesNeedTypedSchemas avoids invoking go list for handlers that never bind or emit a JSON contract.
func handlerFilesNeedTypedSchemas(handlerFiles []string) bool {
	for _, file := range handlerFiles {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			continue
		}
		needed := false
		ast.Inspect(parsed, func(node ast.Node) bool {
			if needed {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "Bind" || selector.Sel.Name == "JSON" {
				needed = true
				return false
			}
			return true
		})
		if needed {
			return true
		}
	}
	return false
}

// typedPackagePatterns batches handlers from the active module while preserving file-query fallbacks for paths the Go command cannot safely treat as literals.
func typedPackagePatterns(root string, handlerFiles []string) []string {
	seen := map[string]struct{}{}
	patterns := make([]string, 0, len(handlerFiles))
	rootModule := ""
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("GO111MODULE")), "off") {
		rootModule = enclosingGoModule(root)
	}
	moduleByDirectory := map[string]string{}
	for _, file := range handlerFiles {
		if strings.TrimSpace(file) == "" {
			continue
		}
		canonical, err := canonicalSourceFile(file)
		if err != nil {
			continue
		}
		directory := filepath.Dir(canonical)
		moduleRoot, exists := moduleByDirectory[directory]
		if !exists {
			moduleRoot = enclosingGoModule(directory)
			moduleByDirectory[directory] = moduleRoot
		}
		pattern := "file=" + canonical
		if rootModule != "" && moduleRoot == rootModule && !strings.Contains(filepath.ToSlash(directory), "...") {
			pattern = directory
		}
		if _, exists := seen[pattern]; exists {
			continue
		}
		seen[pattern] = struct{}{}
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return patterns
}

// enclosingGoModule finds the nearest valid module boundary so directory patterns never cross into an ad-hoc or nested module context.
func enclosingGoModule(path string) string {
	current, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	current = filepath.Clean(current)
	for {
		info, statErr := os.Stat(filepath.Join(current, "go.mod"))
		if statErr == nil {
			if info.Mode().IsRegular() {
				return current
			}
			return ""
		}
		if !os.IsNotExist(statErr) {
			return ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// uniqueTypedPackages removes duplicate results produced when several handler files belong to the same package.
func uniqueTypedPackages(packagesList []*packages.Package) []*packages.Package {
	byID := map[string]*packages.Package{}
	for _, pkg := range packagesList {
		if pkg == nil {
			continue
		}
		if existing := byID[pkg.ID]; existing == nil || len(pkg.Syntax) > len(existing.Syntax) {
			byID[pkg.ID] = pkg
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*packages.Package, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// newTypedSourceSelection groups ranges and removes redundant nested roots so each syntax node avoids scanning every contract call site.
func newTypedSourceSelection(selected []typedSourceRange) typedSourceSelection {
	if selected == nil {
		return nil
	}
	selection := make(typedSourceSelection)
	for _, source := range selected {
		if source.File == "" || source.StartOffset < 0 || source.EndOffset <= source.StartOffset {
			continue
		}
		selection[source.File] = append(selection[source.File], source)
	}
	for file, ranges := range selection {
		sort.Slice(ranges, func(i, j int) bool {
			if ranges[i].StartOffset == ranges[j].StartOffset {
				return ranges[i].EndOffset > ranges[j].EndOffset
			}
			return ranges[i].StartOffset < ranges[j].StartOffset
		})
		reduced := ranges[:0]
		for _, source := range ranges {
			last := len(reduced) - 1
			if last >= 0 && source.EndOffset <= reduced[last].EndOffset {
				continue
			}
			reduced = append(reduced, source)
		}
		selection[file] = reduced
	}
	return selection
}

// contains reports whether a checked expression is wholly nested in a selected contract root.
func (s typedSourceSelection) contains(source typedSourceRange) bool {
	if s == nil {
		return true
	}
	ranges := s[source.File]
	index := sort.Search(len(ranges), func(index int) bool {
		return ranges[index].EndOffset >= source.EndOffset
	})
	return index < len(ranges) && ranges[index].StartOffset <= source.StartOffset
}

// typedSourceRangesIntersect reports whether a syntax node may contain any selected expression.
func typedSourceRangesIntersect(ranges []typedSourceRange, startOffset, endOffset int) bool {
	index := sort.Search(len(ranges), func(index int) bool {
		return ranges[index].EndOffset > startOffset
	})
	return index < len(ranges) && ranges[index].StartOffset < endOffset
}

// typedNodeOffsets returns physical offsets without repeatedly canonicalizing the same package filename.
func typedNodeOffsets(file *token.File, node ast.Node) (int, int, bool) {
	if file == nil || node == nil || node.Pos() == token.NoPos || node.End() == token.NoPos {
		return 0, 0, false
	}
	startPosition := int(node.Pos())
	endPosition := int(node.End())
	fileStart := file.Base()
	fileEnd := fileStart + file.Size()
	if startPosition < fileStart || endPosition < startPosition || endPosition > fileEnd {
		return 0, 0, false
	}
	return file.Offset(node.Pos()), file.Offset(node.End()), true
}

// indexPackageExpressions maps checked expressions by file byte range so the fast parser can use them without sharing a FileSet.
func (r *typedSchemaRegistry) indexPackageExpressions(pkg *packages.Package, selected typedSourceSelection) {
	if pkg == nil || pkg.Fset == nil || pkg.TypesInfo == nil {
		return
	}
	for _, file := range pkg.Syntax {
		if file == nil {
			continue
		}
		var selectedRanges []typedSourceRange
		var tokenFile *token.File
		if selected != nil {
			fileSource, ok := typedRangeForNode(pkg.Fset, file)
			if !ok {
				continue
			}
			selectedRanges = selected[fileSource.File]
			if len(selectedRanges) == 0 {
				continue
			}
			tokenFile = pkg.Fset.File(file.Pos())
			if tokenFile == nil {
				continue
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selected != nil && node != nil {
				startOffset, endOffset, ok := typedNodeOffsets(tokenFile, node)
				if !ok || !typedSourceRangesIntersect(selectedRanges, startOffset, endOffset) {
					return false
				}
				if _, expression := node.(ast.Expr); expression && !selected.contains(typedSourceRange{
					File:        selectedRanges[0].File,
					StartOffset: startOffset,
					EndOffset:   endOffset,
				}) {
					return true
				}
			}
			expression, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			typeOf := pkg.TypesInfo.TypeOf(expression)
			if typeOf == nil {
				return true
			}
			source, ok := typedRangeForNode(pkg.Fset, expression)
			if !ok {
				return true
			}
			r.expressions[typedSourceKey(source)] = typedExpression{
				Type:   typeOf,
				Value:  pkg.TypesInfo.Types[expression].Value,
				Source: source,
			}
			return true
		})
	}
}

// typedRangeForNode converts token positions into stable file offsets that survive reparsing.
func typedRangeForNode(fset *token.FileSet, node ast.Node) (typedSourceRange, bool) {
	if fset == nil || node == nil || node.Pos() == token.NoPos || node.End() == token.NoPos {
		return typedSourceRange{}, false
	}
	start := fset.PositionFor(node.Pos(), false)
	end := fset.PositionFor(node.End(), false)
	if start.Filename == "" || start.Offset < 0 || end.Offset < start.Offset {
		return typedSourceRange{}, false
	}
	canonical, err := canonicalSourceFile(start.Filename)
	if err != nil {
		return typedSourceRange{}, false
	}
	return typedSourceRange{
		File:        canonical,
		StartOffset: start.Offset,
		EndOffset:   end.Offset,
		Line:        start.Line,
	}, true
}

// typedSourceKey serializes a range without depending on platform token.Pos allocation.
func typedSourceKey(source typedSourceRange) string {
	return source.File + ":" + strconv.Itoa(source.StartOffset) + ":" + strconv.Itoa(source.EndOffset)
}

// canonicalSourceFile normalizes paths so package loading and the fast parser agree on source identity.
func canonicalSourceFile(path string) (string, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("source path contains a NUL byte")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absPath), nil
}

// resolveExpression returns the canonical schema backed by the exact checked expression from source.
func (r *typedSchemaRegistry) resolveExpression(fset *token.FileSet, expression ast.Expr) (typedSchemaResolution, bool) {
	checked, source, ok := r.lookupExpression(fset, expression)
	if !ok {
		if r != nil && source.File != "" {
			r.addDiagnostic("typed_expression_not_found", "type information was unavailable for source expression", source)
		}
		return typedSchemaResolution{
			Schema:     map[string]any{},
			Source:     source,
			Confidence: "low",
		}, false
	}
	return r.resolveType(checked.Type, checked.Source), true
}

// resolveJSONExpression preserves exact literal keys and values while using checked types for every nested wire shape.
func (r *typedSchemaRegistry) resolveJSONExpression(fset *token.FileSet, expression ast.Expr) (typedSchemaResolution, bool) {
	checked, source, ok := r.lookupExpression(fset, expression)
	if !ok {
		if r != nil && source.File != "" {
			r.addDiagnostic("typed_expression_not_found", "type information was unavailable for JSON response expression", source)
		}
		return typedSchemaResolution{Schema: map[string]any{}, Source: source, Confidence: "low"}, false
	}
	schema := r.schemaForJSONExpression(fset, expression, checked)
	return r.resolutionForSchema(checked.Type, checked.Source, schema), true
}

// lookupExpression retrieves independently parsed syntax by its stable file offsets without emitting policy diagnostics.
func (r *typedSchemaRegistry) lookupExpression(fset *token.FileSet, expression ast.Expr) (typedExpression, typedSourceRange, bool) {
	if r == nil {
		return typedExpression{}, typedSourceRange{}, false
	}
	source, ok := typedRangeForNode(fset, expression)
	if !ok {
		return typedExpression{}, typedSourceRange{}, false
	}
	checked, ok := r.expressions[typedSourceKey(source)]
	return checked, source, ok
}

// resolveType converts one checked Go type while retaining its canonical identity and source provenance.
func (r *typedSchemaRegistry) resolveType(typeOf types.Type, source typedSourceRange) typedSchemaResolution {
	schema := r.schemaForType(typeOf, source)
	return r.resolutionForSchema(typeOf, source, schema)
}

// resolutionForSchema attaches semantic provenance after either general type mapping or literal-aware mapping has selected a schema.
func (r *typedSchemaRegistry) resolutionForSchema(typeOf types.Type, source typedSourceRange, schema map[string]any) typedSchemaResolution {
	resolution := typedSchemaResolution{
		Schema:       schema,
		TypeIdentity: canonicalGoTypeIdentity(typeOf),
		TypeName:     contractTypeName(typeOf),
		Source:       source,
		Confidence:   "high",
	}
	if named := namedType(typeOf); named != nil && !isWellKnownNamedType(named) {
		identity := canonicalNamedIdentity(named)
		if component := r.componentsByID[identity]; component != nil {
			resolution.ComponentName = component.Name
		}
	}
	if len(schema) == 0 {
		resolution.Confidence = "low"
	}
	return resolution
}

// contractTypeName avoids exposing raw anonymous struct declarations while retaining useful named and container identities.
func contractTypeName(typeOf types.Type) string {
	if named := namedType(typeOf); named != nil {
		return readableNamedType(named)
	}
	typeOf = types.Unalias(typeOf)
	if pointer, ok := typeOf.(*types.Pointer); ok {
		typeOf = types.Unalias(pointer.Elem())
	}
	switch typeOf.(type) {
	case *types.Struct, *types.Interface, *types.Signature, *types.Chan, *types.Tuple:
		return ""
	default:
		return readableGoType(typeOf)
	}
}

// componentSnapshot returns components in semantic-name order so unrelated route insertion cannot perturb output.
func (r *typedSchemaRegistry) componentSnapshot() []typedSchemaComponent {
	if r == nil {
		return nil
	}
	components := make([]typedSchemaComponent, 0, len(r.componentsByID))
	for _, component := range r.componentsByID {
		if component == nil || component.Schema == nil {
			continue
		}
		copyOf := *component
		copyOf.Schema = cloneSchemaMap(component.Schema)
		components = append(components, copyOf)
	}
	sort.Slice(components, func(i, j int) bool {
		if components[i].Name == components[j].Name {
			return components[i].Identity < components[j].Identity
		}
		return components[i].Name < components[j].Name
	})
	return components
}

// componentSchemaMap returns a detached OpenAPI-ready map keyed by readable component name.
func (r *typedSchemaRegistry) componentSchemaMap() map[string]any {
	components := r.componentSnapshot()
	if len(components) == 0 {
		return nil
	}
	out := make(map[string]any, len(components))
	for _, component := range components {
		out[component.Name] = component.Schema
	}
	return out
}

// diagnosticsSnapshot returns deterministic diagnostics accumulated during loading and schema conversion.
func (r *typedSchemaRegistry) diagnosticsSnapshot() []Diagnostic {
	if r == nil || len(r.diagnostics) == 0 {
		return nil
	}
	r.sortDiagnostics()
	return append([]Diagnostic(nil), r.diagnostics...)
}

// recordPackageErrors retains partial type information while making type-check failures visible to the generated contract.
func (r *typedSchemaRegistry) recordPackageErrors(pkg *packages.Package) {
	if pkg == nil {
		return
	}
	for _, packageError := range pkg.Errors {
		file, line := parsePackageErrorPosition(packageError.Pos)
		source := typedSourceRange{File: file, Line: line}
		r.addDiagnostic("typed_package_error", r.sanitizeDiagnosticMessage(packageError.Msg), source)
	}
}

// sanitizeDiagnosticMessage removes checkout-specific roots embedded by go/packages while retaining project-relative evidence.
func (r *typedSchemaRegistry) sanitizeDiagnosticMessage(message string) string {
	message = filepath.ToSlash(strings.TrimSpace(message))
	root := filepath.ToSlash(filepath.Clean(r.root))
	if root == "" || root == "." {
		return message
	}
	message = strings.ReplaceAll(message, root+"/", "")
	message = strings.ReplaceAll(message, root, ".")
	return message
}

// parsePackageErrorPosition extracts the stable portion of go/packages positions without assuming a platform path separator.
func parsePackageErrorPosition(position string) (string, int) {
	position = strings.TrimSpace(position)
	if position == "" || position == "-" {
		return "", 0
	}
	parts := strings.Split(position, ":")
	if len(parts) < 2 {
		canonical, err := canonicalSourceFile(position)
		if err != nil {
			return position, 0
		}
		return canonical, 0
	}
	lineIndex := len(parts) - 1
	if _, err := strconv.Atoi(parts[lineIndex]); err == nil && len(parts) >= 3 {
		lineIndex--
	}
	line, _ := strconv.Atoi(parts[lineIndex])
	file := strings.Join(parts[:lineIndex], ":")
	canonical, err := canonicalSourceFile(file)
	if err != nil {
		return file, line
	}
	return canonical, line
}

// addDiagnostic deduplicates findings because one unsupported type can be reached through several recursive paths.
func (r *typedSchemaRegistry) addDiagnostic(code, message string, source typedSourceRange) {
	if r == nil {
		return
	}
	file := r.relativeSourceFile(source.File)
	key := code + "|" + file + "|" + strconv.Itoa(source.Line) + "|" + message
	if _, exists := r.diagnosticKeys[key]; exists {
		return
	}
	r.diagnosticKeys[key] = struct{}{}
	r.diagnostics = append(r.diagnostics, Diagnostic{
		Severity: "warn",
		Code:     code,
		Message:  message,
		File:     file,
		Line:     source.Line,
	})
}

// relativeSourceFile keeps manifests reproducible across checkout roots while preserving external package paths when needed.
func (r *typedSchemaRegistry) relativeSourceFile(file string) string {
	if file == "" {
		return ""
	}
	relative, err := filepath.Rel(r.root, file)
	if err == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".." {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(file)
}

// sortDiagnostics makes package traversal and recursive schema discovery order irrelevant to emitted artifacts.
func (r *typedSchemaRegistry) sortDiagnostics() {
	sort.SliceStable(r.diagnostics, func(i, j int) bool {
		left := r.diagnostics[i]
		right := r.diagnostics[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}

// cloneSchemaMap protects the registry graph from callers that adapt schemas for a particular projection.
func cloneSchemaMap(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for key, value := range schema {
		out[key] = cloneSchemaValue(value)
	}
	return out
}

// cloneSchemaValue recursively copies only the JSON-compatible containers used by schema generation.
func cloneSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneSchemaMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index := range typed {
			out[index] = cloneSchemaValue(typed[index])
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
