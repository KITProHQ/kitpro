# KITPro Server Video Baseline

> **VIDEO PRODUCTION BASELINE**

Freeze date: 2026-09-15

Factual claims in the KeepItTechie KITPro Server launch video must be checked against this baseline and the linked current-state brief. This file freezes the product state that existed before the documentation-only freeze commit; the commit containing this file is the authoritative documentation snapshot.

## Identity

| Item | Frozen value |
| --- | --- |
| Product release | `v0.1.0-alpha.11` (public prerelease) |
| Private KITPro product-source checkpoint | `12833cff02048e5b94900352b08d8aafc182114c` |
| Public clean-history checkpoint | `d1d0763f846301b7eb27f5bff866d4cff4334d9b` |
| KITPro site | `f15b706aaac09910da38990f6fcb2a07d1a07065` |
| KITPro OS site | `f3eeb26bc2a0e984c539fc485c0f078bd0900e42` |
| Demo automation | `b8831fe5960b75e2f9a5fbb6e13957f80a73e815` |
| Canonical product URL | <https://kitpro.us/server> |
| Public source | <https://github.com/KITProHQ/kitpro> |
| Release | <https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11> |

## Published package identity

| Artifact | Size | SHA-256 |
| --- | ---: | --- |
| `kitpro-server_0.1.0-alpha.11_amd64.deb` | 8,052,172 bytes | `0a832e5e9b8d55d0b21c8331b4a80b7caeba148627d387870ed50c2d6eda8399` |
| `kitpro-server-0.1.0_alpha11-1-x86_64.pkg.tar.zst` | 5,825,195 bytes | `4fa242cadaff977b4bbefbafae65852a7272ebab1ba3dfbf067f7290bfec0722` |
| `kitpro-server_0.1.0-alpha.11_amd64.cdx.json` | 13,788 bytes | `1414c192b9a56f80661b8ba5f58e7ee1fa4d4be1ae97d80d4854f88b4fcf5584` |
| `kitpro-server_0.1.0-alpha.11.build.json` | 1,172 bytes | `4d689d09fbe0a22ebb5cfcaecb8e8598468d18282b02c44cd2ccfd009be4cc2e` |
| `kitpro-server_0.1.0-alpha.11.release.json` | 1,320 bytes | `70b4a37acf6eeb00303a47d2dcd3910bd515207d0cfe301ae06c05a35d8d89c8` |

The signed checksum list was verified against every remote asset. `SHA256SUMS` itself is 544 bytes with SHA-256 `8c56242944f453e23ad80a35b81460af4d9fc942b7616216e26f4433cbb0ca19`.

## Final catalog

FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Paperless-ngx, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo.

Fourteen are single-container applications. Paperless-ngx is a multi-container application managed as one logical installation.

## Supported platforms

- Debian 13 amd64: supported with rootful Docker and enforcing AppArmor.
- Ubuntu 26.04 LTS amd64: supported with rootful Docker and enforcing AppArmor.
- Arch Linux x86_64: supported when fully updated from official repositories with `linux-lts`, rootful Docker, and enforcing AppArmor.
- Rocky Linux 10 amd64: experimental, not certified.

## GPU support summary

Ollama supports CPU mode and optional NVIDIA acceleration. NVIDIA inference is live-certified with an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0 on the three supported platforms. AMD and Intel accelerated workloads, Jellyfin NVIDIA transcoding, multi-GPU selection, GPU scheduling, and VRAM reservation are not certified or implemented.

## Storage/media summary

KITPro provides installation-owned managed storage and administrator-approved trusted roots. Jellyfin, Navidrome, and Audiobookshelf use imported roots read-only; SFTPGo uses an exclusive read-write root. Existing host-mounted NFS/CIFS storage can be detected, but KITPro does not mount shares or manage NAS credentials. Imported external data is not included in control-plane backups.

## Authoritative production inputs

- Product brief: [`docs/product/kitpro-server-current-state.md`](kitpro-server-current-state.md)
- B-roll inventory: [`docs/product/video-broll-inventory.md`](video-broll-inventory.md)
- Platform support: [`docs/support-matrix.md`](../support-matrix.md)
- Known limitations: [`docs/release/known-limitations.md`](../release/known-limitations.md)
- Development freeze: [`docs/product/launch-video-development-freeze.md`](launch-video-development-freeze.md)

## Known limitations

The video must not imply support for AMD/Intel accelerated workloads, Jellyfin NVIDIA transcoding, multiple GPUs, VRAM scheduling, arbitrary devices, arbitrary host binds, NAS mount management, imported-data backup, cross-installation discovery, Immich, Syncthing, clustering, high availability, automatic failover, or complete disaster recovery.

If any allowed critical fix changes a factual statement during video production, update this baseline before recording or publishing affected narration.
