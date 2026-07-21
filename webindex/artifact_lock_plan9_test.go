//go:build plan9

package webindex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestPlan9ArtifactFileLockLifecycle verifies a live guard excludes peers and closing its retained handle removes the guard.
func TestPlan9ArtifactFileLockLifecycle(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), artifactLockFilename)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open artifact lock fixture: %v", err)
	}
	defer file.Close()

	first, acquired, err := tryLockArtifactFile(file)
	if err != nil || !acquired || first.guard == nil {
		t.Fatalf("acquire first Plan 9 guard: acquired=%t state=%#v err=%v", acquired, first, err)
	}
	released := false
	defer func() {
		if !released {
			_ = unlockArtifactFile(file, first)
		}
	}()

	second, acquired, err := tryLockArtifactFile(file)
	if err != nil || acquired || second.guard != nil {
		t.Fatalf("acquire competing Plan 9 guard: acquired=%t state=%#v err=%v", acquired, second, err)
	}
	if err := unlockArtifactFile(file, first); err != nil {
		t.Fatalf("release first Plan 9 guard: %v", err)
	}
	released = true
	if _, err := os.Stat(lockPath + ".guard"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary Plan 9 guard survived close: %v", err)
	}

	third, acquired, err := tryLockArtifactFile(file)
	if err != nil || !acquired || third.guard == nil {
		t.Fatalf("reacquire Plan 9 guard: acquired=%t state=%#v err=%v", acquired, third, err)
	}
	if err := unlockArtifactFile(file, third); err != nil {
		t.Fatalf("release reacquired Plan 9 guard: %v", err)
	}
}
