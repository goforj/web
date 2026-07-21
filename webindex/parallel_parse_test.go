package webindex

import (
	"context"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestParseSourcePathsPreservesInputOrderAndTokenPositions verifies worker completion order cannot change cross-file token identity.
func TestParseSourcePathsPreservesInputOrderAndTokenPositions(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 16)
	sources := make(map[string]string, 16)
	for index := 0; index < 16; index++ {
		name := filepath.Join("pkg", intToString(index), "source.go")
		source := "package sample\n\nvar Value" + intToString(index) + " = " + intToString(index) + "\n"
		if index == 0 {
			// The first input is deliberately expensive so later workers usually finish first.
			source += strings.Repeat("var _ = 1\n", 4096)
		}
		paths = append(paths, filepath.Join(root, name))
		sources[name] = source
	}
	writeFixtureFiles(t, root, sources)

	for iteration := 0; iteration < 20; iteration++ {
		fset := token.NewFileSet()
		results := parseSourcePaths(context.Background(), root, paths, fset, activeSourceBuildContext())
		if len(results) != len(paths) {
			t.Fatalf("iteration %d returned %d results, want %d", iteration, len(results), len(paths))
		}

		expectedBase := 1
		for index, result := range results {
			path := paths[index]
			source := sources[filepath.Join("pkg", intToString(index), "source.go")]
			if len(result.diagnostics) != 0 {
				t.Fatalf("iteration %d source %q produced diagnostics: %+v", iteration, path, result.diagnostics)
			}
			if result.parsed == nil || result.tokenFile == nil {
				t.Fatalf("iteration %d source %q was not parsed", iteration, path)
			}
			if result.parsed.Path != path {
				t.Fatalf("iteration %d result %d path = %q, want %q", iteration, index, result.parsed.Path, path)
			}
			if result.tokenFile.Name() != path {
				t.Fatalf("iteration %d token file %d name = %q, want %q", iteration, index, result.tokenFile.Name(), path)
			}
			if result.tokenFile.Base() != expectedBase {
				t.Fatalf("iteration %d token file %q base = %d, want %d", iteration, path, result.tokenFile.Base(), expectedBase)
			}
			if result.tokenFile.Size() != len(source) {
				t.Fatalf("iteration %d token file %q size = %d, want %d", iteration, path, result.tokenFile.Size(), len(source))
			}
			if got := int(result.parsed.File.Pos()); got != expectedBase {
				t.Fatalf("iteration %d parsed file %q position = %d, want %d", iteration, path, got, expectedBase)
			}
			position := fset.Position(token.Pos(expectedBase))
			if position.Filename != path || position.Offset != 0 || position.Line != 1 || position.Column != 1 {
				t.Fatalf("iteration %d token position for %q = %+v", iteration, path, position)
			}
			expectedBase += len(source) + 1
		}

		files := make([]string, 0, len(paths))
		fset.Iterate(func(file *token.File) bool {
			files = append(files, file.Name())
			return true
		})
		if !reflect.DeepEqual(files, paths) {
			t.Fatalf("iteration %d file-set order = %q, want %q", iteration, files, paths)
		}
	}
}

// TestParseSourcePathsStopsBeforeParsingAfterCancellation verifies cancellation between source loading and parsing cannot publish partial syntax.
func TestParseSourcePathsStopsBeforeParsingAfterCancellation(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 32)
	files := make(map[string]string, 32)
	for index := 0; index < 32; index++ {
		name := filepath.Join("pkg", intToString(index), "source.go")
		paths = append(paths, filepath.Join(root, name))
		files[name] = "//go:build " + activeSourceBuildContext().GOOS + "\n\npackage sample\n"
	}
	writeFixtureFiles(t, root, files)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	matchStarted := make(chan struct{}, 1)
	releaseMatch := make(chan struct{})
	buildContext := activeSourceBuildContext()
	buildContext.OpenFile = func(path string) (io.ReadCloser, error) {
		select {
		case matchStarted <- struct{}{}:
		default:
		}
		<-releaseMatch
		return os.Open(path)
	}
	fset := token.NewFileSet()
	resultChannel := make(chan []sourceParseResult, 1)
	go func() {
		resultChannel <- parseSourcePaths(ctx, root, paths, fset, buildContext)
	}()

	select {
	case <-matchStarted:
		cancel()
		close(releaseMatch)
	case <-time.After(5 * time.Second):
		close(releaseMatch)
		t.Fatal("source matching did not start")
	}

	var results []sourceParseResult
	select {
	case results = <-resultChannel:
	case <-time.After(5 * time.Second):
		t.Fatal("parallel parsing did not stop after cancellation")
	}
	if len(results) != len(paths) {
		t.Fatalf("canceled parse returned %d results, want %d", len(results), len(paths))
	}
	for index, result := range results {
		if result.parsed != nil || result.tokenFile != nil || len(result.diagnostics) != 0 {
			t.Fatalf("canceled parse retained partial result %d: %+v", index, result)
		}
	}
	fileCount := 0
	fset.Iterate(func(_ *token.File) bool {
		fileCount++
		return true
	})
	if fileCount != 0 {
		t.Fatalf("canceled parse registered %d token files", fileCount)
	}
}
