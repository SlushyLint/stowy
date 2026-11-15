# stowy

`stowy` is a small, experimental Arch Linux package installer that keeps packages in a user-owned prefix instead of writing to system paths. It is intended as a learning project and local tool for exploring how package archives can be downloaded, extracted, and exposed from `~/.local` without requiring `sudo`.

This is not a replacement for `pacman`, and it is not a hardened production package manager. It is a compact prototype focused on user-space package experiments.

## What it does

- Searches official Arch repository databases by package name
- Downloads repository metadata from a mirror
- Installs packages from `core`, `extra`, and `multilib`
- Installs local `.pkg.tar.zst` archives
- Extracts package contents into `~/.local/stowy`
- Creates helper launchers under `~/.local/bin`
- Removes packages previously installed by `stowy`
- Rejects unsafe archive entries and unsupported package layout paths

## Why this project exists

The idea is simple: keep package metadata and extracted files under a user-owned prefix, expose the resulting binaries on `PATH`, and avoid modifying root-owned directories. It is useful for experimentation, learning, and local tooling where a full package manager would be overkill.

## Requirements

- Go 1.27+
- Internet access to download Arch repository metadata
- A Linux environment with a standard Arch package layout in mind

## Installation

From the repository root, build the binary:

```bash
go build ./...
```

This produces a `stowy` executable in the current working directory.

## Quick start

```bash
go build ./...
./stowy i ripgrep
ripgrep --version
```

## Commands

### Search a repository

```bash
./stowy s <repo> <term>
```

Example:

```bash
./stowy s core ripgrep
```

This downloads the repository database and prints package matches.

### Install a package from an Arch repository

```bash
./stowy i <package>
```

Example:

```bash
./stowy i ripgrep
```

`stowy` checks the `core`, `extra`, and `multilib` repositories and installs the best match it finds.

### Install a local package archive

```bash
./stowy l <package.tar.zst>
```

Example:

```bash
./stowy l /var/cache/pacman/pkg/ripgrep-14.1.0-1-x86_64.pkg.tar.zst
```

### Remove a package

```bash
./stowy uninstall <package>
```

Aliases are also accepted:

```bash
./stowy d ripgrep
./stowy remove ripgrep
./stowy delete ripgrep
```

## Install targets

Package files are unpacked under:

```bash
~/.local/stowy
```

The project also creates symlinks or launchers in:

```bash
~/.local/bin
```

This keeps installed commands accessible on your `PATH` while leaving the main package tree inside your home directory.

## Repository layout

- `main.go` — CLI entry point and command dispatch
- `repo.go` — repository database download and lookup logic
- `search.go` — search helpers over package metadata
- `meta.go` and `meta/meta.go` — package metadata parsing
- `install/` — archive extraction and install logic
- `testdata/` — fixtures used in tests

## Safety and limitations

`stowy` is intentionally a prototype, not a trusted package-management workflow.

At the moment it does not verify package integrity before extraction. It does not validate cryptographic signatures, compare checksums against trusted metadata, or defend against compromised mirrors or malicious archives in a production-grade way. It assumes repository metadata and package sources are already trusted by the user.

The current implementation also does not provide:

- automatic dependency resolution
- package database tracking
- signature verification
- hardened security guarantees
- robust conflict handling

Use it with packages you are comfortable inspecting and testing in a controlled environment. Even though it installs into a user-owned prefix, a compromised source can still be dangerous.

## License

This repository does not currently include a license file. If you plan to distribute or publish the project, add an explicit license before doing so.
