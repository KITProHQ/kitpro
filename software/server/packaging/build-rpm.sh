#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/.." && pwd)
repo_dir=$(git -C "$server_dir" rev-parse --show-toplevel)
version=${1:-0.1.0~alpha1}
output_dir=${2:-$server_dir/dist}

[[ "$version" =~ ^[0-9][0-9A-Za-z.~_+]*$ ]] || { printf 'invalid RPM version: %s\n' "$version" >&2; exit 2; }
command -v rpmbuild >/dev/null 2>&1 || { printf 'rpmbuild is required; build on Rocky Linux 10\n' >&2; exit 1; }

source_commit=${KITPRO_SOURCE_COMMIT:-$(git -C "$repo_dir" rev-parse HEAD)}
source_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo_dir" show -s --format=%ct "$source_commit")}
[[ "$source_commit" =~ ^[0-9a-f]{40,64}$ ]] || { printf 'invalid source commit: %s\n' "$source_commit" >&2; exit 2; }
[[ "$source_epoch" =~ ^[1-9][0-9]*$ ]] || { printf 'invalid source epoch: %s\n' "$source_epoch" >&2; exit 2; }
build_date=$(date -u -d "@$source_epoch" +%Y-%m-%dT%H:%M:%SZ)

work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT
topdir=$work_dir/rpmbuild
stage=$work_dir/stage
source_root=$stage/kitpro-server-$version
mkdir -p "$topdir/BUILD" "$topdir/BUILDROOT" "$topdir/RPMS" "$topdir/SOURCES" "$topdir/SPECS" "$topdir/SRPMS" "$source_root"

git -C "$repo_dir" ls-files -c -o --exclude-standard -z -- LICENSE docs software/server | while IFS= read -r -d '' path; do
    case "$path" in
        software/server/dist/*|software/server/.arch-build/*) continue ;;
    esac
    install -D -m "$(stat -c '%a' "$repo_dir/$path")" "$repo_dir/$path" "$source_root/$path"
done
find "$source_root" -print0 | xargs -0 touch -h -d "@$source_epoch"
tar --sort=name --mtime="@$source_epoch" --owner=0 --group=0 --numeric-owner -czf "$topdir/SOURCES/kitpro-server-$version.tar.gz" -C "$stage" "kitpro-server-$version"

sed -e "s|@VERSION@|$version|g" -e "s|@SOURCE_COMMIT@|$source_commit|g" -e "s|@BUILD_DATE@|$build_date|g" \
    "$script_dir/rpm/kitpro-server.spec.in" > "$topdir/SPECS/kitpro-server.spec"

export SOURCE_DATE_EPOCH=$source_epoch
rpmbuild -ba \
    --define "_topdir $topdir" \
    --define "source_date_epoch_from_changelog 0" \
    --define "use_source_date_epoch_as_buildtime 1" \
    "$topdir/SPECS/kitpro-server.spec"

mkdir -p "$output_dir"
find "$topdir/RPMS" "$topdir/SRPMS" -type f -name '*.rpm' -exec install -m 0644 -t "$output_dir" {} +
(
    cd "$output_dir"
    for package in *.rpm; do
        sha256sum "$package" > "$package.sha256"
    done
)
printf 'RPM artifacts written to %s\n' "$(cd "$output_dir" && pwd)"
