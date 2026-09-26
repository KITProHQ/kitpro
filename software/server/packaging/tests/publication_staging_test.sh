#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
packaging_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM

source_dir=$fixture/source
first=$fixture/public-first
second=$fixture/public-second
mkdir "$source_dir"

printf 'qualified deb bytes\n' > "$source_dir/demo_1.0~rc1_amd64.deb"
printf '#!/bin/sh\nexit 0\n' > "$source_dir/demo-upgrade"
chmod 0755 "$source_dir/demo-upgrade"
printf '{"format":"CycloneDX","version":"1.0~rc1"}\n' > "$source_dir/demo_1.0~rc1.cdx.json"
printf '{"version":"1.0~rc1","artifact_filename":"demo_1.0~rc1_amd64.deb"}\n' \
    > "$source_dir/demo_1.0~rc1_amd64.deb.build.json"

(cd "$source_dir" && sha256sum demo_1.0~rc1_amd64.deb > demo_1.0~rc1_amd64.deb.sha256)
(cd "$source_dir" && sha256sum demo-upgrade > demo-upgrade.sha256)
(cd "$source_dir" && sha256sum demo_1.0~rc1.cdx.json > demo_1.0~rc1.cdx.json.sha256)
(cd "$source_dir" && sha256sum \
    demo_1.0~rc1_amd64.deb \
    demo-upgrade \
    demo_1.0~rc1_amd64.deb.build.json \
    demo_1.0~rc1.cdx.json \
    > demo-SHA256SUMS)

deb_sha=$(sha256sum "$source_dir/demo_1.0~rc1_amd64.deb" | awk '{print $1}')
deb_size=$(stat -c '%s' "$source_dir/demo_1.0~rc1_amd64.deb")
wrapper_sha=$(sha256sum "$source_dir/demo-upgrade" | awk '{print $1}')
wrapper_size=$(stat -c '%s' "$source_dir/demo-upgrade")
sbom_sha=$(sha256sum "$source_dir/demo_1.0~rc1.cdx.json" | awk '{print $1}')
sbom_size=$(stat -c '%s' "$source_dir/demo_1.0~rc1.cdx.json")
metadata_sha=$(sha256sum "$source_dir/demo_1.0~rc1_amd64.deb.build.json" | awk '{print $1}')
metadata_size=$(stat -c '%s' "$source_dir/demo_1.0~rc1_amd64.deb.build.json")
checksums_sha=$(sha256sum "$source_dir/demo-SHA256SUMS" | awk '{print $1}')
checksums_size=$(stat -c '%s' "$source_dir/demo-SHA256SUMS")

jq -n \
    --arg deb_sha "$deb_sha" --argjson deb_size "$deb_size" \
    --arg wrapper_sha "$wrapper_sha" --argjson wrapper_size "$wrapper_size" \
    --arg sbom_sha "$sbom_sha" --argjson sbom_size "$sbom_size" \
    --arg metadata_sha "$metadata_sha" --argjson metadata_size "$metadata_size" \
    --arg checksums_sha "$checksums_sha" --argjson checksums_size "$checksums_size" \
    '{
        kitpro_version: "1.0-rc1",
        release_status: "candidate-not-published",
        source_commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        source_tree: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        release_url: null,
        artifacts: [
            {filename:"demo_1.0~rc1_amd64.deb", type:"deb", sha256:$deb_sha, size_bytes:$deb_size},
            {filename:"demo-upgrade", type:"upgrade-wrapper", sha256:$wrapper_sha, size_bytes:$wrapper_size}
        ],
        sbom: {filename:"demo_1.0~rc1.cdx.json", format:"CycloneDX JSON", sha256:$sbom_sha, size_bytes:$sbom_size},
        build_metadata: [
            {filename:"demo_1.0~rc1_amd64.deb.build.json", platform:"debian", sha256:$metadata_sha, size_bytes:$metadata_size}
        ],
        checksums: {filename:"demo-SHA256SUMS", sha256:$checksums_sha, size_bytes:$checksums_size},
        completion_rule: "replace before publication"
    }' > "$source_dir/demo-release-manifest.json"
(cd "$source_dir" && sha256sum demo-release-manifest.json > demo-release-manifest.json.sha256)

source_fingerprint=$(find "$source_dir" -maxdepth 1 -type f -print0 | sort -z | xargs -0 sha256sum)

stage() {
    "$packaging_dir/stage-publication.sh" \
        --source-dir "$source_dir" \
        --output-dir "$1" \
        --release-manifest demo-release-manifest.json \
        --tag v1.0-rc1 \
        --release-url https://example.invalid/releases/tag/v1.0-rc1 \
        --source-commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
        --source-tree bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
        >/dev/null
}

stage "$first"
stage "$second"
diff -qr "$first" "$second" >/dev/null
test "$source_fingerprint" = "$(find "$source_dir" -maxdepth 1 -type f -print0 | sort -z | xargs -0 sha256sum)"
test ! -e "$first/demo_1.0~rc1_amd64.deb"
test -f "$first/demo_1.0.rc1_amd64.deb"
test -f "$first/demo_1.0.rc1.cdx.json"
cmp -s "$source_dir/demo_1.0~rc1_amd64.deb" "$first/demo_1.0.rc1_amd64.deb"
cmp -s "$source_dir/demo_1.0~rc1.cdx.json" "$first/demo_1.0.rc1.cdx.json"
test "$(jq -r '.version' "$first/demo_1.0.rc1_amd64.deb.build.json")" = '1.0~rc1'
test "$(jq -r '.artifact_filename' "$first/demo_1.0.rc1_amd64.deb.build.json")" = 'demo_1.0.rc1_amd64.deb'
(cd "$first" && sha256sum -c demo-SHA256SUMS >/dev/null)
for checksum_file in "$first"/*.sha256; do
    (cd "$first" && sha256sum -c "${checksum_file##*/}" >/dev/null)
done
if find "$first" -maxdepth 1 -type f -printf '%f\n' | grep -Fq '~'; then
    echo 'staged filename retained a GitHub-unsafe character' >&2
    exit 1
fi
test "$(jq -r '.release_status' "$first/demo-release-manifest.json")" = published-prerelease
test "$(jq -r '.tag' "$first/demo-release-manifest.json")" = v1.0-rc1
test "$(jq -r '.published' "$first/demo-release-manifest.json")" = true
test "$(jq -r '.prerelease' "$first/demo-release-manifest.json")" = true
test "$(jq -r '.published_asset_count' "$first/demo-release-manifest.json")" -eq "$(find "$first" -maxdepth 1 -type f | wc -l)"
test "$(jq -r '.artifacts[0].sha256' "$first/demo-release-manifest.json")" = "$deb_sha"
test "$(jq -r '.sbom.sha256' "$first/demo-release-manifest.json")" = "$sbom_sha"
test "$(jq -r '.checksums.sha256' "$first/demo-release-manifest.json")" = "$(sha256sum "$first/demo-SHA256SUMS" | awk '{print $1}')"
test "$(jq -r 'has("completion_rule")' "$first/demo-release-manifest.json")" = false

if stage "$first" 2>/dev/null; then
    echo 'publication staging overwrote an existing output directory' >&2
    exit 1
fi

collision_source=$fixture/collision-source
mkdir "$collision_source"
cp -a "$source_dir/." "$collision_source/"
printf 'collision\n' > "$collision_source/demo_1.0.rc1_amd64.deb"
if "$packaging_dir/stage-publication.sh" \
    --source-dir "$collision_source" \
    --output-dir "$fixture/collision-output" \
    --release-manifest demo-release-manifest.json \
    --tag v1.0-rc1 \
    --release-url https://example.invalid/releases/tag/v1.0-rc1 \
    --source-commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
    --source-tree bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
    >/dev/null 2>&1; then
    echo 'publication staging accepted a normalized filename collision' >&2
    exit 1
fi

unsafe_source=$fixture/unsafe-source
mkdir "$unsafe_source"
cp -a "$source_dir/." "$unsafe_source/"
printf 'unsafe\n' > "$unsafe_source/bad name.txt"
if "$packaging_dir/stage-publication.sh" \
    --source-dir "$unsafe_source" \
    --output-dir "$fixture/unsafe-output" \
    --release-manifest demo-release-manifest.json \
    --tag v1.0-rc1 \
    --release-url https://example.invalid/releases/tag/v1.0-rc1 \
    --source-commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
    --source-tree bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
    >/dev/null 2>&1; then
    echo 'publication staging accepted a GitHub-unsafe filename' >&2
    exit 1
fi

echo 'publication staging tests: PASS'
