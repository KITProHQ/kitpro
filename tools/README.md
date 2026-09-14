# Development tools

These tools support architecture validation. They are not the KITPro Server application or production installer.

## Docker installer

[`install-docker.sh`](install-docker.sh) installs Docker Engine from an exact
distribution allowlist. Debian, Ubuntu, and RHEL-family hosts use Docker's
vendor repositories; Arch uses its signed official distribution repository:

| Family | Accepted releases | Repository identity |
| --- | --- | --- |
| Arch | Current fully updated `x86_64` system | Arch official repositories |
| Debian | Debian 13 | Docker Debian repository |
| Ubuntu | Ubuntu 24.04 LTS and 26.04 LTS | Docker Ubuntu repository |
| RHEL | RHEL 9 and 10 | Docker RHEL repository |
| RHEL-compatible derivatives | Rocky Linux 9/10 and AlmaLinux 9/10 | Docker RHEL repository, treated as KITPro compatibility validation rather than Docker certification |

The script rejects unknown IDs, unlisted versions, unsupported architectures, and non-systemd PID 1 environments. It installs Docker Engine, the Docker CLI, `containerd`, Buildx, and the Compose plugin. It uses `docker compose`; it does not install legacy standalone `docker-compose`.

Review planned changes:

```sh
sudo tools/install-docker.sh --dry-run
```

Install interactively:

```sh
sudo tools/install-docker.sh
```

Install non-interactively on a reviewed clean host:

```sh
sudo tools/install-docker.sh --yes
```

Verify an existing installation without changing it:

```sh
sudo tools/install-docker.sh --verify-only
```

The default never adds a user to the `docker` group. The explicit `--grant-user-access` option works only when `sudo` supplies a resolvable non-root `SUDO_USER`; direct root execution and ambiguous sessions fail instead of guessing. Docker group membership grants root-equivalent control.

The script also refuses to remove known package conflicts by default. After reviewing the printed packages, the operator may choose `--remove-conflicts`. This option does not remove Docker data directories.

`--run-hello-world` adds an optional image pull and disposable container run to normal verification. It is off by default. Normal verification checks Docker and containerd services, Engine and daemon response, Compose, Buildx, containerd, host and Docker cgroup reporting, and SELinux mode when the tools exist.

The implementation follows Docker's current repository workflows for [Debian](https://docs.docker.com/engine/install/debian/), [Ubuntu](https://docs.docker.com/engine/install/ubuntu/), and [RHEL](https://docs.docker.com/engine/install/rhel/). Arch installs the official `docker`, `docker-buildx`, and `docker-compose` packages in the same full `pacman -Syu` transaction. Review those sources, current Arch News, and the support matrix before treating a host as compatible.

## Disposable platform validation

See [`platform-validation/README.md`](platform-validation/README.md). Those scripts validate already-created Debian 13 and Rocky Linux 10 VMs. They do not provision VMs or change ADR-0017.
