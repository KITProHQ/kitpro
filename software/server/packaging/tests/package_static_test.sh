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
test -f "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
test -f "$unpack/data/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-helper"
grep -Fxq /etc/apparmor.d/usr.libexec.kitpro-helper "$unpack/control/conffiles"
grep -q 'profile_source=/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-helper' "$unpack/control/postinst"
test -f "$unpack/data/usr/lib/systemd/system/kitpro-helper.socket"
test -f "$unpack/data/usr/lib/tmpfiles.d/kitpro.conf"
grep -q '^d /var/lib/kitpro-helper/application-backups 0700 root root -$' "$unpack/data/usr/lib/tmpfiles.d/kitpro.conf"
grep -q '^Version: 0.1.0~alpha3$' "$unpack/control/control"
grep -q '^Depends: adduser, apparmor, systemd$' "$unpack/control/control"
grep -q 'Environment=KITPRO_API_USER=kitpro-api' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^AppArmorProfile=/usr/libexec/kitpro-helper$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
grep -q '^AmbientCapabilities=CAP_CHOWN CAP_DAC_READ_SEARCH$' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"
if grep -Eq '^CapabilityBoundingSet=.*CAP_(SYS_ADMIN|DAC_OVERRIDE|MKNOD)' "$unpack/data/usr/lib/systemd/system/kitpro-helper.service"; then
    echo "helper gained an unapproved capability" >&2
    exit 1
fi
grep -q '^/usr/libexec/kitpro-helper flags=(attach_disconnected) {$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  deny network inet,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  deny network inet6,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  capability chown,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q '^  capability dac_read_search,$' "$unpack/data/etc/apparmor.d/usr.libexec.kitpro-helper"
grep -q 'apparmor_parser -r -W -T' "$unpack/control/postinst"
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

for script in preinst postinst prerm postrm; do
    sh -n "$unpack/control/$script"
done

echo "package static tests: PASS"
