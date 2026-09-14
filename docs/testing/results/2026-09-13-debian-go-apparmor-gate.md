# Debian Go AppArmor production-gate result — 2026-09-13

**DEBIAN GO APPARMOR GATE: FAIL**

This immutable record covers only the compiled Go helper AppArmor gate on VM 500. No architecture decision was changed.

## Access and baseline

Command:

```sh
ssh -o BatchMode=yes josh@10.10.0.115 'hostname && sudo -n true && echo SUDO_OK'
```

Output: `debian13`, `SUDO_OK`; exit code 0.

AppArmor baseline: module loaded, service `active`, `apparmor-utils` already installed, and `apparmor_parser` became available. Docker remained installed and unchanged.

Disposable identities:

```text
kitpro-api-test  UID/GID 987/987, nologin, not in docker
kitpro-helper-test UID/GID 986/986, nologin, not in docker
docker group: docker:x:989:
```

## Build

The existing `prototypes/go-helper-validation` helper was built with `CGO_ENABLED=0`, `-trimpath`, and an empty build ID.

```text
sha256: c1069d2c913687d64d9499955a01e35e18aa66e1528d9132b5054a07afc5ff3c
file: ELF 64-bit LSB executable, x86-64, statically linked
```

The binary was installed temporarily as `/usr/libexec/kitpro-helper-validation`, root-owned and mode `0755`.

## Profile and process confinement

The disposable profile `/etc/apparmor.d/kitpro-helper-validation` parsed and loaded successfully. `aa-status` showed:

```text
/usr/libexec/kitpro-helper-validation (PID) kitpro-helper-validation
```

`cat /proc/<PID>/attr/current` returned:

```text
kitpro-helper-validation (enforce)
```

The profile was enforcing, not unconfined.

## Positive operations

| Operation | Status | Evidence |
| --- | --- | --- |
| Helper start | PASS | Process started under the named enforcing profile. |
| API UID peer authentication | PASS | API UID 987 connected after the disposable socket group was set to `kitpro-api-test`. |
| `Ping` | PASS | Framed response returned `ok=true` and a stable SHA-256 request hash. |
| `InspectDocker` | PASS | Docker Engine 29.8.0/API 1.56 response returned through direct HTTP-over-Unix. |
| Approved storage preparation | PASS | `/srv/kitpro/validation/go-slot` was created. |
| Helper SQLite receipt | PASS | Helper database was created root-owned with mode `0600`. |
| `openat2` operation | NOT RUN | The probe's current `PrepareTestDirectory` path check was not instrumented to return an explicit `openat2` result over the socket. |
| Graceful shutdown/restart | FAIL | Initial helper ran; a later restart attempt encountered a disposable SQLite `SQLITE_BUSY` startup failure and did not recreate the socket. |

## Negative operations

The helper's `NegativeProbe` operation was executed while confined. Results:

| Attempt | Result |
| --- | --- |
| `/home/josh/.profile` read | DENIED (`permission denied`) |
| `/home/josh/.ssh/id_ed25519` read | DENIED (`permission denied`) |
| `/root/.bashrc` read | DENIED (`permission denied`) |
| `/etc/kitpro-probe-denied` access | DENIED by profile; target did not exist |
| `/srv/other/marker` access | DENIED by profile; target did not exist |
| AF_INET socket | DENIED (`socket: permission denied`) |
| AF_INET6 socket | NOT CONCLUSIVE in this run; earlier address-family probe used an unavailable IPv6 listener address |
| `/bin/sh` execution | DENIED (`Permission denied`) |
| `/usr/bin/id` execution | DENIED (`Permission denied`) |

Kernel audit output included expected AppArmor capability denials for `cap_dac_read_search` and `cap_dac_override`. Profile-load records were present. No broad allow rule was added.

## Failure classification

The gate fails because the complete required positive set did not execute in one stable run: the explicit `openat2` result, full graceful restart, and conclusive IPv6 denial were not all demonstrated. The restart failure was a disposable helper/probe state issue (`SQLITE_BUSY` during startup), not evidence that AppArmor should be weakened. The profile remained enforcing throughout the recorded run.

Cleanup removed the temporary binary, profile, socket, databases, storage paths, and test processes. Normal Docker installation and validation snapshots were not changed. The existing temporary validation sudo rule was not removed.

## Follow-up required

Before accepting this gate, rerun with a fresh database and a test systemd/socket unit, expose an explicit `openat2` operation result, test IPv6 on `::1`, and prove helper restart plus receipt recovery under the enforcing profile in one uninterrupted run.
