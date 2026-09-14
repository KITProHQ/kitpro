# Debian Go systemd-hardening gate — 2026-09-13

**DEBIAN GO SYSTEMD HARDENING GATE: PASS**

Run on Debian VM 500 (`10.10.0.115`) using the preserved
`debian-docker-validation-clean` baseline. The helper remained under the
enforcing `kitpro-helper-validation` AppArmor profile.

## Baseline and control sequence

The test-only socket/service units provided socket activation. With the
accepted hardening contract enabled, Ping, receipt creation, Docker inspection,
approved storage, and `Openat2Validation` all passed. The helper PID reported:

```text
kitpro-helper-validation (enforce)
NoNewPrivs: 1
CapInh/Prm/Eff/Bnd/Amb: 0000000000000000
```

Docker inspection returned Engine `29.8.0`, API `1.56`. The API identity was
denied direct access to `/var/run/docker.sock`.

Restart against the existing helper database passed: the prior receipt replayed,
a new Ping succeeded, and the socket was recreated. Final service shutdown
removed the socket cleanly.

## Directive dispositions

| Directive | Disposition | Measured result |
| --- | --- | --- |
| `Type=simple` | ENABLE | Service started and stopped cleanly. |
| `NoNewPrivileges=yes` | ENABLE | `/proc` reported `NoNewPrivs: 1`. |
| `PrivateTmp=yes` | ENABLE | Required operations passed. |
| `PrivateDevices=yes` | ENABLE | Required operations passed. |
| `ProtectSystem=strict` | ENABLE | Required operations passed with explicit `ReadWritePaths`. |
| `ProtectHome=yes` | ENABLE | Forbidden home/root access remained denied. |
| `ProtectKernelTunables/Modules/Logs/ControlGroups=yes` | ENABLE | Required operations passed. |
| `RestrictAddressFamilies=AF_UNIX` | ENABLE | Docker UDS and helper UDS passed; Internet sockets remained denied. |
| `CapabilityBoundingSet=` / `AmbientCapabilities=` | ENABLE | All capability sets were empty. |
| `LockPersonality=yes` | ENABLE | Required operations passed. |
| `UMask=0077` | ENABLE | Required operations passed. |
| `MemoryDenyWriteExecute=yes` | ENABLE | Go helper startup, SQLite, Docker HTTP, openat2, and restart passed. |
| `ProtectProc=invisible` / `ProcSubset=pid` | ENABLE | Ping, SQLite, Docker, and openat2 passed. |
| `RestrictNamespaces=yes` | ENABLE | Ping passed; helper requires no namespace creation. |
| `RestrictSUIDSGID=yes` | OMIT | `Openat2Validation` returned `function not implemented` with the directive enabled; without it, the same operation returned `invalid cross-device link`. |
| `SystemCallFilter=@system-service` | ENABLE (prototype evidence) | Ping, openat2, and Docker inspection passed. Keep production review conservative. |

## RestrictSUIDSGID decisive evidence

With `RestrictSUIDSGID=yes`:

```text
Ping: PASS
Openat2Validation: rejected=true, error=function not implemented
InspectDocker: PASS
```

With the directive omitted:

```text
Ping: PASS
Openat2Validation: rejected=true, error=invalid cross-device link
InspectDocker: PASS
```

Final disposition: **OMIT**. The helper must not weaken or replace openat2;
this systemd directive is incompatible with the required syscall on the tested
Debian kernel/runtime combination.

## Hardening inspection

`systemd-analyze security kitpro-helper-validation.service` completed with an
overall exposure level of `4.3 OK`. This score was treated as descriptive only;
the individual runtime observations above are the acceptance evidence.

## Cleanup

Disposable units, profile, binary, identities, socket, database, and test
storage were removed. Docker and snapshots were preserved. No production code,
package, or architecture decision was changed.
