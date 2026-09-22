# Upgrade and remove the Debian-compatible package

## Upgrade from alpha.11 to alpha.12

Use the alpha.12 transition wrapper on an existing alpha.11 host. A package
manager can install bytes, but it cannot determine whether KITPro's control and
helper state form a recoverable ownership record. The wrapper validates both
state databases, creates and verifies the required backup set, binds approval
to the expected package and artifact identity, and only then permits the
package transaction.

Raw `apt install` and `dpkg -i` transitions from alpha.11 are unsupported.
They bypass KITPro's application-level checks before package mutation. This
restriction does not apply to a fresh alpha.12 installation.

```sh
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-debian-upgrade
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-server_0.1.0.alpha12_amd64.deb
curl -LO https://github.com/KITProHQ/kitpro/releases/download/v0.1.0-alpha.12/kitpro-alpha12-SHA256SUMS
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
chmod +x kitpro-debian-upgrade
sudo ./kitpro-debian-upgrade ./kitpro-server_0.1.0.alpha12_amd64.deb
```

The wrapper stops KITPro writers and asks each installed binary to create a
SQLite `VACUUM INTO` backup. The verification code opens each backup
independently, runs `integrity_check` and `foreign_key_check`, and records its
schema version. Only then may unpack and migration continue.

Backups remain in `/var/lib/kitpro-api/backups` and
`/var/lib/kitpro-helper/backups` with the trust ownership of their source
database. Post-installation runs sequential production migrations, reloads
AppArmor and the systemd manager configuration, and then restarts services. If pre-upgrade backup fails, unpack is
aborted and the previous services are restarted. Universal rollback after an
incompatible database migration is not promised.

The supported transition migrates both databases from alpha.11 schema 7
through alpha.12 schema 13. It preserves legacy `receipts`,
`ownership`, and `component_ownership` as migration and forensic evidence while
adding helper operations, leases, generations, component progress,
reconciliation state, control projections, and restore journals. Do not delete
the pre-upgrade backups until post-upgrade reconciliation and an application
smoke test pass.

Root-local recovery tooling can verify a restored copy without opening the live
database:

```sh
sudo -u kitpro-api env KITPRO_CONTROL_DB=/path/to/control-copy.db \
  /usr/bin/kitpro-api --verify-database
sudo env KITPRO_HELPER_DB=/path/to/helper-copy.db \
  /usr/libexec/kitpro-helper --verify-database
```

Package downgrade is unsupported. `preinst` rejects a lower package version
before unpack so an older binary cannot open newer state.

## Remove

```sh
sudo apt remove kitpro-server
```

Remove stops and disables KITPro, unloads AppArmor, and removes package-owned
binaries, units, policy, and tmpfiles configuration. It preserves configuration,
control/helper databases, backups, logs, and all application data. Reinstalling
the package reuses that state after migrations succeed.

## Purge

```sh
sudo apt purge kitpro-server
```

Purge removes `/etc/default/kitpro-server`, but conservatively retains both
KITPro state directories, backups, logs, the state-owning service account, and
`/srv/kitpro/apps`. The helper database is the durable authority linking an
installation to its Docker resources; deleting it while a runtime survives
would make safe lifecycle operations impossible. Package purge is therefore
not an application or trusted-state deletion command. Any future destructive
data deletion must be a separate, explicit KITPro operation.

Docker Engine, Docker images, and unrelated containers are never removed by
package remove or purge.
