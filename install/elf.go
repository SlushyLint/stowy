package extract

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	dtNeeded    = int64(1)
	dtSoname    = int64(14)
	dtRpath     = int64(15)
	dtRunpath   = int64(29)
	dtConfig    = int64(0x6ffffefa)
	dtDepaudit  = int64(0x6ffffefb)
	dtAudit     = int64(0x6ffffefc)
	dtAuxiliary = int64(0x7ffffffd)
	dtFilter    = int64(0x7fffffff)
)

type elfStringReference struct {
	tag    int64
	offset uint64
}

type elfNeededRewrite struct {
	offset      uint64
	original    string
	replacement string
}

func rewriteAbsoluteELFNeededPaths(filename, packageRoot string) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var ident [4]byte
	n, err := file.ReadAt(ident[:], 0)
	if err != nil && err != io.EOF {
		return 0, err
	}
	if n != len(ident) || ident != [4]byte{0x7f, 'E', 'L', 'F'} {
		return 0, nil
	}

	elfFile, err := elf.NewFile(file)
	if err != nil {
		return 0, fmt.Errorf("parse ELF file: %w", err)
	}

	var dynamic *elf.Section
	for _, section := range elfFile.Sections {
		if section.Type != elf.SHT_DYNAMIC {
			continue
		}
		if dynamic != nil {
			return 0, fmt.Errorf("ELF file has multiple dynamic sections")
		}
		dynamic = section
	}
	if dynamic == nil {
		return 0, nil
	}
	if uint64(dynamic.Link) >= uint64(len(elfFile.Sections)) {
		return 0, fmt.Errorf("dynamic section links to invalid string table %d", dynamic.Link)
	}
	dynstr := elfFile.Sections[dynamic.Link]
	if dynstr.Type != elf.SHT_STRTAB {
		return 0, fmt.Errorf("dynamic section links to non-string section %q", dynstr.Name)
	}

	dynamicData, err := dynamic.Data()
	if err != nil {
		return 0, fmt.Errorf("read dynamic section: %w", err)
	}
	stringData, err := dynstr.Data()
	if err != nil {
		return 0, fmt.Errorf("read dynamic string table: %w", err)
	}

	entrySize := dynamic.Entsize
	switch elfFile.Class {
	case elf.ELFCLASS32:
		if entrySize == 0 {
			entrySize = 8
		}
		if entrySize < 8 {
			return 0, fmt.Errorf("invalid 32-bit dynamic entry size %d", entrySize)
		}
	case elf.ELFCLASS64:
		if entrySize == 0 {
			entrySize = 16
		}
		if entrySize < 16 {
			return 0, fmt.Errorf("invalid 64-bit dynamic entry size %d", entrySize)
		}
	default:
		return 0, fmt.Errorf("unsupported ELF class %v", elfFile.Class)
	}
	if uint64(len(dynamicData))%entrySize != 0 {
		return 0, fmt.Errorf("dynamic section size %d is not a multiple of entry size %d", len(dynamicData), entrySize)
	}

	var references []elfStringReference
	var rewrites []elfNeededRewrite
	foundNull := false
	for offset := uint64(0); offset < uint64(len(dynamicData)); offset += entrySize {
		entry := dynamicData[int(offset):int(offset+entrySize)]
		tag, value := readELFDynamicEntry(elfFile, entry)
		if tag == int64(elf.DT_NULL) {
			foundNull = true
			break
		}
		if isELFStringTag(tag) {
			references = append(references, elfStringReference{tag: tag, offset: value})
		}
		if tag != dtNeeded {
			continue
		}

		name, err := readELFString(stringData, value)
		if err != nil {
			return 0, fmt.Errorf("read DT_NEEDED string: %w", err)
		}
		if !strings.HasPrefix(name, "/") {
			continue
		}
		bundledPath := filepath.Join(packageRoot, filepath.FromSlash(strings.TrimPrefix(path.Clean(name), "/")))
		info, err := os.Stat(bundledPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("inspect bundled dependency %q: %w", bundledPath, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		replacement := path.Base(name)
		if replacement == "." || replacement == "/" || replacement == "" {
			return 0, fmt.Errorf("invalid absolute DT_NEEDED path %q", name)
		}
		rewrites = append(rewrites, elfNeededRewrite{
			offset:      value,
			original:    name,
			replacement: replacement,
		})
	}
	if !foundNull {
		return 0, fmt.Errorf("dynamic section has no DT_NULL entry")
	}

	for _, rewrite := range rewrites {
		for _, reference := range references {
			if reference.tag == dtNeeded && reference.offset == rewrite.offset {
				continue
			}
			if reference.offset >= rewrite.offset &&
				reference.offset <= rewrite.offset+uint64(len(rewrite.original)) {
				return 0, fmt.Errorf(
					"DT_NEEDED path %q shares string-table storage with dynamic tag %d",
					rewrite.original,
					reference.tag,
				)
			}
		}
	}

	seen := make(map[uint64]struct{})
	uniqueRewrites := make([]elfNeededRewrite, 0, len(rewrites))
	for _, rewrite := range rewrites {
		if _, ok := seen[rewrite.offset]; ok {
			continue
		}
		seen[rewrite.offset] = struct{}{}
		if len(rewrite.replacement) > len(rewrite.original) {
			return 0, fmt.Errorf("replacement for %q is longer than its original string", rewrite.original)
		}
		if dynstr.Offset > uint64(^uint64(0)>>1)-rewrite.offset {
			return 0, fmt.Errorf("dynamic string file offset overflows")
		}
		uniqueRewrites = append(uniqueRewrites, rewrite)
	}
	if len(uniqueRewrites) == 0 {
		return 0, nil
	}

	var writeFile *os.File
	var originalMode os.FileMode
	restoreMode := false
	writeFile, err = os.OpenFile(filename, os.O_RDWR, 0)
	if os.IsPermission(err) {
		info, statErr := os.Stat(filename)
		if statErr != nil {
			return 0, fmt.Errorf("inspect ELF file permissions: %w", statErr)
		}
		originalMode = info.Mode()
		if chmodErr := os.Chmod(filename, originalMode|0o200); chmodErr != nil {
			return 0, fmt.Errorf("make ELF file writable for relocation: %w", chmodErr)
		}
		restoreMode = true
		writeFile, err = os.OpenFile(filename, os.O_RDWR, 0)
		if err != nil {
			restoreErr := os.Chmod(filename, originalMode)
			if restoreErr != nil {
				return 0, fmt.Errorf("open ELF for relocation: %w (also failed to restore permissions: %v)", err, restoreErr)
			}
			return 0, fmt.Errorf("open ELF for relocation: %w", err)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("open ELF for relocation: %w", err)
	}

	rewritten := 0
	var writeErr error
	for _, rewrite := range uniqueRewrites {
		replacement := make([]byte, len(rewrite.original)+1)
		copy(replacement, rewrite.replacement)
		if _, err := writeFile.WriteAt(replacement, int64(dynstr.Offset+rewrite.offset)); err != nil {
			writeErr = fmt.Errorf("rewrite DT_NEEDED path %q: %w", rewrite.original, err)
			break
		}
		rewritten++
	}
	closeErr := writeFile.Close()
	var restoreErr error
	if restoreMode {
		restoreErr = os.Chmod(filename, originalMode)
	}
	if writeErr != nil {
		if restoreErr != nil {
			return 0, fmt.Errorf("%w (also failed to restore permissions: %v)", writeErr, restoreErr)
		}
		return 0, writeErr
	}
	if closeErr != nil {
		if restoreErr != nil {
			return 0, fmt.Errorf("close ELF after relocation: %w (also failed to restore permissions: %v)", closeErr, restoreErr)
		}
		return 0, fmt.Errorf("close ELF after relocation: %w", closeErr)
	}
	if restoreErr != nil {
		return 0, fmt.Errorf("restore ELF file permissions: %w", restoreErr)
	}
	return rewritten, nil
}

func readELFDynamicEntry(file *elf.File, entry []byte) (int64, uint64) {
	if file.Class == elf.ELFCLASS32 {
		return int64(int32(file.ByteOrder.Uint32(entry[:4]))),
			uint64(file.ByteOrder.Uint32(entry[4:8]))
	}
	return int64(file.ByteOrder.Uint64(entry[:8])),
		file.ByteOrder.Uint64(entry[8:16])
}

func readELFString(table []byte, offset uint64) (string, error) {
	if offset >= uint64(len(table)) {
		return "", fmt.Errorf("string offset %d is outside table of size %d", offset, len(table))
	}
	value := table[int(offset):]
	end := bytes.IndexByte(value, 0)
	if end < 0 {
		return "", fmt.Errorf("string at offset %d is not NUL-terminated", offset)
	}
	return string(value[:end]), nil
}

func isELFStringTag(tag int64) bool {
	switch tag {
	case dtNeeded, dtSoname, dtRpath, dtRunpath,
		dtConfig, dtDepaudit, dtAudit, dtAuxiliary, dtFilter:
		return true
	default:
		return false
	}
}
