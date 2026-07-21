package webindex

import (
	"crypto/sha256"
	"io"
	"os"
	"sync"
)

const (
	// indexCacheMemoryCapacity bounds retained decoded records for long-lived development processes.
	indexCacheMemoryCapacity = 4
	// indexCacheVerificationBufferSize keeps content validation allocation-light without retaining another complete cache payload.
	indexCacheVerificationBufferSize = 32 << 10
)

// indexCacheMemoryEntry retains an immutable decoded record behind the exact bytes published to disk.
type indexCacheMemoryEntry struct {
	path         string
	size         int64
	digest       [sha256.Size]byte
	record       indexCacheRecord
	typedPayload []byte
}

// indexCacheMemoryState is a small move-to-front cache because one watcher normally revisits a single project path.
type indexCacheMemoryState struct {
	sync.Mutex
	entries []indexCacheMemoryEntry
}

var decodedIndexCaches indexCacheMemoryState

// readDecodedIndexCacheRecord reuses decoded state and otherwise decodes only the section required by the current input identity.
func readDecodedIndexCacheRecord(path string, inputHash string) (indexCacheRecord, bool) {
	if entry, exists := decodedIndexCaches.entry(path); exists {
		matches, err := indexCacheFileMatchesMemoryEntry(path, entry)
		if err == nil && matches {
			record := entry.record
			if record.InputHash != inputHash && record.TypedState == nil {
				typedState, ok := decodeIndexCacheTypedState(entry.typedPayload)
				if !ok {
					return indexCacheRecord{}, false
				}
				record.TypedState = typedState
				decodedIndexCaches.retainTypedState(path, entry.digest, typedState)
			}
			decodedIndexCaches.promote(path)
			return record, true
		}
	}
	data, ok := readIndexCacheData(path)
	if !ok {
		return indexCacheRecord{}, false
	}
	envelope, ok := decodeIndexCacheEnvelope(data)
	if !ok {
		return indexCacheRecord{}, false
	}
	if envelope.inputHash != inputHash {
		typedState, typedOK := decodeIndexCacheTypedState(envelope.typed)
		if !typedOK {
			return indexCacheRecord{}, false
		}
		return indexCacheRecord{Version: indexCacheFormatVersion, InputHash: envelope.inputHash, TypedState: typedState}, true
	}
	record, ok := decodeIndexCacheExactRecord(envelope.record, envelope.inputHash)
	if !ok {
		return indexCacheRecord{}, false
	}
	rememberDecodedIndexCacheRecord(path, data, record, envelope.typed)
	return record, true
}

// entry snapshots one immutable memory entry without holding the lock during filesystem work.
func (state *indexCacheMemoryState) entry(path string) (indexCacheMemoryEntry, bool) {
	state.Lock()
	defer state.Unlock()
	for _, entry := range state.entries {
		if entry.path == path {
			return entry, true
		}
	}
	return indexCacheMemoryEntry{}, false
}

// promote moves a verified path to the front without changing its immutable record.
func (state *indexCacheMemoryState) promote(path string) {
	state.Lock()
	defer state.Unlock()
	for index, entry := range state.entries {
		if entry.path != path || index == 0 {
			continue
		}
		copy(state.entries[1:index+1], state.entries[0:index])
		state.entries[0] = entry
		return
	}
}

// retainTypedState replaces raw state only when the cache entry decoded by this caller is still current.
func (state *indexCacheMemoryState) retainTypedState(path string, digest [sha256.Size]byte, typedState *typedSchemaIncrementalState) {
	state.Lock()
	defer state.Unlock()
	for index := range state.entries {
		entry := &state.entries[index]
		if entry.path != path || entry.digest != digest {
			continue
		}
		entry.record.TypedState = typedState
		entry.typedPayload = nil
		return
	}
}

// rememberDecodedIndexCacheRecord replaces one path and evicts the least recently used decoded record.
func rememberDecodedIndexCacheRecord(path string, data []byte, record indexCacheRecord, typedPayload []byte) {
	entry := indexCacheMemoryEntry{
		path:         path,
		size:         int64(len(data)),
		digest:       sha256.Sum256(data),
		record:       record,
		typedPayload: typedPayload,
	}
	decodedIndexCaches.Lock()
	defer decodedIndexCaches.Unlock()
	entries := decodedIndexCaches.entries
	for index := range entries {
		if entries[index].path != path {
			continue
		}
		copy(entries[index:], entries[index+1:])
		entries = entries[:len(entries)-1]
		break
	}
	entries = append(entries, indexCacheMemoryEntry{})
	copy(entries[1:], entries[:len(entries)-1])
	entries[0] = entry
	if len(entries) > indexCacheMemoryCapacity {
		entries = entries[:indexCacheMemoryCapacity]
	}
	decodedIndexCaches.entries = entries
}

// indexCacheFileMatchesMemoryEntry hashes disk bytes so process-local reuse cannot hide external replacement or corruption.
func indexCacheFileMatchesMemoryEntry(path string, entry indexCacheMemoryEntry) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.size || info.Size() > indexCacheMaximumSize {
		return false, err
	}
	digest := sha256.New()
	written, err := io.CopyBuffer(digest, file, make([]byte, indexCacheVerificationBufferSize))
	if err != nil || written != entry.size {
		return false, err
	}
	var actual [sha256.Size]byte
	digest.Sum(actual[:0])
	return actual == entry.digest, nil
}
