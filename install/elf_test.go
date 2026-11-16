package extract

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestExpkgRewritesAbsoluteELFDependencies(t *testing.T) {
	tests := []struct {
		name           string
		class          elf.Class
		bundledLPEG    bool
		expectedNeeded string
	}{
		{"32-bit bundled dependency", elf.ELFCLASS32, true, "lpeg.so"},
		{"64-bit bundled dependency", elf.ELFCLASS64, true, "lpeg.so"},
		{"unbundled absolute dependency", elf.ELFCLASS64, false, "/usr/lib/lua/5.1/lpeg.so"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
			files := map[string]string{
				"usr/bin/demo": string(elfWithAbsoluteDependency(test.class)),
			}
			if test.bundledLPEG {
				files["usr/lib/lua/5.1/lpeg.so"] = "bundled library"
			}
			writeTestPackage(t, archive, files)

			if err := Expkg("demo", archive); err != nil {
				t.Fatal(err)
			}

			binaryPath := filepath.Join(home, ".local", "stowy", "root", "usr/bin/demo")
			file, err := os.Open(binaryPath)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := elf.NewFile(file)
			if err != nil {
				file.Close()
				t.Fatal(err)
			}
			needed, err := parsed.DynString(elf.DT_NEEDED)
			parsed.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(needed) != 2 || needed[0] != test.expectedNeeded || needed[1] != "libc.so.6" {
				t.Fatalf("DT_NEEDED = %q, want [%s libc.so.6]", needed, test.expectedNeeded)
			}
		})
	}
}

func TestRewriteAbsoluteELFDependenciesPreservesReadOnlyMode(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "usr/bin/demo")
	dependencyPath := filepath.Join(root, "usr/lib/lua/5.1/lpeg.so")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dependencyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, elfWithAbsoluteDependency(elf.ELFCLASS64), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dependencyPath, []byte("bundled library"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := rewriteAbsoluteELFNeededPaths(binaryPath, root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("ELF permissions = %o, want 444", info.Mode().Perm())
	}
}

func TestRewriteAbsoluteELFDependenciesSkipsReadOnlyScripts(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "bashbug")
	if err := os.WriteFile(filename, []byte("#!/bin/sh\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filename, 0o555); err != nil {
		t.Fatal(err)
	}

	rewritten, err := rewriteAbsoluteELFNeededPaths(filename, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rewritten != 0 {
		t.Fatalf("rewrote %d dependencies in a script, want 0", rewritten)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Fatalf("script permissions = %o, want 555", info.Mode().Perm())
	}
}

func elfWithAbsoluteDependency(class elf.Class) []byte {
	const (
		neededTag = uint64(1)
		nullTag   = uint64(0)
	)
	shstrtab := []byte("\x00.shstrtab\x00.dynstr\x00.dynamic\x00")
	dynstr := []byte("\x00/usr/lib/lua/5.1/lpeg.so\x00libc.so.6\x00")
	absoluteOffset := uint64(1)
	libcOffset := uint64(len("/usr/lib/lua/5.1/lpeg.so") + 2)

	var headerSize, sectionHeaderSize, entrySize int
	switch class {
	case elf.ELFCLASS32:
		headerSize, sectionHeaderSize, entrySize = 52, 40, 8
	case elf.ELFCLASS64:
		headerSize, sectionHeaderSize, entrySize = 64, 64, 16
	default:
		panic("unsupported ELF class")
	}

	shstrOffset := headerSize
	dynstrOffset := shstrOffset + len(shstrtab)
	dynamicOffset := align(dynstrOffset+len(dynstr), entrySize)
	dynamicData := make([]byte, entrySize*3)
	putDynamicEntry(dynamicData[0:entrySize], class, neededTag, absoluteOffset)
	putDynamicEntry(dynamicData[entrySize:2*entrySize], class, neededTag, libcOffset)
	putDynamicEntry(dynamicData[2*entrySize:], class, nullTag, 0)
	sectionOffset := align(dynamicOffset+len(dynamicData), 8)

	data := make([]byte, sectionOffset+sectionHeaderSize*4)
	copy(data[shstrOffset:], shstrtab)
	copy(data[dynstrOffset:], dynstr)
	copy(data[dynamicOffset:], dynamicData)
	data[0], data[1], data[2], data[3] = 0x7f, 'E', 'L', 'F'
	data[4], data[5], data[6] = byte(class), byte(elf.ELFDATA2LSB), 1

	order := binary.LittleEndian
	order.PutUint16(data[16:18], uint16(elf.ET_DYN))
	machine := elf.EM_X86_64
	if class == elf.ELFCLASS32 {
		machine = elf.EM_ARM
	}
	order.PutUint16(data[18:20], uint16(machine))
	order.PutUint32(data[20:24], 1)

	if class == elf.ELFCLASS32 {
		order.PutUint32(data[32:36], uint32(sectionOffset))
		order.PutUint16(data[40:42], uint16(headerSize))
		order.PutUint16(data[46:48], uint16(sectionHeaderSize))
		order.PutUint16(data[48:50], 4)
		order.PutUint16(data[50:52], 1)
	} else {
		order.PutUint64(data[40:48], uint64(sectionOffset))
		order.PutUint16(data[52:54], uint16(headerSize))
		order.PutUint16(data[58:60], uint16(sectionHeaderSize))
		order.PutUint16(data[60:62], 4)
		order.PutUint16(data[62:64], 1)
	}

	names := map[string]uint32{
		".shstrtab": 1,
		".dynstr":   11,
		".dynamic":  19,
	}
	writeSectionHeader(data, class, sectionOffset+sectionHeaderSize, names[".shstrtab"],
		uint32(elf.SHT_STRTAB), uint64(shstrOffset), uint64(len(shstrtab)), 0, 1, 0)
	writeSectionHeader(data, class, sectionOffset+sectionHeaderSize*2, names[".dynstr"],
		uint32(elf.SHT_STRTAB), uint64(dynstrOffset), uint64(len(dynstr)), 0, 1, 0)
	writeSectionHeader(data, class, sectionOffset+sectionHeaderSize*3, names[".dynamic"],
		uint32(elf.SHT_DYNAMIC), uint64(dynamicOffset), uint64(len(dynamicData)), 2, uint64(entrySize), uint64(entrySize))
	return data
}

func putDynamicEntry(entry []byte, class elf.Class, tag, value uint64) {
	order := binary.LittleEndian
	if class == elf.ELFCLASS32 {
		order.PutUint32(entry[0:4], uint32(tag))
		order.PutUint32(entry[4:8], uint32(value))
		return
	}
	order.PutUint64(entry[0:8], tag)
	order.PutUint64(entry[8:16], value)
}

func writeSectionHeader(data []byte, class elf.Class, offset int, name, sectionType uint32, sectionOffset, size uint64, link uint32, alignment, entrySize uint64) {
	order := binary.LittleEndian
	if class == elf.ELFCLASS32 {
		header := data[offset : offset+40]
		order.PutUint32(header[0:4], name)
		order.PutUint32(header[4:8], sectionType)
		order.PutUint32(header[16:20], uint32(sectionOffset))
		order.PutUint32(header[20:24], uint32(size))
		order.PutUint32(header[24:28], link)
		order.PutUint32(header[32:36], uint32(alignment))
		order.PutUint32(header[36:40], uint32(entrySize))
		return
	}
	header := data[offset : offset+64]
	order.PutUint32(header[0:4], name)
	order.PutUint32(header[4:8], sectionType)
	order.PutUint64(header[24:32], sectionOffset)
	order.PutUint64(header[32:40], size)
	order.PutUint32(header[40:44], link)
	order.PutUint64(header[48:56], alignment)
	order.PutUint64(header[56:64], entrySize)
}

func align(value, alignment int) int {
	return (value + alignment - 1) & ^(alignment - 1)
}
