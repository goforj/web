//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package webindex

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockArtifactFile acquires the advisory lock without blocking so context cancellation remains observable.
func tryLockArtifactFile(file *os.File) (artifactFileLock, bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return artifactFileLock{}, true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return artifactFileLock{}, false, nil
	}
	return artifactFileLock{}, false, err
}

// unlockArtifactFile releases the advisory lock while the persistent lock file remains available to other publishers.
func unlockArtifactFile(file *os.File, _ artifactFileLock) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
