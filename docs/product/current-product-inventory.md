# KITPro Server current product inventory

Status date: 2026-09-15. This page records shipped source behavior and validated boundaries, not roadmap intent.

## Product and distribution

| Item | Current state |
| --- | --- |
| Product | KITPro Server public alpha |
| License | Apache License 2.0 for KITPro source; catalog applications retain their upstream licenses |
| Current release | [`v0.1.0-alpha.11`](https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11), published prerelease |
| Packages | Debian/Ubuntu `.deb`; Arch `.pkg.tar.zst`; CycloneDX JSON SBOM and SHA-256 checksums in the published release |
| Supported systems | Debian 13 amd64; Ubuntu 26.04 LTS amd64; Arch Linux x86_64 under the documented `linux-lts` boundary |
| Experimental | Rocky Linux 10 amd64 |
| Public source | <https://github.com/KITProHQ/kitpro> |
| Product page | <https://kitpro.us/server> |
| Releases | <https://github.com/KITProHQ/kitpro/releases> |

## Capabilities

- Fourteen single-container catalog applications and one multi-container application.
- Persistent KITPro-managed storage, generated application secrets, controlled service exposure, reconciliation, and administrator-initiated trusted updates.
- Control-state backup before package migrations and app updates. Development source adds versioned application archives for managed files, SQLite state, and generated secrets. Cross-platform live acceptance is pending.
- Typed NVIDIA device access with CPU fallback. Ollama inference is certified on an RTX A2000 12GB across Debian, Ubuntu, and Arch. AMD and Intel classes exist but accelerated workloads are not live-certified.
- Administrator-approved trusted external roots with canonical-path, symlink, forbidden-path, mount-identity, access-mode, and writer-conflict enforcement.
- Existing host-mounted NFS/CIFS paths can be registered; KITPro does not mount or credential network shares.

## Catalog shape

Single-container: FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo.

Multi-container: Paperless-ngx with an internal Redis broker. Only its web component can receive host exposure.

GPU-aware: Ollama and Jellyfin declare optional NVIDIA access. Ollama GPU inference is certified. Jellyfin NVIDIA transcoding remains unvalidated and is not claimed.

Storage-heavy/media: Jellyfin, Navidrome, and Audiobookshelf use read-only imported libraries. SFTPGo uses one exclusive read-write root. Imported files are never deleted with an installation.

The definitive per-application detail is in the [application catalog](../application-catalog.md); platform-specific claims are in the [support matrix](../support-matrix.md).
