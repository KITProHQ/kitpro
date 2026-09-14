# Contributing

Install Go 1.27.x and the platform tools used by the package you are testing.
Run `cd software/server && go test ./... && go vet ./...` before submitting a
change, and format Go with `gofmt`.

Catalog contributions require authoritative upstream provenance, immutable
linux/amd64 digests, strict manifest validation, persistence/recreation and
exposure tests, and no credentials or arbitrary Docker configuration.
Changes must preserve the unprivileged API, constrained root helper,
AppArmor/systemd hardening, installation-scoped storage, and fail-closed
reconciliation boundaries.
