#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/../.." && pwd)
repo_dir=$(git -C "$server_dir" rev-parse --show-toplevel)
work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT HUP INT TERM

readonly published_alpha12_commit=ada9555ab680613f8b56f6f0762abce6f0955670
alpha12_commit=$(git -C "$repo_dir" rev-parse 'refs/tags/v0.1.0-alpha.12^{commit}')
test "$alpha12_commit" = "$published_alpha12_commit"

alpha12_source="$work_dir/alpha12-source"
install -d "$alpha12_source"
git -C "$repo_dir" archive "$alpha12_commit" software/server | tar -x -C "$alpha12_source"
alpha12_server="$alpha12_source/software/server"
binary_root="$work_dir/alpha12-binaries"
install -d "$binary_root"
(
    cd "$alpha12_server"
    GOCACHE=${GOCACHE:-/var/tmp/kitpro-go-cache} go build -trimpath -buildvcs=false -o "$binary_root/kitpro-api" ./cmd/kitpro-api
    GOCACHE=${GOCACHE:-/var/tmp/kitpro-go-cache} go build -trimpath -buildvcs=false -o "$binary_root/kitpro-helper" ./cmd/kitpro-helper
)

incoming_preinst="$work_dir/preinst"
incoming_postinst="$work_dir/postinst"
sed -e 's/@VERSION@/0.1.0~alpha13/g' \
    -e "s/@SOURCE_COMMIT@/$(git -C "$repo_dir" rev-parse HEAD)/g" \
    "$server_dir/packaging/debian/preinst.in" > "$incoming_preinst"
sed -e 's/@VERSION@/0.1.0~alpha13/g' \
    -e "s/@SOURCE_COMMIT@/$(git -C "$repo_dir" rev-parse HEAD)/g" \
    "$server_dir/packaging/debian/postinst.in" > "$incoming_postinst"
chmod 0755 "$incoming_preinst" "$incoming_postinst"

fake_systemctl="$work_dir/systemctl"
cat > "$fake_systemctl" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "${KITPRO_TEST_SERVICE_LOG:?}"
command=${1:-}
shift || true
case $command in
    is-active)
        [[ ${1:-} != --quiet ]] || shift
        grep -Fxq "${1:-}" "${KITPRO_TEST_ACTIVE_STATE:?}"
        ;;
    stop)
        for unit in "$@"; do
            sed -i "/^${unit//./\\.}$/d" "${KITPRO_TEST_ACTIVE_STATE:?}"
        done
        ;;
    start)
        unit=${1:?}
        case " ${KITPRO_TEST_FAIL_START_UNITS:-} " in
            *" $unit "*) exit 1 ;;
        esac
        grep -Fxq "$unit" "${KITPRO_TEST_ACTIVE_STATE:?}" || printf '%s\n' "$unit" >> "${KITPRO_TEST_ACTIVE_STATE:?}"
        ;;
    *)
        printf 'unexpected systemctl command: %s\n' "$command" >&2
        exit 2
        ;;
esac
EOF
chmod 0755 "$fake_systemctl"

fake_sync="$work_dir/sync"
cat > "$fake_sync" <<'EOF'
#!/usr/bin/env bash
set -eu
exit 0
EOF
chmod 0755 "$fake_sync"

fake_dpkg="$work_dir/dpkg"
cat > "$fake_dpkg" <<'EOF'
#!/usr/bin/env bash
set -eu
[[ ${1:-} == --compare-versions && ${3:-} == gt ]]
old=${2:?}
new=${4:?}
old_number=${old##*alpha}
new_number=${new##*alpha}
(( old_number > new_number ))
EOF
chmod 0755 "$fake_dpkg"

configure_root() {
    local root=$1
    install -d -m 0755 "$root/etc/default" "$root/usr/bin" "$root/usr/libexec"
    install -d -m 0750 "$root/run/kitpro"
    install -d -m 0700 "$root/var/lib/kitpro-api" "$root/var/lib/kitpro-helper" "$root/var/lib/kitpro-helper/backups"
    install -m 0755 "$binary_root/kitpro-api" "$root/usr/bin/kitpro-api.real"
    install -m 0755 "$binary_root/kitpro-helper" "$root/usr/libexec/kitpro-helper.real"
    env KITPRO_CONTROL_DB="$root/var/lib/kitpro-api/control.db" "$root/usr/bin/kitpro-api.real" --migrate-only >/dev/null
    env KITPRO_HELPER_DB="$root/var/lib/kitpro-helper/helper.db" "$root/usr/libexec/kitpro-helper.real" --migrate-only >/dev/null
    cat > "$root/usr/bin/kitpro-api" <<EOF
#!/usr/bin/env bash
set -eu
printf 'api %s\n' "\$*" >> "${root}/binary.log"
[[ \${1:-} != --prepare-upgrade ]] || exit 99
exec "${root}/usr/bin/kitpro-api.real" "\$@"
EOF
    cat > "$root/usr/libexec/kitpro-helper" <<EOF
#!/usr/bin/env bash
set -eu
printf 'helper %s\n' "\$*" >> "${root}/binary.log"
[[ \${1:-} != --prepare-upgrade ]] || exit 99
exec "${root}/usr/libexec/kitpro-helper.real" "\$@"
EOF
    chmod 0755 "$root/usr/bin/kitpro-api" "$root/usr/libexec/kitpro-helper"
    cat > "$root/etc/default/kitpro-server" <<EOF
KITPRO_CONTROL_DB=$root/var/lib/kitpro-api/control.db
KITPRO_HELPER_DB=$root/var/lib/kitpro-helper/helper.db
KITPRO_HELPER_BACKUP_DIR=$root/var/lib/kitpro-helper/backups
EOF
    chmod 0644 "$root/etc/default/kitpro-server"
}

run_preinst() {
    local root=$1 old_version=${2:-0.1.0~alpha12}
    : > "$root/service.log"
    KITPRO_PREINST_TESTING=1 \
    KITPRO_TEST_ROOT="$root" \
    KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_SYNC="$fake_sync" \
    KITPRO_TEST_DPKG="$fake_dpkg" \
    KITPRO_TEST_SERVICE_LOG="$root/service.log" \
    KITPRO_TEST_ACTIVE_STATE="$root/active.state" \
    KITPRO_TEST_FAIL_POINT="${KITPRO_TEST_FAIL_POINT:-}" \
    KITPRO_TEST_FAIL_START_UNITS="${KITPRO_TEST_FAIL_START_UNITS:-}" \
        "$incoming_preinst" upgrade "$old_version"
}

initialize_active_units() {
    local root=$1 units=$2
    printf '%s' "$units" | tr ' ' '\n' > "$root/active.state"
}

assert_live_unchanged() {
    local root=$1 control_hash=$2 helper_hash=$3
    test "$control_hash" = "$(sha256sum "$root/var/lib/kitpro-api/control.db" | awk '{print $1}')"
    test "$helper_hash" = "$(sha256sum "$root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')"
}

assert_no_incomplete_state() {
    local root=$1
    test ! -e "$root/run/kitpro/debian-upgrade-approved"
    test "$(find "$root/var/lib/kitpro-helper/backups/package-transitions" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)" -eq 0
}

assert_start_order() {
    local root=$1 expected=$2 actual
    actual=$(awk '$1 == "start" {print $2}' "$root/service.log" | tr '\n' ' ' | sed 's/ $//')
    test "$actual" = "$expected"
}

# The exact published alpha.12 verification binaries are sufficient. The
# incoming control script creates the pair without an incoming payload path,
# --prepare-upgrade, or a new AppArmor profile.
success_root="$work_dir/success"
configure_root "$success_root"
initialize_active_units "$success_root" 'kitpro-api.service kitpro-helper.service kitpro-helper.socket'
control_before=$(sha256sum "$success_root/var/lib/kitpro-api/control.db" | awk '{print $1}')
helper_before=$(sha256sum "$success_root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')
run_preinst "$success_root"
approval="$success_root/run/kitpro/debian-upgrade-approved"
test "$(stat -c '%a' "$approval")" = 600
grep -Fxq 'format=2' "$approval"
grep -Fxq 'old_version=0.1.0~alpha12' "$approval"
grep -Fxq 'target_version=0.1.0~alpha13' "$approval"
set_id=$(sed -n 's/^backup_set_id=//p' "$approval")
backup_set="$success_root/var/lib/kitpro-helper/backups/package-transitions/set-$set_id-to-0.1.0~alpha13"
test "$(stat -c '%a' "$backup_set")" = 700
test "$(stat -c '%a' "$backup_set/control.db")" = 600
test "$(stat -c '%a' "$backup_set/helper.db")" = 600
env KITPRO_CONTROL_DB="$backup_set/control.db" "$success_root/usr/bin/kitpro-api.real" --verify-database >/dev/null
env KITPRO_HELPER_DB="$backup_set/helper.db" "$success_root/usr/libexec/kitpro-helper.real" --verify-database >/dev/null
assert_live_unchanged "$success_root" "$control_before" "$helper_before"
test ! -s "$success_root/active.state"
assert_start_order "$success_root" ''
test "$(grep -c -- '--verify-database' "$success_root/binary.log")" -eq 4
if grep -q -- '--prepare-upgrade' "$success_root/binary.log"; then exit 1; fi
if grep -q '/usr/libexec/kitpro-debian-upgrade' "$incoming_preinst"; then exit 1; fi
if grep -q 'apparmor_parser' "$incoming_preinst"; then exit 1; fi

# The incoming postinst accepts only the exact version/source-bound approval
# and independently verifies both promoted database sets before migration.
cp "$approval" "$success_root/approval.saved"
sed -i 's/^source_commit=.*/source_commit=0000000000000000000000000000000000000000/' "$approval"
status=0
KITPRO_POSTINST_TESTING=1 KITPRO_TEST_ROOT="$success_root" \
    "$incoming_postinst" configure 0.1.0~alpha12 >"$success_root/postinst-forged.log" 2>&1 || status=$?
(( status != 0 ))
install -m 0600 "$success_root/approval.saved" "$approval"
KITPRO_POSTINST_TESTING=1 KITPRO_TEST_ROOT="$success_root" \
    "$incoming_postinst" configure 0.1.0~alpha12

# Every safety-critical failure after writers stop restores the exact prior
# state, removes staging/final pairs, and leaves both live databases unchanged.
for point in control-verification helper-verification control-backup helper-backup control-backup-verification helper-backup-verification promotion approval; do
    root="$work_dir/fail-$point"
    configure_root "$root"
    initialize_active_units "$root" 'kitpro-api.service kitpro-helper.service kitpro-helper.socket'
    control_before=$(sha256sum "$root/var/lib/kitpro-api/control.db" | awk '{print $1}')
    helper_before=$(sha256sum "$root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')
    status=0
    KITPRO_TEST_FAIL_POINT="$point" run_preinst "$root" >"$root/output.log" 2>&1 || status=$?
    (( status != 0 )) || { printf 'incoming preflight failure point passed: %s\n' "$point" >&2; exit 1; }
    grep -Fq "injected incoming-preflight failure: $point" "$root/output.log"
    grep -Fq 'incoming package preflight failed' "$root/output.log"
    assert_live_unchanged "$root" "$control_before" "$helper_before"
    assert_no_incomplete_state "$root"
    assert_start_order "$root" 'kitpro-helper.socket kitpro-helper.service kitpro-api.service'
done

# Prior inactive states are preserved exactly.
assert_restoration_case() {
    local active=$2 expected=$3 root="$work_dir/restore-$1" status=0
    configure_root "$root"
    initialize_active_units "$root" "$active"
    KITPRO_TEST_FAIL_POINT=promotion run_preinst "$root" >"$root/output.log" 2>&1 || status=$?
    (( status != 0 ))
    assert_no_incomplete_state "$root"
    assert_start_order "$root" "$expected"
}
assert_restoration_case both-active 'kitpro-api.service kitpro-helper.socket' 'kitpro-helper.socket kitpro-api.service'
assert_restoration_case api-inactive 'kitpro-helper.socket' 'kitpro-helper.socket'
assert_restoration_case helper-inactive 'kitpro-api.service' 'kitpro-api.service'
assert_restoration_case both-inactive '' ''

# Restoration failures stay visible alongside the original error and keep a
# nonzero result.
restore_failure_root="$work_dir/restore-failure"
configure_root "$restore_failure_root"
initialize_active_units "$restore_failure_root" 'kitpro-api.service kitpro-helper.socket'
status=0
KITPRO_TEST_FAIL_POINT=promotion KITPRO_TEST_FAIL_START_UNITS=kitpro-helper.socket \
    run_preinst "$restore_failure_root" >"$restore_failure_root/output.log" 2>&1 || status=$?
(( status != 0 ))
grep -Fq 'injected incoming-preflight failure: promotion' "$restore_failure_root/output.log"
grep -Fq 'failed to restore previously active unit: kitpro-helper.socket' "$restore_failure_root/output.log"
grep -Fq 'upgrade recovery was incomplete' "$restore_failure_root/output.log"

# Unsafe path indirection is rejected before any writer is stopped.
symlink_root="$work_dir/symlink"
configure_root "$symlink_root"
initialize_active_units "$symlink_root" 'kitpro-api.service kitpro-helper.socket'
mv "$symlink_root/var/lib/kitpro-api/control.db" "$symlink_root/var/lib/kitpro-api/control.real"
ln -s control.real "$symlink_root/var/lib/kitpro-api/control.db"
status=0
run_preinst "$symlink_root" >"$symlink_root/output.log" 2>&1 || status=$?
(( status != 0 ))
grep -Fq 'database is not a regular file' "$symlink_root/output.log"
if grep -q '^stop ' "$symlink_root/service.log"; then exit 1; fi

# Fresh installs do not require old state, while downgrades remain rejected.
fresh_root="$work_dir/fresh"
install -d "$fresh_root"
KITPRO_PREINST_TESTING=1 KITPRO_TEST_ROOT="$fresh_root" KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_DPKG="$fake_dpkg" \
    KITPRO_TEST_SERVICE_LOG="$fresh_root/service.log" KITPRO_TEST_ACTIVE_STATE="$fresh_root/active.state" \
    "$incoming_preinst" install
downgrade_root="$work_dir/downgrade"
install -d "$downgrade_root"
status=0
KITPRO_PREINST_TESTING=1 KITPRO_TEST_ROOT="$downgrade_root" KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_DPKG="$fake_dpkg" \
    KITPRO_TEST_SERVICE_LOG="$downgrade_root/service.log" KITPRO_TEST_ACTIVE_STATE="$downgrade_root/active.state" \
    "$incoming_preinst" upgrade 0.1.0~alpha99 >"$downgrade_root/output.log" 2>&1 || status=$?
(( status != 0 ))
grep -Fq 'downgrade from 0.1.0~alpha99' "$downgrade_root/output.log"

printf 'Debian incoming preinst tests with published alpha.12 binaries: PASS\n'
