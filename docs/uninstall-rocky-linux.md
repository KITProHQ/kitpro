# Uninstall KITPro Server on Rocky Linux 10

Normal removal stops KITPro services and containers, removes active generated
runtime definitions, and archives those definitions plus secret environment
files under `/var/lib/kitpro-helper/uninstall-preserved`. Application data,
databases, configuration, secrets, backups, and logs remain.

```sh
sudo dnf remove kitpro-server
```

Podman, firewalld, unrelated containers, unrelated firewall rules, and the
container SELinux policy remain installed because other software may use them.
The separately packaged `kitpro-selinux` module may be removed after KITPro if
no preserved KITPro data needs its persistent file context.

Reinstalling the package restores the most recent archived Quadlets and runtime
files without overwriting an existing path. Units that were running before
removal are started again; units that were stopped remain stopped.

## Purge application data

Purge is intentionally separate and requires an explicit acknowledgement:

```sh
sudo kitpro-server-uninstall --purge-data --acknowledge-destroy-data
sudo dnf remove kitpro-server kitpro-selinux
```

The purge deletes only KITPro-owned application data, control/helper state,
runtime configuration, backups, and logs. It is irreversible unless an external
backup exists. It does not remove imported storage roots or Podman images that
may be shared.

After either path, review rather than blindly delete:

```sh
sudo podman ps --all
sudo systemctl list-units 'kitpro-*'
sudo firewall-cmd --list-all
```
