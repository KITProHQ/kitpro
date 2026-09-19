# Upgrade and remove the Debian-compatible package

## Upgrade

Install the newer local package with APT or `dpkg -i`. Before unpacking an
upgrade, the new package stops KITPro writers and asks each currently installed
binary to create a SQLite `VACUUM INTO` backup. The production backup code opens
the generated file independently, runs `integrity_check` and
`foreign_key_check`, and records its schema version. Only then may unpack and
migration continue.

```sh
sudo apt install ./kitpro-server_<new-version>_amd64.deb
```

Backups remain in `/var/lib/kitpro-api/backups` and
`/var/lib/kitpro-helper/backups` with the trust ownership of their source
database. Post-installation runs sequential production migrations, reloads
AppArmor and the systemd manager configuration, and then restarts services. If pre-upgrade backup fails, unpack is
aborted and the previous services are restarted. Universal rollback after an
incompatible database migration is not promised.

The lifecycle release candidate migrates both databases from the public
alpha.11 schema 7 through schema 13. It preserves legacy `receipts`,
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
