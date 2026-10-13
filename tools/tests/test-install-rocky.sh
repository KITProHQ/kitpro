#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
installer=$repo_dir/tools/install-rocky.sh
prepare=$repo_dir/tools/platform-validation/prepare-rocky.sh
verify=$repo_dir/tools/platform-validation/verify-host.sh
runtime_validation=$repo_dir/tools/platform-validation/validate-rocky-runtime.sh

bash -n "$installer" "$prepare" "$verify" "$runtime_validation"
if command -v shellcheck >/dev/null 2>&1; then
    shellcheck "$installer" "$prepare" "$verify" "$runtime_validation"
fi

grep -q 'only Rocky Linux 10 is installable' "$installer"
grep -q 'podman crun container-selinux firewalld policycoreutils-python-utils' "$installer"
grep -q 'systemctl enable --now firewalld.service' "$installer"
grep -q -- '--require-podman' "$installer"
grep -q 'Podman 5 or newer is required' "$verify"
grep -q 'expected crun' "$verify"
grep -q 'Quadlet systemd generator is unavailable' "$verify"
grep -q 'validate-rocky-runtime.sh' "$repo_dir/tools/platform-validation/README.md"

if grep -E 'download\.docker\.com|docker-ce|setenforce[[:space:]]+0|systemctl (disable|stop).*firewalld|--privileged' \
    "$installer" "$prepare" "$runtime_validation"; then
    printf 'unsafe Rocky installer behavior found\n' >&2
    exit 1
fi

printf 'Rocky installer static tests: PASS\n'
