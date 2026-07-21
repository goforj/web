package webindex

import (
	"io"
	"os"
)

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
	record, _, _, ok := decodeIndexCacheReader(file, info.Size(), inputHash)
	return record, ok
}

// decodeIndexCacheReader validates one persisted section while leaving the unrelated section unread.
func decodeIndexCacheReader(reader io.ReaderAt, size int64, inputHash string) (indexCacheRecord, indexCacheEnvelopeLayout, bool, bool) {
	layout, ok := decodeIndexCacheEnvelopeLayout(reader, size)
	if !ok {
		return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
	}
	if layout.inputHash != inputHash {
		typedPayload, valid := readIndexCacheEnvelopeSection(reader, layout.typedOffset, layout.typedLength, layout.typedDigest)
		if !valid {
			return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
		}
		typedState, valid := decodeIndexCacheTypedState(typedPayload)
		if !valid {
			return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
		}
		return indexCacheRecord{Version: indexCacheFormatVersion, InputHash: layout.inputHash, TypedState: typedState}, layout, false, true
	}
	recordPayload, ok := readIndexCacheEnvelopeSection(reader, layout.recordOffset, layout.recordLength, layout.recordDigest)
	if !ok {
		return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
	}
	record, ok := decodeIndexCacheExactRecord(recordPayload, layout.inputHash)
	if !ok {
		return indexCacheRecord{}, indexCacheEnvelopeLayout{}, false, false
	}
	return record, layout, true, true
}
