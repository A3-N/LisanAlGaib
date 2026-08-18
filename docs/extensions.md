# Building an extension

An extension is an out-of-process service. It owns its domain logic,
dependencies, image, jobs, state, artifacts, and restricted command surfaces.
Lisan discovers a small lifecycle bundle, talks protocol v3 over HTTP, and
renders semantic data. It never imports extension code or switches on an
extension ID.

The repository ships without a bundled example extension. Add a directory
under `extensions/` when developing one locally; discovery requires no core
registration.

## Directory contract

Create one directory beneath `extensions/`:

```text
extensions/example/
├── bundle.json
├── Dockerfile
└── cmd/example/...
```

Discovery reads every immediate `extensions/*/bundle.json`. Adding or removing
a valid directory is sufficient; no core registry needs editing.

```json
{
  "schema_version": 1,
  "id": "example",
  "name": "Example",
  "version": "3.0.0",
  "description": "One precise capability",
  "docker": {
    "image": "yourname/lisan-example:3",
    "context": ".",
    "dockerfile": "extensions/example/Dockerfile",
    "container": "lisan-example",
    "user": "10001:10001",
    "port": 7777,
    "tmpfs": ["/run:rw,noexec,nosuid,nodev,size=8m"],
    "environment": ["EXAMPLE_MODE=field"]
  },
  "native": {
    "executable": "extensions/example/bin/example_${os}_${arch}",
    "source_package": "extensions/example/cmd/example"
  },
  "requests": {
    "persistent_state": true,
    "shared_read": true,
    "shared_write": false,
    "internet": false
  }
}
```

All paths are runtime-relative and cannot escape the installed bundle. `${os}`
and `${arch}` resolve from the Lisan binary target. `scripts/build-release`
discovers source packages and embeds the matching native extension executable
in each Linux, macOS, and Windows release, so release users do not need Go.
During source development, `lisan vm` builds a missing native executable in a
temporary directory.

An already-hosted service can instead use an external-only bundle:

```json
{
  "schema_version": 1,
  "id": "hosted-example",
  "name": "Hosted Example",
  "version": "3.0.0",
  "external": {"endpoint": "https://extension.example.test"}
}
```

That endpoint must be reachable from the selected runtime. Lisan discovers and
renders it but does not build, start, stop, or remove it.

## Runtimes and grants

All extensions begin disabled. Enabling an extension and granting its requested
capabilities are separate config choices. A bundle cannot receive a capability
it did not request.

Docker-managed extensions receive:

- a read-only root filesystem, dropped Linux capabilities, and
  `no-new-privileges`;
- a non-root identity chosen by the bundle;
- `/tmp` plus declared bounded tmpfs mounts;
- a private internal control network used only for protocol traffic.

Optional grants add exactly these resources:

| Grant | Docker effect | Environment |
| --- | --- | --- |
| `internet` | Attach to an extension-specific outbound egress network | none |
| `persistent_state` | Managed volume at `/var/lib/lisan-extension` | `LISAN_EXTENSION_STATE` |
| `shared_read` | Read-only shared bind mount at `/shared` | `LISAN_EXTENSION_SHARED` |
| `shared_write` | Make the shared mount writable; implies read | `LISAN_EXTENSION_SHARED` |

The control protocol never needs a published host port, workspace mount,
Docker socket, device, secret, or host home directory.

The workspace joins an enabled extension's private control network because the
core client must reach its API. That network isolates the sidecar from other
extensions; it does not make the extension API inaccessible to hostile code in
the workspace. Treat every endpoint as untrusted input and expose only narrow,
validated actions.

The `internet` grant controls the running sidecar. It does not constrain
Docker's image-build network. A user who enables a locally added bundle trusts
its Dockerfile and the files in its build context. Keep extension-owned build
inputs inside the bundle directory. Lisan fingerprints that whole directory,
the Dockerfile, and shared module/core inputs so relevant source changes
rebuild the image while unchanged Docker layers remain cacheable.

`vm` launches the platform-native executable on an ephemeral loopback port and
stops it with Lisan. It passes granted state/shared paths, but Wormsign is an
unsandboxed host-user mode: those path hints are organization, not a security
boundary. Use Docker for hostile extension code.

Switching tabs leaves the extension process, jobs, and current session alive.
The core keeps at most one open interactive session for each extension and
closes replaced sessions. Closing the final Docker cockpit stops managed
sidecars; Docker state and granted volumes remain for the next launch.
`lisan cleanup` removes managed extension containers, images, state volumes,
and private control/egress networks. It preserves the host shared directory.

## Implement the service

Implement the HTTP contract in [connectors.md](connectors.md) using any
language. Go extensions may use `internal/extensionhost.Handler` as an adapter,
but it is optional; the wire format is the compatibility boundary.

The service advertises:

- semantic views made of text, status, key/value, list, table, and progress
  blocks;
- typed actions with text, number, boolean, and select inputs;
- asynchronous jobs with progress, logs, cancellation, results, and artifacts;
- named sessions that accept input and return bounded output.

Lisan owns navigation, wrapping, scrolling, themes, input editing, polling,
artifact checksum verification, and export to the shared directory. The
extension owns validation and every side effect.

The core also owns control affordances. The sidebar only selects a view,
action, session, or artifact; only category rows use collapse arrows. The main
pane renders manifest inputs as clickable, themed `TEXT`, `NUMBER`, `SELECT`,
and `TOGGLE` fields, followed by a dedicated `RUN` button. Sessions and
artifacts receive dedicated `OPEN` and `SAVE` buttons there as well. Extension
authors should supply semantic types and labels, not ANSI styling,
terminal-specific symbols, or hit-testing logic.

An extension session may feel like a shell, but the extension must define its
boundary. A mobile extension could accept `devices`, `install`, and `logs`, then
map those commands to fixed `adb` argument arrays inside its own process or
container. Do not expose an arbitrary shell merely because the transport can
carry text; implement a restricted command vocabulary instead.

## When the core should change

Do not add extension names, commands, view layouts, or dependencies to Lisan.
Change the core only when an independent extension cannot express a generally
useful capability through the existing lifecycle, grant, or protocol model.
Add the generic capability to the versioned contract, renderer, both runtime
paths, tests, and this guide; keep the domain implementation in the extension.

Before publishing an extension:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Test it disabled, enabled without grants, enabled with each requested grant,
under Docker, and as a native process on every platform you ship.
