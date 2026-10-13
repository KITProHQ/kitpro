# Rocky Linux container runtime architecture

Rocky Linux is a host adapter for KITPro Server, not a separate product. The
catalog, API, helper protocol, ownership database, storage layout, and browser
experience are shared with Debian, Ubuntu, and Arch.

## Selected model

| Concern | Rocky Linux 10 implementation |
| --- | --- |
| Container engine | Rootful Podman 5 |
| OCI runtime | crun |
| Orchestration | Generated Quadlet files |
| Service manager | System systemd instance |
| Access control | SELinux Enforcing plus the existing systemd sandbox |
| Firewall | firewalld remains enabled; no broad package-owned rule |
| Persistent data | Host bind directories under `/srv/kitpro/apps` |
| Runtime secrets | Root-owned mode-0600 environment files |

Rootful system services match the managed-server model. They start at boot,
retain the current numeric UID/GID and backup behavior, and avoid a rootless
user-namespace migration. Application containers do not use `--privileged`,
host namespaces, extra capabilities, or a Podman socket.

## Control flow

The API sends a bounded operation over `/run/kitpro/helper.sock`. The helper
validates the catalog plan and protected ownership state, then asks the selected
runtime adapter to materialize it. On Rocky, that adapter writes one `.network`
per installation and one `.container` per component, runs `systemctl
daemon-reload`, and controls the generated services through systemd.

The Podman API socket is not enabled or mounted as a Docker compatibility
shortcut. Podman CLI calls are limited to version, information, inspection,
listing, and foreign-network-member checks. Container create, pull, restart,
and removal are owned by the generated systemd services.

## Compose to Quadlet mapping

KITPro has no Compose file. Its typed catalog provides the comparable fields.

| KITPro plan field | Quadlet or systemd mapping |
| --- | --- |
| OCI digest | `Image=` |
| Component | `.container` |
| Installation bridge | `.network` and `Network=` |
| Component DNS name | `NetworkAlias=` |
| Managed or imported bind | `Volume=` |
| Numeric identity | `User=` and `Group=` |
| Exact published port | `PublishPort=` |
| Command argv | `Exec=` |
| Restart `unless-stopped` | `Restart=always`; explicit KITPro stop stops the unit |
| Component dependency | `Requires=` and `After=` |
| Device node | `AddDevice=` |
| Generated secret | protected `EnvironmentFile=` |

Named volumes, pods, Kubernetes YAML, and Compose secrets are not used. The
current catalog has no health command, so systemd and Podman running state are
the existing health boundary, not application readiness.

## Rootful security boundary

The root helper can prepare managed storage and control KITPro-generated units,
but it accepts only the existing semantic protocol. Quadlet files contain no
secret values. Environment files are mode `0600`; metadata omits environment
values. Every generated name must match KITPro's resource-name grammar.

The initial `kitpro-selinux` package adds a persistent `container_file_t`
mapping for `/srv/kitpro/apps`. Podman's private `:Z` mount handling supplies
per-container MCS labels. It adds no allow rules, permissive domains, or
booleans. A dedicated helper domain remains a hardening and live-AVC validation
item, not a reason to weaken Enforcing mode.

## Known non-identical behavior

- Quadlet removes a container when its service stops. KITPro synthesizes stopped
  inspection state from protected metadata so a later start can recreate it.
- systemd restart semantics are close to Docker `unless-stopped`, but not the
  same state machine. Reboot and explicit-stop behavior must pass on Rocky.
- Docker NVIDIA `DeviceRequests` are not translated. Rocky GPU support remains
  unavailable until a CDI-based path is designed and certified.
- Imported storage is never relabeled automatically. Each approved root needs
  an explicit label decision and live access test.

The detailed discovery record is
[`rocky-container-runtime-assessment.md`](rocky-container-runtime-assessment.md).
