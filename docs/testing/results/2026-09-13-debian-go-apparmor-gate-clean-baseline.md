# Debian Go AppArmor production-gate clean-baseline run — 2026-09-13

**DEBIAN GO APPARMOR GATE: FAIL**

Run against the preserved `debian-docker-validation-clean` baseline on VM 500
(`10.10.0.115`). Earlier storage-stall results occurred during host backup load
and are not used as evidence here.

## Build and confinement

The existing Go helper was built with Go 1.27.0, Linux amd64, `-trimpath`,
`-buildvcs=false`, stripped symbols, and an empty build ID. SHA-256:

```text
f7011930a253f0c47c9af5d126ad7cf3dfaa432c549b51a150d1d4af433c9ee1
```

The test-only systemd socket and service units started the helper under the
loaded enforcing profile. `cat /proc/10282/attr/current` returned:

```text
kitpro-helper-validation (enforce)
```

## Results

| Check | Status | Evidence |
| --- | --- | --- |
| systemd socket activation | PASS | `kitpro-helper-validation.socket` listened on `/run/kitpro/helper.sock` and triggered the service. |
| API UID / `SO_PEERCRED` | PASS | API UID 987 connected and Ping succeeded. |
| framed Ping | PASS | Typed response and SHA-256 request hash returned. |
| helper SQLite receipt | PASS | Receipt became durable; replay after restart returned `ok` with no result mutation. |
| Docker inspection | PASS | Engine 29.8.0/API 1.56 returned through helper HTTP-over-Unix. |
| approved storage operation | PASS | Operation succeeded after disposable approved directory was pre-created. Initial mkdir failed under the hardened namespace. |
| explicit `openat2` | PASS | `../escape` rejected with `invalid cross-device link`. |
| helper restart / receipt recovery | PASS | Service stopped and restarted; prior request replay succeeded and new Ping succeeded. |
| API direct Docker access | PASS | API identity received `PermissionError: [Errno 13] Permission denied`. |
| filesystem/network negative probes | PASS | Home, SSH key, and root reads denied; IPv4/IPv6 socket creation returned `address family not supported by protocol` under `AF_UNIX` restriction. |
| arbitrary shell/executable denial | FAIL | Direct `/bin/echo` execution under the profile was denied, but `/bin/sh -c 'echo unexpected'` returned exit 0 because `echo` is a shell builtin. The probe does not establish denial of shell interpreter startup itself. |

## AppArmor evidence

The profile was loaded in enforce mode. Audit records showed expected denials
for confined access to `/etc/passwd`, `/etc/group`, and `/etc/nsswitch.conf`
when running the unrelated `id` probe. No required helper operation generated a
profile denial after the approved directory existed.

## Blocking issue

The gate remains **FAIL** because the required negative test for `/bin/sh`
execution was not conclusively demonstrated: `aa-exec` can start the shell
under the selected profile, and the shell builtin completed. Child executable
launches such as `/bin/echo` were denied, but this is not equivalent to denying
the shell interpreter itself. The profile or probe needs a dedicated helper-side
exec test (or an unambiguous interpreter-execution assertion) before PASS.

The initial approved-storage mkdir denial also indicates that the current
systemd/AppArmor fixture assumes the directory exists; production setup must
create and label approved roots before helper startup.

## Cleanup

Disposable identities, units, profile, binary, socket, database, and test
storage were removed. Docker and VM snapshots were preserved. No architecture
documentation was changed.
