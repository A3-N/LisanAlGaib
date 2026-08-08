# Building a Lisan extension

An extension is an independent service that implements Lisan's versioned HTTP
protocol. Keep its feature logic, dependencies, tests, image, and assets inside
its own directory. Lisan should only provide generic lifecycle and rendering
support; change the core only when the shared protocol cannot express a real
extension requirement.

`docker/connectors/ixian-proving-ground` is the bundled protocol reference. It
is disabled by default in every preset, so it is neither built nor started
until the user enables it. The fixture deliberately exercises every protocol
v2 panel, tool, action, result, failure, wrapping, and scrolling path.

## Choose an implementation

There are two supported approaches:

1. **Declarative:** provide an `extension.json` and run Lisan's generic
   `extension-host`. This is the most portable option and is also the only
   managed extension form currently hosted natively by `lisan vm`.
2. **Independent service:** implement the three HTTP endpoints in any language
   or framework. This works as a Docker extension. Use `managed: true` when
   Lisan owns the image/container, or `managed: false` when another system owns
   an already-running container.

Do not add extension-specific packages, IDs, panels, or commands to the Lisan
binary. A new generic protocol capability belongs in the core only when an
extension cannot otherwise work correctly in a supported runtime.

## Declarative extension

Create `docker/connectors/<id>/extension.json`:

```json
{
  "manifest": {
    "protocol_version": 2,
    "id": "example",
    "name": "Example",
    "description": "One precise feature",
    "ui": {
      "sidebar": [
        {"id": "actions", "title": "Actions", "kind": "actions", "expanded": true}
      ],
      "main": [
        {"id": "summary", "title": "Example", "kind": "summary"},
        {"id": "output", "title": "Result", "kind": "action-output"}
      ]
    }
  },
  "actions": [
    {
      "id": "guide",
      "name": "Show guide",
      "description": "Return portable content without a child process",
      "output": "Hello from the extension.\n"
    },
    {
      "id": "inspect",
      "name": "Inspect",
      "description": "Run one fixed executable and argument list",
      "command": "uname",
      "args": ["-a"]
    }
  ]
}
```

An action requires exactly one of:

- `output`: fixed, cross-platform text returned directly by the generic host.
- `command` plus optional `args`: one executable launched without a shell.

Tools use `command` plus optional `version_args`. The generic host locates the
executable and reports readiness/version in the manifest. Commands and actions
are fixed by the extension; protocol v2 never accepts shell text or arbitrary
arguments from the TUI.

### Extension-owned command surfaces

An extension is allowed to run a shell when the shell and every argument are
chosen by the extension. For example, a mobile extension may ship a small
dispatcher whose `devices`, `install`, and `logs` subcommands invoke only the
approved `adb` operations. Declarative actions can call that dispatcher
directly, or call an extension-local shell with a fixed script:

```json
{
  "id": "devices",
  "name": "Connected devices",
  "command": "/bin/sh",
  "args": ["-c", "exec adb devices -l"]
}
```

In Docker mode that process runs in the extension container, not Lisan's main
terminal. In `vm` mode a declarative command is a separate child process on the
host: its aliases, working directory, and shell state cannot mutate Lisan's
terminal session, but its filesystem and network side effects are still real.
Never concatenate user text into `sh -c`; prefer a compiled dispatcher or
script that implements an explicit command allowlist and aliases higher-level
operations to fixed argument arrays.

Protocol v2 actions are one-shot and return captured output. They are not an
interactive PTY. When a real extension needs an interactive console, add a new
versioned, generic streaming/input contract: the restricted REPL or PTY stays
inside the extension, validates commands server-side, and Lisan transports
only input, output, resize, and close events. Do not attach it to the main
terminal session or grant the workspace container a Docker socket merely to
implement the console.

Use a multi-stage Dockerfile like Ixian Proving Ground's: compile only
`cmd/extension-host` and the packages it imports, copy the extension JSON, run
as a numeric non-root user, and expose port `7777`. Give the image a new tag
whenever its contents change; Lisan deliberately reuses an existing image tag.

## Register it

Add a connector object to a profile's `connectors` array:

```json
{
  "id": "example",
  "name": "Example",
  "description": "One precise feature",
  "enabled": false,
  "managed": true,
  "image": "yourname/lisan-example:1",
  "build_context": ".",
  "dockerfile": "docker/connectors/example/Dockerfile",
  "native_config": "docker/connectors/example/extension.json",
  "container": "lisan-example",
  "user": "10001:10001",
  "network": "arrakis-shield-wall",
  "endpoint": "http://lisan-example:7777"
}
```

Keep it disabled initially. The config TUI can then toggle it like any other
extension. Paths must resolve inside Lisan's installed runtime; IDs, container
names, and network names must be Docker-safe and unique.

## What Lisan does

For an enabled managed Docker extension, Lisan:

- builds the image only when the configured tag is missing;
- creates/connects the configured Docker network;
- starts a labelled, read-only container as the configured user;
- drops all capabilities, sets `no-new-privileges`, and supplies only a small
  `/tmp` tmpfs;
- polls the manifest and renders only the panels it advertises;
- stops owned containers when the extension is disabled and removes owned
  state during cleanup.

For `lisan vm`, Lisan reads `native_config`, starts the generic host on an
ephemeral loopback address, and keeps that address in memory for the current
run. It does not persist the loopback endpoint over the Docker endpoint.

The main TUI supports these protocol v2 modules:

| Area | Kind | Data source |
| --- | --- | --- |
| Sidebar | `tools` | manifest `tools` |
| Sidebar | `actions` | manifest `actions` |
| Main | `summary` | extension identity and counts |
| Main | `action-output` | the most recent run response, wrapped to the pane with vertical scrolling |

The service contract is:

- `GET /v1/health` for readiness.
- `GET /v1/manifest` for identity, UI modules, tools, and actions.
- `POST /v1/run` with `{"action_id":"..."}` for an advertised action.

See [connectors.md](connectors.md) for the exact wire examples and limits.

## Current boundaries

Docker extensions receive networking, not workspace mounts, Docker socket
access, secrets, or arbitrary environment injection. Protocol v2 actions have
no user-input form. Native managed extensions use the declarative host rather
than launching an extension-specific native executable. If an extension truly
needs one of those capabilities, document the use case and extend the generic
profile/protocol/runtime contract instead of special-casing that extension.

Test the extension config with `go test ./internal/extensionhost` and then run
the full suite with `go test ./...`. Test Docker mode with the extension both
disabled and explicitly enabled; also test `vm` when `native_config` is
provided.
