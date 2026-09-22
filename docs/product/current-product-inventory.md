# KITPro Server current product inventory

Status date: 2026-09-21. This page records alpha.12 behavior and validated boundaries, not roadmap intent.

## Product and distribution

| Item | Current state |
| --- | --- |
| Product | KITPro Server public alpha |
| License | Apache License 2.0 for KITPro source; catalog applications retain their upstream licenses |
| Current release | [`v0.1.0-alpha.12`](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.12), public alpha prerelease |
| Packages | Supported Debian `.deb`; supported Arch `.pkg.tar.zst`; CycloneDX JSON SBOM and SHA-256 checksums in the published release |
| Supported systems | Debian 13 amd64; Arch Linux x86_64 under the documented `linux-lts` boundary |
| Development / validation only | Ubuntu 26.04 LTS amd64 |
| Experimental | Rocky Linux 10 amd64 and Podman |
| Public source | <https://github.com/KITProHQ/kitpro> |
| Product page | <https://kitpro.us/server> |
| Releases | <https://github.com/KITProHQ/kitpro/releases> |

## Capabilities

- Fourteen single-container catalog applications and one multi-container application.
- Persistent KITPro-managed storage, generated application secrets, controlled service exposure, reconciliation, and administrator-initiated trusted updates.
- Control-state backup before package migrations and app updates. Alpha.12 adds versioned application archives for managed files, SQLite state, and generated secrets within the documented same-installation boundary.
- Typed NVIDIA device access with CPU fallback. Ollama inference is certified on an RTX A2000 12GB on Debian and Arch. Ubuntu results are development evidence. AMD and Intel classes exist but accelerated workloads are not live-certified.
- Administrator-approved trusted external roots with canonical-path, symlink, forbidden-path, mount-identity, access-mode, and writer-conflict enforcement.
- Existing host-mounted NFS/CIFS paths can be registered; KITPro does not mount or credential network shares.

## Catalog shape

Single-container: FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo.

Multi-container: Paperless-ngx with an internal Redis broker. Only its web component can receive host exposure.

GPU-aware: Ollama and Jellyfin declare optional NVIDIA access. Ollama GPU inference is certified. Jellyfin NVIDIA transcoding remains unvalidated and is not claimed.

Storage-heavy/media: Jellyfin, Navidrome, and Audiobookshelf use read-only imported libraries. SFTPGo uses one exclusive read-write root. Imported files are never deleted with an installation.

The definitive per-application detail is in the [application catalog](../application-catalog.md); platform-specific claims are in the [support matrix](../support-matrix.md).
