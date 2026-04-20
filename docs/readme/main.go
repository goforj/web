//go:build ignore
// +build ignore

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	apiStart       = "<!-- api:embed:start -->"
	apiEnd         = "<!-- api:embed:end -->"
	testCountStart = "<!-- test-count:embed:start -->"
	testCountEnd   = "<!-- test-count:embed:end -->"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	fmt.Println("✔ API section updated in README.md")
}

func run() error {
	root, err := findRoot()
	if err != nil {
		return err
	}

	funcs, err := parseFuncs(root)
	if err != nil {
		return err
	}

	readmePath := filepath.Join(root, "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		return err
	}

	out, err := replaceSection(string(data), apiStart, apiEnd, renderAPI(funcs))
	if err != nil {
		return err
	}

	return os.WriteFile(readmePath, []byte(out), 0o644)
}

type FuncDoc struct {
	Key         string
	DisplayName string
	Anchor      string
	Group       string
	Description string
	Examples    []Example
}

type Example struct {
	Label string
	Code  string
	Line  int
}

func findRoot() (string, error) {
	wd, _ := os.Getwd()
	for _, c := range []string{wd, filepath.Join(wd, ".."), filepath.Join(wd, "..", ".."), filepath.Join(wd, "..", "..", "..")} {
		c = filepath.Clean(c)
		if fileExists(filepath.Join(c, "go.mod")) && fileExists(filepath.Join(c, "README.md")) && fileExists(filepath.Join(c, "router.go")) {
			return c, nil
		}
	}
	return "", fmt.Errorf("could not find project root")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseFuncs(root string) ([]*FuncDoc, error) {
	dirs := []struct {
		path   string
		prefix string
	}{
		{path: root},
		{path: filepath.Join(root, "adapter", "echoweb"), prefix: "echoweb"},
		{path: filepath.Join(root, "webindex"), prefix: "webindex"},
		{path: filepath.Join(root, "webmiddleware"), prefix: "webmiddleware"},
		{path: filepath.Join(root, "webprometheus"), prefix: "webprometheus"},
		{path: filepath.Join(root, "webtest"), prefix: "webtest"},
	}

	items := map[string]*FuncDoc{}
	for _, dir := range dirs {
		if !fileExists(dir.path) {
			continue
		}
		if err := parseFuncsInDir(items, dir.path, dir.prefix); err != nil {
			return nil, err
		}
	}

	out := make([]*FuncDoc, 0, len(items))
	for _, item := range items {
		sort.Slice(item.Examples, func(i, j int) bool { return item.Examples[i].Line < item.Examples[j].Line })
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group == out[j].Group {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].Group < out[j].Group
	})
	return out, nil
}

func parseFuncsInDir(out map[string]*FuncDoc, dir, prefix string) error {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return err
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Doc == nil || !ast.IsExported(fn.Name.Name) {
					continue
				}
				displayName := fn.Name.Name
				if recv := extractReceiverName(fn); recv != "" {
					displayName = recv + "." + fn.Name.Name
				}
				if prefix != "" {
					displayName = prefix + "." + displayName
				}
				key := displayName
				item := &FuncDoc{
					Key:         key,
					DisplayName: displayName,
					Anchor:      anchorFor(displayName),
					Group:       extractGroup(fn.Doc),
					Description: extractDescription(fn.Doc),
					Examples:    extractExamples(fset, fn.Doc),
				}
				if existing, ok := out[key]; ok {
					existing.Examples = append(existing.Examples, item.Examples...)
					continue
				}
				out[key] = item
			}
		}
	}

	return nil
}

func extractReceiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return receiverTypeName(fn.Recv.List[0].Type)
}

func receiverTypeName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return receiverTypeName(v.X)
	case *ast.IndexExpr:
		return receiverTypeName(v.X)
	case *ast.IndexListExpr:
		return receiverTypeName(v.X)
	default:
		return ""
	}
}

func extractGroup(group *ast.CommentGroup) string {
	for _, line := range commentLines(group) {
		if strings.HasPrefix(line, "@group ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "@group "))
		}
	}
	return "Core"
}

func extractDescription(group *ast.CommentGroup) string {
	var lines []string
	for _, line := range commentLines(group) {
		if strings.HasPrefix(line, "@group ") || strings.HasPrefix(strings.ToLower(line), "example:") {
			break
		}
		if len(lines) == 0 && line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractExamples(fset *token.FileSet, group *ast.CommentGroup) []Example {
	var examples []Example
	lines := commentLinesWithPos(fset, group)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line.text)
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "example:") {
			continue
		}
		label := strings.TrimSpace(trimmed[len("Example:"):])
		var block []string
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			nextTrimmed := strings.TrimSpace(next.text)
			if strings.HasPrefix(strings.ToLower(nextTrimmed), "example:") || strings.HasPrefix(nextTrimmed, "@group ") {
				break
			}
			if nextTrimmed == "" {
				if len(block) == 0 {
					continue
				}
				break
			}
			block = append(block, next.text)
		}
		if len(block) == 0 {
			continue
		}
		examples = append(examples, Example{
			Label: label,
			Code:  strings.Join(normalizeIndent(block), "\n"),
			Line:  fset.Position(line.pos).Line,
		})
	}
	return examples
}

type docLine struct {
	text string
	pos  token.Pos
}

func commentLines(group *ast.CommentGroup) []string {
	lines := make([]string, 0, len(group.List))
	for _, c := range group.List {
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(c.Text, "//")))
	}
	return lines
}

func commentLinesWithPos(fset *token.FileSet, group *ast.CommentGroup) []docLine {
	lines := make([]docLine, 0, len(group.List))
	for _, c := range group.List {
		line := strings.TrimPrefix(c.Text, "//")
		lines = append(lines, docLine{
			text: line,
			pos:  c.Pos(),
		})
	}
	return lines
}

func normalizeIndent(lines []string) []string {
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := 0
		for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
			indent++
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, line[minIndent:])
	}
	return out
}

func anchorFor(displayName string) string {
	replacer := strings.NewReplacer(".", "-", " ", "-", "_", "-", "/", "-")
	return strings.ToLower(replacer.Replace(displayName))
}

func renderAPI(funcs []*FuncDoc) string {
	var buf bytes.Buffer
	buf.WriteString("## API Index\n\n")
	buf.WriteString("| Group | Functions |\n")
	buf.WriteString("|------:|:-----------|\n")

	byGroup := map[string][]*FuncDoc{}
	var groupNames []string
	for _, fn := range funcs {
		if _, ok := byGroup[fn.Group]; !ok {
			groupNames = append(groupNames, fn.Group)
		}
		byGroup[fn.Group] = append(byGroup[fn.Group], fn)
	}
	sort.Strings(groupNames)

	for _, group := range groupNames {
		items := byGroup[group]
		sort.Slice(items, func(i, j int) bool {
			return items[i].DisplayName < items[j].DisplayName
		})
		links := make([]string, 0, len(items))
		for _, fn := range items {
			links = append(links, fmt.Sprintf("[%s](#%s)", fn.DisplayName, fn.Anchor))
		}
		buf.WriteString(fmt.Sprintf("| **%s** | %s |\n", group, strings.Join(links, " ")))
	}

	buf.WriteString("\n\n")
	buf.WriteString("## API Reference\n\n")
	buf.WriteString("_Generated from public API comments and examples._\n\n")

	for _, group := range groupNames {
		items := byGroup[group]
		sort.Slice(items, func(i, j int) bool {
			return items[i].DisplayName < items[j].DisplayName
		})
		buf.WriteString("### " + group + "\n\n")

		for _, fn := range items {
			buf.WriteString(fmt.Sprintf("#### <a id=\"%s\"></a>%s\n\n", fn.Anchor, fn.DisplayName))
			if fn.Description != "" {
				buf.WriteString(fn.Description + "\n\n")
			}
			for i, ex := range fn.Examples {
				label := strings.TrimSpace(ex.Label)
				if i > 0 {
					if label == "" {
						label = "Example"
					}
					buf.WriteString(label + ":\n\n")
				}
				buf.WriteString("```go\n")
				buf.WriteString(ex.Code)
				buf.WriteString("\n```\n\n")
			}
		}
	}
	return strings.TrimSpace(buf.String()) + "\n"
}

func replaceSection(input, start, end, replacement string) (string, error) {
	si := strings.Index(input, start)
	ei := strings.Index(input, end)
	if si < 0 || ei < 0 || ei < si {
		return "", fmt.Errorf("missing marker pair %q ... %q", start, end)
	}
	var out strings.Builder
	out.WriteString(input[:si+len(start)])
	out.WriteString("\n")
	out.WriteString(replacement)
	out.WriteString(input[ei:])
	return out.String(), nil
}
