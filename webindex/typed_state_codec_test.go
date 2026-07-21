package webindex

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestTypedSchemaIncrementalCodecRoundTrip verifies every typed-state field survives deterministic binary persistence.
func TestTypedSchemaIncrementalCodecRoundTrip(t *testing.T) {
	state := typedSchemaIncrementalCodecFixture()
	encoded, err := encodeTypedSchemaIncrementalState(state)
	if err != nil {
		t.Fatalf("encode typed state: %v", err)
	}
	encodedAgain, err := encodeTypedSchemaIncrementalState(state)
	if err != nil {
		t.Fatalf("encode typed state again: %v", err)
	}
	if !bytes.Equal(encoded, encodedAgain) {
		t.Fatal("identical typed states produced different binary payloads")
	}
	decoded, ok := decodeTypedSchemaIncrementalState(encoded)
	if !ok {
		t.Fatal("decode valid typed state")
	}
	if !reflect.DeepEqual(decoded, state) {
		t.Fatalf("typed state changed after round trip\ngot:  %#v\nwant: %#v", decoded, state)
	}
	reencoded, err := encodeTypedSchemaIncrementalState(decoded)
	if err != nil {
		t.Fatalf("re-encode decoded typed state: %v", err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatal("decoded typed state did not preserve canonical binary bytes")
	}
}

// TestTypedSchemaIncrementalCodecRejectsMalformedPayloads verifies corrupt framing cannot become trusted state.
func TestTypedSchemaIncrementalCodecRejectsMalformedPayloads(t *testing.T) {
	valid, err := encodeTypedSchemaIncrementalState(typedSchemaIncrementalCodecFixture())
	if err != nil {
		t.Fatalf("encode valid typed state: %v", err)
	}
	invalidFlags := append([]byte(nil), valid...)
	invalidFlags[typedSchemaIncrementalCodecFlagsOffset(t, invalidFlags)] = 4
	tests := map[string][]byte{
		"empty":                 nil,
		"wrong magic":           append([]byte("bad!"), valid[len(typedSchemaIncrementalCodecMagic):]...),
		"trailing bytes":        append(append([]byte(nil), valid...), 0),
		"non-canonical varint":  append(append([]byte(nil), []byte(typedSchemaIncrementalCodecMagic)...), 0x82, 0x00),
		"overflowing varint":    append(append([]byte(nil), []byte(typedSchemaIncrementalCodecMagic)...), 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02),
		"unknown package flags": invalidFlags,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, ok := decodeTypedSchemaIncrementalState(payload); ok {
				t.Fatal("decoder accepted malformed typed state")
			}
		})
	}
}

// TestTypedSchemaIncrementalCodecRejectsEveryTruncation verifies no prefix of a valid payload is treated as complete state.
func TestTypedSchemaIncrementalCodecRejectsEveryTruncation(t *testing.T) {
	valid, err := encodeTypedSchemaIncrementalState(typedSchemaIncrementalCodecFixture())
	if err != nil {
		t.Fatalf("encode valid typed state: %v", err)
	}
	for length := range len(valid) {
		if _, ok := decodeTypedSchemaIncrementalState(valid[:length]); ok {
			t.Fatalf("decoder accepted payload truncated to %d of %d bytes", length, len(valid))
		}
	}
}

// TestTypedSchemaIncrementalCodecRejectsOversizedLengths verifies lengths are bounded before any corresponding allocation.
func TestTypedSchemaIncrementalCodecRejectsOversizedLengths(t *testing.T) {
	tests := map[string][]byte{
		"root string": typedSchemaIncrementalCodecOversizedRoot(),
		"packages":    typedSchemaIncrementalCodecOversizedPackages(),
		"imports":     typedSchemaIncrementalCodecOversizedImports(),
		"artifact":    typedSchemaIncrementalCodecOversizedArtifact(),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, ok := decodeTypedSchemaIncrementalState(payload); ok {
				t.Fatal("decoder accepted an oversized typed-state field")
			}
		})
	}
}

// TestTypedSchemaIncrementalCodecRejectsNilState verifies callers cannot accidentally encode an absent section as a state.
func TestTypedSchemaIncrementalCodecRejectsNilState(t *testing.T) {
	if _, err := encodeTypedSchemaIncrementalState(nil); err == nil {
		t.Fatal("nil typed state encoded without an error")
	}
}

// TestTypedSchemaIncrementalEncoderRejectsResourceLimits verifies the writer cannot create payloads the cache or decoder must reject.
func TestTypedSchemaIncrementalEncoderRejectsResourceLimits(t *testing.T) {
	limitTests := []struct {
		name  string
		apply func(*typedSchemaIncrementalEncoder)
	}{
		{name: "raw bytes", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.raw([]byte{1}) }},
		{name: "raw string", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.rawString("x") }},
		{name: "octet", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.octet(1) }},
		{name: "unsigned varint", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.unsigned(1) }},
		{name: "signed varint", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.integer(1) }},
	}
	for _, test := range limitTests {
		t.Run("payload limit "+test.name, func(t *testing.T) {
			encoder := typedSchemaIncrementalEncoder{size: indexCacheMaximumSize}
			test.apply(&encoder)
			if encoder.err == nil || encoder.size != indexCacheMaximumSize {
				t.Fatalf("encoder after overflow = size %d, err %v", encoder.size, encoder.err)
			}
		})
	}

	for _, length := range []int{-1, typedSchemaIncrementalCodecMaximumCollectionLength + 1} {
		t.Run("collection length", func(t *testing.T) {
			encoder := typedSchemaIncrementalEncoder{}
			encoder.count(length, typedSchemaIncrementalCodecMaximumCollectionLength)
			if encoder.err == nil {
				t.Fatalf("encoder accepted collection length %d", length)
			}
		})
	}

	oversizedString := strings.Repeat("x", typedSchemaIncrementalCodecMaximumStringLength+1)
	encoder := typedSchemaIncrementalEncoder{}
	encoder.stringValue(oversizedString)
	if encoder.err == nil {
		t.Fatal("encoder accepted oversized string")
	}
	state := typedSchemaIncrementalCodecFixture()
	state.Root = oversizedString
	if _, err := encodeTypedSchemaIncrementalState(state); err == nil {
		t.Fatal("state encoder accepted oversized root")
	}

	encoder = typedSchemaIncrementalEncoder{}
	encoder.bytesValue(make([]byte, typedSchemaIncrementalArtifactLimit+1))
	if encoder.err == nil {
		t.Fatal("encoder accepted oversized artifact")
	}
}

// TestTypedSchemaIncrementalEncoderPreservesFirstError verifies nested writes stop immediately after a resource violation.
func TestTypedSchemaIncrementalEncoderPreservesFirstError(t *testing.T) {
	sentinel := errors.New("first encoder error")
	tests := []struct {
		name  string
		apply func(*typedSchemaIncrementalEncoder)
	}{
		{name: "raw bytes", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.raw([]byte("changed")) }},
		{name: "raw string", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.rawString("changed") }},
		{name: "octet", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.octet(1) }},
		{name: "unsigned varint", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.unsigned(1) }},
		{name: "signed varint", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.integer(1) }},
		{name: "count", apply: func(encoder *typedSchemaIncrementalEncoder) { encoder.count(1, 1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoder := typedSchemaIncrementalEncoder{data: []byte("before"), size: len("before"), err: sentinel}
			test.apply(&encoder)
			if !errors.Is(encoder.err, sentinel) || encoder.size != len("before") || string(encoder.data) != "before" {
				t.Fatalf("encoder mutated after first error: data=%q size=%d err=%v", encoder.data, encoder.size, encoder.err)
			}
		})
	}
}

// TestTypedSchemaIncrementalDecoderRejectsAllocationAmplification verifies encoded counts and strings share one bounded allocation budget.
func TestTypedSchemaIncrementalDecoderRejectsAllocationAmplification(t *testing.T) {
	decoder := typedSchemaIncrementalDecoder{}
	if decoder.reserve(-1, 1) || decoder.reserve(1, -1) {
		t.Fatal("decoder accepted a negative allocation dimension")
	}
	decoder.reservedAllocation = typedSchemaIncrementalCodecMaximumAllocation
	if decoder.reserve(1, 1) {
		t.Fatal("decoder exceeded its aggregate allocation budget")
	}

	decoder = typedSchemaIncrementalDecoder{
		data:               binary.AppendUvarint(nil, 1),
		reservedAllocation: typedSchemaIncrementalCodecMaximumAllocation,
	}
	if _, ok := decoder.count(1, 0, 1); ok {
		t.Fatal("decoder allocated a collection beyond its aggregate budget")
	}

	decoder = typedSchemaIncrementalDecoder{
		data:               []byte{1, 'x'},
		reservedAllocation: typedSchemaIncrementalCodecMaximumAllocation,
	}
	if _, ok := decoder.stringValue(); ok {
		t.Fatal("decoder copied a string beyond its aggregate budget")
	}
}

// TestTypedSchemaIncrementalDecoderRejectsMalformedUnsignedValues verifies nested lengths use canonical bounded varints too.
func TestTypedSchemaIncrementalDecoderRejectsMalformedUnsignedValues(t *testing.T) {
	for name, payload := range map[string][]byte{
		"truncated":     {0x80},
		"overflowing":   {0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02},
		"non-canonical": {0x81, 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			decoder := typedSchemaIncrementalDecoder{data: payload}
			if _, ok := decoder.unsigned(); ok || decoder.offset != 0 {
				t.Fatalf("decoder accepted malformed unsigned value or advanced: ok=%t offset=%d", ok, decoder.offset)
			}
		})
	}
}

// typedSchemaIncrementalCodecFixture returns a compact state covering every encoded field and discriminator.
func typedSchemaIncrementalCodecFixture() *typedSchemaIncrementalState {
	return &typedSchemaIncrementalState{
		Version:            typedSchemaIncrementalStateVersion,
		Root:               "testdata/project",
		DependencyIdentity: "0123456789abcdef",
		BuildTags:          []string{"integration", "sqlite"},
		GOARCH:             "amd64",
		GoVersion:          "go1.25.0",
		Packages: []typedSchemaIncrementalPackageState{
			{
				Path:     "example.com/dependency",
				Name:     "dependency",
				Artifact: []byte{0, 1, 2, 255},
			},
			{
				Path:     "example.com/application/handler",
				Name:     "handler",
				Selected: true,
				Local:    true,
				Imports: []typedSchemaIncrementalImportState{
					{Path: "alias", Target: "example.com/dependency"},
					{Path: "net/http", Target: "net/http"},
				},
				Source: typedSchemaIncrementalSourceState{
					Directory: "testdata/project/handler",
					Files: []typedSchemaIncrementalFileState{
						{Path: "testdata/project/handler/first.go", ContentHash: "content-a", HeaderHash: "header-a"},
						{Path: "testdata/project/handler/second.go", ContentHash: "content-b", HeaderHash: "header-b"},
					},
					DirectoryFiles: []typedSchemaIncrementalHeaderState{
						{Path: "testdata/project/handler/first.go", HeaderHash: "header-a"},
						{Path: "testdata/project/handler/second_test.go", HeaderHash: "header-test"},
					},
				},
				Artifact:           []byte{3, 4, 5, 0, 255},
				ExpressionPath:     "example.com/application/handler.__goforj_webindex_expr",
				ExpressionArtifact: []byte{6, 7, 8, 0, 255},
				Expressions: []typedSchemaIncrementalExpressionState{
					{
						Name:   "E000000",
						Source: typedSourceRange{File: "testdata/project/handler/first.go", StartOffset: 120, EndOffset: 145, Line: 9},
					},
					{
						Name:     "E000001",
						Source:   typedSourceRange{File: "testdata/project/handler/first.go", StartOffset: -1, EndOffset: 201, Line: 14},
						Constant: &typedSchemaIncrementalConstantState{Kind: "String", Value: "ready"},
					},
				},
			},
		},
	}
}

// typedSchemaIncrementalCodecFlagsOffset locates the first package flags byte through the production decoder primitives.
func typedSchemaIncrementalCodecFlagsOffset(t *testing.T, payload []byte) int {
	t.Helper()
	decoder := typedSchemaIncrementalDecoder{data: payload, offset: len(typedSchemaIncrementalCodecMagic)}
	if _, ok := decoder.integer(); !ok {
		t.Fatal("read fixture version")
	}
	if _, ok := decoder.stringValue(); !ok {
		t.Fatal("read fixture root")
	}
	if _, ok := decoder.stringValue(); !ok {
		t.Fatal("read fixture dependency identity")
	}
	if _, ok := decoder.strings(); !ok {
		t.Fatal("read fixture tags")
	}
	if _, ok := decoder.stringValue(); !ok {
		t.Fatal("read fixture GOARCH")
	}
	if _, ok := decoder.stringValue(); !ok {
		t.Fatal("read fixture Go version")
	}
	count, ok := decoder.count(typedSchemaIncrementalCodecMaximumPackages, 11, typedSchemaIncrementalCodecPackageAllocation)
	if !ok || count == 0 {
		t.Fatal("read fixture package count")
	}
	if _, ok := decoder.stringValue(); !ok {
		t.Fatal("read fixture package path")
	}
	if _, ok := decoder.stringValue(); !ok {
		t.Fatal("read fixture package name")
	}
	return decoder.offset
}

// typedSchemaIncrementalCodecPrefix builds the fixed fields before the package collection.
func typedSchemaIncrementalCodecPrefix() []byte {
	payload := append([]byte(nil), []byte(typedSchemaIncrementalCodecMagic)...)
	payload = binary.AppendVarint(payload, typedSchemaIncrementalStateVersion)
	payload = typedSchemaIncrementalCodecAppendString(payload, "testdata/project")
	payload = typedSchemaIncrementalCodecAppendString(payload, "dependency")
	payload = binary.AppendUvarint(payload, 0)
	payload = typedSchemaIncrementalCodecAppendString(payload, "amd64")
	payload = typedSchemaIncrementalCodecAppendString(payload, "go1.25.0")
	return payload
}

// typedSchemaIncrementalCodecAppendString appends one unchecked string for malformed decoder fixtures.
func typedSchemaIncrementalCodecAppendString(payload []byte, value string) []byte {
	payload = binary.AppendUvarint(payload, uint64(len(value)))
	return append(payload, value...)
}

// typedSchemaIncrementalCodecOversizedRoot builds a state whose first string exceeds the codec limit without allocating its body.
func typedSchemaIncrementalCodecOversizedRoot() []byte {
	payload := append([]byte(nil), []byte(typedSchemaIncrementalCodecMagic)...)
	payload = binary.AppendVarint(payload, typedSchemaIncrementalStateVersion)
	return binary.AppendUvarint(payload, typedSchemaIncrementalCodecMaximumStringLength+1)
}

// typedSchemaIncrementalCodecOversizedPackages builds a state whose package count exceeds the codec limit.
func typedSchemaIncrementalCodecOversizedPackages() []byte {
	payload := typedSchemaIncrementalCodecPrefix()
	return binary.AppendUvarint(payload, typedSchemaIncrementalCodecMaximumPackages+1)
}

// typedSchemaIncrementalCodecOversizedImports builds one package whose nested import count exceeds the codec limit.
func typedSchemaIncrementalCodecOversizedImports() []byte {
	payload := typedSchemaIncrementalCodecPrefix()
	payload = binary.AppendUvarint(payload, 1)
	payload = typedSchemaIncrementalCodecAppendString(payload, "example.com/package")
	payload = typedSchemaIncrementalCodecAppendString(payload, "example")
	payload = append(payload, 0)
	return binary.AppendUvarint(payload, typedSchemaIncrementalCodecMaximumCollectionLength+1)
}

// typedSchemaIncrementalCodecOversizedArtifact builds one package whose artifact length exceeds the importer limit.
func typedSchemaIncrementalCodecOversizedArtifact() []byte {
	payload := typedSchemaIncrementalCodecPrefix()
	payload = binary.AppendUvarint(payload, 1)
	payload = typedSchemaIncrementalCodecAppendString(payload, "example.com/package")
	payload = typedSchemaIncrementalCodecAppendString(payload, "example")
	payload = append(payload, 0)
	payload = binary.AppendUvarint(payload, 0)
	payload = typedSchemaIncrementalCodecAppendString(payload, "")
	payload = binary.AppendUvarint(payload, 0)
	payload = binary.AppendUvarint(payload, 0)
	return binary.AppendUvarint(payload, typedSchemaIncrementalArtifactLimit+1)
}
