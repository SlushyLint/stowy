package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

func writeDoctorReport(w io.Writer, env map[string]string, devRoot string, target packageTarget, targetErr error) {
	fmt.Fprintf(w, "Host architecture: %s\n", runtime.GOARCH)
	if targetErr != nil {
		fmt.Fprintf(w, "Stowy package support: unavailable (%v)\n", targetErr)
	} else {
		fmt.Fprintf(w, "Stowy package support: available (%s Arch packages)\n", target.packageArch)
	}

	driPath := filepath.Join(devRoot, "dri")
	driDevices := deviceEntries(driPath, func(name string) bool {
		return strings.HasPrefix(name, "card") || strings.HasPrefix(name, "renderD")
	})
	if len(driDevices) == 0 {
		fmt.Fprintf(w, "Graphics devices: none found under %s\n", driPath)
	} else {
		sort.Strings(driDevices)
		fmt.Fprintf(w, "Graphics devices: %s\n", strings.Join(driDevices, ", "))
		checkDeviceAccess(w, driPath, "Graphics device access")
	}

	checkWayland(w, env)
	checkDBus(w, env)
	checkDeviceAccess(w, filepath.Join(devRoot, "input"), "Input device access")
}

func checkWayland(w io.Writer, env map[string]string) {
	runtimeDir, display := env["XDG_RUNTIME_DIR"], env["WAYLAND_DISPLAY"]
	if display == "" {
		if env["WAYLAND_SOCKET"] != "" {
			fmt.Fprintln(w, "Wayland session: WAYLAND_SOCKET is set; socket handoff must be provided by the caller")
			return
		}
		fmt.Fprintln(w, "Wayland session: not advertised")
		return
	}
	if runtimeDir == "" && !filepath.IsAbs(display) {
		fmt.Fprintln(w, "Wayland session: WAYLAND_DISPLAY is set but XDG_RUNTIME_DIR is missing")
		return
	}
	socket := display
	if !filepath.IsAbs(socket) {
		socket = filepath.Join(runtimeDir, socket)
	}
	info, err := os.Stat(socket)
	if err != nil {
		fmt.Fprintf(w, "Wayland session: advertised but socket is unavailable (%s: %v)\n", socket, err)
		return
	}
	if info.Mode()&os.ModeSocket == 0 {
		fmt.Fprintf(w, "Wayland session: %s is not a socket\n", socket)
		return
	}
	if err := unix.Access(socket, unix.W_OK); err != nil {
		fmt.Fprintf(w, "Wayland session: socket exists but is not accessible (%s: %v)\n", socket, err)
		return
	}
	fmt.Fprintf(w, "Wayland session: available at %s\n", socket)
}

func checkDBus(w io.Writer, env map[string]string) {
	address := env["DBUS_SESSION_BUS_ADDRESS"]
	if address == "" {
		fmt.Fprintln(w, "D-Bus session: not advertised")
		return
	}
	if !strings.HasPrefix(address, "unix:") {
		fmt.Fprintln(w, "D-Bus session: advertised with a non-Unix address")
		return
	}
	fields := strings.Split(strings.TrimPrefix(address, "unix:"), ",")
	for _, field := range fields {
		if path, ok := strings.CutPrefix(field, "path="); ok {
			info, err := os.Stat(path)
			if err != nil {
				fmt.Fprintf(w, "D-Bus session: advertised but socket is unavailable (%s: %v)\n", path, err)
				return
			}
			if info.Mode()&os.ModeSocket == 0 {
				fmt.Fprintf(w, "D-Bus session: %s is not a socket\n", path)
				return
			}
			fmt.Fprintf(w, "D-Bus session: available at %s\n", path)
			return
		}
		if strings.HasPrefix(field, "abstract=") {
			fmt.Fprintln(w, "D-Bus session: advertised on an abstract Unix socket")
			return
		}
	}
	fmt.Fprintln(w, "D-Bus session: advertised but no Unix socket path was found")
}

func checkDeviceAccess(w io.Writer, dir, label string) {
	devices := deviceEntries(dir, func(string) bool { return true })
	if len(devices) == 0 {
		fmt.Fprintf(w, "%s: no devices found under %s\n", label, dir)
		return
	}
	sort.Strings(devices)
	var accessible []string
	var restricted []string
	for _, device := range devices {
		path := filepath.Join(dir, device)
		if unix.Access(path, unix.R_OK|unix.W_OK) == nil {
			accessible = append(accessible, device)
		} else {
			restricted = append(restricted, device)
		}
	}
	if len(accessible) > 0 {
		fmt.Fprintf(w, "%s: accessible %s\n", label, strings.Join(accessible, ", "))
	}
	if len(restricted) > 0 {
		fmt.Fprintf(w, "%s: restricted %s\n", label, strings.Join(restricted, ", "))
	}
}

func deviceEntries(dir string, include func(string) bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if include(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	return names
}
