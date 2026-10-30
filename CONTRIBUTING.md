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

Release artifacts must pass the
[GitHub-safe publication staging workflow](software/server/packaging/README.md#github-safe-publication-staging)
before upload. Do not upload raw build output or rely on GitHub to normalize
asset filenames.

The project uses AI-assisted development tools for research, drafting, review,
and implementation support. Maintainers remain responsible for technical
direction, source review, testing, and release decisions. Contributions are
judged by the same code, evidence, and security standards regardless of which
tools helped produce them. Review the source and tests directly, and report
suspected defects or security problems through the project's normal public
channels.
