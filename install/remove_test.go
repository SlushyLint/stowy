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

func TestCommandLauncherDelegatesToStowyRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/bin/demo": "#!/bin/sh\n"})
	commandPath := filepath.Join(home, ".local", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0o755); err != nil {
		t.Fatal(err)
	}
	stowy := filepath.Join(filepath.Dir(commandPath), "stowy")
	if err := os.WriteFile(stowy, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
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
	expected := "run\ndemo\nhello\n"
	if string(output) != expected {
		t.Fatalf("command output = %q, want %q", output, expected)
	}
}

func TestCommandLauncherDelegatesForELF(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{
		"usr/bin/demo": string(elfWithAbsoluteDependency(elf.ELFCLASS64)),
	})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}
	commandPath := filepath.Join(home, ".local", "bin", "demo")
	data, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `run 'demo' "$@"`) {
		t.Fatalf("launcher does not delegate to stowy run: %s", data)
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
	firstArchive := filepath.Join(t.TempDir(), "tzdata-v1.pkg.tar.zst")
	writeTestPackage(t, firstArchive, map[string]string{"usr/share/zoneinfo/Africa/Accra": "stale data"})
	if err := Expkg("tzdata", firstArchive); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "tzdata-v2.pkg.tar.zst")
	writeHardlinkTestPackage(t, archive, "usr/share/zoneinfo/Africa/Abidjan", "usr/share/zoneinfo/Africa/Accra", "zone data")

	root := filepath.Join(home, ".local", "stowy", "root")
	destination := filepath.Join(root, "usr/share/zoneinfo/Africa/Accra")

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
	firstArchive := filepath.Join(t.TempDir(), "demo-v1.pkg.tar.zst")
	writeTestPackage(t, firstArchive, map[string]string{"usr/bin/demo": "old contents"})
	if err := Expkg("demo", firstArchive); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	path := filepath.Join(root, "usr/bin/demo")
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}

	nextArchive := filepath.Join(t.TempDir(), "demo-v2.pkg.tar.zst")
	writeTestPackage(t, nextArchive, map[string]string{"usr/bin/demo": "new contents"})
	if err := Expkg("demo", nextArchive); err != nil {
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

func TestExpkgTracksVersionAndDependencies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/share/demo.txt": "package data"})

	if err := ExpkgWithMetadata("demo", "1.2-3", []string{"glibc", "libgcc"}, archive); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(manifestPath(home, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.2-3" || strings.Join(manifest.Dependencies, ",") != "glibc,libgcc" {
		t.Fatalf("manifest metadata = version %q, dependencies %v", manifest.Version, manifest.Dependencies)
	}
}

func TestIsInstalledVersionChecksManifestVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	installed, err := IsInstalledVersion("demo", "1.2-3")
	if err != nil || installed {
		t.Fatalf("missing package = %v, %v; want false, nil", installed, err)
	}
	if err := writeManifest(home, "demo", "1.2-3", nil, []string{"usr/share/demo"}, nil); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		version string
		want    bool
	}{
		{version: "1.2-3", want: true},
		{version: "1.2-4", want: false},
	} {
		installed, err := IsInstalledVersion("demo", test.version)
		if err != nil || installed != test.want {
			t.Errorf("IsInstalledVersion(%q) = %v, %v; want %v, nil", test.version, installed, err, test.want)
		}
	}
}

func TestExpkgSkipsArchPackageMetadataFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{
		".BUILDINFO":         "build info",
		".INSTALL":           "install script",
		".MTREE":             "mtree",
		".PKGINFO":           "pkg info",
		"usr/share/demo.txt": "payload",
	})
	if err := Expkg("demo", archive); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".local", "stowy", "root")
	for _, name := range []string{".BUILDINFO", ".INSTALL", ".MTREE", ".PKGINFO"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("package metadata %q was extracted: %v", name, err)
		}
	}
}

func TestExpkgUpgradeRemovesFilesNoLongerInPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	firstArchive := filepath.Join(t.TempDir(), "demo-v1.pkg.tar.zst")
	writeTestPackage(t, firstArchive, map[string]string{
		"usr/share/old.txt":     "old",
		"usr/share/current.txt": "current",
	})
	if err := ExpkgWithMetadata("demo", "1", nil, firstArchive); err != nil {
		t.Fatal(err)
	}

	secondArchive := filepath.Join(t.TempDir(), "demo-v2.pkg.tar.zst")
	writeTestPackage(t, secondArchive, map[string]string{"usr/share/current.txt": "updated"})
	if err := ExpkgWithMetadata("demo", "2", nil, secondArchive); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".local", "stowy", "root")
	if _, err := os.Stat(filepath.Join(root, "usr/share/old.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale package file remains: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "usr/share/current.txt")); err != nil || string(data) != "updated" {
		t.Fatalf("updated file = %q, %v", data, err)
	}
}

func TestExpkgRejectsConflictingPackageFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".local", "stowy", "root")
	firstArchive := filepath.Join(t.TempDir(), "first.pkg.tar.zst")
	writeTestPackage(t, firstArchive, map[string]string{"usr/share/shared.txt": "first package"})
	if err := Expkg("first", firstArchive); err != nil {
		t.Fatal(err)
	}

	secondArchive := filepath.Join(t.TempDir(), "second.pkg.tar.zst")
	writeTestPackage(t, secondArchive, map[string]string{"usr/share/shared.txt": "different contents"})
	if err := Expkg("second", secondArchive); err == nil || !strings.Contains(err.Error(), "conflicts with another installed package") {
		t.Fatalf("Expkg error = %v, want package file conflict", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "usr/share/shared.txt")); err != nil || string(data) != "first package" {
		t.Fatalf("existing package file changed: %q, %v", data, err)
	}
	if _, err := os.Stat(manifestPath(home, "second")); !os.IsNotExist(err) {
		t.Fatalf("failed package received a manifest: %v", err)
	}
}

func TestInstallTransactionRollbackRestoresReplacedFile(t *testing.T) {
	root := t.TempDir()
	workDir := t.TempDir()
	target := filepath.Join(root, "usr/share/demo.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx := &installTransaction{root: root, workDir: workDir, backups: make(map[string]string)}
	if err := tx.backupPath(target, filepath.Join("root", "usr/share/demo.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tx.rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "old" {
		t.Fatalf("restored file = %q, %v", data, err)
	}
}

func TestExpkgRejectsUnownedFileWithoutOverwriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".local", "stowy", "root")
	path := filepath.Join(root, "usr/share/personal.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("personal data"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "demo.pkg.tar.zst")
	writeTestPackage(t, archive, map[string]string{"usr/share/personal.txt": "package data"})

	if err := Expkg("demo", archive); err == nil || !strings.Contains(err.Error(), "not owned by package") {
		t.Fatalf("Expkg error = %v, want unowned file conflict", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "personal data" {
		t.Fatalf("personal file changed: %q, %v", data, err)
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

func TestExpkgLeavesNoPartialInstallWhenCommandExposureFails(t *testing.T) {
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
	if _, err := os.Stat(manifestPath(home, "demo")); !os.IsNotExist(err) {
		t.Fatalf("failed install left a package manifest: %v", err)
	}
	if data, err := os.ReadFile(command); err != nil || string(data) != "existing command" {
		t.Fatalf("existing command changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "stowy", "root", "usr/bin/demo")); !os.IsNotExist(err) {
		t.Fatalf("failed install left a package file: %v", err)
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
