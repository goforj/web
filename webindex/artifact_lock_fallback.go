//go:build js || plan9 || wasip1

package webindex

import (
	"errors"
	"os"
)

// tryLockArtifactFile uses atomic directory creation on targets without advisory file-lock APIs.
func tryLockArtifactFile(file *os.File) (bool, error) {
	err := os.Mkdir(file.Name()+".guard", 0o755)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	return false, err
}

// unlockArtifactFile removes the fallback guard after the protected publication has finished.
func unlockArtifactFile(file *os.File) error {
	return os.Remove(file.Name() + ".guard")
}
