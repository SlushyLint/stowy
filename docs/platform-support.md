# Platform support

Stowy selects packages for the architecture of its running Linux binary:

| Host | Package architecture | Repositories |
| --- | --- | --- |
| x86-64 (`amd64`) | `x86_64` | Arch Linux `core`, `extra`, `multilib` |
| 64-bit ARM (`arm64`) | `aarch64` | Arch Linux ARM `core`, `extra` |
| 32-bit ARMv7 hard-float | `armv7h` | Arch Linux ARM `core`, `extra` |

Other architectures, and 32-bit ARM machines below ARMv7, are rejected rather than guessed. On 32-bit ARM, Stowy selects the `armv7h` hard-float package set based on the machine reported by `uname`; it does not independently verify the host userspace's float ABI, so that target requires a hard-float userspace. Arch Linux ARM packages use `.pkg.tar.xz`; Stowy also reads the `.pkg.tar.zst` archives used by Arch Linux.

**ARM repository metadata and packages currently download over HTTP, and Stowy does not verify signatures.** This is an acknowledged prototype limitation: network tampering could replace metadata or package contents. Do not use ARM package installs on untrusted networks.

## App runtime and sandbox

Stowy bootstraps Arch's `filesystem`, `glibc`, `bash`, and `ca-certificates`
packages into `~/.local/stowy/root` as needed. Installed apps run with that
tree as their root filesystem inside private Linux user and mount
namespaces, so their ELF loader and userspace libraries come from the Arch
runtime rather than the host distro. Existing command launchers are migrated on
the next package install. Apps still use the host kernel and must match the host
CPU architecture.

The host must allow unprivileged user namespaces and mount namespaces. If it
does not, apps fail to launch; Stowy never retries outside the sandbox. The
sandbox exposes the user's full home directory, desktop-session runtime
directory, host devices, X11 sockets when present, and host DNS/hosts files.
Networking remains enabled. Apps see UID 0 inside the namespace, mapped to the
invoking host user; Linux capabilities are dropped and `no_new_privs` is set
before execution. Apps that refuse to run as root fail clearly. Stowy does not
guess from exit codes or error text and retry, because an app might have already
performed work. Supporting a non-root identity requires additional host
subordinate UID/GID mappings, which are not universally configured.

The sandbox exposes only the app's own process information and selected
read-only kernel information under `/proc`, not the host process list. Desktop
integrations and full home access mean this is not a security boundary for
untrusted apps. Apps may also require runtime packages beyond the base runtime
or host-specific resources that are not exposed.

This removes ordinary userspace-library and ELF-loader dependence on the host
distro; it does not promise that every upstream Arch package will work with
every kernel, desktop session, or hardware setup.
