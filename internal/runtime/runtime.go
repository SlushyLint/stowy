package runtime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func Launcher(program string) string {
	return "#!/bin/sh\n# stowy: managed command launcher\n" +
		"STOWY_CLI=\"${HOME:?}/.local/bin/stowy\"\n" +
		"if [ ! -x \"$STOWY_CLI\" ]; then\n" +
		"  printf 'stowy: command runner is missing: %s\\n' \"$STOWY_CLI\" >&2\n" +
		"  exit 127\n" +
		"fi\n" +
		"exec \"$STOWY_CLI\" run " + shellQuote(program) + " \"$@\"\n"
}

func Run(program string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if program == "" || filepath.Base(program) != program || program == "." || program == ".." {
		return fmt.Errorf("invalid program name %q", program)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	root := filepath.Join(home, ".local", "stowy", "root")
	binary := filepath.Join(root, "usr", "bin", program)
	info, err := os.Stat(binary)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("program %q is not installed at %s", program, binary)
		}
		return fmt.Errorf("inspect program %q: %w", program, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("program %q is not a regular file", program)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("program %q is not executable", program)
	}

	commandPath := binary
	commandArgs := args
	var env []string
	elfBinary, err := isELF(binary)
	if err != nil {
		return fmt.Errorf("inspect program format %q: %w", binary, err)
	}
	if elfBinary {
		loaderName := dynamicLoaderName()
		if loaderName == "" {
			return fmt.Errorf("ELF program %q is unsupported on architecture %s", program, runtime.GOARCH)
		}
		loader := filepath.Join(root, "usr", "lib", loaderName)
		loaderInfo, err := os.Stat(loader)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("bundled dynamic loader is missing: %s", loader)
			}
			return fmt.Errorf("inspect bundled dynamic loader %q: %w", loader, err)
		}
		if !loaderInfo.Mode().IsRegular() || loaderInfo.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("bundled dynamic loader is not executable: %s", loader)
		}
		libraryDirs, err := findLibraryDirs(filepath.Join(root, "usr", "lib"))
		if err != nil {
			return err
		}
		commandPath = loader
		commandArgs = append([]string{"--library-path", strings.Join(libraryDirs, ":"), binary}, args...)
	}

	env = runtimeEnvironment(root, home)
	command := exec.Command(commandPath, commandArgs...)
	command.Env = env
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("execute %q: %w", program, err)
	}
	return nil
}

func isELF(filename string) (bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var magic [4]byte
	if _, err := file.Read(magic[:]); err != nil {
		return false, err
	}
	return magic == [4]byte{0x7f, 'E', 'L', 'F'}, nil
}

func findLibraryDirs(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("bundled library directory is missing: %s", root)
		}
		return nil, fmt.Errorf("inspect bundled library directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundled library path is not a directory: %s", root)
	}
	dirs := []string{root}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find bundled library directories under %q: %w", root, err)
	}
	return dirs, nil
}

func runtimeEnvironment(root, home string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "HOME" && allowedEnvironmentKey(key) {
			env = append(env, entry)
		}
	}
	env = append(env, "HOME="+home)

	var perlDirs []string
	for _, pattern := range []string{
		filepath.Join(root, "usr", "lib", "perl5", "*", "site_perl"),
		filepath.Join(root, "usr", "share", "perl5", "site_perl"),
		filepath.Join(root, "usr", "lib", "perl5", "*", "vendor_perl"),
		filepath.Join(root, "usr", "share", "perl5", "vendor_perl"),
		filepath.Join(root, "usr", "lib", "perl5", "*", "core_perl"),
		filepath.Join(root, "usr", "share", "perl5", "core_perl"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && info.IsDir() {
				perlDirs = append(perlDirs, match)
			}
		}
	}
	if len(perlDirs) > 0 {
		env = append(env, "PERL5LIB="+strings.Join(perlDirs, ":"))
	}
	return env
}

func allowedEnvironmentKey(key string) bool {
	switch key {
	case "PATH", "LANG", "TERM", "COLORTERM", "DISPLAY", "XAUTHORITY",
		"XDG_RUNTIME_DIR", "XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
		"XDG_CONFIG_DIRS", "XDG_DATA_DIRS", "WAYLAND_DISPLAY", "WAYLAND_SOCKET",
		"DBUS_SESSION_BUS_ADDRESS":
		return true
	}
	return strings.HasPrefix(key, "LC_")
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
