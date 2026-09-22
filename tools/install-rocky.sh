#!/usr/bin/env bash

# Install and validate the native Rocky Linux 10 host runtime for KITPro Server.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
DRY_RUN=false
ASSUME_YES=false
PACKAGES=()

usage() {
    cat <<'EOF'
Usage: sudo ./tools/install-rocky.sh [OPTIONS]

Options:
  --package PATH   Install a local KITPro RPM after host setup; may be repeated.
  --dry-run        Print mutating commands without running them.
  --yes            Pass -y to dnf.
  --help

Only Rocky Linux 10 is installable. RHEL 10 and AlmaLinux 10 are recognized by
the application but are not accepted by this installer. SELinux Enforcing and
an enabled, active firewalld service are required. No firewall ports are opened
because KITPro publishes loopback ports by default and manages exact LAN binds.
EOF
}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
run() {
    printf '+'; printf ' %q' "$@"; printf '\n'
    [[ "$DRY_RUN" == true ]] || "$@"
}

while (($#)); do
    case "$1" in
        --package)
            (($# >= 2)) || die "--package requires a path"
            PACKAGES+=("$2")
            shift
            ;;
        --dry-run) DRY_RUN=true ;;
        --yes) ASSUME_YES=true ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

[[ "$DRY_RUN" == true || "$EUID" -eq 0 ]] || die "run as root, normally with sudo"
[[ -r /etc/os-release ]] || die "/etc/os-release is unavailable"
# shellcheck disable=SC1091
source /etc/os-release
[[ "${ID:-}" == "rocky" && "${VERSION_ID%%.*}" == "10" ]] || die "only Rocky Linux 10 is installable; found ${PRETTY_NAME:-unknown}"
command -v getenforce >/dev/null 2>&1 || die "SELinux tooling is unavailable"
[[ $(getenforce) == "Enforcing" ]] || die "SELinux must be Enforcing before installation"
for package in "${PACKAGES[@]}"; do
    [[ -f "$package" ]] || die "RPM not found: $package"
done

dnf_options=()
[[ "$ASSUME_YES" == true ]] && dnf_options=(-y)
run dnf "${dnf_options[@]}" install \
    podman crun container-selinux firewalld policycoreutils-python-utils \
    python3 util-linux iproute audit
run systemctl enable --now firewalld.service

if [[ "$DRY_RUN" == false ]]; then
    "$SCRIPT_DIR/platform-validation/verify-host.sh" --platform rocky10 --require-podman
fi

if [[ ${#PACKAGES[@]} -gt 0 ]]; then
    run dnf "${dnf_options[@]}" install "${PACKAGES[@]}"
    if [[ "$DRY_RUN" == false ]]; then
        systemctl is-active --quiet kitpro-helper.socket || die "kitpro-helper.socket is not active"
        systemctl is-active --quiet kitpro-api.service || die "kitpro-api.service is not active"
        printf 'KITPro package installed. Check status with systemctl status kitpro-api kitpro-helper.socket.\n'
    fi
else
    printf 'Rocky container runtime ready. Build and pass a KITPro RPM with --package to install the application.\n'
fi
