#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
server_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
repo_dir=$(git -C "$server_dir" rev-parse --show-toplevel)
version=${1:-0.1.0~alpha1}
output_dir=${2:-$server_dir/dist}
architecture=amd64

if command -v dpkg >/dev/null 2>&1; then
    if ! dpkg --validate-version "$version" >/dev/null 2>&1; then
        echo "invalid Debian version: $version" >&2
        exit 2
    fi
else
    case $version in
        *[!0-9A-Za-z.+:~_-]*|'')
            echo "invalid Debian version: $version" >&2
            exit 2
            ;;
    esac
fi

source_commit=$(git -C "$repo_dir" rev-parse HEAD)
source_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo_dir" show -s --format=%ct "$source_commit")}
export SOURCE_DATE_EPOCH="$source_epoch"
build_date=$(date -u -d "@$source_epoch" +%Y-%m-%dT%H:%M:%SZ)
changelog_date=$(date -u -d "@$source_epoch" '+%a, %d %b %Y %H:%M:%S +0000')
if git -C "$repo_dir" diff --quiet && git -C "$repo_dir" diff --cached --quiet && [ -z "$(git -C "$repo_dir" ls-files --others --exclude-standard)" ]; then
    source_tree_dirty=false
else
    source_tree_dirty=true
fi
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
root="$work_dir/root"
control="$root/DEBIAN"

install -d "$control" "$root/usr/bin" "$root/usr/libexec" \
    "$root/usr/lib/systemd/system" "$root/usr/lib/tmpfiles.d" \
    "$root/etc/apparmor.d" "$root/etc/default" \
    "$root/usr/share/kitpro-server/apparmor" \
    "$root/usr/share/doc/kitpro-server" "$root/usr/share/man/man8" \
    "$root/usr/share/lintian/overrides"

ldflags="-s -w -X github.com/kitpro/kitpro/software/server/internal/buildinfo.Version=$version -X github.com/kitpro/kitpro/software/server/internal/buildinfo.SourceCommit=$source_commit -X github.com/kitpro/kitpro/software/server/internal/buildinfo.BuildDate=$build_date"
(cd "$server_dir" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$root/usr/bin/kitpro-api" ./cmd/kitpro-api)
(cd "$server_dir" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$root/usr/libexec/kitpro-helper" ./cmd/kitpro-helper)

install -m 0644 "$script_dir/systemd/kitpro-api.service" "$root/usr/lib/systemd/system/"
install -m 0644 "$script_dir/systemd/kitpro-helper.service" "$root/usr/lib/systemd/system/"
install -m 0644 "$script_dir/systemd/kitpro-helper.socket" "$root/usr/lib/systemd/system/"
install -m 0644 "$script_dir/tmpfiles/kitpro.conf" "$root/usr/lib/tmpfiles.d/"
install -m 0644 "$script_dir/apparmor/kitpro-helper" "$root/etc/apparmor.d/usr.libexec.kitpro-helper"
install -m 0644 "$script_dir/apparmor/kitpro-helper" "$root/usr/share/kitpro-server/apparmor/usr.libexec.kitpro-helper"
install -m 0644 "$script_dir/debian/kitpro-server.default" "$root/etc/default/kitpro-server"
install -m 0644 "$script_dir/debian/README.Debian" "$root/usr/share/doc/kitpro-server/"
install -m 0644 "$script_dir/debian/copyright" "$root/usr/share/doc/kitpro-server/"
install -m 0644 "$script_dir/debian/lintian-overrides" "$root/usr/share/lintian/overrides/kitpro-server"
install -m 0644 "$server_dir/README.md" "$root/usr/share/doc/kitpro-server/README"
sed -e "s/@VERSION@/$version/g" -e "s/@DATE@/$changelog_date/g" \
    "$script_dir/debian/changelog.in" | gzip -n -9 > "$root/usr/share/doc/kitpro-server/changelog.gz"
gzip -n -9 -c "$script_dir/debian/kitpro-api.8" > "$root/usr/share/man/man8/kitpro-api.8.gz"
gzip -n -9 -c "$script_dir/debian/kitpro-helper.8" > "$root/usr/share/man/man8/kitpro-helper.8.gz"
install -m 0644 "$script_dir/debian/conffiles" "$control/conffiles"

installed_size=$(du -sk "$root" | awk '{print $1}')
sed -e "s/@VERSION@/$version/g" -e "s/@INSTALLED_SIZE@/$installed_size/g" \
    "$script_dir/debian/control.in" > "$control/control"
for name in preinst postinst prerm postrm; do
    sed "s/@VERSION@/$version/g" "$script_dir/debian/$name.in" > "$control/$name"
    chmod 0755 "$control/$name"
done

find "$root" -type f ! -path "$control/*" -print0 | LC_ALL=C sort -z | \
    xargs -0 md5sum | sed "s|  $root/|  |" > "$control/md5sums"

install -d "$output_dir"
output_dir=$(CDPATH='' cd -- "$output_dir" && pwd)
package="$output_dir/kitpro-server_${version}_${architecture}.deb"
rm -f "$package"

find "$root" -print0 | xargs -0 touch -h -d "@$source_epoch"
if command -v dpkg-deb >/dev/null 2>&1; then
    dpkg-deb --root-owner-group --build "$root" "$package" >/dev/null
else
    archive="$work_dir/archive"
    install -d "$archive"
    printf '2.0\n' > "$archive/debian-binary"
    (cd "$control" && tar --sort=name --mtime="@$source_epoch" --owner=0 --group=0 --numeric-owner -cJf "$archive/control.tar.xz" .)
    rm -rf "$root/DEBIAN"
    (cd "$root" && tar --sort=name --mtime="@$source_epoch" --owner=0 --group=0 --numeric-owner -cJf "$archive/data.tar.xz" .)
    touch -d "@$source_epoch" "$archive/debian-binary" "$archive/control.tar.xz" "$archive/data.tar.xz"
    (cd "$archive" && ar rcsD "$package" debian-binary control.tar.xz data.tar.xz)
fi

(cd "$output_dir" && sha256sum "$(basename "$package")" > "$(basename "$package").sha256")
cat > "$package.build.json" <<EOF
{
  "package": "kitpro-server",
  "version": "$version",
  "architecture": "$architecture",
  "source_commit": "$source_commit",
  "source_tree_dirty": $source_tree_dirty,
  "source_date_epoch": $source_epoch,
  "build_date": "$build_date",
  "go_version": "$(go env GOVERSION)",
  "cgo_enabled": false,
  "build_flags": ["-trimpath", "-buildvcs=false", "-ldflags=-s -w"]
}
EOF

printf '%s\n' "$package"
