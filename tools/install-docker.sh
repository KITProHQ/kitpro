#!/usr/bin/env bash

# KITPro development Docker installer.
# This script installs Docker Engine from Docker's official repositories. It is
# not the KITPro Server installer and does not select a production language.

set -Eeuo pipefail

readonly DOCKER_DEB_KEY_FINGERPRINT="9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
readonly DOCKER_RPM_KEY_FINGERPRINT="060A61C51B558A7F742B77AAC52FEB6B621E9F35"
readonly DOCKER_RHEL_REPOSITORY="https://download.docker.com/linux/rhel/docker-ce.repo"

DRY_RUN=false
GRANT_USER_ACCESS=false
REMOVE_CONFLICTS=false
VERIFY_ONLY=false
ASSUME_YES=false
RUN_HELLO_WORLD=false
TEMP_DIR=""
DOCKER_RESTART_REQUIRED=false

OS_ID=""
OS_VERSION=""
OS_VERSION_MAJOR=""
OS_CODENAME=""
OS_FAMILY=""
OS_DISPLAY=""
DOCKER_REPOSITORY_MEMBER=""

usage() {
    cat <<'EOF'
Usage: sudo ./tools/install-docker.sh [OPTIONS]

Install and verify Docker Engine from Docker's official package repositories.

Options:
  --dry-run             Print mutating commands without executing them
  --grant-user-access   Add the unambiguous sudo-invoking user to docker
  --remove-conflicts    Remove only Docker's documented conflicting packages
  --verify-only         Do not install; verify the existing installation
  --run-hello-world     Run and remove Docker's disposable hello-world test
  --yes                 Use non-interactive package-manager confirmation
  --help                Show this help

Supported operating systems:
  Arch Linux (current official repositories)
  Debian 13
  Ubuntu 24.04 LTS and 26.04 LTS
  RHEL, Rocky Linux, and AlmaLinux 9 or 10

The default does not add any account to the docker group. Docker group access
is root-equivalent. --grant-user-access is accepted only when SUDO_USER names
an existing non-root account; the script never guesses from login state.

On an SELinux-enabled RHEL-family host, a fresh installation enables Docker's
SELinux integration. Existing daemon.json files are preserved and must already
enable that integration for verification to pass while SELinux is Enforcing.

If conflicting distribution packages are present, the default is to stop and
list them. Use --remove-conflicts only after reviewing that removal.
EOF
}

log() {
    printf '%s\n' "$*"
}

warn() {
    printf 'WARNING: %s\n' "$*" >&2
}

die() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

cleanup() {
    if [[ -n "$TEMP_DIR" && -d "$TEMP_DIR" ]]; then
        rm -rf -- "$TEMP_DIR"
    fi
}

print_command() {
    printf '+'
    printf ' %q' "$@"
    printf '\n'
}

run() {
    print_command "$@"
    if [[ "$DRY_RUN" == false ]]; then
        "$@"
    fi
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

parse_arguments() {
    while (($#)); do
        case "$1" in
            --dry-run) DRY_RUN=true ;;
            --grant-user-access) GRANT_USER_ACCESS=true ;;
            --remove-conflicts) REMOVE_CONFLICTS=true ;;
            --verify-only) VERIFY_ONLY=true ;;
            --run-hello-world) RUN_HELLO_WORLD=true ;;
            --yes) ASSUME_YES=true ;;
            --help|-h)
                usage
                exit 0
                ;;
            *) die "Unknown option: $1" ;;
        esac
        shift
    done
}

detect_os() {
    local os_release_file=${1:-/etc/os-release}
    [[ -r "$os_release_file" ]] || die "Cannot read $os_release_file"

    local ID="" VERSION_ID="" VERSION_CODENAME="" UBUNTU_CODENAME="" PRETTY_NAME=""
    # /etc/os-release is owned by the operating system and is the required
    # source of distribution identity for this installer.
    # shellcheck disable=SC1090
    source "$os_release_file"

    OS_ID=${ID,,}
    OS_VERSION=${VERSION_ID:-}
    OS_VERSION_MAJOR=${OS_VERSION%%.*}
    OS_DISPLAY=${PRETTY_NAME:-"$OS_ID $OS_VERSION"}

    case "$OS_ID" in
        debian)
            [[ "$OS_VERSION_MAJOR" == "13" ]] || die "Unsupported Debian release: ${OS_VERSION:-unknown}. Only Debian 13 is supported."
            OS_FAMILY="debian"
            DOCKER_REPOSITORY_MEMBER="debian"
            OS_CODENAME=${VERSION_CODENAME:-}
            ;;
        ubuntu)
            case "$OS_VERSION" in
                24.04|26.04) ;;
                *) die "Unsupported Ubuntu release: ${OS_VERSION:-unknown}. Only Ubuntu 24.04 and 26.04 LTS are supported." ;;
            esac
            OS_FAMILY="debian"
            DOCKER_REPOSITORY_MEMBER="ubuntu"
            OS_CODENAME=${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}
            ;;
        arch)
            OS_FAMILY="arch"
            DOCKER_REPOSITORY_MEMBER="arch"
            ;;
        rhel|rocky|almalinux)
            case "$OS_VERSION_MAJOR" in
                9|10) ;;
                *) die "Unsupported $OS_ID release: ${OS_VERSION:-unknown}. Only major versions 9 and 10 are supported." ;;
            esac
            OS_FAMILY="rhel"
            DOCKER_REPOSITORY_MEMBER="rhel"
            ;;
        *)
            die "Unsupported distribution ID '$OS_ID'. This script does not infer support from ID_LIKE."
            ;;
    esac

    if [[ "$OS_FAMILY" == "debian" ]]; then
        [[ "$OS_CODENAME" =~ ^[a-z0-9]+$ ]] || die "Missing or unsafe distribution codename: '$OS_CODENAME'"
    fi
}

validate_host_baseline() {
    require_command uname
    require_command ps
    require_command systemctl

    local pid1
    pid1=$(ps -p 1 -o comm=)
    [[ "$pid1" == "systemd" ]] || die "PID 1 is '$pid1', not systemd. This installer supports systemd hosts only."

    case "$OS_FAMILY" in
        debian)
            require_command dpkg
            local architecture
            architecture=$(dpkg --print-architecture)
            case "$DOCKER_REPOSITORY_MEMBER:$architecture" in
                debian:amd64|debian:arm64|debian:armhf|debian:ppc64el) ;;
                ubuntu:amd64|ubuntu:arm64|ubuntu:armhf|ubuntu:ppc64el|ubuntu:s390x) ;;
                *) die "Architecture '$architecture' is not in Docker's documented set for $OS_DISPLAY." ;;
            esac
            log "Detected architecture: $architecture"
            ;;
        rhel)
            require_command dnf
            require_command rpm
            local machine
            machine=$(uname -m)
            case "$machine" in
                x86_64|aarch64|s390x) ;;
                *) die "Architecture '$machine' is not in Docker's documented RHEL set." ;;
            esac
            log "Detected architecture: $machine"
            ;;
        arch)
            require_command pacman
            local machine
            machine=$(uname -m)
            [[ "$machine" == "x86_64" ]] || die "Architecture '$machine' is not supported by KITPro on Arch Linux."
            log "Detected architecture: $machine"
            ;;
    esac
}

ensure_root_for_mutation_or_verification() {
    if [[ "$DRY_RUN" == false && "$EUID" -ne 0 ]]; then
        die "Run this script as root, normally with sudo."
    fi
}

make_temp_dir() {
    if [[ "$DRY_RUN" == false && -z "$TEMP_DIR" ]]; then
        TEMP_DIR=$(mktemp -d /tmp/kitpro-install-docker.XXXXXX)
    fi
}

download_and_verify_key() {
    local url=$1
    local expected_fingerprint=$2
    local destination=$3

    if [[ "$DRY_RUN" == true ]]; then
        print_command curl -fsSL "$url" -o TEMPORARY_KEY_FILE
        log "+ verify OpenPGP primary fingerprint $expected_fingerprint"
        print_command install -o root -g root -m 0644 TEMPORARY_KEY_FILE "$destination"
        return
    fi

    make_temp_dir
    local key_file="$TEMP_DIR/docker.asc"
    run curl -fsSL "$url" -o "$key_file"

    local actual_fingerprint
    actual_fingerprint=$(gpg --batch --show-keys --with-colons "$key_file" 2>/dev/null | awk -F: '$1 == "fpr" {print $10; exit}')
    [[ "$actual_fingerprint" == "$expected_fingerprint" ]] || die "Docker signing-key fingerprint mismatch: expected $expected_fingerprint, got ${actual_fingerprint:-none}"
    log "Verified Docker signing-key fingerprint: $actual_fingerprint"
    run install -o root -g root -m 0644 "$key_file" "$destination"
}

installed_deb_conflicts() {
    local package status
    local conflicts=(docker.io docker-compose docker-compose-v2 docker-doc docker-buildx podman-docker containerd runc)
    for package in "${conflicts[@]}"; do
        status=$(dpkg-query -W -f='${db:Status-Abbrev}' "$package" 2>/dev/null || true)
        [[ "$status" == ii* ]] && printf '%s\n' "$package"
    done
}

installed_rpm_conflicts() {
    local package
    local conflicts=(docker docker-client docker-client-latest docker-common docker-latest docker-latest-logrotate docker-logrotate docker-engine podman runc)
    for package in "${conflicts[@]}"; do
        rpm -q "$package" >/dev/null 2>&1 && printf '%s\n' "$package"
    done
}

handle_conflicts() {
    local -a found=()
    if [[ "$OS_FAMILY" == "arch" ]]; then
        return 0
    elif [[ "$OS_FAMILY" == "debian" ]]; then
        mapfile -t found < <(installed_deb_conflicts)
    else
        mapfile -t found < <(installed_rpm_conflicts)
    fi
    ((${#found[@]})) || return 0

    warn "Conflicting packages are installed: ${found[*]}"
    if [[ "$REMOVE_CONFLICTS" == false ]]; then
        die "Review the packages, then rerun with --remove-conflicts if removal is intended. Docker data is not deleted."
    fi

    warn "Removing only the listed conflicting packages. Existing /var/lib/docker data is not removed."
    if [[ "$OS_FAMILY" == "debian" ]]; then
        local -a options=()
        [[ "$ASSUME_YES" == true ]] && options=(-y)
        run env DEBIAN_FRONTEND=noninteractive apt-get remove "${options[@]}" "${found[@]}"
    else
        local -a options=()
        [[ "$ASSUME_YES" == true ]] && options=(-y)
        run dnf "${options[@]}" remove "${found[@]}"
    fi
}

install_arch_family() {
    require_command pacman

    local -a options=(--needed)
    [[ "$ASSUME_YES" == true ]] && options+=(--noconfirm)

    # Arch supports only full-system upgrades. Install Docker from the same
    # synchronized official repositories in one transaction.
    run pacman -Syu "${options[@]}" docker docker-buildx docker-compose
}

install_debian_family() {
    require_command apt-get

    local -a options=()
    [[ "$ASSUME_YES" == true ]] && options=(-y)

    run apt-get update
    run env DEBIAN_FRONTEND=noninteractive apt-get install "${options[@]}" ca-certificates curl gnupg
    run install -o root -g root -m 0755 -d /etc/apt/keyrings
    download_and_verify_key \
        "https://download.docker.com/linux/$DOCKER_REPOSITORY_MEMBER/gpg" \
        "$DOCKER_DEB_KEY_FINGERPRINT" \
        /etc/apt/keyrings/docker.asc

    local architecture repository_uri source_text
    architecture=$(dpkg --print-architecture)
    repository_uri="https://download.docker.com/linux/$DOCKER_REPOSITORY_MEMBER"
    source_text=$(printf 'Types: deb\nURIs: %s\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n' \
        "$repository_uri" "$OS_CODENAME" "$architecture")

    if [[ "$DRY_RUN" == true ]]; then
        log "+ install root-owned mode 0644 /etc/apt/sources.list.d/docker.sources with:"
        printf '%s\n' "$source_text"
    else
        make_temp_dir
        printf '%s\n' "$source_text" >"$TEMP_DIR/docker.sources"
        run install -o root -g root -m 0644 "$TEMP_DIR/docker.sources" /etc/apt/sources.list.d/docker.sources
    fi

    run apt-get update
    run env DEBIAN_FRONTEND=noninteractive apt-get install "${options[@]}" \
        docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

install_rhel_family() {
    require_command dnf
    require_command rpm

    if [[ "$OS_ID" == "rocky" || "$OS_ID" == "almalinux" ]]; then
        warn "Docker documents and supports RHEL. KITPro is validating use of Docker's RHEL repository on $OS_DISPLAY; this is not a claim of Docker certification for this derivative."
    fi

    local -a options=()
    [[ "$ASSUME_YES" == true ]] && options=(-y)

    run dnf "${options[@]}" install ca-certificates curl dnf-plugins-core gnupg2
    download_and_verify_key \
        https://download.docker.com/linux/rhel/gpg \
        "$DOCKER_RPM_KEY_FINGERPRINT" \
        /etc/pki/rpm-gpg/RPM-GPG-KEY-docker
    if [[ "$DRY_RUN" == true ]]; then
        print_command rpm --import /etc/pki/rpm-gpg/RPM-GPG-KEY-docker
    else
        run rpm --import /etc/pki/rpm-gpg/RPM-GPG-KEY-docker
    fi
    local repository_file=/etc/yum.repos.d/docker-ce.repo
    if [[ -r "$repository_file" ]]; then
        if grep -Fq 'download.docker.com/linux/rhel' "$repository_file"; then
            log "Docker RHEL repository already configured: $repository_file"
        else
            die "$repository_file exists but does not identify Docker's RHEL repository. Refusing to overwrite it."
        fi
    else
        run dnf config-manager --add-repo "$DOCKER_RHEL_REPOSITORY"
    fi
    run dnf "${options[@]}" install \
        docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

configure_rhel_selinux() {
    [[ "$OS_FAMILY" == "rhel" ]] || return 0
    command -v getenforce >/dev/null 2>&1 || return 0
    [[ $(getenforce) != "Disabled" ]] || return 0

    local daemon_config=/etc/docker/daemon.json
    if [[ -e "$daemon_config" ]]; then
        warn "$daemon_config already exists; preserving it instead of attempting an unsafe JSON merge."
        warn "Verification will fail unless the existing configuration enables Docker SELinux support."
        return 0
    fi

    local config_text='{"selinux-enabled": true}'
    DOCKER_RESTART_REQUIRED=true
    if [[ "$DRY_RUN" == true ]]; then
        print_command install -o root -g root -m 0755 -d /etc/docker
        log "+ validate and install root-owned mode 0644 $daemon_config with:"
        printf '%s\n' "$config_text"
        return 0
    fi

    require_command dockerd
    make_temp_dir
    printf '%s\n' "$config_text" >"$TEMP_DIR/daemon.json"
    run dockerd --validate --config-file "$TEMP_DIR/daemon.json"
    run install -o root -g root -m 0755 -d /etc/docker
    run install -o root -g root -m 0644 "$TEMP_DIR/daemon.json" "$daemon_config"
    log "Enabled Docker SELinux integration for the SELinux-capable RHEL-family host."
}

resolve_grant_user() {
    local candidate=${SUDO_USER:-}
    [[ -n "$candidate" && "$candidate" != "root" ]] || die "--grant-user-access requires an unambiguous non-root SUDO_USER. The script will not guess."
    [[ "$candidate" =~ ^[a-z_][a-z0-9_-]{0,30}\$?$ ]] || die "SUDO_USER has an unsupported account-name form. Refusing to guess or normalize it."
    getent passwd "$candidate" >/dev/null || die "SUDO_USER '$candidate' is not a local or resolvable account."
    [[ $(id -u -- "$candidate") -ne 0 ]] || die "Refusing to grant Docker access to root through this option."
    printf '%s\n' "$candidate"
}

grant_user_access() {
    [[ "$GRANT_USER_ACCESS" == true ]] || return 0
    require_command getent
    require_command usermod
    local target_user
    target_user=$(resolve_grant_user)
    warn "Docker group membership grants root-equivalent host control."
    warn "Granting that authority only to: $target_user"
    run usermod -aG docker "$target_user"
    log "The user must start a new login session before group membership takes effect."
}

report_selinux() {
    if command -v getenforce >/dev/null 2>&1; then
        log "SELinux: $(getenforce)"
    else
        log "SELinux: tooling not present"
    fi
}

verify_installation() {
    if [[ "$DRY_RUN" == true ]]; then
        print_command systemctl is-active --quiet docker.service
        print_command systemctl is-active --quiet containerd.service
        print_command docker version
        print_command docker info
        print_command docker compose version
        print_command docker buildx version
        print_command containerd --version
        log "+ identify cgroup filesystem and Docker cgroup version"
        [[ "$RUN_HELLO_WORLD" == true ]] && print_command docker run --rm hello-world
        report_selinux
        return
    fi

    require_command docker
    require_command containerd
    systemctl is-active --quiet docker.service || die "Docker service is not active."
    systemctl is-active --quiet containerd.service || die "containerd service is not active."

    log "Docker service: active"
    log "containerd service: active"
    docker version
    docker info --format 'Docker daemon response: server={{.ServerVersion}} cgroup={{.CgroupVersion}} driver={{.Driver}}'
    if [[ "$OS_FAMILY" == "rhel" ]] && command -v getenforce >/dev/null 2>&1 && [[ $(getenforce) == "Enforcing" ]]; then
        local docker_security_options
        docker_security_options=$(docker info --format '{{json .SecurityOptions}}')
        [[ "$docker_security_options" == *'"name=selinux"'* ]] || \
            die "SELinux is Enforcing, but Docker SELinux integration is not enabled."
        log "Docker SELinux integration: enabled"
    fi
    docker compose version
    docker buildx version
    containerd --version
    log "Host cgroup filesystem: $(stat -fc %T /sys/fs/cgroup)"
    report_selinux

    if [[ "$RUN_HELLO_WORLD" == true ]]; then
        warn "Running the optional disposable hello-world container. The image may be pulled."
        docker run --rm hello-world
    fi
}

main() {
    parse_arguments "$@"
    detect_os /etc/os-release
    log "Detected supported host: $OS_DISPLAY"
    validate_host_baseline
    ensure_root_for_mutation_or_verification

    if [[ "$VERIFY_ONLY" == true && ( "$GRANT_USER_ACCESS" == true || "$REMOVE_CONFLICTS" == true ) ]]; then
        die "--verify-only cannot be combined with access grants or package removal."
    fi
    if [[ "$GRANT_USER_ACCESS" == true ]]; then
        require_command getent
        require_command id
        require_command usermod
        resolve_grant_user >/dev/null
    fi

    local selinux_before="unavailable"
    if command -v getenforce >/dev/null 2>&1; then
        selinux_before=$(getenforce)
        log "SELinux before installation: $selinux_before"
    fi

    if [[ "$VERIFY_ONLY" == false ]]; then
        handle_conflicts
        case "$OS_FAMILY" in
            arch) install_arch_family ;;
            debian) install_debian_family ;;
            rhel)
                install_rhel_family
                configure_rhel_selinux
                ;;
        esac
        run systemctl enable --now docker.service containerd.service
        if [[ "$DOCKER_RESTART_REQUIRED" == true ]]; then
            run systemctl restart docker.service
        fi
        grant_user_access
    fi

    verify_installation

    if [[ "$selinux_before" == "Enforcing" ]]; then
        local selinux_after
        selinux_after=$(getenforce)
        [[ "$selinux_after" == "Enforcing" ]] || die "SELinux was Enforcing before installation but is now $selinux_after. Installation is not accepted."
    fi

    log "Docker verification completed successfully."
    if [[ "$GRANT_USER_ACCESS" == false ]]; then
        log "No user was added to the docker group. Use root or sudo for Docker administration."
    fi
}

trap cleanup EXIT

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
    main "$@"
fi
