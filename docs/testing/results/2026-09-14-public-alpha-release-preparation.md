# Public alpha release preparation

Candidate: `0.1.0-alpha.1`
Source commit: `e903873c2f63322f0994af736a53dfffff13915a`

## Acceptance

The current Debian, Ubuntu, and Arch package/platform acceptance records were
reviewed against this candidate. Repository tests, migration/backup tests,
helper and reconciliation suites, package static checks, and reproducible
Debian/Arch builds pass. The first-run path is documented from a clean host:
install Docker, install the native package, open the local dashboard, create an
administrator, install FreshRSS or Paperless-ngx, and explicitly choose service
exposure.

## Supply chain and security

The candidate uses immutable catalog image digests, pinned Go module checksums,
SBOM output, and `SOURCE_DATE_EPOCH` reproducible archives. The API remains
unprivileged; Docker authority stays in the constrained helper with enforcing
AppArmor and systemd hardening. No telemetry is sent. Release artifacts are
unsigned and distributed only through an approved future channel; checksums
must be verified before installation.

## Decision

`READY FOR PUBLIC ALPHA`

Known limitations are listed in [`docs/release/known-limitations.md`](../../release/known-limitations.md).
Do not create the `v0.1.0-alpha.1` tag or publish artifacts until separately
authorized.
