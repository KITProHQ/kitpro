#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
server_dir=$(cd -- "$script_dir/../.." && pwd)
upgrade_script="$server_dir/packaging/arch/kitpro-arch-upgrade"
work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT HUP INT TERM

command -v sqlite3 >/dev/null 2>&1 || { printf 'sqlite3 is required\n' >&2; exit 1; }

binary_root="$work_dir/incoming"
install -d "$binary_root/usr/bin" "$binary_root/usr/libexec"
(
    cd "$server_dir"
    GOCACHE=${GOCACHE:-/tmp/kitpro-go-cache} go build -trimpath -buildvcs=false -o "$binary_root/usr/bin/kitpro-api" ./cmd/kitpro-api
    GOCACHE=${GOCACHE:-/tmp/kitpro-go-cache} go build -trimpath -buildvcs=false -o "$binary_root/usr/libexec/kitpro-helper" ./cmd/kitpro-helper
)

fake_systemctl="$work_dir/systemctl"
cat > "$fake_systemctl" <<'EOF'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "${KITPRO_TEST_SERVICE_LOG:?}"
if [[ ${1:-} == is-active ]]; then
    exit 0
fi
EOF
chmod 0755 "$fake_systemctl"

fake_runuser="$work_dir/runuser"
cat > "$fake_runuser" <<'EOF'
#!/usr/bin/env bash
set -eu
exec "$@"
EOF
chmod 0755 "$fake_runuser"

create_schema_seven_database() {
    local path=$1
    install -d "$(dirname -- "$path")"
    sqlite3 "$path" 'PRAGMA journal_mode=WAL; CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(7);' >/dev/null
}

run_preflight() {
    local root=$1
    KITPRO_UPGRADE_TESTING=1 \
    KITPRO_TEST_ROOT="$root" \
    KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_RUNUSER="$fake_runuser" \
    KITPRO_TEST_PACMAN=/bin/false \
    KITPRO_TEST_BSDTAR=/bin/false \
    KITPRO_TEST_SERVICE_LOG="$root/service.log" \
        "$upgrade_script" --preflight-extracted "$binary_root" 0.1.0_alpha12-1 0.1.0_alpha11-1
}

configure_root() {
    local root=$1
    install -d "$root/etc/conf.d"
    cat > "$root/etc/conf.d/kitpro-server" <<EOF
KITPRO_CONTROL_DB=$root/var/lib/kitpro-api/control.db
KITPRO_HELPER_DB=$root/var/lib/kitpro-helper/helper.db
KITPRO_CONTROL_BACKUP_DIR=$root/var/lib/kitpro-api/backups
KITPRO_HELPER_BACKUP_DIR=$root/var/lib/kitpro-helper/backups
EOF
}

# A valid schema-7 installation is verified and backed up before approval.
valid_root="$work_dir/valid"
configure_root "$valid_root"
create_schema_seven_database "$valid_root/var/lib/kitpro-api/control.db"
create_schema_seven_database "$valid_root/var/lib/kitpro-helper/helper.db"
run_preflight "$valid_root"
test -s "$valid_root/run/kitpro/upgrade-approved"
test "$(find "$valid_root/var/lib/kitpro-api/backups" -type f -name 'pre-upgrade-*.db' | wc -l)" -eq 1
test "$(find "$valid_root/var/lib/kitpro-helper/backups" -type f -name 'pre-upgrade-*.db' | wc -l)" -eq 1
sqlite3 "$(find "$valid_root/var/lib/kitpro-api/backups" -type f -name 'pre-upgrade-*.db')" 'PRAGMA integrity_check;' | grep -Fxq ok
sqlite3 "$(find "$valid_root/var/lib/kitpro-helper/backups" -type f -name 'pre-upgrade-*.db')" 'PRAGMA integrity_check;' | grep -Fxq ok

# A fresh state has no databases to back up and remains eligible for install.
fresh_root="$work_dir/fresh"
configure_root "$fresh_root"
run_preflight "$fresh_root"
test -s "$fresh_root/run/kitpro/upgrade-approved"
test ! -e "$fresh_root/var/lib/kitpro-api/control.db"
test ! -e "$fresh_root/var/lib/kitpro-helper/helper.db"

# A corrupt helper database aborts before either backup is created. The
# original corrupt bytes and schema-7 control database remain unchanged.
corrupt_root="$work_dir/corrupt"
configure_root "$corrupt_root"
create_schema_seven_database "$corrupt_root/var/lib/kitpro-api/control.db"
install -d "$corrupt_root/var/lib/kitpro-helper"
printf 'deliberately corrupt helper database\n' > "$corrupt_root/var/lib/kitpro-helper/helper.db"
control_before=$(sha256sum "$corrupt_root/var/lib/kitpro-api/control.db" | awk '{print $1}')
helper_before=$(sha256sum "$corrupt_root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')
if run_preflight "$corrupt_root" >"$corrupt_root/output.log" 2>&1; then
    printf 'corrupt helper database passed upgrade preflight\n' >&2
    exit 1
fi
test "$control_before" = "$(sha256sum "$corrupt_root/var/lib/kitpro-api/control.db" | awk '{print $1}')"
test "$helper_before" = "$(sha256sum "$corrupt_root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')"
test "$(sqlite3 "$corrupt_root/var/lib/kitpro-api/control.db" 'SELECT version FROM schema_version;')" = 7
test ! -e "$corrupt_root/run/kitpro/upgrade-approved"
test ! -d "$corrupt_root/var/lib/kitpro-api/backups"
test ! -d "$corrupt_root/var/lib/kitpro-helper/backups"
grep -Fq 'KITPro upgrade aborted: existing state failed integrity/backup validation.' "$corrupt_root/output.log"
grep -Fq "$corrupt_root/var/lib/kitpro-helper/helper.db" "$corrupt_root/output.log"

# A backup destination failure aborts without promotion of a final or partial
# backup and without changing the source database.
blocked_root="$work_dir/blocked"
configure_root "$blocked_root"
create_schema_seven_database "$blocked_root/var/lib/kitpro-api/control.db"
create_schema_seven_database "$blocked_root/var/lib/kitpro-helper/helper.db"
install -d "$blocked_root/var/lib/kitpro-api"
printf 'not a directory\n' > "$blocked_root/var/lib/kitpro-api/backups"
blocked_before=$(sha256sum "$blocked_root/var/lib/kitpro-api/control.db" | awk '{print $1}')
if run_preflight "$blocked_root" >"$blocked_root/output.log" 2>&1; then
    printf 'unwritable backup destination passed upgrade preflight\n' >&2
    exit 1
fi
test "$blocked_before" = "$(sha256sum "$blocked_root/var/lib/kitpro-api/control.db" | awk '{print $1}')"
test ! -e "$blocked_root/run/kitpro/upgrade-approved"
test "$(find "$blocked_root/var/lib/kitpro-api" -maxdepth 1 -name '*.partial-*' | wc -l)" -eq 0
grep -Fq 'Control database backup failed validation' "$blocked_root/output.log"

# The bootstrap wrapper's temporary AbortOnFail hook prevents the simulated
# package transaction from changing the installed version when preflight fails.
transaction_root="$work_dir/transaction"
configure_root "$transaction_root"
create_schema_seven_database "$transaction_root/var/lib/kitpro-api/control.db"
install -d "$transaction_root/var/lib/kitpro-helper"
printf 'deliberately corrupt helper database\n' > "$transaction_root/var/lib/kitpro-helper/helper.db"
printf '0.1.0_alpha11-1\n' > "$transaction_root/package-version"
transaction_db_before=$(sha256sum "$transaction_root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')
fake_package="$work_dir/kitpro-server-0.1.0_alpha12-1-x86_64.pkg.tar.zst"
: > "$fake_package"
fake_bsdtar="$work_dir/bsdtar"
cat > "$fake_bsdtar" <<'EOF'
#!/usr/bin/env bash
set -eu
destination=
while (($#)); do
    if [[ $1 == -C ]]; then
        destination=$2
        shift 2
    else
        shift
    fi
done
test -n "$destination"
install -D -m 0755 "$KITPRO_FAKE_BINARY_ROOT/usr/bin/kitpro-api" "$destination/usr/bin/kitpro-api"
install -D -m 0755 "$KITPRO_FAKE_BINARY_ROOT/usr/libexec/kitpro-helper" "$destination/usr/libexec/kitpro-helper"
EOF
chmod 0755 "$fake_bsdtar"
fake_pacman="$work_dir/pacman"
cat > "$fake_pacman" <<'EOF'
#!/usr/bin/env bash
set -eu
case ${1:-} in
    -Q)
        printf 'kitpro-server %s\n' "$(cat "$KITPRO_FAKE_PACKAGE_VERSION")"
        ;;
    -Qp)
        if [[ ${3:-} == %n ]]; then
            printf 'kitpro-server\n'
        else
            printf '0.1.0_alpha12-1\n'
        fi
        ;;
    --hookdir)
        hook_dir=$2
        exec_line=$(sed -n 's/^Exec = //p' "$hook_dir/90-kitpro-server-bootstrap-upgrade.hook")
        read -r -a preflight_command <<< "$exec_line"
        "${preflight_command[@]}"
        printf '0.1.0_alpha12-1\n' > "$KITPRO_FAKE_PACKAGE_VERSION"
        ;;
    *)
        printf 'unexpected fake pacman arguments: %s\n' "$*" >&2
        exit 2
        ;;
esac
EOF
chmod 0755 "$fake_pacman"
if KITPRO_UPGRADE_TESTING=1 \
    KITPRO_TEST_ROOT="$transaction_root" \
    KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_RUNUSER="$fake_runuser" \
    KITPRO_TEST_PACMAN="$fake_pacman" \
    KITPRO_TEST_BSDTAR="$fake_bsdtar" \
    KITPRO_TEST_EXPECTED_PACKAGE_SHA256=0000000000000000000000000000000000000000000000000000000000000000 \
    KITPRO_TEST_SERVICE_LOG="$transaction_root/service.log" \
    KITPRO_FAKE_BINARY_ROOT="$binary_root" \
    KITPRO_FAKE_PACKAGE_VERSION="$transaction_root/package-version" \
        "$upgrade_script" "$fake_package" >"$transaction_root/hash-output.log" 2>&1; then
    printf 'mismatched package SHA-256 passed bootstrap wrapper validation\n' >&2
    exit 1
fi
grep -Fq 'package SHA-256 does not match this upgrade wrapper.' "$transaction_root/hash-output.log"
test "$(cat "$transaction_root/package-version")" = 0.1.0_alpha11-1

if KITPRO_UPGRADE_TESTING=1 \
    KITPRO_TEST_ROOT="$transaction_root" \
    KITPRO_TEST_SYSTEMCTL="$fake_systemctl" \
    KITPRO_TEST_RUNUSER="$fake_runuser" \
    KITPRO_TEST_PACMAN="$fake_pacman" \
    KITPRO_TEST_BSDTAR="$fake_bsdtar" \
    KITPRO_TEST_EXPECTED_PACKAGE_SHA256="$(sha256sum "$fake_package" | awk '{print $1}')" \
    KITPRO_TEST_SERVICE_LOG="$transaction_root/service.log" \
    KITPRO_FAKE_BINARY_ROOT="$binary_root" \
    KITPRO_FAKE_PACKAGE_VERSION="$transaction_root/package-version" \
        "$upgrade_script" "$fake_package" >"$transaction_root/output.log" 2>&1; then
    printf 'corrupt database did not abort the simulated package transaction\n' >&2
    exit 1
fi
test "$(cat "$transaction_root/package-version")" = 0.1.0_alpha11-1
test "$(sqlite3 "$transaction_root/var/lib/kitpro-api/control.db" 'SELECT version FROM schema_version;')" = 7
test "$transaction_db_before" = "$(sha256sum "$transaction_root/var/lib/kitpro-helper/helper.db" | awk '{print $1}')"
test ! -e "$transaction_root/run/kitpro/upgrade-approved"
test ! -d "$transaction_root/var/lib/kitpro-api/backups"
grep -Fq 'KITPro upgrade aborted: existing state failed integrity/backup validation.' "$transaction_root/output.log"

printf 'Arch fail-closed upgrade gate tests: PASS\n'
