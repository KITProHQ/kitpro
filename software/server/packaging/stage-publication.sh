#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
    cat <<'EOF'
Usage: stage-publication.sh \
  --source-dir DIR \
  --output-dir DIR \
  --release-manifest FILENAME \
  --tag TAG \
  --release-url URL \
  --source-commit COMMIT \
  --source-tree TREE \
  [--release-status STATUS] \
  [--prerelease true|false] \
  [--published true|false]

Create a new publication directory without rebuilding release artifacts.
GitHub-unsafe '~' characters are replaced with '.' before filename-dependent
checksums and the release manifest are generated.
EOF
}

source_dir=
output_dir=
manifest_name=
release_tag=
release_url=
source_commit=
source_tree=
release_status='published-prerelease'
prerelease=true
published=true

while (($#)); do
    case "$1" in
        --source-dir) source_dir=${2-}; shift 2 ;;
        --output-dir) output_dir=${2-}; shift 2 ;;
        --release-manifest) manifest_name=${2-}; shift 2 ;;
        --tag) release_tag=${2-}; shift 2 ;;
        --release-url) release_url=${2-}; shift 2 ;;
        --source-commit) source_commit=${2-}; shift 2 ;;
        --source-tree) source_tree=${2-}; shift 2 ;;
        --release-status) release_status=${2-}; shift 2 ;;
        --prerelease) prerelease=${2-}; shift 2 ;;
        --published) published=${2-}; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) printf 'unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
done

for command_name in awk chmod cmp cp find grep install jq mv rm sha256sum sort stat; do
    command -v "$command_name" >/dev/null 2>&1 || {
        printf 'required command not found: %s\n' "$command_name" >&2
        exit 2
    }
done

for required_value in source_dir output_dir manifest_name release_tag release_url source_commit source_tree; do
    [[ -n ${!required_value} ]] || {
        printf 'missing required option: %s\n' "${required_value//_/-}" >&2
        usage >&2
        exit 2
    }
done

[[ $prerelease == true || $prerelease == false ]] || {
    printf '%s\n' '--prerelease must be true or false' >&2
    exit 2
}
[[ $published == true || $published == false ]] || {
    printf '%s\n' '--published must be true or false' >&2
    exit 2
}
[[ $source_commit =~ ^[0-9a-f]{40,64}$ ]] || {
    printf 'invalid source commit: %s\n' "$source_commit" >&2
    exit 2
}
[[ $source_tree =~ ^[0-9a-f]{40,64}$ ]] || {
    printf 'invalid source tree: %s\n' "$source_tree" >&2
    exit 2
}
[[ $manifest_name != */* && $manifest_name == *.json ]] || {
    printf '%s\n' '--release-manifest must be a JSON basename inside --source-dir' >&2
    exit 2
}

source_dir=$(CDPATH='' cd -- "$source_dir" && pwd)
manifest_source=$source_dir/$manifest_name
[[ -f $manifest_source && ! -L $manifest_source ]] || {
    printf 'release manifest is not a regular file: %s\n' "$manifest_source" >&2
    exit 2
}
jq -e . "$manifest_source" >/dev/null
[[ $(jq -r '.source_commit // empty' "$manifest_source") == "$source_commit" ]] || {
    printf '%s\n' 'release manifest source commit does not match --source-commit' >&2
    exit 2
}
[[ $(jq -r '.source_tree // empty' "$manifest_source") == "$source_tree" ]] || {
    printf '%s\n' 'release manifest source tree does not match --source-tree' >&2
    exit 2
}

output_parent=$(dirname -- "$output_dir")
output_base=$(basename -- "$output_dir")
[[ $output_base != . && $output_base != .. && -n $output_base ]] || {
    printf 'unsafe output directory: %s\n' "$output_dir" >&2
    exit 2
}
output_parent=$(CDPATH='' cd -- "$output_parent" && pwd)
output_dir=$output_parent/$output_base
[[ $output_dir != "$source_dir" && ! -e $output_dir ]] || {
    printf 'output directory already exists or aliases the source: %s\n' "$output_dir" >&2
    exit 2
}

work_dir=$output_dir.partial.$$
[[ ! -e $work_dir ]] || {
    printf 'temporary output already exists: %s\n' "$work_dir" >&2
    exit 2
}
install -d -m 0700 "$work_dir"
cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

filename_map='{}'
source_count=0
while IFS= read -r -d '' source_file; do
    source_name=${source_file##*/}
    [[ $source_name != *$'\n'* && $source_name != *$'\t'* ]] || {
        printf 'unsupported control character in filename: %q\n' "$source_name" >&2
        exit 2
    }
    public_name=${source_name//\~/.}
    [[ $public_name =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
        printf 'filename is not GitHub-safe after normalization: %s\n' "$public_name" >&2
        exit 2
    }
    [[ ! -e $work_dir/$public_name ]] || {
        printf 'GitHub-safe filename collision: %s\n' "$public_name" >&2
        exit 2
    }
    cp --preserve=mode,timestamps -- "$source_file" "$work_dir/$public_name"
    filename_map=$(jq -cn \
        --argjson current "$filename_map" \
        --arg source "$source_name" \
        --arg public "$public_name" \
        '$current + {($source): $public}')
    ((source_count += 1))
done < <(find "$source_dir" -maxdepth 1 -type f -print0 | sort -z)
((source_count > 0)) || {
    printf 'source directory contains no regular files: %s\n' "$source_dir" >&2
    exit 2
}

public_manifest_name=$(jq -rn --argjson names "$filename_map" --arg name "$manifest_name" '$names[$name] // empty')
[[ -n $public_manifest_name && -f $work_dir/$public_manifest_name ]] || {
    printf '%s\n' 'release manifest was not staged' >&2
    exit 2
}

# Rewrite exact embedded artifact basenames in declared build metadata only.
# Internal package versions such as 0.1.0~alpha13 are intentionally untouched.
while IFS= read -r metadata_name; do
    [[ -n $metadata_name ]] || continue
    public_metadata_name=$(jq -rn --argjson names "$filename_map" --arg name "$metadata_name" '$names[$name] // empty')
    [[ -n $public_metadata_name && -f $work_dir/$public_metadata_name ]] || {
        printf 'declared build metadata is missing: %s\n' "$metadata_name" >&2
        exit 2
    }
    if jq -e --argjson names "$filename_map" \
        'any(.. | strings; . as $value | ($names[$value]? // $value) != $value)' \
        "$work_dir/$public_metadata_name" >/dev/null; then
        metadata_tmp=$work_dir/.metadata.$$
        jq --argjson names "$filename_map" \
            'walk(if type == "string" and ($names[.]? != null) then $names[.] else . end)' \
            "$work_dir/$public_metadata_name" > "$metadata_tmp"
        chmod --reference="$work_dir/$public_metadata_name" "$metadata_tmp"
        mv -- "$metadata_tmp" "$work_dir/$public_metadata_name"
    fi
done < <(jq -r '.build_metadata[]?.filename // empty' "$manifest_source")

# Regenerate every adjacent checksum from the staged public basename. The
# release-manifest checksum is regenerated again after the manifest is final.
while IFS= read -r -d '' checksum_source; do
    checksum_name=${checksum_source##*/}
    target_name=${checksum_name%.sha256}
    public_target_name=$(jq -rn --argjson names "$filename_map" --arg name "$target_name" '$names[$name] // empty')
    public_checksum_name=$(jq -rn --argjson names "$filename_map" --arg name "$checksum_name" '$names[$name] // empty')
    [[ -n $public_target_name && -f $work_dir/$public_target_name ]] || {
        printf 'adjacent checksum target is missing: %s\n' "$target_name" >&2
        exit 2
    }
    [[ -n $public_checksum_name ]] || {
        printf 'adjacent checksum was not staged: %s\n' "$checksum_name" >&2
        exit 2
    }
    (cd "$work_dir" && sha256sum -- "$public_target_name" > "$public_checksum_name")
done < <(find "$source_dir" -maxdepth 1 -type f -name '*.sha256' -print0 | sort -z)

aggregate_name=$(jq -r '.checksums.filename // empty' "$manifest_source")
[[ -n $aggregate_name ]] || {
    printf '%s\n' 'release manifest does not declare checksums.filename' >&2
    exit 2
}
public_aggregate_name=$(jq -rn --argjson names "$filename_map" --arg name "$aggregate_name" '$names[$name] // empty')
[[ -n $public_aggregate_name && -f $work_dir/$public_aggregate_name ]] || {
    printf 'aggregate checksum file is missing: %s\n' "$aggregate_name" >&2
    exit 2
}
: > "$work_dir/$public_aggregate_name"
while IFS= read -r checksum_line; do
    [[ $checksum_line =~ ^[0-9a-f]{64}\ \ (.+)$ ]] || {
        printf 'invalid aggregate checksum line: %s\n' "$checksum_line" >&2
        exit 2
    }
    declared_name=${BASH_REMATCH[1]}
    public_name=$(jq -rn --argjson names "$filename_map" --arg name "$declared_name" '$names[$name] // empty')
    [[ -n $public_name && -f $work_dir/$public_name ]] || {
        printf 'manifest-declared asset is missing: %s\n' "$declared_name" >&2
        exit 2
    }
    (cd "$work_dir" && sha256sum -- "$public_name") >> "$work_dir/$public_aggregate_name"
done < "$source_dir/$aggregate_name"

aggregate_sha=$(sha256sum "$work_dir/$public_aggregate_name" | awk '{print $1}')
aggregate_size=$(stat -c '%s' "$work_dir/$public_aggregate_name")

asset_data='{}'
while IFS= read -r -d '' staged_file; do
    staged_name=${staged_file##*/}
    staged_sha=$(sha256sum "$staged_file" | awk '{print $1}')
    staged_size=$(stat -c '%s' "$staged_file")
    asset_data=$(jq -cn \
        --argjson current "$asset_data" \
        --arg name "$staged_name" \
        --arg sha "$staged_sha" \
        --argjson size "$staged_size" \
        '$current + {($name): {sha256: $sha, size_bytes: $size}}')
done < <(find "$work_dir" -maxdepth 1 -type f -print0 | sort -z)

public_assets=$(find "$work_dir" -maxdepth 1 -type f -printf '%f\n' | sort | jq -R . | jq -s .)
adjacent_checksums=$(find "$work_dir" -maxdepth 1 -type f -name '*.sha256' -printf '%f\n' | sort | jq -R . | jq -s .)
manifest_tmp=$work_dir/.release-manifest.$$
jq \
    --argjson names "$filename_map" \
    --argjson asset_data "$asset_data" \
    --arg tag "$release_tag" \
    --arg release_url "$release_url" \
    --arg status "$release_status" \
    --argjson prerelease "$prerelease" \
    --argjson published "$published" \
    --arg aggregate_sha "$aggregate_sha" \
    --argjson aggregate_size "$aggregate_size" \
    --argjson public_assets "$public_assets" \
    --argjson adjacent_checksums "$adjacent_checksums" \
    '
    .release_status = $status |
    .tag = $tag |
    .release_url = $release_url |
    .prerelease = $prerelease |
    .published = $published |
    .artifacts |= map(
        .filename = ($names[.filename] // .filename) |
        .sha256 = $asset_data[.filename].sha256 |
        .size_bytes = $asset_data[.filename].size_bytes
    ) |
    .sbom.filename = ($names[.sbom.filename] // .sbom.filename) |
    .sbom.sha256 = $asset_data[.sbom.filename].sha256 |
    .sbom.size_bytes = $asset_data[.sbom.filename].size_bytes |
    .build_metadata |= map(
        .filename = ($names[.filename] // .filename) |
        .sha256 = $asset_data[.filename].sha256 |
        .size_bytes = $asset_data[.filename].size_bytes
    ) |
    .checksums.filename = ($names[.checksums.filename] // .checksums.filename) |
    .checksums.sha256 = $aggregate_sha |
    .checksums.size_bytes = $aggregate_size |
    .published_asset_count = ($public_assets | length) |
    .published_asset_filenames = $public_assets |
    .adjacent_checksum_files = $adjacent_checksums |
    .qualified_artifact_bytes_unchanged = true |
    .publication_filename_rule = "GitHub publication assets use dot-normalized filenames without ~; internal Debian and RPM package versions remain unchanged." |
    .future_release_process = "Stage GitHub-safe public filenames and filename-dependent manifests before upload; do not rely on GitHub filename normalization." |
    del(.completion_rule)
    ' "$manifest_source" > "$manifest_tmp"
chmod --reference="$work_dir/$public_manifest_name" "$manifest_tmp"
mv -- "$manifest_tmp" "$work_dir/$public_manifest_name"

manifest_checksum_name=$manifest_name.sha256
public_manifest_checksum_name=$(jq -rn --argjson names "$filename_map" --arg name "$manifest_checksum_name" '$names[$name] // empty')
if [[ -n $public_manifest_checksum_name ]]; then
    (cd "$work_dir" && sha256sum -- "$public_manifest_name" > "$public_manifest_checksum_name")
fi

(cd "$work_dir" && sha256sum -c -- "$public_aggregate_name" >/dev/null)
while IFS= read -r -d '' checksum_file; do
    checksum_basename=${checksum_file##*/}
    (cd "$work_dir" && sha256sum -c -- "$checksum_basename" >/dev/null)
done < <(find "$work_dir" -maxdepth 1 -type f -name '*.sha256' -print0 | sort -z)

if find "$work_dir" -maxdepth 1 -type f -printf '%f\n' | grep -Fq '~'; then
    printf '%s\n' 'publication staging contains a GitHub-unsafe filename' >&2
    exit 1
fi
actual_assets=$(find "$work_dir" -maxdepth 1 -type f -printf '%f\n' | sort)
manifest_assets=$(jq -r '.published_asset_filenames[]' "$work_dir/$public_manifest_name" | sort)
[[ $actual_assets == "$manifest_assets" ]] || {
    printf '%s\n' 'release manifest asset inventory does not match staged files' >&2
    exit 1
}

mv -- "$work_dir" "$output_dir"
trap - EXIT HUP INT TERM

printf 'GitHub-safe publication staging completed\n'
printf 'output_dir=%s\n' "$output_dir"
printf 'asset_count=%s\n' "$source_count"
printf 'checksums=%s\n' "$public_aggregate_name"
printf 'release_manifest=%s\n' "$public_manifest_name"
printf 'checksums_sha256=%s\n' "$(sha256sum "$output_dir/$public_aggregate_name" | awk '{print $1}')"
printf 'release_manifest_sha256=%s\n' "$(sha256sum "$output_dir/$public_manifest_name" | awk '{print $1}')"
