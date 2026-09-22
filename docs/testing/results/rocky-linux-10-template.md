# Rocky Linux 10 Podman validation result template

## Run identity

- Overall status: NOT RUN
- VM and snapshot identifiers: Not assigned
- Started/completed UTC: Not assigned
- Source revision: Not assigned
- RPM NEVRAs: Not assigned
- Raw transcript and SHA-256: Not assigned
- Checklist: [`docs/testing/rocky-linux-10-validation.md`](../rocky-linux-10-validation.md)

Copy this template for every attempt. Never replace an older result. Leave each
unexecuted row as `NOT RUN`. SELinux must remain `Enforcing`, firewalld must
remain enabled, and Docker must remain absent.

## Host and native runtime

| Test | Status | Actual evidence |
| --- | --- | --- |
| Rocky Linux 10 x86_64 from `/etc/os-release` | NOT RUN | |
| systemd PID 1 and cgroup v2 | NOT RUN | |
| CPU, RAM, filesystem, and free space | NOT RUN | |
| SELinux Enforcing before installation | NOT RUN | |
| firewalld active and enabled | NOT RUN | |
| Docker packages, service, and socket absent | NOT RUN | |
| Podman package and `podman version` | NOT RUN | |
| crun package and version | NOT RUN | |
| container-selinux package and policy version | NOT RUN | |
| Podman cgroup, storage, and network backends | NOT RUN | |
| Quadlet generator path and version evidence | NOT RUN | |

## Package and control plane

| Test | Status | Actual evidence |
| --- | --- | --- |
| `kitpro-server` and `kitpro-selinux` RPM checksums | NOT RUN | |
| Fresh RPM installation | NOT RUN | |
| API and helper database migration | NOT RUN | |
| `kitpro-helper.socket` active | NOT RUN | |
| `kitpro-api.service` active | NOT RUN | |
| UI loopback response | NOT RUN | |
| Account setup and authentication | NOT RUN | |
| Direct Podman API socket not enabled or mounted | NOT RUN | |

## Application and Quadlet

| Test | Status | Actual evidence |
| --- | --- | --- |
| Single-container install and pinned digest | NOT RUN | |
| Paperless-ngx multi-container install | NOT RUN | |
| Generated `.network` and `.container` inventory | NOT RUN | |
| systemd service inventory and active state | NOT RUN | |
| Secrets absent from Quadlet and process arguments | NOT RUN | |
| Protected environment file ownership/mode | NOT RUN | |
| Dependency order and slow database behavior | NOT RUN | |
| Explicit stop remains stopped | NOT RUN | |
| Crash recovers through systemd policy | NOT RUN | |

## Storage, networking, and firewall

| Test | Status | Actual evidence |
| --- | --- | --- |
| Managed database/application data persistence | NOT RUN | |
| Numeric UID/GID behavior | NOT RUN | |
| Imported read-only storage | NOT RUN | |
| Imported read-write exclusive storage | NOT RUN | |
| Missing/substituted mount fails closed | NOT RUN | |
| Component service-name DNS | NOT RUN | |
| Private mode publishes no port | NOT RUN | |
| Exact loopback publication | NOT RUN | |
| Exact LAN publication and required firewalld rule | NOT RUN | |
| IPv4 and callback/reverse-proxy behavior | NOT RUN | |

## Restart, upgrade, and removal

| Test | Status | Actual evidence |
| --- | --- | --- |
| Host reboot returns running applications | NOT RUN | |
| Data, authentication, secrets, and networks survive reboot | NOT RUN | |
| Helper/API service restart | NOT RUN | |
| Prior compatible build installs | NOT RUN | |
| RPM upgrade backup, migration, reload, and health | NOT RUN | |
| Normal removal stops containers and archives runtime config | NOT RUN | |
| Reinstall restores running/stopped state and data | NOT RUN | |
| Acknowledged purge removes only KITPro-owned data | NOT RUN | |
| Podman and unrelated host state remain | NOT RUN | |

## SELinux evidence

| Test | Status | Actual evidence |
| --- | --- | --- |
| SELinux Enforcing throughout | NOT RUN | |
| Application processes run as `container_t` | NOT RUN | |
| Process and mount labels contain MCS categories | NOT RUN | |
| Managed storage receives private `:Z` label | NOT RUN | |
| Imported storage uses an exact reviewed treatment | NOT RUN | |
| AVC window captured and explained | NOT RUN | |
| No permissive, privileged, boolean, or broad-policy workaround | NOT RUN | |

## Final assessment

- Failures: NOT RUN
- Blockers: NOT RUN
- Unexplained AVCs: NOT RUN
- Security workaround required: NOT RUN
- Recommendation to promote Rocky: NOT RUN

Promotion requires every applicable row to pass. Create a new result document
for a later attempt rather than editing a failed or blocked record.
