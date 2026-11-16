package meta

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestParsepkg(t *testing.T) {
	pkg := Parsepkg("pkgname = go-tools\npkgver = 1.22.1-1\narch = x86_64\ndepend = glibc\n")
	if pkg.Name != "go-tools" || pkg.Ver != "1.22.1-1" || pkg.Archbtw != "x86_64" {
		t.Fatalf("parsed package = %+v", pkg)
	}
	if !reflect.DeepEqual(pkg.Depends, []string{"glibc"}) {
		t.Fatalf("dependencies = %v, want glibc", pkg.Depends)
	}
}

func TestParsen(t *testing.T) {
	pkg := Parsen(`%NAME%
stowy-demo

%VERSION%
1.0-1

%FILENAME%
stowy-demo-1.0-1-any.pkg.tar.zst
`)
	want := "stowy-demo-1.0-1-any.pkg.tar.zst"
	if pkg.Fname != want || pkg.Name != "stowy-demo" || pkg.Ver != "1.0-1" {
		t.Fatalf("parsed package = %+v", pkg)
	}
}

func TestParsenReadsRepeatedDependencyAndProviderFields(t *testing.T) {
	pkg := Parsen(`%NAME%
tree-sitter

%VERSION%
0.26.3-1

%ARCH%
aarch64

%DEPENDS%
glibc
libgcc

%PROVIDES%
libtree-sitter.so=0.26-64
tree-sitter-runtime
`)

	if pkg.Name != "tree-sitter" {
		t.Fatalf("package name = %q", pkg.Name)
	}
	if pkg.Archbtw != "aarch64" {
		t.Fatalf("package architecture = %q, want aarch64", pkg.Archbtw)
	}
	if !reflect.DeepEqual(pkg.Depends, []string{"glibc", "libgcc"}) {
		t.Fatalf("dependencies = %v", pkg.Depends)
	}
	if !reflect.DeepEqual(pkg.Provides, []string{"libtree-sitter.so=0.26-64", "tree-sitter-runtime"}) {
		t.Fatalf("providers = %v", pkg.Provides)
	}
}

func TestPkginfoReadsXZPackageArchitecture(t *testing.T) {
	var compressed bytes.Buffer
	writer, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tarWriter := tar.NewWriter(writer)
	const metadata = "pkgname = demo\npkgver = 1.0-1\narch = aarch64\n"
	if err := tarWriter.WriteHeader(&tar.Header{
		Name:     ".PKGINFO",
		Mode:     0o644,
		Size:     int64(len(metadata)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(metadata)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "demo-aarch64.pkg.tar.xz")
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	pkg, err := Pkginfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "demo" || pkg.Archbtw != "aarch64" {
		t.Fatalf("parsed package = %+v, want demo for aarch64", pkg)
	}
}
