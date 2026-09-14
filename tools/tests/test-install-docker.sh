#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$SCRIPT_DIR/../install-docker.sh"

TEST_TEMP=$(mktemp -d /tmp/kitpro-installer-tests.XXXXXX)
trap 'rm -rf -- "$TEST_TEMP"' EXIT

passed=0
failed=0

run_case() {
    local name=$1 expected=$2
    shift 2
    local actual
    if actual=$("$@" 2>&1); then
        if [[ "$expected" == "PASS" ]]; then
            passed=$((passed + 1))
        else
            printf 'FAIL %s: unexpectedly succeeded\n%s\n' "$name" "$actual"
            failed=$((failed + 1))
        fi
    else
        if [[ "$expected" == "FAIL" ]]; then
            passed=$((passed + 1))
        else
            printf 'FAIL %s: unexpectedly failed\n%s\n' "$name" "$actual"
            failed=$((failed + 1))
        fi
    fi
}

assert_output() {
    local name=$1 expected=$2
    shift 2
    local actual
    if ! actual=$("$@" 2>&1); then
        printf 'FAIL %s: command failed\n%s\n' "$name" "$actual"
        failed=$((failed + 1))
    elif [[ "$actual" != "$expected" ]]; then
        printf 'FAIL %s: expected <%s>, got <%s>\n' "$name" "$expected" "$actual"
        failed=$((failed + 1))
    else
        passed=$((passed + 1))
    fi
}

write_os_release() {
    local name=$1 id=$2 version=$3 codename=${4:-}
    printf 'ID=%q\nVERSION_ID=%q\nVERSION_CODENAME=%q\nPRETTY_NAME=%q\n' \
        "$id" "$version" "$codename" "$name" >"$TEST_TEMP/$name"
}

detect_case() {
    detect_os "$1"
    printf '%s|%s|%s|%s\n' "$OS_ID" "$OS_VERSION" "$OS_FAMILY" "$DOCKER_REPOSITORY_MEMBER"
}

resolve_without_sudo_user() {
    unset SUDO_USER
    resolve_grant_user
}

resolve_root_user() {
    export SUDO_USER=root
    resolve_grant_user
}

resolve_unsafe_user() {
    export SUDO_USER=--root
    resolve_grant_user
}

render_debian_install() {
    DRY_RUN=true
    ASSUME_YES=true
    OS_FAMILY=debian
    OS_ID=debian
    OS_DISPLAY="Debian 13"
    OS_CODENAME=trixie
    DOCKER_REPOSITORY_MEMBER=debian
    install_debian_family
}

render_rhel_install() {
    DRY_RUN=true
    ASSUME_YES=true
    OS_FAMILY=rhel
    OS_ID=rocky
    OS_DISPLAY="Rocky Linux 10"
    install_rhel_family
}

render_arch_install() {
    DRY_RUN=true
    ASSUME_YES=true
    OS_FAMILY=arch
    OS_ID=arch
    OS_DISPLAY="Arch Linux"
    install_arch_family
}

render_rhel_selinux_configuration() {
    DRY_RUN=true
    OS_FAMILY=rhel
    # Invoked indirectly when configure_rhel_selinux probes the command.
    # shellcheck disable=SC2329
    getenforce() { printf 'Enforcing\n'; }
    configure_rhel_selinux
    printf 'restart-required=%s\n' "$DOCKER_RESTART_REQUIRED"
}

assert_contains() {
    local name=$1 expected=$2
    shift 2
    local actual
    if ! actual=$("$@" 2>&1); then
        printf 'FAIL %s: command failed\n%s\n' "$name" "$actual"
        failed=$((failed + 1))
    elif [[ "$actual" != *"$expected"* ]]; then
        printf 'FAIL %s: output did not contain <%s>\n%s\n' "$name" "$expected" "$actual"
        failed=$((failed + 1))
    else
        passed=$((passed + 1))
    fi
}

write_os_release debian-13 debian 13 trixie
assert_output "Debian 13 mapping" "debian|13|debian|debian" detect_case "$TEST_TEMP/debian-13"

for version in 24.04 26.04; do
    codename=noble
    [[ "$version" == 26.04 ]] && codename=resolute
    write_os_release "ubuntu-$version" ubuntu "$version" "$codename"
    assert_output "Ubuntu $version mapping" "ubuntu|$version|debian|ubuntu" detect_case "$TEST_TEMP/ubuntu-$version"
done

for distribution in rhel rocky almalinux; do
    for version in 9 10; do
        write_os_release "$distribution-$version" "$distribution" "$version"
        assert_output "$distribution $version mapping" "$distribution|$version|rhel|rhel" detect_case "$TEST_TEMP/$distribution-$version"
    done
done

write_os_release arch-current arch rolling
assert_output "Arch mapping" "arch|rolling|arch|arch" detect_case "$TEST_TEMP/arch-current"

write_os_release debian-12 debian 12 bookworm
run_case "Debian 12 denied" FAIL detect_case "$TEST_TEMP/debian-12"
write_os_release ubuntu-22 ubuntu 22.04 jammy
run_case "Ubuntu 22.04 denied" FAIL detect_case "$TEST_TEMP/ubuntu-22"
write_os_release fedora-42 fedora 42
run_case "unknown derivative denied" FAIL detect_case "$TEST_TEMP/fedora-42"
write_os_release unsafe-codename debian 13 "bad code"
run_case "unsafe codename denied" FAIL detect_case "$TEST_TEMP/unsafe-codename"
run_case "unknown option denied" FAIL parse_arguments --not-an-option
run_case "ambiguous Docker grant denied" FAIL resolve_without_sudo_user
run_case "root Docker grant denied" FAIL resolve_root_user
run_case "unsafe Docker grant name denied" FAIL resolve_unsafe_user

FAKE_BIN="$TEST_TEMP/fake-bin"
mkdir -p "$FAKE_BIN"
printf '#!/usr/bin/env bash\nexit 0\n' >"$FAKE_BIN/apt-get"
# The generated fake must evaluate its own first argument at test time.
# shellcheck disable=SC2016
printf '#!/usr/bin/env bash\nif [[ "${1:-}" == "--print-architecture" ]]; then printf "amd64\\n"; fi\n' >"$FAKE_BIN/dpkg"
printf '#!/usr/bin/env bash\nexit 0\n' >"$FAKE_BIN/dnf"
printf '#!/usr/bin/env bash\nexit 0\n' >"$FAKE_BIN/rpm"
printf '#!/usr/bin/env bash\nexit 0\n' >"$FAKE_BIN/pacman"
chmod 0755 "$FAKE_BIN/apt-get" "$FAKE_BIN/dpkg" "$FAKE_BIN/dnf" "$FAKE_BIN/rpm" "$FAKE_BIN/pacman"
export PATH="$FAKE_BIN:$PATH"

assert_contains "Debian repository render" "URIs: https://download.docker.com/linux/debian" render_debian_install
assert_contains "Debian package render" "docker-compose-plugin" render_debian_install
assert_contains "Rocky derivative warning" "not a claim of Docker certification" render_rhel_install
assert_contains "RHEL repository render" "https://download.docker.com/linux/rhel/docker-ce.repo" render_rhel_install
assert_contains "RHEL package render" "docker-compose-plugin" render_rhel_install
assert_contains "RHEL SELinux daemon configuration render" '"selinux-enabled": true' render_rhel_selinux_configuration
assert_contains "RHEL SELinux daemon configuration requires restart" 'restart-required=true' render_rhel_selinux_configuration
assert_contains "Arch full upgrade install" 'pacman -Syu --needed --noconfirm docker docker-buildx docker-compose' render_arch_install

printf 'Installer detection tests: %d passed, %d failed\n' "$passed" "$failed"
((failed == 0))
