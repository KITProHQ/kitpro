# Changelog

## 0.1.0-alpha.11 (unreleased)

- Add Navidrome and Audiobookshelf with administrator-approved read-only media libraries.
- Add SFTPGo as the first exclusive imported read-write consumer, with browser-based first administrator setup and no default credential.
- Run trusted images under bounded numeric identities and assign ownership only to their KITPro-managed storage directories.
- Prevent mixed readers and writers on one trusted root while allowing safe read-only sharing.
- Mark Jellyfin as optional NVIDIA acceleration with CPU fallback.

## 0.1.0-alpha.10 (unreleased)

- Add administrator-approved trusted storage roots with canonical path and filesystem identity checks; see the [trusted storage guide](docs/trusted-storage.md).
- Add schema v4 logical read-only/read-write storage slots without raw host paths or Docker bind input.
- Reconcile missing, extra, changed, or weakened mounts as security drift and block unavailable storage before start or recreation.
- Add Jellyfin 12.1 with managed config/cache and a required read-only imported media library.

## 0.1.0-alpha.9 (unreleased)

- Show detected GPU identity, runtime readiness, certification, current availability, and installed CPU/GPU mode in the server interface.
- Reject GPU candidates whose complete runtime or data path cannot retain immutable provenance, bounded storage, and typed devices.
- Keep Open WebUI and Ollama independent until trusted cross-installation service discovery exists.
- Record why ComfyUI, Jellyfin, Frigate, whisper.cpp, and InvokeAI do not yet fit the trusted runtime boundary.

## 0.1.0-alpha.8 (unreleased)

- Add schema v3 trusted hardware classes with helper-side discovery and exact device resolution.
- Persist component-scoped CPU/GPU intent and reconcile device drift.
- Add normalized hardware inventory and hardware status in catalog/settings.
- Add Ollama 0.34.0 with persistent models, private API, and CPU fallback.
- Validate bounded NVIDIA acceleration, reboot persistence, and CPU fallback on Debian, Ubuntu, and Arch.

## 0.1.0-alpha.7 (prepared, not published)

- Add immutable Open WebUI 0.11.3 and IT-Tools 2024.10.22 catalog entries.
- Add stable, installation-scoped `random-hex-32` application secrets without
  exposing values through the API, interface, receipts, or logs.
- Allow large image pulls to use a bounded 30-minute transfer window and drain
  the complete Docker progress stream.
- Preserve the existing capability-free helper and reject candidates that need
  storage ownership mutation, unsafe bootstrap credentials, or unsupported
  orchestration behavior.

## 0.1.0-alpha.2

- Refined the public-alpha dashboard, catalog, application management, access,
  update, error, responsive, and accessibility experience.
- Refreshed public product screenshots and documentation to match the shipped
  interface.
- Kept the trusted catalog, persistence, reconciliation, and security
  boundaries from alpha.1 unchanged.

## 0.1.0-alpha.1

- Public alpha packaging for Debian, Ubuntu, and Arch.
- Authenticated local dashboard with CSRF and origin/host protections.
- Trusted immutable application catalog with seven single-container apps and
  Paperless-ngx multi-container support.
- Installation identity, persistent storage reuse, reconciliation, and
  controlled internal/loopback/LAN exposure.
- AppArmor/systemd hardening, reproducible packages, SBOMs, and native update
  backup/migration lifecycle.
