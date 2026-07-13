package webindex

import (
	"path/filepath"
	"sort"
	"strings"
)

// Diagnostic captures parser/indexer warnings and informational findings.
type Diagnostic struct {
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Operation string `json:"operation,omitempty"`
}

// relativizeManifestPaths keeps generated artifacts portable across checkout roots and machines.
func relativizeManifestPaths(root string, manifest *Manifest) {
	for i := range manifest.Operations {
		manifest.Operations[i].Handler.File = relativeSourcePath(root, manifest.Operations[i].Handler.File)
		for middlewareIndex := range manifest.Operations[i].middlewareProvenance {
			manifest.Operations[i].middlewareProvenance[middlewareIndex].File = relativeSourcePath(root, manifest.Operations[i].middlewareProvenance[middlewareIndex].File)
		}
	}
	for i := range manifest.Diagnostics {
		manifest.Diagnostics[i].File = relativeSourcePath(root, manifest.Diagnostics[i].File)
	}
}

// relativeSourcePath returns a slash-separated project path without leaking an absolute path when source is unexpectedly outside the root.
func relativeSourcePath(root string, sourcePath string) string {
	if strings.TrimSpace(sourcePath) == "" {
		return ""
	}
	if !filepath.IsAbs(sourcePath) {
		return filepath.ToSlash(filepath.Clean(sourcePath))
	}
	relative, err := filepath.Rel(root, sourcePath)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return filepath.Base(sourcePath)
}

// sortDiagnostics makes diagnostic artifacts independent from traversal and map iteration order.
func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left := diagnostics[i]
		right := diagnostics[j]
		leftKey := left.File + "|" + intToString(left.Line) + "|" + left.Operation + "|" + left.Code + "|" + left.Message + "|" + left.Severity
		rightKey := right.File + "|" + intToString(right.Line) + "|" + right.Operation + "|" + right.Code + "|" + right.Message + "|" + right.Severity
		return leftKey < rightKey
	})
}

// duplicateOperationDiagnostics rejects ambiguous method/path ownership because OpenAPI maps can represent only one operation per pair.
func duplicateOperationDiagnostics(operations []Operation) []Diagnostic {
	seen := map[string]Operation{}
	diagnostics := make([]Diagnostic, 0)
	for _, operation := range operations {
		key := strings.ToUpper(strings.TrimSpace(operation.Method)) + " " + canonicalRouteTemplateShape(operation.Path)
		first, exists := seen[key]
		if !exists {
			seen[key] = operation
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Severity:  "error",
			Code:      "duplicate_operation",
			Message:   "duplicate route conflicts with handler " + first.Handler.Expression + " declared at line " + intToString(first.Handler.Line),
			File:      operation.Handler.File,
			Line:      operation.Handler.Line,
			Operation: operation.ID,
		})
	}
	return diagnostics
}

// canonicalRouteTemplateShape removes parameter labels because OpenAPI treats equal templated segment positions as one path shape.
func canonicalRouteTemplateShape(path string) string {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if (strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*")) && len(part) > 1 {
			parts[index] = "{}"
			continue
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			parts[index] = "{}"
		}
	}
	return strings.Join(parts, "/")
}
