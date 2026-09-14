# Phase 1 helper package layout and upgrade contract

This is a packaging dry-run specification, not a built package.

```text
/usr/bin/kitpro-api
/usr/libexec/kitpro-helper
/etc/apparmor.d/kitpro-helper
/lib/systemd/system/kitpro-api.service
/lib/systemd/system/kitpro-helper.service
/lib/systemd/system/kitpro-helper.socket
/var/lib/kitpro-api/control.db
/var/lib/kitpro-helper/helper.db
/var/log/kitpro/                 # only if required; journald preferred
/run/kitpro/helper.sock
/srv/kitpro/                     # approved application data roots
```

The helper belongs in `/usr/libexec` so ordinary users are not encouraged to invoke it directly. The API and helper have separate service identities and database ownership. Runtime directories are systemd-owned; state directories are root-owned for helper state and API-owned for control state. AppArmor policy, systemd units, migrations, and frontend assets are package-owned and verified before activation.

Upgrade order:

1. Verify package identity, signature/checksum, and platform compatibility.
2. Check both database integrity and create consistent backups.
3. Quiesce affected operations.
4. Install binaries/assets/policy and verify file ownership.
5. Apply transactional migrations; never silently downgrade.
6. Load AppArmor and verify enforcing status.
7. Reload/start services and run health checks.
8. Preserve the previous artifact and backup metadata for manual rollback.

Migration failure, integrity failure, policy-load failure, protocol-version mismatch, or helper startup failure blocks activation and requires explicit recovery. Automatic binary rollback is not promised after an irreversible schema migration.
