# Current development state

KITPro Server is a public alpha. The published alpha.13 release is the upgrade
baseline for the local alpha.14 development branch. Alpha.14 adds opt-in
managed nginx ingress, provided and ACME certificates, optional Cloudflare
Tunnel, durable first-run state, typed system readiness, host-state backup and
restore semantics, administrator recovery, and redacted support bundles.

Debian 13, Ubuntu 26.04 LTS, and Arch Linux are Supported. Rocky Linux 10 and
Podman remain Experimental. The Rocky package and host boundary passes, but
alpha.13 application installation is unavailable because Podman does not
implement the staged-generation lifecycle contract.

The current published upgrade baseline is
[`v0.1.0-alpha.13`](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.13).
Published release metadata and checksums remain authoritative for downloaded
packages. Alpha.14 has not been pushed, tagged, published, or deployed.

Read the historical [alpha.13 current-state
reference](../product/kitpro-server-current-state.md), the [support
matrix](../support-matrix.md), the [alpha.14 known
limitations](known-limitations.md), the [alpha.14 release notes
draft](0.1.0-alpha.14-release-notes.md), and the [alpha.13 upgrade
guide](upgrade-alpha13-to-alpha14.md). Alpha.14 remains blocked until live
platform, security-enforcement, reboot, package-upgrade, and final-artifact
qualification pass.
