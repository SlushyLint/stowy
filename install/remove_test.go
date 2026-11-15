package extract

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
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
	if installed, err := IsInstalled("demo"); err != nil || !installed {
		t.Fatalf("IsInstalled(demo) = %t, %v; want true", installed, err)
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	command := filepath.Join(home, ".local", "bin", "demo")
	if data, err := os.ReadFile(command); err != nil || string(data) != commandLauncher("demo") {
		t.Fatalf("command launcher = %q, %v", data, err)
	}

	if err := Remove("demo"); err != nil {
		t.Fatal(err)
	}
	if installed, err := IsInstalled("demo"); err != nil || installed {
		t.Fatalf("IsInstalled(demo) after remove = %t, %v; want false", installed, err)
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

func TestCommandLauncherUsesSandboxRunner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{
		"usr/bin/demo": "executable",
	})
	commandPath := filepath.Join(home, ".local", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	launcher, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "#!/bin/sh\nexec \"${HOME:?}/.local/bin/stowy\" __run 'demo' \"$@\"\n"
	if string(launcher) != expected {
		t.Fatalf("command launcher = %q, want sandbox runner invocation %q", launcher, expected)
	}
}

func TestRemoveDeletesLegacyCommandLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "executable"})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	command := filepath.Join(home, ".local", "bin", "demo")
	if err := os.WriteFile(command, []byte(legacyCommandLauncher("demo")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Remove("demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(command); !os.IsNotExist(err) {
		t.Fatalf("legacy launcher was not removed: %v", err)
	}
}

func TestExpkgUpgradesLegacyCommandLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "executable"})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	command := filepath.Join(home, ".local", "bin", "demo")
	if err := os.WriteFile(command, []byte(legacyCommandLauncher("demo")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(command); err != nil || string(data) != commandLauncher("demo") {
		t.Fatalf("upgraded launcher = %q, %v", data, err)
	}
}

func TestMigrateCommandLaunchers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "executable"})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}

	command := filepath.Join(home, ".local", "bin", "demo")
	if err := os.WriteFile(command, []byte(legacyCommandLauncher("demo")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MigrateCommandLaunchers(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(command); err != nil || string(data) != commandLauncher("demo") {
		t.Fatalf("migrated launcher = %q, %v", data, err)
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
