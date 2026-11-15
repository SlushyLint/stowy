# Platform support

Stowy selects packages for the architecture of its running Linux binary:

| Host | Package architecture | Repositories |
| --- | --- | --- |
| x86-64 (`amd64`) | `x86_64` | Arch Linux `core`, `extra`, `multilib` |
| 64-bit ARM (`arm64`) | `aarch64` | Arch Linux ARM `core`, `extra` |
| 32-bit ARMv7 hard-float | `armv7h` | Arch Linux ARM `core`, `extra` |

Other architectures, and 32-bit ARM machines below ARMv7, are rejected rather than guessed. On 32-bit ARM, Stowy selects the `armv7h` hard-float package set based on the machine reported by `uname`; it does not independently verify the host userspace's float ABI, so that target requires a hard-float userspace. Arch Linux ARM packages use `.pkg.tar.xz`; Stowy also reads the `.pkg.tar.zst` archives used by Arch Linux.

**ARM repository metadata and packages currently download over HTTP, and Stowy does not verify signatures.** This is an acknowledged prototype limitation: network tampering could replace metadata or package contents. Do not use ARM package installs on untrusted networks.

Architecture selection does not guarantee compatibility with every glibc-based distribution. Packages may require a newer glibc or other host runtime facilities; Stowy does not provide a complete isolated runtime.
