package webindex

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errIndexCacheInputsChanged prevents publishing an analysis assembled from more than one source generation.
var errIndexCacheInputsChanged = errors.New("API index inputs changed during analysis")

// errIndexCacheSourceTreeNotCovered disables persistence when a package tree is hidden behind a directory symlink.
var errIndexCacheSourceTreeNotCovered = errors.New("API index source tree contains an untracked directory symlink")

// validatePublication rejects cache-backed output when its source snapshot is no longer current.
func (cache *indexCacheSession) validatePublication(ctx context.Context) error {
	if cache == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache.inputStillCurrent(ctx) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errIndexCacheInputsChanged
}

// publishIndexCacheArtifacts validates cache freshness after staging while holding the locks that serialize visible replacement.
func publishIndexCacheArtifacts(ctx context.Context, cache *indexCacheSession, encoded []encodedJSONArtifact) (changed bool, err error) {
	return publishIndexCacheArtifactsValidated(ctx, encoded, os.Rename, cache.validatePublication)
}

// publishIndexCacheArtifactsValidated keeps the final source validation injectable so its exact publication boundary can be exercised deterministically.
func publishIndexCacheArtifactsValidated(ctx context.Context, encoded []encodedJSONArtifact, rename func(string, string) error, validate func(context.Context) error) (changed bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateEncodedJSONArtifactPaths(encoded); err != nil {
		return false, err
	}
	locks, err := acquireArtifactDirectoryLocks(ctx, encoded)
	if err != nil {
		return false, err
	}
	defer func() {
		if releaseErr := releaseArtifactDirectoryLocks(locks); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()
	return publishEncodedJSONArtifactsLockedValidated(ctx, encoded, rename, validate)
}

// indexCachePathsEqual rejects case-only aliases because mount-level case behavior cannot be inferred from GOOS.
func indexCachePathsEqual(left string, right string) bool {
	return strings.EqualFold(left, right)
}

// indexCacheSourceEntryCovered rejects symlinked directories because WalkDir does not traverse their package sources.
func indexCacheSourceEntryCovered(path string, entry fs.DirEntry) (bool, error) {
	if entry.Type()&fs.ModeSymlink == 0 {
		return true, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

// indexCacheFileDigestsCovered rejects source forms whose generated package types can depend on untracked external inputs.
func indexCacheFileDigestsCovered(files []indexCacheFileDigest) bool {
	for _, file := range files {
		extension := strings.ToLower(filepath.Ext(file.path))
		if extension == ".swig" || extension == ".swigcxx" {
			return false
		}
		for _, importPath := range file.metadata.imports {
			if importPath == "C" {
				return false
			}
		}
	}
	return true
}

// indexCacheGoFlagsCovered rejects effective build inputs that syntax discovery or the source fingerprint cannot mirror.
func indexCacheGoFlagsCovered(goFlags string) bool {
	return sourceBuildEnvironmentError(goFlags) == nil
}
