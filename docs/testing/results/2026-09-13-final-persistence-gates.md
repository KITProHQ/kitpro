# Final persistence-gate follow-up — 2026-09-13

**PRODUCTION DEVELOPMENT APPROVED: NO**

## Completed evidence

- Debian lock contention using `modernc.org/sqlite` and WAL: 100/250/500/1000
  ms busy timeouts returned `SQLITE_BUSY` at approximately the configured
  duration. Ten readers plus two writers succeeded. Recommended initial timeout:
  `250ms`, with bounded retry only.
- Bounded `VACUUM INTO` backup: PASS; captured generation was internally
  consistent and `integrity_check` returned `ok`.
- Two clean helper builds with Go 1.27.0 and fixed release flags were
  byte-identical (SHA-256 `e5be209afc9fc9e74a3884c23864406b55e0e6fde80b4ad8a368554c180dfdcb`).
- `govulncheck@v1.8.0`: completed; no reachable vulnerabilities. One
  unreachable required-module finding was reported.
- CycloneDX SBOM: generated at
  `prototypes/go-helper-validation/sbom/kitpro-validation.cdx.json` using
  `cyclonedx-gomod@v1.7.0`.

## Remaining blockers

- Dedicated process-kill committed/uncommitted/WAL recovery: NOT RUN.
- Dedicated active-WAL concurrent-generation backup: NOT RUN.
- Full migration failure matrix (future version, downgrade, preflight,
  backup-hook, duplicate/gap, interruption): NOT RUN.

The prior tmpfiles/reboot, AppArmor, systemd-hardening, and permission gates
remain valid and were not rerun here.
