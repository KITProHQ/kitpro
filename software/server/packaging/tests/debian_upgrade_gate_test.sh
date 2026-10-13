#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/../.." && pwd)
upgrade_script="$server_dir/packaging/debian/kitpro-debian-upgrade"
work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT HUP INT TERM

command -v sqlite3 >/dev/null 2>&1 || { printf 'sqlite3 is required\n' >&2; exit 1; }

binary_root="$work_dir/binaries"
install -d "$binary_root"
(
    cd "$server_dir"
    GOCACHE=${GOCACHE:-/tmp/kitpro-go-cache} go build -trimpath -buildvcs=false -o "$binary_root/kitpro-api-real" ./cmd/kitpro-api
    GOCACHE=${GOCACHE:-/tmp/kitpro-go-cache} go build -trimpath -buildvcs=false -o "$binary_root/kitpro-helper-real" ./cmd/kitpro-helper
)

fake_systemctl="$work_dir/systemctl"
cat > "$fake_systemctl" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "${KITPRO_TEST_SERVICE_LOG:?}"
unit=${!#}
case ${1:-} in
    is-active)
        case " ${KITPRO_TEST_ACTIVE_UNITS-kitpro-api.service kitpro-helper.service kitpro-helper.socket} " in
            *" $unit "*) exit 0 ;;
            *) exit 3 ;;
        esac
        ;;
    start)
        case " ${KITPRO_TEST_FAIL_START_UNITS:-} " in
            *" $unit "*) exit 1 ;;
        esac
        ;;
esac
EOF
chmod 0755 "$fake_systemctl"

fake_runuser="$work_dir/runuser"
cat > "$fake_runuser" <<'EOF'
#!/usr/bin/env bash
set -eu
exec "$@"
EOF
chmod 0755 "$fake_runuser"

fake_apt="$work_dir/apt-get"
cat > "$fake_apt" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "${KITPRO_TEST_APT_LOG:?}"
printf '%s' "${KITPRO_TEST_TARGET_VERSION:-0.1.0~alpha12}" > "${KITPRO_TEST_INSTALLED_VERSION_FILE:?}"
cp "${KITPRO_TEST_APPROVAL_PATH:?}" "${KITPRO_TEST_APPROVAL_PATH}.consumed"
rm -f -- "${KITPRO_TEST_APPROVAL_PATH:?}"
EOF
chmod 0755 "$fake_apt"

fake_dpkg_deb="$work_dir/dpkg-deb"
cat > "$fake_dpkg_deb" <<'EOF'
#!/usr/bin/env bash
set -eu
case ${3:-} in
    Package) printf '%s\n' "${KITPRO_TEST_TARGET_NAME:-kitpro-server}" ;;
    Version) printf '%s\n' "${KITPRO_TEST_TARGET_VERSION:-0.1.0~alpha12}" ;;
    Architecture) printf '%s\n' "${KITPRO_TEST_TARGET_ARCHITECTURE:-amd64}" ;;
    *) printf 'unexpected dpkg-deb field: %s\n' "${3:-}" >&2; exit 2 ;;
esac
EOF
chmod 0755 "$fake_dpkg_deb"

fake_dpkg_query="$work_dir/dpkg-query"
cat > "$fake_dpkg_query" <<'EOF'
#!/usr/bin/env bash
set -eu
cat "${KITPRO_TEST_INSTALLED_VERSION_FILE:?}"
EOF
chmod 0755 "$fake_dpkg_query"

fake_mv="$work_dir/mv"
cat > "$fake_mv" <<'EOF'
#!/usr/bin/env bash
set -eu
destination=${!#}
if [[ ${KITPRO_TEST_FAIL_HELPER_PROMOTION:-0} == 1 && "$destination" == *'/pre-upgrade-helper-set-'* ]]; then
    exit 1
fi
if [[ ${KITPRO_TEST_FAIL_APPROVAL_PROMOTION:-0} == 1 && "$destination" == */debian-upgrade-approved ]]; then
    exit 1
fi
exec /usr/bin/mv "$@"
EOF
chmod 0755 "$fake_mv"

fake_sync="$work_dir/sync"
cat > "$fake_sync" <<'EOF'
#!/usr/bin/env bash
set -eu
exit 0
EOF
chmod 0755 "$fake_sync"

create_database() {
    local path=$1
    install -d "$(dirname -- "$path")"
    sqlite3 "$path" 'PRAGMA journal_mode=WAL; CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(7);' >/dev/null
}

configure_root() {
    local root=$1
    install -d "$root/etc/default" "$root/usr/bin" "$root/usr/libexec"
    install -m 0755 "$binary_root/kitpro-api-real" "$root/usr/bin/kitpro-api-real"
    install -m 0755 "$binary_root/kitpro-helper-real" "$root/usr/libexec/kitpro-helper-real"
    cat > "$root/usr/bin/kitpro-api" <<EOF
#!/usr/bin/env bash
set -eu
if [[ \${1:-} == --prepare-upgrade && \${KITPRO_TEST_FAIL_CONTROL_PREPARE:-0} == 1 ]]; then
    exit 1
fi
"$root/usr/bin/kitpro-api-real" "\$@"
EOF
    chmod 0755 "$root/usr/bin/kitpro-api"
    cat > "$root/usr/libexec/kitpro-helper" <<EOF
#!/usr/bin/env bash
set -eu
if [[ \${1:-} == --prepare-upgrade && \${KITPRO_TEST_FAIL_HELPER_PREPARE:-0} == 1 ]]; then
    exit 1
fi
"$root/usr/libexec/kitpro-helper-real" "\$@"
if [[ \${1:-} == --prepare-upgrade && \${KITPRO_TEST_CORRUPT_HELPER_BACKUP:-0} == 1 ]]; then
    backup=\$(find "\${KITPRO_HELPER_BACKUP_DIR:?}" -maxdepth 1 -type f -name '*.db' -print -quit)
    printf 'malformed backup\n' > "\$backup"
fi
EOF
    chmod 0755 "$root/usr/libexec/kitpro-helper"
    cat > "$root/etc/default/kitpro-server" <<EOF
KITPRO_CONTROL_DB=$root/var/lib/kitpro-api/control.db
KITPRO_HELPER_DB=$root/var/lib/kitpro-helper/helper.db
KITPRO_CONTROL_BACKUP_DIR=$root/var/lib/kitpro-api/backups
KITPRO_HELPER_BACKUP_DIR=$root/var/lib/kitpro-helper/backups
EOF
    printf '0.1.0~alpha11' > "$root/installed-version"
}

run_wrapper() {
    local root=$1 package=$2
    KITPRO_UPGRADE_TESTING=1 \
    KITPRO_TEST_ROOT="$root" \
    KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_RUNUSER="$fake_runuser" \
    KITPRO_TEST_APT_GET="$fake_apt" \
    KITPRO_TEST_DPKG_DEB="$fake_dpkg_deb" \
    KITPRO_TEST_DPKG_QUERY="$fake_dpkg_query" \
    KITPRO_TEST_MV="$fake_mv" \
    KITPRO_TEST_SYNC="$fake_sync" \
    KITPRO_TEST_SERVICE_LOG="$root/service.log" \
    KITPRO_TEST_APT_LOG="$root/apt.log" \
    KITPRO_TEST_INSTALLED_VERSION_FILE="$root/installed-version" \
    KITPRO_TEST_APPROVAL_PATH="$root/run/kitpro/debian-upgrade-approved" \
    KITPRO_TEST_EXPECTED_PACKAGE_SHA256="$(sha256sum "$package" | awk '{print $1}')" \
    KITPRO_TEST_EXPECTED_PACKAGE_VERSION=0.1.0~alpha12 \
    KITPRO_TEST_ACTIVE_UNITS="${KITPRO_TEST_ACTIVE_UNITS-kitpro-api.service kitpro-helper.service kitpro-helper.socket}" \
    KITPRO_TEST_FAIL_START_UNITS="${KITPRO_TEST_FAIL_START_UNITS:-}" \
        "$upgrade_script" "$package"
}

assert_started_units() {
    local root=$1 expected=$2 actual
    actual=$(awk '$1 == "start" {print $2}' "$root/service.log" | sort | tr '\n' ' ' | sed 's/ $//')
    test "$actual" = "$expected"
}

assert_restoration_order() {
    local root=$1 expected=$2 actual
    actual=$(awk '$1 == "start" {print $2}' "$root/service.log" | tr '\n' ' ' | sed 's/ $//')
    test "$actual" = "$expected"
}

assert_no_backup_set() {
    local root=$1
    test "$(find "$root/var/lib/kitpro-api" "$root/var/lib/kitpro-helper" -type f -name 'pre-upgrade-*-set-*.db' 2>/dev/null | wc -l)" -eq 0
    test "$(find "$root/var/lib/kitpro-api" "$root/var/lib/kitpro-helper" -type d -name '.kitpro-upgrade-*.partial' 2>/dev/null | wc -l)" -eq 0
    test ! -e "$root/run/kitpro/debian-upgrade-approved"
    test ! -e "$root/apt.log"
}

fake_package="$work_dir/kitpro-server_0.1.0~alpha12_amd64.deb"
printf 'exact package bytes\n' > "$fake_package"

# A valid alpha.11 state creates a complete pair with one set identity before
# the package manager is invoked.
valid_root="$work_dir/valid"
configure_root "$valid_root"
create_database "$valid_root/var/lib/kitpro-api/control.db"
create_database "$valid_root/var/lib/kitpro-helper/helper.db"
run_wrapper "$valid_root" "$fake_package"
test ! -e "$valid_root/run/kitpro/debian-upgrade-approved"
test -s "$valid_root/run/kitpro/debian-upgrade-approved.consumed"
grep -Fxq "install -- $fake_package" "$valid_root/apt.log"
control_backup=$(find "$valid_root/var/lib/kitpro-api/backups" -type f -name 'pre-upgrade-control-set-*.db')
helper_backup=$(find "$valid_root/var/lib/kitpro-helper/backups" -type f -name 'pre-upgrade-helper-set-*.db')
test -s "$control_backup"
test -s "$helper_backup"
control_set=${control_backup#*pre-upgrade-control-set-}
control_set=${control_set%-to-*}
helper_set=${helper_backup#*pre-upgrade-helper-set-}
helper_set=${helper_set%-to-*}
test "$control_set" = "$helper_set"
sqlite3 "$control_backup" 'PRAGMA integrity_check;' | grep -Fxq ok
sqlite3 "$helper_backup" 'PRAGMA integrity_check;' | grep -Fxq ok

# Hash mismatch is rejected before package metadata or package-manager use.
hash_root="$work_dir/hash"
configure_root "$hash_root"
create_database "$hash_root/var/lib/kitpro-api/control.db"
create_database "$hash_root/var/lib/kitpro-helper/helper.db"
if KITPRO_UPGRADE_TESTING=1 \
    KITPRO_TEST_ROOT="$hash_root" \
    KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_RUNUSER="$fake_runuser" \
    KITPRO_TEST_APT_GET="$fake_apt" \
    KITPRO_TEST_DPKG_DEB="$fake_dpkg_deb" \
    KITPRO_TEST_DPKG_QUERY="$fake_dpkg_query" \
    KITPRO_TEST_INSTALLED_VERSION_FILE="$hash_root/installed-version" \
    KITPRO_TEST_EXPECTED_PACKAGE_SHA256=0000000000000000000000000000000000000000000000000000000000000000 \
    KITPRO_TEST_EXPECTED_PACKAGE_VERSION=0.1.0~alpha12 \
        "$upgrade_script" "$fake_package" >"$hash_root/output.log" 2>&1; then
    printf 'mismatched Debian package hash passed wrapper validation\n' >&2
    exit 1
fi
grep -Fq 'package SHA-256 does not match this upgrade wrapper.' "$hash_root/output.log"
assert_no_backup_set "$hash_root"

# Package identity, target version, and architecture come from control metadata,
# never from the filename.
for metadata_case in wrong-name wrong-version wrong-architecture; do
    metadata_root="$work_dir/$metadata_case"
    configure_root "$metadata_root"
    create_database "$metadata_root/var/lib/kitpro-api/control.db"
    create_database "$metadata_root/var/lib/kitpro-helper/helper.db"
    metadata_status=0
    case $metadata_case in
        wrong-name) KITPRO_TEST_TARGET_NAME=another-package run_wrapper "$metadata_root" "$fake_package" >"$metadata_root/output.log" 2>&1 || metadata_status=$? ;;
        wrong-version) KITPRO_TEST_TARGET_VERSION=0.1.0~alpha13 run_wrapper "$metadata_root" "$fake_package" >"$metadata_root/output.log" 2>&1 || metadata_status=$? ;;
        wrong-architecture) KITPRO_TEST_TARGET_ARCHITECTURE=arm64 run_wrapper "$metadata_root" "$fake_package" >"$metadata_root/output.log" 2>&1 || metadata_status=$? ;;
    esac
    (( metadata_status != 0 )) || { printf '%s metadata passed wrapper validation\n' "$metadata_case" >&2; exit 1; }
    assert_no_backup_set "$metadata_root"
done

assert_corrupt_rejected() {
    local component=$1 root="$work_dir/corrupt-$1" database
    configure_root "$root"
    create_database "$root/var/lib/kitpro-api/control.db"
    create_database "$root/var/lib/kitpro-helper/helper.db"
    case $component in
        control) database="$root/var/lib/kitpro-api/control.db" ;;
        helper) database="$root/var/lib/kitpro-helper/helper.db" ;;
        *) return 2 ;;
    esac
    printf 'deliberately corrupt database\n' > "$database"
    local control_before helper_before
    control_before=$(sha256sum "$root/var/lib/kitpro-api/control.db" | awk '{print $1}')
    helper_before=$(sha256sum "$root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')
    if run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1; then
        printf 'corrupt %s database passed Debian wrapper preflight\n' "$component" >&2
        exit 1
    fi
    test "$control_before" = "$(sha256sum "$root/var/lib/kitpro-api/control.db" | awk '{print $1}')"
    test "$helper_before" = "$(sha256sum "$root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')"
    assert_no_backup_set "$root"
    assert_restoration_order "$root" 'kitpro-helper.socket kitpro-helper.service kitpro-api.service'
}

assert_corrupt_rejected helper
assert_corrupt_rejected control

assert_staging_failure() {
    local label=$1 root="$work_dir/$1"
    configure_root "$root"
    create_database "$root/var/lib/kitpro-api/control.db"
    create_database "$root/var/lib/kitpro-helper/helper.db"
    case $label in
        control-temp)
            failure_status=0
            KITPRO_TEST_FAIL_CONTROL_PREPARE=1 run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1 || failure_status=$?
            ;;
        helper-temp)
            failure_status=0
            KITPRO_TEST_FAIL_HELPER_PREPARE=1 run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1 || failure_status=$?
            ;;
        backup-integrity)
            failure_status=0
            KITPRO_TEST_CORRUPT_HELPER_BACKUP=1 run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1 || failure_status=$?
            ;;
        promotion)
            failure_status=0
            KITPRO_TEST_FAIL_HELPER_PROMOTION=1 run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1 || failure_status=$?
            ;;
        approval)
            failure_status=0
            KITPRO_TEST_FAIL_APPROVAL_PROMOTION=1 run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1 || failure_status=$?
            ;;
        *) return 2 ;;
    esac
    if (( failure_status == 0 )); then
        printf '%s passed Debian wrapper preflight\n' "$label" >&2
        exit 1
    fi
    assert_no_backup_set "$root"
    assert_restoration_order "$root" 'kitpro-helper.socket kitpro-helper.service kitpro-api.service'
}

assert_staging_failure control-temp
assert_staging_failure helper-temp
assert_staging_failure backup-integrity
assert_staging_failure promotion
assert_staging_failure approval

# Every failure after writers stop restores only the units that were active
# before preflight. The helper socket is restored before its dependent service.
assert_service_restoration() {
    local label=$1 active_units=$2 expected_started=$3 root="$work_dir/restore-$1"
    configure_root "$root"
    create_database "$root/var/lib/kitpro-api/control.db"
    create_database "$root/var/lib/kitpro-helper/helper.db"
    failure_status=0
    KITPRO_TEST_ACTIVE_UNITS="$active_units" KITPRO_TEST_FAIL_HELPER_PROMOTION=1 \
        run_wrapper "$root" "$fake_package" >"$root/output.log" 2>&1 || failure_status=$?
    (( failure_status != 0 )) || { printf '%s restoration case passed failed preflight\n' "$label" >&2; exit 1; }
    assert_no_backup_set "$root"
    assert_started_units "$root" "$expected_started"
    grep -Fq 'helper backup promotion failed; the incomplete control backup was removed.' "$root/output.log"
    grep -Fq 'existing state failed complete backup-set validation' "$root/output.log"
}

assert_service_restoration both-active \
    'kitpro-api.service kitpro-helper.socket' \
    'kitpro-api.service kitpro-helper.socket'
assert_restoration_order "$work_dir/restore-both-active" \
    'kitpro-helper.socket kitpro-api.service'
assert_service_restoration api-inactive \
    'kitpro-helper.socket' \
    'kitpro-helper.socket'
assert_service_restoration helper-inactive \
    'kitpro-api.service' \
    'kitpro-api.service'
assert_service_restoration both-inactive '' ''

# A failed restoration remains visible alongside the original preflight error.
restoration_failure_root="$work_dir/restoration-failure"
configure_root "$restoration_failure_root"
create_database "$restoration_failure_root/var/lib/kitpro-api/control.db"
create_database "$restoration_failure_root/var/lib/kitpro-helper/helper.db"
failure_status=0
KITPRO_TEST_ACTIVE_UNITS='kitpro-api.service kitpro-helper.socket' \
KITPRO_TEST_FAIL_START_UNITS='kitpro-helper.socket' \
KITPRO_TEST_FAIL_HELPER_PROMOTION=1 \
    run_wrapper "$restoration_failure_root" "$fake_package" >"$restoration_failure_root/output.log" 2>&1 || failure_status=$?
(( failure_status != 0 )) || { printf 'restoration failure passed failed preflight\n' >&2; exit 1; }
assert_no_backup_set "$restoration_failure_root"
grep -Fq 'helper backup promotion failed; the incomplete control backup was removed.' "$restoration_failure_root/output.log"
grep -Fq 'recovery failed to restore previously active unit: kitpro-helper.socket' "$restoration_failure_root/output.log"
grep -Fq 'upgrade recovery was incomplete' "$restoration_failure_root/output.log"
assert_started_units "$restoration_failure_root" 'kitpro-api.service kitpro-helper.socket'

# The alpha.12 postinst must return before migration on every dpkg abort path.
action_line=$(grep -n "abort-upgrade|abort-install|abort-remove|abort-deconfigure)" "$server_dir/packaging/debian/postinst.in" | cut -d: -f1)
migration_line=$(grep -n -- '--migrate-only' "$server_dir/packaging/debian/postinst.in" | head -1 | cut -d: -f1)
test "$action_line" -lt "$migration_line"
for action in abort-upgrade abort-install abort-remove abort-deconfigure; do
    grep -Fq "$action" "$server_dir/packaging/debian/postinst.in"
done

printf 'Debian fail-closed upgrade gate tests: PASS\n'
"$script_dir/debian_incoming_preinst_test.sh"
