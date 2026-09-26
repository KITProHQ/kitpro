# KITPro Server current state

Status date: 2026-09-26

This document defines the public capability boundary for KITPro Server
`v0.1.0-alpha.13`. Alpha.13 is a release candidate until a human authorizes
the tag and publication. Shorter pages should link here instead of creating a
second definition of what the release can do.

## What alpha.13 is

KITPro Server is an open-source platform for operating a trusted catalog of
self-hosted applications on a Linux server. It coordinates runtime, storage,
updates, backup, restore, and recovery through a local browser interface and a
narrowly privileged helper.

The Linux host, application workloads, and persistent data remain under the
owner's control. KITPro uses standard Linux, systemd, and container-runtime
foundations that an operator can inspect. It is not a generic Docker or
Compose administration interface.

Alpha.13 is active alpha software. Breaking changes and incomplete workflows
remain possible. Read [what alpha.13 does not yet provide](#what-alpha13-does-not-yet-provide)
before using it with important data.

## What works today

### Application lifecycle

An administrator can use the local browser interface to install a trusted
application, start or stop its runtime, recreate the runtime, change declared
service exposure, and apply a trusted catalog update. KITPro supports both
single-component applications and one logical application made from multiple
components.

Catalog definitions pin images by digest and describe allowed storage,
services, secrets, dependencies, and optional devices. KITPro rejects
arbitrary Compose files, Docker options, host networking, privileged
containers, raw bind mounts, and arbitrary device access.

Alpha.13 has exactly 20 visible applications. The original 15 are FreshRSS,
Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant,
Paperless-ngx, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome,
Audiobookshelf, and SFTPGo. Alpha.13 adds Forgejo, Plex, Nextcloud, Pi-hole,
and Syncthing.

Forgejo has no SSH exposure. Plex is CPU-only and does not configure automatic
remote access. Nextcloud is an Experimental SQLite small-instance profile.
Pi-hole is an Experimental DNS-only Network Service. Syncthing is an
Experimental manual-peer TCP-only profile with discovery, relays, NAT
traversal, QUIC, and UDP disabled.

Removing an application runtime preserves its installation identity and data.
Destructive application-data deletion is not a completed lifecycle.

### Runtime and reconciliation

KITPro records desired state, observed runtime state, and reconciliation state
separately. Alpha.12 can report whether a runtime is running, stopped, missing,
removed, or unavailable. It can also report whether recorded ownership and
runtime evidence are consistent, repairable, unknown, or action-required.

A running runtime is not proof that the application is ready. Alpha.12 has no
application-aware readiness contract. A configured endpoint means that KITPro
assigned exposure for a declared service. It does not prove that the
application behind that endpoint is healthy.

Lifecycle mutations have durable operation IDs. The helper binds each ID to a
canonical request hash, serializes mutations per installation, and rejects a
conflicting replay. If an API response is lost after submission, the API looks
up the existing helper operation instead of authorizing duplicate privileged
work.

Reconciliation can recommend a bounded repair based on fresh evidence. It can
start the exact active generation, recreate a missing generation from trusted
state, clean exact non-active runtime resources, or acknowledge that a retained
generation is already gone. KITPro stops with action required when ownership or
restore state cannot be resolved safely.

Read [lifecycle recovery](../operations/lifecycle-recovery.md) for states,
repair actions, and escalation boundaries.

### Storage

Each installation has durable identity. KITPro-managed storage belongs to that
installation and survives runtime replacement and runtime removal. The helper
verifies storage paths and ownership before it changes a runtime generation.

An administrator can register a local directory or an already-mounted NFS or
CIFS directory as trusted imported storage. Applications request named,
bounded slots. Imported storage remains outside KITPro's deletion and backup
lifecycle.

Generated application secrets remain stable across recreate, reboot, backup,
and restore. Normal catalog, installation, operation, and HTML responses do
not contain their values. A manifest-authorized credential reveal requires an
authenticated session, CSRF, and valid Host and Origin headers, and returns
`Cache-Control: no-store`.

Generated secrets are root-bound plaintext in helper state and restrictive
local backups. KITPro does not claim encryption at rest. Host root remains
trusted.

### Backup and restore

Alpha.13 can create a cold backup of declared managed application storage for
an existing installation. The backup records the installation, release,
runtime generation, component images, storage topology, generated secrets,
checksums, and detected SQLite databases.

Restore is deliberately narrow. It targets the same installation identity,
release, runtime generation, component images, managed-storage topology, and
imported-storage bindings. The helper journals filesystem swaps before
cutover. After interruption, it either proves a forward state, restores the
prior tree, records cleanup debt, or stops with action required.

Application backup does not copy imported storage and is not complete disaster
recovery. Read [application backup and restore](../operations/application-backup-restore.md)
before depending on it.

### Upgrades

Fresh alpha.13 installations use the normal supported package path for Debian,
Ubuntu, or Arch Linux.

The Debian alpha.12 to alpha.13 transition uses the incoming package preflight.
It verifies both state databases, creates a paired backup set, binds approval
to the incoming package, and restores previously active services when
preflight fails. Arch uses the package-bound upgrade wrapper and pacman
pre-transaction hook.

The supported transition migrates alpha.12 schema 13 databases to alpha.13
schema 14 while preserving receipts and ownership evidence.
Package downgrade after migration is unsupported.

### Release integrity and provenance

The alpha.13 release set includes SHA-256 checksums, a CycloneDX JSON SBOM, build
metadata, a release manifest, and a frozen source revision. These artifacts let
an owner verify downloaded bytes, inspect the packaged component inventory,
trace the release to its reviewed source state, and distinguish the frozen
release files from unverified replacements.

These artifacts do not claim that every build is reproducible or provide a
formal supply-chain guarantee.

### Platform support

| Category | Platform | Alpha.13 boundary |
| --- | --- | --- |
| Supported | Debian 13 amd64 | Rootful Docker, systemd, and enforcing AppArmor |
| Supported | Ubuntu 26.04 LTS amd64 | Same qualified `.deb`, rootful Docker, systemd, and enforcing AppArmor |
| Supported | Arch Linux x86_64 | Fully updated official repositories, `linux-lts`, rootful Docker, systemd, and enforcing AppArmor |
| Experimental | Rocky Linux 10 amd64 | Native RPM, Podman 5, Quadlet, crun, systemd, firewalld, and SELinux Enforcing; application lifecycle unavailable in alpha.13 |
| Experimental | Podman | Used only by the Experimental Rocky path; the staged-generation lifecycle contract is not implemented |

The [support matrix](../support-matrix.md) defines the platform-specific test
and support boundary.

Every Supported Docker host also requires an operator-selected,
non-overlapping default address pool. KITPro validates current pool capacity,
routes, and Docker networks. It does not edit Docker configuration or promise
future collision detection.

## What alpha.13 does not yet provide

Alpha.13 does not provide:

- application-aware readiness or health checks;
- imported-storage backup;
- host-to-host restore;
- bare-host recovery;
- automatic rollback of irreversible upstream application schema changes;
- automatic resolution of ambiguous ownership or mixed restore state;
- a complete browser interface for every reconciliation, repair, backup,
  restore, and recovery workflow;
- destructive deletion of application data;
- clustering, high availability, or automatic failover;
- public TLS, domain, reverse-proxy, Certbot or ACME, or Cloudflare Tunnel automation;
- generic Docker or Compose administration;
- arbitrary host paths, devices, capabilities, or network modes;
- automatic mounting of NFS or CIFS shares; or
- a Supported Rocky Linux or Podman application lifecycle.

The [known limitations](../release/known-limitations.md) adds application,
hardware, and network-specific details.

## What is directional rather than shipped

The architecture leaves room for application-aware readiness, broader backup
and recovery, more complete browser recovery workflows, explicit data
deletion, public TLS, additional supported platforms, and optional remote
services. None of those directions changes the alpha.13 capability boundary.

Read the [roadmap](../roadmap.md) for current priorities. Read the
[architecture](../architecture.md) and [principles](../principles.md) for the
design constraints that future work must preserve.
