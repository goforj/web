package webindex

import (
	"io"
	"os"
	"sync"
)

const (
	// indexCacheMemoryCapacity bounds bookkeeping when several small projects share one long-lived process.
	indexCacheMemoryCapacity = 4
	// indexCacheMemoryMaximumEncodedBytes limits the validated on-disk size represented by retained decoded entries.
	indexCacheMemoryMaximumEncodedBytes int64 = 16 << 20
)

// indexCacheMemoryEntry retains decoded sections behind the checksummed layout published to disk.
type indexCacheMemoryEntry struct {
	path       string
	layout     indexCacheEnvelopeLayout
	record     indexCacheRecord
	typedState *typedSchemaIncrementalState
}

// indexCacheMemoryState retains an encoded-size-bounded LRU of decoded project records for long-lived watcher processes.
type indexCacheMemoryState struct {
	sync.Mutex
	entries []indexCacheMemoryEntry
}

var decodedIndexCaches indexCacheMemoryState

// readDecodedIndexCacheRecord validates only the section required by the current input identity.
func readDecodedIndexCacheRecord(path string, inputHash string) (indexCacheRecord, bool) {
	entry, exists := decodedIndexCaches.entry(path)
	file, err := os.Open(path)
	if err != nil {
		if exists {
			decodedIndexCaches.forget(path, entry.layout)
		}
		return indexCacheRecord{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > indexCacheMaximumSize {
		if exists {
			decodedIndexCaches.forget(path, entry.layout)
		}
		return indexCacheRecord{}, false
	}
	record, layout, exact, ok := decodeIndexCacheReader(file, info.Size(), inputHash, entry)
	if !ok {
		if exists {
			decodedIndexCaches.forget(path, entry.layout)
		}
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
	if entry.path == path && entry.layout == layout {
		decodedIndexCaches.promote(path)
	}
	return record, true
}

// promote moves a verified path to the front so encoded-size eviction discards the least recently used record.
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

// forget removes invalid decoded state only when no newer record replaced the entry during filesystem verification.
func (state *indexCacheMemoryState) forget(path string, layout indexCacheEnvelopeLayout) {
	state.Lock()
	defer state.Unlock()
	for index, entry := range state.entries {
		if entry.path != path || entry.layout != layout {
			continue
		}
		copy(state.entries[index:], state.entries[index+1:])
		state.entries[len(state.entries)-1] = indexCacheMemoryEntry{}
		state.entries = state.entries[:len(state.entries)-1]
		return
	}
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

// rememberDecodedIndexCacheRecord retains recently used records only while count and encoded-size bounds both hold.
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
		entries[len(entries)-1] = indexCacheMemoryEntry{}
		entries = entries[:len(entries)-1]
		break
	}
	if layout.size <= 0 || layout.size > indexCacheMemoryMaximumEncodedBytes {
		decodedIndexCaches.entries = entries
		return
	}
	entries = append(entries, indexCacheMemoryEntry{})
	copy(entries[1:], entries[:len(entries)-1])
	entries[0] = entry
	retainedEncodedBytes := int64(0)
	retainedCount := 0
	for retainedCount < len(entries) && retainedCount < indexCacheMemoryCapacity {
		candidateSize := entries[retainedCount].layout.size
		if candidateSize <= 0 || candidateSize > indexCacheMemoryMaximumEncodedBytes-retainedEncodedBytes {
			break
		}
		retainedEncodedBytes += candidateSize
		retainedCount++
	}
	if retainedCount < len(entries) {
		for index := retainedCount; index < len(entries); index++ {
			entries[index] = indexCacheMemoryEntry{}
		}
		entries = entries[:retainedCount]
	}
	decodedIndexCaches.entries = entries
}
