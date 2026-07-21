package webindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ArtifactPublicationLockFilename is the persistent cross-process lock name shared by webindex and GoForj artifact publishers.
const ArtifactPublicationLockFilename = ".webindex-artifacts.lock"

// artifactLockFilename keeps internal publication paths tied to the exported interoperability contract.
const artifactLockFilename = ArtifactPublicationLockFilename

// ArtifactPublicationLock holds process-local and operating-system directory locks for one canonical artifact set and must not be copied after first use.
type ArtifactPublicationLock struct {
	locks []artifactDirectoryLock
	once  sync.Once
	err   error
}

// artifactDirectoryLock pairs the advisory file lock with the same-process guard advisory APIs cannot provide.
type artifactDirectoryLock struct {
	file         *os.File
	fileLock     artifactFileLock
	path         string
	processLocal artifactProcessLock
}

// artifactFileLock carries platform-specific ownership that must remain live until directory publication finishes.
type artifactFileLock struct {
	guard *os.File
}

// artifactProcessLockEntry remains shared while waiters or holders still reference one canonical lock path.
type artifactProcessLockEntry struct {
	semaphore  chan struct{}
	references int
}

// artifactProcessLock records the registry entry that must be released after its directory transaction.
type artifactProcessLock struct {
	entry *artifactProcessLockEntry
	path  string
}

// artifactProcessLocks prevents same-process publishers from bypassing process-associated advisory lock semantics.
var artifactProcessLocks = struct {
	sync.Mutex
	entries map[string]*artifactProcessLockEntry
}{entries: map[string]*artifactProcessLockEntry{}}

// AcquireArtifactPublicationLock lets coordinating publishers use the exact lock ordering and process-local registry used by webindex publication.
func AcquireArtifactPublicationLock(ctx context.Context, artifactPaths ...string) (*ArtifactPublicationLock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	encoded := make([]encodedJSONArtifact, 0, len(artifactPaths))
	for _, path := range artifactPaths {
		if path != "" {
			encoded = append(encoded, encodedJSONArtifact{path: path})
		}
	}
	locks, err := acquireArtifactDirectoryLocks(ctx, encoded)
	if err != nil {
		return nil, err
	}
	return &ArtifactPublicationLock{locks: locks}, nil
}

// Release relinquishes an artifact publication lock at most once and returns the same result to repeated callers.
func (lock *ArtifactPublicationLock) Release() error {
	lock.once.Do(func() {
		lock.err = releaseArtifactDirectoryLocks(lock.locks)
		lock.locks = nil
	})
	return lock.err
}

// acquireArtifactDirectoryLocks locks every output directory in canonical order so overlapping artifact sets cannot deadlock or interleave snapshots.
func acquireArtifactDirectoryLocks(ctx context.Context, encoded []encodedJSONArtifact) ([]artifactDirectoryLock, error) {
	directories, err := artifactDirectories(encoded)
	if err != nil {
		return nil, err
	}
	locks := make([]artifactDirectoryLock, 0, len(directories))
	for _, directory := range directories {
		lockPath := filepath.Join(directory, artifactLockFilename)
		processLocal, err := acquireArtifactProcessLock(ctx, lockPath)
		if err != nil {
			cause := fmt.Errorf("acquire process-local artifact lock %q: %w", lockPath, err)
			return nil, joinArtifactLockErrors(cause, releaseArtifactDirectoryLocks(locks))
		}
		pending := artifactDirectoryLock{path: lockPath, processLocal: processLocal}
		if err := ctx.Err(); err != nil {
			return nil, joinArtifactLockErrors(err, abandonArtifactDirectoryLock(pending, false), releaseArtifactDirectoryLocks(locks))
		}
		if err := os.MkdirAll(directory, 0o755); err != nil {
			cause := fmt.Errorf("create artifact lock directory %q: %w", directory, err)
			return nil, joinArtifactLockErrors(cause, abandonArtifactDirectoryLock(pending, false), releaseArtifactDirectoryLocks(locks))
		}
		if err := ctx.Err(); err != nil {
			return nil, joinArtifactLockErrors(err, abandonArtifactDirectoryLock(pending, false), releaseArtifactDirectoryLocks(locks))
		}
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			cause := fmt.Errorf("open artifact lock %q: %w", lockPath, err)
			return nil, joinArtifactLockErrors(cause, abandonArtifactDirectoryLock(pending, false), releaseArtifactDirectoryLocks(locks))
		}
		pending.file = file
		if err := ctx.Err(); err != nil {
			return nil, joinArtifactLockErrors(err, abandonArtifactDirectoryLock(pending, false), releaseArtifactDirectoryLocks(locks))
		}
		fileLock, err := acquireArtifactFileLock(ctx, file)
		if err != nil {
			cause := fmt.Errorf("acquire artifact lock %q: %w", lockPath, err)
			return nil, joinArtifactLockErrors(cause, abandonArtifactDirectoryLock(pending, false), releaseArtifactDirectoryLocks(locks))
		}
		pending.fileLock = fileLock
		if err := ctx.Err(); err != nil {
			return nil, joinArtifactLockErrors(err, abandonArtifactDirectoryLock(pending, true), releaseArtifactDirectoryLocks(locks))
		}
		locks = append(locks, pending)
	}
	return locks, nil
}

// abandonArtifactDirectoryLock releases a partially acquired directory lock so failed acquisition cannot strand later publishers.
func abandonArtifactDirectoryLock(lock artifactDirectoryLock, fileLocked bool) error {
	var releaseErrors []error
	if lock.file != nil {
		if fileLocked {
			if err := unlockArtifactFile(lock.file, lock.fileLock); err != nil {
				releaseErrors = append(releaseErrors, fmt.Errorf("unlock artifact lock %q: %w", lock.path, err))
			}
		}
		if err := lock.file.Close(); err != nil {
			releaseErrors = append(releaseErrors, fmt.Errorf("close artifact lock %q: %w", lock.path, err))
		}
	}
	releaseArtifactProcessLock(lock.processLocal)
	return errors.Join(releaseErrors...)
}

// joinArtifactLockErrors preserves the triggering failure while reporting every cleanup failure that could weaken later lock attempts.
func joinArtifactLockErrors(cause error, cleanupErrors ...error) error {
	joined := []error{cause}
	for _, cleanupErr := range cleanupErrors {
		if cleanupErr != nil {
			joined = append(joined, cleanupErr)
		}
	}
	if len(joined) == 1 {
		return cause
	}
	return errors.Join(joined...)
}

// acquireArtifactProcessLock serializes goroutines before advisory locking because process-associated lock implementations cannot protect same-process publishers.
func acquireArtifactProcessLock(ctx context.Context, path string) (artifactProcessLock, error) {
	canonicalPath, err := canonicalIndexCachePath(path)
	if err != nil {
		return artifactProcessLock{}, err
	}
	directoryInfo, _ := os.Stat(filepath.Dir(canonicalPath))
	artifactProcessLocks.Lock()
	path = canonicalPath
	entry := artifactProcessLocks.entries[path]
	if entry == nil && directoryInfo != nil {
		for existingPath, existingEntry := range artifactProcessLocks.entries {
			existingInfo, statErr := os.Stat(filepath.Dir(existingPath))
			if statErr == nil && os.SameFile(directoryInfo, existingInfo) {
				path = existingPath
				entry = existingEntry
				break
			}
		}
	}
	if entry == nil {
		entry = &artifactProcessLockEntry{semaphore: make(chan struct{}, 1)}
		entry.semaphore <- struct{}{}
		artifactProcessLocks.entries[path] = entry
	}
	entry.references++
	artifactProcessLocks.Unlock()

	select {
	case <-ctx.Done():
		artifactProcessLocks.Lock()
		entry.references--
		if entry.references == 0 {
			delete(artifactProcessLocks.entries, path)
		}
		artifactProcessLocks.Unlock()
		return artifactProcessLock{}, ctx.Err()
	case <-entry.semaphore:
		return artifactProcessLock{entry: entry, path: path}, nil
	}
}

// releaseArtifactProcessLock wakes one waiter and removes unused registry entries without weakening an already queued lock reference.
func releaseArtifactProcessLock(lock artifactProcessLock) {
	lock.entry.semaphore <- struct{}{}
	artifactProcessLocks.Lock()
	lock.entry.references--
	if lock.entry.references == 0 {
		delete(artifactProcessLocks.entries, lock.path)
	}
	artifactProcessLocks.Unlock()
}

// artifactDirectories returns physically unique parents so symlink and case aliases cannot acquire the same lock twice.
func artifactDirectories(encoded []encodedJSONArtifact) ([]string, error) {
	type directoryIdentity struct {
		path string
		info os.FileInfo
	}
	identities := make([]directoryIdentity, 0, len(encoded))
	for _, artifact := range encoded {
		if artifact.path == "" {
			continue
		}
		absolute, err := filepath.Abs(filepath.Dir(filepath.Clean(artifact.path)))
		if err != nil {
			return nil, fmt.Errorf("resolve artifact directory for %q: %w", artifact.path, err)
		}
		absolute = filepath.Clean(absolute)
		if err := os.MkdirAll(absolute, 0o755); err != nil {
			return nil, fmt.Errorf("create artifact directory %q: %w", absolute, err)
		}
		canonical, err := canonicalIndexCachePath(absolute)
		if err != nil {
			return nil, fmt.Errorf("canonicalize artifact directory %q: %w", absolute, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, fmt.Errorf("inspect artifact directory %q: %w", canonical, err)
		}
		duplicate := false
		for _, existing := range identities {
			if canonical == existing.path || os.SameFile(info, existing.info) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			identities = append(identities, directoryIdentity{path: canonical, info: info})
		}
	}
	directories := make([]string, 0, len(identities))
	for _, identity := range identities {
		directories = append(directories, identity.path)
	}
	sort.Strings(directories)
	return directories, nil
}

// acquireArtifactFileLock retries nonblocking OS locks so a canceled build does not wait behind another publisher indefinitely.
func acquireArtifactFileLock(ctx context.Context, file *os.File) (artifactFileLock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return artifactFileLock{}, err
		}
		fileLock, acquired, err := tryLockArtifactFile(file)
		if err != nil {
			return artifactFileLock{}, err
		}
		if acquired {
			return fileLock, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return artifactFileLock{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// releaseArtifactDirectoryLocks unlocks in reverse canonical order and attempts every release even after an earlier failure.
func releaseArtifactDirectoryLocks(locks []artifactDirectoryLock) error {
	var releaseErrors []error
	for index := len(locks) - 1; index >= 0; index-- {
		lock := locks[index]
		if err := unlockArtifactFile(lock.file, lock.fileLock); err != nil {
			releaseErrors = append(releaseErrors, fmt.Errorf("unlock artifact lock %q: %w", lock.path, err))
		}
		if err := lock.file.Close(); err != nil {
			releaseErrors = append(releaseErrors, fmt.Errorf("close artifact lock %q: %w", lock.path, err))
		}
		releaseArtifactProcessLock(lock.processLocal)
	}
	return errors.Join(releaseErrors...)
}
