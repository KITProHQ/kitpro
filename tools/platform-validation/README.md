# Platform-validation tooling

These scripts prepare and test already-created disposable reference VMs. They do not create VMs, choose the Phase 1 reference distribution, or install production KITPro services.

The scripts deliberately separate three concerns:

- [`install-docker.sh`](../install-docker.sh) owns the Debian and Arch development Docker setup.
- [`install-rocky.sh`](../install-rocky.sh) owns the Rocky 10 native Podman setup.
- [`verify-host.sh`](verify-host.sh) performs read-only reference-host acceptance checks.
- The preparation and fixture runners own disposable validation setup. They do not add users to the Docker group, alter SELinux mode, or change firewall policy.

## Safety gate

Create a clean VM and take a hypervisor snapshot named `clean-os` before running
a preparation script. Use a `docker-installed` snapshot for the Debian fixture
or a `podman-installed` snapshot for Rocky. Mutation and recovery runners
require `--acknowledge-disposable-vm`. The exact provisioning sequence is in
[`docs/testing/platform-provisioning-plan.md`](../../docs/testing/platform-provisioning-plan.md).

Do not run these scripts on a workstation, production host, or Docker host with important workloads. Snapshot restoration is the authoritative cleanup.

## Debian 13 procedure

Target: Debian 13 amd64, systemd PID 1, cgroup v2, 2 vCPU, 4 GiB RAM, 40 GiB disk, ext4, and a private network.

From a reviewed checkout in the guest:

```sh
sudo tools/platform-validation/verify-host.sh --platform debian13
sudo tools/platform-validation/prepare-debian.sh \
  --acknowledge-disposable-vm \
  --yes
sudo tools/platform-validation/run-privilege-fixture.sh \
  --platform debian13 \
  --acknowledge-disposable-vm
```

If the clean image contains packages that conflict with Docker CE, inspect the reported list. Rerun preparation with `--remove-conflicts` only when removal is intended. The flag removes only Docker's documented conflict set and does not delete `/var/lib/docker`.

## Rocky Linux 10 procedure

Target: Rocky Linux 10 x86_64, systemd PID 1, cgroup v2, 2 vCPU, 4 GiB RAM, approximately 40 GiB storage, a private network, and SELinux Enforcing.

```sh
sudo tools/platform-validation/verify-host.sh --platform rocky10
sudo tools/platform-validation/prepare-rocky.sh \
  --acknowledge-disposable-vm \
  --yes
sudo tools/platform-validation/validate-rocky-runtime.sh \
  --acknowledge-disposable-vm
```

The preparation path installs Podman, crun, container-selinux, firewalld, and
diagnostics from Rocky's native repositories. It does not add Docker's
repository, install Docker, disable firewalld, or alter SELinux mode.

Stop if SELinux is not Enforcing or if an AVC blocks the test. Follow [`docs/testing/selinux-rocky.md`](../../docs/testing/selinux-rocky.md); do not disable enforcement or generate broad policy.

The 2026-09-12 Docker results remain historical evidence only. They do not
certify the Podman path. The native runner requires `container_t`, non-empty
process and mount labels, and no recent unexplained AVC. Follow the current
[`Rocky validation checklist`](../../docs/testing/rocky-linux-10-validation.md).

## Capturing an immutable record

Copy the relevant template in [`docs/testing/results/`](../../docs/testing/results/) before the run. Capture the terminal with `script`, then hash the raw transcript. Use a new timestamped filename for every attempt and never overwrite a prior result.

```sh
mkdir -p validation-output
script --return --flush \
  --log-out validation-output/debian-13-YYYYMMDDTHHMMSSZ.log \
  --command 'sudo tools/platform-validation/run-privilege-fixture.sh --platform debian13 --acknowledge-disposable-vm'
sha256sum validation-output/debian-13-YYYYMMDDTHHMMSSZ.log \
  > validation-output/debian-13-YYYYMMDDTHHMMSSZ.log.sha256
```

Use `rocky10` and a Rocky-specific filename on Rocky. Store the reviewed result document in `docs/testing/results/`; retain raw transcripts according to project evidence policy. Do not include credentials, registry tokens, or secrets.

## What the core runner covers

The runner validates the exact host, Docker service, systemd socket permissions, real `SO_PEERCRED`, denial of direct Docker access, denial of an unrelated Unix user, helper Docker API access, malformed and oversized request rejection, forbidden Docker attributes, digest identity, fixed safe container settings, one internal per-instance bridge, no host ports, unrelated-container protection, duplicate operation handling, helper restart, Docker outage and restart, and exact test-resource cleanup.

It intentionally does not automate snapshot-dependent ownership disagreement cases, mount substitution, high-frequency race injection, or arbitrary interruption points. Complete those from [`prototypes/privilege-boundary/RUNBOOK.md`](../../prototypes/privilege-boundary/RUNBOOK.md), recording each as `PASS`, `FAIL`, `BLOCKED`, `NOT RUN`, or `OBSERVATION`.

## Docker group authority check

The required default tests are operational: root reaches Docker, an ordinary user is denied, the API-test identity is denied, and the helper reaches Docker. The claim that Docker group membership is root-equivalent is also documented by [Docker's post-install warning](https://docs.docker.com/engine/install/linux-postinstall/).

If an operational group-membership check is desired, take a snapshot and use a separate throwaway user. Never use the API-test identity. Record that the user is denied before membership and can query the daemon from a fresh credential context after explicit membership. Do not launch a privileged container or mount the host root merely to demonstrate the known consequence. Restore the snapshot immediately afterward.
