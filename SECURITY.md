# Security

## What Docker mode is designed to contain

Assume a repository, npm package, installer, editor plugin, or coding agent gets
arbitrary command execution and becomes root inside Sietch Tabr. Lisan treats
that entire workspace as compromised.

The shipped Docker configuration gives that code no normal filesystem path to
the host except the explicit `shared/` bind mount. It does not mount a host
project, home directory, Docker/Podman socket, device, or host namespace. It
does not publish workspace or managed-extension ports. Docker's long bind-mount
syntax also refuses to silently create a mistyped host source directory. See
Docker's [bind-mount behavior](https://docs.docker.com/engine/storage/bind-mounts/).

Managed extensions run in separate containers with:

- one private internal control network per extension;
- a read-only root filesystem and bounded `noexec,nosuid,nodev` tmpfs mounts;
- all Linux capabilities dropped and `no-new-privileges` enabled;
- a required numeric non-root user and no automatic restart;
- no state, shared-folder, or outbound-network access unless the bundle asks
  for it and the user grants it.

Lisan validates extension image arguments, names, environment entries, mounts,
and grants before constructing Docker commands. It never accepts an arbitrary
managed-container command from configuration.

## What remains exposed

Docker mode is containment, not a virtual machine or a guarantee against every
host compromise.

| Asset or capability | Result after workspace compromise |
| --- | --- |
| Workspace container and root filesystem | Fully compromised; `fremen` has passwordless `sudo` by design |
| Usul home volume and projects inside it | Readable, changeable, and deletable |
| Agent credentials stored in the container | Readable and usable by malicious workspace code |
| Host `shared/` directory | Readable, changeable, and deletable |
| Host files outside `shared/` | Not mounted and not reachable through normal filesystem paths |
| Network | Outbound access is enabled; exfiltration and access to reachable host/LAN services remain possible |
| CPU, memory, processes, and disk | No hardcoded limits; denial of service remains possible |
| Docker engine and kernel | Not mounted, but Docker/kernel/daemon vulnerabilities or unsafe daemon configuration can defeat containment |
| Managed extension APIs | Reachable from the workspace on each extension's control network |

Extension control-network separation prevents sidecars from sharing one common
network, but the workspace intentionally joins each enabled extension's network
so the TUI can call it. A compromised workspace can therefore invoke whatever
that extension advertises. Keep extension commands narrow and validate them in
the extension service.

Runtime extension grants do not govern image builds. Enabling a locally added
extension trusts its Dockerfile and build context. Review extension source
before building it, just as you would review another local Docker image.

Lisan does not collect API keys; agent CLIs own their authentication and files.
Use disposable or least-privileged credentials inside a hostile workspace. Do
not put host credentials, SSH agents, cloud sockets, or sensitive files in
`shared/`.

## Host hardening

Keep Docker Desktop/Engine and the host kernel current. On Linux, consider
[rootless mode](https://docs.docker.com/engine/security/rootless/uid-gid-mapping/)
or [user-namespace remapping](https://docs.docker.com/engine/security/userns-remap/).
Docker Desktop users on supported managed configurations can also evaluate
[Enhanced Container Isolation](https://docs.docker.com/security/faqs/containers/).
Do not expose an unauthenticated Docker API over TCP.

`vm` is **Wormsign** mode. Despite its name, it is not a virtual machine:
editors, packages, agents, commands, and native extensions run directly as the
current host user. None of the Docker guarantees above apply.

## Lifecycle and deletion

Switching TUI tabs preserves child sessions. Closing the final Docker cockpit
stops the workspace and managed extensions so idle background processes do not
continue indefinitely; named volumes and container state persist for the next
launch. `lisan cleanup` removes Lisan-owned containers, images, networks, and
volumes, including the Usul home volume. It deliberately preserves the host
`shared/` directory.

## Reporting

When the project is hosted publicly, report vulnerabilities through the host's
private security-advisory feature. Include the affected mode, platform,
configuration, reproduction steps, and expected impact. Avoid publishing an
exploit or credential material before a fix is available.
