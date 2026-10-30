#!/usr/bin/env bash

# Prepare an already-created disposable Debian 13 VM for KITPro validation.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
INSTALLER="$SCRIPT_DIR/../install-docker.sh"
ACKNOWLEDGED=false
DRY_RUN=false
ASSUME_YES=false
REMOVE_CONFLICTS=false

usage() {
    cat <<'EOF'
Usage: sudo ./prepare-debian.sh --acknowledge-disposable-vm [OPTIONS]

Options:
  --dry-run
  --yes
  --remove-conflicts
  --help

This script mutates the current guest. Take the required clean snapshot first.
It does not create a VM and does not grant Docker access to any user.
EOF
}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
run() {
    printf '+'; printf ' %q' "$@"; printf '\n'
    [[ "$DRY_RUN" == true ]] || "$@"
}

while (($#)); do
    case "$1" in
        --acknowledge-disposable-vm) ACKNOWLEDGED=true ;;
        --dry-run) DRY_RUN=true ;;
        --yes) ASSUME_YES=true ;;
        --remove-conflicts) REMOVE_CONFLICTS=true ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

[[ "$ACKNOWLEDGED" == true ]] || die "--acknowledge-disposable-vm is required"
[[ "$DRY_RUN" == true || "$EUID" -eq 0 ]] || die "run as root, normally with sudo"
"$SCRIPT_DIR/verify-host.sh" --platform debian13

package_options=()
[[ "$ASSUME_YES" == true ]] && package_options=(-y)
run apt-get update
run env DEBIAN_FRONTEND=noninteractive apt-get install "${package_options[@]}" python3 util-linux iproute2

installer_options=()
[[ "$DRY_RUN" == true ]] && installer_options+=(--dry-run)
[[ "$ASSUME_YES" == true ]] && installer_options+=(--yes)
[[ "$REMOVE_CONFLICTS" == true ]] && installer_options+=(--remove-conflicts)
"$INSTALLER" "${installer_options[@]}"

if [[ "$DRY_RUN" == false ]]; then
    "$SCRIPT_DIR/verify-host.sh" --platform debian13 --require-docker
fi

printf 'Preparation complete. No account was added to the docker group.\n'
