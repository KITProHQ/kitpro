# Rocky Linux 10 platform-validation result template

## Run identity

- Overall status: NOT RUN
- VM identifier: Not assigned
- Snapshot identifier: Not assigned
- Started UTC: Not assigned
- Completed UTC: Not assigned
- Test revision or source checksum: Not assigned
- Raw transcript: Not assigned
- Transcript SHA-256: Not assigned
- Procedure: [`prototypes/privilege-boundary/RUNBOOK.md`](../../../prototypes/privilege-boundary/RUNBOOK.md)
- SELinux procedure: [`docs/testing/selinux-rocky.md`](../selinux-rocky.md)
- Tooling: [`tools/platform-validation/`](../../../tools/platform-validation/)

Copy this template for every new Rocky run and leave unexecuted rows as `NOT RUN`. Do not reuse a previous result as evidence for a new VM or daemon configuration. SELinux must remain `Enforcing` throughout.

## Baseline observations

| Item | Status | Actual observation |
| --- | --- | --- |
| `/etc/os-release` | NOT RUN | |
| Kernel and architecture | NOT RUN | |
| systemd PID 1 and version | NOT RUN | |
| cgroup mode/controllers | NOT RUN | |
| CPU and RAM | NOT RUN | |
| Root source, filesystem, size, free bytes/inodes | NOT RUN | |
| Network routes/listeners | NOT RUN | |
| firewalld and nftables/iptables state | NOT RUN | |
| SELinux baseline mode and policy | NOT RUN | |
| Docker inventory before test | NOT RUN | |

## Docker installation and authority

| Test | Status | Command/output evidence |
| --- | --- | --- |
| RHEL-compatible Docker repository configured with derivative caveat | NOT RUN | |
| Repository signing-key fingerprint verified | NOT RUN | |
| Engine, CLI, containerd, Buildx, Compose plugin installed | NOT RUN | |
| Docker and containerd active/enabled | NOT RUN | |
| Engine version/API and `docker info` response | NOT RUN | |
| `docker compose version` | NOT RUN | |
| `docker buildx version` | NOT RUN | |
| cgroup version and Docker storage driver | NOT RUN | |
| Root reaches Docker | NOT RUN | |
| Ordinary user denied Docker socket | NOT RUN | |
| API-test identity denied Docker socket | NOT RUN | |
| Docker group grant authority documented or disposable test performed | NOT RUN | |
| SELinux remains Enforcing after installation | NOT RUN | |

## Privilege and protocol boundary

| Test | Status | Command/output evidence |
| --- | --- | --- |
| Root-owned runtime directory and socket modes | NOT RUN | |
| Allowed service identity connects | NOT RUN | |
| `SO_PEERCRED` reports exact API-test UID | NOT RUN | |
| Root reaches socket but helper rejects wrong peer UID | NOT RUN | |
| Unrelated Unix user denied | NOT RUN | |
| Helper reaches Docker Engine API | NOT RUN | |
| API cannot tunnel raw Docker or root commands | NOT RUN | |
| Unknown, malformed, stale, and oversized requests denied | NOT RUN | |
| Forbidden mounts, devices, namespaces, capabilities, security options, and sysctls denied | NOT RUN | |
| Disconnected and concurrent clients bounded | NOT RUN | |

## Docker resource behavior

| Test | Status | Command/output evidence |
| --- | --- | --- |
| Discovery registry/repository/tag/platform recorded | NOT RUN | |
| Fully qualified immutable digest deployed and inspected | NOT RUN | |
| KITPro-test container lifecycle | NOT RUN | |
| One internal per-instance bridge | NOT RUN | |
| Unrelated networks untouched | NOT RUN | |
| No host ports by default | NOT RUN | |
| IPv4 loopback publication/reachability | NOT RUN | |
| IPv6 loopback publication/reachability | NOT RUN | |
| Non-loopback reachability denied | NOT RUN | |
| Unrelated container stop/remove denied and object unchanged | NOT RUN | |
| Unknown volume manipulation unavailable/denied | NOT RUN | |

## Ownership disagreement

| Case | Status | Command/output evidence |
| --- | --- | --- |
| Exact record and labels agree | NOT RUN | |
| Expected labels missing | NOT RUN | |
| Helper record missing | NOT RUN | |
| Incorrect instance ID | NOT RUN | |
| Incorrect resource type | NOT RUN | |
| Unknown Docker object ID | NOT RUN | |
| Label-only resource | NOT RUN | |
| Helper-record-only resource | NOT RUN | |
| Duplicate semantic identity | NOT RUN | |
| Foreign look-alike name | NOT RUN | |

## Filesystem safety

| Test | Status | Command/output evidence |
| --- | --- | --- |
| Traversal and absolute paths rejected | NOT RUN | |
| Symlink escape rejected | NOT RUN | |
| Symlink replacement rejected | NOT RUN | |
| Hard-link case | NOT RUN | |
| Descriptor-relative containment | NOT RUN | |
| Unexpected mount/mount ID rejected | NOT RUN | |
| Mount substitution rejected | NOT RUN | |
| Basic deterministic TOCTOU case | NOT RUN | |
| High-frequency rename/link/mount race | NOT RUN | |

## Restart and recovery

| Test | Status | Command/output evidence |
| --- | --- | --- |
| Client restart/reconnect | NOT RUN | |
| Same operation ID and body replays one result | NOT RUN | |
| Same operation ID with different body conflicts | NOT RUN | |
| Unknown operation ID | NOT RUN | |
| Helper restart preserves receipt | NOT RUN | |
| Docker outage returns bounded failure | NOT RUN | |
| Docker restart recovers | NOT RUN | |
| Interrupted operation returns recovery state | NOT RUN | |
| Host restart with managed resource | NOT RUN | |

## SELinux evidence

| Test | Status | Command/output evidence |
| --- | --- | --- |
| Helper process context | NOT RUN | |
| Helper socket/runtime/state file contexts | NOT RUN | |
| Docker socket context | NOT RUN | |
| Docker reports SELinux integration | NOT RUN | |
| Container process and mount labels | NOT RUN | |
| AVC search window and audit service state | NOT RUN | |
| Fixture-related AVC classification | NOT RUN | |
| Minimal policy requirement | NOT RUN | |
| Final mode remains Enforcing | NOT RUN | |

## Final inventory and assessment

| Item | Status | Actual observation |
| --- | --- | --- |
| Test-labelled containers after cleanup | NOT RUN | |
| Test-labelled networks after cleanup | NOT RUN | |
| Unrelated object unchanged | NOT RUN | |
| Fixture audit/event evidence | NOT RUN | |
| Security compromise required | NOT RUN | |
| Workaround required | NOT RUN | |
| Rocky suitability | NOT RUN | |

## Failures, blockers, and remaining work

Record every failed, blocked, and not-run case. A later attempt gets a new result file; do not rewrite this record. Any need to disable enforcement is a failure, not a workaround.
