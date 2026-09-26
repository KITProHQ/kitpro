# Current development state

KITPro Server is a public alpha. The alpha.13 release candidate contains the
secure API/helper runtime, local-administrator authentication, a 20-application
immutable catalog, durable operations, single- and multi-component runtime
generations, reconciliation, repair, backup and restore, persistent storage,
generated secrets, typed application configuration, TCP and UDP exposure,
trusted updates, and native Debian, Arch, and Rocky packages.

Debian 13, Ubuntu 26.04 LTS, and Arch Linux are Supported. Rocky Linux 10 and
Podman remain Experimental. The Rocky package and host boundary passes, but
alpha.13 application installation is unavailable because Podman does not
implement the staged-generation lifecycle contract.

The current published version remains
[`v0.1.0-alpha.12`](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.12)
until a human authorizes alpha.13 publication. Published release metadata and
checksums remain authoritative for downloaded packages.

Read the [current-state reference](../product/kitpro-server-current-state.md),
[support matrix](../support-matrix.md), [known limitations](known-limitations.md),
the prepared [alpha.13 notes](0.1.0-alpha.13-release-notes.md), and the
[alpha.13 release-manifest template](0.1.0-alpha.13-release-manifest.template.json)
for the exact boundary. The template is completed outside the frozen source
tree after final artifacts exist.
