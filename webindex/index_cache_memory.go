package webindex

import (
	"io"
	"os"
	"sync"
)

const (
	// indexCacheMemoryCapacity bounds retained decoded records for long-lived development processes.
	indexCacheMemoryCapacity = 4
)

// indexCacheMemoryEntry retains decoded sections behind the checksummed layout published to disk.
type indexCacheMemoryEntry struct {
	path       string
	layout     indexCacheEnvelopeLayout
	record     indexCacheRecord
	typedState *typedSchemaIncrementalState
}

// indexCacheMemoryState is a small move-to-front cache because one watcher normally revisits a single project path.
type indexCacheMemoryState struct {
	sync.Mutex
	entries []indexCacheMemoryEntry
}

var decodedIndexCaches indexCacheMemoryState

// readDecodedIndexCacheRecord validates only the section required by the current input identity.
func readDecodedIndexCacheRecord(path string, inputHash string) (indexCacheRecord, bool) {
	file, err := os.Open(path)
	if err != nil {
		return indexCacheRecord{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > indexCacheMaximumSize {
		return indexCacheRecord{}, false
	}
	entry, _ := decodedIndexCaches.entry(path)
	record, layout, exact, ok := decodeIndexCacheReader(file, info.Size(), inputHash, entry)
	if !ok {
		return indexCacheRecord{}, false
	}
	if exact {
		if entry.path == path && entry.layout == layout {
			decodedIndexCaches.promote(path)
		} else {
			rememberDecodedIndexCacheRecord(path, layout, record)
		}
		return record, true
	}
	if entry.path == path && entry.layout == layout && entry.typedState == nil {
		decodedIndexCaches.retainTypedState(path, layout, record.TypedState)
	}
	return record, true
}

// decodeIndexCacheReader validates one persisted section while leaving the unrelated section unread.
func decodeIndexCacheReader(reader io.ReaderAt, size int64, inputHash string, entry indexCacheMemoryEntry) (indexCacheRecord, indexCacheEnvelopeLayout, bool, bool) {
	layout, ok := decodeIndexCacheEnvelopeLayout(reader, size)
	if !ok {
		return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
	}
	if layout.inputHash != inputHash {
		typedPayload, valid := readIndexCacheEnvelopeSection(reader, layout.typedOffset, layout.typedLength, layout.typedDigest)
		if !valid {
			return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
		}
		var typedState *typedSchemaIncrementalState
		if entry.layout == layout && entry.typedState != nil {
			typedState = entry.typedState
		} else {
			typedState, valid = decodeIndexCacheTypedState(typedPayload)
			if !valid {
				return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
			}
		}
		return indexCacheRecord{Version: indexCacheFormatVersion, InputHash: layout.inputHash, TypedState: typedState}, layout, false, true
	}
	recordPayload, ok := readIndexCacheEnvelopeSection(reader, layout.recordOffset, layout.recordLength, layout.recordDigest)
	if !ok {
		return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
	}
	if entry.layout == layout {
		record := entry.record
		record.TypedState = entry.typedState
		return record, layout, true, true
	}
	record, ok := decodeIndexCacheExactRecord(recordPayload, layout.inputHash)
	if !ok {
		return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
	}
	return record, layout, true, true
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

// retainTypedState keeps decoded incremental state only when the layout observed by this caller is still current.
func (state *indexCacheMemoryState) retainTypedState(path string, layout indexCacheEnvelopeLayout, typedState *typedSchemaIncrementalState) {
	state.Lock()
	defer state.Unlock()
	for index := range state.entries {
		entry := &state.entries[index]
		if entry.path != path || entry.layout != layout {
			continue
		}
		entry.typedState = typedState
		return
	}
}

// rememberDecodedIndexCacheRecord replaces one path and evicts the least recently used decoded record.
func rememberDecodedIndexCacheRecord(path string, layout indexCacheEnvelopeLayout, record indexCacheRecord) {
	typedState := record.TypedState
	record.TypedState = nil
	entry := indexCacheMemoryEntry{
		path:       path,
		layout:     layout,
		record:     record,
		typedState: typedState,
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
