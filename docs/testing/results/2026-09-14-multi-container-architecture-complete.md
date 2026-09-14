# Multi-container architecture implementation record

- Date: 2026-09-14
- Status: live validation complete; final checkpoint pending review

The repository now contains a schema-version-2 typed component model,
dependency-first ordering, explicit protocol component plans, helper-side
component validation, per-component trusted ownership state, deterministic
component aliases on the installation bridge, and bounded component runtime
creation. Schema-version-1 applications remain on the existing single-container
path.

No Docker Compose or arbitrary Docker configuration is accepted. Components
are created only from digest-pinned catalog definitions, logical storage
declarations, typed environment entries, declared internal services, and an
acyclic bounded dependency graph. The helper rejects component plans whose
image, dependency, command, environment, storage, or service shape differs
from the trusted catalog.

The Paperless-ngx manifest is the first schema-version-2 catalog entry. Live
acceptance exercised the production API/helper path on Debian 13, Ubuntu 26.04,
and Arch Linux: install, dependency-ordered component startup, exact
reconciliation, loopback/LAN or internal exposure, runtime recreation, stop/
start, missing-component detection, foreign-network drift detection, reboot,
and persistent storage marker preservation. The Debian Paperless marker SHA-256
was `27645b2d0a1d48b5d9d9ef3ebc0260a6b5ea7cdbcddf29a2a6659beadeb09837`.

Observed platform results:

- Debian 13: web and Redis components healthy; LAN binding `10.10.0.115:20001`; missing web classified `missing`; foreign member classified `security_drift`; recreate and reboot returned `exact`.
- Ubuntu 26.04: web and Redis components healthy; loopback binding `127.0.0.1:20000`; recreate and reboot completed without duplicate resources.
- Arch Linux: native alpha5 package, `linux-lts`, enforcing AppArmor, full `pacman -Syu`, reboot, web and Redis components healthy; loopback binding `127.0.0.1:20000`; exact reconciliation after reboot.

The multi-component stop path was corrected to stop without removing component
containers; removal remains explicit. Multi-component reconciliation now checks
expected host exposure for every component set and rejects unexpected bindings.

Package artifacts built from the validated tree:

- Debian/Ubuntu `.deb`: `software/server/dist/kitpro-server_0.1.0~alpha5_amd64.deb`, SHA-256 `c2b377e2b34ff16adf087ba74cde11f907456ca5c2d76042778614055f25fc41`.
- Arch `.pkg.tar.zst`: `software/server/dist/kitpro-server-0.1.0_alpha5-1-x86_64.pkg.tar.zst`, SHA-256 `72a12ffbc88772b72608d0a5704e421a275202d36375a92455bc67774e2c0365`.

Both package families reproduced byte-for-byte in clean secondary build
directories. Go tests and vet pass after the lifecycle and platform validation
fixes.

Acceptance gates:

- `PAPERLESS DEBIAN LIVE ACCEPTANCE: PASS`
- `PAPERLESS UBUNTU LIVE ACCEPTANCE: PASS`
- `PAPERLESS ARCH LIVE ACCEPTANCE: PASS`
- `MULTI-CONTAINER APPLICATION ARCHITECTURE: PASS`
