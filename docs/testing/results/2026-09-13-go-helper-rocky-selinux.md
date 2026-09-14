# Go helper production-shape validation — Rocky Linux 10 — 2026-09-13

Status: **NOT RUN for MAC execution**. Baseline access was confirmed, but the compiled Go helper was not installed under the existing test SELinux domain in this pass.

## Baseline observations

- Rocky Linux 10.2, x86_64.
- SELinux: `Enforcing`.
- Docker Engine: 29.8.0.
- Docker security options include `name=selinux` and `name=seccomp`.

## Cases

| Case | Status | Notes |
| --- | --- | --- |
| Go executable transition | NOT RUN | Existing policy targets the copied Python fixture path. |
| Dedicated helper domain | NOT RUN | Requires Go executable labeling and policy-path update. |
| UDS, SQLite, Docker, `openat2` under domain | NOT RUN | Must be run without changing Enforcing mode. |
| Negative home/root/etc/IP/exec probes | NOT RUN | Requires Go-specific domain run. |
| AVC review | NOT RUN | No Go-domain run occurred. |

Rocky remains experimental and does not block Debian readiness.
