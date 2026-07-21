package webindex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPublishJSONArtifactsValidatesBeforeWriting protects the last coherent
// artifact set when a later value cannot be serialized.
func TestPublishJSONArtifactsValidatesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("previous\n"), 0o644); err != nil {
		t.Fatalf("write previous manifest: %v", err)
	}

	changed, err := publishJSONArtifacts([]jsonArtifact{
		{path: manifestPath, value: map[string]any{"version": "2"}},
		{path: filepath.Join(root, "openapi.json"), value: func() {}},
	})
	if err == nil {
		t.Fatal("expected unsupported JSON value to fail")
	}
	if changed {
		t.Fatal("expected validation failure before filesystem changes")
	}
	content, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatalf("read preserved manifest: %v", readErr)
	}
	if string(content) != "previous\n" {
		t.Fatalf("manifest changed before complete validation: %q", content)
	}
}

// TestPublishJSONArtifactsSkipsIdenticalContent verifies that routine builds do
// not replace files whose deterministic bytes have not changed.
func TestPublishJSONArtifactsSkipsIdenticalContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	artifacts := []jsonArtifact{{path: path, value: map[string]any{"openapi": "3.0.3"}}}

	changed, err := publishJSONArtifacts(artifacts)
	if err != nil {
		t.Fatalf("publish initial artifact: %v", err)
	}
	if !changed {
		t.Fatal("expected initial publication to report a change")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat initial artifact: %v", err)
	}

	changed, err = publishJSONArtifacts(artifacts)
	if err != nil {
		t.Fatalf("republish identical artifact: %v", err)
	}
	if changed {
		t.Fatal("expected identical publication to report no change")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat unchanged artifact: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("unchanged artifact was replaced: before=%s after=%s", before.ModTime(), after.ModTime())
	}
}

// TestPublishJSONArtifactsUsesCompleteAtomicFiles checks the public byte shape
// and ensures temporary files are removed after publication.
func TestPublishJSONArtifactsUsesCompleteAtomicFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "api_index.json")

	changed, err := publishJSONArtifacts([]jsonArtifact{{path: path, value: map[string]any{"version": "2"}}})
	if err != nil {
		t.Fatalf("publish artifact: %v", err)
	}
	if !changed {
		t.Fatal("expected new artifact to report a change")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(content) != "{\n  \"version\": \"2\"\n}\n" {
		t.Fatalf("unexpected artifact bytes: %q", content)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".api_index.json.tmp-*"))
	if err != nil {
		t.Fatalf("find temporary artifacts: %v", err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary artifacts remain after publication: %v", temporary)
	}
}

// TestPublishJSONArtifactsScavengesCrashedCandidates verifies the next lock holder removes a fully staged file abandoned by process death.
func TestPublishJSONArtifactsScavengesCrashedCandidates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	command := exec.Command(os.Args[0], "-test.run=^TestArtifactPublicationCrashSubprocessHelper$")
	command.Env = append(os.Environ(), "WEBINDEX_ARTIFACT_CRASH_PATH="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run crashing artifact publisher: %v\n%s", err, output)
	}
	candidates, err := filepath.Glob(filepath.Join(root, ".api_index.json.tmp-*"))
	if err != nil {
		t.Fatalf("find crashed artifact candidate: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("crashing publisher left %d candidates, want 1: %v", len(candidates), candidates)
	}
	changed, err := publishEncodedJSONArtifacts([]encodedJSONArtifact{{path: path, data: []byte("final\n")}}, os.Rename)
	if err != nil || !changed {
		t.Fatalf("publish after process crash: changed=%t err=%v", changed, err)
	}
	assertNoArtifactCandidates(t, root)
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "final\n" {
		t.Fatalf("read post-crash artifact: contents=%q err=%v", contents, err)
	}
}

// TestArtifactPublicationCrashSubprocessHelper exits after durable staging so the parent can exercise crash scavenging.
func TestArtifactPublicationCrashSubprocessHelper(t *testing.T) {
	path := os.Getenv("WEBINDEX_ARTIFACT_CRASH_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	encoded := []encodedJSONArtifact{{path: path, data: []byte("abandoned\n")}}
	_, err := publishIndexCacheArtifactsValidated(context.Background(), encoded, os.Rename, func(context.Context) error {
		os.Exit(0)
		return nil
	})
	t.Fatalf("crash helper returned instead of exiting: %v", err)
}

// TestPublishJSONArtifactsScavengesOnlyRecognizedRegularCandidates verifies cleanup cannot follow links or remove unrelated prefix-sharing entries.
func TestPublishJSONArtifactsScavengesOnlyRecognizedRegularCandidates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	if err := os.WriteFile(path, []byte("same\n"), 0o644); err != nil {
		t.Fatalf("write stable artifact: %v", err)
	}
	prefix := filepath.Join(root, jsonArtifactTemporaryPrefix(path))
	currentCandidate := prefix + "current" + jsonArtifactTemporaryMarker
	legacyCandidate := prefix + "123456"
	unrelated := prefix + "notes"
	directory := prefix + "789"
	symlink := prefix + "linked" + jsonArtifactTemporaryMarker
	for candidate, contents := range map[string]string{
		currentCandidate: "current orphan",
		legacyCandidate:  "legacy orphan",
		unrelated:        "user data",
	} {
		if err := os.WriteFile(candidate, []byte(contents), 0o644); err != nil {
			t.Fatalf("write cleanup fixture %q: %v", candidate, err)
		}
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("create candidate-shaped directory: %v", err)
	}
	if err := os.Symlink(unrelated, symlink); err != nil {
		t.Skipf("create candidate-shaped symlink: %v", err)
	}

	changed, err := publishEncodedJSONArtifacts([]encodedJSONArtifact{{path: path, data: []byte("same\n")}}, os.Rename)
	if err != nil || changed {
		t.Fatalf("publish unchanged artifact during cleanup: changed=%t err=%v", changed, err)
	}
	if _, statErr := os.Lstat(currentCandidate); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marked orphan remains at %q: %v", currentCandidate, statErr)
	}
	for _, preserved := range []string{legacyCandidate, unrelated, directory, symlink} {
		if _, statErr := os.Lstat(preserved); statErr != nil {
			t.Fatalf("non-candidate entry %q was removed: %v", preserved, statErr)
		}
	}
}

// TestPublishJSONArtifactsScavengesAfterLockWait verifies a waiter never removes candidates before it owns the directory transaction.
func TestPublishJSONArtifactsScavengesAfterLockWait(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	orphan := filepath.Join(root, jsonArtifactTemporaryPrefix(path)+"orphan"+jsonArtifactTemporaryMarker)
	if err := os.WriteFile(orphan, []byte("orphan"), 0o644); err != nil {
		t.Fatalf("write lock-wait orphan: %v", err)
	}
	lock, err := AcquireArtifactPublicationLock(context.Background(), path)
	if err != nil {
		t.Fatalf("hold artifact publication lock: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, publishErr := publishEncodedJSONArtifacts([]encodedJSONArtifact{{path: path, data: []byte("published\n")}}, os.Rename)
		result <- publishErr
	}()
	select {
	case publishErr := <-result:
		_ = lock.Release()
		t.Fatalf("waiting publisher bypassed held lock: %v", publishErr)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(orphan); err != nil {
		_ = lock.Release()
		t.Fatalf("waiter scavenged before lock ownership: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release artifact publication lock: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("publish after lock wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not resume after lock release")
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan survived completed locked publication: %v", err)
	}
}

// TestPublishEncodedJSONArtifactsRollsBackRenameFailure verifies a late filesystem error cannot expose a mixed artifact generation.
func TestPublishEncodedJSONArtifactsRollsBackRenameFailure(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "api_index.json")
	openAPIPath := filepath.Join(root, "openapi.json")
	for _, path := range []string{manifestPath, openAPIPath} {
		if err := os.WriteFile(path, []byte("previous:"+filepath.Base(path)+"\n"), 0o644); err != nil {
			t.Fatalf("write previous artifact %q: %v", path, err)
		}
	}

	renameCount := 0
	changed, err := publishEncodedJSONArtifacts([]encodedJSONArtifact{
		{path: manifestPath, data: []byte("new manifest\n")},
		{path: openAPIPath, data: []byte("new openapi\n")},
	}, func(source string, destination string) error {
		renameCount++
		if renameCount == 2 {
			return errors.New("injected rename failure")
		}
		return os.Rename(source, destination)
	})
	if err == nil {
		t.Fatal("expected injected rename failure")
	}
	if changed {
		t.Fatal("rolled-back publication must not report a visible change")
	}
	for _, path := range []string{manifestPath, openAPIPath} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read rolled-back artifact %q: %v", path, readErr)
		}
		want := "previous:" + filepath.Base(path) + "\n"
		if string(contents) != want {
			t.Fatalf("artifact %q mixed generations: got %q want %q", path, contents, want)
		}
	}
	temporary, globErr := filepath.Glob(filepath.Join(root, ".*.tmp-*"))
	if globErr != nil {
		t.Fatalf("find transaction candidates: %v", globErr)
	}
	if len(temporary) != 0 {
		t.Fatalf("transaction candidates remain after rollback: %v", temporary)
	}
}

// TestPublishJSONArtifactsRejectsDuplicateCanonicalPaths verifies artifact roles cannot silently overwrite one another through relative path aliases.
func TestPublishJSONArtifactsRejectsDuplicateCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	changed, err := publishJSONArtifacts([]jsonArtifact{
		{path: path, value: map[string]any{"kind": "manifest"}},
		{path: filepath.Join(root, ".", "api_index.json"), value: map[string]any{"kind": "diagnostics"}},
	})
	if err == nil || !strings.Contains(err.Error(), "same destination") {
		t.Fatalf("expected duplicate destination error, got changed=%t err=%v", changed, err)
	}
	if changed {
		t.Fatal("duplicate validation must happen before publication")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("duplicate destination created an artifact: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, artifactLockFilename)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("duplicate validation created a lock file: %v", statErr)
	}
}

// TestPublishJSONArtifactsRejectsDuplicatePhysicalPaths verifies symlink and case aliases cannot assign multiple roles to one output.
func TestPublishJSONArtifactsRejectsDuplicatePhysicalPaths(t *testing.T) {
	t.Run("symlinked directory", func(t *testing.T) {
		parent := t.TempDir()
		realDirectory := filepath.Join(parent, "real")
		if err := os.Mkdir(realDirectory, 0o755); err != nil {
			t.Fatalf("create artifact directory: %v", err)
		}
		aliasDirectory := filepath.Join(parent, "alias")
		if err := os.Symlink(realDirectory, aliasDirectory); err != nil {
			t.Skipf("create artifact directory alias: %v", err)
		}
		realPath := filepath.Join(realDirectory, "api_index.json")
		aliasPath := filepath.Join(aliasDirectory, "api_index.json")
		changed, err := publishJSONArtifacts([]jsonArtifact{
			{path: realPath, value: map[string]any{"kind": "manifest"}},
			{path: aliasPath, value: map[string]any{"kind": "diagnostics"}},
		})
		if err == nil || !strings.Contains(err.Error(), "same destination") {
			t.Fatalf("expected symlinked destination error, got changed=%t err=%v", changed, err)
		}
		if changed {
			t.Fatal("symlinked destination validation must happen before publication")
		}
		if _, statErr := os.Stat(realPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("symlinked destination created an artifact: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(realDirectory, artifactLockFilename)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("symlinked destination created a lock file: %v", statErr)
		}
	})

	t.Run("case-only path", func(t *testing.T) {
		root := t.TempDir()
		upperPath := filepath.Join(root, "API_INDEX.json")
		lowerPath := filepath.Join(root, "api_index.JSON")
		changed, err := publishJSONArtifacts([]jsonArtifact{
			{path: upperPath, value: map[string]any{"kind": "manifest"}},
			{path: lowerPath, value: map[string]any{"kind": "diagnostics"}},
		})
		if err == nil || !strings.Contains(err.Error(), "same destination") {
			t.Fatalf("expected case-only destination error, got changed=%t err=%v", changed, err)
		}
		if changed {
			t.Fatal("case-only destination validation must happen before publication")
		}
		for _, path := range []string{upperPath, lowerPath} {
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("case-only destination created an artifact: %v", statErr)
			}
		}
	})
}

// TestPublishJSONArtifactsLockWaitHonorsCancellation verifies a canceled build does not wait indefinitely behind another publisher.
func TestPublishJSONArtifactsLockWaitHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	lock, err := AcquireArtifactPublicationLock(context.Background(), filepath.Join(root, "held.json"))
	if err != nil {
		t.Fatalf("acquire lock fixture: %v", err)
	}
	defer func() {
		_ = lock.Release()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	path := filepath.Join(root, "api_index.json")
	changed, err := publishJSONArtifactsContext(ctx, []jsonArtifact{{path: path, value: map[string]any{"version": "2"}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected lock wait cancellation, got changed=%t err=%v", changed, err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canceled lock wait created an artifact: %v", statErr)
	}
}

// TestPublishJSONArtifactsCancellationAfterLock avoids filesystem mutation when cancellation arrives after lock acquisition but before staging.
func TestPublishJSONArtifactsCancellationAfterLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	encoded := []encodedJSONArtifact{{path: path, data: []byte("new\n")}}
	ctx, cancel := context.WithCancel(context.Background())
	locks, err := acquireArtifactDirectoryLocks(ctx, encoded)
	if err != nil {
		t.Fatalf("acquire locks: %v", err)
	}
	t.Cleanup(func() { _ = releaseArtifactDirectoryLocks(locks) })
	cancel()

	changed, err := publishEncodedJSONArtifactsLocked(ctx, encoded, os.Rename)
	if !errors.Is(err, context.Canceled) || changed {
		t.Fatalf("expected canceled locked publication, got changed=%t err=%v", changed, err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canceled publication created an artifact: %v", statErr)
	}
	assertNoArtifactCandidates(t, root)
}

// TestPublishJSONArtifactsCancellationRollsBackVisibleRenames preserves the prior generation when cancellation arrives mid-publication.
func TestPublishJSONArtifactsCancellationRollsBackVisibleRenames(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "api_index.json")
	second := filepath.Join(root, "openapi.json")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("old:"+filepath.Base(path)+"\n"), 0o644); err != nil {
			t.Fatalf("write previous artifact: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	renames := 0
	changed, err := publishEncodedJSONArtifactsContext(ctx, []encodedJSONArtifact{
		{path: first, data: []byte("new:first\n")},
		{path: second, data: []byte("new:second\n")},
	}, func(source string, destination string) error {
		if err := os.Rename(source, destination); err != nil {
			return err
		}
		renames++
		if renames == 1 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || changed {
		t.Fatalf("expected canceled transaction, got changed=%t err=%v", changed, err)
	}
	for _, path := range []string{first, second} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read rolled-back artifact: %v", readErr)
		}
		want := "old:" + filepath.Base(path) + "\n"
		if string(contents) != want {
			t.Fatalf("artifact %s was not rolled back: got=%q want=%q", path, contents, want)
		}
	}
	assertNoArtifactCandidates(t, root)
}

// TestPublishJSONArtifactsSerializesGoroutines verifies the process-local registry protects platforms whose advisory locks are process-associated.
func TestPublishJSONArtifactsSerializesGoroutines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	firstRenamed := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		_, err := publishEncodedJSONArtifacts([]encodedJSONArtifact{{path: path, data: []byte("first\n")}}, func(source string, destination string) error {
			if err := os.Rename(source, destination); err != nil {
				return err
			}
			close(firstRenamed)
			<-releaseFirst
			return nil
		})
		firstResult <- err
	}()
	select {
	case <-firstRenamed:
	case <-time.After(5 * time.Second):
		t.Fatal("first publisher did not reach rename")
	}

	secondResult := make(chan error, 1)
	go func() {
		_, err := publishEncodedJSONArtifacts([]encodedJSONArtifact{{path: path, data: []byte("second\n")}}, os.Rename)
		secondResult <- err
	}()
	select {
	case err := <-secondResult:
		close(releaseFirst)
		t.Fatalf("second publisher bypassed process-local lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first publisher failed: %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second publisher failed: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final artifact: %v", err)
	}
	if string(contents) != "second\n" {
		t.Fatalf("unexpected final generation: %q", contents)
	}
}

// TestAcquireArtifactPublicationLockCoordinatesDirectPublishers verifies external publishers join webindex's process-local registry and OS protocol.
func TestAcquireArtifactPublicationLockCoordinatesDirectPublishers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api_index.json")
	lock, err := AcquireArtifactPublicationLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire shared publication lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	changed, err := publishJSONArtifactsContext(ctx, []jsonArtifact{{path: path, value: map[string]any{"version": "2"}}})
	if !errors.Is(err, context.DeadlineExceeded) || changed {
		t.Fatalf("direct publisher bypassed shared lock: changed=%t err=%v", changed, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release shared publication lock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("idempotent shared lock release: %v", err)
	}
}

// TestArtifactPublicationLockRejectsNilRelease verifies invalid lifecycle wiring fails at the caller instead of silently leaking synchronization.
func TestArtifactPublicationLockRejectsNilRelease(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil publication lock release did not panic")
		}
	}()
	var lock *ArtifactPublicationLock
	_ = lock.Release()
}

// TestArtifactPublicationLockConcurrentReleaseRetainsFailure verifies cleanup runs once, shares its result, and does not strand other directories.
func TestArtifactPublicationLockConcurrentReleaseRetainsFailure(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "first", "api_index.json"),
		filepath.Join(root, "second", "openapi.json"),
	}
	lock, err := AcquireArtifactPublicationLock(context.Background(), paths...)
	if err != nil {
		t.Fatalf("acquire publication lock: %v", err)
	}
	if err := lock.locks[0].file.Close(); err != nil {
		t.Fatalf("force release failure: %v", err)
	}

	const callers = 8
	results := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			ready.Done()
			<-start
			results <- lock.Release()
		}()
	}
	ready.Wait()
	close(start)

	first := <-results
	if first == nil {
		t.Fatal("release unexpectedly hid the closed lock file")
	}
	for range callers - 1 {
		if result := <-results; result != first {
			t.Fatalf("concurrent release returned a different stored error: first=%v result=%v", first, result)
		}
	}
	if result := lock.Release(); result != first {
		t.Fatalf("repeated release returned a different stored error: first=%v result=%v", first, result)
	}

	reacquired, err := AcquireArtifactPublicationLock(context.Background(), paths...)
	if err != nil {
		t.Fatalf("reacquire after failed cleanup: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatalf("release reacquired publication lock: %v", err)
	}
}

// TestPublishJSONArtifactsSerializesProcesses verifies two publishers cannot expose a mixed manifest, diagnostics, and OpenAPI generation.
func TestPublishJSONArtifactsSerializesProcesses(t *testing.T) {
	root := t.TempDir()
	markerPath := filepath.Join(root, "first-rename.marker")
	releasePath := filepath.Join(root, "release.marker")
	first := artifactPublicationHelperCommand(root, "first", markerPath, releasePath)
	if err := first.Start(); err != nil {
		t.Fatalf("start first publisher: %v", err)
	}
	waitForPath(t, markerPath)

	second := artifactPublicationHelperCommand(root, "second", "", "")
	if err := second.Start(); err != nil {
		_ = os.WriteFile(releasePath, []byte("release"), 0o644)
		_ = first.Wait()
		t.Fatalf("start second publisher: %v", err)
	}
	secondResult := make(chan error, 1)
	go func() { secondResult <- second.Wait() }()
	select {
	case err := <-secondResult:
		_ = os.WriteFile(releasePath, []byte("release"), 0o644)
		_ = first.Wait()
		t.Fatalf("second publisher bypassed the held lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := os.WriteFile(releasePath, []byte("release"), 0o644); err != nil {
		t.Fatalf("release first publisher: %v", err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first publisher failed: %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second publisher failed: %v", err)
	}
	for _, name := range []string{"api_index.json", "api_index.diagnostics.json", "openapi.json"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read final %s: %v", name, err)
		}
		if string(contents) != "second:"+name+"\n" {
			t.Fatalf("mixed artifact generation for %s: %q", name, contents)
		}
	}
	if _, err := os.Stat(filepath.Join(root, artifactLockFilename)); err != nil {
		t.Fatalf("persistent interoperability lock missing: %v", err)
	}
}

// TestArtifactPublicationSubprocessHelper publishes one generation and optionally pauses after its first visible rename.
func TestArtifactPublicationSubprocessHelper(t *testing.T) {
	root := os.Getenv("WEBINDEX_ARTIFACT_HELPER_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	generation := os.Getenv("WEBINDEX_ARTIFACT_HELPER_GENERATION")
	markerPath := os.Getenv("WEBINDEX_ARTIFACT_HELPER_MARKER")
	releasePath := os.Getenv("WEBINDEX_ARTIFACT_HELPER_RELEASE")
	encoded := make([]encodedJSONArtifact, 0, 3)
	for _, name := range []string{"api_index.json", "api_index.diagnostics.json", "openapi.json"} {
		encoded = append(encoded, encodedJSONArtifact{path: filepath.Join(root, name), data: []byte(generation + ":" + name + "\n")})
	}
	renameCount := 0
	_, err := publishEncodedJSONArtifacts(encoded, func(source string, destination string) error {
		if err := os.Rename(source, destination); err != nil {
			return err
		}
		renameCount++
		if renameCount != 1 || markerPath == "" {
			return nil
		}
		if err := os.WriteFile(markerPath, []byte("renamed"), 0o644); err != nil {
			return err
		}
		for {
			if _, err := os.Stat(releasePath); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatalf("publish helper generation: %v", err)
	}
}

// artifactPublicationHelperCommand creates an isolated test process that uses the same persistent lock protocol as ordinary Run calls.
func artifactPublicationHelperCommand(root string, generation string, markerPath string, releasePath string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestArtifactPublicationSubprocessHelper$")
	command.Env = append(os.Environ(),
		"WEBINDEX_ARTIFACT_HELPER_ROOT="+root,
		"WEBINDEX_ARTIFACT_HELPER_GENERATION="+generation,
		"WEBINDEX_ARTIFACT_HELPER_MARKER="+markerPath,
		"WEBINDEX_ARTIFACT_HELPER_RELEASE="+releasePath,
	)
	return command
}

// waitForPath waits for a subprocess synchronization file without hiding an early process exit behind a long timeout.
func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat synchronization path: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// assertNoArtifactCandidates ensures cancellation and rollback clean every staged temporary file.
func assertNoArtifactCandidates(t *testing.T, root string) {
	t.Helper()
	candidates, err := filepath.Glob(filepath.Join(root, ".*.tmp-*"))
	if err != nil {
		t.Fatalf("find artifact candidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("artifact candidates remain: %v", candidates)
	}
}
