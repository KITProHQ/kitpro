#!/usr/bin/env bash

# Prepare an already-created disposable Rocky Linux 10 VM for KITPro validation.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
INSTALLER="$SCRIPT_DIR/../install-docker.sh"
ACKNOWLEDGED=false
DRY_RUN=false
ASSUME_YES=false
REMOVE_CONFLICTS=false

usage() {
    cat <<'EOF'
Usage: sudo ./prepare-rocky.sh --acknowledge-disposable-vm [OPTIONS]

Options:
  --dry-run
  --yes
  --remove-conflicts
  --help

This script mutates the current guest. Take the required clean snapshot first.
It requires SELinux Enforcing before and after preparation. It does not alter
SELinux mode, firewalld, or Docker group membership.
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
"$SCRIPT_DIR/verify-host.sh" --platform rocky10
[[ $(getenforce) == "Enforcing" ]] || die "SELinux is not Enforcing"

package_options=()
[[ "$ASSUME_YES" == true ]] && package_options=(-y)
run dnf "${package_options[@]}" install python3 util-linux iproute audit policycoreutils

installer_options=()
[[ "$DRY_RUN" == true ]] && installer_options+=(--dry-run)
[[ "$ASSUME_YES" == true ]] && installer_options+=(--yes)
[[ "$REMOVE_CONFLICTS" == true ]] && installer_options+=(--remove-conflicts)
"$INSTALLER" "${installer_options[@]}"

if [[ "$DRY_RUN" == false ]]; then
    [[ $(getenforce) == "Enforcing" ]] || die "SELinux changed from Enforcing"
    "$SCRIPT_DIR/verify-host.sh" --platform rocky10 --require-docker
fi

printf 'Preparation complete. SELinux remains Enforcing; firewalld and Docker group membership were not changed.\n'
