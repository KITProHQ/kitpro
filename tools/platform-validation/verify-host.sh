#!/usr/bin/env bash

# Read-only acceptance checks for a disposable KITPro platform-validation VM.

set -Eeuo pipefail

PLATFORM=""
REQUIRE_DOCKER=false

usage() {
    cat <<'EOF'
Usage: ./verify-host.sh --platform debian13|rocky10 [--require-docker]

Print a structured host baseline and fail if a required platform invariant is
not met. Resource sizes are recorded and warned on, because VM partitioning can
make a nominal 40 GiB disk appear smaller at the root filesystem.
EOF
}

die() {
    printf 'FAIL %s\n' "$*" >&2
    exit 1
}

observe() {
    printf 'OBSERVATION %s=%s\n' "$1" "$2"
}

pass() {
    printf 'PASS %s\n' "$*"
}

warn() {
    printf 'OBSERVATION warning=%s\n' "$*"
}

while (($#)); do
    case "$1" in
        --platform)
            (($# >= 2)) || die "--platform requires a value"
            PLATFORM=$2
            shift
            ;;
        --require-docker) REQUIRE_DOCKER=true ;;
        --help|-h)
            usage
            exit 0
            ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

case "$PLATFORM" in
    debian13|rocky10) ;;
    *) die "--platform must be debian13 or rocky10" ;;
esac

for required in uname ps stat nproc awk findmnt df tr systemctl lscpu; do
    command -v "$required" >/dev/null 2>&1 || die "required baseline command not found: $required"
done

[[ -r /etc/os-release ]] || die "/etc/os-release is unavailable"
# shellcheck disable=SC1091
source /etc/os-release

case "$PLATFORM" in
    debian13)
        [[ "${ID:-}" == "debian" && "${VERSION_ID%%.*}" == "13" ]] || die "expected Debian 13, found ${PRETTY_NAME:-unknown}"
        ;;
    rocky10)
        [[ "${ID:-}" == "rocky" && "${VERSION_ID%%.*}" == "10" ]] || die "expected Rocky Linux 10, found ${PRETTY_NAME:-unknown}"
        ;;
esac
pass "distribution=$PRETTY_NAME"

architecture=$(uname -m)
[[ "$architecture" == "x86_64" ]] || die "expected x86_64, found $architecture"
pass "architecture=$architecture"

pid1=$(ps -p 1 -o comm=)
[[ "$pid1" == "systemd" ]] || die "expected systemd PID 1, found $pid1"
pass "pid1=systemd"

cgroup_fs=$(stat -fc %T /sys/fs/cgroup)
[[ "$cgroup_fs" == "cgroup2fs" ]] || die "expected cgroup v2, found $cgroup_fs"
pass "cgroup_filesystem=$cgroup_fs"

cpu_count=$(nproc)
memory_kib=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
root_size=$(findmnt -bno SIZE /)
root_free=$(df -B1 --output=avail / | awk 'NR == 2 {print $1}')
root_filesystem=$(findmnt -no FSTYPE /)
root_source=$(findmnt -no SOURCE /)
observe cpu_count "$cpu_count"
observe cpu_model "$(lscpu | awk -F: '/^Model name:/ {sub(/^[[:space:]]+/, "", $2); print $2; exit}')"
observe memory_kib "$memory_kib"
observe root_source "$root_source"
observe root_filesystem "$root_filesystem"
observe root_size_bytes "$root_size"
observe root_free_bytes "$root_free"

((cpu_count >= 2)) || warn "fewer than the target 2 vCPUs"
((memory_kib >= 3670016)) || warn "less than approximately 4 GiB RAM"
((root_size >= 37580963840)) || warn "root filesystem is below the approximately 40 GiB VM target"

if [[ "$PLATFORM" == "debian13" ]]; then
    [[ "$root_filesystem" == "ext4" ]] || die "Debian reference root must be ext4; found $root_filesystem"
else
    command -v getenforce >/dev/null 2>&1 || die "SELinux tooling is unavailable"
    selinux_mode=$(getenforce)
    [[ "$selinux_mode" == "Enforcing" ]] || die "SELinux must remain Enforcing; found $selinux_mode"
    pass "selinux=Enforcing"
    if systemctl list-unit-files firewalld.service >/dev/null 2>&1; then
        observe firewalld_active "$(systemctl is-active firewalld.service 2>/dev/null || true)"
        observe firewalld_enabled "$(systemctl is-enabled firewalld.service 2>/dev/null || true)"
    else
        observe firewalld "not-installed"
    fi
fi

observe kernel "$(uname -srvo)"
observe systemd "$(systemctl --version | sed -n '1p')"
observe root_mount "$(findmnt -no FSTYPE,OPTIONS /)"
observe cgroup_controllers "$(tr '\n' ' ' </sys/fs/cgroup/cgroup.controllers)"

if [[ "$REQUIRE_DOCKER" == true ]]; then
    command -v docker >/dev/null 2>&1 || die "docker CLI is unavailable"
    command -v containerd >/dev/null 2>&1 || die "containerd is unavailable"
    systemctl is-active --quiet docker.service || die "docker.service is not active"
    systemctl is-active --quiet containerd.service || die "containerd.service is not active"
    docker version
    docker info --format 'Docker daemon response: server={{.ServerVersion}} cgroup={{.CgroupVersion}} driver={{.Driver}}'
    docker compose version
    docker buildx version
    containerd --version
    pass "docker_verification=complete"
fi

pass "host_baseline=accepted"
