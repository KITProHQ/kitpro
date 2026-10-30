# Public Docker installer follow-up

## Scope

This is a future standalone task. It records how [`tools/install-docker.sh`](../../tools/install-docker.sh) could inform a replacement for the script documented on the [KeepItTechie Docker and Docker Compose page](https://wiki.kitpro.us/en/articles/docker-script). This task did not change the wiki, its downloadable asset, or any external repository.

## Current public-page limitations

The page and its linked `/install-docker.sh` asset were both inspected read-only on 2026-09-12. They are specific to Ubuntu Server 24.04 LTS and use the same workflow: install Docker Engine, Buildx, and the Compose plugin from Docker's Ubuntu repository, then add a selected account to the Docker group. The current workflow has several limits for a broader public script:

- it does not detect or reject unsupported distributions and versions;
- it does not support Debian, Ubuntu 26.04, RHEL, Rocky, or AlmaLinux;
- it automatically adds a guessed current account to the `docker` group;
- its fallback can select `SUDO_USER`, `logname`, or the current effective user rather than refusing ambiguity;
- it does not explain that Docker group membership grants root-level authority;
- it verifies CLI version output but not daemon activity, daemon response, containerd, Buildx, cgroup mode, or SELinux state;
- it has no dry-run or verify-only mode;
- it has no explicit handling for conflicting distribution packages; and
- it uses an older one-line `.list` repository example, while Docker's current Debian and Ubuntu documentation uses a deb822 `.sources` file and a key in `/etc/apt/keyrings`.

The page does install the current `docker-compose-plugin` package and documents the correct `docker compose` command family. No legacy standalone `docker-compose` package is needed.

## Candidate replacement behavior

The repository script currently provides:

- exact `/etc/os-release` checks for Debian 13, Ubuntu 24.04/26.04, and RHEL/Rocky/AlmaLinux 9/10;
- Debian and Ubuntu repository separation;
- Docker's RHEL repository for the RHEL family, with a visible derivative-support caveat for Rocky and AlmaLinux;
- official Docker Engine, CLI, containerd, Buildx, and Compose plugin packages;
- signing-key fingerprint verification;
- explicit refusal to remove conflicting packages without `--remove-conflicts`;
- no Docker group grant by default;
- an opt-in `--grant-user-access` that requires an unambiguous non-root `SUDO_USER`;
- `--dry-run`, `--verify-only`, `--yes`, and an optional `--run-hello-world`;
- daemon, Engine, Compose, Buildx, containerd, cgroup, and SELinux reporting;
- a minimal validated Docker SELinux daemon setting on SELinux-enabled RHEL-family hosts when no existing daemon configuration is present; and
- no SELinux enforcement or firewall mutation.

## 2026-09-12 real-host observations

### Rocky Linux 10.2

The installer completed on a clean Rocky Linux 10.2 VM without a manual package workaround. It selected Docker's RHEL repository, printed the derivative-support warning, verified the repository key, installed the intended component set, enabled Docker and containerd, and did not add `josh` to the Docker group. Installed versions were Docker Engine/CLI 29.8.0, containerd 2.3.5, Buildx 0.37.1, and Compose 5.5.1.

SELinux remained Enforcing and firewalld remained active. The installer did not weaken either control.

The initial run exposed an important documentation and verification gap: Enforcing host mode did not mean Docker was using SELinux container labels. Docker reported only seccomp and cgroup namespaces, and the test container ran as `spc_t` with empty process and mount labels. The installed `container-selinux` package supplied policy but did not enable Docker's SELinux integration.

A follow-up enabled Docker's documented `selinux-enabled` daemon setting. Docker then reported `name=selinux`, and disposable workloads received `container_t` process and `container_file_t` mount labels with MCS categories. The corrected full helper run passed while SELinux remained Enforcing and firewalld remained active.

The development installer now creates the minimal validated daemon configuration on an SELinux-enabled RHEL-family host only when `/etc/docker/daemon.json` does not already exist, restarts an already-active daemon after creating that configuration, and verifies Docker's effective SELinux option when the host is Enforcing. It refuses to perform an unsafe shell-level merge into administrator configuration. The daemon setting was validated manually on Rocky and the installer branches pass repository tests, but a clean-snapshot Rocky installation with the corrected script is still `NOT RUN`. Before public use, this behavior also needs idempotent install, upgrade, pre-existing configuration, and rollback coverage across every claimed RHEL-family release.

Docker also created firewalld `docker`-zone membership, a `docker-forwarding` policy, and iptables-nft chains. Public documentation must explain that published container ports interact with Docker-managed firewall rules even though the installer itself does not rewrite global firewall policy.

### Debian 13

The first Debian run stopped correctly because the candidate VM's 40 GiB disk had only an 8.4 GiB root filesystem with 7.0 GiB free; the remaining space was assigned mostly to `/home`. After the disposable VM was corrected to a 40 GiB ext4 root with 36.6 GiB free and a new clean snapshot was taken, the installer completed without a manual workaround.

The installer selected Docker's Debian repository, verified the signing key, installed Docker Engine/CLI 29.8.0, containerd 2.3.5, Buildx 0.37.1, and Compose 5.5.1, and left `josh` outside the Docker group. Docker used cgroup v2 with the systemd driver and overlayfs. AppArmor loaded `docker-default` in enforce mode. The full privilege fixture, Docker outage/recovery, reboot, and cleanup then passed.

This demonstrates that checking nominal disk size is insufficient. The public script should report available space on the filesystem that will hold Docker data and fail or warn according to a documented minimum. KITPro's 20 GiB minimum covers only system/KITPro capacity; application data, images, media, backups, and databases need additional planning.

Docker documents Debian 13 and Ubuntu 24.04/26.04 directly. Docker also documents RHEL 9/10, but its general platform guidance warns that derivative distributions are not tested or verified. Public documentation must say that Rocky and AlmaLinux use a RHEL-compatible path validated by KITPro, not that Docker independently certifies those derivatives.

## Validation required before publication

- Run installation, idempotent rerun, `--verify-only`, and package upgrade on every claimed distribution/version pair.
- Test each documented architecture before claiming it publicly.
- Test fresh hosts and hosts with every detected conflict package.
- Verify unattended key import and fingerprint failure behavior.
- Confirm Docker, containerd, Buildx, and Compose version output and failure modes.
- Verify no user gains Docker group membership by default.
- Test ambiguous `sudo`, direct root, non-root, CI, and opt-in group-grant contexts.
- Preserve SELinux Enforcing on RHEL-family hosts and record firewalld interaction.
- Report host SELinux mode and Docker's effective SELinux integration as separate facts.
- Test absent, valid existing, invalid existing, and conflicting `daemon.json` cases without overwriting administrator settings.
- Test Docker data-root capacity rather than relying on the VM or physical disk's nominal size.
- Test repository behavior across distribution point releases and distribution upgrades.
- State Rocky Linux 10's x86-64-v3 requirement and test behavior on supported and older AMD/Intel hardware.
- Decide whether public releases pin a tested Docker major/package version instead of always installing the latest stable release.
- Review Docker's support matrix immediately before release.
- Add a versioned release, checksum, change log, and rollback guidance for the public script.

## Backward compatibility and migration

Existing readers may expect `./install-docker.sh` to grant passwordless Docker access automatically. Changing that default is intentional and security-relevant. The public page should explain that root or `sudo docker` is now the default, show the explicit opt-in, and require a new login only when the operator selected that option.

The updated page should also:

- list exact supported distributions and versions;
- explain Docker's official versus KITPro-validated derivative support;
- show download, inspection, checksum verification, and local execution instead of presenting `curl | bash` as the primary path;
- document conflict removal as an explicit decision;
- use `docker compose`, not `docker-compose`;
- describe firewall implications of published container ports;
- state that the script does not configure production daemon policy, rootless Docker, application networks, or KITPro Server; and
- separate fresh installation from tested upgrade behavior.

The Debian and Rocky reference-host runs are complete. Publication should still wait for the broader supported-distribution, upgrade, and existing-configuration installer matrix. The current development script is a candidate basis, not yet a public compatibility promise.
