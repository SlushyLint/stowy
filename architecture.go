package main

import (
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

type packageTarget struct {
	name         string
	packageArch  string
	repositories []string
	armSource    bool
}

func detectPackageTarget() (packageTarget, error) {
	machine := ""
	if runtime.GOARCH == "arm" {
		var uts unix.Utsname
		if err := unix.Uname(&uts); err != nil {
			return packageTarget{}, fmt.Errorf("detect ARM machine variant: %w", err)
		}
		machine = strings.TrimRight(string(uts.Machine[:]), "\x00")
	}

	return packageTargetFor(runtime.GOARCH, machine)
}

func packageTargetFor(goarch, machine string) (packageTarget, error) {
	switch goarch {
	case "amd64":
		return packageTarget{
			name:         "amd64",
			packageArch:  "x86_64",
			repositories: []string{"core", "extra", "multilib"},
		}, nil
	case "arm64":
		return packageTarget{
			name:         "arm64",
			packageArch:  "aarch64",
			repositories: []string{"core", "extra"},
			armSource:    true,
		}, nil
	case "arm":
		if strings.HasPrefix(machine, "armv7") ||
			strings.HasPrefix(machine, "armv8") ||
			machine == "aarch64" {
			return packageTarget{
				name:         "armv7h",
				packageArch:  "armv7h",
				repositories: []string{"core", "extra"},
				armSource:    true,
			}, nil
		}
		return packageTarget{}, fmt.Errorf("unsupported 32-bit ARM machine %q; Stowy supports ARMv7 hard-float (armv7h)", machine)
	default:
		return packageTarget{}, fmt.Errorf("unsupported host architecture %q; supported architectures are amd64, arm64, and armv7h", goarch)
	}
}

func (target packageTarget) supportsRepository(repo string) bool {
	for _, supported := range target.repositories {
		if repo == supported {
			return true
		}
	}
	return false
}

func (target packageTarget) repositoryDatabaseURL(repo string) string {
	if target.armSource {
		return fmt.Sprintf("http://mirror.archlinuxarm.org/%s/%s/%s.db", target.packageArch, repo, repo)
	}
	return fmt.Sprintf("https://geo.mirror.pkgbuild.com/%s/os/%s/%s.db", repo, target.packageArch, repo)
}

func (target packageTarget) packageURL(repo, filename string) string {
	if target.armSource {
		return fmt.Sprintf("http://mirror.archlinuxarm.org/%s/%s/%s", target.packageArch, repo, filename)
	}
	return fmt.Sprintf("https://geo.mirror.pkgbuild.com/%s/os/%s/%s", repo, target.packageArch, filename)
}

func (target packageTarget) acceptsPackageArchitecture(arch string) bool {
	return arch == "any" || arch == target.packageArch
}
