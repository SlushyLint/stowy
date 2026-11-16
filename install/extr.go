package extract

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	stowyruntime "stowy/internal/runtime"
	"stowy/internal/statusline"
	"stowy/pkgarchive"

	"golang.org/x/term"
)

// btw i use arch
func Expkg(pkgname, archpath string) error {
	return ExpkgWithMetadata(pkgname, "", nil, archpath)
}

func ExpkgWithMetadata(pkgname, version string, dependencies []string, archpath string) error {
	// find user's ~ dir
	home, e := os.UserHomeDir()
	if e != nil {
		return fmt.Errorf("E: user is a homeless streetbegger.\nE: cannot find ~ dir\nSTDERR: %w\n", e)
	}
	if !validPackageName(pkgname) {
		return fmt.Errorf("E: invalid package name %q", pkgname)
	}

	stowyDir := filepath.Join(home, ".local", "stowy")
	if e := os.MkdirAll(stowyDir, 0o700); e != nil {
		return fmt.Errorf("I/O: could not create Stowy directory %s: %w", stowyDir, e)
	}
	workDir, e := os.MkdirTemp(stowyDir, ".install-*")
	if e != nil {
		return fmt.Errorf("I/O: create install staging directory: %w", e)
	}
	defer os.RemoveAll(workDir)
	pkgdir := filepath.Join(workDir, "root")
	if e := os.MkdirAll(pkgdir, 0o755); e != nil {
		return fmt.Errorf("I/O: could not create staging root %s: %w", pkgdir, e)
	}
	root := filepath.Join(stowyDir, "root")
	//	logStep("extract", "extracting package archive %s", archpath)
	//	logStep("extract", "writing package files to %s", pkgdir)

	if e := os.MkdirAll(pkgdir, 0o755); e != nil {
		return fmt.Errorf("I/O: could not create directory %s of perms 755\nSTDERR: %w\n", pkgdir, e)
	}

	// open pkg archive
	tr, closeArchive, e := pkgarchive.Open(archpath)
	if e != nil {
		return fmt.Errorf("I/O: could not open %s\nSTDERR: %w\n", archpath, e)
	}
	defer closeArchive()

	type pendsymlink struct {
		name   string
		target string
	}

	type pendhardlink struct {
		name   string
		target string
	}

	var symlinks []pendsymlink
	var hardlinks []pendhardlink
	var files []string
	var regularFiles []string
	var dirs []string
	progress := extractionProgress{
		packageName: pkgname,
		// Match the shared renderer, which only animates when stdout is a terminal.
		enabled: term.IsTerminal(int(os.Stdout.Fd())),
	}
	// Show extraction as the footer status; the lower row is its moving activity bar.
	statusline.Status(fmt.Sprintf("Extracting %s", pkgname))
	// Flush the latest extraction state even when extraction exits early with an error.
	defer statusline.FinishProgress()

	// read every package entry
	for {
		hdr, e := tr.Next()

		if e == io.EOF {
			for len(hardlinks) > 0 {
				var pending []pendhardlink
				created := false
				for _, l := range hardlinks {
					src := filepath.Join(pkgdir, l.target)
					info, err := os.Lstat(src)
					if os.IsNotExist(err) {
						pending = append(pending, l)
						continue
					}
					if err != nil {
						return fmt.Errorf("I/O: inspect hard link target %q: %w", src, err)
					}
					if !info.Mode().IsRegular() {
						return fmt.Errorf("I/O: hard link target %q is not a regular file", src)
					}
					dst := filepath.Join(pkgdir, l.name)
					dstInfo, err := os.Lstat(dst)
					if err == nil {
						if os.SameFile(info, dstInfo) {
							continue
						}
						if dstInfo.IsDir() {
							return fmt.Errorf("I/O: hard link destination %q is a directory", dst)
						}
						if err := os.Remove(dst); err != nil {
							return fmt.Errorf("I/O: remove existing hard link destination %q: %w", dst, err)
						}
					} else if !os.IsNotExist(err) {
						return fmt.Errorf("I/O: inspect hard link destination %q: %w", dst, err)
					}
					if err := os.Link(src, dst); err != nil {
						return fmt.Errorf("I/O: create hard link %q to %q: %w", dst, src, err)
					}
					created = true
				}
				if len(pending) > 0 && !created {
					return fmt.Errorf("E: unresolved package hard link %q to %q", pending[0].name, pending[0].target)
				}
				hardlinks = pending
			}
			// we dont need to create an error message
			// this just stops the loop if we reached the end of the archive
			for _, l := range symlinks {
				lp := filepath.Join(pkgdir, l.name)
				info, err := os.Lstat(lp)
				if err == nil {
					if info.Mode()&os.ModeSymlink == 0 {
						return fmt.Errorf("I/O: package symlink %q conflicts with an existing non-symlink", lp)
					}
					existingTarget, err := os.Readlink(lp)
					if err != nil {
						return fmt.Errorf("I/O: inspect existing symlink %q: %w", lp, err)
					}
					if existingTarget != l.target {
						return fmt.Errorf("I/O: package symlink %q conflicts with existing target %q", lp, existingTarget)
					}
					continue
				}
				if !os.IsNotExist(err) {
					return fmt.Errorf("I/O: inspect package symlink %q: %w", lp, err)
				}
				if e := os.Symlink(l.target, lp); e != nil {
					return fmt.Errorf("I/O: create symlink %q: %w", lp, e)
				}
			}
			if err := commitPackage(home, root, workDir, pkgname, version, dependencies, pkgdir, files, dirs, regularFiles); err != nil {
				return fmt.Errorf("I/O: install package %q: %w", pkgname, err)
			}
			progress.finish()
			logStep("done", "package extraction complete")
			return nil
		}
		if e != nil {
			return fmt.Errorf("R: Error reading %s:\nSTDERR: %w", archpath, e)
		}

		// skip pkg meta
		if isPackageMetadataFile(hdr.Name) {
			continue
		}

		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("P: unsafe archive path %q\n", hdr.Name)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." && hdr.Typeflag == tar.TypeDir {
			continue
		}
		// build target path in the pkgdir
		targ := filepath.Join(pkgdir, name)

		// create parent dirs
		if e := os.MkdirAll(filepath.Dir(targ), 0o755); e != nil {
			return fmt.Errorf("I/O: could not create directory %v\nSTDERR: %w\n", filepath.Dir(targ), e)
		}

		switch hdr.Typeflag {

		case tar.TypeSymlink:
			target, err := localSymlinkTarget(name, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("I/O: unsafe symlink %q > %q: %w", name, hdr.Linkname, err)
			}
			symlinks = append(symlinks, pendsymlink{
				name:   name,
				target: target,
			})
			files = append(files, name)
		case tar.TypeLink:
			if !filepath.IsLocal(hdr.Linkname) {
				return fmt.Errorf("I/O: unsafe hard link %q > %q", name, hdr.Linkname)
			}
			target := filepath.Clean(hdr.Linkname)
			if target == "." {
				return fmt.Errorf("I/O: invalid hard link target %q", hdr.Linkname)
			}
			hardlinks = append(hardlinks, pendhardlink{
				name:   name,
				target: target,
			})
			files = append(files, name)
		case tar.TypeDir:
			// create directories as a clone from tar
			if err := os.MkdirAll(targ, 0o755); err != nil {
				return fmt.Errorf("I/O: could not create directory %v: %w", targ, err)
			}
			dirs = append(dirs, name)
		case tar.TypeReg, tar.TypeRegA:
			info, err := os.Lstat(targ)
			if err == nil {
				if info.IsDir() {
					return fmt.Errorf("I/O: package file %q conflicts with an existing directory", targ)
				}
				if err := os.Remove(targ); err != nil {
					return fmt.Errorf("I/O: remove existing package file %q: %w", targ, err)
				}
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("I/O: inspect package file %q: %w", targ, err)
			}
			out, e := os.OpenFile(targ, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if e != nil {
				return fmt.Errorf("I/o: could not create extracted files\nSTDERR: %w", e)
			}
			if _, e := io.Copy(out, tr); e != nil {
				out.Close()

				return fmt.Errorf("I/O: could not copy extracted files\nSTDERR: %w\n", e)

			}

			if e := out.Close(); e != nil {
				return fmt.Errorf("I/O: could not close %v\nSTDERR: %w", out, e)
			}
			files = append(files, name)
			regularFiles = append(regularFiles, name)
		default:
			return fmt.Errorf("E: %q has an unsupported archive entry of type %d\n", hdr.Name, hdr.Typeflag)

		}
		progress.step()

	}

}

func isPackageMetadataFile(name string) bool {
	switch name {
	case ".PKGINFO", ".BUILDINFO", ".MTREE", ".INSTALL":
		return true
	default:
		return false
	}
}

type packageManifest struct {
	Package      string   `json:"package"`
	Version      string   `json:"version,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Files        []string `json:"files"`
	Dirs         []string `json:"dirs"`
}

func validPackageName(pkg string) bool {
	return pkg != "" && pkg != "." && pkg != ".." &&
		filepath.IsLocal(pkg) && filepath.Base(pkg) == pkg
}

func manifestPath(home, pkg string) string {
	return filepath.Join(home, ".local", "stowy", "installed", pkg+".json")
}

func IsInstalledVersion(pkg, version string) (bool, error) {
	if !validPackageName(pkg) {
		return false, fmt.Errorf("invalid package name %q", pkg)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("locate home directory: %w", err)
	}
	manifest, err := readManifest(manifestPath(home, pkg))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read package manifest %q: %w", pkg, err)
	}
	if manifest.Package != pkg {
		return false, fmt.Errorf("package manifest %q contains mismatched package name %q", pkg, manifest.Package)
	}
	return manifest.Version == version, nil
}

func writeManifest(home, pkg, version string, dependencies, files, dirs []string) error {
	sort.Strings(files)
	sort.Strings(dirs)
	sort.Strings(dependencies)
	data, err := json.Marshal(packageManifest{
		Package: pkg, Version: version, Dependencies: dependencies, Files: files, Dirs: dirs,
	})
	if err != nil {
		return err
	}
	path := manifestPath(home, pkg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func expose(home, pkgdir string, files []string) error {
	pkgbin := filepath.Join(pkgdir, "usr", "bin")
	var entries []string
	for _, file := range files {
		if filepath.Dir(file) != filepath.Join("usr", "bin") {
			continue
		}
		entries = append(entries, filepath.Base(file))
	}
	if len(entries) == 0 {
		return nil
	}
	userbin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(userbin, 0o755); err != nil {
		return fmt.Errorf("I/O: could not create cmd dir: %w", err)
	}

	//	logStep("link", "creating command links in %s", userbin)
	for _, name := range entries {
		src := filepath.Join(pkgbin, name)
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("E: failed to find %q: %v\n", src, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		link := filepath.Join(userbin, name)
		launcher := commandLauncher(name)
		existing, e := os.Lstat(link)
		if e == nil {
			if existing.Mode()&os.ModeSymlink != 0 {
				target, readErr := os.Readlink(link)
				if readErr != nil {
					return fmt.Errorf("I/O: could not inspect %s: %w", link, readErr)
				}
				if target != src {
					return fmt.Errorf("E: %s already exists", link)
				}
			} else if existing.Mode().IsRegular() {
				data, readErr := os.ReadFile(link)
				if readErr != nil {
					return fmt.Errorf("I/O: could not inspect %s: %w", link, readErr)
				}
				if string(data) == launcher {
					continue
				}
				if !isManagedCommandLauncher(name, string(data)) {
					return fmt.Errorf("E: %s already exists", link)
				}
			} else {
				return fmt.Errorf("E: %s already exists", link)
			}
		} else if !os.IsNotExist(e) {
			return fmt.Errorf("I/O: could not inspect %s: %w", link, e)
		}
		temp, e := os.CreateTemp(userbin, ".stowy-launcher-*")
		if e != nil {
			return fmt.Errorf("I/O: create launcher for %q: %w", link, e)
		}
		tempName := temp.Name()
		defer os.Remove(tempName)
		if e := temp.Chmod(0o755); e != nil {
			temp.Close()
			return fmt.Errorf("I/O: set launcher permissions for %q: %w", link, e)
		}
		if _, e := temp.WriteString(launcher); e != nil {
			temp.Close()
			return fmt.Errorf("I/O: write launcher for %q: %w", link, e)
		}
		if e := temp.Close(); e != nil {
			return fmt.Errorf("I/O: close launcher for %q: %w", link, e)
		}
		if e := os.Rename(tempName, link); e != nil {
			return fmt.Errorf("E: install launcher %q: %w", link, e)
		}
		logStep("link", "exposed command %s", link)
	}
	//	logStep("bin", "command directory is %s; if zsh cannot find a newly installed command, run: rehash", userbin)
	return nil

}

func commandLauncher(command string) string {
	return stowyruntime.Launcher(command)
}

func commandLauncherVersion(command string, includePerlModules, searchLibrarySubdirectories bool) string {
	launcher := "#!/bin/sh\n"
	if includePerlModules {
		launcher += "# stowy: managed command launcher\n"
	}
	launcher +=
		"STOWY_ROOT=\"${HOME:?}/.local/stowy/root\"\n" +
			"STOWY_BINARY=\"$STOWY_ROOT/usr/bin/\"" + shellQuote(command) + "\n"
	if includePerlModules {
		launcher +=
			"STOWY_PERL5LIB=\n" +
				"for STOWY_PERL_DIR in \"$STOWY_ROOT\"/usr/lib/perl5/*/site_perl \"$STOWY_ROOT\"/usr/share/perl5/site_perl \"$STOWY_ROOT\"/usr/lib/perl5/*/vendor_perl \"$STOWY_ROOT\"/usr/share/perl5/vendor_perl \"$STOWY_ROOT\"/usr/lib/perl5/*/core_perl \"$STOWY_ROOT\"/usr/share/perl5/core_perl; do\n" +
				"  [ -d \"$STOWY_PERL_DIR\" ] || continue\n" +
				"  STOWY_PERL5LIB=\"${STOWY_PERL5LIB:+$STOWY_PERL5LIB:}$STOWY_PERL_DIR\"\n" +
				"done\n" +
				"if [ -n \"$STOWY_PERL5LIB\" ]; then\n" +
				"  export PERL5LIB=\"$STOWY_PERL5LIB${PERL5LIB:+:$PERL5LIB}\"\n" +
				"fi\n"
	}
	if loader := dynamicLoaderName(); loader != "" {
		libraryInvocation := "  exec \"$STOWY_LOADER\" --library-path \"$STOWY_ROOT/usr/lib\" \"$STOWY_BINARY\" \"$@\"\n"
		if searchLibrarySubdirectories {
			libraryInvocation =
				"  STOWY_LIBRARY_PATH=$(find \"$STOWY_ROOT/usr/lib\" -type d -print) || {\n" +
					"    printf '%s\\n' 'stowy: could not locate bundled library directories' >&2\n" +
					"    exit 127\n" +
					"  }\n" +
					"  STOWY_LIBRARY_PATH=$(printf '%s\\n' \"$STOWY_LIBRARY_PATH\" | tr '\\n' ':')\n" +
					"  exec \"$STOWY_LOADER\" --library-path \"$STOWY_LIBRARY_PATH\" \"$STOWY_BINARY\" \"$@\"\n"
		}
		launcher +=
			"if [ \"$(od -An -N4 -tx1 \"$STOWY_BINARY\" 2>/dev/null | tr -d '[:space:]')\" = 7f454c46 ]; then\n" +
				"  STOWY_LOADER=\"$STOWY_ROOT/usr/lib/" + loader + "\"\n" +
				"  if [ ! -x \"$STOWY_LOADER\" ]; then\n" +
				"    printf '%s\\n' 'stowy: bundled dynamic loader is missing' >&2\n" +
				"    exit 127\n" +
				"  fi\n" +
				libraryInvocation +
				"fi\n"
	}
	return launcher + "exec \"$STOWY_BINARY\" \"$@\"\n"
}

func legacyCommandLauncher(command string) string {
	return "#!/bin/sh\n" +
		"STOWY_ROOT=\"${HOME:?}/.local/stowy/root\"\n" +
		"export LD_LIBRARY_PATH=\"$STOWY_ROOT/usr/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}\"\n" +
		"exec \"$STOWY_ROOT/usr/bin/\"" + shellQuote(command) + " \"$@\"\n"
}

func bundledLoaderCommandLauncher(command string) string {
	return commandLauncherVersion(command, false, false)
}

func nestedLibraryCommandLauncher(command string) string {
	return commandLauncherVersion(command, false, true)
}

func isManagedCommandLauncher(command, content string) bool {
	if strings.HasPrefix(content, "#!/bin/sh\n# stowy: managed command launcher\n") {
		return true
	}
	for _, launcher := range []string{
		legacyCommandLauncher(command),
		bundledLoaderCommandLauncher(command),
		nestedLibraryCommandLauncher(command),
	} {
		if content == launcher {
			return true
		}
	}
	return false
}

func dynamicLoaderName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "ld-linux-x86-64.so.2"
	case "arm64":
		return "ld-linux-aarch64.so.1"
	case "arm":
		return "ld-linux-armhf.so.3"
	default:
		return ""
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func logStep(label, format string, args ...interface{}) {
	// Route extraction logs through the shared writer so progress is appended after each message.
	statusline.Logf("\033[1m\033[36m%s\033[0m %s", label, fmt.Sprintf(format, args...))
}

func localSymlinkTarget(name, linkname string) (string, error) {
	var target string

	if filepath.IsAbs(linkname) {
		// Treat an archive's absolute path as rooted at stowy's shared install root.
		target = filepath.Clean(strings.TrimLeft(linkname, string(filepath.Separator)))
	} else {
		target = filepath.Clean(filepath.Join(filepath.Dir(name), linkname))
	}

	if !filepath.IsLocal(target) {
		return "", fmt.Errorf("target escapes install root")
	}

	return filepath.Rel(filepath.Dir(name), target)
}

type extractionProgress struct {
	packageName string
	count       int
	lastUpdate  time.Time
	enabled     bool
}

func (p *extractionProgress) step() {
	p.count++
	if !p.enabled || time.Since(p.lastUpdate) < 100*time.Millisecond {
		return
	}

	// Keep the status row static and animate only the lower row; the archive has no known total.
	statusline.Progress(fmt.Sprintf("%s %d items", p.activityBar(), p.count))
	p.lastUpdate = time.Now()
}

func (p *extractionProgress) finish() {
	statusline.FinishProgress()
	statusline.Status(fmt.Sprintf("Extracted %s: %d items", p.packageName, p.count))
}

func (p *extractionProgress) activityBar() string {
	const barWidth = 20
	const markerWidth = 4
	travel := barWidth - markerWidth
	position := p.count % (2 * travel)
	if position > travel {
		position = 2*travel - position
	}
	return "[" + strings.Repeat(" ", position) +
		"\x1b[36m" + strings.Repeat("~", markerWidth) + "\x1b[0m" +
		strings.Repeat(" ", barWidth-markerWidth-position) + "]"
}
