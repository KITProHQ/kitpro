#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
packaging_dir=$(cd -- "$script_dir/.." && pwd)
spec=$packaging_dir/rpm/kitpro-server.spec.in
rocky_unit=$packaging_dir/systemd/kitpro-helper-rocky.service
uninstaller=$packaging_dir/rpm/kitpro-server-uninstall
podman_command=$packaging_dir/rpm/kitpro-podman-command

bash -n "$packaging_dir/build-rpm.sh" "$uninstaller" "$podman_command"
if command -v shellcheck >/dev/null 2>&1; then
    shellcheck "$packaging_dir/build-rpm.sh" "$uninstaller" "$podman_command"
fi
grep -q -- '--define "source_date_epoch_from_changelog 0"' "$packaging_dir/build-rpm.sh"
grep -q -- '--define "clamp_mtime_to_source_date_epoch 1"' "$packaging_dir/build-rpm.sh"
grep -q -- '--define "use_source_date_epoch_as_buildtime 1"' "$packaging_dir/build-rpm.sh"
grep -Fq 'work_dir=/var/tmp/kitpro-rpm-build-$source_commit' "$packaging_dir/build-rpm.sh"
grep -Fq 'mkdir -m 0700 -- "$work_dir"' "$packaging_dir/build-rpm.sh"

grep -q '^Requires:       podman >= 5$' "$spec"
grep -q '^%global debug_package %{nil}$' "$spec"
grep -q '^Requires:       crun$' "$spec"
grep -q '^Requires:       container-selinux$' "$spec"
grep -q '^Requires:       kitpro-selinux' "$spec"
grep -q 'only on Rocky Linux 10' "$spec"
grep -q 'SELinux must be Enforcing' "$spec"
grep -q 'kitpro-server-uninstall --preserve-data' "$spec"
grep -q '^Environment=KITPRO_CONTAINER_RUNTIME=podman$' "$rocky_unit"
grep -q '^Environment=KITPRO_PODMAN_PATH=/usr/libexec/kitpro-podman-command$' "$rocky_unit"
grep -q '/etc/containers/systemd' "$rocky_unit"
grep -q '/etc/kitpro-server/runtime' "$rocky_unit"
grep -q '^NoNewPrivileges=yes$' "$rocky_unit"
grep -q '^CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$rocky_unit"
grep -q '^AmbientCapabilities=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$rocky_unit"
grep -q '^d /var/lib/kitpro-helper/application-backups 0700 root root -$' "$packaging_dir/tmpfiles/kitpro.conf"
grep -q -- '--property=CapabilityBoundingSet=CAP_SYS_ADMIN' "$podman_command"
grep -q -- "SystemCallFilter=open_tree fsopen fsconfig fsmount move_mount mount_setattr mount umount2" "$podman_command"
grep -Fq "valid_container_ref='^(kitpro-[a-z0-9-]{3,220}|[a-f0-9]{64})$'" "$podman_command"
grep -q -- '--property="StandardOutput=truncate:\$runtime_output"' "$podman_command"
grep -q -- '--property="StandardError=truncate:\$runtime_error"' "$podman_command"
if grep -q -- '--pipe' "$podman_command"; then
    printf 'Podman command wrapper must not proxy output through the socket-activated helper\n' >&2
    exit 1
fi
grep -q -- '/usr/bin/podman "\$@"' "$podman_command"
grep -q 'container_file_t' "$packaging_dir/selinux/kitpro.fc"
grep -q -- '--acknowledge-destroy-data' "$uninstaller"

if grep -R -E 'setenforce[[:space:]]+0|SELINUX=disabled|systemctl (disable|stop).*firewalld|--privileged|docker-ce|download\.docker\.com' \
    "$packaging_dir/rpm" "$packaging_dir/selinux" "$rocky_unit"; then
    printf 'unsafe Rocky packaging behavior found\n' >&2
    exit 1
fi

if "$podman_command" inspect --format json invalid-container-ref >/dev/null 2>&1; then
    printf 'Podman command wrapper accepted an invalid container reference\n' >&2
    exit 1
fi

printf 'RPM package static tests: PASS\n'
