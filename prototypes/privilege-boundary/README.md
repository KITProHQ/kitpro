# Privilege-boundary test fixture

> **Disposable experiment only.** This directory is not production KITPro code. Do not install it on a machine that contains important workloads. Python was chosen for a small, auditable experiment and does not select KITPro's production language.

This fixture tests the main claims in [ADR-0003](../../docs/decisions/0003-privileged-helper-protocol.md) and [ADR-0004](../../docs/decisions/0004-docker-integration.md):

```text
unprivileged test client
        |
        | length-prefixed, typed JSON over AF_UNIX
        v
root test helper
        |
        | fixed Docker Engine API calls
        v
Docker Engine Unix socket
```

The socket unit controls who can connect. The helper verifies `SO_PEERCRED` on every connection and accepts only the configured API-test UID. It then parses a closed request schema and applies its own policy. There is no shared secret.

Compromise of the API-test identity permits every semantic operation authorized to that identity. The helper limits what those operations can mean. It does not prove that a human approved a request.

## Implemented operations

| Operation | Caller-controlled values | Effect |
| --- | --- | --- |
| `Ping` | None | Return protocol and authenticated peer observations |
| `InspectRuntime` | None | Read a bounded Docker version response |
| `CreateTestContainer` | Validated test instance ID | Create one internal bridge and one fixed-shape, digest-pinned test container |
| `InspectTestContainer` | Validated test instance ID | Inspect only a resource with matching helper state and labels |
| `StartTestContainer` | Validated test instance ID | Start the proven-owned test container |
| `StopTestContainer` | Validated test instance ID | Stop the proven-owned test container |
| `RemoveTestContainer` | Validated test instance ID | Remove the proven-owned container and disposable network |
| `PrepareTestDirectory` | Validated storage-slot ID | Create or inspect one directory beneath the fixed test root |
| `GetOperation` | Operation UUID | Return a stored operation receipt |

The protocol cannot carry a Docker ID for a lifecycle request. It cannot carry a host path, command, Compose document, Docker request, mount, device, capability, security option, sysctl, namespace choice, port binding, image, or environment entry.

The helper always constructs the Docker request. Its only container shape is:

- the root filesystem is read-only;
- all Linux capabilities are dropped;
- `no-new-privileges` is set;
- the process uses UID and GID 65534;
- no device or bind mount is present;
- no host port is published;
- only the instance's internal test network is attached; and
- the image must be a fully qualified `sha256` registry digest for `linux/amd64`.

The command inside the fixed test container is hard-coded fixture behavior. It is not caller input and is not a general command interface.

## Test ownership namespace

The fixture uses `invalid.kitpro.privilege-boundary-test.*`. The `invalid` top-level name makes clear that this is not the production reverse-domain namespace. Do not create production or persistent Docker resources with these labels.

Ownership requires all of the following:

1. a protected helper record for the semantic instance and resource type;
2. the exact Docker object ID recorded by the helper;
3. the expected fixture-generated object name;
4. every expected test label, including the fixture run, instance, and resource type;
5. the recorded immutable image identity for containers;
6. exactly the expected network attachment; and
7. no unexpected member on the instance network, including created or stopped endpoints omitted by Docker's network-inspect member map.

Labels alone are not ownership. A record alone is not ownership. Disagreement blocks inspection and mutation. Removal verifies every target before deleting the first object.

## Repository-only tests

Run from this directory:

```sh
python3 -m unittest discover -s tests -v
```

These tests need only the Python standard library. They exercise framing, parsing, peer credentials, helper and client restarts, concurrent clients, idempotency receipts, fixed Docker request construction, ownership disagreement through a fake Docker boundary, and descriptor-relative filesystem handling. They do not contact Docker or require root.

Some restrictive execution sandboxes prohibit Unix sockets. In that case, run the same command in a disposable local environment and record the restriction separately from the test result.

## Reference-VM execution

Use [RUNBOOK.md](RUNBOOK.md) on a disposable Debian 13 or Rocky Linux 10 VM. The runbook deliberately separates distribution package setup, systemd installation, SELinux inspection, Docker tests, and cleanup. Never run it on a host with unrelated workloads.

The fixture has no production installer. Every privileged setup command is shown for review. The test operator must snapshot the VM first and must record the selected image's actual immutable digest before the helper starts.

## Known fixture limitations

- The JSON structures are hand-written experimental types, not a production schema toolchain.
- Operation receipts use a root-owned JSON file. A `running` receipt after a crash returns `RecoveryRequired`; it does not reconcile automatically.
- Receipt storage has an in-process lock, not a cross-process lock. Stop the helper before using the fault-injection state tool.
- The fixture serializes all mutations with one conservative lock and bounds accepted concurrent connections at eight. Production needs instance-scoped locks and an explicit request-rate policy.
- Accepted work continues after a client disconnect. This small fixture does not implement safe-point cancellation.
- Docker pulls are test setup, not a helper operation. The helper only accepts an image already present by digest.
- The filesystem experiment covers directory preparation. It does not claim to validate recursive deletion, ownership transfer, backup, restore, or a production data layout.
- Mount substitution and high-frequency race tests require the disposable VM harness described in the runbook.
- The systemd hardening profile is a candidate to validate on both platforms. Docker socket authority still makes a compromised helper root-equivalent.
- Rocky systemd 257 made `openat2` return `ENOSYS` when the test unit used `RestrictSUIDSGID=yes`; the fixture omits that defense-in-depth setting pending a production hardening review.
- Docker 29 omits created and stopped endpoints from network-inspect membership. The fixture therefore performs a bounded all-container query by exact network name before lifecycle and destructive operations.
- The fixture uses an internal network and intentionally tests no reverse proxy or public exposure.

## Safety

All fixture Docker names start with `kitpro-pb-test-`, all fixture labels use the test-only namespace, and every operation is scoped to one run ID. Cleanup must still inspect labels and names before removal. The helper never performs wildcard cleanup.
