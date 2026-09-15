# Changelog

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
