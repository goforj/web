package webindex

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// indexCacheFileState captures a filesystem generation token without making metadata alone authoritative on unsupported platforms.
type indexCacheFileState struct {
	info              fs.FileInfo
	changeSeconds     int64
	changeNanoseconds int64
	changeKnown       bool
}

// newIndexCacheFileState captures identity and change time from one filesystem observation.
func newIndexCacheFileState(info fs.FileInfo) indexCacheFileState {
	if info == nil {
		return indexCacheFileState{}
	}
	seconds, nanoseconds, known := indexCacheFileChangeToken(info)
	return indexCacheFileState{
		info:              info,
		changeSeconds:     seconds,
		changeNanoseconds: nanoseconds,
		changeKnown:       known,
	}
}

// fastMatches proves a path still names the captured generation when the host exposes a non-restorable change token.
func (state indexCacheFileState) fastMatches(info fs.FileInfo) bool {
	current := newIndexCacheFileState(info)
	return state.changeKnown && current.changeKnown && indexCacheFileStatesEqual(state, current)
}

// indexCacheFileStatesEqual compares observations without treating size and mtime as a sufficient content identity.
func indexCacheFileStatesEqual(left indexCacheFileState, right indexCacheFileState) bool {
	if left.info == nil || right.info == nil || !os.SameFile(left.info, right.info) {
		return false
	}
	if left.info.Size() != right.info.Size() || left.info.Mode() != right.info.Mode() || !left.info.ModTime().Equal(right.info.ModTime()) {
		return false
	}
	if left.changeKnown != right.changeKnown {
		return false
	}
	return !left.changeKnown || left.changeSeconds == right.changeSeconds && left.changeNanoseconds == right.changeNanoseconds
}

// readIndexCacheFileSnapshot ties immutable bytes to a stable file observation so publication can later validate metadata safely.
func readIndexCacheFileSnapshot(path string) ([]byte, indexCacheFileState, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, indexCacheFileState{}, err
	}
	info, statErr := file.Stat()
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if statErr != nil {
		return nil, indexCacheFileState{}, statErr
	}
	if readErr != nil {
		return nil, indexCacheFileState{}, readErr
	}
	if closeErr != nil {
		return nil, indexCacheFileState{}, closeErr
	}
	// The descriptor identity precedes the read so a replacement or in-place write necessarily changes identity or ctime before validation.
	return data, newIndexCacheFileState(info), nil
}

// snapshotStillCurrent validates the first fingerprint's immutable inputs without constructing a second complete fingerprint.
func (cache *indexCacheSession) snapshotStillCurrent(ctx context.Context) bool {
	checks := []func(context.Context) bool{
		cache.environmentSnapshotStillCurrent,
		cache.optionSnapshotStillCurrent,
		cache.configurationSnapshotStillCurrent,
		cache.sourceMembershipStillCurrent,
		cache.sourceFilesStillCurrent,
		cache.embedSnapshotStillCurrent,
	}
	results := make([]bool, len(checks))
	var workers sync.WaitGroup
	workers.Add(len(checks))
	for index, check := range checks {
		go func() {
			defer workers.Done()
			results[index] = check(ctx)
		}()
	}
	workers.Wait()
	if ctx.Err() != nil {
		return false
	}
	for _, current := range results {
		if !current {
			return false
		}
	}
	return true
}

// environmentSnapshotStillCurrent preserves effective go env semantics while the filesystem checks run in parallel.
func (cache *indexCacheSession) environmentSnapshotStillCurrent(ctx context.Context) bool {
	current, _, err := indexCacheGoEnvironment(ctx, cache.root)
	return err == nil && bytes.Equal(current, cache.goEnvironmentData)
}

// optionSnapshotStillCurrent rejects mutations through slices or maps retained by the caller's option value.
func (cache *indexCacheSession) optionSnapshotStillCurrent(context.Context) bool {
	current, err := indexCacheOptionData(cache.root, cache.options)
	return err == nil && bytes.Equal(current, cache.optionData)
}

// configurationSnapshotStillCurrent validates module, sum, environment, composition, and vendor state from captured bytes.
func (cache *indexCacheSession) configurationSnapshotStillCurrent(ctx context.Context) bool {
	if !indexCacheParallelMatches(ctx, len(cache.inputFiles), func(index int) bool {
		return indexCacheInputFileStillCurrent(cache.inputFiles[index])
	}) {
		return false
	}
	vendorInfo, err := os.Stat(filepath.Join(cache.root, "vendor"))
	if os.IsNotExist(err) {
		return true
	}
	return err == nil && !vendorInfo.IsDir()
}

// indexCacheInputFileStillCurrent uses exact bytes whenever metadata cannot prove the optional input stayed unchanged.
func indexCacheInputFileStillCurrent(input indexCacheInputFile) bool {
	info, err := os.Stat(input.path)
	if !input.present {
		return os.IsNotExist(err)
	}
	if err != nil {
		return false
	}
	if input.state.fastMatches(info) {
		return true
	}
	data, err := os.ReadFile(input.path)
	return err == nil && bytes.Equal(data, input.data)
}

// sourceMembershipStillCurrent avoids a second walk when every visited directory retains its identity and change token.
func (cache *indexCacheSession) sourceMembershipStillCurrent(ctx context.Context) bool {
	directoriesCurrent := indexCacheParallelMatches(ctx, len(cache.sourceDirectories), func(index int) bool {
		directory := cache.sourceDirectories[index]
		info, err := os.Stat(directory.path)
		return err == nil && info.IsDir() && directory.state.fastMatches(info)
	})
	if directoriesCurrent && len(cache.sourceDirectories) != 0 {
		return true
	}

	currentPaths := make([]string, 0, len(cache.packageFiles))
	for _, root := range cache.sourceRoots {
		paths, _, err := indexCacheSourcePaths(ctx, root)
		if err != nil {
			return false
		}
		currentPaths = append(currentPaths, paths...)
	}
	if len(currentPaths) != len(cache.packageFiles) {
		return false
	}
	for index, path := range currentPaths {
		if path != cache.packageFiles[index].path {
			return false
		}
	}
	return true
}

// sourceFilesStillCurrent validates known source paths concurrently and reads bytes only after a metadata miss.
func (cache *indexCacheSession) sourceFilesStillCurrent(ctx context.Context) bool {
	return indexCacheParallelMatches(ctx, len(cache.packageFiles), func(index int) bool {
		file := cache.packageFiles[index]
		info, err := os.Stat(file.path)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if file.state.fastMatches(info) {
			return true
		}
		data, err := os.ReadFile(file.path)
		return err == nil && bytes.Equal(data, file.data)
	})
}

// embedSnapshotStillCurrent rechecks only filesystem structure selected by immutable go:embed directives.
func (cache *indexCacheSession) embedSnapshotStillCurrent(ctx context.Context) bool {
	current, cacheable, err := indexCacheEmbedStructure(ctx, cache.packageFiles)
	return err == nil && cacheable && bytes.Equal(current, cache.embedStructure)
}

// indexCacheParallelMatches bounds metadata fan-out while retaining deterministic failure handling.
func indexCacheParallelMatches(ctx context.Context, count int, match func(int) bool) bool {
	if count == 0 {
		return true
	}
	workerCount := min(runtime.GOMAXPROCS(0), maxSourceParserWorkers, count)
	jobs := make(chan int)
	results := make([]bool, count)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() == nil {
					results[index] = match(index)
				}
			}
		}()
	}
	for index := range count {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return false
	}
	for _, result := range results {
		if !result {
			return false
		}
	}
	return true
}
