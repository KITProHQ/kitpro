# Public alpha readiness

## Decision

**ALPHA.13 RELEASE CANDIDATE QUALIFIED** for the Supported environments below,
subject to the limitations in this document. Tagging and publication still
require explicit human authorization.

## Objective criteria

- Fresh installation, authenticated setup, catalog installation, lifecycle,
  controlled exposure, and reconciliation work.
- Single- and multi-container applications preserve installation-scoped data.
- Debian/Ubuntu and Arch package artifacts passed the alpha.13 release validation,
  and AppArmor and systemd hardening remain active.
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

Supported: Debian 13 amd64, Ubuntu 26.04 LTS amd64, and Arch Linux x86_64 with
`linux-lts` and fully updated official repositories. Each uses rootful Docker,
enforcing AppArmor, and an operator-selected non-overlapping address pool.

Rocky Linux 10 and Podman remain Experimental. Native package, SELinux, host,
storage registration, and reboot checks pass. Application lifecycle is
unavailable because the Podman adapter does not implement alpha.13 staged
generations.
