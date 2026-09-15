#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/../.." && pwd)
arch_dir="$server_dir/packaging/arch"

bash -n "$server_dir/packaging/build-arch-package.sh" "$arch_dir/kitpro-server.install"
shellcheck "$server_dir/packaging/build-arch-package.sh" "$arch_dir/kitpro-server.install"
grep -Fxq "depends=('apparmor' 'docker' 'systemd')" "$arch_dir/PKGBUILD"
grep -Fxq "license=('Apache-2.0')" "$arch_dir/PKGBUILD"
grep -Fxq "options=('!debug' '!strip')" "$arch_dir/PKGBUILD"
grep -Fq 'local public_version=${pkgver/_alpha/-alpha.}' "$arch_dir/PKGBUILD"
grep -q 'EnvironmentFile=-/etc/conf.d/kitpro-server' "$server_dir/packaging/systemd/kitpro-api.service"
grep -q 'EnvironmentFile=-/etc/conf.d/kitpro-server' "$server_dir/packaging/systemd/kitpro-helper.service"
grep -q 'aa-enabled' "$arch_dir/kitpro-server.install"
grep -q 'apparmor_parser -r -W -T' "$arch_dir/kitpro-server.install"
grep -q 'prepare-upgrade' "$arch_dir/kitpro-server.install"
grep -q 'usr/share/kitpro-server/kitpro-server.conf' "$arch_dir/PKGBUILD"
grep -q 'usr/share/licenses/kitpro-server/COPYING' "$arch_dir/PKGBUILD"
grep -q 'usr/share/man/man8/kitpro-api.8' "$arch_dir/PKGBUILD"
grep -q 'usr/share/man/man8/kitpro-helper.8' "$arch_dir/PKGBUILD"
grep -q 'invalid source commit' "$server_dir/packaging/build-arch-package.sh"
grep -q 'invalid source epoch' "$server_dir/packaging/build-arch-package.sh"
grep -Fq 'build_dir="/tmp/kitpro-arch-package-build-$(id -u)"' "$server_dir/packaging/build-arch-package.sh"
grep -Fq 'flock 9' "$server_dir/packaging/build-arch-package.sh"
grep -q 'install -Dm0644 /usr/share/kitpro-server/kitpro-server.conf /etc/conf.d/kitpro-server' "$arch_dir/kitpro-server.install"
if grep -E 'rm .*/srv/kitpro|rm -rf /var/lib/kitpro' "$arch_dir/kitpro-server.install"; then
    printf 'Arch removal deletes persistent or trusted state\n' >&2
    exit 1
fi
if grep -R -E 'usermod.*docker|gpasswd.*docker' "$arch_dir"; then
    printf 'Arch package grants Docker group membership\n' >&2
    exit 1
fi

printf 'Arch package static tests: PASS\n'
