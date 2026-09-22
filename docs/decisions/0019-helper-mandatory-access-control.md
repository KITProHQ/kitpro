# ADR-0019: Privileged helper mandatory-access-control confinement

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Related principles: Secure defaults, Inspectability, Reversibility, Open foundations
- Related decisions: ADR-0003, ADR-0004, ADR-0016, ADR-0017, ADR-0018

## Context

The privileged helper runs as root and can reach the rootful Docker Engine socket. Unix ownership, `SO_PEERCRED`, semantic operation policy, ownership records, and systemd restrictions reduce the authority reachable from the API. They do not contain a helper compromise by themselves.

Supported Debian 13 and Arch Linux hosts use AppArmor. Ubuntu 26.04 LTS uses
the same AppArmor boundary for development and validation only.
Rocky Linux 10 uses SELinux and Docker requires explicit SELinux integration
to label containers. The two systems need different policy mechanisms. The
helper protocol and operation model must remain distribution-neutral.

The helper's legitimate authority is narrow:

- connect to the local Docker Engine Unix socket;
- inspect and create only validated KITPro resources;
- read bounded logs and image metadata;
- read and write helper-owned receipts, ownership records, leases, and audit events;
- create and inspect directories below approved KITPro storage roots;
- use its runtime directory and inherited systemd socket; and
- read the minimum system facts needed to validate operations.

It does not need general access to `/home`, `/root`, arbitrary `/etc`, SSH keys, unrelated application data, package-manager state, devices, user sessions, or IP networking. It does not need a shell, Docker CLI, Compose CLI, or arbitrary child process.

## Decision

KITPro will add a platform-specific mandatory-access-control layer around the helper while keeping the protocol, ownership model, and Docker Engine abstraction common.

The first production release must ship a useful enforcing helper profile on
Debian, Ubuntu, and Arch when the profile passes the complete helper and Docker
validation suite. The profile must deny unrelated host reads and writes,
arbitrary execution, and unnecessary IP networking. AppArmor policy is a
release requirement on every supported host, not an optional tuning exercise.

Rocky remains experimental until a dedicated SELinux helper domain, file contexts, package lifecycle, and upgrade tests pass with SELinux Enforcing. Rocky must not use a permissive helper domain or disable SELinux. The test policy under [`prototypes/privilege-boundary/selinux/`](../../prototypes/privilege-boundary/selinux/) is a feasibility fixture, not a production module.

MAC is defense in depth. Docker socket access remains powerful host authority. A compromised helper can ask Docker to create a privileged container, mount `/`, use host namespaces, attach devices, or change container security settings unless the helper's semantic policy rejects those requests. MAC does not replace that validation.

## Common security goals

All supported platforms must:

1. confine the helper executable to a dedicated domain or profile;
2. permit only the helper state, runtime, audit, and approved storage roots;
3. permit the Docker Unix socket without exposing a generic runtime API to the browser-facing service;
4. deny arbitrary shell and unrelated binary execution;
5. deny general IP networking unless a later operation has an accepted need;
6. preserve Unix DAC, helper validation, ownership checks, and postcondition inspection as separate controls; and
7. fail closed when policy loading or the required confined context cannot be verified.

The helper profile must not grant arbitrary host filesystem access through a recursive root rule. Rules that are needed only by a language runtime or libc must be named and reviewed. Compatibility rules must not silently become authority for application data, credentials, devices, or host configuration.

## AppArmor strategy

The Debian-compatible and Arch package adapters install and load the same
profile attached to the packaged helper executable. The production executable
path and profile name are packaging decisions, not protocol values. The
profile mediates:

- the helper executable and required interpreter or runtime libraries;
- the root-owned helper state and audit paths;
- the runtime directory, socket handoff, and Docker Unix socket;
- the approved storage roots; and
- narrowly reviewed reads of `/proc`, `/sys`, `/etc/passwd`, `/etc/group`, and locale or loader data where required.

The profile will deny or omit `/home`, `/root`, SSH material, arbitrary `/etc` writes, unrelated application data, Docker storage, devices, shells, unrelated binaries, and IP networking. Unix socket mediation is required. The profile must be tested in enforcing mode, including helper restart and Docker outage recovery.

The profile cannot constrain actions that the Docker daemon performs after the helper sends an allowed Engine API request. The helper therefore retains responsibility for rejecting host mounts, host namespaces, privileged mode, devices, capabilities, security-option changes, arbitrary ports, and raw API forwarding.

## Rocky SELinux strategy

The Rocky adapter will ship a dedicated executable transition and types for the helper domain, executable, runtime directory and socket, helper state and audit data, and approved storage roots. It will use the narrowest installed Docker policy interface that permits the helper's required socket connection. It will not grant a general container-management domain or generic root-daemon permissions.

The helper domain will receive no generic TCP or UDP network permission and no shell or unrelated binary execution. The policy must preserve Docker's `selinux-enabled` configuration and normal `container_t` or equivalent workload labels. A denial is evidence to classify, not a reason to run permissive or copy all `audit2allow` output into the module.

Rocky package and policy work remains experimental. It includes file-context installation and `restorecon`, executable-transition verification, policy upgrades, Docker upgrades, firewalld regression tests, and rollback tests. A Rocky host that cannot load the dedicated policy while Enforcing remains outside official Phase 1 support.

## Systemd interaction and hardening

The helper remains a root-owned, socket-activated service with no network listener. Systemd provides an additional boundary:

- `NoNewPrivileges=yes`;
- an empty capability bounding and ambient set;
- `PrivateTmp=yes` and `ProtectHome=yes`;
- `ProtectSystem=strict` with explicit writable helper state;
- kernel, control-group, hostname, clock, device, namespace, realtime, and personality restrictions where the fixture still works;
- `RestrictAddressFamilies=AF_UNIX`;
- memory, task, file-descriptor, timeout, restart, and umask limits; and
- a sanitized environment.

The fixture omits `RestrictSUIDSGID=yes`. Rocky systemd 257 made the required descriptor-relative `openat2` call return `ENOSYS` when that setting was present. The setting is useful defense in depth but cannot replace the primary path-containment control. Production must either reproduce a working equivalent or document the omission with a targeted alternative. It must not trade away descriptor-relative containment to enable this one restriction.

Systemd hardening is not a substitute for MAC. Docker socket authority also limits what a generic sandbox can contain. Each setting must be validated against the helper's actual system calls and storage behavior before production packaging.

## Child processes and networking

The helper does not require external execution in the approved architecture. Docker communication uses the Engine API over the local Unix socket. The production helper will not invoke a shell, Docker CLI, Compose CLI, package manager, firewall CLI, or user-provided executable. MAC and systemd should reinforce this by denying unrelated execution.

The helper has no outbound IP requirement. Host inspection that needs network data belongs in a separate constrained component or uses local kernel and system interfaces. Any future exception requires a new operation, an explicit destination policy, and a new validation case.

## Packaging implications

Debian-compatible and Arch installation needs a profile file owned by the
KITPro package, a load and reload step, profile-state checks, upgrade-safe
replacement, and a defined failure if the profile cannot load. On Arch,
AppArmor must also be present in the active kernel LSM list before installation.
The installer must not silently fall back to an unconfined helper.

Rocky installation eventually needs a versioned SELinux policy package or module, executable and data file contexts, `restorecon` handling, policy upgrade and removal rules, and a defined failure if the helper transition or required context is unavailable. Neither platform may weaken MAC to complete installation.

## Consequences

### Benefits

- A helper compromise meets an independent host-enforced restriction beyond Unix DAC.
- Unrelated host files, shells, child processes, and IP networking become separately testable denial cases.
- Debian receives a release-relevant AppArmor boundary without coupling the protocol to Debian.
- Rocky's extra policy work stays explicit and does not block Debian development.

### Costs

- Profiles must track runtime, systemd, Docker, and package changes.
- AppArmor and SELinux require separate build, load, upgrade, and diagnostic workflows.
- Docker socket access remains a high-trust boundary even under MAC.
- Incorrectly narrow policy can break recovery or storage operations. Incorrectly broad policy can create false confidence.

## Validation plan

The test-only [MAC runbook](../../prototypes/privilege-boundary/MAC-RUNBOOK.md)
must run on each candidate platform. It must record positive lifecycle tests,
negative filesystem and process tests, contexts, denials, Docker security
labels, helper restart, Docker outage recovery, and combined systemd plus MAC
behavior. Native package certification adds profile installation, reload,
upgrade, removal, and reboot evidence for each supported platform.

The Debian result must show AppArmor `enforce` for the helper and successful Docker lifecycle, state, socket, storage, ownership, and recovery tests. The Rocky result must show SELinux `Enforcing`, a dedicated helper domain, normal container labels, successful lifecycle and recovery tests, and no broad policy workaround.

The test records must distinguish `PASS`, `FAIL`, `BLOCKED`, `NOT RUN`, and `OBSERVATION`. A policy that loads but leaves the helper unconfined is not a pass.

The disposable feasibility runs are recorded in the [Debian AppArmor result](../testing/results/2026-09-12-debian-helper-apparmor.md) and [Rocky SELinux result](../testing/results/2026-09-12-rocky-helper-selinux.md). Both passed bounded helper lifecycle tests under enforcing MAC; neither is production package, upgrade, or rollback acceptance.

## Open questions

- What production executable path and packaging layout allow stable AppArmor attachment across runtime updates?
- Which exact AppArmor rules are required after the helper implementation language is chosen?
- Which Docker socket type and policy interface are stable across Rocky 10 updates?
- Does Rocky require a narrow custom rule for the helper-to-Docker socket, and can package upgrades preserve it?
- Which systemd hardening equivalent replaces `RestrictSUIDSGID` without breaking `openat2`?
- How should policy load failure appear during first install and package upgrade?
- Which audit and diagnostic data can be exposed without granting the API access to helper policy or state?

## Review conditions

Revisit this ADR if:

- Debian cannot run the complete Phase 1 helper workflow under an enforcing AppArmor profile;
- Rocky cannot run the helper in a dedicated domain while Enforcing;
- a required Docker operation needs generic shell, network, or filesystem authority;
- systemd or MAC restrictions conflict with descriptor-relative path safety;
- Docker socket access makes the proposed profile misleading about helper compromise; or
- package upgrades repeatedly require manual policy repair.
