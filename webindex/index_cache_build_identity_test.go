package webindex

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

const (
	// testGoExecutableBuildIDFirst represents one ordinary content-derived Go executable identity.
	testGoExecutableBuildIDFirst = "AAAAAAAAAAAAAAAAAAAA/BBBBBBBBBBBBBBBBBBBB/CCCCCCCCCCCCCCCCCCCC/DDDDDDDDDDDDDDDDDDDD"
	// testGoExecutableBuildIDSecond changes one action ID while retaining the standard linker shape.
	testGoExecutableBuildIDSecond = "EEEEEEEEEEEEEEEEEEEE/BBBBBBBBBBBBBBBBBBBB/CCCCCCCCCCCCCCCCCCCC/DDDDDDDDDDDDDDDDDDDD"
)

// TestIndexCacheAnalyzerModuleFromBuildInfo verifies module discovery prefers the main module and safely handles incomplete dependency metadata.
func TestIndexCacheAnalyzerModuleFromBuildInfo(t *testing.T) {
	mainModule := debug.Module{Path: indexCacheAnalyzerModulePath, Version: "v1.0.0"}
	dependencyModule := debug.Module{Path: indexCacheAnalyzerModulePath, Version: "v2.0.0"}
	otherModule := debug.Module{Path: "example.com/other", Version: "v3.0.0"}
	tests := []struct {
		name      string
		buildInfo *debug.BuildInfo
		available bool
		want      debug.Module
		found     bool
	}{
		{name: "unavailable", available: false},
		{name: "nil metadata", available: true},
		{
			name:      "main module",
			buildInfo: &debug.BuildInfo{Main: mainModule, Deps: []*debug.Module{&dependencyModule}},
			available: true,
			want:      mainModule,
			found:     true,
		},
		{
			name:      "dependency after nil entry",
			buildInfo: &debug.BuildInfo{Main: otherModule, Deps: []*debug.Module{nil, &otherModule, &dependencyModule}},
			available: true,
			want:      dependencyModule,
			found:     true,
		},
		{
			name:      "module absent",
			buildInfo: &debug.BuildInfo{Main: otherModule, Deps: []*debug.Module{nil, &otherModule}},
			available: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, found := indexCacheAnalyzerModuleFromBuildInfo(test.buildInfo, test.available)
			if found != test.found || module.Path != test.want.Path || module.Version != test.want.Version {
				t.Fatalf("selected module = %#v, found=%t; want %#v, found=%t", module, found, test.want, test.found)
			}
		})
	}
}

// TestIndexCacheAnalyzerBuildIdentityForPublishedModule verifies immutable module metadata does not depend on a machine-specific executable.
func TestIndexCacheAnalyzerBuildIdentityForPublishedModule(t *testing.T) {
	called := false
	identity, ok := indexCacheAnalyzerBuildIdentityForModule(debug.Module{
		Path:    indexCacheAnalyzerModulePath,
		Version: "v0.7.0",
		Sum:     "h1:published",
	}, true, func() (string, bool) {
		called = true
		return "", false
	})
	if !ok {
		t.Fatal("published analyzer identity was rejected")
	}
	if called {
		t.Fatal("published analyzer identity read the executable build ID")
	}
	var decoded indexCacheBuildModuleIdentity
	if err := json.Unmarshal([]byte(identity), &decoded); err != nil {
		t.Fatalf("decode published analyzer identity: %v", err)
	}
	if decoded.Version != "v0.7.0" || decoded.Sum != "h1:published" || decoded.ExecutableBuildID != "" {
		t.Fatalf("published analyzer identity = %#v", decoded)
	}
}

// TestIndexCacheAnalyzerBuildIdentityForLocalModule verifies compiled code changes invalidate unversioned analyzer identities.
func TestIndexCacheAnalyzerBuildIdentityForLocalModule(t *testing.T) {
	modules := []debug.Module{
		{Path: indexCacheAnalyzerModulePath, Version: "(devel)"},
		{
			Path:    indexCacheAnalyzerModulePath,
			Version: "v0.0.0",
			Replace: &debug.Module{Path: "../web"},
		},
	}
	for _, module := range modules {
		first, ok := indexCacheAnalyzerBuildIdentityForModule(module, true, func() (string, bool) {
			return testGoExecutableBuildIDFirst, true
		})
		if !ok {
			t.Fatalf("local analyzer identity was rejected for %#v", module)
		}
		second, ok := indexCacheAnalyzerBuildIdentityForModule(module, true, func() (string, bool) {
			return testGoExecutableBuildIDSecond, true
		})
		if !ok || second == first {
			t.Fatalf("compiled analyzer change retained identity %q for %#v", first, module)
		}
	}
}

// TestIndexCacheAnalyzerBuildIdentityForLocalModuleFailsClosed verifies persistence is disabled when compiled local code cannot be identified.
func TestIndexCacheAnalyzerBuildIdentityForLocalModuleFailsClosed(t *testing.T) {
	identity, ok := indexCacheAnalyzerBuildIdentityForModule(debug.Module{
		Path:    indexCacheAnalyzerModulePath,
		Version: "(devel)",
	}, true, func() (string, bool) {
		return "", false
	})
	if ok || identity != "" {
		t.Fatalf("unidentified local analyzer identity = %q, cacheable=%t", identity, ok)
	}
}

// TestValidGoExecutableBuildIDRejectsNonstandardIDs verifies arbitrary linker labels cannot masquerade as content identities.
func TestValidGoExecutableBuildIDRejectsNonstandardIDs(t *testing.T) {
	for _, buildID := range []string{"", "fixed", "one/two/three", "one//three/four", "one/two/three/four/five", "one/two/three/four", "AAAAAAAAAAAAAAAAAAA!/BBBBBBBBBBBBBBBBBBBB/CCCCCCCCCCCCCCCCCCCC/DDDDDDDDDDDDDDDDDDDD"} {
		if validGoExecutableBuildID(buildID) {
			t.Fatalf("custom executable build ID %q was accepted", buildID)
		}
	}
	if !validGoExecutableBuildID(testGoExecutableBuildIDFirst) {
		t.Fatal("ordinary four-part Go executable build ID was rejected")
	}
}

// TestIndexCacheAnalyzerBuildIdentityRequiresModuleMetadata verifies an executable ID cannot stand in for an unknown analyzer selection.
func TestIndexCacheAnalyzerBuildIdentityRequiresModuleMetadata(t *testing.T) {
	identity, ok := indexCacheAnalyzerBuildIdentityForModule(debug.Module{}, false, func() (string, bool) {
		return testGoExecutableBuildIDFirst, true
	})
	if ok || identity != "" {
		t.Fatalf("missing analyzer module identity = %q, cacheable=%t", identity, ok)
	}
}

// TestIndexCacheAnalyzerBuildIdentityForVersionedReplacement verifies checksum-backed replacement modules remain portable.
func TestIndexCacheAnalyzerBuildIdentityForVersionedReplacement(t *testing.T) {
	called := false
	identity, ok := indexCacheAnalyzerBuildIdentityForModule(debug.Module{
		Path:    indexCacheAnalyzerModulePath,
		Version: "v0.7.0",
		Sum:     "h1:original",
		Replace: &debug.Module{Path: "example.com/webfork", Version: "v1.2.0", Sum: "h1:replacement"},
	}, true, func() (string, bool) {
		called = true
		return "", false
	})
	if !ok || identity == "" || called {
		t.Fatalf("versioned replacement identity = %q, cacheable=%t, read_executable=%t", identity, ok, called)
	}
}

// TestReadGoExecutableBuildID verifies the direct reader identifies the executable running the cache analyzer.
func TestReadGoExecutableBuildID(t *testing.T) {
	buildID, ok := readGoExecutableBuildID()
	if !ok || !validGoExecutableBuildID(buildID) {
		t.Fatalf("current executable build ID = %q, available=%t", buildID, ok)
	}
}

// TestReadGoExecutableBuildIDFromFileRoutesFormats verifies each supported executable shape reaches the correct parser without a subprocess.
func TestReadGoExecutableBuildIDFromFileRoutesFormats(t *testing.T) {
	raw := goRawBuildIDFixture(testGoExecutableBuildIDFirst)
	tests := []struct {
		name      string
		data      []byte
		want      string
		available bool
	}{
		{name: "raw marker", data: raw, want: testGoExecutableBuildIDFirst, available: true},
		{
			name:      "Mach-O initial window",
			data:      goMachOExecutableFixture(t, "__text", 0, uint64(len(raw)), raw),
			want:      testGoExecutableBuildIDFirst,
			available: true,
		},
		{
			name:      "Mach-O text section fallback",
			data:      goMachOExecutableFixture(t, "__text", goExecutableBuildIDReadSize+64, uint64(len(raw)), raw),
			want:      testGoExecutableBuildIDFirst,
			available: true,
		},
		{name: "malformed ELF", data: []byte("\x7fELF")},
		{name: "malformed Mach-O", data: binary.LittleEndian.AppendUint32(nil, macho.Magic64)},
		{name: "unrecognized short file", data: []byte("plain executable")},
		{name: "empty file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openExecutableBuildIDFixture(t, test.data)
			buildID, available := readGoExecutableBuildIDFromFile(file)
			if available != test.available || buildID != test.want {
				t.Fatalf("build ID = %q, available=%t; want %q, available=%t", buildID, available, test.want, test.available)
			}
		})
	}

	closed := openExecutableBuildIDFixture(t, raw)
	if err := closed.Close(); err != nil {
		t.Fatalf("close executable fixture: %v", err)
	}
	if buildID, available := readGoExecutableBuildIDFromFile(closed); available || buildID != "" {
		t.Fatalf("closed executable build ID = %q, available=%t", buildID, available)
	}
}

// TestHasGoMachOMagic verifies all thin Mach-O byte orders and word sizes are recognized without accepting truncated or unrelated headers.
func TestHasGoMachOMagic(t *testing.T) {
	magics := [][]byte{
		binary.BigEndian.AppendUint32(nil, macho.Magic32),
		binary.BigEndian.AppendUint32(nil, macho.Magic64),
		binary.LittleEndian.AppendUint32(nil, macho.Magic32),
		binary.LittleEndian.AppendUint32(nil, macho.Magic64),
	}
	for _, magic := range magics {
		if !hasGoMachOMagic(magic) {
			t.Fatalf("Mach-O magic %x was rejected", magic)
		}
	}
	for _, data := range [][]byte{nil, {0xfe, 0xed, 0xfa}, {0, 1, 2, 3}} {
		if hasGoMachOMagic(data) {
			t.Fatalf("non-Mach-O header %x was accepted", data)
		}
	}
}

// TestReadGoMachOBuildID verifies section lookup is bounded and fails closed for absent or truncated text.
func TestReadGoMachOBuildID(t *testing.T) {
	raw := goRawBuildIDFixture(testGoExecutableBuildIDFirst)
	largeText := make([]byte, goExecutableBuildIDReadSize)
	copy(largeText, raw)
	tests := []struct {
		name      string
		data      []byte
		want      string
		available bool
	}{
		{name: "invalid file", data: []byte("not Mach-O")},
		{name: "missing text section", data: goMachOExecutableFixture(t, "__data", 0, uint64(len(raw)), raw)},
		{name: "text marker", data: goMachOExecutableFixture(t, "__text", 0, uint64(len(raw)), raw), want: testGoExecutableBuildIDFirst, available: true},
		{name: "bounded text", data: goMachOExecutableFixture(t, "__text", 0, goExecutableBuildIDReadSize+1, largeText), want: testGoExecutableBuildIDFirst, available: true},
		{name: "truncated text", data: goMachOExecutableFixture(t, "__text", 0, uint64(len(raw)+1), raw)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openExecutableBuildIDFixture(t, test.data)
			buildID, available := readGoMachOBuildID(file)
			if available != test.available || buildID != test.want {
				t.Fatalf("Mach-O build ID = %q, available=%t; want %q, available=%t", buildID, available, test.want, test.available)
			}
		})
	}
}

// TestReadGoRawBuildID verifies quoted build IDs are decoded instead of treated as opaque marker bytes.
func TestReadGoRawBuildID(t *testing.T) {
	data := append([]byte("prefix"), goExecutableBuildIDPrefix...)
	data = append(data, []byte(testGoExecutableBuildIDFirst)...)
	data = append(data, goExecutableBuildIDEnd...)
	data = append(data, []byte("suffix")...)
	buildID, ok := readGoRawBuildID(data)
	if !ok || buildID != testGoExecutableBuildIDFirst {
		t.Fatalf("raw executable build ID = %q, available=%t", buildID, ok)
	}
	if _, ok := readGoRawBuildID(data[:len(data)-len("suffix")-1]); ok {
		t.Fatal("unterminated raw executable build ID was accepted")
	}
}

// TestReadGoELFNotes verifies Go and GNU note identities are distinguished.
func TestReadGoELFNotes(t *testing.T) {
	goValue := []byte(testGoExecutableBuildIDFirst)
	goNote := frameGoELFBuildIDNote(binary.LittleEndian, []byte("Go\x00\x00"), 4, goValue)
	goBuildID, gnuBuildID, ok := readGoELFNotes(bytes.NewReader(goNote), 0, uint64(len(goNote)), 4, binary.LittleEndian)
	if !ok || goBuildID != string(goValue) || gnuBuildID != "" {
		t.Fatalf("Go ELF note = go %q, GNU %q, valid=%t", goBuildID, gnuBuildID, ok)
	}

	gnuValue := []byte{0x01, 0x02, 0x03, 0x04}
	gnuNote := frameGoELFBuildIDNote(binary.BigEndian, []byte("GNU\x00"), 3, gnuValue)
	goBuildID, gnuBuildID, ok = readGoELFNotes(bytes.NewReader(gnuNote), 0, uint64(len(gnuNote)), 4, binary.BigEndian)
	if !ok || goBuildID != "" || !bytes.Equal([]byte(gnuBuildID), gnuValue) {
		t.Fatalf("GNU ELF note = go %q, GNU %x, valid=%t", goBuildID, []byte(gnuBuildID), ok)
	}

	combined := append(frameGoELFBuildIDNote(binary.LittleEndian, []byte("GNU\x00"), 3, gnuValue), goNote...)
	goBuildID, gnuBuildID, ok = readGoELFNotes(bytes.NewReader(combined), 0, uint64(len(combined)), 4, binary.LittleEndian)
	if !ok || goBuildID != string(goValue) || !bytes.Equal([]byte(gnuBuildID), gnuValue) {
		t.Fatalf("combined ELF notes = go %q, GNU %x, valid=%t", goBuildID, []byte(gnuBuildID), ok)
	}
}

// TestReadGoELFNotesRejectsMalformedAndOversizedMetadata verifies corrupt binaries cannot trigger partial identities or large allocations.
func TestReadGoELFNotesRejectsMalformedAndOversizedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		offset uint64
		size   uint64
		align  uint64
	}{
		{name: "truncated header", data: make([]byte, 8), size: 16, align: 4},
		{name: "truncated name", data: framePartialGoELFBuildIDNote(binary.LittleEndian, 4, 0, 4, []byte("Go")), size: 16, align: 4},
		{name: "truncated value", data: framePartialGoELFBuildIDNote(binary.LittleEndian, 4, 4, 4, []byte("Go\x00\x00ab")), size: 20, align: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, ok := readGoELFNotes(bytes.NewReader(test.data), test.offset, test.size, test.align, binary.LittleEndian); ok {
				t.Fatal("malformed ELF build-ID note was accepted")
			}
		})
	}

	oversized := make([]byte, 12)
	binary.LittleEndian.PutUint32(oversized[0:4], goExecutableBuildIDMaximumNoteSize)
	binary.LittleEndian.PutUint32(oversized[4:8], 1)
	if _, _, ok := readGoELFNotes(bytes.NewReader(oversized), 0, goExecutableBuildIDMaximumNoteSize+64, 4, binary.LittleEndian); ok {
		t.Fatal("oversized ELF build-ID note was accepted")
	}
}

// TestReadGoELFNotesValidatesProgramAlignment verifies segment padding is consumed exactly and malformed boundaries fail closed.
func TestReadGoELFNotesValidatesProgramAlignment(t *testing.T) {
	ignored := frameGoELFBuildIDNote(binary.LittleEndian, []byte("TEST"), 1, []byte{1})
	offset := uint64(1)
	alignment := uint64(8)
	padding := ((offset + uint64(len(ignored)) + alignment - 1) &^ (alignment - 1)) - offset - uint64(len(ignored))
	valid := append(append([]byte(nil), ignored...), make([]byte, padding)...)
	if goBuildID, gnuBuildID, ok := readGoELFNotes(bytes.NewReader(valid), offset, uint64(len(valid)), alignment, binary.LittleEndian); !ok || goBuildID != "" || gnuBuildID != "" {
		t.Fatalf("aligned ignored ELF note = go %q, GNU %q, valid=%t", goBuildID, gnuBuildID, ok)
	}
	if _, _, ok := readGoELFNotes(bytes.NewReader(valid[:len(valid)-1]), offset, uint64(len(valid)-1), alignment, binary.LittleEndian); ok {
		t.Fatal("ELF note with insufficient declared padding was accepted")
	}
	if _, _, ok := readGoELFNotes(bytes.NewReader(ignored), offset, uint64(len(valid)), alignment, binary.LittleEndian); ok {
		t.Fatal("ELF note with truncated physical padding was accepted")
	}

	goNote := frameGoELFBuildIDNote(binary.LittleEndian, []byte("Go\x00\x00"), 4, []byte(testGoExecutableBuildIDFirst))
	withoutProgramAlignment := append(append([]byte(nil), ignored...), goNote...)
	goBuildID, _, ok := readGoELFNotes(bytes.NewReader(withoutProgramAlignment), 0, uint64(len(withoutProgramAlignment)), 0, binary.LittleEndian)
	if !ok || goBuildID != testGoExecutableBuildIDFirst {
		t.Fatalf("zero-alignment ELF notes = %q, valid=%t", goBuildID, ok)
	}
}

// frameGoELFBuildIDNote encodes one aligned note fixture in the same representation consumed by PT_NOTE readers.
func frameGoELFBuildIDNote(byteOrder binary.ByteOrder, name []byte, noteType uint32, value []byte) []byte {
	alignedNameLength := (len(name) + 3) &^ 3
	alignedValueLength := (len(value) + 3) &^ 3
	encoded := make([]byte, 12+alignedNameLength+alignedValueLength)
	byteOrder.PutUint32(encoded[0:4], uint32(len(name)))
	byteOrder.PutUint32(encoded[4:8], uint32(len(value)))
	byteOrder.PutUint32(encoded[8:12], noteType)
	copy(encoded[12:12+alignedNameLength], name)
	copy(encoded[12+alignedNameLength:], value)
	return encoded
}

// framePartialGoELFBuildIDNote encodes a declared header followed by intentionally incomplete payload bytes.
func framePartialGoELFBuildIDNote(byteOrder binary.ByteOrder, nameLength uint32, valueLength uint32, noteType uint32, payload []byte) []byte {
	encoded := make([]byte, 12+len(payload))
	byteOrder.PutUint32(encoded[0:4], nameLength)
	byteOrder.PutUint32(encoded[4:8], valueLength)
	byteOrder.PutUint32(encoded[8:12], noteType)
	copy(encoded[12:], payload)
	return encoded
}

// goRawBuildIDFixture wraps an ordinary executable build ID in the linker marker consumed by raw readers.
func goRawBuildIDFixture(buildID string) []byte {
	data := append([]byte(nil), goExecutableBuildIDPrefix...)
	data = append(data, []byte(buildID)...)
	return append(data, goExecutableBuildIDEnd...)
}

// openExecutableBuildIDFixture writes and opens one executable parser fixture for ReaderAt-based format handling.
func openExecutableBuildIDFixture(t *testing.T, data []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "executable")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open executable fixture: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

// goMachOExecutableFixture encodes one minimal 64-bit little-endian executable with an optional section payload.
func goMachOExecutableFixture(t *testing.T, sectionName string, sectionOffset uint32, sectionSize uint64, payload []byte) []byte {
	t.Helper()
	const headerSize = 32
	if sectionName == "" {
		var encoded bytes.Buffer
		if err := binary.Write(&encoded, binary.LittleEndian, macho.FileHeader{Magic: macho.Magic64, Cpu: macho.CpuAmd64, Type: macho.TypeExec}); err != nil {
			t.Fatalf("encode Mach-O header: %v", err)
		}
		if err := binary.Write(&encoded, binary.LittleEndian, uint32(0)); err != nil {
			t.Fatalf("encode Mach-O reserved header field: %v", err)
		}
		return encoded.Bytes()
	}

	commandSize := binary.Size(macho.Segment64{}) + binary.Size(macho.Section64{})
	if sectionOffset == 0 {
		sectionOffset = uint32(headerSize + commandSize)
	}
	fileSize := int(sectionOffset) + len(payload)
	if minimum := headerSize + commandSize; fileSize < minimum {
		fileSize = minimum
	}
	header := macho.FileHeader{
		Magic: macho.Magic64,
		Cpu:   macho.CpuAmd64,
		Type:  macho.TypeExec,
		Ncmd:  1,
		Cmdsz: uint32(commandSize),
	}
	segment := macho.Segment64{
		Cmd:    macho.LoadCmdSegment64,
		Len:    uint32(commandSize),
		Filesz: uint64(fileSize),
		Nsect:  1,
	}
	copy(segment.Name[:], "__TEXT")
	section := macho.Section64{Size: sectionSize, Offset: sectionOffset}
	copy(section.Name[:], sectionName)
	copy(section.Seg[:], "__TEXT")

	var headerAndCommands bytes.Buffer
	for _, value := range []any{header, uint32(0), segment, section} {
		if err := binary.Write(&headerAndCommands, binary.LittleEndian, value); err != nil {
			t.Fatalf("encode Mach-O fixture: %v", err)
		}
	}
	encoded := make([]byte, fileSize)
	copy(encoded, headerAndCommands.Bytes())
	copy(encoded[int(sectionOffset):], payload)
	return encoded
}
