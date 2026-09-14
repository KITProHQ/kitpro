# Rocky Linux 10 experimental-support backlog

Rocky Linux 10 is a secondary experimental KITPro host. This backlog does not block Debian 13 Phase 1 work or change the platform-neutral helper and Docker contracts.

| Work | Current evidence | Completion requirement |
| --- | --- | --- |
| Clean corrected installation | `NOT RUN` from a clean snapshot | Run `tools/install-docker.sh` from `clean-os`. Verify that it creates or preserves safe daemon configuration, restarts Docker when required, reports `name=selinux`, and leaves SELinux Enforcing. |
| `selinux-enabled` installer regression | Repository render and restart-required tests pass | Add host-level cases for absent, valid, invalid, and conflicting `daemon.json` files. The installer must not overwrite administrator configuration. |
| Dedicated helper SELinux domain | `PASS` for test-only feasibility: a dedicated domain compiled/loaded and ran under Enforcing; production packaging remains open | Define a packaged executable transition and grant only the measured Unix-socket, state, audit, filesystem, and Docker-socket access. Do not use a broad permissive domain. |
| Socket, runtime, and state labels | `PASS` for test-only types and relabeling; package lifecycle not run | Define dedicated types and verify creation, restart, package upgrade, relabel, and removal behavior. |
| firewalld regression | One real no-port run passed | Test Docker zone and forwarding behavior across install, restart, application-network creation, cleanup, and future loopback publication without rewriting global policy. |
| Rocky package upgrades | `NOT RUN` | Test supported point-release and security updates from a recorded snapshot. |
| Docker upgrades | `NOT RUN` | Test Engine API compatibility, SELinux configuration preservation, networking, ownership checks, and service recovery across each supported Docker update. |
| Rollback | `NOT RUN` | Define and test package, daemon-configuration, helper-policy, and Docker-version rollback without disabling SELinux. |
| x86-64-v3 documentation | The validation CPU passed | Document the requirement before users install Rocky. Provide a preflight check and explain that older AMD and Intel systems may be incompatible. |

Keep Rocky experimental until the clean-install, helper-domain, upgrade, and rollback rows pass. Record each real-host run as a new immutable result.
