package webindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// jsonArtifact keeps each value bound to its destination until the complete set has encoded successfully.
type jsonArtifact struct {
	path  string
	value any
}

// encodedJSONArtifact carries deterministic bytes into the shared publication transaction.
type encodedJSONArtifact struct {
	path string
	data []byte
}

// preparedJSONArtifact retains enough prior state to restore an all-or-nothing publication attempt.
type preparedJSONArtifact struct {
	path           string
	temporaryPath  string
	previousData   []byte
	previousExists bool
	changed        bool
}

// writeJSON preserves the original single-artifact API while using the same
// changed-only atomic publication path as complete index runs.
func writeJSON(path string, value any) error {
	_, err := publishJSONArtifacts([]jsonArtifact{{path: path, value: value}})
	return err
}

// publishJSONArtifacts encodes every artifact before touching the filesystem,
// so an invalid later value cannot leave an earlier artifact partially updated.
func publishJSONArtifacts(artifacts []jsonArtifact) (bool, error) {
	return publishJSONArtifactsContext(context.Background(), artifacts)
}

// publishJSONArtifactsContext keeps lock waits cancelable while preserving the original background-context helper for focused callers.
func publishJSONArtifactsContext(ctx context.Context, artifacts []jsonArtifact) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	encoded, err := encodeJSONArtifacts(artifacts)
	if err != nil {
		return false, err
	}
	return publishEncodedJSONArtifactsContext(ctx, encoded, os.Rename)
}

// publishEncodedJSONArtifacts stages the complete changed set before rename and restores prior files if any rename fails.
func publishEncodedJSONArtifacts(encoded []encodedJSONArtifact, rename func(string, string) error) (bool, error) {
	return publishEncodedJSONArtifactsContext(context.Background(), encoded, rename)
}

// publishEncodedJSONArtifactsContext serializes every overlapping output directory before snapshots are read or candidates are renamed.
func publishEncodedJSONArtifactsContext(ctx context.Context, encoded []encodedJSONArtifact, rename func(string, string) error) (changed bool, err error) {
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
	return publishEncodedJSONArtifactsLocked(ctx, encoded, rename)
}

// publishEncodedJSONArtifactsLocked performs the transaction while its caller owns every directory lock for the output set.
func publishEncodedJSONArtifactsLocked(ctx context.Context, encoded []encodedJSONArtifact, rename func(string, string) error) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	prepared := make([]preparedJSONArtifact, 0, len(encoded))
	for _, artifact := range encoded {
		if err := ctx.Err(); err != nil {
			cleanupPreparedJSONArtifacts(prepared)
			return false, err
		}
		candidate, err := prepareJSONArtifact(artifact)
		if err != nil {
			cleanupPreparedJSONArtifacts(prepared)
			return false, err
		}
		prepared = append(prepared, candidate)
		if err := ctx.Err(); err != nil {
			cleanupPreparedJSONArtifacts(prepared)
			return false, err
		}
	}
	defer cleanupPreparedJSONArtifacts(prepared)

	published := make([]preparedJSONArtifact, 0, len(prepared))
	for _, artifact := range prepared {
		if !artifact.changed {
			continue
		}
		if err := ctx.Err(); err != nil {
			rollbackErr := rollbackJSONArtifactsLocked(published)
			return false, errors.Join(err, rollbackErr)
		}
		if err := rename(artifact.temporaryPath, artifact.path); err != nil {
			rollbackErr := rollbackJSONArtifactsLocked(published)
			if rollbackErr != nil {
				return false, fmt.Errorf("publish artifact %q: %w; rollback failed: %v", artifact.path, err, rollbackErr)
			}
			return false, fmt.Errorf("publish artifact %q: %w", artifact.path, err)
		}
		artifact.temporaryPath = ""
		published = append(published, artifact)
		if err := ctx.Err(); err != nil {
			rollbackErr := rollbackJSONArtifactsLocked(published)
			return false, errors.Join(err, rollbackErr)
		}
	}
	if err := ctx.Err(); err != nil {
		rollbackErr := rollbackJSONArtifactsLocked(published)
		return false, errors.Join(err, rollbackErr)
	}
	return len(published) > 0, nil
}

// validateEncodedJSONArtifactPaths rejects one physical destination serving multiple artifact roles before lock files or candidates are created.
func validateEncodedJSONArtifactPaths(encoded []encodedJSONArtifact) error {
	seen := make(map[string]string, len(encoded))
	for _, artifact := range encoded {
		if artifact.path == "" {
			continue
		}
		absolute, err := filepath.Abs(filepath.Clean(artifact.path))
		if err != nil {
			return fmt.Errorf("resolve JSON artifact path %q: %w", artifact.path, err)
		}
		canonical := filepath.Clean(absolute)
		if previous, exists := seen[canonical]; exists {
			return fmt.Errorf("JSON artifact paths %q and %q resolve to the same destination %q", previous, artifact.path, canonical)
		}
		seen[canonical] = artifact.path
	}
	return nil
}

// encodeJSONArtifacts validates the complete artifact set in memory before
// publication and keeps generated JSON byte-stable with a final newline.
func encodeJSONArtifacts(artifacts []jsonArtifact) ([]encodedJSONArtifact, error) {
	encoded := make([]encodedJSONArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.path == "" {
			continue
		}
		data, err := json.MarshalIndent(artifact.value, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode JSON artifact %q: %w", artifact.path, err)
		}
		data = append(data, '\n')
		encoded = append(encoded, encodedJSONArtifact{path: artifact.path, data: data})
	}
	return encoded, nil
}

// prepareJSONArtifact writes a same-directory candidate while retaining enough state to roll back a later publication failure.
func prepareJSONArtifact(artifact encodedJSONArtifact) (preparedJSONArtifact, error) {
	prepared := preparedJSONArtifact{path: artifact.path}
	existing, err := os.ReadFile(artifact.path)
	if err == nil {
		prepared.previousData = existing
		prepared.previousExists = true
		if bytes.Equal(existing, artifact.data) {
			return prepared, nil
		}
	} else if !os.IsNotExist(err) {
		return preparedJSONArtifact{}, fmt.Errorf("read existing artifact %q: %w", artifact.path, err)
	}

	dir := filepath.Dir(artifact.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return preparedJSONArtifact{}, fmt.Errorf("create artifact directory %q: %w", dir, err)
	}
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(artifact.path)+".tmp-*")
	if err != nil {
		return preparedJSONArtifact{}, fmt.Errorf("create temporary artifact for %q: %w", artifact.path, err)
	}
	prepared.temporaryPath = temporary.Name()
	prepared.changed = true
	if err := writeJSONCandidate(temporary, artifact.path, artifact.data); err != nil {
		_ = os.Remove(prepared.temporaryPath)
		return preparedJSONArtifact{}, err
	}
	return prepared, nil
}

// writeJSONCandidate completes and syncs a candidate before it can replace a visible artifact.
func writeJSONCandidate(temporary *os.File, destination string, data []byte) error {
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary artifact permissions for %q: %w", destination, err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary artifact for %q: %w", destination, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary artifact for %q: %w", destination, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary artifact for %q: %w", destination, err)
	}
	return nil
}

// cleanupPreparedJSONArtifacts removes candidates that were skipped or never published.
func cleanupPreparedJSONArtifacts(prepared []preparedJSONArtifact) {
	for _, artifact := range prepared {
		if artifact.temporaryPath != "" {
			_ = os.Remove(artifact.temporaryPath)
		}
	}
}

// rollbackJSONArtifactsLocked restores the exact pre-publication set when a later artifact cannot be renamed.
func rollbackJSONArtifactsLocked(published []preparedJSONArtifact) error {
	var rollbackErrors []error
	for index := len(published) - 1; index >= 0; index-- {
		artifact := published[index]
		if !artifact.previousExists {
			if err := os.Remove(artifact.path); err != nil && !os.IsNotExist(err) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove newly published artifact %q: %w", artifact.path, err))
			}
			continue
		}
		if _, err := publishEncodedJSONArtifactsLocked(context.Background(), []encodedJSONArtifact{{path: artifact.path, data: artifact.previousData}}, os.Rename); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore artifact %q: %w", artifact.path, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

// writeFileAtomicallyIfChanged avoids build churn and ensures readers observe
// either the complete previous file or the complete replacement file.
func writeFileAtomicallyIfChanged(path string, data []byte) (bool, error) {
	return publishEncodedJSONArtifacts([]encodedJSONArtifact{{path: path, data: data}}, os.Rename)
}
