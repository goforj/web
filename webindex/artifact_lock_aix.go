//go:build aix

package webindex

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockArtifactFile acquires the AIX advisory record lock without blocking so context cancellation remains observable.
func tryLockArtifactFile(file *os.File) (artifactFileLock, bool, error) {
	lock := unix.Flock_t{Type: int16(unix.F_WRLCK), Whence: int16(io.SeekStart)}
	err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &lock)
	if err == nil {
		return artifactFileLock{}, true, nil
	}
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EAGAIN) {
		return artifactFileLock{}, false, nil
	}
	return artifactFileLock{}, false, err
}

// unlockArtifactFile releases the AIX advisory record lock while the persistent lock file remains available to other publishers.
func unlockArtifactFile(file *os.File, _ artifactFileLock) error {
	lock := unix.Flock_t{Type: int16(unix.F_UNLCK), Whence: int16(io.SeekStart)}
	return unix.FcntlFlock(file.Fd(), unix.F_SETLK, &lock)
}
