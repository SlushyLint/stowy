package extract

import (
	"archive/tar"
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func TestRemoveDeletesInstalledPackageFilesAndLinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{
		"usr/bin/demo":       "executable",
		"usr/share/demo.txt": "package data",
	})

	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	command := filepath.Join(home, ".local", "bin", "demo")
	if data, err := os.ReadFile(command); err != nil || string(data) != commandLauncher("demo") {
		t.Fatalf("command launcher = %q, %v", data, err)
	}

	if err := Remove("demo"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "usr/bin/demo"),
		filepath.Join(root, "usr/share/demo.txt"),
		command,
		manifestPath(home, "demo"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("expected %q to be removed; lstat error = %v", path, err)
		}
	}
}

func TestCommandLauncherSetsBundledPerlModulePathForScripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LD_LIBRARY_PATH", "/existing")
	t.Setenv("PERL5LIB", "/existing-perl")
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{
		"usr/bin/demo":                               "#!/bin/sh\nprintf '%s\\n' \"$LD_LIBRARY_PATH\" \"$PERL5LIB\" \"$1\"\n",
		"usr/lib/perl5/5.44/core_perl/Cwd.pm":        "package Cwd;",
		"usr/share/perl5/core_perl/File/Basename.pm": "package File::Basename;",
	})
	commandPath := filepath.Join(home, ".local", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".local", "stowy", "root", "usr/bin/demo"), commandPath); err != nil {
		t.Fatal(err)
	}
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(commandPath, "hello")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run command launcher: %v: %s", err, output)
	}
	root := filepath.Join(home, ".local", "stowy", "root")
	expected := "/existing\n" +
		filepath.Join(root, "usr/lib/perl5/5.44/core_perl") + ":" +
		filepath.Join(root, "usr/share/perl5/core_perl") + ":/existing-perl\n" +
		"hello\n"
	if string(output) != expected {
		t.Fatalf("command output = %q, want %q", output, expected)
	}
}

func TestCommandLauncherUsesBundledLoaderForELF(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LD_LIBRARY_PATH", "/existing")
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{
		"usr/bin/demo": string(elfWithAbsoluteDependency(elf.ELFCLASS64)),
	})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	loader := filepath.Join(root, "usr", "lib", dynamicLoaderName())
	nestedLibraryDir := filepath.Join(root, "usr", "lib", "perl5", "5.44", "core_perl", "CORE")
	if err := os.MkdirAll(nestedLibraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(loader), 0o755); err != nil {
		t.Fatal(err)
	}
	loaderScript := "#!/bin/sh\n" +
		"if [ \"$1\" = --verify ]; then exit 0; fi\n" +
		"printf '%s\\n' \"$LD_LIBRARY_PATH\" \"$1\" \"$2\" \"$3\" \"$4\"\n"
	if err := os.WriteFile(loader, []byte(loaderScript), 0o755); err != nil {
		t.Fatal(err)
	}

	commandPath := filepath.Join(home, ".local", "bin", "demo")
	command := exec.Command(commandPath, "hello")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run command launcher: %v: %s", err, output)
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != 5 || lines[0] != "/existing" || lines[1] != "--library-path" ||
		lines[3] != filepath.Join(root, "usr/bin/demo") || lines[4] != "hello" {
		t.Fatalf("command output = %q, unexpected loader invocation", output)
	}
	libraryDirs := strings.Split(strings.TrimSuffix(lines[2], ":"), ":")
	for _, want := range []string{
		filepath.Join(root, "usr/lib"),
		nestedLibraryDir,
	} {
		found := false
		for _, got := range libraryDirs {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("loader library path %q does not include %q", lines[2], want)
		}
	}
	for _, path := range libraryDirs {
		if path == "/existing" {
			t.Errorf("loader library path unexpectedly includes host library path: %q", lines[2])
		}
	}
}

func TestExpkgReplacesLegacyCommandLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "executable"})
	commandPath := filepath.Join(home, ".local", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, oldLauncher := range []string{
		legacyCommandLauncher("demo"),
		bundledLoaderCommandLauncher("demo"),
		nestedLibraryCommandLauncher("demo"),
		"#!/bin/sh\n# stowy: managed command launcher\n# previous launcher version\n",
	} {
		if err := os.WriteFile(commandPath, []byte(oldLauncher), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Expkg("demo", archive); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(commandPath); err != nil || string(data) != commandLauncher("demo") {
			t.Fatalf("command launcher = %q, %v", data, err)
		}
	}
}

func TestRemoveDeletesPreviousCommandLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "executable"})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	commandPath := filepath.Join(home, ".local", "bin", "demo")
	if err := os.WriteFile(commandPath, []byte(nestedLibraryCommandLauncher("demo")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Remove("demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(commandPath); !os.IsNotExist(err) {
		t.Fatalf("previous command launcher was not removed: %v", err)
	}
}

func TestExpkgCanBeRepeatedWithTheSameSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "neovim.pkg.tar.zst")
	writeSymlinkTestPackage(t, archive, "usr/share/nvim/runtime/parser", "../../../lib/tree_sitter")

	for range 2 {
		if err := Expkg("neovim", archive); err != nil {
			t.Fatalf("Expkg failed: %v", err)
		}
	}

	link := filepath.Join(home, ".local", "stowy", "root", "usr/share/nvim/runtime/parser")
	if target, err := os.Readlink(link); err != nil || target != "../../../lib/tree_sitter" {
		t.Fatalf("symlink target = %q, %v", target, err)
	}
}

func TestExpkgExtractsHardlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "tzdata.pkg.tar.zst")
	writeHardlinkTestPackage(t, archive, "usr/share/zoneinfo/Africa/Accra", "usr/share/zoneinfo/Africa/Abidjan", "zone data")

	if err := Expkg("tzdata", archive); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	source, err := os.Stat(filepath.Join(root, "usr/share/zoneinfo/Africa/Accra"))
	if err != nil {
		t.Fatal(err)
	}
	link, err := os.Stat(filepath.Join(root, "usr/share/zoneinfo/Africa/Abidjan"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(source, link) {
		t.Fatal("extracted hard-link entries are not the same file")
	}
}

func TestExpkgReplacesExistingHardlinkDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "tzdata.pkg.tar.zst")
	writeHardlinkTestPackage(t, archive, "usr/share/zoneinfo/Africa/Abidjan", "usr/share/zoneinfo/Africa/Accra", "zone data")

	root := filepath.Join(home, ".local", "stowy", "root")
	destination := filepath.Join(root, "usr/share/zoneinfo/Africa/Accra")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("stale data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Expkg("tzdata", archive); err != nil {
		t.Fatal(err)
	}

	sourceInfo, err := os.Stat(filepath.Join(root, "usr/share/zoneinfo/Africa/Abidjan"))
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, destinationInfo) {
		t.Fatal("existing destination was not replaced by the package hard link")
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "zone data" {
		t.Fatalf("destination contents = %q, %v", data, err)
	}
}

func TestExpkgReplacesReadOnlyExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "new contents"})

	root := filepath.Join(home, ".local", "stowy", "root")
	path := filepath.Join(root, "usr/bin/demo")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old contents"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}

	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new contents" {
		t.Fatalf("file contents = %q, %v", data, err)
	}
}

func TestExpkgExtractsXZPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo-armv7h.pkg.tar.xz")
	writeXZTestPackage(t, archive, map[string]string{"usr/share/demo.txt": "arm package"})

	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".local", "stowy", "root", "usr/share/demo.txt")
	if data, err := os.ReadFile(path); err != nil || string(data) != "arm package" {
		t.Fatalf("extracted contents = %q, %v", data, err)
	}
}

func TestExpkgRejectsHardlinksOutsideInstallRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "unsafe.pkg.tar.zst")
	writeHardlinkTestPackage(t, archive, "usr/share/file", "../outside", "contents")

	if err := Expkg("unsafe", archive); err == nil {
		t.Fatal("Expkg accepted a hard link target outside the install root")
	}
}

func TestRemoveCleansPackageWhenCommandExposureFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "executable"})
	command := filepath.Join(home, ".local", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("existing command"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Expkg("demo", archive); err == nil {
		t.Fatal("Expkg succeeded despite an existing command")
	}
	if err := Remove("demo"); err != nil {
		t.Fatalf("Remove could not clean the partially installed package: %v", err)
	}
	if data, err := os.ReadFile(command); err != nil || string(data) != "existing command" {
		t.Fatalf("existing command changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "stowy", "root", "usr/bin/demo")); !os.IsNotExist(err) {
		t.Fatalf("partially installed package file was not removed: %v", err)
	}
}

func TestRemoveKeepsFilesOwnedByAnotherPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/share/shared.txt": "shared"})

	if err := Expkg("first", archive); err != nil {
		t.Fatal(err)
	}
	if err := Expkg("second", archive); err != nil {
		t.Fatal(err)
	}
	if err := Remove("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "stowy", "root", "usr/share/shared.txt")); err != nil {
		t.Fatalf("shared file was removed: %v", err)
	}
}

func TestRemoveKeepsNonEmptyDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/share/demo.txt": "package data"})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	personalFile := filepath.Join(home, ".local", "stowy", "root", "usr/share/personal.txt")
	if err := os.WriteFile(personalFile, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Remove("demo"); err != nil {
		t.Fatalf("Remove failed with a non-empty package directory: %v", err)
	}
	if data, err := os.ReadFile(personalFile); err != nil || string(data) != "keep me" {
		t.Fatalf("personal file changed: %q, %v", data, err)
	}
	if _, err := os.Stat(manifestPath(home, "demo")); !os.IsNotExist(err) {
		t.Fatalf("package manifest was not removed: %v", err)
	}
}

func TestRemoveRejectsInvalidPackageName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "nested/name"} {
		if err := Remove(name); err == nil {
			t.Errorf("Remove(%q) unexpectedly succeeded", name)
		}
	}
}

func TestRemoveDoesNotFollowReplacedParentSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/share/demo.txt": "package data"})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	share := filepath.Join(root, "usr/share")
	if err := os.Remove(filepath.Join(share, "demo.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(share); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "demo.txt")
	if err := os.WriteFile(outsideFile, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, share); err != nil {
		t.Fatal(err)
	}

	if err := Remove("demo"); err == nil {
		t.Fatal("Remove succeeded through a replaced parent symlink")
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "keep me" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}

func writeXZTestPackage(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var compressed bytes.Buffer
	writer, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(writer)
	for name, contents := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(contents)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSymlinkTestPackage(t *testing.T, path, name, target string) {
	t.Helper()
	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: target,
		Mode:     0o777,
		Typeflag: tar.TypeSymlink,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeHardlinkTestPackage(t *testing.T, path, source, link, contents string) {
	t.Helper()
	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     source,
		Mode:     0o644,
		Size:     int64(len(contents)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     link,
		Linkname: source,
		Typeflag: tar.TypeLink,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestPackage(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for name, contents := range files {
		mode := int64(0o644)
		if name == "usr/bin/demo" {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     mode,
			Size:     int64(len(contents)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
