# Container runtime abstraction

Runtime-specific behavior belongs behind
`software/server/internal/containers.Runtime`. The Docker adapter remains the
Debian, Ubuntu, and Arch implementation. The Podman adapter is selected by
strict platform classification on Rocky Linux 10.

## Boundary

The interface contains only operations KITPro already needs: runtime
information, image preparation, network creation, validated container-plan
creation, lifecycle, inspection, listing, and foreign-member detection. Catalog
and API code must not import a concrete runtime package.

`ContainerPlan` is the shared OCI-oriented model. Add a field only when a
catalog feature needs it on at least one host path. Do not mirror every Docker
or Podman option.

## Selection

`internal/platform` parses `/etc/os-release` without `ID_LIKE` fallback:

- Debian 13 and Ubuntu 26.04 select Docker and Compose-compatible orchestration.
- Arch selects Docker.
- Rocky 10 selects Podman and Quadlet with experimental support.
- RHEL 10 and AlmaLinux 10 are recognized as experimental but not installable.
- Unknown platforms fail instead of receiving Debian behavior.

`KITPRO_CONTAINER_RUNTIME` may explicitly select `docker` or `podman` for tests
and controlled development. Other values fail.

## Podman adapter invariants

- Generated names begin with `kitpro-` and pass a strict grammar.
- Quadlets go to `/etc/containers/systemd` by default.
- Secrets appear only in mode-0600 environment files.
- Managed `/srv/kitpro/apps` mounts use private `:Z`; imported mounts do not.
- Published ports include the exact trusted host address.
- Lifecycle goes through systemd, not direct `podman start` and `podman stop`.
- The Podman API socket is not used.
- GPU `DeviceRequests` fail closed until a Rocky CDI path is certified.

Run the Go tests after changing either adapter:

```sh
cd software/server
GOCACHE=/tmp/kitpro-go-cache go test ./...
GOCACHE=/tmp/kitpro-go-cache go vet ./...
```
