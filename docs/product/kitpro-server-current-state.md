# KITPro Server current-state product brief

## What KITPro Server is

KITPro Server is a local-first web control panel that installs and operates a trusted catalog of self-hosted applications on a supported Linux server while keeping privileged Docker, device, and storage operations behind a constrained helper.

## The problem it solves

Self-hosting normally asks a user to understand container images, persistent volumes, ports, credentials, updates, Linux permissions, device access, and failure recovery before the first useful app appears. A small configuration mistake can expose a service, lose data, or grant more host access than intended. KITPro turns reviewed deployment patterns into a guided local workflow.

## What KITPro does differently

- A trusted catalog uses official images pinned to immutable digests.
- Core administration is local-first and requires no KITPro cloud account or telemetry.
- Installation identity and managed data survive container replacement.
- Services begin private and can be exposed only on loopback or one configured LAN address.
- A narrowly privileged, AppArmor-confined helper revalidates every typed operation.
- One application may safely own several internal components without exposing their implementation to normal users.
- Trusted release updates preserve installation state and create bounded control-state backups.
- Hardware access is expressed as approved device classes, never arbitrary `/dev` paths.
- External storage comes from administrator-approved roots, never arbitrary bind-mount syntax.

## Current catalog

FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Paperless-ngx, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo. See the [catalog reference](../application-catalog.md) for versions, storage, ports, and limitations.

## Supported platforms

Debian 13 amd64 and Ubuntu 26.04 LTS amd64 are supported with rootful Docker and enforcing AppArmor. Arch Linux x86_64 is supported when fully updated from official repositories, running `linux-lts`, rootful Docker, and enforcing AppArmor. Rocky Linux 10 amd64 remains experimental. The [support matrix](../support-matrix.md) is authoritative.

## Security philosophy

KITPro deliberately refuses general Docker administration. Users cannot submit Compose files, shell commands, raw capabilities, host networking, arbitrary devices, or arbitrary host mounts. Trusted manifests describe a bounded intention; the helper independently resolves and checks the exact runtime plan. Security-sensitive drift fails closed.

## GPU and AI capabilities

Ollama runs on CPU or can use the one unambiguous NVIDIA GPU detected through the NVIDIA Container Toolkit. Real inference on an RTX A2000 12GB passed on Debian, Ubuntu, and Arch. Open WebUI is a separate authenticated frontend and can be configured with a reachable model backend after installation; KITPro does not yet create a cross-installation connection automatically. AMD and Intel device classes exist, but accelerated workloads are not certified. KITPro does not schedule GPU workloads or reserve VRAM.

## Media and storage capabilities

An administrator registers a trusted local or already-mounted network-storage root. A catalog app requests a named read-only or read-write slot; the user selects a compatible root. Jellyfin, Navidrome, and Audiobookshelf read media without modifying the library. SFTPGo is the first exclusive read-write consumer. KITPro checks canonical paths, symlinks, forbidden host areas, mount identity, access mode, and writer conflicts. It detects host-mounted NFS/CIFS filesystems but does not mount shares or manage their credentials. Jellyfin NVIDIA transcoding is not yet certified.

## Package and install experience

Debian and Ubuntu use a signed/checksummed `.deb`; Arch uses a native `.pkg.tar.zst`. After package installation, the user opens the local dashboard in a browser and creates the first administrator. Rootful Docker and AppArmor remain visible prerequisites rather than hidden vendor services.

## What a user actually does

1. Install the package for the server operating system.
2. Open the local KITPro address in a browser.
3. Create the first administrator.
4. Pick a reviewed application.
5. Select approved storage when the app needs it.
6. Install it, choose Private, This server only, or Local network access, and open the app.

## What KITPro does behind the scenes

KITPro validates a trusted manifest, prepares managed directories and durable secrets, asks the helper to revalidate the exact plan, pulls the pinned image, creates an isolated network and runtime, and records what it owns. Reconciliation later compares that record with Docker, hardware, and storage state so missing or extra privileges do not pass unnoticed.

## What is not supported yet

KITPro does not provide arbitrary Compose, arbitrary devices or bind mounts, host networking, public TLS/reverse-proxy automation, multi-GPU selection, GPU scheduling, NAS mounting, cross-installation service discovery, clustering, high availability, or complete application-data disaster recovery. AMD/Intel workloads and Jellyfin NVIDIA transcoding are not certified. Immich, Syncthing, Frigate, ComfyUI, and other deferred candidates are not in the catalog. See [known limitations](../release/known-limitations.md).

## Why this matters

Useful personal infrastructure should not require surrendering ownership to a hosted platform or granting a dashboard unrestricted root access. KITPro aims for a practical middle ground: the privacy and durability of self-hosting, with a comprehensible interface and explicit security boundaries that ordinary users can reason about.
