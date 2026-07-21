package webindex

import (
	"bytes"
	"context"
	"go/scanner"
	"go/token"
	"hash"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/mod/modfile"
)

// indexCacheSourceMetadata retains dependency and embed inputs discovered while source bytes are already in memory for hashing.
type indexCacheSourceMetadata struct {
	imports       []string
	embedPatterns []string
	valid         bool
}

// indexCacheLocalModule maps import-path prefixes to local module roots without consulting the Go package driver.
type indexCacheLocalModule struct {
	importPath string
	root       string
}

// indexCacheEmbedTarget identifies the narrowest tree that can contain matches for one embed pattern.
type indexCacheEmbedTarget struct {
	path string
}

// indexCacheSourceMetadataScanner extracts header dependencies and embed directives during one lexical pass.
type indexCacheSourceMetadataScanner struct {
	scanner         scanner.Scanner
	metadata        indexCacheSourceMetadata
	validateLexical bool
}

// readIndexCacheSourceMetadata parses dependency and embed metadata from the same bytes used by the content fingerprint.
func readIndexCacheSourceMetadata(filename string, data []byte) indexCacheSourceMetadata {
	metadata := indexCacheSourceMetadata{valid: true}
	if filepath.Ext(filename) != ".go" {
		return metadata
	}
	importSignal, embedSignal := indexCacheSourceMetadataSignals(data)
	if !importSignal && !embedSignal {
		// Cache publication rejects parser diagnostics, so signal-free files need no redundant lexical pass here.
		return metadata
	}

	fileSet := token.NewFileSet()
	file := fileSet.AddFile(filename, -1, len(data))
	reader := indexCacheSourceMetadataScanner{metadata: metadata, validateLexical: embedSignal}
	reader.scanner.Init(file, data, reader.recordError, scanner.ScanComments)
	if !reader.readHeader() {
		return indexCacheSourceMetadata{valid: false}
	}
	// The full source parser owns body lexing unless directives require this scanner to inspect the body too.
	if embedSignal {
		reader.drain()
	}
	reader.metadata.imports = sortedUniqueIndexCacheStrings(reader.metadata.imports)
	reader.metadata.embedPatterns = sortedUniqueIndexCacheStrings(reader.metadata.embedPatterns)
	return reader.metadata
}

// indexCacheSourceMetadataSignals reports which metadata forms can occur in the raw source bytes.
func indexCacheSourceMetadataSignals(data []byte) (bool, bool) {
	return bytes.Contains(data, []byte("import")), bytes.Contains(data, []byte("go:embed"))
}

// recordError marks scanner failures without emitting diagnostics during a cache eligibility check.
func (reader *indexCacheSourceMetadataScanner) recordError(token.Position, string) {
	if reader.validateLexical {
		reader.metadata.valid = false
	}
}

// nextCodeToken processes comment directives and returns the next non-comment token.
func (reader *indexCacheSourceMetadataScanner) nextCodeToken() (token.Token, string) {
	for {
		_, scannedToken, literal := reader.scanner.Scan()
		if scannedToken != token.COMMENT {
			return scannedToken, literal
		}
		arguments, directive, ok := parseIndexCacheEmbedDirective(literal)
		if !directive {
			continue
		}
		if !ok {
			reader.metadata.valid = false
			continue
		}
		reader.metadata.embedPatterns = append(reader.metadata.embedPatterns, arguments...)
	}
}

// readHeader validates the package clause and contiguous import declarations accepted by parser.ImportsOnly.
func (reader *indexCacheSourceMetadataScanner) readHeader() bool {
	parsedToken, _ := reader.nextCodeToken()
	if parsedToken != token.PACKAGE {
		return false
	}
	parsedToken, _ = reader.nextCodeToken()
	if parsedToken != token.IDENT {
		return false
	}
	parsedToken, _ = reader.nextCodeToken()
	if parsedToken != token.SEMICOLON {
		return false
	}
	parsedToken, _ = reader.nextCodeToken()
	for parsedToken == token.IMPORT {
		if !reader.readImportDeclaration() {
			return false
		}
		parsedToken, _ = reader.nextCodeToken()
	}
	return true
}

// readImportDeclaration validates and records one single or parenthesized import declaration.
func (reader *indexCacheSourceMetadataScanner) readImportDeclaration() bool {
	parsedToken, literal := reader.nextCodeToken()
	if parsedToken != token.LPAREN {
		if !reader.readImportSpec(parsedToken, literal) {
			return false
		}
		parsedToken, _ = reader.nextCodeToken()
		return parsedToken == token.SEMICOLON
	}

	parsedToken, literal = reader.nextCodeToken()
	for parsedToken != token.RPAREN {
		if parsedToken == token.EOF || !reader.readImportSpec(parsedToken, literal) {
			return false
		}
		parsedToken, literal = reader.nextCodeToken()
		if parsedToken == token.RPAREN {
			break
		}
		if parsedToken != token.SEMICOLON {
			return false
		}
		parsedToken, literal = reader.nextCodeToken()
	}
	parsedToken, _ = reader.nextCodeToken()
	return parsedToken == token.SEMICOLON
}

// readImportSpec accepts optional aliases and unquotes the required import-path string.
func (reader *indexCacheSourceMetadataScanner) readImportSpec(parsedToken token.Token, literal string) bool {
	if parsedToken == token.IDENT || parsedToken == token.PERIOD {
		parsedToken, literal = reader.nextCodeToken()
	}
	if parsedToken != token.STRING {
		return false
	}
	importPath, err := strconv.Unquote(literal)
	if err != nil {
		return false
	}
	reader.metadata.imports = append(reader.metadata.imports, importPath)
	return true
}

// drain completes lexical validation and discovers directives after the import header.
func (reader *indexCacheSourceMetadataScanner) drain() {
	for {
		parsedToken, _ := reader.nextCodeToken()
		if parsedToken == token.EOF {
			return
		}
	}
}

// parseIndexCacheEmbedDirective accepts the quoted and unquoted argument forms supported by Go 1.25 embed directives.
func parseIndexCacheEmbedDirective(comment string) ([]string, bool, bool) {
	const prefix = "//go:embed"
	if !strings.HasPrefix(comment, prefix) {
		return nil, false, true
	}
	if len(comment) > len(prefix) && comment[len(prefix)] != ' ' && comment[len(prefix)] != '\t' {
		return nil, false, true
	}
	remainder := comment[len(prefix):]
	patterns := make([]string, 0, 1)
	for {
		remainder = strings.TrimLeftFunc(remainder, unicode.IsSpace)
		if remainder == "" {
			return patterns, true, len(patterns) != 0
		}
		if remainder[0] == '"' || remainder[0] == '`' {
			quoted, err := strconv.QuotedPrefix(remainder)
			if err != nil {
				return nil, true, false
			}
			value, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, true, false
			}
			remainder = remainder[len(quoted):]
			if remainder != "" {
				next, _ := utf8.DecodeRuneInString(remainder)
				if !unicode.IsSpace(next) {
					return nil, true, false
				}
			}
			patterns = append(patterns, value)
			continue
		}
		separator := len(remainder)
		for index, character := range remainder {
			if unicode.IsSpace(character) {
				separator = index
				break
			}
		}
		if separator == len(remainder) {
			patterns = append(patterns, remainder)
			return patterns, true, true
		}
		patterns = append(patterns, remainder[:separator])
		remainder = remainder[separator:]
	}
}

// indexCacheLocalImportsCovered rejects local dependency edges that cross source trees intentionally excluded from indexing.
func indexCacheLocalImportsCovered(root string, moduleFile *modfile.File, files []indexCacheFileDigest, inputs []indexCacheInputFile) bool {
	modules, ok := indexCacheLocalModules(root, moduleFile, inputs)
	if !ok {
		return false
	}
	for _, file := range files {
		if !file.metadata.valid {
			return false
		}
		for _, importPath := range file.metadata.imports {
			module, relative, local := resolveIndexCacheLocalImport(modules, importPath)
			if !local {
				continue
			}
			if module.root == "" || indexCacheImportCrossesIgnoredDirectory(relative) {
				return false
			}
		}
	}
	return true
}

// indexCacheLocalModules derives local import mappings from the main module and path-only replacements.
func indexCacheLocalModules(root string, moduleFile *modfile.File, inputs []indexCacheInputFile) ([]indexCacheLocalModule, bool) {
	if moduleFile == nil || moduleFile.Module == nil || strings.TrimSpace(moduleFile.Module.Mod.Path) == "" {
		return nil, false
	}
	byImportPath := map[string]string{}
	add := func(importPath string, moduleRoot string) bool {
		importPath = strings.TrimSpace(importPath)
		moduleRoot = filepath.Clean(moduleRoot)
		if importPath == "" || moduleRoot == "" {
			return false
		}
		if existing, exists := byImportPath[importPath]; exists && existing != moduleRoot {
			return false
		}
		byImportPath[importPath] = moduleRoot
		return true
	}
	if !add(moduleFile.Module.Mod.Path, root) {
		return nil, false
	}
	for _, replacement := range moduleFile.Replace {
		if replacement == nil || replacement.New.Version != "" {
			continue
		}
		replacementRoot := replacement.New.Path
		if !filepath.IsAbs(replacementRoot) {
			replacementRoot = filepath.Join(root, replacementRoot)
		}
		absoluteRoot, err := filepath.Abs(replacementRoot)
		if err != nil {
			return nil, false
		}
		absoluteRoot = filepath.Clean(absoluteRoot)
		if !add(replacement.Old.Path, absoluteRoot) {
			return nil, false
		}
		replacementData, ok := indexCacheInputFileData(inputs, filepath.Join(absoluteRoot, "go.mod"))
		if !ok {
			return nil, false
		}
		replacementFile, err := modfile.Parse(filepath.Join(absoluteRoot, "go.mod"), replacementData, nil)
		if err != nil || replacementFile.Module == nil {
			return nil, false
		}
		if !add(replacementFile.Module.Mod.Path, absoluteRoot) {
			return nil, false
		}
	}

	modules := make([]indexCacheLocalModule, 0, len(byImportPath))
	for importPath, moduleRoot := range byImportPath {
		modules = append(modules, indexCacheLocalModule{importPath: importPath, root: moduleRoot})
	}
	sort.Slice(modules, func(left int, right int) bool {
		if len(modules[left].importPath) != len(modules[right].importPath) {
			return len(modules[left].importPath) > len(modules[right].importPath)
		}
		return modules[left].importPath < modules[right].importPath
	})
	return modules, true
}

// indexCacheInputFileData returns only present bytes from the immutable configuration snapshot.
func indexCacheInputFileData(inputs []indexCacheInputFile, path string) ([]byte, bool) {
	path = filepath.Clean(path)
	for _, input := range inputs {
		if input.path == path {
			return input.data, input.present
		}
	}
	return nil, false
}

// indexCacheInputFilePresent distinguishes an initially absent optional input from one whose captured contents are empty.
func indexCacheInputFilePresent(inputs []indexCacheInputFile, path string) bool {
	path = filepath.Clean(path)
	for _, input := range inputs {
		if input.path == path {
			return input.present
		}
	}
	return false
}

// resolveIndexCacheLocalImport resolves one import against the longest matching local module prefix.
func resolveIndexCacheLocalImport(modules []indexCacheLocalModule, importPath string) (indexCacheLocalModule, string, bool) {
	for _, module := range modules {
		if importPath == module.importPath {
			return module, "", true
		}
		prefix := module.importPath + "/"
		if strings.HasPrefix(importPath, prefix) {
			return module, strings.TrimPrefix(importPath, prefix), true
		}
	}
	return indexCacheLocalModule{}, "", false
}

// indexCacheImportCrossesIgnoredDirectory reports imports whose physical source is outside the indexer's parsed tree.
func indexCacheImportCrossesIgnoredDirectory(relativeImportPath string) bool {
	for _, segment := range strings.Split(relativeImportPath, "/") {
		if sourceDirectoryIgnored(segment) {
			return true
		}
	}
	return false
}

// writeIndexCacheEmbedStructure fingerprints path membership and file kinds because embedded bytes do not affect static API contracts.
func writeIndexCacheEmbedStructure(ctx context.Context, digest hash.Hash, files []indexCacheFileDigest) (bool, error) {
	targets := make([]indexCacheEmbedTarget, 0)
	for _, file := range files {
		if !file.metadata.valid {
			return false, nil
		}
		for _, pattern := range file.metadata.embedPatterns {
			target, ok := indexCacheEmbedPatternTarget(filepath.Dir(file.path), pattern)
			if !ok {
				return false, nil
			}
			targets = append(targets, target)
		}
	}
	sort.Slice(targets, func(left int, right int) bool {
		return targets[left].path < targets[right].path
	})
	lastPath := ""
	for _, target := range targets {
		if target.path == lastPath {
			continue
		}
		lastPath = target.path
		if err := writeIndexCacheStructuralPath(ctx, digest, target.path); err != nil {
			return false, err
		}
	}
	return true, nil
}

// indexCacheEmbedPatternTarget returns the literal directory prefix that contains every possible pattern match.
func indexCacheEmbedPatternTarget(packageDirectory string, patternValue string) (indexCacheEmbedTarget, bool) {
	patternValue = strings.TrimPrefix(patternValue, "all:")
	if patternValue == "" || patternValue == "." || !fs.ValidPath(patternValue) {
		return indexCacheEmbedTarget{}, false
	}
	if _, err := path.Match(patternValue, ""); err != nil {
		return indexCacheEmbedTarget{}, false
	}

	relativeTarget := patternValue
	if strings.Contains(patternValue, `\`) {
		relativeTarget = ""
	} else if wildcard := firstIndexCacheGlobMeta(patternValue); wildcard >= 0 {
		literalPrefix := patternValue[:wildcard]
		if slash := strings.LastIndex(literalPrefix, "/"); slash >= 0 {
			relativeTarget = literalPrefix[:slash]
		} else {
			relativeTarget = ""
		}
	}
	targetPath := filepath.Clean(packageDirectory)
	if relativeTarget != "" {
		targetPath = filepath.Join(targetPath, filepath.FromSlash(relativeTarget))
	}
	relative, err := filepath.Rel(filepath.Clean(packageDirectory), targetPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return indexCacheEmbedTarget{}, false
	}
	return indexCacheEmbedTarget{path: filepath.Clean(targetPath)}, true
}

// firstIndexCacheGlobMeta finds the first unescaped pattern metacharacter.
func firstIndexCacheGlobMeta(patternValue string) int {
	escaped := false
	for index := 0; index < len(patternValue); index++ {
		character := patternValue[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '*' || character == '?' || character == '[' {
			return index
		}
	}
	return -1
}

// writeIndexCacheStructuralPath records only filesystem shape because embed content is opaque to static indexing.
func writeIndexCacheStructuralPath(ctx context.Context, digest hash.Hash, targetPath string) error {
	writeIndexCacheHashString(digest, "embed-structure-v1")
	writeIndexCacheHashString(digest, filepath.Clean(targetPath))
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		writeIndexCacheHashString(digest, "missing")
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return writeIndexCacheStructuralEntry(digest, targetPath, ".", info)
	}
	if indexCacheEmbedVCSDirectory(filepath.Base(targetPath)) {
		return writeIndexCacheStructuralEntry(digest, targetPath, ".", info)
	}
	return filepath.WalkDir(targetPath, func(entryPath string, entry fs.DirEntry, walkErr error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if walkErr != nil {
			return walkErr
		}
		if entryPath != targetPath && entry.IsDir() && indexCacheEmbedVCSDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		relative, relativeErr := filepath.Rel(targetPath, entryPath)
		if relativeErr != nil {
			return relativeErr
		}
		return writeIndexCacheStructuralEntry(digest, entryPath, filepath.ToSlash(relative), entryInfo)
	})
}

// indexCacheEmbedVCSDirectory matches directories the Go command always excludes from embedded package data.
func indexCacheEmbedVCSDirectory(name string) bool {
	switch name {
	case ".bzr", ".git", ".hg", ".svn":
		return true
	default:
		return false
	}
}

// writeIndexCacheStructuralEntry records names, modes, and link destinations while intentionally excluding regular-file bytes.
func writeIndexCacheStructuralEntry(digest hash.Hash, entryPath string, relative string, info fs.FileInfo) error {
	writeIndexCacheHashString(digest, relative)
	writeIndexCacheHashString(digest, info.Mode().String())
	if info.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	target, err := os.Readlink(entryPath)
	if err != nil {
		return err
	}
	writeIndexCacheHashString(digest, target)
	return nil
}

// sortedUniqueIndexCacheStrings returns stable metadata without making source declaration order part of cache policy.
func sortedUniqueIndexCacheStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	output := values[:0]
	for _, value := range values {
		if len(output) != 0 && output[len(output)-1] == value {
			continue
		}
		output = append(output, value)
	}
	return output
}
