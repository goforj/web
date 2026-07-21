package webindex

import (
	"context"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestIndexCacheInputHashRejectsIgnoredLocalImports verifies excluded local dependency trees never produce authoritative cache hits.
func TestIndexCacheInputHashRejectsIgnoredLocalImports(t *testing.T) {
	ignoredDirectories := []string{
		"templates",
		"tmp",
		"bin",
		"node_modules",
		"testdata",
		".cache",
		".private",
		"_private",
	}
	for _, directory := range ignoredDirectories {
		t.Run(directory, func(t *testing.T) {
			root := writeIndexCacheInputsProject(t, map[string]string{
				"go.mod": "module example.com/cacheinputs\n\ngo 1.25.0\n",
				"app.go": "package app\n\nimport _ \"example.com/cacheinputs/" + directory + "/contracts\"\n",
			})
			if _, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{}); err != nil || cacheable {
				t.Fatalf("ignored local import cacheability = %t, %v; want false", cacheable, err)
			}
		})
	}

	t.Run("external path", func(t *testing.T) {
		root := writeIndexCacheInputsProject(t, map[string]string{
			"go.mod": "module example.com/cacheinputs\n\ngo 1.25.0\n",
			"app.go": "package app\n\nimport _ \"example.net/external/templates/contracts\"\n",
		})
		if _, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{}); err != nil || !cacheable {
			t.Fatalf("external import cacheability = %t, %v; want true", cacheable, err)
		}
	})

	t.Run("local replacement", func(t *testing.T) {
		root := writeIndexCacheInputsProject(t, map[string]string{
			"go.mod":             "module example.com/cacheinputs\n\ngo 1.25.0\n\nrequire example.net/contracts v0.0.0\nreplace example.net/contracts => ./contracts\n",
			"app.go":             "package app\n\nimport _ \"example.net/contracts/templates/model\"\n",
			"contracts/go.mod":   "module example.net/contracts\n\ngo 1.25.0\n",
			"contracts/model.go": "package contracts\n",
		})
		if _, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{}); err != nil || cacheable {
			t.Fatalf("ignored replacement import cacheability = %t, %v; want false", cacheable, err)
		}
	})
}

// TestReadIndexCacheSourceMetadata verifies import and embed parsing uses compiler-compatible directive arguments.
func TestReadIndexCacheSourceMetadata(t *testing.T) {
	source := []byte("package fixture\n\nimport _ \"embed\"\n\nvar ordinary = `//go:embed ignored`\n\n//go:embedded ignored\n//go:embed plain \"space name\" `raw name` \" leading and trailing \"\u2003unicode\nvar files string\n")
	metadata := readIndexCacheSourceMetadata("fixture.go", source)
	if !metadata.valid {
		t.Fatal("valid source metadata was rejected")
	}
	if !reflect.DeepEqual(metadata.imports, []string{"embed"}) {
		t.Fatalf("imports = %v, want [embed]", metadata.imports)
	}
	if !reflect.DeepEqual(metadata.embedPatterns, []string{" leading and trailing ", "plain", "raw name", "space name", "unicode"}) {
		t.Fatalf("embed patterns = %v", metadata.embedPatterns)
	}

	withoutImport := readIndexCacheSourceMetadata("fixture.go", []byte("package fixture\n\n//go:embed ignored\nvar files string\n"))
	if !withoutImport.valid || !reflect.DeepEqual(withoutImport.embedPatterns, []string{"ignored"}) {
		t.Fatalf("directive without same-file embed import = %+v, want structural tracking", withoutImport)
	}

	malformed := readIndexCacheSourceMetadata("fixture.go", []byte("package fixture\n\nimport _ \"embed\"\n\n//go:embed \"unterminated\nvar files string\n"))
	if malformed.valid {
		t.Fatal("malformed embed directive was accepted")
	}
}

// TestReadIndexCacheSourceMetadataMatchesReference verifies the single-pass reader preserves the prior parser and scanner contract.
func TestReadIndexCacheSourceMetadataMatchesReference(t *testing.T) {
	tests := map[string]string{
		"package only":               "package fixture\n",
		"explicit package semicolon": "package fixture; var value = 1\n",
		"blank package name":         "package _\n",
		"single import":              "package fixture\nimport \"fmt\"\n",
		"separate imports":           "package fixture\nimport \"fmt\"\nimport alias \"net/http\"\n",
		"grouped imports":            "package fixture\nimport (\n\t_ \"embed\"\n\t. `example.com/dot`\n\talias \"example.com/alias\"\n)\n",
		"compact import group":       "package fixture; import (\"fmt\"); var value = 1\n",
		"comments in imports":        "// package doc\npackage /* name */ fixture\nimport /* declaration */ (\n\t// first\n\t\"fmt\" // trailing\n\t/* second */ alias \"net/http\"\n)\n",
		"duplicate imports":          "package fixture\nimport \"fmt\"\nimport _ \"fmt\"\n",
		"embed argument forms":       "package fixture\nimport _ \"embed\"\n//go:embed plain \"space name\" `raw name` \" leading \"\u2003unicode\nvar files string\n",
		"embed lookalikes":           "package fixture\nvar quoted = \"//go:embed quoted\"\nvar raw = `//go:embed raw`\n/* //go:embed block */\n//go:embedded prefix\n// go:embed spaced\n///go:embed slashed\n",
		"body syntax error":          "package fixture\nfunc broken( {\n",
		"late import":                "package fixture\nvar value = 1\nimport \"fmt\"\n",
		"missing package":            "import \"fmt\"\n",
		"missing package name":       "package\nimport \"fmt\"\n",
		"missing package semicolon":  "package fixture import \"fmt\"\n",
		"missing import path":        "package fixture\nimport\n",
		"non-string import path":     "package fixture\nimport 123\n",
		"double import alias":        "package fixture\nimport first second \"fmt\"\n",
		"unclosed import group":      "package fixture\nimport (\n\t\"fmt\"\n",
		"malformed embed quote":      "package fixture\n//go:embed \"unterminated\nvar file string\n",
		"empty embed directive":      "package fixture\n//go:embed\nvar file string\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			data := []byte(source)
			got := readIndexCacheSourceMetadata("fixture.go", data)
			want := readIndexCacheSourceMetadataReference("fixture.go", data)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("single-pass metadata differs from reference\ngot:  %+v\nwant: %+v", got, want)
			}
		})
	}
}

// TestReadIndexCacheSourceMetadataSkipsSignalFreeFiles verifies the fast path cannot hide imports or embed directives.
func TestReadIndexCacheSourceMetadataSkipsSignalFreeFiles(t *testing.T) {
	tests := map[string]struct {
		source       string
		importSignal bool
		embedSignal  bool
	}{
		"ordinary source":           {source: "package fixture\nvar value = 1\n"},
		"lexical error":             {source: "package fixture\nvar value = \"unterminated\n"},
		"illegal byte":              {source: "package fixture\nvar value = \x00\n"},
		"capitalized identifier":    {source: "package fixture\nvar Import = 1\n"},
		"import keyword":            {source: "package fixture\nimport \"fmt\"\n", importSignal: true},
		"import substring":          {source: "package fixture\nvar important = 1\n", importSignal: true},
		"embed directive":           {source: "package fixture\n//go:embed asset.txt\nvar asset string\n", embedSignal: true},
		"embed prefix lookalike":    {source: "package fixture\n//go:embedded ignored\n", embedSignal: true},
		"spaced embed lookalike":    {source: "package fixture\n//go : embed ignored\n"},
		"capitalized embed comment": {source: "package fixture\n//go:Embed ignored\n"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			data := []byte(test.source)
			importSignal, embedSignal := indexCacheSourceMetadataSignals(data)
			if importSignal != test.importSignal || embedSignal != test.embedSignal {
				t.Fatalf("metadata signals = import:%t embed:%t, want import:%t embed:%t", importSignal, embedSignal, test.importSignal, test.embedSignal)
			}
			metadata := readIndexCacheSourceMetadata("fixture.go", data)
			if !test.importSignal && !test.embedSignal && (!metadata.valid || len(metadata.imports) != 0 || len(metadata.embedPatterns) != 0) {
				t.Fatalf("signal-free metadata = %+v, want valid and empty", metadata)
			}
		})
	}
}

// TestReadIndexCacheSourceMetadataDefersSignalFreeBodyValidation verifies the main parser owns body errors when no embed scan is required.
func TestReadIndexCacheSourceMetadataDefersSignalFreeBodyValidation(t *testing.T) {
	withoutEmbed := readIndexCacheSourceMetadata("fixture.go", []byte("package fixture\nimport \"fmt\"\n@\n"))
	if !withoutEmbed.valid || !reflect.DeepEqual(withoutEmbed.imports, []string{"fmt"}) {
		t.Fatalf("metadata without embed signal = %+v, want valid import metadata", withoutEmbed)
	}
	withEmbed := readIndexCacheSourceMetadata("fixture.go", []byte("package fixture\nimport _ \"embed\"\n//go:embed asset.txt\nvar asset string\n@\n"))
	if withEmbed.valid {
		t.Fatal("full embed scan accepted a lexical body error")
	}
}

// TestReadIndexCacheSourceMetadataAdversarialForms verifies imports and directives are recognized only in their grammatical contexts.
func TestReadIndexCacheSourceMetadataAdversarialForms(t *testing.T) {
	source := []byte("//go:embed before-package\npackage fixture\n\nimport /* gap */ (\n\t_ \"embed\"\n\talias `example.com/alias`\n\t. \"example.com/dot\"\n)\n\nvar quoted = \"//go:embed quoted\"\nvar raw = `//go:embed raw`\n/* //go:embed block */\n//go:embedded prefix\n// go:embed spaced\n///go:embed slash\n//go:embed plain\t\"space name\" `raw name` \"unicode\\u2003space\"\nvar files string\n")
	metadata := readIndexCacheSourceMetadata("fixture.go", source)
	if !metadata.valid {
		t.Fatal("valid adversarial source metadata was rejected")
	}
	wantImports := []string{"embed", "example.com/alias", "example.com/dot"}
	if !reflect.DeepEqual(metadata.imports, wantImports) {
		t.Fatalf("imports = %v, want %v", metadata.imports, wantImports)
	}
	wantPatterns := []string{"before-package", "plain", "raw name", "space name", "unicode\u2003space"}
	if !reflect.DeepEqual(metadata.embedPatterns, wantPatterns) {
		t.Fatalf("embed patterns = %q, want %q", metadata.embedPatterns, wantPatterns)
	}
}

// readIndexCacheSourceMetadataReference preserves the former two-pass implementation as a compatibility oracle.
func readIndexCacheSourceMetadataReference(filename string, data []byte) indexCacheSourceMetadata {
	metadata := indexCacheSourceMetadata{valid: true}
	if filepath.Ext(filename) != ".go" {
		return metadata
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, data, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		metadata.valid = false
		return metadata
	}
	for _, imported := range parsed.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			metadata.valid = false
			return metadata
		}
		metadata.imports = append(metadata.imports, importPath)
	}
	metadata.imports = sortedUniqueIndexCacheStrings(metadata.imports)

	fileSet := token.NewFileSet()
	file := fileSet.AddFile(filename, -1, len(data))
	var scanErrors int
	var sourceScanner scanner.Scanner
	sourceScanner.Init(file, data, func(token.Position, string) {
		scanErrors++
	}, scanner.ScanComments)
	for {
		_, scannedToken, literal := sourceScanner.Scan()
		if scannedToken == token.EOF {
			break
		}
		if scannedToken != token.COMMENT || !strings.HasPrefix(literal, "//go:embed") {
			continue
		}
		arguments, directive, ok := parseIndexCacheEmbedDirective(literal)
		if !directive {
			continue
		}
		if !ok {
			metadata.valid = false
			return metadata
		}
		metadata.embedPatterns = append(metadata.embedPatterns, arguments...)
	}
	if scanErrors != 0 {
		metadata.valid = false
		return metadata
	}
	metadata.embedPatterns = sortedUniqueIndexCacheStrings(metadata.embedPatterns)
	return metadata
}

// TestIndexCacheEmbedPatternTarget verifies glob patterns narrow traversal without excluding possible matches.
func TestIndexCacheEmbedPatternTarget(t *testing.T) {
	packageDirectory := filepath.Join(t.TempDir(), "package")
	tests := []struct {
		name    string
		pattern string
		want    string
		valid   bool
	}{
		{name: "exact file", pattern: "assets/schema.json", want: filepath.Join(packageDirectory, "assets", "schema.json"), valid: true},
		{name: "significant spaces", pattern: " leading and trailing ", want: filepath.Join(packageDirectory, " leading and trailing "), valid: true},
		{name: "directory glob", pattern: "frontend/dist/*", want: filepath.Join(packageDirectory, "frontend", "dist"), valid: true},
		{name: "file glob", pattern: "assets/*.json", want: filepath.Join(packageDirectory, "assets"), valid: true},
		{name: "character class", pattern: "assets/[ab]/*.json", want: filepath.Join(packageDirectory, "assets"), valid: true},
		{name: "package glob", pattern: "*.json", want: packageDirectory, valid: true},
		{name: "all prefix", pattern: "all:frontend/dist/*", want: filepath.Join(packageDirectory, "frontend", "dist"), valid: true},
		{name: "escaped glob", pattern: `assets/\*.json`, want: packageDirectory, valid: true},
		{name: "empty", pattern: "", valid: false},
		{name: "package dot", pattern: ".", valid: false},
		{name: "parent", pattern: "../assets", valid: false},
		{name: "empty segment", pattern: "assets//schema.json", valid: false},
		{name: "invalid glob", pattern: "assets/[", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, valid := indexCacheEmbedPatternTarget(packageDirectory, test.pattern)
			if valid != test.valid {
				t.Fatalf("valid = %t, want %t", valid, test.valid)
			}
			if valid && target.path != filepath.Clean(test.want) {
				t.Fatalf("target = %q, want %q", target.path, test.want)
			}
		})
	}
}

// TestIndexCacheEmbedStructureTracksShapeNotContent verifies static indexing follows embed membership without reacting to opaque bytes.
func TestIndexCacheEmbedStructureTracksShapeNotContent(t *testing.T) {
	root := writeIndexCacheInputsProject(t, map[string]string{
		"go.mod":               "module example.com/cacheinputs\n\ngo 1.25.0\n",
		"app.go":               "package app\n\nimport \"embed\"\n\n//go:embed frontend/dist/*\nvar assets embed.FS\n",
		"frontend/dist/app.js": "first payload",
		"frontend/node_modules/dependency/index.js": "unrelated payload",
	})
	baseline := requireIndexCacheInputsHash(t, root)

	writeIndexCacheInputsFile(t, filepath.Join(root, "frontend", "dist", "app.js"), "different content with a different size")
	if changed := requireIndexCacheInputsHash(t, root); changed != baseline {
		t.Fatal("embedded content bytes changed static index identity")
	}

	writeIndexCacheInputsFile(t, filepath.Join(root, "frontend", "node_modules", "other", "index.js"), "unrelated sibling")
	if changed := requireIndexCacheInputsHash(t, root); changed != baseline {
		t.Fatal("literal embed prefix traversed an unrelated sibling tree")
	}

	nestedPath := filepath.Join(root, "frontend", "dist", "nested", "new.js")
	writeIndexCacheInputsFile(t, nestedPath, "new member")
	if changed := requireIndexCacheInputsHash(t, root); changed == baseline {
		t.Fatal("adding an embedded path did not invalidate static index identity")
	}
	if err := os.RemoveAll(filepath.Dir(nestedPath)); err != nil {
		t.Fatalf("remove embedded nested directory: %v", err)
	}
	if restored := requireIndexCacheInputsHash(t, root); restored != baseline {
		t.Fatal("removing the embedded path did not restore static index identity")
	}

	if err := os.Rename(filepath.Join(root, "frontend", "dist", "app.js"), filepath.Join(root, "frontend", "dist", "main.js")); err != nil {
		t.Fatalf("rename embedded path: %v", err)
	}
	if changed := requireIndexCacheInputsHash(t, root); changed == baseline {
		t.Fatal("renaming an embedded path did not invalidate static index identity")
	}
}

// TestIndexCacheEmbedStructureIgnoresVCSDirectories verifies broad patterns do not traverse metadata Go never embeds.
func TestIndexCacheEmbedStructureIgnoresVCSDirectories(t *testing.T) {
	root := writeIndexCacheInputsProject(t, map[string]string{
		"go.mod":    "module example.com/cacheinputs\n\ngo 1.25.0\n",
		"app.go":    "package app\n\nimport \"embed\"\n\n//go:embed *\nvar assets embed.FS\n",
		"asset.txt": "asset",
	})
	baseline := requireIndexCacheInputsHash(t, root)
	writeIndexCacheInputsFile(t, filepath.Join(root, ".git", "objects", "large"), "irrelevant repository metadata")
	if changed := requireIndexCacheInputsHash(t, root); changed != baseline {
		t.Fatal("version-control metadata changed embed structure identity")
	}
}

// TestIndexCacheEmbedStructureTracksMissingAndLinkTargets verifies structural states cover absent and irregular inputs.
func TestIndexCacheEmbedStructureTracksMissingAndLinkTargets(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root := writeIndexCacheInputsProject(t, map[string]string{
			"go.mod": "module example.com/cacheinputs\n\ngo 1.25.0\n",
			"app.go": "package app\n\nimport _ \"embed\"\n\n//go:embed missing.txt\nvar asset string\n",
		})
		baseline := requireIndexCacheInputsHash(t, root)
		writeIndexCacheInputsFile(t, filepath.Join(root, "missing.txt"), "present")
		if changed := requireIndexCacheInputsHash(t, root); changed == baseline {
			t.Fatal("creating a missing embed target did not invalidate static index identity")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation is not reliably available on Windows")
		}
		root := writeIndexCacheInputsProject(t, map[string]string{
			"go.mod":     "module example.com/cacheinputs\n\ngo 1.25.0\n",
			"app.go":     "package app\n\nimport _ \"embed\"\n\n//go:embed asset.txt\nvar asset string\n",
			"first.txt":  "first",
			"second.txt": "second",
		})
		linkPath := filepath.Join(root, "asset.txt")
		if err := os.Symlink("first.txt", linkPath); err != nil {
			t.Skipf("create symlink fixture: %v", err)
		}
		baseline := requireIndexCacheInputsHash(t, root)
		if err := os.Remove(linkPath); err != nil {
			t.Fatalf("remove symlink fixture: %v", err)
		}
		if err := os.Symlink("second.txt", linkPath); err != nil {
			t.Fatalf("retarget symlink fixture: %v", err)
		}
		if changed := requireIndexCacheInputsHash(t, root); changed == baseline {
			t.Fatal("retargeting an embedded symlink did not invalidate static index identity")
		}
	})
}

// writeIndexCacheInputsProject writes a standalone module used by cache-input tests.
func writeIndexCacheInputsProject(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Setenv("GOWORK", "off")
	root := t.TempDir()
	for relative, contents := range files {
		writeIndexCacheInputsFile(t, filepath.Join(root, filepath.FromSlash(relative)), contents)
	}
	return root
}

// writeIndexCacheInputsFile writes one fixture file after creating its parent directory.
func writeIndexCacheInputsFile(t *testing.T, filename string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("create cache-input fixture directory: %v", err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatalf("write cache-input fixture %q: %v", filename, err)
	}
}

// requireIndexCacheInputsHash returns the cache identity for a fixture that must remain eligible.
func requireIndexCacheInputsHash(t *testing.T, root string) string {
	t.Helper()
	identity, cacheable, err := indexCacheInputHash(context.Background(), root, IndexOptions{})
	if err != nil {
		t.Fatalf("compute cache-input identity: %v", err)
	}
	if !cacheable {
		t.Fatal("cache-input fixture unexpectedly disabled caching")
	}
	return identity
}
