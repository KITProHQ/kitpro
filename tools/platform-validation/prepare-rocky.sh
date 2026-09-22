#!/usr/bin/env bash

# Prepare an already-created disposable Rocky Linux 10 VM for KITPro validation.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
INSTALLER="$SCRIPT_DIR/../install-rocky.sh"
ACKNOWLEDGED=false
DRY_RUN=false
ASSUME_YES=false

usage() {
    cat <<'EOF'
Usage: sudo ./prepare-rocky.sh --acknowledge-disposable-vm [OPTIONS]

Options:
  --dry-run
  --yes
  --help

This script mutates the current guest. Take the required clean snapshot first.
It requires SELinux Enforcing before and after preparation. It does not alter
SELinux mode or firewall policy. It installs the native Rocky Podman stack.
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
        --help|-h) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

[[ "$ACKNOWLEDGED" == true ]] || die "--acknowledge-disposable-vm is required"
[[ "$DRY_RUN" == true || "$EUID" -eq 0 ]] || die "run as root, normally with sudo"
"$SCRIPT_DIR/verify-host.sh" --platform rocky10
[[ $(getenforce) == "Enforcing" ]] || die "SELinux is not Enforcing"

installer_options=()
[[ "$DRY_RUN" == true ]] && installer_options+=(--dry-run)
[[ "$ASSUME_YES" == true ]] && installer_options+=(--yes)
"$INSTALLER" "${installer_options[@]}"

if [[ "$DRY_RUN" == false ]]; then
    [[ $(getenforce) == "Enforcing" ]] || die "SELinux changed from Enforcing"
    "$SCRIPT_DIR/verify-host.sh" --platform rocky10 --require-podman
fi

printf 'Preparation complete. SELinux remains Enforcing and firewalld remains enabled. No Docker repository was added.\n'
