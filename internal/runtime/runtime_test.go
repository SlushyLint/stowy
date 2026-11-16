package runtime

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunELFUsesBundledLoaderAndSessionEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	t.Setenv("LD_LIBRARY_PATH", "/host/lib")
	t.Setenv("STOWY_TEST_UNALLOWED", "must-not-pass")
	root := filepath.Join(home, ".local", "stowy", "root")
	binary := filepath.Join(root, "usr", "bin", "demo")
	copyExecutable(t, binary)

	loaderName := dynamicLoaderName()
	if loaderName == "" {
		t.Skip("no bundled loader for this architecture")
	}
	loader := filepath.Join(root, "usr", "lib", loaderName)
	if err := os.MkdirAll(filepath.Dir(loader), 0o755); err != nil {
		t.Fatal(err)
	}
	nestedLibraryDir := filepath.Join(filepath.Dir(loader), "nested")
	if err := os.MkdirAll(nestedLibraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loaderScript := "#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\" \"$3\" \"$4\" \"$WAYLAND_DISPLAY\" \"${LD_LIBRARY_PATH-unset}\" \"${STOWY_TEST_UNALLOWED-unset}\"\n"
	if err := os.WriteFile(loader, []byte(loaderScript), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run("demo", []string{"hello"}, nil, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v; stderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 7 || lines[0] != "--library-path" ||
		!strings.Contains(lines[1], filepath.Join(root, "usr", "lib")) ||
		!strings.Contains(lines[1], nestedLibraryDir) ||
		lines[2] != binary || lines[3] != "hello" ||
		lines[4] != "wayland-test" || lines[5] != "unset" || lines[6] != "unset" {
		t.Fatalf("loader output = %q", stdout.String())
	}
}

func TestRunScriptReceivesArgumentsAndBundledPerlPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LD_LIBRARY_PATH", "/host/lib")
	root := filepath.Join(home, ".local", "stowy", "root")
	binary := filepath.Join(root, "usr", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	perlDir := filepath.Join(root, "usr", "lib", "perl5", "5.44", "core_perl")
	if err := os.MkdirAll(perlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nread input\nprintf '%s\\n' \"$input\" \"$1\" \"$PERL5LIB\" \"${LD_LIBRARY_PATH-unset}\" \"$HOME\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run("demo", []string{"hello"}, strings.NewReader("from-stdin\n"), &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v; stderr: %s", err, stderr.String())
	}
	expected := "from-stdin\nhello\n" + perlDir + "\nunset\n" + home + "\n"
	if stdout.String() != expected {
		t.Fatalf("script output = %q, want %q", stdout.String(), expected)
	}
}

func TestRunReportsMissingBundledLoader(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	loaderName := dynamicLoaderName()
	if loaderName == "" {
		t.Skip("no bundled loader for this architecture")
	}
	binary := filepath.Join(home, ".local", "stowy", "root", "usr", "bin", "demo")
	copyExecutable(t, binary)

	err := Run("demo", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "bundled dynamic loader is missing") {
		t.Fatalf("Run error = %v, want missing loader error", err)
	}
}

func TestRunReportsMissingProgram(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := Run("missing", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `program "missing" is not installed`) {
		t.Fatalf("Run error = %v, want missing program error", err)
	}
}

func copyExecutable(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o755); err != nil {
		t.Fatal(err)
	}
}
