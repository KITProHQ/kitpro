# Debian reference-host test plan

## Purpose

This plan defines the disposable host used to validate ADR-0003 and ADR-0004 before production code begins. It does not provision a VM. Josh accepted ADR-0017 after the Debian and Rocky runs completed.

ADR-0017 names Debian 13 as the primary/reference host and Rocky Linux 10 as secondary/experimental. The tests avoid Debian-only assumptions so that the same protocol and Docker cases run on a Rocky or RHEL-family SELinux-enforcing host.

## Reference VM

Use this initial machine:

| Item | Baseline |
| --- | --- |
| Distribution | Debian 13 with current security and point-release updates |
| Architecture | `amd64` |
| CPU | 2 virtual CPUs |
| Memory | 4 GiB |
| System disk | 40 GiB, local, ext4 |
| Init | systemd as PID 1 |
| Control groups | unified cgroup v2 hierarchy |
| Network | private test network with no public forwarding |
| Existing workloads | none |
| Virtualization | full VM with console and snapshot support |

The 20 GiB free-space minimum in ADR-0017 is KITPro and system capacity. It covers KITPro, Docker Engine, container images, logs, update staging, and normal operating headroom. It does not cover application data, media libraries, photos, databases, backups, or other workload content. Plan and test that capacity separately for each application. The 40 GiB VM disk gives the architecture tests room to run. It is not a claim that 40 GiB fits a real application.

## Base installation assumptions

Start from a clean minimal server installation:

- systemd runs as PID 1;
- cgroup v2 exposes the controllers required by Docker;
- the system disk is ext4 with known free-byte and free-inode counts;
- the VM clock is synchronized;
- the test administrator has console access and explicit sudo or root access;
- no pre-existing containers, Docker networks, custom Docker configuration, reverse proxy, or application data exists;
- the host has no public route or inbound router forwarding;
- the test network includes a second machine for exposure checks; and
- the hypervisor can take powered-off or application-consistent snapshots.

Record `/etc/os-release`, kernel, systemd, cgroup, CPU, memory, mount, filesystem, listener, route, firewall, and SELinux status before each run. Recording a fact does not authorize the test harness to change it.

## Docker installation expectations

Provision Docker with [`tools/install-docker.sh`](../../tools/install-docker.sh) through the disposable-host adapter in [`tools/platform-validation/`](../../tools/platform-validation/). The cross-distribution installer owns repository, package, service, and verification behavior. The helper protocol remains independent of APT, DNF, package locations, and distribution service defaults.

The test Docker setup must meet these conditions:

- rootful Docker Engine uses its local Unix socket;
- no unauthenticated or TLS Docker TCP listener is enabled;
- the API test identity is not in the `docker` group and cannot open the socket;
- the helper test identity is the only KITPro component that can open the socket;
- Docker's exact package source, package versions, daemon version, API range, storage driver, cgroup driver, firewall backend, socket path, and unit state are recorded;
- the daemon starts under systemd and survives a normal host restart; and
- the tests do not change the daemon configuration to make KITPro pass.

The first accepted Docker version range must pass the loopback-port, IPv6, firewall, label, health, log, restart, and API-version tests in this plan.

The first 2026-09-12 Debian attempt stopped before Docker installation because `/` had only 7.0 GiB free. That evidence remains in the [immutable blocked result](results/2026-09-12-debian13-validation.md). The disposable host was then corrected to a 40 GiB ext4 root with 36.6 GiB free and checkpointed as `storage-corrected-clean-os`. Docker installation and the real-host fixture subsequently completed; see the [storage-remediation record](results/2026-09-12-debian13-storage-remediation.md) and [completed validation record](results/2026-09-12-debian13-completed-validation.md).

## Snapshot strategy

Use named snapshots so that every destructive test starts from known state:

1. `clean-os`: clean operating system before Docker.
2. `docker-installed`: Docker installed and verified, before any KITPro test artifacts.
3. `helper-installed`: test-only helper protocol fixture and systemd units installed, before Docker objects.
4. `managed-fixture`: one labeled test application fixture in a known healthy state.

Revert to the nearest prior snapshot after a destructive or failure-injection group. Do not reuse a manually repaired VM as proof. Record the snapshot identifier, host inventory, test revision, and result for every run.

Back up no real data to this VM. Use fixture-only directories, volumes, images, and credentials.

## Tests required before coding

Architecture acceptance requires an isolated prototype or test fixture, not the production application. The fixture must prove the interface shape before the project selects its implementation language.

### Host baseline

- Verify systemd socket activation and service restart behavior.
- Verify cgroup v2 and required controllers.
- Verify that the kernel supports `SO_PEERCRED` and the selected descriptor-relative path controls.
- Record the Docker Unix socket owner, group, mode, and SELinux context when present.
- Verify local ext4 mount identity, free space, free inodes, and behavior when a declared test mount disappears.
- Confirm that no test depends on APT, `dpkg`, Debian unit paths, or Debian firewall defaults after fixture installation.

### Privilege-boundary tests

- Create a root-owned Unix stream socket through a systemd socket unit.
- Set the parent directory and socket ownership and modes to the ADR-0003 values.
- Verify that the helper receives the API test UID through `SO_PEERCRED`.
- Verify that an unauthorized UID cannot connect when permissions are correct.
- Temporarily loosen the fixture socket inside the disposable VM and verify that the helper still rejects the unauthorized UID.
- On an SELinux-enforcing companion VM, verify a peer-domain rule and denial while discretionary access still permits the connection.
- Verify that the browser-facing fixture has no Docker socket, helper state, helper policy, or root-owned audit access.
- Verify that no shared secret is required for the fixed service identity.
- Compromise the API fixture and verify that it can request allowed semantic operations but cannot expand their vocabulary, targets, paths, Docker grants, or destructive scope.
- Verify that administrator or session metadata changes audit context only and grants no helper authority.

### Protocol tests

- Accept one valid read request and one valid mutation fixture.
- Reject an unknown operation, field, enum value, protocol version, and operation revision.
- Reject malformed UTF-8, invalid JSON, duplicate keys, truncated framing, extra bytes, and a declared length over 1 MiB.
- Reject invalid installation, application, instance, role, storage-slot, and operation IDs.
- Return stable typed errors without raw secrets or unbounded dependency output.
- Verify request and operation IDs in responses, logs, and audit events.
- Bound open connections, in-flight requests, request rate, response size, and log reads.

### Idempotency and recovery tests

- Send the same operation ID and body twice. Verify one mutation and one stored result.
- Reuse an operation ID with a different body. Verify `OperationConflict` before mutation.
- Disconnect the API after acceptance. Verify that the operation reaches a recorded result or recovery state.
- Restart the API while the helper runs. Verify status recovery through `GetOperation`.
- Kill and restart the helper after each externally visible operation step.
- Restart Docker during image pull, container creation, start, stop, log read, and removal.
- Restart the host with queued, running, partially applied, and completed operations.
- Request cancellation before acceptance, during a safe step, and during a non-cancellable step.
- Verify that a deadline or disconnected client never appears as proof of rollback.
- Run conflicting mutations for one instance and bounded independent reads for separate instances.

### Filesystem security-negative tests

For every case, verify no write, ownership change, permission change, move, or deletion occurs outside the fixture root:

- `..` traversal and absolute paths;
- empty, overlong, NUL-containing, and Unicode-confusable identifiers;
- a symbolic link in every path component and at the final component;
- a procfs magic link;
- a hard link to a root-owned test file;
- a bind mount that replaces a checked directory;
- an unmounted application-data target with a writable underlying directory;
- a mount change between validation and mutation;
- a path on an unexpected filesystem or mount ID;
- caller-supplied UID, GID, mode, SELinux context, or relabel option; and
- concurrent rename, link, and mount races during the operation.

Run race tests many times with deterministic barriers around the check and mutation points. A string-prefix check does not count as a passing implementation.

### Docker integration tests

- Query `/version` and the approved `/info` fields through the Unix socket.
- Verify API-version selection inside KITPro's tested range.
- Reject a non-Docker or untested Docker-compatible socket.
- Exercise exact-digest image inspect and pull for `linux/amd64`.
- Record and compare the Docker image ID and repository digest. Compare approved index and platform-manifest digests when catalog metadata provides them.
- Create, inspect, start, stop, read bounded logs from, and remove a fixture container through the approved Engine API calls.
- Read Docker health state from a fixture with deterministic healthy and unhealthy transitions.
- Create and remove one fixture application network per instance.
- Create and inspect a fixture named volume without deleting it during normal uninstall.
- Verify that no test calls Docker build, exec, attach, archive, commit, prune, plugin, Swarm, or daemon-configuration endpoints.
- Verify that the fixture does not need the Docker CLI after installation.

### Docker ownership tests

Create only disposable resources with the fixture's `invalid.kitpro.privilege-boundary-test.*` labels and a matching helper-owned authority record. This namespace is intentionally invalid for production and does not settle KITPro's future reverse-domain label prefix. Then test every disagreement:

- record and exact labels match;
- record exists and the object is missing;
- labels exist and no record exists;
- record exists and a label is missing;
- record and label values conflict;
- two objects claim the same semantic identity;
- a foreign object uses a KITPro-like name;
- a user changes a container setting by recreating it;
- a user removes and recreates an object with the same name; and
- a resource comes from another KITPro installation ID.

Only the exact match may receive an automatic mutation. Orphan, conflict, drift, and ambiguity cases must preserve the Docker object and report the reason.

### Application-policy security-negative tests

The helper must refuse a request that attempts any of these changes:

- arbitrary container creation;
- privileged mode;
- host PID, IPC, user, UTS, or network namespace;
- added capabilities;
- arbitrary devices or device rules;
- a Docker or helper socket mount;
- `/`, `/etc`, `/proc`, `/sys`, or `/dev` as a bind mount;
- a bind mount outside an approved storage slot;
- another application's storage or network;
- an arbitrary command, entry point, user, environment key, or secret;
- a raw security option, SELinux label change, seccomp change, or sysctl;
- an unqualified or unapproved image;
- an arbitrary registry credential;
- an arbitrary host IP or port; or
- deletion by caller-supplied Docker container, network, or volume ID.

The helper must refuse the same requests when they use duplicate fields, type confusion, invalid Unicode, integer edge cases, nested unknown data, or another malformed encoding.

### Network tests

- Give each of two fixture instances its own user-defined bridge network.
- Verify that Docker name resolution and direct container traffic do not cross those networks.
- Verify that neither instance joins the default bridge or a user-created network.
- Verify no host port exists when the plan declares none.
- Publish an approved unprivileged test port to IPv4 loopback and test it from the host and a second machine.
- Repeat the test for IPv6 loopback as a separate case.
- Verify that wildcard, non-loopback, privileged-port, publish-all, host-network, and conflicting-port requests fail.
- Record Docker's firewall backend and effective rules without changing the host's selected backend.
- Repeat the network cases with the supported firewalld and nftables arrangements before RHEL-family support.

### Image and update tests

- Deploy a fixture by a fully qualified digest reference.
- Move its human-readable test tag to another digest in an isolated registry. Verify that the existing release does not change.
- Verify that update discovery reports the new candidate but cannot deploy it without a new approved release.
- Reject a digest mismatch, registry mismatch, repository mismatch, platform mismatch, stale release, and missing trust record.
- Verify offline start of an already present approved image.
- Verify a clear failure when an absent image cannot be pulled.
- Keep the prior image during the test rollback window and restore the prior container definition by exact digest.
- Verify that image rollback does not claim to restore modified fixture data.

### Destructive-operation tests

- Try to remove a foreign container by name and by Docker ID.
- Copy all KITPro labels to a foreign container without creating a helper record, then try to remove it.
- Try to remove an unknown Docker network and named volume.
- Remove the helper record before a cleanup request and verify that the object remains.
- Corrupt the helper authority registry and verify that cleanup stops.
- Run normal uninstall and verify that persistent directories and named data volumes remain.
- Interrupt removal after each resource step and verify that retry preserves unknown and persistent resources.
- Verify that all destructive requests require explicit operation intent and create audit events.

### Audit and secret tests

- Correlate request, operation, application, instance, Docker object, and audit identifiers.
- Verify start, policy denial, completion, partial failure, cancellation, and recovery events.
- Inject known marker secrets into registry credentials, application configuration, Docker errors, and container logs.
- Search normal logs, audit events, responses, labels, environment diagnostics, and error details for each marker.
- Verify that the API cannot read the helper's protected authority records or alter authoritative audit events.

## Required API isolation results

The unprivileged API fixture must fail every attempt to:

- read or connect to the Docker socket;
- invoke an arbitrary root command;
- write an arbitrary root-owned file;
- create an arbitrary Docker container;
- mount the host root filesystem;
- request a privileged container;
- delete a non-KITPro container;
- manipulate an unknown Docker volume; or
- bypass helper policy with a malformed request.

The helper must refuse:

- unknown operations;
- invalid instance IDs;
- unowned or ambiguous Docker resources;
- arbitrary paths;
- forbidden mounts;
- forbidden capabilities;
- oversized and malformed requests; and
- unauthorized socket clients.

Any unexpected success is a release blocker. A failed test must preserve foreign Docker objects and files outside the fixture roots.

## Rocky Linux and RHEL-family companion lane

Before choosing or officially supporting a RHEL-family reference host, run the same protocol and Docker suite on a current candidate with SELinux enforcing. Add only these host-specific inputs:

- RPM package installation and ownership;
- systemd unit and tmpfiles packaging paths;
- Docker Engine source and supported version;
- firewalld and nftables interaction;
- SELinux domains, socket labels, file contexts, container-storage contexts, denials, and policy packaging; and
- distribution-upgrade behavior.

Do not change the JSON protocol, semantic operations, ownership proof, or application plan to pass this lane. If the lane needs a new security concept, revisit ADR-0003 or ADR-0004 before adding a distribution branch. Never disable SELinux to turn a failure into a pass.

## Test record

Each run records:

- VM image and snapshot identifiers;
- distribution, kernel, systemd, Docker, API, cgroup, filesystem, firewall, and SELinux versions or state;
- test-suite revision and configuration;
- every command and fixture artifact needed to reproduce the run;
- pass, fail, skip, and blocked counts with reasons;
- host inventory before and after the run;
- Docker object inventory before and after the run; and
- retained application-data fixtures after uninstall.

No test result from the current development sandbox counts as reference-host acceptance. The sandbox is not Debian 13, does not run systemd as PID 1, and does not permit Docker Engine access.

## Current development-host observation

A read-only check on 2026-09-12 found:

- Linux `7.1.8-arch1-3` on `x86_64`;
- cgroup v2 mounted at `/sys/fs/cgroup`;
- systemd tools installed, but the validation sandbox process runs as PID 1;
- Docker CLI `29.7.2` installed;
- `/run/docker.sock` present with mode `0660` through the sandbox mapping;
- file-mode checks report that the current mapped account can read and write the mapped Docker socket, while the validation sandbox blocks an actual connection; and
- no `getenforce` command in the sandbox.

These results confirm that this machine is not a safe reference environment for systemd activation, Docker API, label, port, container, or SELinux tests. The mode result also shows why the VM must check the actual API service identity and all supplementary groups: sandbox denial is not a security control. No Docker objects, services, users, firewall rules, or host files were changed. The planned experiments remain assigned to disposable VMs.

The test-only fixture and exact execution procedure are in [`prototypes/privilege-boundary/`](../../prototypes/privilege-boundary/). Platform comparison and immutable result records are in [`platform-comparison.md`](platform-comparison.md) and [`results/`](results/).

## Exit criteria

The architecture investigation is complete when:

- every required negative test has an executable fixture and expected result;
- the protocol passes identity, parser, idempotency, restart, and path-race tests;
- the Docker adapter passes its endpoint and field allowlist tests;
- ownership conflicts preserve resources;
- normal uninstall preserves application data;
- no application or helper test needs a public network;
- the Debian reference run is reproducible from `clean-os`; and
- the open RHEL-family questions are recorded without disabling SELinux or adding Debian behavior to the core protocol.

Passing this plan validates the privilege and Docker boundaries. It does not accept the backend language, frontend, database, authentication, TLS, reverse proxy, backup, cloud, application specification, or final data layout.
