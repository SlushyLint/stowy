package main

import "testing"

func TestPackageTargetForSupportedArchitectures(t *testing.T) {
	tests := []struct {
		goarch       string
		machine      string
		packageArch  string
		repositories []string
	}{
		{"amd64", "", "x86_64", []string{"core", "extra", "multilib"}},
		{"arm64", "", "aarch64", []string{"core", "extra"}},
		{"arm", "armv7l", "armv7h", []string{"core", "extra"}},
		{"arm", "armv8l", "armv7h", []string{"core", "extra"}},
		{"arm", "aarch64", "armv7h", []string{"core", "extra"}},
	}

	for _, test := range tests {
		target, err := packageTargetFor(test.goarch, test.machine)
		if err != nil {
			t.Fatalf("packageTargetFor(%q, %q): %v", test.goarch, test.machine, err)
		}
		if target.packageArch != test.packageArch {
			t.Errorf("packageTargetFor(%q, %q) architecture = %q, want %q", test.goarch, test.machine, target.packageArch, test.packageArch)
		}
		if len(target.repositories) != len(test.repositories) {
			t.Errorf("packageTargetFor(%q, %q) repositories = %v, want %v", test.goarch, test.machine, target.repositories, test.repositories)
			continue
		}
		for i, repo := range test.repositories {
			if target.repositories[i] != repo {
				t.Errorf("packageTargetFor(%q, %q) repositories = %v, want %v", test.goarch, test.machine, target.repositories, test.repositories)
				break
			}
		}
	}
}

func TestPackageTargetForRejectsUnsupportedArchitectures(t *testing.T) {
	for _, test := range []struct {
		goarch  string
		machine string
	}{
		{"arm", "armv6l"},
		{"arm", "arm"},
		{"386", ""},
	} {
		if _, err := packageTargetFor(test.goarch, test.machine); err == nil {
			t.Errorf("packageTargetFor(%q, %q) unexpectedly succeeded", test.goarch, test.machine)
		}
	}
}

func TestPackageTargetBuildsRepositoryURLs(t *testing.T) {
	amd64, _ := packageTargetFor("amd64", "")
	if got, want := amd64.repositoryDatabaseURL("core"), "https://geo.mirror.pkgbuild.com/core/os/x86_64/core.db"; got != want {
		t.Errorf("amd64 repository URL = %q, want %q", got, want)
	}

	arm64, _ := packageTargetFor("arm64", "")
	if got, want := arm64.repositoryDatabaseURL("extra"), "http://mirror.archlinuxarm.org/aarch64/extra/extra.db"; got != want {
		t.Errorf("arm64 repository URL = %q, want %q", got, want)
	}

	arm32, _ := packageTargetFor("arm", "armv7l")
	if got, want := arm32.packageURL("core", "acl-1-armv7h.pkg.tar.xz"), "http://mirror.archlinuxarm.org/armv7h/core/acl-1-armv7h.pkg.tar.xz"; got != want {
		t.Errorf("arm32 package URL = %q, want %q", got, want)
	}
}

func TestPackageTargetAcceptsOnlyMatchingOrAnyArchitecture(t *testing.T) {
	target, _ := packageTargetFor("arm64", "")
	for _, arch := range []string{"aarch64", "any"} {
		if !target.acceptsPackageArchitecture(arch) {
			t.Errorf("aarch64 target rejected package architecture %q", arch)
		}
	}
	for _, arch := range []string{"", "x86_64", "armv7h"} {
		if target.acceptsPackageArchitecture(arch) {
			t.Errorf("aarch64 target accepted package architecture %q", arch)
		}
	}
}
