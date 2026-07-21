package webindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestArtifactPublicationLockAllowsEmptySets verifies coordinating callers can safely acquire and repeatedly release an empty artifact set.
func TestArtifactPublicationLockAllowsEmptySets(t *testing.T) {
	lock, err := AcquireArtifactPublicationLock(nil, "")
	if err != nil {
		t.Fatalf("acquire empty artifact lock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release empty artifact lock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("repeat empty artifact lock release: %v", err)
	}
}

// TestJoinArtifactLockErrorsPreservesCleanupFailures verifies lock acquisition failures retain every cleanup error.
func TestJoinArtifactLockErrorsPreservesCleanupFailures(t *testing.T) {
	cause := errors.New("acquire failed")
	cleanup := errors.New("cleanup failed")
	if got := joinArtifactLockErrors(cause, nil); got != cause {
		t.Fatalf("single lock error = %v, want original cause", got)
	}
	joined := joinArtifactLockErrors(cause, cleanup)
	if !errors.Is(joined, cause) || !errors.Is(joined, cleanup) {
		t.Fatalf("joined lock error = %v", joined)
	}
}

// TestAbandonArtifactDirectoryLockReleasesPartialOwnership verifies failed acquisition cannot strand either process-local or operating-system ownership.
func TestAbandonArtifactDirectoryLockReleasesPartialOwnership(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, artifactLockFilename)

	processOnly, err := acquireArtifactProcessLock(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("acquire process-only lock: %v", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open partial lock file: %v", err)
	}
	if err := abandonArtifactDirectoryLock(artifactDirectoryLock{
		file:         file,
		path:         lockPath,
		processLocal: processOnly,
	}, false); err != nil {
		t.Fatalf("abandon process-only lock: %v", err)
	}

	processAndFile, err := acquireArtifactProcessLock(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("reacquire process lock: %v", err)
	}
	file, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen lock file: %v", err)
	}
	fileLock, err := acquireArtifactFileLock(context.Background(), file)
	if err != nil {
		_ = file.Close()
		releaseArtifactProcessLock(processAndFile)
		t.Fatalf("acquire file lock: %v", err)
	}
	if err := abandonArtifactDirectoryLock(artifactDirectoryLock{
		file:         file,
		fileLock:     fileLock,
		path:         lockPath,
		processLocal: processAndFile,
	}, true); err != nil {
		t.Fatalf("abandon fully acquired lock: %v", err)
	}

	reacquired, err := AcquireArtifactPublicationLock(context.Background(), filepath.Join(root, "api.json"))
	if err != nil {
		t.Fatalf("reacquire abandoned lock: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatalf("release reacquired lock: %v", err)
	}
}

// TestArtifactPublicationLockRejectsBlockedParent verifies invalid filesystem topology fails before publication and succeeds once repaired.
func TestArtifactPublicationLockRejectsBlockedParent(t *testing.T) {
	root := t.TempDir()
	blockedDirectory := filepath.Join(root, "blocked")
	if err := os.WriteFile(blockedDirectory, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	artifactPath := filepath.Join(blockedDirectory, "api.json")
	if _, err := AcquireArtifactPublicationLock(context.Background(), artifactPath); err == nil {
		t.Fatal("artifact lock accepted a regular file as its directory")
	}
	if err := os.Remove(blockedDirectory); err != nil {
		t.Fatalf("remove blocking file: %v", err)
	}
	lock, err := AcquireArtifactPublicationLock(context.Background(), artifactPath)
	if err != nil {
		t.Fatalf("acquire after directory repair: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release repaired-directory lock: %v", err)
	}
}
