# LisanAlGaib

**Full-permission vibe coding without handing your host to the vibes.**

LisanAlGaib is a Go terminal cockpit for coding agents, NvChad, and shells. Its
recommended `docker` mode gives agents a persistent Linux workspace where they
can install tools, run commands, and use `sudo` freely—without mounting your
host project, Docker socket, or home directory.

The goal is simple: let the agents cook without fearing for your life every time
they execute a command.

## Runs everywhere

One standalone binary embeds everything Lisan needs to assemble its selected
Docker workspace. Release users need Docker, not Go, Node.js, or a repository
clone.

| Release platform | `amd64` | `arm64` |
|---|:---:|:---:|
| Linux | ✓ | ✓ |
| macOS | ✓ | ✓ |
| Windows | ✓ | ✓ |

Docker mode runs the same Linux workspace on every host architecture. Lisan
selects compatible native agent downloads while building the image.
It only checks for Docker plus the Compose plugin and never installs host
software. Missing host prerequisites produce an error. Only `vm` may install
selected host dependencies.

## Quick start

Linux `amd64` example (substitute `darwin` or `arm64` for your release):

```bash
chmod +x ./lisan_linux_amd64
./lisan_linux_amd64 install
lisan config
lisan docker
```

Windows PowerShell:

```powershell
./lisan_windows_amd64.exe install
lisan config
lisan docker
```

Choose only the tools and agents you want in `config`. Lisan derives the Docker
image from that profile, so disabled tools are not merely hidden from the UI;
they are not installed.

## The boundary

```text
Your terminal
  └─ Docker
      └─ Sietch Tabr
          ├─ coding agents with full container permissions
          ├─ NvChad and your selected shell
          ├─ /home/fremen/projects     persistent container projects
          ├─ /home/fremen/shared       explicit host exchange folder
          └─ no host Docker socket, home, or implicit project mount
```

The Usul Docker volume preserves the container home between launches. The only
host bind mount is `shared/`, making file exchange deliberate and easy to
audit. Anything placed there can be read, changed, or deleted by code in the
container.

```bash
lisan cleanup
```

Cleanup removes Lisan-owned containers, images, networks, and the persistent
Usul volume. It preserves the host `shared/` directory.

Docker is the recommended boundary for untrusted repositories, plugins,
installers, and agent-generated commands. A malicious package can own the
workspace, its credentials, the Usul volume, and `shared/`; it should not gain a
normal filesystem path to the rest of the host. Docker escape vulnerabilities,
network access, and resource exhaustion remain outside that promise. Keep
secrets and sensitive files outside the sandbox and read the exact threat model
in [SECURITY.md](SECURITY.md).

## Commands

```text
lisan docker [workspace]  isolated Docker workspace (recommended)
lisan vm [workspace]      full permissions as the current host user (unsafe)
lisan config              choose features, tools, agents, shell, and extensions
lisan cleanup             remove owned Docker state; preserve shared files
lisan install             install the binary and embedded runtime
lisan uninstall           remove the binary/runtime; preserve user data
lisan help | -h | --help  show command help
```

`vm` is Wormsign mode. Despite the name, it is not a virtual machine: agents run
directly as your host user. Use it only when you intentionally want host access.

## What you can select

- Codex, OpenCode, Claude, and Kimi agent CLIs
- Common language runtimes, package managers, network diagnostics, and workspace utilities
- Fish, Bash, Zsh, or POSIX `sh` inside Docker
- Manifest-driven extensions, disabled by default
- Minimal through full presets, with profile-aware Docker layer reuse

The TUI stays inside the terminal that launched it. Lisan does not choose your
terminal or host shell. A Nerd Font is recommended for interface icons.

## Sessions and resources

Changing tabs does not restart shells, agents, the editor, or extension
sessions. Shells and agents may keep working in the background; on Unix the
hidden editor is paused to avoid idle redraw work. Closing the last Docker
cockpit stops Lisan's containers, while their container state and named volumes
remain ready for the next launch. Background processes do not survive that
stop. `cleanup` is the destructive reset.

Lisan deliberately does not hardcode CPU or memory limits. That avoids quietly
starving a heavy build and lets the same release fit laptops and workstations.
Set the Docker VM's CPU, memory, swap, disk, and idle behavior in
[Docker Desktop settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/);
[Resource Saver](https://docs.docker.com/desktop/use-desktop/resource-saver/)
can reduce idle Desktop usage. On Linux, tune the Docker host itself. Enable
only the tools and extensions you use; profile-dependent installs remain in the
last Docker layers so changing them reuses the expensive base layers.

## Development and releases

Source development requires Go 1.26 or newer:

```bash
./scripts/go test ./...
./scripts/go test -race ./...
./scripts/go vet ./...
./scripts/build
```

Build every release target and its checksum file:

```bash
./scripts/build-release
```

Output is written to ignored `dist/` files for Linux, macOS, and Windows on
`amd64` and `arm64`, plus `dist/SHA256SUMS`. Native Windows currently uses a
standard-pipe child backend; deeply interactive child TUIs are most complete in
Docker or WSL.

Extension authors can start with the modular authoring guide in
[docs/extensions.md](docs/extensions.md); the exact versioned wire protocol is
documented in [docs/connectors.md](docs/connectors.md).
