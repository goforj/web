package webindex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestRunPreservesUnchangedArtifactFiles verifies a repeated complete index does not create watcher-visible filesystem churn.
func TestRunPreservesUnchangedArtifactFiles(t *testing.T) {
	root := writeStabilityFixture(t)
	buildDir := filepath.Join(root, "build")
	paths := []string{
		filepath.Join(buildDir, "api_index.json"),
		filepath.Join(buildDir, "api_index.diagnostics.json"),
		filepath.Join(buildDir, "openapi.json"),
	}
	options := IndexOptions{
		Root:            root,
		OutPath:         paths[0],
		DiagnosticsPath: paths[1],
		OpenAPIPath:     paths[2],
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("run initial index: %v", err)
	}

	fixedTime := time.Unix(1_700_000_000, 123_000_000)
	before := make(map[string]os.FileInfo, len(paths))
	for _, path := range paths {
		if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
			t.Fatalf("set artifact times for %s: %v", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat initial artifact %s: %v", path, err)
		}
		before[path] = info
	}
	if err := os.Chtimes(buildDir, fixedTime, fixedTime); err != nil {
		t.Fatalf("set output directory times: %v", err)
	}
	directoryBefore, err := os.Stat(buildDir)
	if err != nil {
		t.Fatalf("stat initial output directory: %v", err)
	}

	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("rerun unchanged index: %v", err)
	}
	for _, path := range paths {
		after, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat unchanged artifact %s: %v", path, err)
		}
		if !after.ModTime().Equal(before[path].ModTime()) {
			t.Fatalf("artifact %s modtime = %s, want %s", path, after.ModTime(), before[path].ModTime())
		}
		if !os.SameFile(before[path], after) {
			t.Fatalf("unchanged artifact %s was replaced", path)
		}
	}
	directoryAfter, err := os.Stat(buildDir)
	if err != nil {
		t.Fatalf("stat unchanged output directory: %v", err)
	}
	// Platforms without advisory file locks use a short-lived guard directory, while artifact files remain unchanged.
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" || runtime.GOOS == "wasip1" {
		return
	}
	if !directoryAfter.ModTime().Equal(directoryBefore.ModTime()) {
		t.Fatalf("output directory modtime = %s, want %s", directoryAfter.ModTime(), directoryBefore.ModTime())
	}
}
