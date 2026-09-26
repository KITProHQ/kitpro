#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
packaging_dir=$(cd -- "$script_dir/.." && pwd)
spec=$packaging_dir/rpm/kitpro-server.spec.in
rocky_unit=$packaging_dir/systemd/kitpro-helper-rocky.service
acme_unit=$packaging_dir/systemd/kitpro-acme-rocky.service
cloudflare_unit=$packaging_dir/systemd/kitpro-cloudflare-rocky.service
validator_unit=$packaging_dir/systemd/kitpro-ingress-validator-rocky.service
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
grep -q '^RestrictAddressFamilies=AF_UNIX AF_NETLINK$' "$rocky_unit"
grep -q 'go build .* -o kitpro-acme ./cmd/kitpro-acme' "$spec"
grep -q 'go build .* -o kitpro-cloudflare ./cmd/kitpro-cloudflare' "$spec"
grep -q 'go build .* -o kitpro-ingress-validator ./cmd/kitpro-ingress-validator' "$spec"
grep -q 'kitpro-ingress-validator.service' "$spec"
grep -q 'kitpro-ingress-reload.path' "$spec"
grep -q 'kitpro-ingress-reload.service' "$spec"
grep -q 'kitpro-ingress-reload.path' "$uninstaller"
grep -q '^PrivateNetwork=yes$' "$validator_unit"
grep -q '^CapabilityBoundingSet=CAP_NET_BIND_SERVICE$' "$validator_unit"
grep -q '^AmbientCapabilities=CAP_NET_BIND_SERVICE$' "$validator_unit"
grep -q 'kitpro-acme.service' "$spec"
grep -q '^CapabilityBoundingSet=CAP_CHOWN$' "$acme_unit"
grep -q '^CapabilityBoundingSet=CAP_SETUID CAP_SETGID$' "$cloudflare_unit"
if grep -q '^AmbientCapabilities=' "$cloudflare_unit"; then
    printf 'Cloudflare tunnel runtime would inherit worker capabilities\n' >&2
    exit 1
fi
grep -q 'kitpro_acme_exec_t' "$packaging_dir/selinux/kitpro.fc"
grep -q '^files_type(kitpro_acme_runtime_t)$' "$packaging_dir/selinux/kitpro.te"
grep -q 'corenet_tcp_connect_http_port(kitpro_acme_t)' "$packaging_dir/selinux/kitpro.te"
grep -q 'kitpro_cloudflare_exec_t' "$packaging_dir/selinux/kitpro.fc"
grep -q 'kitpro_cloudflare_state_t' "$packaging_dir/selinux/kitpro.fc"
grep -q '^files_type(kitpro_cloudflare_runtime_t)$' "$packaging_dir/selinux/kitpro.te"
grep -q 'attribute unreserved_port_type;' "$packaging_dir/selinux/kitpro.te"
grep -q 'allow kitpro_cloudflare_t unreserved_port_type:udp_socket { recv_msg send_msg };' "$packaging_dir/selinux/kitpro.te"
grep -q 'allow kitpro_cloudflare_t self:capability { setgid setuid };' "$packaging_dir/selinux/kitpro.te"
grep -q 'kitpro-cloudflared' "$packaging_dir/rpm/kitpro-server.sysusers"
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

if grep -q 'files_runtime_file' "$packaging_dir/selinux/kitpro.te"; then
    printf 'SELinux policy uses a runtime-file interface unavailable on Rocky Linux 10\n' >&2
    exit 1
fi

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
