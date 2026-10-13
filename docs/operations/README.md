# Operate KITPro Server

These guides cover ongoing application operations after installation.

- [Complete first run](first-run.md)
- [Operate managed ingress](managed-ingress.md)
- [Back up and restore alpha.14 host state](host-backup-restore.md)
- [Recover an administrator and collect diagnostics](admin-recovery-and-diagnostics.md)

## Routine operation

- Use the local dashboard to install, start, stop, recreate, update, and expose
  trusted application services.
- Use the [API reference](../api-reference.md) when a shipped alpha workflow
  does not yet have a complete browser interface.
- Read the [current-state reference](../product/kitpro-server-current-state.md)
  before treating runtime state as application health.

## Backup and restore

- [Back up and restore an application](application-backup-restore.md) describes
  the supported same-installation, managed-storage boundary.
- Keep an independent backup of imported storage. KITPro records imported
  bindings but does not copy imported data.

## Recovery and troubleshooting

- [Recover application lifecycle state](lifecycle-recovery.md) explains
  reconciliation, bounded repair, interrupted restore, and action-required
  states.
- [Debian installation troubleshooting](../install-debian-package.md#troubleshooting)
  covers package, Docker, systemd, and AppArmor checks.
- [Known alpha.14 limitations](../release/known-limitations.md) lists recovery
  work that KITPro does not yet perform.

Do not create a second destructive request after a lost response. Check the
existing operation and reconcile the installation first.
