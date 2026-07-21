package webindex

import (
	"bytes"
	"encoding/binary"
	"reflect"
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

// typedSchemaIncrementalCodecFixture returns a compact state covering every encoded field and discriminator.
func typedSchemaIncrementalCodecFixture() *typedSchemaIncrementalState {
	return &typedSchemaIncrementalState{
		Version:            typedSchemaIncrementalStateVersion,
		Root:               "/workspace/example",
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
					Directory: "/workspace/example/handler",
					Files: []typedSchemaIncrementalFileState{
						{Path: "/workspace/example/handler/first.go", ContentHash: "content-a", HeaderHash: "header-a"},
						{Path: "/workspace/example/handler/second.go", ContentHash: "content-b", HeaderHash: "header-b"},
					},
					DirectoryFiles: []typedSchemaIncrementalHeaderState{
						{Path: "/workspace/example/handler/first.go", HeaderHash: "header-a"},
						{Path: "/workspace/example/handler/second_test.go", HeaderHash: "header-test"},
					},
				},
				Artifact:           []byte{3, 4, 5, 0, 255},
				ExpressionPath:     "example.com/application/handler.__goforj_webindex_expr",
				ExpressionArtifact: []byte{6, 7, 8, 0, 255},
				Expressions: []typedSchemaIncrementalExpressionState{
					{
						Name:   "E000000",
						Source: typedSourceRange{File: "/workspace/example/handler/first.go", StartOffset: 120, EndOffset: 145, Line: 9},
					},
					{
						Name:     "E000001",
						Source:   typedSourceRange{File: "/workspace/example/handler/first.go", StartOffset: -1, EndOffset: 201, Line: 14},
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
	payload = typedSchemaIncrementalCodecAppendString(payload, "/workspace/example")
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
