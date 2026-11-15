package pkgarchive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"fmt"
	"os"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func Open(path string) (*tar.Reader, func(), error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	buffered := bufio.NewReader(file)
	header, err := buffered.Peek(6)
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("read package archive header %q: %w", path, err)
	}

	switch {
	case bytes.Equal(header, []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}):
		decoder, err := xz.NewReader(buffered)
		if err != nil {
			file.Close()
			return nil, nil, err
		}
		return tar.NewReader(decoder), func() {
			file.Close()
		}, nil
	case bytes.Equal(header[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}):
		decoder, err := zstd.NewReader(buffered)
		if err != nil {
			file.Close()
			return nil, nil, err
		}
		return tar.NewReader(decoder), func() {
			decoder.Close()
			file.Close()
		}, nil
	default:
		file.Close()
		return nil, nil, fmt.Errorf("unsupported package archive compression for %q", path)
	}
}
