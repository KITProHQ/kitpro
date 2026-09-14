#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
server_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
version=${1:-0.1.0~alpha6}
output_dir=${2:-$server_dir/dist}
output="$output_dir/kitpro-server_${version}_amd64.cdx.json"

install -d "$output_dir"
output_dir=$(CDPATH='' cd -- "$output_dir" && pwd)
output="$output_dir/kitpro-server_${version}_amd64.cdx.json"
source_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$server_dir" show -s --format=%ct HEAD)}
timestamp=$(date -u -d "@$source_epoch" '+%Y-%m-%dT%H:%M:%SZ')
temporary_output=$(mktemp)
trap 'rm -f "$temporary_output"' EXIT HUP INT TERM
(cd "$server_dir" && go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.7.0 mod -json -licenses -output "$temporary_output")
jq --arg timestamp "$timestamp" 'del(.serialNumber) | .metadata.timestamp = $timestamp' "$temporary_output" > "$output"
(cd "$output_dir" && sha256sum "$(basename "$output")" > "$(basename "$output").sha256")
printf '%s\n' "$output"
