# Docker address-pool prerequisite

KITPro alpha.13 creates a separate Docker bridge network for every application
generation. A Supported Docker host must therefore have an explicit,
operator-selected `default-address-pools` configuration with enough
non-overlapping address space.

KITPro does not choose a universal CIDR and does not edit
`/etc/docker/daemon.json`. The administrator must select private IPv4 space
that fits the host's LAN, VPN, routed datacenter, cloud, and container
environment.

## Select a pool

Before choosing a pool, inventory at least:

```sh
ip -4 route show
docker network ls --quiet | xargs -r docker network inspect \
  --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}} {{end}}'
```

Also account for routes that are temporarily disconnected or may be added by
VPN clients, site-to-site links, orchestration systems, or future network
changes. KITPro checks the current host route table and Docker network
inventory for obvious conflicts, but no local preflight can guarantee that a
future route will not collide.

The selected configuration must:

- be wholly contained in RFC 1918 private IPv4 space;
- not overlap current or planned host routes or Docker network ranges;
- allocate child networks no smaller than `/24`; and
- leave at least 64 child networks available to KITPro.

The number of child networks in one pool is `2^(child prefix - base prefix)`.
For example, a base prefix four bits shorter than its child-network prefix
provides 16 child networks and is too small by itself. Multiple non-overlapping
pools may be used, and their available capacity is combined.

## Configure Docker

Manually merge the setting into `/etc/docker/daemon.json`, preserving every
unrelated existing key. The required shape is:

```json
{
  "default-address-pools": [
    {
      "base": "ADMIN_SELECTED_RFC1918_CIDR",
      "size": 24
    }
  ]
}
```

`ADMIN_SELECTED_RFC1918_CIDR` is a placeholder, not a KITPro default. Replace
it with a pool selected for this host. If `daemon.json` already exists, edit
the existing JSON object instead of replacing the file.

Validate the complete daemon configuration before restarting Docker:

```sh
sudo dockerd --validate --config-file=/etc/docker/daemon.json
sudo systemctl restart docker.service
sudo /usr/libexec/kitpro-helper --verify-host-prerequisites
```

Restarting Docker can interrupt running containers. Plan that maintenance for
the host. Docker applies a changed default address pool only to networks
created afterward; it does not renumber existing networks. If existing
networks conflict with the selected pool, choose a different pool or migrate
those networks through their owning product's supported procedure.

The KITPro package runs the same fail-closed preflight before database
migration or service activation. Network-creating application operations run
it again so a later daemon or routing change cannot silently bypass the
Supported-host prerequisite.
