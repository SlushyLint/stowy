package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	extract "stowy/install"
	stowyruntime "stowy/internal/runtime"
	"stowy/internal/statusline"
	"stowy/meta"
)

type Paths struct {
	Bin   string // compiled packages
	Lib   string // user-local libs
	Share string // data
}

const (
	colorReset = "\033[0m"
	colorCyan  = "\033[36m"
	colorGreen = "\033[32m"
	colorWarn  = "\033[33m"
	colorBold  = "\033[1m"
)

func main() {
	os.Exit(run())
}

// The command body returns an exit code; main exits only after it has finished printing.
func run() (exitCode int) {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		return runInstalledProgram(os.Args[2:])
	}

	// Run logs and progress in the shared terminal UI when attached to a terminal.
	if err := statusline.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "E: initialize terminal UI: %v\n", err)
		return 1
	}
	defer func() {
		if err := statusline.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "E: close terminal UI: %v\n", err)
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}()

	// this motherfucker gets to be the first line because it  pissed  me off

	home, err := os.UserHomeDir()
	if err != nil {
		statusline.LogfTo(os.Stderr, "E: locate home directory: %v", err)
		return 1
	}
	paths := Paths{
		Bin:   filepath.Join(home, ".local", "stowy", "usr", "bin"),
		Lib:   filepath.Join(home, ".local", "stowy", "usr", "lib"),
		Share: filepath.Join(home, ".local", "stowy", "usr", "share"),
	}

	for _, p := range []string{paths.Bin, paths.Lib, paths.Share} {
		if err := cp(p); err != nil {
			statusline.LogfTo(os.Stderr, "E: %v", err)
			return 1
		}
	}

	// symlink Stowy* to ~/.local/bin

	exe, err := os.Executable()
	if err != nil {
		panic(err) // im too fucking lazy to make a error message for this
	}
	usrbin := filepath.Join(home, ".local", "bin", "stowy")
	dir := filepath.Dir(exe)
	path := filepath.Join(dir, "stowy")

	_, e := os.Lstat(usrbin)
	if e == nil {
	} else if os.IsNotExist(e) {
		statusline.Logf("Symlink %v > %v does not exist\nCreating symlink...", path, usrbin)
		err := os.Symlink(path, usrbin)
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: failed to create symlink: %v", err)
			statusline.Logf("Continuing anyways...")
		}
	} else {
		statusline.LogfTo(os.Stderr, "E: an error occured when symlinking stowy to ~/.local/bin/\ncontinuing anyways...")
	}

	//		if err := cp(path); err != nil {
	//		fmt.Fprintf(os.Stderr, "E: could not start because Stowy is a bitch about ~/.local/stowy/usr/bin/: %v\n", err)
	//		os.Exit(1)
	//	}
	if len(os.Args) < 2 {
		statusline.LogfTo(os.Stderr, "E: usage: stowy <search|install|local|uninstall|run|doctor> ...")
		return 2
	}
	switch os.Args[1] {
	case "doctor":
		target, targetErr := detectPackageTarget()
		env := make(map[string]string)
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if ok {
				env[key] = value
			}
		}
		writeDoctorReport(os.Stdout, env, "/dev", target, targetErr)
	case "s":
		if len(os.Args) != 4 {
			statusline.LogfTo(os.Stderr, "E: usage (S): stowy S <repo> <term>")
			return 2
		}
		repo := os.Args[2]
		target, err := detectPackageTarget()
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: %v", err)
			return 1
		}
		if !target.supportsRepository(repo) {
			statusline.LogfTo(os.Stderr, "E: repository %q is not available for %s", repo, target.name)
			return 1
		}
		warnInsecureARMSource(target)
		term := os.Args[3]
		dbpath, err := repoDatabase(repo, target)
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: downloading repo db (%s): %v", repo, err)
			return 1
		}

		matches, err := lsdb(dbpath, term)
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: search db (%s): %v", repo, err)
			return 1
		}
		if len(matches) == 0 {
			lw("no package matches for %q in %s", term, repo)
			return 0
		}
		pplist(matches)

	case "l":
		if len(os.Args) != 3 {
			statusline.LogfTo(os.Stderr, "E: usage: stowy l <package archive>")
			return 2
		}
		ls("archive", "reading local package metadata from %s", os.Args[2])
		pkg, err := meta.Pkginfo(os.Args[2])
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: %v", err)
			return 1
		}

		target, err := detectPackageTarget()
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: %v", err)
			return 1
		}
		warnInsecureARMSource(target)
		if err := installPackage(pkg, "", os.Args[2], newInstallState(), target); err != nil {
			statusline.LogfTo(os.Stderr, "E: install %s: %v", pkg.Name, err)
			return 1
		}
		lS("installed %s", pkg.Name)

	case "d", "delete", "remove", "uninstall":
		if len(os.Args) != 3 {
			statusline.LogfTo(os.Stderr, "E: usage: stowy uninstall <package>")
			return 2
		}
		if err := extract.Remove(os.Args[2]); err != nil {
			statusline.LogfTo(os.Stderr, "E: remove error: %v", err)
			return 1
		}
		lS("removed %s", os.Args[2])
	case "i":
		if len(os.Args) != 3 {
			statusline.LogfTo(os.Stderr, "E: usage: stowy i <package>")
			return 2
		}

		term := os.Args[2]
		target, err := detectPackageTarget()
		if err != nil {
			statusline.LogfTo(os.Stderr, "E: %v", err)
			return 1
		}
		warnInsecureARMSource(target)
		repos := target.repositories

		var pkg meta.Pkg
		repo := ""

		for _, candidateRepo := range repos {
			dbpath, err := repoDatabase(candidateRepo, target)
			if err != nil {
				statusline.LogfTo(os.Stderr, "E: download database (%s): %v", candidateRepo, err)
				return 1
			}

			matches, err := lsdb(dbpath, term)
			if err != nil {
				statusline.LogfTo(os.Stderr, "E: search database (%s): %v", candidateRepo, err)
				return 1
			}

			for _, match := range matches {
				if strings.EqualFold(term, match.Name) && target.acceptsPackageArchitecture(match.Archbtw) {
					pkg = match
					repo = candidateRepo
					break
				}
			}

			if repo != "" {
				break
			}
		}

		if repo == "" {
			statusline.LogfTo(os.Stderr, "E: package %q not found in %s", term, strings.Join(repos, ", "))
			return 1
		}

		ls("select", "selected %s %s from %s", pkg.Name, pkg.Ver, repo)

		if err := installPackage(pkg, repo, "", newInstallState(), target); err != nil {
			statusline.LogfTo(os.Stderr, "E: install package (%s): %v", pkg.Name, err)
			return 1
		}

		lS("installed %s", pkg.Name)
	default:
		statusline.LogfTo(os.Stderr, "E: unknown cmd: %q\nusage: stowy <search|install|local|uninstall|run|doctor> ...", os.Args[1])
		return 2
	}
	return 0
}

func runInstalledProgram(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "E: usage: stowy run <program> [args...]")
		return 2
	}
	if err := stowyruntime.Run(args[0], args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "E: %v\n", err)
		return 1
	}
	return 0
}

// These helpers write regular logs only; operation status is set explicitly by download/extract code.
func ls(label, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	statusline.Logf("%s%s%s%s %s", colorBold, colorCyan, label, colorReset, msg)
}

func lS(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	statusline.Logf("%s%s✔%s %s", colorBold, colorGreen, colorReset, msg)
}

func lw(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	statusline.Logf("%s%s!%s %s", colorBold, colorWarn, colorReset, msg)
}

func ppkginf(pkg meta.Pkg) {
	statusline.Logf("\n%sPackage%s %s %s", colorBold, colorReset, pkg.Name, pkg.Ver)
	statusline.Logf("  arch: %s", pkg.Archbtw)
	if len(pkg.Depends) > 0 {
		statusline.Logf("  depends:")
		for _, dep := range pkg.Depends {
			statusline.Logf("    - %s", dep)
		}
	} else {
		statusline.Logf("  depends: none")
	}
	statusline.Logf("")
}

func pplist(matches []meta.Pkg) {
	statusline.Logf("\n%sResults%s", colorBold, colorReset)
	for _, pkg := range matches {
		statusline.Logf("  %s %s - %s", pkg.Name, pkg.Ver, pkg.Desk)
		statusline.Logf("    archive: %s", pkg.Fname)
	}
	statusline.Logf("")
}

func list(dir string) []string {
	// returns the installed packages
	o, e := os.ReadDir(dir)
	if e != nil {
		panic(e)
	}
	var obj []string
	for i := range o {
		obj = append(obj, o[i].Name())
	}

	return obj
}

func depends(dev string, target packageTarget) (meta.Pkg, string, error) {
	for _, repo := range target.repositories {
		dbpath, err := repoDatabase(repo, target)
		if err != nil {
			return meta.Pkg{}, "", err
		}
		matches, err := lsdb(dbpath, dev)
		if err != nil {
			return meta.Pkg{}, "", err
		}
		for _, match := range matches {
			if target.acceptsPackageArchitecture(match.Archbtw) && packageSatisfies(match, dev) {
				return match, repo, nil
			}
		}
	}
	return meta.Pkg{}, "", fmt.Errorf("could not resolve dependency %q", dev)
}

var loadedRepoDatabases = make(map[string]string)

func repoDatabase(repo string, target packageTarget) (string, error) {
	if !target.supportsRepository(repo) {
		return "", fmt.Errorf("repository %q is not available for %s", repo, target.name)
	}
	key := target.packageArch + "/" + repo
	if path, ok := loadedRepoDatabases[key]; ok {
		return path, nil
	}
	path := repodbpath(repo, target.packageArch)
	if repositoryDatabaseIsFresh(path, time.Now()) {
		loadedRepoDatabases[key] = path
		return path, nil
	}
	url := target.repositoryDatabaseURL(repo)
	ls("repo", "downloading %s repository database", repo)
	if err := dlfile(context.Background(), url, path); err != nil {
		return "", err
	}
	loadedRepoDatabases[key] = path
	return path, nil
}

func repositoryDatabaseIsFresh(path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	age := now.Sub(info.ModTime())
	return age >= 0 && age < time.Hour
}

type installState struct {
	installing map[string]bool
	installed  map[string]bool
}

func newInstallState() *installState {
	return &installState{
		installing: make(map[string]bool),
		installed:  make(map[string]bool),
	}
}

func installPackage(pkg meta.Pkg, repo, archivePath string, state *installState, target packageTarget) error {
	if state.installed[pkg.Name] {
		return nil
	}
	if state.installing[pkg.Name] {
		return fmt.Errorf("dependency cycle involving %q", pkg.Name)
	}
	installed, err := extract.IsInstalledVersion(pkg.Name, pkg.Ver)
	if err != nil {
		return fmt.Errorf("check installed package %q: %w", pkg.Name, err)
	}
	if installed {
		state.installing[pkg.Name] = true
		defer delete(state.installing, pkg.Name)
		for _, requirement := range pkg.Depends {
			dependency, dependencyRepo, err := depends(requirement, target)
			if err != nil {
				return fmt.Errorf("resolve dependency %q of %s: %w", requirement, pkg.Name, err)
			}
			if err := installPackage(dependency, dependencyRepo, "", state, target); err != nil {
				return fmt.Errorf("install dependency %q of %s: %w", requirement, pkg.Name, err)
			}
		}
		state.installed[pkg.Name] = true
		return nil
	}
	state.installing[pkg.Name] = true
	defer delete(state.installing, pkg.Name)

	if archivePath == "" {
		if repo == "" {
			return fmt.Errorf("no repository specified for package %q", pkg.Name)
		}
		url := target.packageURL(repo, pkg.Fname)
		archivePath = filepath.Join(os.TempDir(), filepath.Base(pkg.Fname))
		ls("download", "fetching package archive from %s", url)
		if err := dlfile(context.Background(), url, archivePath); err != nil {
			return fmt.Errorf("download %s: %w", pkg.Name, err)
		}
	}

	ls("meta", "parsing %s package metadata", pkg.Name)
	packageInfo, err := meta.Pkginfo(archivePath)
	if err != nil {
		return fmt.Errorf("parse %s package metadata: %w", pkg.Name, err)
	}
	if !target.acceptsPackageArchitecture(packageInfo.Archbtw) {
		return fmt.Errorf("package %q targets architecture %q, but this host requires %q", packageInfo.Name, packageInfo.Archbtw, target.packageArch)
	}
	if pkg.Name != "" && packageInfo.Name != pkg.Name {
		return fmt.Errorf("archive package name %q does not match repository package %q", packageInfo.Name, pkg.Name)
	}

	//	fmt.Printf("Package %s depends on:\n", packageInfo.Name)
	for _, requirement := range packageInfo.Depends {
		//		fmt.Printf("\t%s\n", requirement)
		dependency, dependencyRepo, err := depends(requirement, target)
		if err != nil {
			return fmt.Errorf("resolve dependency %q of %s: %w", requirement, packageInfo.Name, err)
		}
		if err := installPackage(dependency, dependencyRepo, "", state, target); err != nil {
			return fmt.Errorf("install dependency %q of %s: %w", requirement, packageInfo.Name, err)
		}
	}

	ls("install", "installing %s %s", packageInfo.Name, packageInfo.Ver)
	if err := extract.ExpkgWithMetadata(packageInfo.Name, packageInfo.Ver, packageInfo.Depends, archivePath); err != nil {
		return fmt.Errorf("extract package %s: %w", packageInfo.Name, err)
	}
	state.installed[pkg.Name] = true
	return nil
}

func warnInsecureARMSource(target packageTarget) {
	if target.armSource {
		statusline.LogfTo(os.Stderr, "WARNING: ARM package metadata and archives use HTTP and are not signature-verified")
	}
}

func cp(path string) error {
	info, err := os.Stat(path)

	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0o755)
	}

	if err != nil {
		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("%s exists but isn't a directory", path)
	}

	return nil
}
