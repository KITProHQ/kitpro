# Rocky Linux 10 container runtime assessment

Status: discovery complete; runtime implementation present; clean-host
validation pending.

Implementation update, 2026-09-15: the runtime interface, strict platform
classification, Podman/Quadlet adapter, Rocky installer, RPM design, standard
SELinux file-context package, removal/reinstall lifecycle, and validation
scripts now exist. This document retains the pre-change inventory and mapping
that informed those changes. See
[`rocky-runtime-architecture.md`](rocky-runtime-architecture.md) for the selected
design. Rocky remains experimental because no clean Rocky 10 Podman result has
completed the validation matrix.

This assessment records the repository state before the Rocky runtime work. It
describes the current product, not a proposed separate edition.

## What KITPro runs today

KITPro Server does not use Docker Compose. The shipped control plane consists
of two native Go binaries:

- `kitpro-api` runs as the unprivileged `kitpro-api` account.
- `kitpro-helper` runs as root behind a root-owned Unix socket. It is the only
  KITPro process allowed to manage containers and protected host storage.

systemd starts the API and socket-activates the helper. The helper calls the
Docker Engine HTTP API through `/var/run/docker.sock`. Catalog manifests are a
bounded KITPro format, not Compose files. They describe OCI images, storage,
environment values, dependencies, restart policy, services, and optional
hardware. The helper turns those typed plans into Docker API requests.

The catalog contains 15 user-visible applications plus one hidden BusyBox
validation workload. Fourteen user-visible applications use one container per
installation. Paperless-ngx uses two containers, `broker` and `web`.

## Repository inventory

### Runtime code

| Area | Current dependency |
| --- | --- |
| Docker client | `software/server/internal/docker/client.go` implements Docker Engine HTTP calls for version, info, pull, network creation/removal, container create/inspect/start/stop/remove, listing, and foreign network-member detection. |
| Helper | `software/server/cmd/kitpro-helper/main.go` constructs `docker.Client` directly throughout create, start, stop, remove, reconcile, hardware, and health operations. |
| API | `software/server/cmd/kitpro-api/main.go` names its health operation `InspectDocker`, displays Docker-specific health text, and reports `apt/dpkg` for every non-Arch host. |
| Exposure | `software/server/internal/exposure/exposure.go` names Docker in protocol conversion and inspect-shape validation. |
| Hardware | `software/server/internal/hardware/hardware.go` interprets Docker's `Runtimes` info map to determine NVIDIA runtime availability. |
| Tests | Docker request and inspect JSON shapes are asserted in client, helper, exposure, hardware, manifest, and catalog tests. |

There is no Docker SDK dependency. The Docker dependency is the request
protocol, response shape, socket path, error wording, and package/service
contract.

### Orchestration and service management

| File | Current behavior |
| --- | --- |
| `software/server/packaging/systemd/kitpro-api.service` | Native system service, restart on failure, writes only API state. |
| `software/server/packaging/systemd/kitpro-helper.service` | Native root service, ordered after `docker.service`, AppArmor-confined, and limited to `CAP_CHOWN`. |
| `software/server/packaging/systemd/kitpro-helper.socket` | Root-owned socket with group access for `kitpro-api`. |
| `software/server/packaging/tmpfiles/kitpro.conf` | Creates runtime, database, backup, log, and application-data directories. |
| `software/server/internal/multicontainer/plan.go` | Computes dependency-first start order for bounded multi-container plans. |

No `.container`, `.network`, `.volume`, `.pod`, or `.kube` Quadlet files exist.
No code uses `podman generate systemd` or `podman-compose`.

### Installation, upgrade, and uninstall

| Platform path | Current behavior |
| --- | --- |
| Debian and Ubuntu | Reproducible `.deb` builder, maintainer scripts, AppArmor profile, systemd units, tmpfiles, database migration, pre-upgrade SQLite backups, and data-preserving removal. Docker must already be active. |
| Arch | `PKGBUILD`, install hooks, sysusers, AppArmor, systemd, tmpfiles, pre-upgrade backups, and data-preserving removal. Depends on the `docker` package. |
| Rocky | No KITPro RPM, install script, upgrade transaction, or uninstall transaction exists. `tools/platform-validation/prepare-rocky.sh` installs Docker CE for an old experimental fixture. |
| Generic Docker tooling | `tools/install-docker.sh` is explicitly a development Docker installer. It currently supports RHEL-family hosts and adds Docker's external repository. That is not an acceptable Rocky production path for this work. |

The control and helper databases live at `/var/lib/kitpro-api/control.db` and
`/var/lib/kitpro-helper/helper.db`. Backups live in sibling `backups`
directories. Package upgrades stop the services, create verified SQLite
backups, install the new files, run migrations, and restart the API/socket.
Package removal preserves databases, backups, logs, `/srv/kitpro`, catalog
application data, configuration, secrets stored in helper state, and runtime
ownership records.

### Existing Rocky and SELinux work

The repository has Rocky 10 host checks, a disposable Docker-based validation
runner, and a test-only SELinux policy under
`prototypes/privilege-boundary/selinux/`. Prior evidence established that a
dedicated helper domain is feasible and that SELinux can remain Enforcing. It
did not package the production Go helper policy or validate Podman/Quadlet.

The current production helper unit names `AppArmorProfile=` unconditionally.
The production AppArmor profile grants access to Docker's socket and denies
child execution. It has no Rocky equivalent.

## Docker-specific dependency map

### Direct API and socket use

The helper opens `/var/run/docker.sock` in `docker.New()`. There is no socket
configuration setting or runtime interface. The approved API subset is:

- `GET /version`
- `GET /info`
- `POST /images/create`
- `POST /networks/create`
- `GET /networks/{name}`
- `DELETE /networks/{name}`
- `POST /containers/create`
- `GET /containers/json?all=true`
- `GET /containers/{id}/json`
- `POST /containers/{id}/start`
- `POST /containers/{id}/stop?t=5`
- `DELETE /containers/{id}?v=false`

No application container receives the Docker socket. The browser-facing API
cannot open or proxy it. Replacing the Docker socket with a Podman socket would
keep the root-equivalent authority and would not provide Quadlet ownership, so
it is not an automatic substitution.

### Docker request and response assumptions

- Container creation uses Docker `HostConfig`, `NetworkingConfig`,
  `EndpointsConfig`, `PortBindings`, `Binds`, `Devices`, `DeviceRequests`, and
  `RestartPolicy` JSON shapes.
- Reconciliation expects Docker `Config`, `HostConfig`, `NetworkSettings`,
  `State`, and network-inspect `Containers` shapes.
- Ownership stores Docker container IDs and network names in the helper
  database. Required `com.kitpro.*` labels must agree with that state.
- NVIDIA support currently depends on Docker's `Runtimes` map and a Docker
  `DeviceRequests` entry using driver `nvidia` and capability `gpu`.
- Errors and user-facing text say Docker. Operation categorization also looks
  for Docker-specific error strings.

### Shell, package, and service assumptions

- Debian pre-install and Arch hooks require `docker.service` and a Docker
  socket.
- The helper systemd unit orders itself after `docker.service`.
- The AppArmor profile allows only Docker socket paths.
- Arch packaging depends on `docker`.
- Host verification and disposable fixtures invoke `docker`, `containerd`,
  `docker compose`, and `docker buildx` directly.
- Documentation, API labels, UI copy, and test fixtures use Docker terminology.
- Historical result documents are evidence records and must not be rewritten.

## Compose feature inventory

No Compose file exists and KITPro never invokes Compose. The equivalent
features in the catalog/runtime model are listed here because they still need
native Rocky mappings.

| Current KITPro feature | Current implementation | Rocky mapping | Translation |
| --- | --- | --- | --- |
| Images | Fully qualified `docker.io` or `ghcr.io` digest references | OCI image reference in `Image=` | Direct |
| Container definitions | Docker Engine create JSON | One generated `.container` per component | Direct for supported fields |
| Per-installation network | One bridge network name stored in helper state | One `.network`, referenced by component Quadlets | Direct |
| Network aliases | Docker endpoint alias, used for component DNS such as `broker` | `NetworkAlias=` | Direct, requires live DNS validation |
| Persistent storage | Host bind directories under `/srv/kitpro/apps/...` | `Volume=/host:/container[:options]` in `.container` | Direct, SELinux labeling requires policy |
| Imported storage | Validated bind paths under `/mnt`, `/media`, `/data`, or `/srv` | Read-only or read-write `Volume=` bind | Does not translate safely with blind `:z` or `:Z` relabeling |
| Environment | Bounded plain values and generated secrets become Docker `Env` entries | Root-owned `EnvironmentFile=` or Podman secret | Direct for plain values; secrets require protected files or Podman secrets |
| Command | Bounded argv array | `Exec=` | Direct |
| User | Numeric `UID:GID` | `User=` | Direct, ownership must be validated |
| Ports | Exact loopback or configured LAN address, dynamic host port 20000 through 29999 | Exact-address `PublishPort=` | Direct, but firewalld behavior needs live tests |
| Restart | `no` or Docker `unless-stopped` | systemd `Restart=` policy on generated service | Similar, not identical. Explicit KITPro stop must not trigger restart. |
| Dependencies | Topological creation/start order | `After=` plus `Requires=` between component units | Direct for startup order; readiness is not implied |
| Health | Container state only; catalog manifests have no health-check field | systemd active state plus Podman health when a future manifest declares it | Current behavior maps directly, but it is not application readiness |
| Devices | Exact DRM device mappings or Docker NVIDIA device request | `AddDevice=` and NVIDIA CDI where validated | Not a direct JSON-shape mapping; needs Rocky GPU validation |
| Labels | `com.kitpro.*` labels plus protected DB record | `Label=` plus protected DB record | Direct |
| Named volumes | Not used | No `.volume` units required for current data layout | Not applicable |
| Compose secrets/configs | Not used | No mapping required | Not applicable |
| Pods | Not used | Do not add pods unless a measured need appears | Not applicable |

Quadlet's supported rootful search paths include `/etc/containers/systemd/`
for administrator-managed units and `/usr/share/containers/systemd/` for
distribution files. KITPro's generated definitions use flat, validated
`/etc/containers/systemd/kitpro-*` names. Quadlet generates normal systemd services,
and unit dependencies can reference other Quadlet files. See the upstream
[Quadlet manual](https://docs.podman.io/en/v5.5.1/markdown/podman-systemd.unit.5.html).

## Features that do not translate cleanly

### Dynamic lifecycle versus declarative units

Docker creation is one API call. Quadlet is declarative and becomes active
through the systemd generator after `daemon-reload`. KITPro must atomically
write or replace a complete set of per-installation unit files, reload
systemd, start the generated target or component units, and retain enough
state to recover from a crash between those steps. Direct Podman lifecycle
calls cannot be mixed casually with a systemd-owned Quadlet container.

### Runtime identity and ownership

The database currently treats container ID plus labels as runtime identity.
Quadlet recreates containers as a normal part of service management, so a
stable systemd unit and Quadlet file digest must become part of the trusted
identity. Container ID remains an observation, not the stable owner key.

### `unless-stopped`

Docker persists an explicit stop separately from daemon restart. systemd
restart policies do not have exactly the same state model. KITPro should map
catalog `unless-stopped` to `Restart=on-failure` or `always` only while desired
state is running, and use systemd stop/disable semantics for an administrator
stop. This needs reboot and crash tests.

### Secrets

Generated secrets currently reside in the root-owned helper database and are
sent to Docker in create JSON. Putting them directly in Quadlet files or
systemd command arguments would expose them. The Rocky path needs root-owned
per-installation environment files with mode `0600`, or Podman secrets where
the image can consume secret files. The current catalog expects environment
variables, so protected environment files are the compatible first step.

### Hardware

Docker `DeviceRequests` does not map to one generic Quadlet key. DRM devices
can use explicit device mappings. NVIDIA should use the Rocky-supported CDI
path only after the toolkit and the catalog workloads pass live tests. GPU
support must remain unclaimed on Rocky until then.

## SELinux-sensitive paths

| Host path | Consumer | Required treatment |
| --- | --- | --- |
| `/usr/libexec/kitpro-helper` | systemd executes privileged helper | Dedicated executable type and transition into a narrow helper domain. |
| `/run/kitpro` and `/run/kitpro/helper.sock` | API/helper Unix socket | Dedicated runtime type; API can connect, helper and systemd can create/manage it. |
| `/var/lib/kitpro-helper` | Helper DB, receipts, generated secrets, backups | Dedicated helper state type; root-only modes remain. |
| `/var/lib/kitpro-api` | API DB, sessions, backups | Dedicated API state type or a service-appropriate existing type. |
| `/srv/kitpro/apps` | Per-installation managed bind storage | Persistent file-context rule suitable for confined containers. Per-installation MCS isolation must be tested before choosing shared or private relabeling. |
| `/mnt`, `/media`, `/data`, selected `/srv` paths | Administrator-imported storage | Never relabel the whole parent. Validate the exact root. Use an explicit administrator-approved file context or a narrow custom container policy when standard labels are insufficient. Network filesystems need separate testing. |
| `/etc/containers/systemd/kitpro-*` | Generated Quadlet definitions | Root-owned, helper-writable only through the exact systemd sandbox path, no secrets in unit text. |
| `/etc/kitpro-server/runtime` | Proposed protected environment files | Root-owned mode `0700` directory and `0600` files with a dedicated config/secret type. |
| `/run/podman/podman.sock` | Only needed if the helper retains a Podman API inspection path | Root-equivalent authority. Do not mount it into containers. Prefer Quadlet plus bounded inspection; grant only measured helper access. |
| `/run/systemd/private` | Needed if the helper controls generated units through D-Bus | High-impact authority. A narrow broker or measured SELinux rule is required; do not grant generic systemd administration without an operation allowlist. |

Using `:Z` on KITPro-exclusive application directories may be sufficient, but
it can conflict when multiple components share one path. `:z` is appropriate
only when the exact content is intentionally shared by containers. Neither
option is safe as a blanket treatment for imported host or network storage.
Udica can help produce an initial custom container policy when standard labels
cannot express an approved imported-storage need, but generated policy still
requires manual reduction and review.

## Networking and firewalld

Each installation gets one bridge network. Paperless components use aliases
for service-name DNS. Private services publish no port. Loopback and LAN modes
publish one exact address and a host port in `20000-29999`; SFTPGo can expose a
second declared service. Wildcard publication is rejected.

The repository currently makes no firewalld changes. Prior Docker evidence
observed runtime-created zones and forwarding policy, but that does not prove
Podman behavior. The Rocky installer must leave firewalld enabled. Loopback
publication needs no permanent firewalld opening. LAN publication must add an
exact, KITPro-owned firewalld rule only if live Rocky testing proves it is
required, record that ownership, and remove only that rule on uninstall.

Quadlet supports bridge `.network` units, internal networks, DNS aliases, and
exact `PublishPort=` values. The current production Go adapter creates a bridge
without Docker's `Internal` flag even though older design documents describe
an internal network. Rocky implementation must not silently claim egress
isolation. The intended egress policy needs a separate compatibility decision
and regression coverage.

## Rootful versus rootless decision

Use rootful Podman system services for the first Rocky implementation.

This matches the current managed-server security model:

- The helper already runs as root to prepare owned storage and validate host
  mounts and devices.
- Units must start during normal system boot without user lingering.
- Exact host storage ownership, imported storage, device access, backups, and
  recovery are simpler to keep consistent in the system instance.
- Rootless user-namespace ownership would change current UID/GID behavior and
  require a migration design for existing application data.
- The approved exposure range is unprivileged, so privileged ports are not the
  deciding factor.

Rootful does not mean privileged containers. Keep host namespaces disabled,
avoid `--privileged`, add no capabilities by default, pass only approved
devices, preserve numeric users where declared, and keep the helper protocol
as the authorization boundary.

## Smallest viable architecture change

1. Introduce a narrow `runtime` interface at the helper boundary. Preserve the
   current Docker adapter and tests for Debian, Ubuntu, and Arch.
2. Rename protocol/UI health concepts from Docker to container runtime while
   accepting the old operation name during a compatibility window.
3. Add strict `/etc/os-release` platform classification: Rocky 10 supported by
   the new path, AlmaLinux 10 and RHEL 10 experimental, everything else
   unsupported unless already in the Debian, Ubuntu, or Arch matrix.
4. Add a rootful Podman/Quadlet adapter that renders one network unit and one
   container unit per component, plus a generated target or explicit unit
   dependencies for the logical application.
5. Keep secrets out of Quadlet text. Store environment files in a root-only
   runtime configuration directory.
6. Package the helper SELinux domain, path file contexts, tmpfiles entries,
   RPM scriptlets, and native Rocky dependencies. Do not install Docker or
   disable SELinux/firewalld.
7. Extend the disposable-host suite for Podman, Quadlet, reboot, failure,
   upgrade, removal, DNS, storage, firewalld, and AVC evidence before changing
   Rocky from experimental to supported.

## Initial readiness verdict

`PARTIALLY_READY`. The application model is compatible with OCI containers and
the Podman adapter, generated Quadlets, RPM inputs, and SELinux file-context
module now exist. Supported status remains blocked on a reproducible RPM build
and the complete clean Rocky 10 acceptance matrix, including reboot, failure
recovery, imported storage, firewalld, upgrade, reinstall, and AVC evidence.
