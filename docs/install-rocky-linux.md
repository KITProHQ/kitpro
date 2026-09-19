# Install KITPro Server on Rocky Linux 10

Rocky Linux 10 support is experimental until the complete acceptance matrix in
[`testing/rocky-linux-10-validation.md`](testing/rocky-linux-10-validation.md)
passes on a clean VM. This path uses Rocky packages, rootful Podman, Quadlet,
systemd, SELinux Enforcing, crun, and firewalld. It does not install Docker.

## Requirements

- Rocky Linux 10 x86_64
- systemd as PID 1 and cgroup v2
- SELinux `Enforcing`
- firewalld enabled and active
- 2 CPUs, 4 GiB RAM, and 20 GiB free system storage at minimum
- root access for package installation

The installer rejects RHEL 10 and AlmaLinux 10. The runtime classifier knows
about them so future validation can extend the matrix, but neither is currently
installable or supported.

## Prepare the native runtime

From a reviewed checkout:

```sh
sudo ./tools/install-rocky.sh --yes
```

This installs `podman`, `crun`, `container-selinux`, `firewalld`, and the native
policy and diagnostic tools. It enables firewalld without opening a port and
then verifies Podman 5 or newer, crun, cgroup v2, the storage driver, networking,
Quadlet, and SELinux state.

## Build the RPMs

Build on Rocky Linux 10. Install build dependencies from Rocky repositories:

```sh
sudo dnf install -y rpm-build golang selinux-policy-devel selinux-policy-targeted
./software/server/packaging/build-rpm.sh 0.1.0~alpha11
```

The build produces `kitpro-server`, `kitpro-selinux`, and source RPM artifacts
under `software/server/dist/`, with adjacent SHA-256 files. The repository does
not publish these packages or configure a package repository.

Install both binary RPMs in one transaction:

```sh
sudo dnf install ./software/server/dist/kitpro-selinux-*.rpm \
  ./software/server/dist/kitpro-server-*.rpm
```

The transaction creates the service identity and data directories, installs
the SELinux file-context module, migrates both databases, reloads systemd, and
starts `kitpro-helper.socket` and `kitpro-api.service`.

## Verify the installation

```sh
getenforce
podman version
podman info --format 'runtime={{.Host.OCIRuntime.Name}} cgroups={{.Host.CgroupsVersion}} network={{.Host.NetworkBackend}} storage={{.Store.GraphDriverName}}'
systemctl status kitpro-helper.socket kitpro-api.service
curl -I http://127.0.0.1:8080/
```

The dashboard is loopback-only by default. Use an SSH tunnel for initial setup.
Do not add a broad `20000-29999` firewalld rule. KITPro publishes only an exact
loopback or configured LAN address. With Rocky's default
`StrictForwardPorts=no`, firewalld admits Podman/netavark's exact published
port without listing it as a permanent zone port. See
[`operations/podman-quadlet.md`](operations/podman-quadlet.md) before enabling
LAN access or changing `StrictForwardPorts`.

## Files installed or created

| Path | Purpose |
| --- | --- |
| `/usr/bin/kitpro-api` | Unprivileged control-plane API |
| `/usr/libexec/kitpro-helper` | Root helper with the bounded KITPro protocol |
| `/etc/sysconfig/kitpro-server` | Rocky package configuration |
| `/etc/containers/systemd/kitpro-*` | Generated Quadlet definitions |
| `/etc/kitpro-server/runtime` | Root-only environment and runtime metadata |
| `/srv/kitpro/apps` | Persistent application data |
| `/var/lib/kitpro-api` | API database and backups |
| `/var/lib/kitpro-helper` | Helper database, secrets, ownership, and backups |

See [`operations/podman-quadlet.md`](operations/podman-quadlet.md) for normal
operation and [`security/selinux-rocky-podman.md`](security/selinux-rocky-podman.md)
before changing labels.
