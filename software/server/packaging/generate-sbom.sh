#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
server_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
repo_dir=$(git -C "$server_dir" rev-parse --show-toplevel)
version=${1:-0.1.0~alpha1}
output_dir=${2:-$server_dir/dist}
output="$output_dir/kitpro-server_${version}_amd64.cdx.json"
source_commit=$(git -C "$repo_dir" rev-parse HEAD)
source_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repo_dir" show -s --format=%ct "$source_commit")}
build_date=$(date -u -d "@$source_epoch" +%Y-%m-%dT%H:%M:%SZ)

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

install -d "$output_dir"
output_dir=$(CDPATH='' cd -- "$output_dir" && pwd)
output="$output_dir/kitpro-server_${version}_amd64.cdx.json"
generated=$(mktemp)
trap 'rm -f "$generated"' EXIT HUP INT TERM
(cd "$server_dir" && go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.7.0 mod -json -licenses -noserial -output "$generated")
jq --arg version "$version" --arg source_commit "$source_commit" --arg build_date "$build_date" '
  ("pkg:golang/github.com/kitpro/kitpro/software/server@" + $version + "?type=module") as $root_ref
  | .metadata.timestamp = $build_date
  | .metadata.component.version = $version
  | .metadata.component["bom-ref"] = $root_ref
  | .metadata.component.purl = ("pkg:golang/github.com/kitpro/kitpro/software/server@" + $version + "?type=module&goos=linux&goarch=amd64")
  | .metadata.properties = ((.metadata.properties // []) + [{"name":"kitpro:source_commit","value":$source_commit}])
  | .dependencies |= map(if (.ref | startswith("pkg:golang/github.com/kitpro/kitpro/software/server@")) then .ref = $root_ref else . end)
' "$generated" > "$output"
(cd "$output_dir" && sha256sum "$(basename "$output")" > "$(basename "$output").sha256")
printf '%s\n' "$output"
