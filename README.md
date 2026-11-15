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
Stowy also installs an isolated Arch runtime (`filesystem`, `glibc`, `bash`,
and `ca-certificates`) so installed apps use their package runtime instead of
the host distro's libraries. Existing command launchers are migrated to
sandboxed execution on the next package installation.

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

Apps run in private Linux user and mount namespaces, with the Stowy runtime as
their root filesystem. User namespaces must be enabled by the host kernel;
Stowy reports an error instead of falling back to an unsandboxed launch. Apps
can access the user's full home directory, desktop session sockets, host
devices, and network. Apps see UID 0 inside the namespace, with capabilities
dropped; apps that refuse to run as root fail without an automatic retry. This
improves runtime portability, but is not a security boundary for untrusted
apps. See [platform-support.md](./docs/platform-support.md) for details.

Stowy is a prototype, not a hardened package-management workflow. Review the
security and package-source limitations before installing packages.
