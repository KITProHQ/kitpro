# Application manifest/catalog validation — 2026-09-13

Result: PASS

## Scope

Phase 1 now represents the disposable BusyBox workload as embedded strict JSON
catalog content. The API resolves the manifest into a typed plan; the helper
revalidates that plan before using the Docker Engine API. No Compose/YAML/raw
Docker passthrough was added.

## Evidence

- Manifest format: JSON, schema version 1.
- Catalog load: embedded BusyBox entry parsed and validated at API startup.
- Parser: unknown fields, duplicate keys, oversized input, invalid schema,
  digest, registry, platform, storage, environment, command, and restart
  values rejected by tests.
- Helper plan validation: tampered mutable image, host path, unsafe restart,
  shell command, and secret environment plans rejected before Docker dispatch.
- Debian VM 500: authenticated catalog list `200`; app detail `200`; install
  through `POST /api/v1/apps/busybox/install` returned `202` and created a
  digest-pinned BusyBox workload; exact reconciliation returned `exact`; start,
  stop, and remove operations returned `202`.
- Dashboard: authenticated page `200` and catalog entry rendered under
  “Available applications”. The form install path succeeded using the
  SameSite CSRF cookie fallback.
- An approved storage marker under the derived
  `/srv/kitpro/apps/busybox/<instance>/data/` path remained present with the
  same digest after runtime removal; persistent data is not removed by the
  lifecycle operation.
- Authentication regression: existing session/CSRF/Origin/Host protections
  remained active; anonymous lifecycle requests remain denied.

## Security decisions

Application identity (`busybox`) is distinct from release (`1.37.0`) and
Docker object names. Host storage is derived under
`/srv/kitpro/apps/<application>/<instance>/`; manifest files cannot provide
host bind sources. Only Docker Hub/GHCR identities, immutable digests,
linux/amd64, bounded argv, explicit environment entries, and `no`/
`unless-stopped` restart policies are accepted. Secrets cannot be embedded.

One invalid built-in catalog entry fails catalog startup in this implementation;
future catalog availability policy should choose per-entry quarantine only when
there is a signed/catalog-index boundary to preserve trust. Remote catalog
downloads and signing are not implemented.

## Remaining work

A larger real application entry remains outside this bounded validation. The
next milestone should validate one carefully selected real catalog entry before
expanding the catalog.
