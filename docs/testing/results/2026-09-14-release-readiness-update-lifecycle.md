# Release readiness and update lifecycle

Date: 2026-09-14
Release: `0.1.0~alpha6`
Source checkpoint: `d088ae12a000f05d358b21019ea451531a6c8262`

## Version and package evidence

API and helper report the same injected version, source commit, and build
metadata. Debian and Arch artifacts are built with `SOURCE_DATE_EPOCH` from
the source commit, `-trimpath`, disabled CGO, and reproducible archive
metadata. Two clean builds matched byte-for-byte:

- Debian package SHA-256: `7d57a99e81ef10c227ae62f78ce2294da2b78b794fcf63b02117cb5d301e7b41`
- Arch package SHA-256: `68c3b33f29a64413fe995e66ca4453bc66d0f3c95eb84f594e393f6eae98128e`
- SBOM SHA-256: `c75bdafc188435396b7c1f197f3b6f39ef5c9a2052a362703732d9a2dc5f005f`

## Update lifecycle

Native apt/dpkg and pacman remain authoritative. Maintainer hooks stop the
services, run the production migration path, and create validated SQLite
`VACUUM INTO` backups before an upgrade. Integrity and foreign-key checks are
performed before activation; downgrade is rejected as unsupported. Migration
or service failure leaves the prior backup and an explicit package-manager
error without deleting `/srv/kitpro/apps`.

The API exposes authenticated build metadata at `/api/v1/version`. Trusted
application updates use a strict release identifier, preserve installation
identity/storage, advance runtime generation, and record `app_update_started`,
`app_update_succeeded`, or `app_update_failed`.

## Validation

Focused API coverage proves strict release selection, pre-update backup,
installation identity preservation, generation advancement, and metadata
reporting. Existing package, migration, helper, reconciliation, and platform
acceptance evidence remains valid from the preceding checkpoints. No automatic
application updates are enabled in alpha; update actions require an
authenticated administrator and trusted catalog release.

## Readiness decision

`READY FOR PUBLIC ALPHA` within the documented platform and security boundary.
Application-data backup remains a separate future capability, and irreversible
schema rollback is not promised.
