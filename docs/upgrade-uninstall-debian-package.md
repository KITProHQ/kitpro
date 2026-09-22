# Upgrade and remove the Debian-compatible package

## Upgrade

### Alpha.11 to alpha.12

This transition must use `kitpro-debian-upgrade`. Raw `apt install` and
`dpkg -i` are unsupported for alpha.11 to alpha.12.

```sh
sha256sum -c kitpro-alpha12-SHA256SUMS --ignore-missing
chmod +x kitpro-debian-upgrade
sudo ./kitpro-debian-upgrade ./kitpro-server_0.1.0.alpha12_amd64.deb
```

The wrapper checks the frozen package hash, package name, installed version,
target version, and architecture. It validates both existing databases and
creates the complete verified backup set before invoking APT. The schema 7 to
schema 13 migration is supported and validated. Downgrading after migration is
unsupported.

### Later supported upgrades

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
database. Post-installation runs sequential production migrations and reloads
AppArmor before restarting services. If pre-upgrade backup fails, unpack is
aborted and the previous services are restarted. Universal rollback after an
incompatible database migration is not promised.

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
