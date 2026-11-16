package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorDistinguishesPackageSupportFromSessionFacilities(t *testing.T) {
	target, err := packageTargetFor("amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	writeDoctorReport(&report, map[string]string{}, t.TempDir(), target, nil)
	output := report.String()
	for _, expected := range []string{
		"Stowy package support: available (x86_64 Arch packages)",
		"Graphics devices: none found",
		"Wayland session: not advertised",
		"D-Bus session: not advertised",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("doctor output %q does not contain %q", output, expected)
		}
	}
}

func TestDoctorReportsSessionSocketsAndDeviceAccess(t *testing.T) {
	devRoot := t.TempDir()
	for _, dir := range []string{"dri", "input"} {
		if err := os.MkdirAll(filepath.Join(devRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(devRoot, "dri", "card0"),
		filepath.Join(devRoot, "dri", "renderD128"),
		filepath.Join(devRoot, "input", "event0"),
	} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	runtimeDir := t.TempDir()
	waylandPath := filepath.Join(runtimeDir, "wayland-1")
	wayland, err := net.Listen("unix", waylandPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wayland.Close()
	dbusPath := filepath.Join(runtimeDir, "bus")
	dbus, err := net.Listen("unix", dbusPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dbus.Close()

	target, err := packageTargetFor("amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	writeDoctorReport(&report, map[string]string{
		"XDG_RUNTIME_DIR":          runtimeDir,
		"WAYLAND_DISPLAY":          "wayland-1",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=" + dbusPath,
	}, devRoot, target, nil)
	output := report.String()
	for _, expected := range []string{
		"Graphics devices: card0, renderD128",
		"Graphics device access: accessible card0, renderD128",
		"Wayland session: available at " + waylandPath,
		"D-Bus session: available at " + dbusPath,
		"Input device access: accessible event0",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("doctor output %q does not contain %q", output, expected)
		}
	}
}

func TestDoctorReportsUnsupportedArchitectureSeparately(t *testing.T) {
	var report bytes.Buffer
	writeDoctorReport(&report, nil, t.TempDir(), packageTarget{}, errors.New("unsupported host architecture"))
	if !strings.Contains(report.String(), "Stowy package support: unavailable (unsupported host architecture)") {
		t.Fatalf("doctor output = %q", report.String())
	}
}
