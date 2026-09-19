#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/.." && pwd)
if repo_dir=$(git -C "$server_dir" rev-parse --show-toplevel 2>/dev/null); then
    have_git=true
else
    repo_dir=$(cd -- "$server_dir/../.." && pwd)
    have_git=false
fi
pkgver=${1:-0.1.0_alpha1}
output_dir=${2:-$server_dir/dist}
pkgrel=${3:-1}
arch_dir="$script_dir/arch"

[[ "$pkgver" =~ ^[0-9][0-9A-Za-z._+]*$ ]] || { printf 'invalid Arch pkgver: %s\n' "$pkgver" >&2; exit 2; }
[[ "$pkgrel" =~ ^[1-9][0-9]*$ ]] || { printf 'invalid Arch pkgrel: %s\n' "$pkgrel" >&2; exit 2; }
command -v makepkg >/dev/null 2>&1 || { printf 'makepkg is required\n' >&2; exit 1; }

if [[ "$have_git" == true ]]; then
    source_commit=${KITPRO_SOURCE_COMMIT:-$(git -C "$repo_dir" rev-parse HEAD)}
    source_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo_dir" show -s --format=%ct "$source_commit")}
else
    source_commit=${KITPRO_SOURCE_COMMIT:?KITPRO_SOURCE_COMMIT is required outside a Git checkout}
    source_epoch=${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required outside a Git checkout}
fi
[[ "$source_commit" =~ ^[0-9a-f]{40,64}$ ]] || { printf 'invalid source commit: %s\n' "$source_commit" >&2; exit 2; }
[[ "$source_epoch" =~ ^[1-9][0-9]*$ ]] || { printf 'invalid source epoch: %s\n' "$source_epoch" >&2; exit 2; }
stage_dir=$(mktemp -d)
build_dir="$server_dir/.arch-build"
case "$build_dir" in
    "$server_dir"/.arch-build) ;;
    *) printf 'unsafe Arch build directory: %s\n' "$build_dir" >&2; exit 1 ;;
esac
rm -rf -- "$build_dir"
mkdir -p "$build_dir"
trap 'rm -rf -- "$stage_dir" "$build_dir"' EXIT
source_root="$stage_dir/kitpro-server-$pkgver"
mkdir -p "$source_root"

source_tree_dirty=true
if [[ "$have_git" == true ]] && git -C "$repo_dir" diff --quiet && git -C "$repo_dir" diff --cached --quiet && [[ -z $(git -C "$repo_dir" ls-files --others --exclude-standard) ]]; then
    source_tree_dirty=false
elif [[ "$have_git" == false ]]; then
    source_tree_dirty=true
fi

if [[ "$have_git" == true ]]; then
    git -C "$repo_dir" ls-files -c -o --exclude-standard -z -- docs/install-arch-package.md software/server
else
    find "$repo_dir/docs/install-arch-package.md" "$repo_dir/software/server" -type f -print0
fi | while IFS= read -r -d '' path; do
    path=${path#"$repo_dir/"}
    case "$path" in
        software/server/dist/*|software/server/.arch-build/*|software/server/packaging/arch/*.tar.*|software/server/packaging/arch/pkg/*|software/server/packaging/arch/src/*) continue ;;
    esac
    [[ -e "$repo_dir/$path" ]] || continue
    install -D -m "$(stat -c '%a' "$repo_dir/$path")" "$repo_dir/$path" "$source_root/$path"
done

find "$source_root" -print0 | xargs -0 touch -h -d "@$source_epoch"
tarball="$build_dir/kitpro-server-$pkgver.tar.gz"
tar --sort=name --mtime="@$source_epoch" --owner=0 --group=0 --numeric-owner -czf "$tarball" -C "$stage_dir" "kitpro-server-$pkgver"

pkgbuild="$build_dir/PKGBUILD"
sed -e "s/^pkgver=.*/pkgver=$pkgver/" -e "s/^pkgrel=.*/pkgrel=$pkgrel/" -e "s/'SKIP'/'$(sha256sum "$tarball" | awk '{print $1}')'/" "$arch_dir/PKGBUILD" > "$pkgbuild"
cp "$arch_dir/kitpro-server.install" "$build_dir/"

mkdir -p "$output_dir"
(
    cd "$build_dir"
    export SOURCE_DATE_EPOCH="$source_epoch" KITPRO_SOURCE_COMMIT="$source_commit" BUILDDIR="$build_dir/work"
    makepkg --clean --force --nodeps --noconfirm
)
package=$(find "$build_dir" -maxdepth 1 -name "kitpro-server-${pkgver}-${pkgrel}-x86_64.pkg.tar.zst" -print -quit)
[[ -n "$package" ]] || { printf 'Arch package was not produced\n' >&2; exit 1; }
install -m 0644 "$package" "$output_dir/$(basename "$package")"
(cd "$output_dir" && sha256sum "$(basename "$package")" > "$(basename "$package").sha256")
upgrade_wrapper="$output_dir/kitpro-server-${pkgver}-${pkgrel}-upgrade.sh"
package_sha256=$(sha256sum "$package" | awk '{print $1}')
sed "s/@KITPRO_PACKAGE_SHA256@/$package_sha256/" "$arch_dir/kitpro-arch-upgrade" > "$upgrade_wrapper"
chmod 0755 "$upgrade_wrapper"
(cd "$output_dir" && sha256sum "$(basename "$upgrade_wrapper")" > "$(basename "$upgrade_wrapper").sha256")
cat > "$output_dir/$(basename "$package").build.json" <<EOF
{
  "package": "kitpro-server",
  "version": "$pkgver-$pkgrel",
  "architecture": "x86_64",
  "source_commit": "$source_commit",
  "source_tree_dirty": $source_tree_dirty,
  "source_date_epoch": $source_epoch,
  "go_version": "$(go env GOVERSION)",
  "builder": "$(makepkg --version | awk 'NF {print; exit}')",
  "cgo_enabled": false,
  "build_flags": ["-trimpath", "-buildvcs=false", "-ldflags=-s -w"]
}
EOF
printf '%s\n' "$output_dir/$(basename "$package")"
