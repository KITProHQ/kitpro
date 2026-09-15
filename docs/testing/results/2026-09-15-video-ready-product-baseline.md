# KITPro video-ready product baseline

Date: 2026-09-15

## Scope and outcome

This milestone freezes presentation and documentation around the already-validated product. It adds no runtime architecture and no catalog application. The source baseline contains 15 catalog apps: fourteen single-container applications and Paperless-ngx as the one multi-container application.

## Final catalog and support

FreshRSS, Uptime Kuma, Mealie, Memos, Actual Budget, Vaultwarden, Home Assistant, Paperless-ngx, Open WebUI, IT-Tools, Ollama, Jellyfin, Navidrome, Audiobookshelf, and SFTPGo. Exact versions, digests, licenses, storage, services, and limitations are in `docs/application-catalog.md`.

- Supported: Debian 13 amd64, Ubuntu 26.04 LTS amd64, Arch Linux x86_64 under the fully updated `linux-lts`/rootful-Docker/enforcing-AppArmor boundary.
- Experimental: Rocky Linux 10 amd64.
- NVIDIA: Ollama inference certified using an RTX A2000 12GB and NVIDIA Container Toolkit 1.20.0 across all three supported platforms.
- AMD/Intel: typed/scoped device support exists; accelerated workloads are not live-certified.
- Storage: local trusted roots and read-only/exclusive-read-write bindings validated. NFS/CIFS filesystem detection exists, but the live disposable NAS failure drill remains unavailable.
- Jellyfin: read-only media validated; NVIDIA transcoding is not certified.

## Documentation and screenshots

The root README, technical Server README, quickstart, platform matrix, catalog, architecture, security boundary, limitations, changelog, current-development note, product inventory, product brief, and B-roll inventory were reconciled. Historical evidence remains historical and is not rewritten as a current claim.

Reusable Chromium automation captured real authenticated product state on Debian VM 500 at 1440×900 plus a 390×844 mobile dashboard. Legacy/deferred rows were removed only from the disposable screenshot database; the original control/helper databases were backed up for restoration. Captures contain no credentials or raw installation IDs, and advanced host paths are closed.

Canonical images cover Dashboard, Catalog, Installed application, Access controls, Trusted storage, Hardware, Media, Updates, and Mobile. The capture VM did not boot with the passthrough GPU attached, so the hardware image truthfully shows CPU availability and no detected accelerator. It is not presented as NVIDIA certification evidence; that remains in the dated GPU architecture and expansion results.

## Website and public source

The Server product page was refreshed without a redesign. It names the 15-app catalog, exact platform boundary, trusted storage, Ollama/NVIDIA certification, and unvalidated GPU/media limits. Download actions lead to the releases index rather than an obsolete hard-coded alpha tag. The clean public repository receives the same factual README, docs, and canonical screenshots without private Forgejo history.

Website commit `ab71c94ed24ff0ae52bea61de3952ac3ecbe91e5` was pushed to its private Forgejo remote, deployed to `/var/www/kitpro-os-site`, rebuilt, restarted through PM2, and verified at `https://os.kitpro.us/server` with HTTP 200 and the expected catalog, storage, NVIDIA, and Arch boundary text. Reusable automation commit `f04b24432813fdf326a83c7c0902629393219747` was pushed separately without staging the user's pre-existing automation edits.

Public GitHub metadata now uses `https://os.kitpro.us/server` as its homepage, a current local-first trusted-app/AI/media description, and the focused topics `self-hosted`, `homelab`, `linux`, `home-server`, `docker`, `privacy`, `local-first`, `ai`, `ollama`, `media-server`, `debian`, `ubuntu`, and `arch-linux`.

The website's locked Next.js 15.3.2 dependency set reported 13 known npm
advisories, including two critical. The site was upgraded to Next.js 16.3.5,
React 19.2.1, and the supported flat ESLint configuration. The final npm audit
reports zero vulnerabilities and the production build passes.

## Package and release status

No package was rebuilt and no release was published because this milestone changes documentation, presentation, and reusable screenshot automation only. Prepared alpha.11 artifacts remain:

| Artifact | SHA-256 |
| --- | --- |
| `kitpro-server_0.1.0~alpha11_amd64.deb` | `9af3b958996e9f1bcffee91879e837c02c651d4d02b738cba8c851a124c5cda2` |
| `kitpro-server-0.1.0_alpha11-1-x86_64.pkg.tar.zst` | `1c020fa7d81ce114a3c712be7a954fc5351bdcc0340deefd4694e3c1d6794ed5` |
| `kitpro-server_0.1.0~alpha11_amd64.cdx.json` | `965d2e81a718db3ede458899012b3ec4f0a66489a6f9012d7bc3727717276849` |

The published public package materially predates GPU, trusted-storage, and media/data work. A new alpha release is recommended, but this milestone does not publish it.

## Validation

- Go tests, vet, formatting: PASS (`go test ./...`, `go vet ./...`, zero `gofmt -l` output)
- Package and installer tests: PASS (Debian and Arch static package suites; 26 installer tests)
- ShellCheck and JSON: PASS (all shell files with the intentional SC2016 test assertion excluded; all JSON parsed by `jq`)
- AppArmor/systemd/package content: PASS through the package static suites; runtime architecture was unchanged
- Markdown/link audit: PASS after replacing one moved LocalAI page and two Red Hat links that rejected automated checks; release-facing documents report zero dead links
- Website: PASS (`npm ci`, lint, production build, zero-vulnerability audit, and local `/server` render)
- Screenshot automation: PASS (real capture plus 151 automation tests)
- Sensitive-data scan and `git diff --check`: PASS for KITPro, website, and the two intended automation files
- Cleanup: PASS (original VM 500 databases restored, synthetic root and temporary credentials removed, services active, tunnel stopped; RTX A2000 restored to running VM 107)

## Remaining limitations

The authoritative list is `docs/release/known-limitations.md`: no arbitrary Docker/devices/binds/host networking, no AMD or Intel workload certification, no multi-GPU selection or VRAM scheduling, no NAS mount management, no cross-installation service discovery, no Jellyfin transcoding certification, and no clustering/HA/full disaster recovery.
