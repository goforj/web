package webindex

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goforj/str"
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

	parsed, fset, parseDiagnostics, err := parseGoFiles(ctx, root, opts.SkipDir, opts.BuildTags...)
	if err != nil {
		return Manifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if selectedDiagnostics := selectedCompositionParseDiagnostics(root, opts.RouteCompositionPath, parseDiagnostics); len(selectedDiagnostics) > 0 {
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

	scope, err := newRouteScope(root, opts.RouteCompositionPath, parsed)
	if err != nil {
		return Manifest{}, err
	}
	routes, handlers, prefixes, mapping := discoverRoutesAndHandlers(fset, parsed, scope)
	typedSchemas, err := loadTypedSchemaRegistry(ctx, typedSchemaLoadOptions{
		Root:                root,
		HandlerFiles:        selectedHandlerFiles(routes, handlers),
		ContractExpressions: selectedHandlerContractExpressions(routes, handlers, fset),
		BuildTags:           opts.BuildTags,
	})
	if err != nil {
		return Manifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	ops, diagnostics := normalize(routes, handlers, prefixes, mapping, fset, typedSchemas)
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
			openAPIOptions.Info.Title = openAPITitleFromRoot(root)
		}
		document, projectionErr := ProjectOpenAPI(manifest, openAPIOptions)
		if projectionErr != nil {
			return manifest, projectionErr
		}
		artifacts = append(artifacts, jsonArtifact{path: opts.OpenAPIPath, value: document})
	}
	if _, err := publishJSONArtifactsContext(ctx, artifacts); err != nil {
		return Manifest{}, fmt.Errorf("publish API index artifacts: %w", err)
	}

	return manifest, nil
}

// selectedCompositionParseDiagnostics promotes parse failures in the requested composition file because indexing cannot safely fall back to an unscoped API.
func selectedCompositionParseDiagnostics(root string, compositionPath string, diagnostics []Diagnostic) []Diagnostic {
	if strings.TrimSpace(compositionPath) == "" {
		return nil
	}
	candidate := compositionPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if _, err := os.Stat(candidate); err != nil {
		return nil
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
	fset := token.NewFileSet()
	parsed := make([]*parsedFile, 0, 128)
	diagnostics := make([]Diagnostic, 0)
	buildContext := activeSourceBuildContext(buildTags...)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil {
			return err
		}
		base := d.Name()
		if d.IsDir() {
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
		if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
			return nil
		}
		matchesBuild, matchErr := buildContext.MatchFile(filepath.Dir(path), base)
		if matchErr != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: "warn",
				Code:     "build_constraint_error",
				Message:  matchErr.Error(),
				File:     relativeSourcePath(root, path),
			})
			return nil
		}
		if !matchesBuild {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			diagnostics = append(diagnostics, parseErrorDiagnostics(root, path, parseErr)...)
			return nil
		}
		parsed = append(parsed, &parsedFile{
			Path:        path,
			PackageName: file.Name.Name,
			File:        file,
		})
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return parsed, fset, diagnostics, nil
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

// appNameFromDotEnv reads only APP_NAME so indexing never takes ownership of general runtime environment loading.
func appNameFromDotEnv(root string) string {
	path := filepath.Join(root, ".env")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
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
