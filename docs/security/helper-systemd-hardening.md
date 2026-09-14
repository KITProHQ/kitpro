# Helper systemd hardening contract

This matrix is a correctness-oriented starting point for the root, socket-activated helper. It is not a `systemd-analyze security` score exercise. Every setting must be retested against the selected runtime and both supported packaging adapters.

| Directive | Disposition | Reason |
| --- | --- | --- |
| `NoNewPrivileges` | ENABLE | Prevents gaining privilege through exec transitions. |
| `PrivateTmp` | ENABLE | No shared temporary-file namespace is required. |
| `PrivateDevices` | ENABLE | Helper needs no device access. |
| `ProtectSystem` | ENABLE WITH EXCEPTION | `strict`; explicitly allow helper state, audit, runtime, and approved storage. |
| `ProtectHome` | ENABLE | Home and SSH data are outside the contract. |
| `ProtectKernelTunables`, `ProtectKernelModules`, `ProtectKernelLogs` | ENABLE | No kernel administration is required. |
| `ProtectControlGroups` | ENABLE WITH EXCEPTION | Read-only host inspection may be needed; no cgroup mutation. |
| `ProtectClock`, `ProtectHostname` | ENABLE | No host identity or clock mutation. |
| `ProtectProc` / `ProcSubset` | ENABLE WITH EXCEPTION | Permit only the process facts required by runtime/inspection; validate on target kernels. |
| `RestrictAddressFamilies` | ENABLE | `AF_UNIX` only; Docker uses the Unix socket. |
| `RestrictNamespaces` | ENABLE | No namespace creation or entry is needed. |
| `RestrictRealtime` | ENABLE | No realtime scheduling. |
| `RestrictSUIDSGID` | DO NOT ENABLE (for now) | On Rocky systemd 257 the setting coincided with `openat2` returning `ENOSYS`; descriptor-relative containment is primary. Revisit with runtime-specific tracing. |
| `LockPersonality` | ENABLE | No alternate execution personality is needed. |
| `MemoryDenyWriteExecute` | DEFER UNTIL IMPLEMENTATION | Validate against runtime/JIT behavior; do not assume. |
| `SystemCallArchitectures` | ENABLE WITH EXCEPTION | Restrict to native architecture after runtime validation. |
| `SystemCallFilter` | DEFER UNTIL IMPLEMENTATION | Build from measured syscall traces; never use a guessed denylist. |
| `CapabilityBoundingSet` | ENABLE | Start empty; add only measured capability requirements. |
| `AmbientCapabilities` | ENABLE | Empty. |
| `UMask` | ENABLE | Use a restrictive mask suitable for root-owned state. |
| `RuntimeDirectory` | ENABLE | Systemd owns the transient socket directory and mode. |
| `StateDirectory` | ENABLE WITH EXCEPTION | Use for helper-owned durable state where packaging permits; audit/storage paths may be separate. |
| `LogsDirectory` | ENABLE WITH EXCEPTION | Use only for helper-owned non-sensitive audit/log output; prefer journald for operational logs. |
| `ReadWritePaths` | ENABLE | Explicit allowlist of runtime, state, audit, and approved storage roots. |
| `ReadOnlyPaths` | ENABLE WITH EXCEPTION | Add required system/runtime metadata paths explicitly. |
| `InaccessiblePaths` | ENABLE WITH EXCEPTION | Hide `/home`, `/root`, SSH material, Docker data, devices, and unrelated application roots where compatible with MAC. |

## `RestrictSUIDSGID` finding

The observed failure was `openat2(2)` returning `ENOSYS` only when the Rocky systemd 257 unit enabled `RestrictSUIDSGID=yes`; removing it restored the call. This strongly implicates systemd's generated seccomp hardening filter, but the disposable run did not independently trace the filter or identify a kernel flag defect. No evidence implicated `openat2` flags or the fixture's path logic. The disposition is therefore to omit the generic switch, preserve `openat2` containment, and later test a measured syscall policy or newer systemd/kernel combination. Production must not replace descriptor-relative safety with a weaker path check to recover this score.
