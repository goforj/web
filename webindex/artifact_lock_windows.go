//go:build windows

package webindex

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockArtifactFile acquires the advisory lock without blocking so context cancellation remains observable.
func tryLockArtifactFile(file *os.File) (artifactFileLock, bool, error) {
	overlapped := &windows.Overlapped{}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	)
	if err == nil {
		return artifactFileLock{}, true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return artifactFileLock{}, false, nil
	}
	return artifactFileLock{}, false, err
}

// unlockArtifactFile releases the advisory lock while the persistent lock file remains available to other publishers.
func unlockArtifactFile(file *os.File, _ artifactFileLock) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
