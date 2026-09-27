#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
server_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
first=$(mktemp -d)
second=$(mktemp -d)
unpack=$(mktemp -d)
trap 'rm -rf "$first" "$second" "$unpack"' EXIT HUP INT TERM

GOCACHE=${GOCACHE:-/tmp/kitpro-go-cache} "$server_dir/packaging/build-package.sh" 0.1.0~alpha3 "$first" >/dev/null
GOCACHE=${GOCACHE:-/tmp/kitpro-go-cache} "$server_dir/packaging/build-package.sh" 0.1.0~alpha3 "$second" >/dev/null

one="$first/kitpro-server_0.1.0~alpha3_amd64.deb"
two="$second/kitpro-server_0.1.0~alpha3_amd64.deb"
if grep -q / "$one.sha256"; then
    echo "checksum contains a non-portable path" >&2
    exit 1
fi
(cd "$first" && sha256sum -c "$(basename "$one").sha256" >/dev/null)
test "$(sha256sum "$one" | awk '{print $1}')" = "$(sha256sum "$two" | awk '{print $1}')"

(cd "$unpack" && ar x "$one")
mkdir "$unpack/control" "$unpack/data"
tar -xJf "$unpack/control.tar.xz" -C "$unpack/control"
tar -xJf "$unpack/data.tar.xz" -C "$unpack/data"

test -x "$unpack/data/usr/bin/kitpro-api"
test -x "$unpack/data/usr/libexec/kitpro-helper"
test -x "$unpack/data/usr/libexec/kitpro-acme"
test -x "$unpack/data/usr/libexec/kitpro-cloudflare"
test -x "$unpack/data/usr/libexec/kitpro-ingress-validator"
test -x "$unpack/data/usr/libexec/kitpro-debian-upgrade"
test -x "$first/kitpro-debian-upgrade"
test "$(sha256sum "$first/kitpro-debian-upgrade" | awk '{print $1}')" = "$(sha256sum "$second/kitpro-debian-upgrade" | awk '{print $1}')"
test -f "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
test -f "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-acme"
test -f "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare"
test -f "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
test -f "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-helper"
cmp -s "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper" "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-helper"
test -f "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-acme"
cmp -s "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-acme" "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-acme"
test -f "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-cloudflare"
cmp -s "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare" "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-cloudflare"
cmp -s "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator" "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-ingress-validator"
grep -Fxq /etc/apparmor.d/usr.libexec.kitpro-helper "$unpack/control/conffiles"
grep -Fxq /etc/apparmor.d/usr.libexec.kitpro-acme "$unpack/control/conffiles"
grep -Fxq /etc/apparmor.d/usr.libexec.kitpro-cloudflare "$unpack/control/conffiles"
grep -Fxq /etc/apparmor.d/usr.libexec.kitpro-ingress-validator "$unpack/control/conffiles"
grep -q 'for profile_name in usr.libexec.kitpro-helper usr.libexec.kitpro-acme usr.libexec.kitpro-cloudflare usr.libexec.kitpro-ingress-validator' "$unpack/control/postinst"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-helper.socket"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-acme.service"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-cloudflare.service"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-ingress-validator.service"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.path"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.service"
test -f "$unpack/data/usr/lib/tmpfiles.d/kitpro.conf"
grep -q '^d /var/lib/kitpro-helper/application-backups 0700 root root -$' "$unpack/data/usr/lib/tmpfiles.d/kitpro.conf"
for temp_dir in client-body proxy fastcgi uwsgi scgi; do
    grep -q "^d /var/lib/kitpro-helper/ingress/temp-$temp_dir 0700 kitpro-ingress kitpro-ingress -$" "$unpack/data/usr/lib/tmpfiles.d/kitpro.conf"
done
for directive in \
    'client_body_temp_path temp-client-body;' \
    'proxy_temp_path temp-proxy;' \
    'fastcgi_temp_path temp-fastcgi;' \
    'uwsgi_temp_path temp-uwsgi;' \
    'scgi_temp_path temp-scgi;'
do
    grep -Fq "$directive" "$unpack/data/usr/share/kitpro-server/ingress-bootstrap.conf"
done
grep -q '^Version: 0.1.0~alpha3$' "$unpack/control/control"
grep -q '^Depends: adduser, apparmor, nginx, systemd$' "$unpack/control/control"
grep -q 'Environment=KITPRO_API_USER=kitpro-api' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^AppArmorProfile=/usr/libexec/kitpro-helper$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^AppArmorProfile=/usr/libexec/kitpro-acme$' "$unpack/data/usr/lib/systemd/system/kitpro-acme.service"
grep -q '^AppArmorProfile=/usr/libexec/kitpro-cloudflare$' "$unpack/data/usr/lib/systemd/system/kitpro-cloudflare.service"
grep -q '^AppArmorProfile=/usr/libexec/kitpro-ingress-validator$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-validator.service"
grep -q '^CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^AmbientCapabilities=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^RestrictAddressFamilies=AF_UNIX$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK$' "$unpack/data/usr/lib/systemd/system/kitpro-api.service"
if grep -Eq '^CapabilityBoundingSet=.*CAP_(SYS_ADMIN|DAC_OVERRIDE|MKNOD)' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"; then
    echo "helper gained an unapproved capability" >&2
    exit 1
fi
grep -q '^/usr/libexec/kitpro-helper flags=(attach_disconnected) {$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  deny network inet,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  deny network inet6,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -Fqx '/usr/libexec/kitpro-ingress-validator flags=(attach_disconnected) {' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  /usr/sbin/nginx ix,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  /usr/bin/nginx ix,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
test "$(grep -Ec ' (rix|ix),$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator")" -eq 3
grep -Fqx '  capability net_bind_service,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  network inet stream,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  network inet6 stream,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  /sys/fs/cgroup/system.slice/kitpro-ingress-validator.service/cpu.max r,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -q '^PrivateNetwork=yes$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-validator.service"
grep -q '^CapabilityBoundingSet=CAP_NET_BIND_SERVICE$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-validator.service"
grep -q '^AmbientCapabilities=CAP_NET_BIND_SERVICE$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-validator.service"
grep -q '^PathExists=/run/kitpro/ingress-reload.request$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.path"
grep -Fxq 'PIDFile=/run/kitpro-ingress/nginx.pid' "$unpack/data/usr/lib/systemd/system/kitpro-ingress.service"
grep -Fxq 'pid /run/kitpro-ingress/nginx.pid;' "$unpack/data/usr/share/kitpro-server/ingress-bootstrap.conf"
for lifecycle in \
    "$unpack/control/postinst" \
    "$server_dir/packaging/arch/kitpro-server.install" \
    "$server_dir/packaging/rpm/kitpro-server.spec.in"
do
    grep -Fq 'ingress/bootstrap/.nginx.conf.new' "$lifecycle"
    grep -Fq 'mv -fT /var/lib/kitpro-helper/ingress/bootstrap/.nginx.conf.new /var/lib/kitpro-helper/ingress/bootstrap/nginx.conf' "$lifecycle"
    if grep -Fq 'if [ ! -e /var/lib/kitpro-helper/ingress/bootstrap/nginx.conf' "$lifecycle" || \
       grep -Fq 'if [[ ! -e /var/lib/kitpro-helper/ingress/bootstrap/nginx.conf' "$lifecycle"; then
        echo "package lifecycle preserves a stale managed ingress bootstrap" >&2
        exit 1
    fi
done
grep -Fqx 'ExecStart=/usr/bin/systemctl reload kitpro-ingress.service' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.service"
grep -Fqx 'ExecStartPost=/usr/bin/mv -T -- /run/kitpro/ingress-reload.request /run/kitpro/ingress-reload.complete' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.service"
grep -q '^CapabilityBoundingSet=$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.service"
grep -q '^RestrictAddressFamilies=AF_UNIX$' "$unpack/data/usr/lib/systemd/system/kitpro-ingress-reload.service"
if grep -q '^  signal ' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"; then
    echo "helper gained process-signaling authority" >&2
    exit 1
fi
grep -q '^  network inet stream,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-acme"
grep -q '^  network inet6 stream,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-acme"
grep -q '^CapabilityBoundingSet=CAP_CHOWN$' "$unpack/data/usr/lib/systemd/system/kitpro-acme.service"
if grep -Eq '^CapabilityBoundingSet=.*CAP_(SYS_ADMIN|DAC_OVERRIDE|DAC_READ_SEARCH|MKNOD)' "$unpack/data/usr/lib/systemd/system/kitpro-acme.service"; then
    echo "ACME worker gained an unapproved capability" >&2
    exit 1
fi
grep -q '^CapabilityBoundingSet=CAP_SETUID CAP_SETGID$' "$unpack/data/usr/lib/systemd/system/kitpro-cloudflare.service"
if grep -q '^AmbientCapabilities=' "$unpack/data/usr/lib/systemd/system/kitpro-cloudflare.service"; then
    echo "Cloudflare tunnel runtime would inherit worker capabilities" >&2
    exit 1
fi
grep -Fq '/usr/bin/cloudflared ix,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare"
grep -Fq 'network inet dgram,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare"
grep -Fq 'network inet6 dgram,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare"
if grep -Rq -- '--token ' "$unpack/data/usr/lib/systemd/system/kitpro-cloudflare.service" "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare"; then
    echo "Cloudflare token is exposed in packaged process arguments" >&2
    exit 1
fi
grep -q '^  capability chown,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  capability dac_read_search,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -Fqx '  /etc/group r,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  /etc/nsswitch.conf r,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-ingress-validator"
grep -Fqx '  /run/kitpro/helper-state.lock rwk,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -Fqx '  /run/kitpro-ingress/nginx.pid r,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
for profile in usr.libexec.kitpro-acme usr.libexec.kitpro-cloudflare; do
    grep -Fqx '  /proc/*/cgroup r,' "$unpack/data/etc/apparmor.d/$profile"
    grep -Fqx '  /proc/*/mountinfo r,' "$unpack/data/etc/apparmor.d/$profile"
    grep -Fqx '  /proc/sys/net/core/somaxconn r,' "$unpack/data/etc/apparmor.d/$profile"
done
grep -Fqx '  /sys/fs/cgroup/system.slice/kitpro-acme.service/cpu.max r,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-acme"
grep -Fqx '  /sys/fs/cgroup/system.slice/kitpro-cloudflare.service/cpu.max r,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-cloudflare"
grep -Fqx '  link subset /var/lib/kitpro-helper/backups/.kitpro-upgrade-*.partial/pre-upgrade-helper-to-*.db -> /var/lib/kitpro-helper/backups/.kitpro-upgrade-*.partial/.pre-upgrade-helper-to-*.db.partial-*,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -Fqx '  link subset /var/lib/kitpro-helper/backups/pre-upgrade-helper-to-*.db -> /var/lib/kitpro-helper/backups/.pre-upgrade-helper-to-*.db.partial-*,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -Fqx '  link subset /var/lib/kitpro-helper/backups/.host-backup-*/helper.db -> /var/lib/kitpro-helper/backups/.host-backup-*/.helper.db.partial-*,' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
test "$(grep -Ec '^  link ' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper")" -eq 3
if grep -Eq '^  (/var/lib/kitpro-helper/\*\*|/var/lib/kitpro-helper/backups/\*\*) [^,]*l[^,]*,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"; then
    echo "helper gained broad hard-link authority" >&2
    exit 1
fi
grep -q 'apparmor_parser -r -W -T' "$unpack/control/postinst"
grep -q '/usr/libexec/kitpro-helper --verify-host-prerequisites' "$unpack/control/postinst"
grep -q 'restore_previous_services true' "$unpack/control/postinst"
grep -Fq "systemctl is-active --quiet nginx.service && die 'an active host nginx service conflicts with KITPro managed ingress'" "$unpack/control/preinst"
grep -q '^systemctl disable --now nginx.service$' "$unpack/control/postinst"
grep -q '^systemctl daemon-reload$' "$unpack/control/postinst"
if grep -R -E '__API_UID__|RestrictSUIDSGID|docker group|0\.0\.0\.0' "$unpack/data/usr/lib/systemd/system" "$unpack/data/etc/default"; then
    echo "unsafe or unresolved package configuration found" >&2
    exit 1
fi
if grep -R -E 'usermod.*docker|adduser.*docker' "$unpack/control"; then
    echo "package grants Docker group membership" >&2
    exit 1
fi
if grep -E 'rm .*/srv/kitpro|rm -rf /var/lib/kitpro' "$unpack/control/postrm"; then
    echo "package removal deletes persistent or trusted state" >&2
    exit 1
fi

for script in postinst prerm postrm; do
    sh -n "$unpack/control/$script"
done
bash -n "$unpack/control/preinst"
grep -q '^set -Eeuo pipefail$' "$unpack/control/preinst"
grep -Fq 'alpha.11 upgrades require the package-bound kitpro-debian-upgrade wrapper' "$unpack/control/preinst"
grep -Fq 'incoming_preflight' "$unpack/control/preinst"
grep -Fq 'package-transitions' "$unpack/control/preinst"
# shellcheck disable=SC2016
grep -Fq 'KITPRO_CONTROL_DB="$path" "$api_binary" --verify-database' "$unpack/control/preinst"
# shellcheck disable=SC2016
grep -Fq 'KITPRO_HELPER_DB="$path" "$helper_binary" --verify-database' "$unpack/control/preinst"
grep -Fq "package_source_commit='$(git -C "$server_dir" rev-parse HEAD)'" "$unpack/control/preinst"
if grep -Fq '/usr/libexec/kitpro-debian-upgrade --preflight-installed' "$unpack/control/preinst"; then
    echo 'incoming preinst delegates transition authority to the installed package' >&2
    exit 1
fi
if grep -Fq -- '--prepare-upgrade' "$unpack/control/preinst"; then
    echo 'incoming preinst delegates backup creation to installed runtime binaries' >&2
    exit 1
fi
if grep -Fq 'apparmor_parser' "$unpack/control/preinst"; then
    echo 'incoming preinst depends on changing the installed AppArmor profile' >&2
    exit 1
fi
grep -Fq 'abort-upgrade|abort-install|abort-remove|abort-deconfigure)' "$unpack/control/postinst"
grep -Fq 'Never migrate with code that was' "$unpack/control/postinst"
grep -Fq 'verify_upgrade_approval_metadata' "$unpack/control/postinst"
grep -Fq "package_source_commit='$(git -C "$server_dir" rev-parse HEAD)'" "$unpack/control/postinst"
if grep -Fq -- '--preflight-installed' "$unpack/data/usr/libexec/kitpro-debian-upgrade"; then
    echo 'installed upgrade helper retained obsolete preflight authority' >&2
    exit 1
fi
grep -Fq 'package SHA-256 does not match this upgrade wrapper' "$first/kitpro-debian-upgrade"
grep -Fq "embedded_package_version='0.1.0~alpha3'" "$first/kitpro-debian-upgrade"
grep -Fq "embedded_package_sha256='$(sha256sum "$one" | awk '{print $1}')'" "$first/kitpro-debian-upgrade"
grep -Fq 'kitpro-debian-upgrade wrapper' "$unpack/data/usr/share/doc/kitpro-server/README.Debian"

echo "package static tests: PASS"
