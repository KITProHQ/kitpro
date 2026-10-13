# Rocky Linux 10 validation checklist

Rocky remains experimental until one immutable clean-host result passes every
required row. Use a disposable VM with SELinux Enforcing, firewalld enabled,
and no Docker installation. Record exact package versions, commands, timestamps,
VM identity, source revision, and transcript SHA-256.

## Fresh install and host runtime

- [ ] `/etc/os-release` is Rocky Linux 10 x86_64.
- [ ] systemd is PID 1 and the host uses cgroup v2.
- [ ] `getenforce` remains `Enforcing` before and after every phase.
- [ ] firewalld remains enabled and active.
- [ ] Docker packages, service, and socket are absent.
- [ ] Podman 5+, crun, storage driver, network backend, and Quadlet versions are recorded.
- [ ] Both RPMs install from a reviewed local build.
- [ ] `kitpro-api.service` and `kitpro-helper.socket` are active.

Run the read-only runtime check after initial setup:

```sh
sudo tools/platform-validation/validate-rocky-runtime.sh \
  --acknowledge-disposable-vm
```

## Application

- [ ] The UI loads on loopback.
- [ ] Initial account setup, login, logout, and password change work.
- [ ] A single-container application installs from its pinned OCI digest.
- [ ] Paperless-ngx installs with two components and service-name DNS.
- [ ] Start, stop, recreate, update, backup, restore, and remove behave correctly.
- [ ] Runtime environment values match the catalog without appearing in Quadlets or process arguments.

After installing a test application:

```sh
sudo tools/platform-validation/validate-rocky-runtime.sh \
  --acknowledge-disposable-vm --require-app
```

## Storage and networking

- [ ] Managed database and application data survive container recreation.
- [ ] UID/GID ownership matches every manifest.
- [ ] Read-only and read-write imported roots behave as declared.
- [ ] Missing or substituted imported mounts fail closed.
- [ ] Container DNS resolves component aliases.
- [ ] Private mode publishes no port.
- [ ] Loopback mode is reachable only on the assigned loopback address.
- [ ] LAN mode uses one exact address and only the required firewalld rule, if any.
- [ ] IPv4, callbacks, and reverse-proxy communication behave as declared.

## Reboot and failure recovery

- [ ] Reboot the host and confirm all previously running units return.
- [ ] Confirm intentionally stopped applications remain stopped.
- [ ] Confirm data, secrets, networks, ports, and authentication persist.
- [ ] Kill one restart-enabled container process and confirm recovery:

```sh
sudo tools/platform-validation/validate-rocky-runtime.sh \
  --acknowledge-disposable-vm --require-app --exercise-recovery
```

- [ ] Stop/start an application unit and the control-plane services.
- [ ] Test slow database startup for the multi-container application.

## SELinux

- [ ] Every application process is `container_t`, never `spc_t`.
- [ ] Every container has non-empty process and mount labels.
- [ ] Managed `:Z` directories have expected MCS labels.
- [ ] Imported storage uses an exact reviewed label treatment.
- [ ] `ausearch -m AVC,USER_AVC` shows no unexplained denial in each test window.
- [ ] No permissive mode, broad generated policy, boolean, or privileged container was used.

## Upgrade and uninstall

- [ ] Install the oldest compatible Rocky RPM build and create test data.
- [ ] Upgrade both RPMs; verify backups, migration, reload, recreation, and health.
- [ ] Remove `kitpro-server`; verify containers stop and runtime configuration is archived.
- [ ] Reinstall; verify previously running/stopped state and preserved data.
- [ ] On a separate snapshot, run acknowledged purge and verify only KITPro-owned data is removed.
- [ ] Confirm Podman, unrelated containers, firewalld, and unrelated SELinux configuration remain.

## Promotion gate

Do not change Rocky to supported while any row is `FAIL`, `BLOCKED`, or `NOT
RUN`. GPU capability is a separate support claim and does not block CPU-only
Rocky support if the UI fails it closed and the matrix says it is unavailable.
