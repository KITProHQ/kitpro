# Public alpha readiness

## Decision

**READY FOR PUBLIC ALPHA** for the certified environments below, subject to
the limitations in this document.

## Objective criteria

- Fresh installation, authenticated setup, catalog installation, lifecycle,
  controlled exposure, and reconciliation work.
- Single- and multi-container applications preserve installation-scoped data.
- Debian, Ubuntu, and Arch package artifacts are reproducible and AppArmor /
  systemd hardening remains active.
- Native package upgrades create a validated pre-update backup and surface
  migration or service failures without deleting application data.
- Application updates accept only trusted catalog releases and preserve the
  installation identity and storage.
- No known critical security invariant is weakened; wildcard exposure,
  arbitrary Docker configuration, and embedded credentials remain rejected.

Alpha remains intentionally conservative: application-data backup is separate
from KITPro control-state backup, automatic updates are not enabled, and
rollback of irreversible schema migrations is not promised.

## Support boundary

Supported: Debian 13 amd64; Ubuntu 26.04 LTS amd64; Arch Linux x86_64 with
linux-lts, fully updated official repositories, rootful Docker, and enforcing
AppArmor. Rocky Linux 10 amd64 is experimental.
