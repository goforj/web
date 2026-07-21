package webindex

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	// goExecutableBuildIDReadSize matches the Go toolchain's bounded initial executable read.
	goExecutableBuildIDReadSize = 32 * 1024
	// goExecutableBuildIDMaximumNoteSize rejects unreasonable note allocations while parsing the trusted executable.
	goExecutableBuildIDMaximumNoteSize = 1 << 20
)

var (
	// goExecutableBuildIDPrefix marks raw Go build IDs in non-ELF executables.
	goExecutableBuildIDPrefix = []byte("\xff Go build ID: \"")
	// goExecutableBuildIDEnd terminates raw Go build IDs in non-ELF executables.
	goExecutableBuildIDEnd = []byte("\"\n \xff")
	// indexCacheAnalyzerIdentity captures local executable identity before its path can be replaced by a rebuild.
	indexCacheAnalyzerIdentity, indexCacheAnalyzerIdentityCacheable = initializeIndexCacheAnalyzerBuildIdentity()
)

// indexCacheAnalyzerBuildIdentity returns immutable published module metadata or binds a local analyzer to its compiled executable.
func indexCacheAnalyzerBuildIdentity() (string, bool) {
	return indexCacheAnalyzerIdentity, indexCacheAnalyzerIdentityCacheable
}

// initializeIndexCacheAnalyzerBuildIdentity captures build provenance before ordinary runtime work can replace the executable path.
func initializeIndexCacheAnalyzerBuildIdentity() (string, bool) {
	module, ok := indexCacheAnalyzerModule()
	return indexCacheAnalyzerBuildIdentityForModule(module, ok, readGoExecutableBuildID)
}

// indexCacheAnalyzerModule finds Web in build metadata without guessing when the module has been omitted.
func indexCacheAnalyzerModule() (debug.Module, bool) {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return debug.Module{}, false
	}
	if buildInfo.Main.Path == indexCacheAnalyzerModulePath {
		return buildInfo.Main, true
	}
	for _, dependency := range buildInfo.Deps {
		if dependency != nil && dependency.Path == indexCacheAnalyzerModulePath {
			return *dependency, true
		}
	}
	return debug.Module{}, false
}

// indexCacheAnalyzerBuildIdentityForModule makes local replacements fail closed unless their compiled code can be identified.
func indexCacheAnalyzerBuildIdentityForModule(module debug.Module, found bool, executableBuildID func() (string, bool)) (string, bool) {
	if !found {
		return "", false
	}
	identity := indexCacheBuildModuleIdentity{
		Path:    module.Path,
		Version: module.Version,
		Sum:     module.Sum,
	}
	if module.Replace != nil {
		identity.Replacement = &indexCacheBuildModuleIdentity{
			Path:    module.Replace.Path,
			Version: module.Replace.Version,
			Sum:     module.Replace.Sum,
		}
	}
	if !indexCacheAnalyzerModuleIsPublished(module) {
		buildID, ok := executableBuildID()
		if !ok || !validGoExecutableBuildID(buildID) {
			return "", false
		}
		identity.ExecutableBuildID = buildID
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// validGoExecutableBuildID rejects disabled or nonstandard linker IDs that cannot establish ordinary Go content provenance.
func validGoExecutableBuildID(buildID string) bool {
	parts := strings.Split(buildID, "/")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if len(part) != 20 {
			return false
		}
		for _, character := range part {
			if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
				return false
			}
		}
	}
	return true
}

// indexCacheAnalyzerModuleIsPublished accepts only checksum-backed immutable module selections.
func indexCacheAnalyzerModuleIsPublished(module debug.Module) bool {
	selected := module
	if module.Replace != nil {
		selected = *module.Replace
	}
	return selected.Version != "" && selected.Version != "(devel)" && selected.Sum != ""
}

// readGoExecutableBuildID reads the current Go executable identity without invoking another process.
func readGoExecutableBuildID() (string, bool) {
	path, err := os.Executable()
	if err != nil {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	data := make([]byte, goExecutableBuildIDReadSize)
	read, err := io.ReadFull(file, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", false
	}
	data = data[:read]
	if bytes.HasPrefix(data, []byte("\x7fELF")) {
		return readGoELFBuildID(file)
	}
	if hasGoMachOMagic(data) {
		if buildID, ok := readGoRawBuildID(data); ok {
			return buildID, true
		}
		return readGoMachOBuildID(file)
	}
	return readGoRawBuildID(data)
}

// readGoELFBuildID reads the Go PT_NOTE and falls back to a GNU build ID for gccgo executables.
func readGoELFBuildID(file *os.File) (string, bool) {
	executable, err := elf.NewFile(file)
	if err != nil {
		return "", false
	}
	defer executable.Close()
	var gnuBuildID string
	for _, program := range executable.Progs {
		if program.Type != elf.PT_NOTE || program.Filesz < 16 {
			continue
		}
		goBuildID, candidateGNU, ok := readGoELFProgramBuildID(program, executable.ByteOrder)
		if !ok {
			continue
		}
		if goBuildID != "" {
			return goBuildID, true
		}
		if candidateGNU != "" {
			gnuBuildID = "gnu:" + hex.EncodeToString([]byte(candidateGNU))
		}
	}
	return gnuBuildID, gnuBuildID != ""
}

// readGoELFProgramBuildID parses bounded ELF notes without depending on section headers retained by the linker.
func readGoELFProgramBuildID(program *elf.Prog, byteOrder binary.ByteOrder) (string, string, bool) {
	return readGoELFNotes(program.Open(), program.Off, program.Filesz, program.Align, byteOrder)
}

// readGoELFNotes isolates note framing so malformed and oversized executable metadata can be tested directly.
func readGoELFNotes(reader io.Reader, offset uint64, size uint64, alignment uint64, byteOrder binary.ByteOrder) (string, string, bool) {
	remaining := size
	var gnuBuildID string
	for remaining >= 16 {
		var header [12]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return "", "", false
		}
		nameLength := uint64(byteOrder.Uint32(header[0:4]))
		valueLength := uint64(byteOrder.Uint32(header[4:8]))
		noteType := byteOrder.Uint32(header[8:12])
		alignedNameLength := (nameLength + 3) &^ 3
		alignedValueLength := (valueLength + 3) &^ 3
		noteSize := uint64(len(header)) + alignedNameLength + alignedValueLength
		if noteSize > remaining || noteSize > goExecutableBuildIDMaximumNoteSize {
			return "", "", false
		}
		name := make([]byte, alignedNameLength)
		if _, err := io.ReadFull(reader, name); err != nil {
			return "", "", false
		}
		value := make([]byte, alignedValueLength)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", "", false
		}
		name = name[:nameLength]
		value = value[:valueLength]
		if nameLength == 4 && noteType == 4 && bytes.Equal(name, []byte("Go\x00\x00")) && valueLength != 0 {
			return string(value), gnuBuildID, true
		}
		if nameLength == 4 && noteType == 3 && bytes.Equal(name, []byte("GNU\x00")) && valueLength != 0 {
			gnuBuildID = string(value)
		}

		remaining -= noteSize
		offset += noteSize
		if remaining == 0 || alignment == 0 {
			continue
		}
		alignedOffset := (offset + alignment - 1) &^ (alignment - 1)
		padding := alignedOffset - offset
		if padding > remaining {
			return "", "", false
		}
		if padding != 0 {
			if _, err := io.CopyN(io.Discard, reader, int64(padding)); err != nil {
				return "", "", false
			}
			remaining -= padding
			offset = alignedOffset
		}
	}
	return "", gnuBuildID, true
}

// hasGoMachOMagic recognizes thin Mach-O binaries before the more expensive section lookup.
func hasGoMachOMagic(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	for _, magic := range [][]byte{
		{0xfe, 0xed, 0xfa, 0xce},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xcf, 0xfa, 0xed, 0xfe},
	} {
		if bytes.Equal(data[:4], magic) {
			return true
		}
	}
	return false
}

// readGoMachOBuildID finds the raw Go marker at the start of the Mach-O text section.
func readGoMachOBuildID(file *os.File) (string, bool) {
	executable, err := macho.NewFile(file)
	if err != nil {
		return "", false
	}
	defer executable.Close()
	section := executable.Section("__text")
	if section == nil {
		return "", false
	}
	length := section.Size
	if length > goExecutableBuildIDReadSize {
		length = goExecutableBuildIDReadSize
	}
	data := make([]byte, length)
	if _, err := file.ReadAt(data, int64(section.Offset)); err != nil {
		return "", false
	}
	return readGoRawBuildID(data)
}

// readGoRawBuildID extracts and validates the quoted marker emitted by the Go linker.
func readGoRawBuildID(data []byte) (string, bool) {
	start := bytes.Index(data, goExecutableBuildIDPrefix)
	if start < 0 {
		return "", false
	}
	remainder := data[start+len(goExecutableBuildIDPrefix):]
	end := bytes.Index(remainder, goExecutableBuildIDEnd)
	if end < 0 {
		return "", false
	}
	quoted := data[start+len(goExecutableBuildIDPrefix)-1 : start+len(goExecutableBuildIDPrefix)+end+1]
	buildID, err := strconv.Unquote(string(quoted))
	if err != nil || buildID == "" {
		return "", false
	}
	return buildID, true
}
