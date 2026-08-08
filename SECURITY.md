# Security

## Runtime boundary

Docker mode is LisanAlGaib's recommended boundary for untrusted repositories,
package installers, editor plugins, and coding agents. The supplied workspace
does not mount the host Docker socket or a host project directory, and the
bundled extension has no published port. The persistent `arrakis-usul` volume is
still trusted data: code running in the workspace can read and modify anything
stored there.

`vm` deliberately enters **Wormsign** mode and removes that boundary.
Every editor plugin, package,
agent, shell command, and managed native extension runs with the current host
user's permissions. Review the active profile and extension manifests before
using it.

The `fremen` container account has passwordless `sudo`; root and fremen password
logins are locked. Privilege separation inside the workspace container is not
treated as a security boundary.

## Secrets

Lisan does not collect API keys. Agent CLIs own their authentication flows and
credential files. Do not commit generated configuration, home-volume data,
environment files, logs, or agent credentials to this repository.

## Reporting

When the project is hosted publicly, report vulnerabilities through the host's
private security-advisory feature. Include the affected mode, platform,
configuration, reproduction steps, and expected impact. Avoid publishing an
exploit or credential material before a fix is available.
