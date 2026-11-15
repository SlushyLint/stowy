package pkgarchive

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestOpenReadsXZPackageArchive(t *testing.T) {
	var compressed bytes.Buffer
	writer, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tarWriter := tar.NewWriter(writer)
	const contents = "arm package"
	if err := tarWriter.WriteHeader(&tar.Header{
		Name:     "usr/share/example",
		Mode:     0o644,
		Size:     int64(len(contents)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "example.pkg")
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	reader, closeReader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReader()

	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "usr/share/example" {
		t.Fatalf("entry name = %q, want %q", header.Name, "usr/share/example")
	}
	data := make([]byte, len(contents))
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != contents {
		t.Fatalf("entry contents = %q, want %q", data, contents)
	}
}
