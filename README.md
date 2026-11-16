# stowy-Debian

Stowy is an experimental user-space package installer. It extracts packages into
`~/.local/stowy/root` and exposes installed commands through `~/.local/bin`.
Despite the project name, the current package sources are Arch Linux and Arch
Linux ARM repositories; this is not yet a Debian package frontend.

## Build and use

```sh
go build -o stowy .
./stowy i ripgrep
./stowy l ./package.pkg.tar.zst
./stowy uninstall ripgrep
```

Local Arch Linux ARM `.pkg.tar.xz` archives are also supported. Package
dependencies are downloaded from the matching repository when available.

## Platform support

Stowy selects packages for the architecture of its running Linux binary:

| Host | Package architecture | Repositories |
| --- | --- | --- |
| x86-64 (`amd64`) | `x86_64` | Arch Linux `core`, `extra`, `multilib` |
| 64-bit ARM (`arm64`) | `aarch64` | Arch Linux ARM `core`, `extra` |
| 32-bit ARMv7 hard-float | `armv7h` | Arch Linux ARM `core`, `extra` |

Other architectures and 32-bit ARM machines below ARMv7 are rejected.
On 32-bit ARM, Stowy selects `armv7h` based on the machine reported by `uname`;
it does not independently verify the userspace float ABI, so a hard-float
userspace is required.

**ARM repository metadata and packages currently download over HTTP, and Stowy
does not verify signatures.** Network tampering could replace metadata or
package contents. Do not install ARM packages over untrusted networks.

Exposed dynamically linked commands use the package set's glibc loader and
libraries, avoiding mixing them with the host's glibc. This is not a complete
runtime isolation: packages may still require host kernel features or other
host facilities. See
[platform-support.md](./docs/platform-support.md) for details.

Stowy is a prototype, not a hardened package-management workflow. Review the
security and package-source limitations before installing packages.
