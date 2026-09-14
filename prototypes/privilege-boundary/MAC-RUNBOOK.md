# Disposable helper MAC runbook

This runbook tests the helper's mandatory-access-control boundary on the disposable Debian 13 and Rocky Linux 10 validation VMs. It is not a production installation procedure. Do not run it on the Arch workstation, a host with unrelated workloads, or a production VM.

The runbook uses the existing privilege-boundary fixture and the test-only policy files in this directory. It does not disable AppArmor or SELinux, change a firewall policy, add Docker-group access, or modify the clean snapshots.

## Record the baseline

Use the same repository revision on both VMs. Record the fixture revision, kernel, architecture, systemd version, cgroup mode, filesystem, Docker version, and current MAC state.

On Debian:

```sh
sudo aa-status || true
sudo apparmor_status || true
sudo docker info --format '{{json .SecurityOptions}}'
```

On Rocky:

```sh
getenforce
sestatus
sudo docker info --format '{{json .SecurityOptions}}'
sudo semodule -l | grep -E 'container|kitpro' || true
```

Rocky must remain `Enforcing`. Docker must report `name=selinux`. Stop if either condition is false.

## Stage the fixture

Run the existing core runner from a clean `docker-installed` VM snapshot only when the MAC test needs a fresh fixture. The runner creates the disposable user, state, socket, service, and Docker resources:

```sh
sudo tools/platform-validation/run-privilege-fixture.sh \
  --platform debian13 --acknowledge-disposable-vm
```

Use `--platform rocky10` on Rocky. Complete and record the core run before applying MAC policy. The helper service must be stopped before replacing its unit or executable.

## Debian AppArmor experiment

The profile attaches to a fixed copied interpreter path so the experiment does not profile every system Python process. On the Debian VM, as root:

```sh
install -d -o root -g root -m 0755 /opt/kitpro-privilege-boundary-test/bin
install -o root -g root -m 0755 /usr/bin/python3 /opt/kitpro-privilege-boundary-test/bin/kitpro-pb-helper
install -o root -g root -m 0644 /path/to/kitpro/prototypes/privilege-boundary/apparmor/kitpro-pb-test-helper /etc/apparmor.d/kitpro-pb-test-helper
apparmor_parser -r /etc/apparmor.d/kitpro-pb-test-helper
aa-enforce kitpro-pb-test-helper
```

Copy the fixture source into the paths named by the profile. Install the test-only `kitpro-pb-test-mac.service` and the matching socket unit. The service must use the copied executable and the same arguments as the core unit. Run `systemd-analyze verify` before starting the socket.

Validate positive cases through the API-test identity:

- helper startup and socket activation;
- `SO_PEERCRED` and unauthorized-peer rejection;
- runtime and Docker inspection;
- test container and internal network lifecycle;
- ownership checks and bounded logs;
- receipt and helper-state reads and writes;
- approved storage preparation;
- helper restart; and
- Docker outage followed by recovery.

Run the harmless negative probe from the confined helper context:

```sh
sudo -u kitpro-pb-api-test python3 -m fixture.client --socket /run/kitpro-pb-test/helper.sock ping
sudo journalctl -k --since '10 minutes ago' --no-pager | grep -i apparmor || true
sudo cat /sys/kernel/security/apparmor/profiles | grep kitpro-pb-test-helper
```

The helper must not read the disposable home marker, SSH marker, root marker, unrelated storage marker, or Docker state marker. It must not write `/etc`, `/root`, or firewalld test paths. It must not execute `/bin/sh` or `/usr/bin/id`, and an IPv4 socket attempt must fail. Record each probe and the AppArmor denial. A profile that denies the helper's required Docker socket or state access is a failure to classify, not a reason to add a recursive rule.

## Rocky SELinux experiment

Install only the test policy prerequisites required to compile the module. Do not change SELinux mode. From the repository checkout, review the `.te` and `.fc` files, then compile and load the module using the distribution's policy tooling:

```sh
make -f /usr/share/selinux/devel/Makefile kitpro_pb_helper.pp
sudo semodule -i kitpro_pb_helper.pp
sudo semanage fcontext -a -t kitpro_pb_helper_exec_t /opt/kitpro-privilege-boundary-test/bin/kitpro-pb-helper
sudo semanage fcontext -a -t kitpro_pb_state_t '/var/lib/kitpro-pb-test(/.*)?'
sudo semanage fcontext -a -t kitpro_pb_runtime_t '/run/kitpro-pb-test(/.*)?'
sudo restorecon -RFv /opt/kitpro-privilege-boundary-test /var/lib/kitpro-pb-test /run/kitpro-pb-test
```

If the policy interface or module does not compile, record `BLOCKED` with the exact tool output. Do not replace it with `audit2allow` output. Install the test-only MAC service, start the socket, and record:

```sh
getenforce
ps -eZ | grep kitpro-pb-test || true
ls -ldZ /opt/kitpro-privilege-boundary-test /var/lib/kitpro-pb-test /run/kitpro-pb-test
ls -lZ /run/kitpro-pb-test/helper.sock /var/run/docker.sock
```

Run the same positive lifecycle, ownership, state, storage, helper-restart, and Docker-outage tests as Debian. The helper must run in `kitpro_pb_helper_t` or the exact dedicated domain from the loaded policy. The test container must retain a normal Docker SELinux process and mount label.

Run the same negative probes. Review only fixture-related AVCs:

```sh
sudo ausearch -m AVC,USER_AVC -ts recent
sudo journalctl -k --since '10 minutes ago' --no-pager | grep -i avc || true
```

For each denial, record the source context, target context, class, permission, path, and classification. Add a rule only when the access is required by the approved helper contract. Never make the domain permissive.

## Systemd hardening comparison

Run the MAC service with the candidate hardening in `systemd/kitpro-pb-test-mac.service`. Record the result of every positive test with and without each candidate setting where a comparison is safe. Pay special attention to `RestrictSUIDSGID=yes` and the fixture's descriptor-relative `openat2` path check. The known Rocky observation is `ENOSYS` with that setting. Do not accept a hardening change that disables path containment.

The candidate service includes `NoNewPrivileges`, empty capability and ambient sets, `PrivateTmp`, `ProtectHome`, `ProtectSystem=strict`, explicit writable state, kernel and namespace restrictions, `RestrictAddressFamilies=AF_UNIX`, resource limits, and environment sanitization. It deliberately omits `RestrictSUIDSGID` pending a working equivalent.

## Cleanup and evidence

Record status as `PASS`, `FAIL`, `BLOCKED`, `NOT RUN`, or `OBSERVATION`. Preserve denial and context output without credentials. Remove only test policy, units, copied fixture files, test identities, and fixture-labelled Docker resources after evidence capture. Do not remove the VMs or modify snapshots.

The final records belong in:

- `docs/testing/results/<date>-debian-helper-apparmor.md`;
- `docs/testing/results/<date>-rocky-helper-selinux.md`; and
- `docs/security/helper-confinement-matrix.md`.
