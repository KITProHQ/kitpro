# Overnight persistence validation — 2026-09-13

## SQLite contention

Debian VM 500, `modernc.org/sqlite`, WAL mode:

```text
100ms  -> 101ms, SQLITE_BUSY
250ms  -> 251ms, SQLITE_BUSY
500ms  -> 501ms, SQLITE_BUSY
1000ms -> 1002ms, SQLITE_BUSY
```

Ten readers plus two writers completed successfully in 2 ms. A 250 ms Phase 1
busy timeout is recommended. `SQLITE_BUSY` should be surfaced to operation
reconciliation after one bounded retry policy; infinite retries are prohibited.

## Backup

The bounded `VACUUM INTO` probe passed with WAL enabled: backup generation 1,
`integrity_check=ok`, and foreign-key check empty. A dedicated continuous
reader/writer generation stress run remains NOT RUN.

## Reproducibility

Two clean builds with Go 1.27.0, `CGO_ENABLED=0`, `-trimpath`,
`-buildvcs=false`, stripped symbols, and empty build ID were byte-identical:

```text
e5be209afc9fc9e74a3884c23864406b55e0e6fde80b4ad8a368554c180dfdcb
```

## Vulnerability scan

Pinned `govulncheck@v1.1.4` was attempted. It panicked in `golang.org/x/tools/go/ssa`
(`unexpected expr: *ast.KeyValueExpr`) under the Go 1.27 toolchain; no scan
result was produced. This is a tooling compatibility blocker, not a claimed
clean security result.

## SBOM and migration matrix

Formal SBOM generation and the full migration failure matrix were NOT RUN.

## Gate disposition

`PRODUCTION DEVELOPMENT APPROVED: NO` — dedicated process-kill recovery,
active-WAL concurrent backup, migration failure, formal SBOM, and a compatible
vulnerability scan remain outstanding.
