# Launch-video baseline freeze — 2026-09-15

## Decision

`KITPRO LAUNCH-VIDEO BASELINE FREEZE: PASS`

KITPro Server `v0.1.0-alpha.11` is the authoritative product release for the KeepItTechie launch-video cycle. No replacement software release is required. Product development is temporarily frozen under [`docs/product/launch-video-development-freeze.md`](../../product/launch-video-development-freeze.md).

## Repository checkpoints

| Repository | Audited product checkpoint | Final state for this freeze |
| --- | --- | --- |
| Private KITPro | `12833cff02048e5b94900352b08d8aafc182114c` | Product code unchanged; this evidence and synchronized docs are committed by the freeze commit that contains this file |
| Public KITPro clean history | `d1d0763f846301b7eb27f5bff866d4cff4334d9b` | Public-safe documentation is synchronized in a separate clean-history commit |
| `kitpro-site` | `f15b706aaac09910da38990f6fcb2a07d1a07065` | Factual screenshot/alt-text correction deployed |
| `kitpro-os-site` | `40b6b36495c285b7ec7a5d3faeb28c4b8cd040a6` | No change required |
| Demo automation | `e3af564b48382fdf479012844a87a370b1959811` | Reusable VM/SSH recorder and screenshot correction pushed |

The private and public KITPro repositories intentionally use separate histories. The audited private and public checkpoints were clean and aligned with their intended remotes before this documentation freeze. Site and automation commits were pushed without force.

## Public release identity

- Tag: `v0.1.0-alpha.11`
- Release: <https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11>
- Status: published GitHub prerelease; not a draft
- Annotated tag object: `9a10124b245ba3fa96344644e6ce6c93bbdde864`
- Tag/source commit: `1eed6b0a2199e927a9647e50206e89f6cfc19995`
- Debian package: `kitpro-server_0.1.0-alpha.11_amd64.deb`, 8,052,172 bytes, SHA-256 `0a832e5e9b8d55d0b21c8331b4a80b7caeba148627d387870ed50c2d6eda8399`
- Arch package: `kitpro-server-0.1.0_alpha11-1-x86_64.pkg.tar.zst`, 5,825,195 bytes, SHA-256 `4fa242cadaff977b4bbefbafae65852a7272ebab1ba3dfbf067f7290bfec0722`
- CycloneDX SBOM: `kitpro-server_0.1.0-alpha.11_amd64.cdx.json`, 13,788 bytes, SHA-256 `1414c192b9a56f80661b8ba5f58e7ee1fa4d4be1ae97d80d4854f88b4fcf5584`
- Build metadata SHA-256: `4d689d09fbe0a22ebb5cfcaecb8e8598468d18282b02c44cd2ccfd009be4cc2e`
- Release manifest SHA-256: `70b4a37acf6eeb00303a47d2dcd3910bd515207d0cfe301ae06c05a35d8d89c8`
- `SHA256SUMS` SHA-256: `8c56242944f453e23ad80a35b81460af4d9fc942b7616216e26f4433cbb0ca19`

Every downloaded remote asset matched the published `SHA256SUMS`. Build metadata reports a clean source tree, Debian package version `0.1.0~alpha11`, Arch package version `0.1.0_alpha11-1`, CGO disabled, and the recorded reproducible build flags. The release was not rebuilt.

## Product inventory

The catalog contains 15 applications: FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Paperless-ngx, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo. Fourteen are single-container. Paperless-ngx is managed as one logical multi-container application with an internal Redis broker.

The supported host boundary is Debian 13 amd64, Ubuntu 26.04 LTS amd64, and fully updated Arch Linux x86_64 with `linux-lts`, rootful Docker, and enforcing AppArmor. Rocky Linux 10 amd64 remains experimental.

Ollama CPU mode and NVIDIA inference are certified within the documented boundary. NVIDIA validation used an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0. AMD/Intel accelerated workloads, Jellyfin NVIDIA transcoding, multiple-GPU selection, scheduling, and VRAM reservation are not claimed.

Trusted storage supports managed data, administrator-approved imported read-only roots, one exclusive imported read-write root, and detection of existing host-mounted NFS/CIFS storage. KITPro does not mount shares, manage NAS credentials, or back up imported data automatically.

## Documentation

- Primary factual brief: [`docs/product/kitpro-server-current-state.md`](../../product/kitpro-server-current-state.md)
- Visual plan: [`docs/product/video-broll-inventory.md`](../../product/video-broll-inventory.md)
- Immutable production record: [`docs/product/kitpro-server-video-baseline.md`](../../product/kitpro-server-video-baseline.md)
- Support matrix: [`docs/support-matrix.md`](../../support-matrix.md)
- Limitations: [`docs/release/known-limitations.md`](../../release/known-limitations.md)

The gstack document-generation workflow produced reference, explanation, and production how-to coverage in the existing documentation structure. The gstack release-documentation audit reconciled README, technical entry point, changelog, release state, manifests, support matrix, limitations, and discoverability. The installed gstack capability is a skill workflow, while `/usr/bin/gstack` on this host is an unrelated process-stack utility; the skill was therefore executed directly rather than through that conflicting binary. No `VERSION` bump or new software release was made, as explicitly required.

## Screenshot and automation audit

Dashboard, Catalog, Installed Application, Access Controls, Storage, Hardware/GPU, Updates, Mobile Dashboard, and a media-catalog view are current, 1440×900 except the 390×844 mobile capture. The hardware capture truthfully shows no detected accelerator and CPU availability.

The old file labeled as a media-app view actually showed IT-Tools and was rejected. It was replaced in active public use by a current catalog view that visibly shows Jellyfin and Navidrome trusted-storage choices. A true installed Jellyfin/Navidrome runtime shot remains a recommended production capture, not a prerequisite for factual site use. Historical NVIDIA screenshots may be used only with their certified-hardware context.

Demo automation now includes a reusable managed-VM SSH recorder, protected sudo-password loading, bounded cleanup, fail-fast media-catalog targeting, and tests. Validation result: 151 tests passed; scoped Ruff lint/format, shell syntax, Python compile, offline validation, whitespace, sensitive-data, and generated-artifact checks passed. The prior validation VM was unavailable during the final audit, so no product state was fabricated to replace it.

## Website and public GitHub status

- <https://kitpro.us/>: HTTP 200
- <https://kitpro.us/server>: HTTP 200, canonical product route, live browser/console PASS
- Primary `/os`, `/hardware`, `/cloud`, `/services`, and `/about` routes: HTTP 200
- Corrected public screenshot assets: HTTP 200; deployed media asset matched local SHA-256 `423bd5b0bced57d91c416ec55bbdaafe664d79e0ce5dea90a1fb7fbe83a4812a`
- <https://os.kitpro.us/>: HTTP 200 and independent
- <https://os.kitpro.us/server>: permanent 308 redirect to <https://kitpro.us/server>
- GitHub repository homepage: <https://kitpro.us/server>
- License: Apache-2.0
- `README.md`, `SECURITY.md`, `CONTRIBUTING.md`, topics, support matrix, release link, and current release references: present

The public release-note body is synchronized with absolute, tag-pinned documentation URLs. No new GitHub software release was created.

## Validation results

| Area | Result |
| --- | --- |
| `go test ./...` | PASS |
| `go vet ./...` | PASS |
| `gofmt` verification | PASS |
| Debian package static tests | PASS |
| Arch package static tests | PASS |
| Active documentation external links | PASS: 45 checked |
| Repository Markdown local links | PASS: 165 files, zero broken targets |
| Release JSON parsing | PASS |
| `kitpro-site` dependency audit | PASS: zero advisories |
| `kitpro-site` validation/build | PASS |
| `kitpro-site` local primary routes | PASS: HTTP 200 |
| `kitpro-site` production deployment | PASS |
| `kitpro-os-site` install/lint/build | PASS; four existing `<img>` optimization warnings, zero errors |
| Demo automation | PASS: 151 tests |
| `git diff --check` | PASS |

## Sensitive-data review

Current active docs, baseline files, changed website source, changed images, release metadata, and automation commit contain no credentials, cookies, session IDs, temporary passwords, private keys, live API tokens, user-specific SSH-key paths, or generated recordings. Screenshot PNGs were visually reviewed and binary-string scanned. Pre-existing dated validation evidence retains private RFC1918 test addresses and VM identifiers as intentional archival test context; no active onboarding, product, release, or video-production document exposes them.

## Remaining limitations

- AMD and Intel accelerated workloads are not live-certified.
- Jellyfin NVIDIA transcoding is not certified.
- No multi-GPU selection/scheduling or VRAM reservation.
- No arbitrary devices, host bind mounts, Compose input, or host networking.
- No NAS mount or credential management.
- No automatic backup of imported external data.
- No cross-installation trusted service discovery.
- Immich and Syncthing remain unsupported for the documented architecture reasons.
- No clustering, high availability, automatic failover, or complete disaster recovery.

## Freeze rules

Allowed during launch-video production: critical security fixes, release-breaking bug fixes, and factual documentation corrections. Deferred: new applications, runtime architecture, storage/network/device primitives, UI or website redesign, new hardware capabilities, disaster-recovery architecture, and other feature expansion.
