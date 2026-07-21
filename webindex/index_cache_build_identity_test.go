package webindex

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"runtime/debug"
	"testing"
)

const (
	// testGoExecutableBuildIDFirst represents one ordinary content-derived Go executable identity.
	testGoExecutableBuildIDFirst = "AAAAAAAAAAAAAAAAAAAA/BBBBBBBBBBBBBBBBBBBB/CCCCCCCCCCCCCCCCCCCC/DDDDDDDDDDDDDDDDDDDD"
	// testGoExecutableBuildIDSecond changes one action ID while retaining the standard linker shape.
	testGoExecutableBuildIDSecond = "EEEEEEEEEEEEEEEEEEEE/BBBBBBBBBBBBBBBBBBBB/CCCCCCCCCCCCCCCCCCCC/DDDDDDDDDDDDDDDDDDDD"
)

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
}

// TestReadGoELFNotesRejectsMalformedAndOversizedMetadata verifies corrupt binaries cannot trigger partial identities or large allocations.
func TestReadGoELFNotesRejectsMalformedAndOversizedMetadata(t *testing.T) {
	malformed := make([]byte, 12)
	binary.LittleEndian.PutUint32(malformed[0:4], 4)
	binary.LittleEndian.PutUint32(malformed[4:8], 32)
	if _, _, ok := readGoELFNotes(bytes.NewReader(malformed), 0, 64, 4, binary.LittleEndian); ok {
		t.Fatal("truncated ELF build-ID note was accepted")
	}

	oversized := make([]byte, 12)
	binary.LittleEndian.PutUint32(oversized[0:4], goExecutableBuildIDMaximumNoteSize)
	binary.LittleEndian.PutUint32(oversized[4:8], 1)
	if _, _, ok := readGoELFNotes(bytes.NewReader(oversized), 0, goExecutableBuildIDMaximumNoteSize+64, 4, binary.LittleEndian); ok {
		t.Fatal("oversized ELF build-ID note was accepted")
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
