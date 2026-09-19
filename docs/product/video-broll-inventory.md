# KITPro Server Video and B-Roll Inventory

Status date: 2026-09-15. This is the authoritative visual-planning inventory for the launch-video cycle, not a script. Product claims must remain within the [current-state brief](kitpro-server-current-state.md).

Companion automation repository: `asciinema-demo-automation`. Canonical public stills: `docs/assets/screenshots/`.

## Product/web visuals

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| KITPro homepage | KITPro as the personal-infrastructure umbrella | <https://kitpro.us/> | Yes; live browser-validated | `browse goto https://kitpro.us/` | Browser video/still | None | Opening / ecosystem |
| KITPro Server page | Current Server positioning, screenshots, supported systems, public alpha CTA | <https://kitpro.us/server> | Yes; live browser-validated | `browse goto https://kitpro.us/server` | Browser video/still | None | Product introduction |
| GitHub repository | Open-source code, Apache-2.0 license, docs, support matrix | <https://github.com/KITProHQ/kitpro> | Yes; live browser-validated | `browse goto https://github.com/KITProHQ/kitpro` | Browser video/still | Hide signed-in account chrome if present | Open source / ownership |
| GitHub release | Published alpha.11 packages, checksums, manifest, build metadata, SBOM | <https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11> | Yes; live browser-validated | `browse goto https://github.com/KITProHQ/kitpro/releases/tag/v0.1.0-alpha.11` | Browser video/still | Hide signed-in account chrome if present | Install / release evidence |

## Installation

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Debian package install | Native `.deb` installation on Debian 13 | Disposable Debian 13 validation VM | No final public capture | `./record-demo-remote.sh <ignored-debian-launch-scenario.yaml>` | Terminal video | Remove IPs, usernames, shell history, and temporary credentials | Installation |
| Ubuntu package install | Same `.deb` workflow on Ubuntu 26.04 LTS | Disposable Ubuntu validation VM | No final public capture | `./record-demo-remote.sh <ignored-ubuntu-launch-scenario.yaml>` | Terminal video | Same as Debian | Platform support |
| Arch package install | Native `.pkg.tar.zst` installation under `linux-lts` | Disposable Arch validation VM | No final public capture | `./record-demo-remote.sh <ignored-arch-launch-scenario.yaml>` | Terminal video | Same as Debian; show `linux-lts` truthfully | Platform support |
| Service startup | Native service starts and local UI becomes available | Disposable supported VM | No final public capture | Remote scenario: package install, service status, local URL | Terminal video | Hide host address unless intentionally generic | Installation |
| First-run page | Creation of the first local administrator | Fresh disposable KITPro state | No final public capture | Browser capture after package installation | Browser video | Use temporary demo credentials and do not record the password | User journey |

## Main UI

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Login | Local authentication boundary | Validation host `/login` | No final public capture | `npm run capture:kitpro-screenshots` flow | Browser video | Do not show password or cookies | User journey |
| Dashboard | Server health, installed apps, current alpha version | `docs/assets/screenshots/kitpro-server-dashboard.png` | Yes; current | `npm run capture:kitpro-screenshots` | Still / browser video | Public-safe now | Product overview |
| Catalog | Reviewed 15-app catalog and capability labels | `docs/assets/screenshots/kitpro-server-catalog.png` | Yes; current | `npm run capture:kitpro-screenshots` | Still / browser video | Public-safe now | Trusted catalog |
| Application install | App choice, storage/GPU options, private default | Disposable validation host | Partial; catalog still only | Browser capture on disposable host | Browser video | Use synthetic data | User journey |
| Operation progress | Visible asynchronous install/update progress | Validation host | Yes; updates still; motion not captured | Browser capture during controlled install | Browser video | Mask operation and installation IDs | User journey |
| Installed application | Status, lifecycle controls, durable-data language | `docs/assets/screenshots/kitpro-server-application.png` | Yes; current FreshRSS state | `npm run capture:kitpro-screenshots` | Still / browser video | Technical IDs and endpoints masked | Manage apps |
| Access controls | Private, This server only, and Local network choices | `docs/assets/screenshots/kitpro-server-access.png` | Yes; current | `npm run capture:kitpro-screenshots` | Still / browser video | LAN address masked | Access and privacy |
| Storage settings | Trusted-root registration and status | `docs/assets/screenshots/kitpro-server-storage.png` | Yes; current | `npm run capture:kitpro-screenshots` | Still / browser video | Advanced path collapsed; placeholder only | Storage/media |
| Hardware/GPU settings | Honest accelerator detection and CPU availability | `docs/assets/screenshots/kitpro-server-hardware.png` | Yes; current no-GPU state | `npm run capture:kitpro-screenshots` | Still / browser video | Do not imply a GPU is attached | AI/GPU |
| Updates | Trusted operations and update state | `docs/assets/screenshots/kitpro-server-updates.png` | Yes; current | `npm run capture:kitpro-screenshots` | Still / browser video | Operation IDs masked | Updates/backups |
| Mobile dashboard | Responsive product UI | `docs/assets/screenshots/kitpro-server-mobile.png` | Yes; current, 390×844 | `npm run capture:kitpro-screenshots` | Still / browser video | Public-safe now | Closing montage |

## Apps

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| FreshRSS | Real installed-app management and Open App flow | Current Debian capture state | Yes; application/access stills | `npm run capture:kitpro-screenshots` | Still / browser video | Use demo feeds only | User journey |
| Actual Budget | Local-first personal finance catalog choice | Catalog | Yes; catalog still | Catalog browse capture | Browser video | Never show real finances | Catalog montage |
| Vaultwarden | Security-oriented catalog choice | Catalog | Yes; catalog still | Catalog browse capture | Browser video | Never show vault contents | Catalog montage |
| Paperless-ngx | Multi-container app presented as one logical product | `docs/assets/screenshots/kitpro-server-paperless.png` in site/automation set | Yes; current catalog card, not installed runtime | `npm run capture:kitpro-screenshots` | Still | Do not imply it is installed | Multi-container |
| Open WebUI | Authenticated AI frontend configured separately | Catalog or disposable installation | Catalog: yes; runtime: no | Catalog browse; optional controlled install | Browser video | No API keys, chats, or private prompts | AI |
| Ollama | Local model runtime, CPU fallback, optional NVIDIA | Catalog/hardware plus certified evidence | Catalog/hardware: yes; inference motion: no | Controlled inference on certified host | Browser/terminal video | Use a generic prompt and public model | AI/GPU |
| IT-Tools | Stateless browser utilities | Current installed-app screenshot source | Yes, but not a media visual | Browser capture on validation host | Browser video | Public-safe demo values | Catalog montage |
| Jellyfin | Read-only media root and media-library experience | Disposable media validation environment | No approved final runtime still | Controlled browser capture after real install | Browser video | Synthetic/public-domain media only | Storage/media |
| Navidrome | Read-only music library | Disposable media validation environment | No approved final runtime still | Controlled browser capture after real install | Browser video | Synthetic/public-domain audio and metadata | Storage/media montage |
| Audiobookshelf | Read-only audiobook library | Disposable media validation environment | No approved final runtime still | Controlled browser capture after real install | Browser video | Synthetic/public-domain books only | Storage/media montage |
| SFTPGo | Exclusive read-write trusted root | Disposable data validation environment | No approved final runtime still | Controlled browser capture after real install | Browser video | Synthetic files; hide usernames/hostnames | Storage/media |

The strongest product visuals are Dashboard, Catalog, Installed Application, Access Controls, Storage, Hardware, Updates, Mobile Dashboard, Paperless-ngx, and a freshly validated Jellyfin or Navidrome runtime. The current file named `kitpro-server-media.png` is rejected for media use because it shows IT-Tools, not a media application.

## AI/GPU

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Ollama CPU | GPU is optional and CPU remains available | Supported no-GPU validation host | UI evidence yes; inference motion no | Install Ollama in CPU mode and run a generic prompt | Browser/terminal video | No private prompts or model credentials | AI/GPU |
| Ollama NVIDIA | Certified NVIDIA acceleration | RTX A2000 validation environment | Historical certification evidence; no approved launch still | Re-run only on attached certified hardware | Browser/terminal video | Show exact hardware truthfully; no fabricated state | AI/GPU |
| Detected GPU | Hardware identity and runtime readiness | Hardware settings | Current still shows no accelerator; certified historical evidence exists | `npm run capture:kitpro-screenshots` when hardware is attached | Still / browser video | Do not reuse no-GPU still as GPU proof | AI/GPU |
| GPU assignment/status | One unambiguous approved GPU class | Ollama install/management view | No final motion capture | Controlled install on certified host | Browser video | Avoid host-specific identifiers when unnecessary | AI/GPU |
| Model inference | Useful local workload using the selected mode | Ollama API or UI | No final launch capture | Generic prompt against a public model | Browser/terminal video | No personal prompts; identify CPU vs NVIDIA accurately | AI/GPU |

## Storage/media

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Trusted-root registration | Administrator approves storage before apps can use it | Storage settings | Yes; current still | `npm run capture:kitpro-screenshots` | Still / browser video | Placeholder path only | Storage/media |
| Read-only media selector | App requests a named compatible read-only root | Jellyfin/Navidrome/Audiobookshelf install | No final capture | Controlled install on disposable host | Browser video | Use generic root names | Storage/media |
| Read-write root | SFTPGo receives one exclusive writer root | SFTPGo install/management | No final capture | Controlled install on disposable host | Browser video | Synthetic files and generic root | Storage/media |
| Jellyfin library | Real media app using trusted imported storage | Disposable Jellyfin installation | No approved final capture | Controlled browser capture | Browser video | Public-domain media only | Storage/media |
| SFTPGo root | Real write-enabled app constrained to approved storage | Disposable SFTPGo installation | No approved final capture | Controlled browser capture | Browser video | Synthetic files only | Storage/media |
| Storage unavailable | Changed/missing mount fails closed | Disposable storage fixture | Evidence exists; no launch visual | Controlled unplug/remount test only if useful | Browser video | No real NAS paths or hostnames | Security / limitations |

## Platform

| Item | What it demonstrates | Source | Existing capture | Automation command | Format | Sanitization | Recommended script section |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Debian 13 | Fully supported host boundary | Disposable Debian VM and package | Validation evidence yes; launch capture no | Remote terminal scenario | Terminal video | Hide network inventory | Platform support |
| Ubuntu 26.04 LTS | Fully supported host boundary | Disposable Ubuntu VM and package | Validation evidence yes; launch capture no | Remote terminal scenario | Terminal video | Hide network inventory | Platform support |
| Arch `linux-lts` | Supported rolling host under exact boundary | Disposable Arch VM and package | Validation evidence yes; launch capture no | Remote terminal scenario | Terminal video | Show full update and `linux-lts`; hide network inventory | Platform support |
| Package artifacts | Native `.deb`, `.pkg.tar.zst`, checksums, manifest, metadata, SBOM | GitHub alpha.11 release | Yes; published | Release-page browser capture | Browser video/still | None | Installation / open source |

## Repeatable demo path

The preferred deterministic path is: dashboard → catalog → install an app → operation progress → manage the app → select **Local network** → **Open App** → register/select trusted storage → show a real media app → show truthful GPU/Ollama state → updates.

Use disposable authenticated hosts and synthetic content. Do not change KITPro behavior to simplify capture, expose passwords or internal infrastructure, or substitute mocked state for real product behavior. If no GPU is attached, show CPU availability and label certified NVIDIA evidence separately.
