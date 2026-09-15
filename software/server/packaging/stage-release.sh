#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
server_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
repo_dir=$(git -C "$server_dir" rev-parse --show-toplevel)
dist_dir=${1:-$server_dir/dist}
release_dir=${2:?usage: stage-release.sh [dist-dir] release-dir}
public_version=$(tr -d '\n' < "$repo_dir/VERSION")
deb_version=$(printf '%s\n' "$public_version" | sed -E 's/-alpha\.([0-9]+)$/~alpha\1/')
arch_version=$(printf '%s\n' "$public_version" | sed -E 's/-alpha\.([0-9]+)$/_alpha\1/')
source_commit=$(git -C "$repo_dir" rev-parse HEAD)

deb_input_name="kitpro-server_${deb_version}_amd64.deb"
deb_name="kitpro-server_${public_version}_amd64.deb"
arch_name="kitpro-server-${arch_version}-1-x86_64.pkg.tar.zst"
sbom_input_name="kitpro-server_${deb_version}_amd64.cdx.json"
sbom_name="kitpro-server_${public_version}_amd64.cdx.json"
release_prefix="kitpro-server_${public_version}"
build_name="${release_prefix}.build.json"
manifest_name="${release_prefix}.release.json"
checksums_name="${release_prefix}_SHA256SUMS"

for path in "$dist_dir/$deb_input_name" "$dist_dir/$deb_input_name.build.json" \
    "$dist_dir/$arch_name" "$dist_dir/$arch_name.build.json" \
    "$dist_dir/$sbom_input_name"; do
    test -f "$path" || { echo "missing release input: $path" >&2; exit 1; }
done

for metadata in "$dist_dir/$deb_input_name.build.json" "$dist_dir/$arch_name.build.json"; do
    test "$(jq -r .source_commit "$metadata")" = "$source_commit" || {
        echo "build metadata source commit does not match HEAD: $metadata" >&2
        exit 1
    }
    test "$(jq -r .version "$metadata")" = "$public_version" || {
        echo "build metadata public version does not match VERSION: $metadata" >&2
        exit 1
    }
    test "$(jq -r .source_tree_dirty "$metadata")" = false || {
        echo "release input was built from a dirty source tree: $metadata" >&2
        exit 1
    }
done

sbom_commit=$(jq -r '.metadata.properties[] | select(.name == "kitpro:source_commit") | .value' "$dist_dir/$sbom_input_name")
test "$sbom_commit" = "$source_commit" || { echo "SBOM source commit does not match HEAD" >&2; exit 1; }
test "$(jq -r .metadata.component.version "$dist_dir/$sbom_input_name")" = "$public_version" || {
    echo "SBOM public version does not match VERSION" >&2
    exit 1
}

install -d "$release_dir"
release_dir=$(CDPATH='' cd -- "$release_dir" && pwd)
install -m 0644 "$dist_dir/$deb_input_name" "$release_dir/$deb_name"
install -m 0644 "$dist_dir/$arch_name" "$release_dir/$arch_name"
install -m 0644 "$dist_dir/$sbom_input_name" "$release_dir/$sbom_name"

jq -n --arg version "$public_version" --arg source_commit "$source_commit" \
    --slurpfile deb "$dist_dir/$deb_input_name.build.json" \
    --slurpfile arch "$dist_dir/$arch_name.build.json" \
    '{kitpro_version:$version,source_commit:$source_commit,packages:{debian:$deb[0],arch:$arch[0]}}' \
    > "$release_dir/$build_name"

deb_sha=$(sha256sum "$release_dir/$deb_name" | awk '{print $1}')
arch_sha=$(sha256sum "$release_dir/$arch_name" | awk '{print $1}')
sbom_sha=$(sha256sum "$release_dir/$sbom_name" | awk '{print $1}')
build_sha=$(sha256sum "$release_dir/$build_name" | awk '{print $1}')
deb_size=$(stat -c %s "$release_dir/$deb_name")
arch_size=$(stat -c %s "$release_dir/$arch_name")
sbom_size=$(stat -c %s "$release_dir/$sbom_name")

jq -n \
    --arg version "$public_version" --arg source_commit "$source_commit" \
    --arg deb_name "$deb_name" --arg deb_sha "$deb_sha" --argjson deb_size "$deb_size" \
    --arg arch_name "$arch_name" --arg arch_sha "$arch_sha" --argjson arch_size "$arch_size" \
    --arg sbom_name "$sbom_name" --arg sbom_sha "$sbom_sha" --argjson sbom_size "$sbom_size" \
    --arg build_name "$build_name" --arg build_sha "$build_sha" \
    '{kitpro_version:$version,source_commit:$source_commit,supported_platforms:["debian-13-amd64","ubuntu-26.04-amd64","arch-x86_64-linux-lts"],schema_version:4,catalog_schema_version:2,artifacts:[{path:$deb_name,package_family:"deb",architecture:"amd64",size:$deb_size,sha256:$deb_sha},{path:$arch_name,package_family:"pacman",architecture:"x86_64",size:$arch_size,sha256:$arch_sha}],sbom:{path:$sbom_name,size:$sbom_size,sha256:$sbom_sha},build_metadata:{path:$build_name,sha256:$build_sha},build_policy:{go:"go1.27.x",cgo:false,flags:["-trimpath","-buildvcs=false","-s","-w"],source_date_epoch:"source commit timestamp"}}' \
    > "$release_dir/$manifest_name"

(cd "$release_dir" && sha256sum "$deb_name" "$arch_name" "$sbom_name" "$build_name" "$manifest_name" > "$checksums_name")
printf '%s\n' "$release_dir"
