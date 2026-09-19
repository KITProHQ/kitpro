# Operate KITPro with Podman and Quadlet

KITPro-generated definitions live in `/etc/containers/systemd/`. Quadlet turns
them into normal systemd services at daemon reload and boot. Do not edit a
generated file while KITPro owns the installation.

## Routine status

```sh
podman ps --all
podman network ls
systemctl status kitpro-api kitpro-helper.socket
systemctl list-units 'kitpro-*'
journalctl -u kitpro-api -u kitpro-helper.service
getenforce
```

Each `.container` file named `kitpro-example.container` generates
`kitpro-example.service`. A `.network` file generates a service with the
`-network.service` suffix.

## Restart an application component

Use KITPro's UI for logical application operations. For diagnosis on a
disposable or maintenance host:

```sh
sudo systemctl restart kitpro-APPLICATION-INSTANCE-g1.service
sudo journalctl -u kitpro-APPLICATION-INSTANCE-g1.service
sudo podman logs kitpro-APPLICATION-INSTANCE-g1
```

Multi-container applications include systemd dependencies. Starting the
dependent component also starts its required component. `After=` controls order
but does not claim database readiness beyond the catalog's current behavior.

## Reload generated definitions

KITPro writes files atomically and reloads systemd itself. After a reviewed
manual package or recovery operation:

```sh
sudo systemctl daemon-reload
sudo systemctl list-unit-files 'kitpro-*'
```

Do not use `podman generate systemd` or `podman-compose`; they would create a
second lifecycle owner.

## Storage and secrets

Managed data remains under `/srv/kitpro/apps`. Runtime environment files under
`/etc/kitpro-server/runtime` are root-only and may contain secrets. Do not paste
them into support logs or pass them on process command lines.

Back up data and both SQLite control databases together. A container or
Quadlet file is disposable; the application data, helper ownership database,
API database, and runtime secret files are not.

## Networking and firewalld

KITPro creates one Podman bridge per installation. Component aliases provide
DNS within that bridge. Private applications publish nothing. Loopback and LAN
exposure use exact `PublishPort=` addresses.

The package does not disable firewalld or open a permanent range. Rocky's
default `StrictForwardPorts=no` setting allows the exact ports published by
Podman/netavark even when `firewall-cmd --list-ports` is empty. Selecting LAN
access in KITPro is therefore the authorization that publishes that one port;
it does not add a permanent firewalld port or a `20000-29999` range rule.

Confirm the actual binding with both `podman port <container>` and
`firewall-cmd --zone=<host-zone> --list-all`. A host configured with
`StrictForwardPorts=yes` needs separate, generation-aware firewalld forwarding
to the current container address. That configuration is not part of the Rocky
runtime path yet; keep the service internal or loopback-only instead of adding
a broad exception.
