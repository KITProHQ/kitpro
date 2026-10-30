# ADR-0001: Production implementation stack

- Status: Accepted
- Date: 2026-09-12
- Owners: Josh
- Related decisions: ADR-0003, ADR-0004, ADR-0016, ADR-0019

## Context

KITPro needs one maintainable implementation stack for an unprivileged control plane, a root privileged helper, and background reconciliation. The helper has demanding Linux integration, crash-safety, and MAC requirements. Splitting languages would add runtimes, packaging paths, diagnostics, and upgrade surfaces.

## Decision

Use Go for both the Phase 1 control plane and privileged helper, built as separate binaries (`kitpro-api` and `kitpro-helper`) from one repository and toolchain. Keep the frontend technology separate from this ADR. The disposable runtime probes passed; production code remains deferred.

## Evaluation

Go provides mature Unix and HTTP primitives, `golang.org/x/sys/unix` for peer credentials and `openat2`, direct HTTP-over-Unix Docker access without a CLI, straightforward JSON handling, static or low-dependency binaries, cross-compilation, systemd integration, and strong fuzz/testing support. A pure-Go helper should be preferred; CGO is deferred and requires explicit packaging review if a database driver needs it.

Rust offers stronger compile-time guarantees and excellent `serde`/Unix support, but adds higher compile-time and contributor complexity, greater unsafe/FFI review for `openat2` and low-level socket work, and a larger initial toolchain burden. Rust remains the credible fallback.

Python is reasonable for a control plane, but using it beside a different helper would add virtualenv/dependency lifecycle, native-extension, runtime, and diagnostic burden. Using Python for both would weaken the low-dependency/static helper deployment model.

## Consequences

Benefits include one operational toolchain, shared protocol types where safe, simple Debian packaging, small helper artifacts, and no required child processes. Costs include Go's garbage collector/runtime syscall profile, careful CGO avoidance, and the need to validate Go-specific systemd/MAC behavior before acceptance.

## Toolchain policy

The current stable Go release is Go 1.27.1 (Go 1.27.0 released in August 2026; 1.27.1 is the current security/bug-fix revision). Phase 1 CI should pin `go1.27.1` through a checked-in toolchain declaration and reproducible build environment. The minimum supported toolchain is the latest supported 1.27 patch release; security updates may advance the pinned patch revision after validation. Do not use tip or nightly builds. Builds should record the exact toolchain, module versions, flags, and source revision, use a module proxy only through a verified lock/sum set, and support an offline cache for reproducible release builds.

## Risks and mitigations

- A Go runtime may require additional syscalls: measure before `SystemCallFilter` is finalized.
- Docker client libraries can grow the graph: use a focused direct HTTP client or narrowly reviewed library.
- Shared language must not collapse trust boundaries: helper imports protocol/domain types only, never frontend or control-plane policy.

The disposable Go helper-validation probe passed framing, peer credentials, request hashing, `openat2` rejection, SQLite transaction/WAL checks, atomic receipt writes, graceful shutdown, and direct Docker-transport compilation. A CGO-disabled build was static and approximately 16 MiB. Debian AppArmor and Rocky SELinux runs for the compiled helper remain separate gates.

## Alternatives

- Rust for both components: credible fallback with stronger compile-time safety but higher contributor/toolchain cost.
- Go helper plus Python control plane: familiar web development, but two runtimes and packaging models.
- Python for both: lower initial velocity cost, but weaker static/low-dependency helper fit.

## Review conditions

Revisit if a Go prototype cannot satisfy peer credentials, `openat2`, crash-safe persistence, direct Docker API, MAC packaging, or measured resource limits; or if contributor/packaging evidence favors Rust.
