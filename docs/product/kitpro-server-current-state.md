# KITPro Server Current State

Status date: 2026-09-15. This is the primary factual source for the KITPro Server launch-video script. It describes released behavior and validated boundaries, not roadmap intent.

## What KITPro Server is

KITPro Server is a local-first web control panel that installs and operates a trusted catalog of self-hosted applications on a supported Linux server. It keeps Docker, device, network, and storage operations behind a narrowly privileged helper while giving the owner a clear browser interface for normal administration.

## The problem it solves

Self-hosting with standard Linux and Docker tooling can require a user to understand images, volumes, ports, credentials, updates, permissions, devices, and recovery before the first useful application is available. KITPro does not replace or criticize those tools. It keeps standard Linux and rootful Docker underneath, then abstracts their repetitive infrastructure details into reviewed, bounded workflows that are easier to understand and operate safely.

## Core product idea

- **Local-first:** administration runs on the user's server. No KITPro cloud account, telemetry service, or required cloud control plane is involved.
- **User-owned server and data:** the user controls the host and the application data. Imported data remains outside KITPro's deletion lifecycle.
- **Browser management:** first-run setup, catalog browsing, installation, access, updates, storage, and hardware status are available in the local web interface.
- **Trusted catalog:** KITPro accepts reviewed, schema-versioned application definitions with immutable image digests. It is not a general Docker or Compose dashboard.
- **Explicit boundaries:** network exposure, devices, storage roots, and privileged operations are typed and independently revalidated.

## User journey

1. Install the native KITPro package for the server operating system.
2. Open the local KITPro address in a browser.
3. Create the first local administrator.
4. Browse the trusted application catalog.
5. Select and install an application.
6. Choose **Private**, **This server only**, or **Local network** access.
7. Use **Open App** when the selected access mode makes the service reachable.
8. Manage, update, stop, start, or recreate the application while preserving its installation identity and managed data.
9. Register and select an administrator-approved trusted storage root when an application requires imported data.
10. Use CPU mode or an approved GPU mode for supported workloads and hardware.

## Current catalog

The public alpha contains 15 applications:

- **FreshRSS 1.29.1:** a self-hosted RSS reader with managed data and extensions.
- **Uptime Kuma 2.3.1:** a service-monitoring dashboard with managed persistent data.
- **Mealie 3.24.0:** a recipe manager whose browser signup is disabled by default.
- **Memos 0.30.0:** a lightweight notes and capture application.
- **Actual Budget 26.9.0:** a local-first personal budgeting application.
- **Vaultwarden 1.37.2:** a password-vault server; public TLS remains an administrator responsibility.
- **Home Assistant stable:** a home-automation dashboard without arbitrary device or host-network access.
- **Paperless-ngx 2.20.15 with Redis 7.4.11:** a document archive presented as one logical application.
- **Open WebUI 0.11.3:** an authenticated AI interface whose model backend is configured separately.
- **IT-Tools 2024.10.22-7ca5933:** a stateless collection of browser-based technical utilities.
- **Ollama 0.34.0:** a local model runtime with CPU mode and optional certified NVIDIA acceleration.
- **Jellyfin 12.1:** a media server with managed config/cache and one read-only trusted media root.
- **Navidrome 0.64.0:** a music server with a managed database and read-only trusted music library.
- **Audiobookshelf 2.36.0:** an audiobook server with managed metadata and a read-only trusted library.
- **SFTPGo 2.7.5:** scoped file access backed by an exclusive trusted read-write root; only its web UI is exposable through KITPro.

Exact images, digests, ports, storage, and upstream licenses are recorded in the [application catalog](../application-catalog.md).

## Multi-container support

Paperless-ngx is the current multi-container application. KITPro owns its web component and internal Redis broker as one installation on one isolated network. The user installs and manages Paperless-ngx as a single logical app; only the web component can receive host exposure. Other catalog entries are single-container applications.

## AI/GPU

Open WebUI and Ollama are independent catalog applications. Open WebUI starts with authentication enabled and can be configured with a reachable model backend; KITPro does not automatically connect separate installations. Ollama works in CPU mode without a GPU.

NVIDIA Ollama inference is live-certified on Debian 13, Ubuntu 26.04 LTS, and Arch Linux under the documented host boundaries using an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0. The host still needs a compatible NVIDIA driver and toolkit. KITPro accepts one unambiguous GPU; it does not provide multi-GPU selection, GPU scheduling, or VRAM reservation. AMD and Intel device classes exist, but accelerated workloads are not live-certified. Jellyfin can request optional NVIDIA access, but NVENC/NVDEC transcoding is not certified.

## Storage/media

KITPro-managed storage lives under installation-owned directories and survives container recreation. For external data, an administrator registers a trusted local directory or an already-mounted network-storage directory. Applications request named read-only or read-write slots; users never enter arbitrary Docker bind syntax.

Jellyfin, Navidrome, and Audiobookshelf use imported data read-only. SFTPGo is the current exclusive read-write consumer. The helper checks canonical paths, symlinks, forbidden host areas, filesystem and mount identity, declared access mode, and writer conflicts. Existing host-mounted NFS and CIFS filesystems can be detected and registered, but KITPro does not mount shares, store NAS credentials, or manage the NAS lifecycle. Imported external data is not included in KITPro control-plane backups.

## Security model

- The browser UI and API run without Docker or broad host privileges.
- A separate privileged helper accepts only typed operations and independently validates the exact plan.
- The helper is confined by AppArmor on supported hosts.
- Trusted manifests use immutable image digests and reject arbitrary Compose, shell commands, capabilities, privileged mode, host networking, devices, raw bind mounts, and host-port requests.
- Application secrets are generated and preserved without being displayed through the normal API or interface.
- Services begin **Private** and may be changed only to **This server only** or one configured **Local network** address. KITPro does not provide wildcard or automatic Internet exposure.
- Hardware access uses approved device classes; arbitrary device passthrough is unavailable.
- Storage uses administrator-approved trusted roots; arbitrary host mounts are unavailable.
- Reconciliation compares recorded intent with Docker, storage, hardware, and network state. Security-sensitive drift fails closed.

## Updates/backups

The host's native package manager remains authoritative for KITPro package updates. Application updates are administrator-initiated and are available only when the trusted catalog defines a reviewed release transition. Before package migrations and application updates, KITPro creates a bounded backup of its control-plane SQLite state.

That backup is not a complete application-data or host disaster-recovery system. Imported external data is not copied, irreversible upstream schema rollback is not promised, and complete host-to-host recovery remains administrator work.

## Supported platforms

| Platform | Status | Exact boundary |
| --- | --- | --- |
| Debian 13 amd64 | Supported | Rootful Docker and enforcing AppArmor |
| Ubuntu 26.04 LTS amd64 | Supported | Rootful Docker and enforcing AppArmor |
| Arch Linux x86_64 | Supported | Fully updated official repositories, `linux-lts`, rootful Docker, and enforcing AppArmor; no partial upgrades |
| Rocky Linux 10 amd64 | Experimental | Not certified; SELinux and Docker integration require further validation |

The [support matrix](../support-matrix.md) is definitive for platform-specific capability certification.

## Package formats

- Debian and Ubuntu: `.deb`
- Arch Linux: `.pkg.tar.zst`

Published releases also include SHA-256 checksums, build metadata, a release manifest, and a CycloneDX JSON SBOM.

## Licensing

KITPro is licensed under the Apache License 2.0. Catalog applications retain their own upstream licenses.

## Current release

The current public release is [`v0.1.0-alpha.11`](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11), published as a GitHub prerelease from source commit `1eed6b0a2199e927a9647e50206e89f6cfc19995`.

## Known limitations

- AMD and Intel accelerated workloads are not live-certified.
- Jellyfin NVIDIA transcoding is not certified.
- There is no multi-GPU selection or scheduling and no VRAM reservation.
- Arbitrary device passthrough, host bind mounts, Compose input, and host networking are unavailable.
- KITPro does not mount NFS/CIFS shares or manage NAS credentials.
- Imported external data is not automatically backed up.
- Cross-installation service discovery is unavailable; Open WebUI and Ollama remain separately configured.
- Immich is unsupported because its current topology requires trusted primitives for shared secrets, health-gated dependencies, bounded database shared memory, and database-aware backup/rollback that KITPro does not yet provide.
- Syncthing is unsupported because its LAN/UDP discovery topology conflicts with KITPro's current bridge-network and no-host-networking boundary.
- Clustering, high availability, automatic failover, and complete disaster recovery are unavailable.
- Public TLS, domains, and reverse-proxy automation are outside the current product boundary.

The public [known limitations](../release/known-limitations.md) document is authoritative if a shorter product summary differs.

## Why KITPro matters

KITPro makes personal infrastructure more approachable without taking ownership away from the user. It combines local-first administration, open-source software, explicit privacy and security boundaries, and standard Linux/Docker foundations so people can learn, self-host, and control more of their digital lives without first becoming infrastructure experts.
