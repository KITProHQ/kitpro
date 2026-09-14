# Debian Go AppArmor child-execution gate — 2026-09-13

**DEBIAN GO APPARMOR GATE: PASS**

This immutable follow-up resolves the final child-execution question using the
compiled Go helper itself on Debian VM 500. The preserved baseline was
`debian-docker-validation-clean`; snapshots were not modified.

## Probe changes

The disposable helper gained two hardcoded validation-only operations:

- `ProbeShellExecution`: runs `/bin/sh -c 'exit 0'` through `exec.Command`.
- `ProbeExecutableExecution`: runs `/bin/echo kitpro-validation` through `exec.Command`.

No caller-controlled executable or arguments were added. The AppArmor profile's
execution denials were changed to `audit deny` so kernel evidence is emitted.

## Build and confinement

Build command used Go 1.27.0 with Linux amd64, `-trimpath`, `-buildvcs=false`,
stripped symbols, and an empty build ID. SHA-256:

```text
6933903b8ea86d00827c1f825ed19bd1caa68df31777a06bd331ab712a82238a
```

The helper was started by the disposable systemd socket/service units. The
helper PID was `10850`; `/proc/10850/attr/current` returned:

```text
kitpro-helper-validation (enforce)
```

Profile syntax check and reload both succeeded:

```text
apparmor_parser -Q /etc/apparmor.d/kitpro-helper-validation: PASS
apparmor_parser -r /etc/apparmor.d/kitpro-helper-validation: PASS
```

## Child-execution results

Requests were sent by API UID 987 through `/run/kitpro/helper.sock`.

```text
ProbeShellExecution {"ok":true,"result":{"denied":true,"error":"fork/exec /bin/sh: permission denied"}}
ProbeExecutableExecution {"ok":true,"result":{"denied":true,"error":"fork/exec /bin/echo: permission denied"}}
```

Kernel audit evidence:

```text
apparmor="DENIED" operation="exec" profile="kitpro-helper-validation" name="/usr/bin/dash" comm="kitpro-helper-v" requested_mask="x" denied_mask="x"
apparmor="DENIED" operation="exec" profile="kitpro-helper-validation" name="/usr/bin/echo" comm="kitpro-helper-v" requested_mask="x" denied_mask="x"
```

The shell request resolves `/bin/sh` to `/usr/bin/dash`; execution was denied
by the helper's profile before the child ran.

## Regression checks

All previously passing operations remained successful after the profile change:

- Ping: PASS
- helper receipt persistence: PASS
- Docker inspection through helper: PASS (Engine 29.8.0/API 1.56)
- approved storage operation: PASS with the approved directory provisioned
- `Openat2Validation`: PASS (`invalid cross-device link`)
- helper stop/restart against existing DB: PASS
- previous receipt replay after restart: PASS
- Ping after restart: PASS
- final graceful shutdown and socket removal: PASS

The API identity received `PermissionError: [Errno 13] Permission denied` when
attempting direct Docker socket access.

## Packaging observation

The approved `/srv/kitpro/validation` root and its test subdirectory were
provisioned before service startup. This remains an installation/package
requirement; the helper does not receive authority to create arbitrary
top-level storage roots.

## Cleanup

Disposable service units, AppArmor profile, helper binary, identities, socket,
database, and test storage were removed. Docker and snapshots were preserved.
No architecture documentation was changed.
