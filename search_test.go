package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"stowy/meta"
)

func TestPackageSatisfiesDependencies(t *testing.T) {
	pkg := meta.Pkg{
		Name:     "tree-sitter",
		Ver:      "0.26.3-1",
		Provides: []string{"libtree-sitter.so=0.26-64"},
	}

	for _, requirement := range []string{
		"tree-sitter",
		"libtree-sitter.so=0.26-64",
		"tree-sitter>=0.26.3",
	} {
		if !packageSatisfies(pkg, requirement) {
			t.Errorf("%q was not satisfied", requirement)
		}
	}
	for _, requirement := range []string{
		"tree-sitter>=0.27",
		"libtree-sitter.so=0.25-64",
		"libvterm",
	} {
		if packageSatisfies(pkg, requirement) {
			t.Errorf("%q was unexpectedly satisfied", requirement)
		}
	}
}

func TestLSDBReadsSeparateDependencyAndProviderMetadata(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(writer)
	entries := []struct {
		name string
		data string
	}{
		{
			name: "bash-5.3.20-1/desc",
			data: "%FILENAME%\nbash-5.3.20-1-armv7h.pkg.tar.xz\n\n" +
				"%NAME%\nbash\n\n%VERSION%\n5.3.20-1\n\n%ARCH%\narmv7h\n",
		},
		{
			name: "bash-5.3.20-1/depends",
			data: "%DEPENDS%\nreadline\n\n%PROVIDES%\nsh\nlibdemo.so=1.2-64\n",
		},
	}
	for _, entry := range entries {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			Size:     int64(len(entry.data)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "core.db")
	if err := os.WriteFile(dbPath, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"sh", "libdemo.so=1.2-64"} {
		matches, err := lsdb(dbPath, query)
		if err != nil {
			t.Fatalf("lsdb(%q): %v", query, err)
		}
		if len(matches) != 1 {
			t.Fatalf("lsdb(%q) returned %d matches, want 1", query, len(matches))
		}
		pkg := matches[0]
		if pkg.Name != "bash" || pkg.Ver != "5.3.20-1" ||
			pkg.Fname != "bash-5.3.20-1-armv7h.pkg.tar.xz" || pkg.Archbtw != "armv7h" {
			t.Fatalf("lsdb(%q) returned incomplete package metadata: %+v", query, pkg)
		}
		if len(pkg.Depends) != 1 || pkg.Depends[0] != "readline" {
			t.Fatalf("dependencies = %v, want [readline]", pkg.Depends)
		}
	}
}
