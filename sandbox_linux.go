package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func runSandbox(args []string) int {
	if len(args) == 0 || !validSandboxCommand(args[0]) {
		fmt.Fprintln(os.Stderr, "E: invalid sandbox command")
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: find home directory: %v\n", err)
		return 1
	}
	home, err = filepath.Abs(home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: resolve home directory: %v\n", err)
		return 1
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: resolve home directory: %v\n", err)
		return 1
	}
	if home == string(filepath.Separator) {
		fmt.Fprintln(os.Stderr, "E: refusing to sandbox with the filesystem root as the home directory")
		return 1
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: find working directory: %v\n", err)
		return 1
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: find Stowy executable: %v\n", err)
		return 1
	}

	uid, gid := os.Getuid(), os.Getgid()
	childArgs := []string{
		"__sandbox-child",
		args[0],
		home,
		workingDirectory,
		strconv.Itoa(uid),
	}
	childArgs = append(childArgs, args[1:]...)
	command := exec.Command(executable, childArgs...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	gateReader, gateWriter, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: create sandbox startup gate: %v\n", err)
		return 1
	}
	defer gateWriter.Close()
	command.ExtraFiles = []*os.File{gateReader}
	command.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: uid, Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: gid, Size: 1},
		},
		GidMappingsEnableSetgroups: false,
		Pdeathsig:                  syscall.SIGKILL,
	}
	if err := command.Start(); err != nil {
		gateReader.Close()
		fmt.Fprintf(os.Stderr, "E: create app sandbox (Linux user and mount namespaces are required): %v\n", err)
		return 1
	}
	if err := gateReader.Close(); err != nil {
		stopSandboxCommand(command)
		fmt.Fprintf(os.Stderr, "E: close sandbox startup gate: %v\n", err)
		return 1
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		stopSandboxCommand(command)
		fmt.Fprintf(os.Stderr, "E: protect sandbox manager from app inspection: %v\n", err)
		return 1
	}
	if _, err := gateWriter.Write([]byte{1}); err != nil {
		stopSandboxCommand(command)
		fmt.Fprintf(os.Stderr, "E: release sandbox startup gate: %v\n", err)
		return 1
	}
	if err := gateWriter.Close(); err != nil {
		stopSandboxCommand(command)
		fmt.Fprintf(os.Stderr, "E: close sandbox startup gate: %v\n", err)
		return 1
	}
	if err := command.Wait(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			if code := exitError.ExitCode(); code >= 0 {
				return code
			}
			if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
				return 128 + int(waitStatus.Signal())
			}
			return 1
		}
		fmt.Fprintf(os.Stderr, "E: create app sandbox (Linux user and mount namespaces are required): %v\n", err)
		return 1
	}
	return 0
}

func stopSandboxCommand(command *exec.Cmd) {
	if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		fmt.Fprintf(os.Stderr, "E: stop sandbox process: %v\n", err)
	}
	if err := command.Wait(); err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			fmt.Fprintf(os.Stderr, "E: wait for stopped sandbox process: %v\n", err)
		}
	}
}

func runSandboxChild(args []string) int {
	if len(args) < 4 || !validSandboxCommand(args[0]) {
		fmt.Fprintln(os.Stderr, "E: invalid internal sandbox invocation")
		return 2
	}
	app, home, workingDirectory := args[0], args[1], args[2]
	hostUID, err := strconv.Atoi(args[3])
	if err != nil || hostUID < 0 {
		fmt.Fprintln(os.Stderr, "E: invalid host user ID for sandbox")
		return 2
	}
	appArgs := args[4:]
	if err := waitForSandboxGate(); err != nil {
		fmt.Fprintf(os.Stderr, "E: await sandbox startup: %v\n", err)
		return 1
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "E: enable app process metadata: %v\n", err)
		return 1
	}

	root := filepath.Join(home, ".local", "stowy", "root")
	info, err := os.Lstat(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: inspect sandbox runtime %q: %v\n", root, err)
		return 1
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(os.Stderr, "E: sandbox runtime %q is not a real directory\n", root)
		return 1
	}

	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		fmt.Fprintf(os.Stderr, "E: isolate sandbox mounts: %v\n", err)
		return 1
	}
	if err := prepareSandboxMounts(root, home, hostUID); err != nil {
		fmt.Fprintf(os.Stderr, "E: prepare app sandbox: %v\n", err)
		return 1
	}

	appPath := filepath.Join(root, "usr", "bin", app)
	info, err = os.Stat(appPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "E: inspect app command %q: %v\n", app, err)
		return 1
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		fmt.Fprintf(os.Stderr, "E: app command %q is not an executable regular file\n", app)
		return 1
	}

	sandboxCWD := sandboxWorkingDirectory(home, workingDirectory)
	if err := unix.Chroot(root); err != nil {
		fmt.Fprintf(os.Stderr, "E: enter app runtime: %v\n", err)
		return 1
	}
	if err := os.Chdir(sandboxCWD); err != nil {
		fmt.Fprintf(os.Stderr, "E: set app working directory %q: %v\n", sandboxCWD, err)
		return 1
	}
	if err := dropSandboxCapabilities(); err != nil {
		fmt.Fprintf(os.Stderr, "E: drop sandbox capabilities: %v\n", err)
		return 1
	}

	appEnvironment := sandboxEnvironment(home, sandboxCWD, hostUID)
	argv := append([]string{"/usr/bin/" + app}, appArgs...)
	if err := unix.Exec(argv[0], argv, appEnvironment); err != nil {
		fmt.Fprintf(os.Stderr, "E: execute app %q inside sandbox: %v\n", app, err)
		return 1
	}
	return 0
}

func waitForSandboxGate() error {
	gate := os.NewFile(3, "sandbox-startup-gate")
	if gate == nil {
		return fmt.Errorf("startup gate is unavailable")
	}
	defer gate.Close()
	var signal [1]byte
	if _, err := gate.Read(signal[:]); err != nil {
		return err
	}
	if signal[0] != 1 {
		return fmt.Errorf("invalid startup signal")
	}
	return nil
}

func prepareSandboxMounts(root, home string, hostUID int) error {
	homeRelative := strings.TrimPrefix(home, string(filepath.Separator))
	homeTarget := filepath.Join(root, homeRelative)
	for _, directory := range []struct {
		path string
		mode os.FileMode
	}{
		{"dev", 0o755},
		{"etc", 0o755},
		{"proc", 0o555},
		{"proc/self", 0o555},
		{"run", 0o755},
		{"tmp", 0o1777},
		{"usr", 0o755},
		{"usr/bin", 0o755},
		{"home", 0o755},
		{homeRelative, 0o755},
	} {
		if err := ensureSandboxDirectory(root, directory.path, directory.mode); err != nil {
			return err
		}
	}
	if err := os.Chmod(filepath.Join(root, "tmp"), 0o1777); err != nil {
		return fmt.Errorf("set sandbox /tmp permissions: %w", err)
	}

	if err := bindMount("/dev", filepath.Join(root, "dev"), true); err != nil {
		return fmt.Errorf("expose host devices: %w", err)
	}
	if err := bindMount(home, homeTarget, true); err != nil {
		return fmt.Errorf("expose user home: %w", err)
	}
	processInformation := filepath.Join("/proc", strconv.Itoa(os.Getpid()))
	if err := bindMount(processInformation, filepath.Join(root, "proc", "self"), false); err != nil {
		return fmt.Errorf("expose app process information: %w", err)
	}
	for _, path := range []string{
		"/proc/cpuinfo",
		"/proc/loadavg",
		"/proc/meminfo",
		"/proc/stat",
		"/proc/uptime",
		"/proc/version",
	} {
		if err := bindHostFile(root, path, false); err != nil {
			return err
		}
	}
	threadSelf := filepath.Join(root, "proc", "thread-self")
	if _, err := os.Lstat(threadSelf); os.IsNotExist(err) {
		if err := os.Symlink("self", threadSelf); err != nil {
			return fmt.Errorf("create sandbox /proc/thread-self: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect sandbox /proc/thread-self: %w", err)
	} else if target, err := os.Readlink(threadSelf); err != nil || target != "self" {
		if err != nil {
			return fmt.Errorf("inspect sandbox /proc/thread-self: %w", err)
		}
		return fmt.Errorf("sandbox /proc/thread-self points to %q, want %q", target, "self")
	}

	runtimeDirectory := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDirectory == "" {
		runtimeDirectory = filepath.Join("/run/user", strconv.Itoa(hostUID))
		if _, err := os.Stat(runtimeDirectory); os.IsNotExist(err) {
			runtimeDirectory = ""
		} else if err != nil {
			return fmt.Errorf("inspect desktop session directory: %w", err)
		}
	}
	if runtimeDirectory != "" {
		if !filepath.IsAbs(runtimeDirectory) || filepath.Clean(runtimeDirectory) != runtimeDirectory {
			return fmt.Errorf("invalid XDG_RUNTIME_DIR %q", runtimeDirectory)
		}
		resolvedRuntimeDirectory, err := filepath.EvalSymlinks(runtimeDirectory)
		if err != nil {
			return fmt.Errorf("resolve desktop session directory %q: %w", runtimeDirectory, err)
		}
		info, err := os.Stat(resolvedRuntimeDirectory)
		if err != nil {
			return fmt.Errorf("inspect desktop session directory %q: %w", runtimeDirectory, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("desktop session path %q is not a directory", runtimeDirectory)
		}
		if !pathWithin(home, runtimeDirectory) {
			runtimeRelative := strings.TrimPrefix(runtimeDirectory, string(filepath.Separator))
			target := filepath.Join(root, runtimeRelative)
			if err := ensureSandboxDirectory(root, runtimeRelative, 0o755); err != nil {
				return err
			}
			if err := bindMount(resolvedRuntimeDirectory, target, true); err != nil {
				return fmt.Errorf("expose desktop session directory: %w", err)
			}
		}
	}

	if err := bindHostFile(root, "/etc/hosts", true); err != nil {
		return err
	}
	if err := bindHostFile(root, "/etc/resolv.conf", true); err != nil {
		return err
	}

	x11Socket := "/tmp/.X11-unix"
	if info, err := os.Stat(x11Socket); err == nil && info.IsDir() {
		target := filepath.Join(root, "tmp", ".X11-unix")
		if err := ensureSandboxDirectory(root, filepath.Join("tmp", ".X11-unix"), 0o1777); err != nil {
			return err
		}
		if err := bindMount(x11Socket, target, true); err != nil {
			return fmt.Errorf("expose X11 socket directory: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect X11 socket directory: %w", err)
	}

	return nil
}

func ensureSandboxDirectory(root, relative string, mode os.FileMode) error {
	if relative == "" || filepath.IsAbs(relative) || !filepath.IsLocal(relative) || filepath.Clean(relative) != relative {
		return fmt.Errorf("unsafe sandbox directory %q", relative)
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, mode); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create sandbox directory %q: %w", current, err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect sandbox directory %q: %w", current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sandbox mount path %q is not a real directory", current)
		}
	}
	return nil
}

func bindMount(source, target string, recursive bool) error {
	flags := uintptr(unix.MS_BIND)
	if recursive {
		flags |= unix.MS_REC
	}
	if err := unix.Mount(source, target, "", flags, ""); err != nil {
		return err
	}
	return nil
}

func bindMountReadOnly(source, target string, recursive bool) error {
	if err := bindMount(source, target, recursive); err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	if err := unix.Mount("", target, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("remount read-only: %w", err)
	}
	return nil
}

func bindHostFile(root, source string, readOnly bool) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect host configuration %q: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("host configuration %q is not a regular file", source)
	}

	relative := strings.TrimPrefix(source, string(filepath.Separator))
	target := filepath.Join(root, relative)
	parent := filepath.Dir(relative)
	if err := ensureSandboxDirectory(root, parent, 0o755); err != nil {
		return err
	}
	if targetInfo, err := os.Lstat(target); os.IsNotExist(err) {
		file, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if createErr != nil {
			return fmt.Errorf("create sandbox configuration %q: %w", target, createErr)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close sandbox configuration %q: %w", target, err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect sandbox configuration %q: %w", target, err)
	} else if !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("sandbox configuration path %q is not a regular file", target)
	}

	var mountErr error
	if readOnly {
		mountErr = bindMountReadOnly(source, target, false)
	} else {
		mountErr = bindMount(source, target, false)
	}
	if mountErr != nil {
		return fmt.Errorf("expose host configuration %q: %w", source, mountErr)
	}
	return nil
}

func sandboxWorkingDirectory(home, workingDirectory string) string {
	relative, err := filepath.Rel(home, workingDirectory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "/" + filepath.ToSlash(strings.TrimPrefix(home, string(filepath.Separator)))
	}
	homePath := strings.TrimPrefix(home, string(filepath.Separator))
	if relative == "." {
		return "/" + filepath.ToSlash(homePath)
	}
	return "/" + filepath.ToSlash(filepath.Join(homePath, relative))
}

func sandboxEnvironment(home, workingDirectory string, hostUID int) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "HOME", "PWD", "TMPDIR", "XDG_RUNTIME_DIR", "STOWY_ROOT":
			continue
		}
		if strings.HasPrefix(key, "LD_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"HOME="+home,
		"PWD="+workingDirectory,
		"TMPDIR=/tmp",
	)
	if runtimeDirectory := os.Getenv("XDG_RUNTIME_DIR"); runtimeDirectory != "" {
		env = append(env, "XDG_RUNTIME_DIR="+runtimeDirectory)
	} else {
		defaultRuntimeDirectory := filepath.Join("/run/user", strconv.Itoa(hostUID))
		if info, err := os.Stat(defaultRuntimeDirectory); err == nil && info.IsDir() {
			env = append(env, "XDG_RUNTIME_DIR="+defaultRuntimeDirectory)
		}
	}
	return env
}

func pathWithin(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validSandboxCommand(command string) bool {
	return command != "" && filepath.IsLocal(command) &&
		filepath.Base(command) == command && command != "." && command != ".."
}

type capabilityHeader struct {
	version uint32
	pid     int32
}

type capabilityData struct {
	effective   uint32
	permitted   uint32
	inheritable uint32
}

func dropSandboxCapabilities() error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("disable app process inspection: %w", err)
	}

	header := capabilityHeader{version: 0x20080522}
	var data [2]capabilityData
	_, _, errno := unix.Syscall(unix.SYS_CAPSET,
		uintptr(unsafe.Pointer(&header)),
		uintptr(unsafe.Pointer(&data[0])),
		0)
	if errno != 0 {
		return fmt.Errorf("clear effective, permitted, and inheritable capabilities: %w", errno)
	}
	return nil
}
