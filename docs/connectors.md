# Connector protocol v2

Connectors keep specialist task logic outside the Lisan binary. The cockpit is
responsible for lifecycle, discovery, navigation, input selection, and safe
rendering; the sidecar owns its tools and predefined actions.

## Profile entry

```json
{
  "id": "host-check",
  "name": "Ornithopter",
  "icon": "󰒋",
  "description": "Read-only runtime host diagnostics example",
  "enabled": true,
  "managed": true,
  "image": "lisanalgaib/host-check:1",
  "build_context": ".",
  "dockerfile": "docker/connectors/host-check/Dockerfile",
  "native_config": "docker/connectors/host-check/extension.json",
  "container": "lisan-host-check",
  "user": "10001:10001",
  "network": "arrakis-shield-wall",
  "endpoint": "http://lisan-host-check:7777"
}
```

For a managed connector, also provide `image`. `build_context` and `dockerfile`
are optional; when present they must resolve inside Lisan's installed runtime.
Lisan builds that context only when the enabled connector's image is missing;
it does not overwrite an existing tag. Bump the configured image tag when a
connector build changes.
`native_config` is the mandatory declarative host configuration used by
`vm`; it must also resolve inside the installed runtime. Wormsign mode
starts managed extensions on ephemeral loopback ports and cleans them up with
the TUI. External connectors remain external in both modes.
Managed containers receive no published ports, run as the profile's explicit
`user` identity (the bundled default is non-root), and are read-only with dropped capabilities and
`no-new-privileges`. They are labelled with their connector ID and a runtime
configuration signature so relevant profile changes recreate stale containers.
Each profile can configure up to 16 extensions.

An external (`managed: false`) connector must already exist. Lisan connects it
and `sietch-tabr` to `network` but does not start, stop, rebuild, or remove it.

## Manifest

`GET /v1/manifest` returns:

```json
{
  "protocol_version": 2,
  "id": "host-check",
  "name": "Ornithopter",
  "icon": "󰒋",
  "description": "Read-only runtime host diagnostics example",
  "ui": {
    "sidebar": [{"id": "actions", "title": "Checks", "kind": "actions", "expanded": true}],
    "main": [{"id": "summary", "title": "Ornithopter", "kind": "summary"}]
  },
  "tools": [{
    "id": "uname",
    "name": "uname",
    "description": "Operating-system information",
    "version": "available",
    "ready": true
  }],
  "actions": [{
    "id": "system",
    "name": "System information",
    "description": "Show the kernel and architecture"
  }]
}
```

The tab itself comes from the profile so it exists even when the service is
offline. Tools and actions come entirely from the manifest.

## Actions

`POST /v1/run` accepts only an advertised action ID:

```json
{"action_id":"system"}
```

The response is:

```json
{
  "action_id": "system",
  "output": "Linux host-check 6.x x86_64 GNU/Linux\n",
  "exit_code": 0,
  "duration_ms": 42,
  "error": ""
}
```

Lisan does not send arbitrary shell commands. Connector authors should map IDs
to fixed executables and argument arrays, set timeouts and output limits, avoid
shell interpolation, and run as an unprivileged user. Responses are capped at
1 MiB by the client and control characters are stripped before display. A
manifest can advertise at most 32 panels in each area, 100 tools, and 100
actions.

## Compatibility

The client rejects manifest protocol versions it does not understand. A future
protocol should add a new version rather than changing v2 semantics in place.
