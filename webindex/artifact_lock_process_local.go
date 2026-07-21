//go:build js || wasip1

package webindex

import "os"

// tryLockArtifactFile delegates exclusion to the process-local semaphore where shared host filesystem locking is unavailable.
func tryLockArtifactFile(_ *os.File) (artifactFileLock, bool, error) {
	return artifactFileLock{}, true, nil
}

// unlockArtifactFile leaves no filesystem guard because the process-local semaphore owns the complete lifecycle.
func unlockArtifactFile(_ *os.File, _ artifactFileLock) error {
	return nil
}
