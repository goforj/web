//go:build js || wasip1

package webindex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestProcessLocalArtifactFileLockAvoidsFilesystemGuards verifies sandboxed targets leave exclusion to the shared process semaphore.
func TestProcessLocalArtifactFileLockAvoidsFilesystemGuards(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), artifactLockFilename)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open artifact lock fixture: %v", err)
	}
	defer file.Close()

	fileLock, acquired, err := tryLockArtifactFile(file)
	if err != nil || !acquired || fileLock.guard != nil {
		t.Fatalf("acquire process-local file lock: acquired=%t state=%#v err=%v", acquired, fileLock, err)
	}
	if err := unlockArtifactFile(file, fileLock); err != nil {
		t.Fatalf("release process-local file lock: %v", err)
	}
	if _, err := os.Stat(lockPath + ".guard"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("process-local lock created a filesystem guard: %v", err)
	}
}
