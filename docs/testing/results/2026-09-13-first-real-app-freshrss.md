# First real application — FreshRSS (2026-09-13)

Result: **FAIL — remaining lifecycle/recovery cases not completed**

## Catalog evidence

- Manifest: `software/server/internal/catalog/manifests/freshrss.json`.
- FreshRSS release: 1.29.1, official `docker.io/freshrss/freshrss`.
- linux/amd64 platform digest: `sha256:118f51ee604853547c085a0235d08a2ed98222e7a7010156cb9e7c86a7f24c21`.
- Persistent logical paths: `/var/www/FreshRSS/data` and
  `/var/www/FreshRSS/extensions`.
- Internal HTTP port: 80; no host publication.
- Environment: typed non-secret `TZ=UTC`; restart `unless-stopped`.

Official sources were checked 2026-09-13: Docker Hub image/tag metadata,
FreshRSS Docker documentation, and the FreshRSS release page (links are in
`docs/application-manifest.md`).

## Validation

- Strict manifest parsing, semantic validation, catalog loading, and helper
  plan revalidation: PASS.
- Production binaries rebuilt with the FreshRSS manifest embedded; Debian
  services restarted successfully and remained active: PASS.
- Unauthenticated catalog endpoint remained protected with HTTP 401: PASS.
- Temporary administrator setup/login through the production routes, catalog
  list/detail, and authenticated install: PASS.
- FreshRSS image pull/create/start, digest and security-shape inspection,
  internal diagnostic HTTP (302/200), exact reconciliation, stop, start,
  remove, backup sanity, and persistent marker preservation: PASS.
- Temporary FreshRSS runtime/auth state was cleaned up and the prior
  disposable validation databases restored.

Remaining acceptance cases—missing-resource reconciliation, security-drift
reconciliation, service/Docker/VM restart with FreshRSS present, and
reinstall/reuse verification—were not run in this execution window.

The production model now supports multiple typed logical storage mounts; host
paths remain KITPro-derived and helper-validated. No FreshRSS-specific
privileged code or arbitrary Docker configuration was added.
