# LisanAlGaib

LisanAlGaib is a mouse-aware Go terminal cockpit for NvChad, shells, coding
agents, and manifest-driven extensions. Its recommended runtime is a persistent
Docker workspace assembled from the active profile: tools and agents are added
only when selected or required by an enabled feature.

Lisan stays inside the terminal that launched it. It never selects or starts
Ghostty, Kitty, Windows Terminal, or another emulator.

## Runtime and trust boundary

```text
User-selected host terminal
  └─ docker compose exec
       └─ Sietch Tabr (`fremen@tabr`)
            ├─ Lisan TUI
            ├─ optional NvChad and configured shell
            ├─ selected native agent executables
            ├─ Usul persistent home volume
            └─ Shield Wall private connector network
```

The workspace does not mount the host Docker socket or a host project
directory. Code inside it can still read and modify everything stored in the
Usul volume, so treat that volume as trusted persistent data.

| Name | Actual component |
|---|---|
| Arrakis | Docker Compose project |
| Heighliner | disposable Go builder stage |
| Sietch Tabr | isolated workspace container |
| Usul | persistent `arrakis-usul` home volume |
| Shield Wall | private `arrakis-shield-wall` network |
| Golden Path | configuration screen and full preset |
| Mentats | coding-agent pages |
| Ornithopter | bundled read-only diagnostic extension |
| Wormsign | unsandboxed current-host execution used by `vm` |

The `vm` command retains the agreed CLI name, but its current implementation
does **not** create a hypervisor virtual machine. It is Wormsign: tools execute
with the current host user's permissions. Use `docker` for untrusted code.

## Install

Release binaries support Linux, macOS, and Windows on `amd64` and `arm64`.
Each binary embeds the small source runtime required to build the selected
Linux Docker image; release users do not need Go or a repository clone.

Linux example:

```bash
chmod +x ./lisan_linux_amd64
./lisan_linux_amd64 install
```

PowerShell example:

```powershell
./lisan_windows_amd64.exe install
```

| Platform | Installed executable | Config/runtime |
|---|---|---|
| Linux | `~/.local/bin/lisan` | `$XDG_CONFIG_HOME/lisanalgaib` or `~/.config/lisanalgaib` |
| macOS | `~/.local/bin/lisan` | `~/Library/Application Support/lisanalgaib` |
| Windows | `%LOCALAPPDATA%\Programs\LisanAlGaib\lisan.exe` | `%LOCALAPPDATA%\lisanalgaib` |

`install` writes only the executable, config, and Docker build runtime. Host
NvChad and agent workspaces are created later only when selected in Wormsign.

```bash
lisan uninstall
```

Uninstall removes the installed executable and runtime. It preserves profiles,
user editor/agent files, Docker images and containers, and the Usul volume.

## Commands

Running `lisan` without a command opens the launcher.

```text
lisan docker [workspace]  isolated Sietch Tabr workspace (recommended)
lisan vm [workspace]      Wormsign as the current host user (unsandboxed)
lisan config              edit, save, and activate profiles
lisan cleanup             stop owned services after an interrupted launch
lisan install             install this executable and embedded runtime
lisan uninstall           remove executable/runtime and preserve user data
lisan help | -h | --help  print command help
```

Commands use positional words; legacy `--mode` flags are intentionally not
accepted. `-h` and `--help` are the only flag aliases. A workspace argument is
a container path for `docker` and a host path for `vm`.

## Profiles and image size

The configuration UI provides four starting presets:

| Preset | Purpose |
|---|---|
| Golden Path | every page, tool, agent, and Ornithopter |
| Mentat | NvChad, Git, search, and tool inventory |
| Landsraad | editor, terminal, and every agent page |
| Muad'Dib | overview only; no child process |

Features, tools, agents, the Docker shell, and extensions are independently
selectable. Saving a new combination creates a versioned profile; selecting an
existing combination reuses its revision.

The Docker build plan is derived from the active profile:

- Git, ripgrep, Neovim/NvChad, Node.js/npm, Go, and Python are installed only
  when selected or required.
- Codex, OpenCode, Claude, and Kimi are version- and checksum-pinned native
  downloads in Docker. Selecting Codex does not install Node.js or npm.
- Agent download tools remain in a disposable builder stage.
- Stable OS packages precede the profile-dependent apt layer, allowing profile
  changes to reuse the expensive base cache.
- Tests, release tooling, Windows-only Go source, docs, scripts, and build
  artifacts are excluded from the installed Linux Docker runtime.

Managed extensions are built only when they are present, enabled, managed, and
their configured image is missing. Disabled extensions are not built. External
(`managed: false`) extensions are never built by Lisan. Disabling a previously
built extension stops its owned container but retains the image for fast reuse.
When a managed extension's build changes, bump its configured image tag; Lisan
does not overwrite an existing tag automatically.

## Docker lifecycle and storage

The first `docker` launch builds `lisanalgaib:latest`, starts `sietch-tabr`,
synchronizes enabled extensions, and executes Lisan as the configured container
user. The image carries a signature of the resolved profile and relevant build
inputs. Matching launches reuse it; relevant changes rebuild and recreate the
workspace while retaining Usul.

Lisan records which services it started and stops only those services when the
session exits. Containers and images remain reusable. If the process was killed
before deferred cleanup ran:

```bash
lisan cleanup
```

Cleanup is idempotent and stops only Lisan-labelled services. It never deletes
persistent data.

The Compose file deliberately has no hard CPU, memory, PID, or shared-memory
limit. Idle operation remains small while compilers and agents can burst into
the resources Docker is allowed to use. On Docker Desktop, configure that
allowance in Docker Desktop's resource settings.

Useful inspection commands:

```bash
docker stats sietch-tabr
docker system df
```

BuildKit cache trades disk for faster profile rebuilds. Prune it only when
reclaiming disk matters more than the next build.

### Projects and persistence

Clone projects from the embedded terminal into `/home/fremen/projects`. To copy
an existing host project without mounting it into the sandbox:

```bash
docker exec -u fremen sietch-tabr mkdir -p /home/fremen/projects/my-project
docker cp /absolute/host/project/. sietch-tabr:/home/fremen/projects/my-project/
docker exec -u root sietch-tabr chown -R fremen:fremen /home/fremen/projects/my-project
```

The container accounts have locked passwords. `fremen` is the default user and
has passwordless `sudo` inside the workspace; this convenience is not a
security boundary. No supplied service publishes a host port.

## Wormsign host behavior

Wormsign installs only missing dependencies reachable from the active profile,
using apt on supported Linux hosts, Homebrew on macOS, and winget on Windows.
Files/NvChad derives Neovim, Git, ripgrep, `fd`, and unzip requirements. Host
NvChad and only the selected agent instruction folders are seeded on first use;
unrelated existing files are preserved.

Codex and OpenCode currently use their npm installers in Wormsign with a
writable `~/.local` prefix. Claude and Kimi use their native installer
endpoints. These host installers and everything they launch receive the current
user's filesystem permissions.

## Extensions

Extensions use a generic versioned HTTP protocol. Each manifest declares its
sidebar and main panels, tools, and fixed actions; the core TUI contains no
extension-specific page logic. Responses are bounded and terminal controls are
removed before display.

Ornithopter is the single bundled example. It exposes read-only hostname,
system, uptime, and filesystem checks. Its container runs unprivileged and
read-only with dropped capabilities and `no-new-privileges`. In Wormsign, the
same manifest is hosted on an ephemeral loopback endpoint for the lifetime of
the Lisan process.

Protocol, manifest, lifecycle, and authoring details are in
[docs/connectors.md](docs/connectors.md).

## Files, agents, and shell behavior

Files embeds NvChad at the full main-pane size. Its dashboard can choose a
workspace, find/recent files, search text, and show mappings. `Ctrl-N` remains
NvChad's NvimTree toggle. Docker Files can see only the Usul home; Wormsign can
see the selected host workspace.

The persistent Docker home includes one instruction workspace per agent:

```text
/home/fremen/agents/codex/AGENTS.md
/home/fremen/agents/opencode/AGENTS.md
/home/fremen/agents/kimi/AGENTS.md
/home/fremen/agents/claude/CLAUDE.md
```

Agents run directly in their own folder and own their authentication and
credential files. Lisan does not read or display API-key values.

The host terminal page uses the default shell from `SHELL`; Windows falls back
to `COMSPEC`, PowerShell, or Command Prompt. Only the embedded Docker shell is
configurable: Fish (default), Bash, Zsh, or POSIX `sh`. Lisan cannot change the
invoking terminal or its font.

| Input | Action |
|---|---|
| Mouse | select navigation, sidebar items, and embedded applications |
| `Ctrl-G` | toggle input between an embedded application and Lisan |
| `Tab` / `Shift-Tab` | cycle top-level pages |
| `Ctrl-B` | collapse or expand the contextual sidebar |
| `h` / `l`, `j` / `k` | move focus and selection |
| `Enter` / `Space` | activate or expand the selected item |
| `e` | open the selected skill in NvChad |
| `F2` | cycle themes |
| `r` | rescan configured tools and skills |
| `?` | show help |
| `Ctrl-C` | quit from wrapper mode |

## Development and releases

Go 1.26 is required for source builds.

```bash
./scripts/go test ./...
./scripts/go test -race ./...
./scripts/go vet ./...
./scripts/build
./scripts/run docker
```

`scripts/go` keeps its module and build cache outside the checkout. Lisan
filters the legacy keyboard sequences that conflict with Kitty's native
protocol without modifying downloaded dependencies.

Build all supported release targets:

```bash
./scripts/build-release
```

The script creates stripped, reproducible binaries and `dist/SHA256SUMS` for:

- Linux: `amd64`, `arm64`
- macOS: `amd64`, `arm64`
- Windows: `amd64`, `arm64`

All release output and generated embedded archives are ignored by Git. Unix
uses a real PTY. The native Windows child backend currently uses standard pipes,
so deeply interactive child TUIs are more complete inside Docker/WSL.

## Security and licensing notes

Use Docker for untrusted repositories, plugins, package installers, and agents.
The exact trust boundary, persistence caveats, and reporting guidance are in
[SECURITY.md](SECURITY.md).

The navigation and Vim-oriented workflow are inspired by
[NvChad](https://github.com/NvChad/NvChad), which is GPL-3.0. Lisan does not copy
or translate NvChad source; it runs the separately installed NvChad/Neovim
process through a PTY.
