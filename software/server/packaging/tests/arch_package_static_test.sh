#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/../.." && pwd)
arch_dir="$server_dir/packaging/arch"
upgrade_script="$arch_dir/kitpro-arch-upgrade"
upgrade_hook="$arch_dir/kitpro-server-upgrade.hook"

bash -n "$server_dir/packaging/build-arch-package.sh" "$arch_dir/kitpro-server.install" "$upgrade_script"
grep -Fq 'chown kitpro-api:kitpro-api "$KITPRO_CONTROL_DB"' "$arch_dir/kitpro-server.install"
grep -Fq 'chmod 0600 "$KITPRO_CONTROL_DB"' "$arch_dir/kitpro-server.install"
grep -Fq 'chown root:root "$KITPRO_HELPER_DB"' "$arch_dir/kitpro-server.install"
grep -Fq 'chmod 0600 "$KITPRO_HELPER_DB"' "$arch_dir/kitpro-server.install"
shellcheck "$server_dir/packaging/build-arch-package.sh" "$arch_dir/kitpro-server.install" "$upgrade_script"
grep -Fxq "depends=('apparmor' 'docker' 'iproute2' 'iputils' 'nginx' 'systemd' 'util-linux')" "$arch_dir/PKGBUILD"
grep -Fxq "license=('Apache-2.0')" "$arch_dir/PKGBUILD"
grep -Fxq "options=('!debug' '!strip')" "$arch_dir/PKGBUILD"
grep -Fq 'local public_version=${pkgver/_/"~"}' "$arch_dir/PKGBUILD"
grep -q 'EnvironmentFile=-/etc/conf.d/kitpro-server' "$server_dir/packaging/systemd/kitpro-api.service"
grep -q 'EnvironmentFile=-/etc/conf.d/kitpro-server' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -q '^d /var/lib/kitpro-helper/application-backups 0700 root root -$' "$server_dir/packaging/tmpfiles/kitpro.conf"
grep -q '^CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -q '^AmbientCapabilities=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -Fqx 'BindReadOnlyPaths=-/dev/disk' "$server_dir/packaging/systemd/kitpro-api.service"
grep -Fqx 'BindReadOnlyPaths=-/dev/disk' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -Fqx '  /etc/kitpro/storage-management.json r,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -Fqx '  /etc/kitpro/network-management.json r,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -Fqx '  network netlink raw,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -q '^RestrictAddressFamilies=AF_UNIX AF_NETLINK$' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -Fq '/etc/systemd/network' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -Fq '/run/systemd/system' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -Fqx '  /dev/disk/by-uuid/* r,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -q 'go build .* -o kitpro-acme ./cmd/kitpro-acme' "$arch_dir/PKGBUILD"
grep -q 'go build .* -o kitpro-ingress-validator ./cmd/kitpro-ingress-validator' "$arch_dir/PKGBUILD"
grep -q 'kitpro-acme.service' "$arch_dir/kitpro-server.install"
grep -q 'usr.libexec.kitpro-acme' "$arch_dir/kitpro-server.install"
grep -q 'kitpro-ingress-validator.service' "$arch_dir/kitpro-server.install"
grep -q 'usr.libexec.kitpro-ingress-validator' "$arch_dir/kitpro-server.install"
grep -q 'kitpro-ingress-reload.path' "$arch_dir/kitpro-server.install"
grep -q 'kitpro-ingress-reload.path' "$arch_dir/PKGBUILD"
grep -q 'kitpro-ingress-reload.service' "$arch_dir/PKGBUILD"
grep -q '^CapabilityBoundingSet=CAP_CHOWN$' "$server_dir/packaging/systemd/kitpro-acme.service"
if grep -Rqi 'cloudflare' "$arch_dir/PKGBUILD" "$arch_dir/kitpro-server.sysusers"; then
    printf 'Arch package still exposes the deferred Cloudflare capability\n' >&2
    exit 1
fi
grep -Fq 'retire_deferred_cloudflare' "$arch_dir/kitpro-server.install"
grep -Fq 'systemctl disable --now kitpro-cloudflare.service' "$arch_dir/kitpro-server.install"
grep -Fq "profile /usr/libexec/kitpro-cloudflare {}" "$arch_dir/kitpro-server.install"
grep -Fq 'apparmor_parser -R "$removal_profile"' "$arch_dir/kitpro-server.install"
grep -Fq 'rm -f -- "$profile"' "$arch_dir/kitpro-server.install"
grep -Fq 'userdel kitpro-cloudflared' "$arch_dir/kitpro-server.install"
grep -Fq 'groupdel kitpro-cloudflared' "$arch_dir/kitpro-server.install"
if grep -Eq '^CapabilityBoundingSet=.*CAP_(SYS_ADMIN|DAC_OVERRIDE|MKNOD)' "$server_dir/packaging/systemd/kitpro-helper.service"; then
    printf 'helper gained an unapproved capability\n' >&2
    exit 1
fi
grep -q 'aa-enabled' "$arch_dir/kitpro-server.install"
grep -q 'apparmor_parser -r -W -T' "$arch_dir/kitpro-server.install"
grep -q '/usr/libexec/kitpro-helper --verify-host-prerequisites' "$arch_dir/kitpro-server.install"
grep -q 'restore_previous_services' "$arch_dir/kitpro-server.install"
grep -Fqx '  link subset /var/lib/kitpro-helper/backups/.kitpro-upgrade-*.partial/pre-upgrade-helper-to-*.db -> /var/lib/kitpro-helper/backups/.kitpro-upgrade-*.partial/.pre-upgrade-helper-to-*.db.partial-*,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -Fqx '  link subset /var/lib/kitpro-helper/backups/pre-upgrade-helper-to-*.db -> /var/lib/kitpro-helper/backups/.pre-upgrade-helper-to-*.db.partial-*,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -Fqx '  link subset /var/lib/kitpro-helper/backups/.host-backup-*/helper.db -> /var/lib/kitpro-helper/backups/.host-backup-*/.helper.db.partial-*,' "$server_dir/packaging/apparmor/kitpro-helper"
test "$(grep -Ec '^  link ' "$server_dir/packaging/apparmor/kitpro-helper")" -eq 3
if grep -Eq '^  (/var/lib/kitpro-helper/\*\*|/var/lib/kitpro-helper/backups/\*\*) [^,]*l[^,]*,$' "$server_dir/packaging/apparmor/kitpro-helper"; then
    printf 'helper gained broad hard-link authority\n' >&2
    exit 1
fi
grep -q 'require_upgrade_approval' "$arch_dir/kitpro-server.install"
grep -q '^When = PreTransaction$' "$upgrade_hook"
grep -q '^AbortOnFail$' "$upgrade_hook"
grep -q '^Exec = /usr/libexec/kitpro-arch-upgrade --preflight-installed$' "$upgrade_hook"
grep -q 'usr/libexec/kitpro-arch-upgrade' "$arch_dir/PKGBUILD"
grep -q 'usr/share/libalpm/hooks/90-kitpro-server-upgrade.hook' "$arch_dir/PKGBUILD"
grep -q 'kitpro-server-${pkgver}-${pkgrel}-upgrade.sh' "$server_dir/packaging/build-arch-package.sh"
grep -q '@KITPRO_PACKAGE_SHA256@' "$upgrade_script"
grep -q '@KITPRO_PACKAGE_VERSION@' "$upgrade_script"
grep -Fq 's/@KITPRO_PACKAGE_SHA256@/$package_sha256/' "$server_dir/packaging/build-arch-package.sh"
grep -Fq 's/@KITPRO_PACKAGE_VERSION@/$package_version/' "$server_dir/packaging/build-arch-package.sh"
grep -q 'package SHA-256 does not match this upgrade wrapper' "$upgrade_script"
grep -q 'package metadata .PKGINFO is missing or unreadable' "$upgrade_script"
grep -Fq '"$bsdtar_command" -xOf "$package" .PKGINFO' "$upgrade_script"
grep -Fq 'installed_hook=$(path_in_root /usr/share/libalpm/hooks/90-kitpro-server-upgrade.hook)' "$upgrade_script"
grep -Fq 'etc/apparmor.d/usr.libexec.kitpro-helper' "$upgrade_script"
grep -Fq '"$apparmor_parser_command" -Q -T "$incoming_profile"' "$upgrade_script"
grep -Fq '"$apparmor_parser_command" -r -W -T "$incoming_profile"' "$upgrade_script"
grep -Fq 'restore_installed_profile || true' "$upgrade_script"
forbidden_option='--print-''format'
if grep -R -F -- "$forbidden_option" "$upgrade_script" "$server_dir/packaging/tests"; then
    printf 'Arch wrapper still relies on the unsupported pacman formatting option\n' >&2
    exit 1
fi
grep -q -- '--preflight-extracted' "$upgrade_script"
grep -q -- '--hookdir' "$upgrade_script"
grep -q 'AbortOnFail' "$upgrade_script"
grep -q -- '--verify-database' "$upgrade_script"
grep -q -- '--prepare-upgrade' "$upgrade_script"
grep -q 'usr/share/kitpro-server/kitpro-server.conf' "$arch_dir/PKGBUILD"
grep -q 'usr/share/licenses/kitpro-server/COPYING' "$arch_dir/PKGBUILD"
grep -q 'usr/share/man/man8/kitpro-api.8' "$arch_dir/PKGBUILD"
grep -q 'usr/share/man/man8/kitpro-helper.8' "$arch_dir/PKGBUILD"
grep -q 'invalid source commit' "$server_dir/packaging/build-arch-package.sh"
grep -q 'invalid source epoch' "$server_dir/packaging/build-arch-package.sh"
grep -Fq '[[ -e "$repo_dir/$path" ]] || continue' "$server_dir/packaging/build-arch-package.sh"
grep -q 'install -Dm0644 /usr/share/kitpro-server/kitpro-server.conf /etc/conf.d/kitpro-server' "$arch_dir/kitpro-server.install"
if grep -E 'rm .*/srv/kitpro|rm -rf /var/lib/kitpro' "$arch_dir/kitpro-server.install"; then
    printf 'Arch removal deletes persistent or trusted state\n' >&2
    exit 1
fi
if grep -R -E 'usermod.*docker|gpasswd.*docker' "$arch_dir"; then
    printf 'Arch package grants Docker group membership\n' >&2
    exit 1
fi

grep -Fq '/etc/systemd/system' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -Fqx 'd /var/lib/kitpro-helper/storage-persistence 0700 root root -' "$server_dir/packaging/tmpfiles/kitpro.conf"
grep -Fqx '  /etc/systemd/system/*.mount rw,' "$server_dir/packaging/apparmor/kitpro-helper"
grep -Fqx '  /run/udev/data/b* r,' "$server_dir/packaging/apparmor/kitpro-helper"

printf 'Arch package static tests: PASS\n'
