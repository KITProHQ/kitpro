# Upgrade KITPro Server on Rocky Linux 10

Use RPM transactions so database backups, migrations, systemd reload, policy
updates, and service restart stay ordered.

## Before upgrade

```sh
sudo systemctl status kitpro-api kitpro-helper.socket
sudo podman ps --all
sudo getenforce
```

Take an application-consistent backup according to the catalog workload. Keep
copies of `/var/lib/kitpro-api`, `/var/lib/kitpro-helper`,
`/etc/sysconfig/kitpro-server`, `/etc/kitpro-server/runtime`, and
`/srv/kitpro/apps`.

## Upgrade transaction

Build the new RPMs and install them together:

```sh
sudo dnf upgrade ./kitpro-selinux-NEW.rpm ./kitpro-server-NEW.rpm
```

The RPM pre-transaction path stops the control plane and asks the installed
binaries to create verified pre-upgrade database backups. The new package then
runs migrations, restores labels, reloads systemd, and starts the API and
helper socket. Existing Quadlet files, environment secrets, and application
data remain in place.

## Validate

```sh
sudo systemctl daemon-reload
sudo systemctl status kitpro-api kitpro-helper.socket
sudo podman ps --all
sudo ausearch -m AVC,USER_AVC -ts recent -i
```

Validate the UI, authentication, one core application operation, component DNS,
storage, and restart recovery. A package build is not upgrade-certified until
an older compatible build has passed this transaction on a clean Rocky VM.

Downgrades are not supported. Do not manually delete a container or Quadlet to
force an upgrade; persistent state must be proven before runtime replacement.
