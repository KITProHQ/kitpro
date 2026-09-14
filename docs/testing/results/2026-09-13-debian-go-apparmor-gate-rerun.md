# Debian Go AppArmor production-gate rerun — 2026-09-13

**DEBIAN GO APPARMOR GATE: FAIL**

This immutable record covers the requested rerun of the compiled Go helper under
the enforcing AppArmor profile on Debian VM 500. No architecture decision was
changed.

## Access

Command:

```sh
ssh -o BatchMode=yes josh@10.10.0.115 'hostname && sudo -n true && echo SUDO_OK'
```

Exit code: `0`.

Stdout:

```text
debian13
SUDO_OK
```

Stderr was empty.

## Build

The existing `prototypes/go-helper-validation` helper was rebuilt for Linux
amd64 with `-trimpath`, `-buildvcs=false`, stripped symbols, and an empty build
ID. The resulting artifact was installed temporarily at
`/usr/libexec/kitpro-helper-validation` as root-owned mode `0755`.

```text
sha256: fbc86ff3768e7869d7f201b078e546ebe6343669fbe00e6f3958946debcede84
file: ELF 64-bit LSB executable, x86-64, version 1 (SYSV), dynamically linked,
       interpreter /lib64/ld-linux-x86-64.so.2, stripped
```

The source probe now includes an explicit `Openat2Validation` operation and a
restart regression test. Local non-lifecycle tests and `go vet` passed; the
local lifecycle test is blocked by the development sandbox rejecting Unix
socket setup (`setsockopt: operation not permitted`).

## AppArmor and identities

`apparmor` was active, the validation profile parsed and loaded, and
`aa-status` reported `kitpro-helper-validation` in enforce mode. The existing
disposable identities remained `kitpro-api-test` UID/GID 987/987 and
`kitpro-helper-test` UID/GID 986/986; neither belongs to the Docker group.

## Rerun attempts

The stale helper processes from the prior run were identified by PID and
terminated. Their disposable socket and database files were removed. A fresh
database was then used for each attempt.

Command (profile-enforced attempt):

```sh
sudo -n env KITPRO_TEST_SOCKET=/run/kitpro/helper.sock \
  KITPRO_TEST_DB=/var/lib/kitpro-helper/helper-gate.db \
  KITPRO_TEST_ROOT=/srv/kitpro/validation KITPRO_TEST_ALLOWED_UID=987 \
  nohup aa-exec -p kitpro-helper-validation -- \
  /usr/libexec/kitpro-helper-validation >/tmp/kitpro-helper-gate.log 2>&1 &
```

Observed result: the helper process remained in uninterruptible `D` state and
never created the socket. `/proc/<pid>/stack` showed the kernel blocked in
`fsync` through ext4 writeback (`rq_qos_wait` / `wbt_wait`). The database stayed
at zero bytes with a journal file; no application error was emitted.

The same behavior reproduced without AppArmor using a fresh database:

```text
sudo timeout 8s env ... /usr/libexec/kitpro-helper-validation
RC=124
/run/kitpro/helper.sock: absent
/var/lib/kitpro-helper/helper-noaa.db: 0 bytes
```

A one-megabyte disposable `dd conv=fsync` completed in 0.378 seconds, but the
SQLite startup fsync did not complete within the bounded eight-second helper
attempt. This is an environmental VM storage stall, not an AppArmor denial.

## Required operations

| Operation | Status | Evidence |
| --- | --- | --- |
| Profile enforcing and process confinement | PASS (prior run) | `/proc/<pid>/attr/current` returned `kitpro-helper-validation (enforce)`. |
| Ping / peer credentials / receipt / Docker inspection | NOT RUN in this fresh rerun | Helper did not reach socket creation. |
| Explicit `openat2` operation | NOT RUN in this fresh rerun | New operation is present in the probe but could not be invoked. |
| Graceful restart and post-restart Ping | NOT RUN in this fresh rerun | Startup did not complete. |
| Negative filesystem/network probes | NOT RUN in this fresh rerun | Startup did not complete. |

## Cleanup

Only disposable helper processes, sockets, databases, and logs were removed.
Docker installation, VM snapshots, and unrelated resources were not changed.
The temporary validation sudo rule was retained.

## Blocking failure

The gate remains **FAIL** because the helper cannot complete SQLite startup on
the Debian VM during the bounded rerun: it blocks in kernel ext4 fsync before
creating its Unix socket. The failure reproduces without AppArmor, so changing
the profile would not address it. A fresh VM/storage checkpoint or a diagnosed
storage-layer fix is required before the AppArmor positive and negative suites
can be rerun as one complete gate.
