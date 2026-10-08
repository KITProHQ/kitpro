# Alpha.14 implementation continuation

Date: 2026-09-26

Status: `ALPHA14_BLOCKED_PLATFORM_QUALIFICATION`

Update (2026-10-08): [fresh Debian qualification passed](2026-10-08-alpha14-fresh-debian-qualification.md)
after two local fixes. ACME staging and Cloudflare live gates remain open. The
observations below are historical and retain their original scope.

This record covers the local alpha.14 continuation after commit `c6f904c`. It
does not qualify a release candidate and does not replace the historical
alpha.13 platform evidence.

## Implemented locally

- ACME HTTP-01 issuance, validation, atomic activation, typed expiry state, and
  scheduled renewal
- optional Cloudflare Tunnel configuration, scoped-token validation, runtime
  isolation, route lifecycle, and explicit remote deletion
- alpha.14 host-state backup and journaled restore with fail-closed ingress
  holds
- durable ten-step first-run workflow, typed readiness, and Docker address-pool
  assessment
- operator, API, security, backup, upgrade, and release-draft documentation
- alpha.14 threat-model and security-invariant updates

## Local regression evidence

The following checks passed from the alpha.14 working tree:

- `go test ./...`
- `node --test cmd/kitpro-api/web_test.mjs`
- Debian package static tests
- Debian incoming pre-install tests
- Debian fail-closed upgrade tests
- Arch package static tests
- Arch fail-closed upgrade tests
- RPM package static tests
- publication-staging unit tests
- `git diff --check`

The Go suite used its controlled loopback HTTP servers. The Debian package
tests used `/dev/shm` after the shared `/tmp` user quota prevented a temporary
binary copy. The unchanged tests then passed.

## Live qualification boundary

The recorded disposable platform targets were checked without mutation:

| Target | Result |
| --- | --- |
| Debian 13, VM 500, `10.10.0.115` | Unreachable from the workspace; no route to host |
| Ubuntu 26.04, VM 502, `10.10.0.116` | Unreachable from the workspace; no route to host |
| Arch Linux, VM 503, `10.10.0.119` | Reachable; `linux-lts` 6.18.54; no non-interactive sudo or root SSH |
| Rocky Linux 10, VM 501, `10.10.0.114` | Reachable; Rocky 10.2; no non-interactive sudo or root SSH |
| Proxmox node `hermes` | Reachable; the automation identity cannot inspect or restore VM snapshots without a password |

The targets were not installed, upgraded, rebooted, or otherwise changed.
Without a verified disposable snapshot and administrative guest access, live
package installation would risk unknown state and could not be recovered by
the automation identity.

## Gates not run

The following release-candidate gates remain open:

- live nginx HTTP, HTTPS, route-isolation, reload-failure, and rollback tests;
- ACME path with the installed services and reboot renewal recovery;
- Cloudflare provider restart, outage, and host reboot tests;
- AppArmor enforcement on Debian and Ubuntu;
- SELinux enforcement on Rocky;
- fresh installation and published-alpha.13 package transition on every
  Supported platform;
- all 20 visible application upgrade-preservation checks;
- platform reboot and service-failure recovery;
- final clean Debian, Arch, and RPM artifact rebuild and smoke tests; and
- publication staging and checksum verification of final frozen bytes.

No final alpha.14 artifact was declared. No publication staging was performed.
Nothing was pushed, tagged, published, uploaded, installed, or deployed during
this continuation.
