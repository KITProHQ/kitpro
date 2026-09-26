# Test KITPro Server on Rocky Linux 10

Rocky Linux 10 and Podman are Experimental in alpha.13. They are outside the
Supported Debian, Ubuntu, and Arch Linux baseline. Passing an acceptance run
does not promote either one to Supported.

> KITPro Server is active alpha software. Breaking changes and incomplete
> workflows may occur. Review the [alpha.13 current state](product/kitpro-server-current-state.md)
> and [known limitations](release/known-limitations.md) before using the
> Experimental Rocky path with important data.

The Rocky path uses rootful Podman, Quadlet, systemd, SELinux Enforcing, crun,
and firewalld. KITPro does not publish a Supported Rocky package repository.
Use this path only for development and validation.

Alpha.13 has a known Experimental limitation. The package, SELinux policy,
host checks, storage-root registration, and reboot recovery pass, but normal
application installation fails before runtime creation. The staged-generation
lifecycle requires a contract that the Podman/Quadlet adapter does not yet
implement. Do not bypass the API, call helper operations manually, or manage
generated Quadlet files to work around this limit.

## Requirements

- Rocky Linux 10 x86_64
- systemd as PID 1 and cgroup v2
- SELinux `Enforcing`
- firewalld enabled and active
- 2 CPUs, 4 GiB RAM, and 20 GiB free system storage at minimum
- root access for package installation

The experimental installer rejects RHEL 10 and AlmaLinux 10. Neither platform
is installable or supported.

## Use the frozen alpha.13 source

The Experimental Rocky scripts live in the frozen alpha.13 source tag. Check
out that tag before you run or build them:

```sh
git clone https://github.com/KITProHQ/kitpro.git
cd kitpro
git checkout v0.1.0-alpha.13
```

Do not substitute the current development branch for the frozen release source.

## Prepare the native runtime

Review `tools/install-rocky.sh`, then run:

```sh
sudo ./tools/install-rocky.sh --yes
```

The script installs `podman`, `crun`, `container-selinux`, `firewalld`, and the
native policy and diagnostic tools. It enables firewalld without opening a
port. It then verifies Podman 5 or newer, crun, cgroup v2, the storage driver,
networking, Quadlet, and SELinux state.

## Build experimental RPMs

Build on Rocky Linux 10. Install the build dependencies from Rocky repositories:

```sh
sudo dnf install -y rpm-build golang selinux-policy-devel selinux-policy-targeted
./software/server/packaging/build-rpm.sh 0.1.0~alpha13
```

The build writes `kitpro-server`, `kitpro-selinux`, and source RPM files under
`software/server/dist/`. Each package has an adjacent SHA-256 file. These are
locally built Experimental packages, not published supported artifacts.

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
loopback or configured LAN address.

The frozen source contains the detailed Rocky validation plan, Podman and
Quadlet operations notes, and SELinux policy notes. Treat older alpha.12 live
application evidence as historical. It does not override the alpha.13
staged-lifecycle limitation.
