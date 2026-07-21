//go:build plan9

package webindex

import (
	"errors"
	"os"
)

// tryLockArtifactFile creates a temporary exclusive guard whose open handle ties ownership to the process lifetime.
func tryLockArtifactFile(file *os.File) (artifactFileLock, bool, error) {
	guard, err := os.OpenFile(file.Name()+".guard", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600|os.ModeTemporary|os.ModeExclusive)
	if err == nil {
		return artifactFileLock{guard: guard}, true, nil
	}
	if errors.Is(err, os.ErrExist) {
		return artifactFileLock{}, false, nil
	}
	return artifactFileLock{}, false, err
}

// unlockArtifactFile closes the temporary guard so Plan 9 removes it after normal exit as well as process death.
func unlockArtifactFile(_ *os.File, lock artifactFileLock) error {
	return lock.guard.Close()
}
