# Current security boundary

KITPro Server is an authenticated local control plane, not a general Docker frontend.

- The browser-facing API is unprivileged and cannot open the Docker socket.
- The root helper accepts only framed, typed operations over a protected Unix socket, verifies the API peer identity, and revalidates the trusted manifest and requested plan.
- Catalog manifests cannot supply Compose, shell commands, arbitrary capabilities, privileged mode, host networking, arbitrary devices, arbitrary groups, raw bind mounts, or host port bindings.
- Official application images are pinned by immutable digest. Trusted update transitions are catalog-defined and administrator initiated.
- Generated secrets are installation-scoped, preserved across recreation/update/reboot, supplied only to intended components, and omitted from normal API/UI/log output.
- Application services begin internal-only. Publication is limited to loopback or one configured LAN address and the exact declared TCP service; wildcard IPv4/IPv6 exposure is rejected.
- Device access uses a closed class registry. The helper discovers and resolves the device, records stable assignment intent, scopes it to the declaring component, and fails on missing, extra, changed, wrong-vendor, or ambiguous mappings.
- External data uses administrator-registered roots. Canonical path, symlink, forbidden-path, filesystem identity, availability, mode, container target, and writer conflicts are checked again by the helper. Read-only is enforced in Docker; writable roots are exclusive.
- The helper runs under an enforcing AppArmor profile on supported platforms and a capability bounding set containing only the narrowly required `CAP_CHOWN` for newly created managed app directories. It cannot change imported-root ownership.
- Reconciliation compares desired state, helper ownership records, and fresh Docker/hardware/storage observation. Security drift fails closed.

These controls reduce the damage available to a compromised API or application, but they do not make containers, Docker, the Linux kernel, or upstream application code invulnerable. A host administrator with root or Docker-group access remains root-equivalent. See the full [threat model](threat-model.md), [security invariants](invariants.md), and [known limitations](../release/known-limitations.md).
