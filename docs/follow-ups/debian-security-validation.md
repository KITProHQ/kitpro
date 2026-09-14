# Debian 13 security-validation backlog

Debian 13 is the Phase 1 primary/reference host. Its real-host fixture passed, but these production security and recovery tests remain open.

| Work | Status | Completion requirement |
| --- | --- | --- |
| Dedicated helper AppArmor profile | `PASS` test-only enforcing profile; production package/runtime and upgrade validation remain open | Constrain the packaged helper's executable, state, runtime, audit, storage, and Docker-socket access without changing the helper protocol. Required before the first Phase 1 release if the final runtime can pass the same confinement checks. |
| Debian package upgrades | `NOT RUN` | Test supported point-release and security updates from a recorded snapshot. |
| Docker upgrades | `NOT RUN` | Test Engine API compatibility, AppArmor confinement, networking, ownership checks, and service recovery across each supported Docker update. |
| Rollback | `NOT RUN` | Define and test package, service, configuration, and Docker-version rollback. |
| High-frequency filesystem races | `NOT RUN` | Add deterministic rename, link, symlink, and mount race barriers around descriptor-relative mutations. |
| Hard-link-sensitive mutations | `NOT RUN` | Add tests when the helper gains a file mutation that can encounter hard links. The directory-only prototype cannot prove this case. |
| Interrupted Docker operations | `NOT RUN` | Interrupt each externally visible mutation and prove durable reconciliation after helper, Docker, and host restart. |
| Loopback publication | `NOT RUN` | Before adding a port operation, verify IPv4 and IPv6 loopback binding, non-loopback denial, Docker firewall behavior, and conflict handling. |

Do not treat the completed prototype as evidence for these rows. Each row needs a new result tied to the production design or a purpose-built disposable fixture.
