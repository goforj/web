package webindex

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	// typedSchemaIncrementalCodecMagic rejects typed-state payloads written by incompatible encoders.
	typedSchemaIncrementalCodecMagic = "gwi\x01"
	// typedSchemaIncrementalCodecMaximumPackages prevents a tiny corrupt payload from reserving a huge package slice.
	typedSchemaIncrementalCodecMaximumPackages = 1 << 16
	// typedSchemaIncrementalCodecMaximumCollectionLength bounds every nested slice before allocation.
	typedSchemaIncrementalCodecMaximumCollectionLength = 1 << 20
	// typedSchemaIncrementalCodecMaximumStringLength allows large constants while rejecting unreasonable metadata fields.
	typedSchemaIncrementalCodecMaximumStringLength = typedSchemaIncrementalArtifactLimit
	// typedSchemaIncrementalCodecMaximumAllocation bounds aggregate decoder-owned memory independently of the payload size.
	typedSchemaIncrementalCodecMaximumAllocation = indexCacheMaximumSize * 2
)

const (
	// typedSchemaIncrementalCodecPackageAllocation approximates one package entry's slice backing cost.
	typedSchemaIncrementalCodecPackageAllocation = 256
	// typedSchemaIncrementalCodecImportAllocation approximates one import entry's slice backing cost.
	typedSchemaIncrementalCodecImportAllocation = 32
	// typedSchemaIncrementalCodecFileAllocation approximates one compiled-file entry's slice backing cost.
	typedSchemaIncrementalCodecFileAllocation = 48
	// typedSchemaIncrementalCodecHeaderAllocation approximates one directory-file entry's slice backing cost.
	typedSchemaIncrementalCodecHeaderAllocation = 32
	// typedSchemaIncrementalCodecExpressionAllocation approximates one expression entry's slice backing cost.
	typedSchemaIncrementalCodecExpressionAllocation = 72
	// typedSchemaIncrementalCodecStringAllocation approximates one string entry's slice backing cost.
	typedSchemaIncrementalCodecStringAllocation = 16
)

// typedSchemaIncrementalEncoder appends one deterministic typed-state payload while enforcing cache size limits.
type typedSchemaIncrementalEncoder struct {
	data []byte
	size int
	err  error
}

// typedSchemaIncrementalDecoder reads one typed-state payload without trusting encoded lengths or counts.
type typedSchemaIncrementalDecoder struct {
	data               []byte
	offset             int
	reservedAllocation uint64
}

// encodeTypedSchemaIncrementalState encodes the private package graph without JSON's byte and field-name overhead.
func encodeTypedSchemaIncrementalState(state *typedSchemaIncrementalState) ([]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("encode API index typed state: state is nil")
	}
	sizer := typedSchemaIncrementalEncoder{}
	sizer.state(state)
	if sizer.err != nil {
		return nil, sizer.err
	}
	encoder := typedSchemaIncrementalEncoder{data: make([]byte, 0, sizer.size)}
	encoder.state(state)
	if encoder.err != nil {
		return nil, encoder.err
	}
	return encoder.data, nil
}

// state traverses every field identically during validation sizing and final encoding.
func (encoder *typedSchemaIncrementalEncoder) state(state *typedSchemaIncrementalState) {
	encoder.raw([]byte(typedSchemaIncrementalCodecMagic))
	encoder.integer(state.Version)
	encoder.stringValue(state.Root)
	encoder.stringValue(state.DependencyIdentity)
	encoder.strings(state.BuildTags)
	encoder.stringValue(state.GOARCH)
	encoder.stringValue(state.GoVersion)
	encoder.packages(state.Packages)
}

// decodeTypedSchemaIncrementalState decodes a bounded payload and rejects trailing or non-canonical bytes.
func decodeTypedSchemaIncrementalState(payload []byte) (*typedSchemaIncrementalState, bool) {
	if len(payload) < len(typedSchemaIncrementalCodecMagic) || len(payload) > indexCacheMaximumSize || !bytes.HasPrefix(payload, []byte(typedSchemaIncrementalCodecMagic)) {
		return nil, false
	}
	decoder := typedSchemaIncrementalDecoder{data: payload, offset: len(typedSchemaIncrementalCodecMagic)}
	version, ok := decoder.integer()
	if !ok {
		return nil, false
	}
	root, ok := decoder.stringValue()
	if !ok {
		return nil, false
	}
	dependencyIdentity, ok := decoder.stringValue()
	if !ok {
		return nil, false
	}
	buildTags, ok := decoder.strings()
	if !ok {
		return nil, false
	}
	goarch, ok := decoder.stringValue()
	if !ok {
		return nil, false
	}
	goVersion, ok := decoder.stringValue()
	if !ok {
		return nil, false
	}
	packages, ok := decoder.packages()
	if !ok || decoder.offset != len(decoder.data) {
		return nil, false
	}
	return &typedSchemaIncrementalState{
		Version:            version,
		Root:               root,
		DependencyIdentity: dependencyIdentity,
		BuildTags:          buildTags,
		GOARCH:             goarch,
		GoVersion:          goVersion,
		Packages:           packages,
	}, true
}

// raw appends bytes only while the complete payload remains cacheable.
func (encoder *typedSchemaIncrementalEncoder) raw(value []byte) {
	if encoder.err != nil {
		return
	}
	if len(value) > indexCacheMaximumSize-encoder.size {
		encoder.err = fmt.Errorf("encode API index typed state: payload exceeds %d bytes", indexCacheMaximumSize)
		return
	}
	encoder.size += len(value)
	if encoder.data != nil {
		encoder.data = append(encoder.data, value...)
	}
}

// rawString appends a string directly so the sizing pass and final write avoid temporary byte slices.
func (encoder *typedSchemaIncrementalEncoder) rawString(value string) {
	if encoder.err != nil {
		return
	}
	if len(value) > indexCacheMaximumSize-encoder.size {
		encoder.err = fmt.Errorf("encode API index typed state: payload exceeds %d bytes", indexCacheMaximumSize)
		return
	}
	encoder.size += len(value)
	if encoder.data != nil {
		encoder.data = append(encoder.data, value...)
	}
}

// octet appends one discriminator without constructing a temporary slice.
func (encoder *typedSchemaIncrementalEncoder) octet(value byte) {
	if encoder.err != nil {
		return
	}
	if encoder.size == indexCacheMaximumSize {
		encoder.err = fmt.Errorf("encode API index typed state: payload exceeds %d bytes", indexCacheMaximumSize)
		return
	}
	encoder.size++
	if encoder.data != nil {
		encoder.data = append(encoder.data, value)
	}
}

// unsigned appends one canonical unsigned varint.
func (encoder *typedSchemaIncrementalEncoder) unsigned(value uint64) {
	if encoder.err != nil {
		return
	}
	var encoded [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(encoded[:], value)
	encoder.raw(encoded[:length])
}

// integer appends one canonical signed varint.
func (encoder *typedSchemaIncrementalEncoder) integer(value int) {
	if encoder.err != nil {
		return
	}
	var encoded [binary.MaxVarintLen64]byte
	length := binary.PutVarint(encoded[:], int64(value))
	encoder.raw(encoded[:length])
}

// count appends a collection length only when it is within its format-specific limit.
func (encoder *typedSchemaIncrementalEncoder) count(length int, maximum int) {
	if encoder.err != nil {
		return
	}
	if length < 0 || length > maximum {
		encoder.err = fmt.Errorf("encode API index typed state: collection length %d exceeds %d", length, maximum)
		return
	}
	encoder.unsigned(uint64(length))
}

// stringValue appends a length-delimited string without field-name repetition.
func (encoder *typedSchemaIncrementalEncoder) stringValue(value string) {
	if len(value) > typedSchemaIncrementalCodecMaximumStringLength {
		encoder.err = fmt.Errorf("encode API index typed state: string length %d exceeds %d", len(value), typedSchemaIncrementalCodecMaximumStringLength)
		return
	}
	encoder.unsigned(uint64(len(value)))
	encoder.rawString(value)
}

// bytesValue appends an artifact after applying the same per-package limit used by the artifact importer.
func (encoder *typedSchemaIncrementalEncoder) bytesValue(value []byte) {
	if len(value) > typedSchemaIncrementalArtifactLimit {
		encoder.err = fmt.Errorf("encode API index typed state: artifact length %d exceeds %d", len(value), typedSchemaIncrementalArtifactLimit)
		return
	}
	encoder.unsigned(uint64(len(value)))
	encoder.raw(value)
}

// strings appends a deterministic string slice in its existing semantic order.
func (encoder *typedSchemaIncrementalEncoder) strings(values []string) {
	encoder.count(len(values), typedSchemaIncrementalCodecMaximumCollectionLength)
	for _, value := range values {
		encoder.stringValue(value)
	}
}

// packages appends every package and its nested source and expression evidence.
func (encoder *typedSchemaIncrementalEncoder) packages(packages []typedSchemaIncrementalPackageState) {
	encoder.count(len(packages), typedSchemaIncrementalCodecMaximumPackages)
	for index := range packages {
		encoder.packageState(&packages[index])
	}
}

// packageState appends one package graph node in a fixed field order.
func (encoder *typedSchemaIncrementalEncoder) packageState(state *typedSchemaIncrementalPackageState) {
	encoder.stringValue(state.Path)
	encoder.stringValue(state.Name)
	var flags byte
	if state.Selected {
		flags |= 1
	}
	if state.Local {
		flags |= 2
	}
	encoder.octet(flags)
	encoder.count(len(state.Imports), typedSchemaIncrementalCodecMaximumCollectionLength)
	for index := range state.Imports {
		encoder.stringValue(state.Imports[index].Path)
		encoder.stringValue(state.Imports[index].Target)
	}
	encoder.sourceState(&state.Source)
	encoder.bytesValue(state.Artifact)
	encoder.stringValue(state.ExpressionPath)
	encoder.bytesValue(state.ExpressionArtifact)
	encoder.count(len(state.Expressions), typedSchemaIncrementalCodecMaximumCollectionLength)
	for index := range state.Expressions {
		encoder.expressionState(&state.Expressions[index])
	}
}

// sourceState appends package source evidence in the order produced by the incremental state builder.
func (encoder *typedSchemaIncrementalEncoder) sourceState(state *typedSchemaIncrementalSourceState) {
	encoder.stringValue(state.Directory)
	encoder.count(len(state.Files), typedSchemaIncrementalCodecMaximumCollectionLength)
	for index := range state.Files {
		encoder.stringValue(state.Files[index].Path)
		encoder.stringValue(state.Files[index].ContentHash)
		encoder.stringValue(state.Files[index].HeaderHash)
	}
	encoder.count(len(state.DirectoryFiles), typedSchemaIncrementalCodecMaximumCollectionLength)
	for index := range state.DirectoryFiles {
		encoder.stringValue(state.DirectoryFiles[index].Path)
		encoder.stringValue(state.DirectoryFiles[index].HeaderHash)
	}
}

// expressionState appends one source range and its optional constant value.
func (encoder *typedSchemaIncrementalEncoder) expressionState(state *typedSchemaIncrementalExpressionState) {
	encoder.stringValue(state.Name)
	encoder.stringValue(state.Source.File)
	encoder.integer(state.Source.StartOffset)
	encoder.integer(state.Source.EndOffset)
	encoder.integer(state.Source.Line)
	if state.Constant == nil {
		encoder.octet(0)
		return
	}
	encoder.octet(1)
	encoder.stringValue(state.Constant.Kind)
	encoder.stringValue(state.Constant.Value)
}

// remaining reports unread bytes so every decoded count can be checked before allocation.
func (decoder *typedSchemaIncrementalDecoder) remaining() int {
	return len(decoder.data) - decoder.offset
}

// reserve records estimated decoder-owned memory and rejects aggregate allocation amplification.
func (decoder *typedSchemaIncrementalDecoder) reserve(count int, elementSize int) bool {
	if count < 0 || elementSize < 0 {
		return false
	}
	allocation := uint64(count) * uint64(elementSize)
	if allocation > typedSchemaIncrementalCodecMaximumAllocation-decoder.reservedAllocation {
		return false
	}
	decoder.reservedAllocation += allocation
	return true
}

// unsigned reads one canonical unsigned varint.
func (decoder *typedSchemaIncrementalDecoder) unsigned() (uint64, bool) {
	if decoder.remaining() <= 0 {
		return 0, false
	}
	value, length := binary.Uvarint(decoder.data[decoder.offset:])
	if length <= 0 {
		return 0, false
	}
	var canonical [binary.MaxVarintLen64]byte
	if binary.PutUvarint(canonical[:], value) != length {
		return 0, false
	}
	decoder.offset += length
	return value, true
}

// integer reads one canonical signed varint that fits the current platform's int width.
func (decoder *typedSchemaIncrementalDecoder) integer() (int, bool) {
	if decoder.remaining() <= 0 {
		return 0, false
	}
	value, length := binary.Varint(decoder.data[decoder.offset:])
	if length <= 0 {
		return 0, false
	}
	var canonical [binary.MaxVarintLen64]byte
	if binary.PutVarint(canonical[:], value) != length {
		return 0, false
	}
	converted := int(value)
	if int64(converted) != value {
		return 0, false
	}
	decoder.offset += length
	return converted, true
}

// count reads a collection length and verifies both its format limit and minimum encoded bytes.
func (decoder *typedSchemaIncrementalDecoder) count(maximum int, minimumElementBytes int, allocationSize int) (int, bool) {
	value, ok := decoder.unsigned()
	if !ok || value > uint64(maximum) {
		return 0, false
	}
	count := int(value)
	if minimumElementBytes > 0 && count > decoder.remaining()/minimumElementBytes {
		return 0, false
	}
	if !decoder.reserve(count, allocationSize) {
		return 0, false
	}
	return count, true
}

// stringValue reads a bounded string and accounts for the copy required by safe string conversion.
func (decoder *typedSchemaIncrementalDecoder) stringValue() (string, bool) {
	length, ok := decoder.unsigned()
	if !ok || length > typedSchemaIncrementalCodecMaximumStringLength || length > uint64(decoder.remaining()) {
		return "", false
	}
	if !decoder.reserve(int(length), 1) {
		return "", false
	}
	end := decoder.offset + int(length)
	value := string(decoder.data[decoder.offset:end])
	decoder.offset = end
	return value, true
}

// bytesValue reads a bounded artifact as a zero-copy slice of the immutable cache payload.
func (decoder *typedSchemaIncrementalDecoder) bytesValue() ([]byte, bool) {
	length, ok := decoder.unsigned()
	if !ok || length > typedSchemaIncrementalArtifactLimit || length > uint64(decoder.remaining()) {
		return nil, false
	}
	end := decoder.offset + int(length)
	if end == decoder.offset {
		return nil, true
	}
	value := decoder.data[decoder.offset:end]
	decoder.offset = end
	return value, true
}

// byteValue reads one discriminator byte.
func (decoder *typedSchemaIncrementalDecoder) byteValue() (byte, bool) {
	if decoder.remaining() <= 0 {
		return 0, false
	}
	value := decoder.data[decoder.offset]
	decoder.offset++
	return value, true
}

// strings reads a string slice after bounding its backing array.
func (decoder *typedSchemaIncrementalDecoder) strings() ([]string, bool) {
	count, ok := decoder.count(typedSchemaIncrementalCodecMaximumCollectionLength, 1, typedSchemaIncrementalCodecStringAllocation)
	if !ok || count == 0 {
		return nil, ok
	}
	values := make([]string, count)
	for index := range values {
		values[index], ok = decoder.stringValue()
		if !ok {
			return nil, false
		}
	}
	return values, true
}

// packages reads the complete package graph after bounding its backing array.
func (decoder *typedSchemaIncrementalDecoder) packages() ([]typedSchemaIncrementalPackageState, bool) {
	count, ok := decoder.count(typedSchemaIncrementalCodecMaximumPackages, 11, typedSchemaIncrementalCodecPackageAllocation)
	if !ok || count == 0 {
		return nil, ok
	}
	packages := make([]typedSchemaIncrementalPackageState, count)
	for index := range packages {
		if !decoder.packageState(&packages[index]) {
			return nil, false
		}
	}
	return packages, true
}

// packageState reads one package graph node and rejects unknown flag bits.
func (decoder *typedSchemaIncrementalDecoder) packageState(state *typedSchemaIncrementalPackageState) bool {
	var ok bool
	state.Path, ok = decoder.stringValue()
	if !ok {
		return false
	}
	state.Name, ok = decoder.stringValue()
	if !ok {
		return false
	}
	flags, ok := decoder.byteValue()
	if !ok || flags&^byte(3) != 0 {
		return false
	}
	state.Selected = flags&1 != 0
	state.Local = flags&2 != 0
	importCount, ok := decoder.count(typedSchemaIncrementalCodecMaximumCollectionLength, 2, typedSchemaIncrementalCodecImportAllocation)
	if !ok {
		return false
	}
	if importCount > 0 {
		state.Imports = make([]typedSchemaIncrementalImportState, importCount)
	}
	for index := range state.Imports {
		state.Imports[index].Path, ok = decoder.stringValue()
		if !ok {
			return false
		}
		state.Imports[index].Target, ok = decoder.stringValue()
		if !ok {
			return false
		}
	}
	if !decoder.sourceState(&state.Source) {
		return false
	}
	state.Artifact, ok = decoder.bytesValue()
	if !ok {
		return false
	}
	state.ExpressionPath, ok = decoder.stringValue()
	if !ok {
		return false
	}
	state.ExpressionArtifact, ok = decoder.bytesValue()
	if !ok {
		return false
	}
	expressionCount, ok := decoder.count(typedSchemaIncrementalCodecMaximumCollectionLength, 6, typedSchemaIncrementalCodecExpressionAllocation)
	if !ok {
		return false
	}
	if expressionCount > 0 {
		state.Expressions = make([]typedSchemaIncrementalExpressionState, expressionCount)
	}
	for index := range state.Expressions {
		if !decoder.expressionState(&state.Expressions[index]) {
			return false
		}
	}
	return true
}

// sourceState reads package source evidence after bounding both nested slices.
func (decoder *typedSchemaIncrementalDecoder) sourceState(state *typedSchemaIncrementalSourceState) bool {
	var ok bool
	state.Directory, ok = decoder.stringValue()
	if !ok {
		return false
	}
	fileCount, ok := decoder.count(typedSchemaIncrementalCodecMaximumCollectionLength, 3, typedSchemaIncrementalCodecFileAllocation)
	if !ok {
		return false
	}
	if fileCount > 0 {
		state.Files = make([]typedSchemaIncrementalFileState, fileCount)
	}
	for index := range state.Files {
		state.Files[index].Path, ok = decoder.stringValue()
		if !ok {
			return false
		}
		state.Files[index].ContentHash, ok = decoder.stringValue()
		if !ok {
			return false
		}
		state.Files[index].HeaderHash, ok = decoder.stringValue()
		if !ok {
			return false
		}
	}
	headerCount, ok := decoder.count(typedSchemaIncrementalCodecMaximumCollectionLength, 2, typedSchemaIncrementalCodecHeaderAllocation)
	if !ok {
		return false
	}
	if headerCount > 0 {
		state.DirectoryFiles = make([]typedSchemaIncrementalHeaderState, headerCount)
	}
	for index := range state.DirectoryFiles {
		state.DirectoryFiles[index].Path, ok = decoder.stringValue()
		if !ok {
			return false
		}
		state.DirectoryFiles[index].HeaderHash, ok = decoder.stringValue()
		if !ok {
			return false
		}
	}
	return true
}

// expressionState reads one expression range and a strictly encoded optional constant.
func (decoder *typedSchemaIncrementalDecoder) expressionState(state *typedSchemaIncrementalExpressionState) bool {
	var ok bool
	state.Name, ok = decoder.stringValue()
	if !ok {
		return false
	}
	state.Source.File, ok = decoder.stringValue()
	if !ok {
		return false
	}
	state.Source.StartOffset, ok = decoder.integer()
	if !ok {
		return false
	}
	state.Source.EndOffset, ok = decoder.integer()
	if !ok {
		return false
	}
	state.Source.Line, ok = decoder.integer()
	if !ok {
		return false
	}
	present, ok := decoder.byteValue()
	if !ok || present > 1 {
		return false
	}
	if present == 0 {
		return true
	}
	constant := &typedSchemaIncrementalConstantState{}
	constant.Kind, ok = decoder.stringValue()
	if !ok {
		return false
	}
	constant.Value, ok = decoder.stringValue()
	if !ok {
		return false
	}
	state.Constant = constant
	return true
}
