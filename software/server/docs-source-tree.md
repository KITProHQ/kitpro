# Production source tree

`cmd/kitpro-api` is the unprivileged browser-facing service. `cmd/kitpro-helper` is the root-owned semantic operation service. The packages under `internal/` are intentionally shared only through typed interfaces: protocol framing, state/migrations, Docker HTTP transport, ownership identity, operations, configuration, catalog/manifest validation, and conservative reconciliation classification. `web.html` is embedded into the API binary for local, offline packaging.

The SQL files under `migrations/` document the minimal control/helper schema. Runtime migration code remains small and transaction-wrapped; no production catalog schema is included.

`packaging/` contains the canonical Debian package builder, maintainer scripts,
systemd units, AppArmor profile, tmpfiles policy, package configuration, and
static package tests. Runtime databases and application data are never embedded
in the package.
