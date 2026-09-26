# Upgrade and remove the Debian-compatible package

## Upgrade from alpha.12 to alpha.13

Alpha.13's incoming package owns the pre-unpack gate for an existing alpha.12
host. The gate validates both state databases with the installed alpha.12
binaries, creates one paired and verified backup set, records approval for the
exact incoming package, and only then permits unpack and migration.

Use `apt install` so the package manager resolves the local artifact and runs
the incoming maintainer scripts:

```sh
sha256sum -c kitpro-alpha13-SHA256SUMS --ignore-missing
sudo apt install ./kitpro-server_0.1.0.alpha13_amd64.deb
```

Do not use `dpkg -i` as a substitute for the documented apt path. An alpha.11
host must first follow the published alpha.11 to alpha.12 transition. Do not
skip a release transition that owns a package-specific recovery contract.

The incoming preflight stops KITPro writers and asks each installed binary to
create a SQLite-safe backup. It verifies both backups independently and binds
them under one transition identity. Only then may unpack and migration
continue.

Backups remain in `/var/lib/kitpro-api/backups` and
`/var/lib/kitpro-helper/backups` with the trust ownership of their source
database. Post-installation runs sequential production migrations, reloads
AppArmor and the systemd manager configuration, and then restarts services. If pre-upgrade backup fails, unpack is
aborted and the previous services are restarted. Universal rollback after an
incompatible database migration is not promised.

The supported transition migrates both databases from alpha.12 schema 13 to
alpha.13 schema 14. Do not delete the paired pre-upgrade backups until
post-upgrade reconciliation and an application smoke test pass.

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
