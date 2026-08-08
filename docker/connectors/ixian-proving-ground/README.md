# Ixian Proving Ground

Ixian Proving Ground is the one bundled reference extension. It is disabled in
every preset and is never built or started until explicitly enabled.

It exercises every capability that protocol v2 and the current TUI actually
support:

- tool readiness and version probes in a `tools` sidebar panel;
- advertised actions in an `actions` sidebar panel;
- generic `summary` and `action-output` main panels;
- static output without a child process;
- fixed command plus fixed argument actions;
- an allowlisted extension-owned shell dispatcher;
- captured stdout/stderr, duration, non-zero exit status, and errors;
- long output and long lines for TUI wrapping and vertical scrolling;
- a private HTTP call from the sidecar to its own health endpoint.

The dispatcher accepts exactly four hardcoded operations. It cannot receive
shell text or arbitrary arguments from Lisan. In Docker mode it runs as the
numeric `ixian` user inside the restricted extension sidecar, not in Sietch
Tabr and not in the user's terminal.

The container intentionally has no workspace or Fremen-home mount, Docker
socket, device access, injected secrets, or Linux capabilities. Its root
filesystem is read-only at runtime and Lisan supplies only the standard small
`/tmp` tmpfs and extension network.

The container-specific dispatcher actions are a Docker demonstration. In
`lisan vm`, the static TUI tour and host tool discovery work, while actions
whose executable is `/usr/local/bin/ixian-showcase` correctly fail unless the
developer has installed that fixture on the host. This makes the runtime
boundary visible instead of hiding it with core-specific behavior.

This is a protocol fixture, not a useful product feature. Copy its structure
when authoring an extension, then replace the identity, tools, actions, image,
and tests with one focused capability.
