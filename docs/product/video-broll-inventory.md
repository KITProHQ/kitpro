# KITPro Server video and B-roll inventory

This inventory is preparation material, not a launch script. Canonical stills are real product captures from the Debian validation VM and are stored in `docs/assets/screenshots/`.

## Canonical public screenshot set

| Order | Visual | Source | Demonstrates | Use | Cleanup |
| --- | --- | --- | --- | --- | --- |
| 1 | Dashboard | `kitpro-server-dashboard.png` | Server health, private apps, calm overview | README, product page, release notes, video | Demo account and legacy rows removed; no endpoints shown |
| 2 | Catalog | `kitpro-server-catalog.png` | Reviewed 15-app catalog, trusted versions, capability labels | README, product page, video | Verify no internal host data |
| 3 | Installed application | `kitpro-server-application.png` | Exposure, managed storage, CPU/GPU state, lifecycle | README, product page, video | Technical identifiers collapsed or masked |
| 4 | Storage | `kitpro-server-storage.png` | Trusted-root registration and access mode | Product page, release notes, video | Advanced paths closed; placeholder path only |
| 5 | Hardware | `kitpro-server-hardware.png` | Honest accelerator state and CPU availability | Product page, video | This capture shows the current no-GPU VM state; pair with certified GPU evidence, not a fabricated status |
| 6 | Mobile | `kitpro-server-mobile.png` | Responsive dashboard | README, product page, video | No endpoints or credentials |

## Future motion/B-roll shots

| Shot | Source or method | What it demonstrates | Sensitive cleanup |
| --- | --- | --- | --- |
| Dashboard tour | Browser capture automation | Health and installed-app summary | Hide endpoints and IDs |
| Catalog browse | Browser capture automation | Categories, trusted releases, CPU/GPU and storage labels | None beyond normal state review |
| Install flow | Disposable Debian VM | Selection, operation progress, private default | Use synthetic app data |
| Access modes | Installed-app view | Private, server-only, exact-LAN choices | Mask LAN address |
| Storage root | Settings and app selector | Admin-approved root and read-only/read-write intent | Keep advanced host path closed |
| Ollama | Installed-app view and API test | CPU fallback; optional NVIDIA policy | Never show models containing private prompts |
| NVIDIA inference | RTX A2000 validation VM | Real GPU state and inference | Mask device identity only if host-specific data appears; no fake capture |
| Jellyfin | Disposable media library | Read-only media access | Use synthetic/demo media only |
| Application update | Updates view | Trusted update and persistent identity | Hide installation IDs |
| Mobile UI | 390×844 browser capture | Responsive administration | No cleanup expected |
| Package install | Fresh Debian/Ubuntu/Arch VM | Native package workflow | Hide shell history, IPs, usernames when unnecessary |
| Platform trio | Debian 13, Ubuntu 26.04, Arch `linux-lts` | Certified operating-system boundary | Show versions, not network inventory |
| Product page | <https://kitpro.us/server> | Public positioning | No authenticated state |
| GitHub release | <https://github.com/KITProHQ/kitpro/releases> | Packages, checksums, SBOM | Use the actually published release only |

## Reproducible demo sequence

The reusable capture tooling lives in `/home/josh/Documents/GitHub/demo_automation/asciinema-demo-automation`. The prepared sequence is: clean dashboard, catalog, app install, operation progress, exact LAN selection, Open App, trusted-storage selection, Ollama hardware state, media app, and updates. It must run against a disposable authenticated KITPro host with synthetic data. It must not change product behavior or substitute mocked screenshots for product state.
