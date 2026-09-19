#!/usr/bin/env bash

# Read-only Rocky runtime acceptance with an optional disposable recovery test.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ACKNOWLEDGED=false
REQUIRE_APP=false
EXERCISE_RECOVERY=false

usage() {
    cat <<'EOF'
Usage: sudo ./validate-rocky-runtime.sh --acknowledge-disposable-vm [OPTIONS]

Options:
  --require-app         Fail unless at least one KITPro container is running.
  --exercise-recovery  Kill one KITPro container process and require systemd recovery.
  --help

The recovery option mutates a running disposable validation host. The default
run is read-only. AVCs from the last ten minutes fail either mode.
EOF
}

die() { printf 'FAIL %s\n' "$*" >&2; exit 1; }
pass() { printf 'PASS %s\n' "$*"; }
observe() { printf 'OBSERVATION %s=%s\n' "$1" "$2"; }

while (($#)); do
    case "$1" in
        --acknowledge-disposable-vm) ACKNOWLEDGED=true ;;
        --require-app) REQUIRE_APP=true ;;
        --exercise-recovery) EXERCISE_RECOVERY=true ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

[[ "$ACKNOWLEDGED" == true ]] || die "--acknowledge-disposable-vm is required"
[[ "$EUID" -eq 0 ]] || die "run as root, normally with sudo"
"$SCRIPT_DIR/verify-host.sh" --platform rocky10 --require-podman

for unit in kitpro-helper.socket kitpro-api.service; do
    systemctl is-active --quiet "$unit" || die "$unit is not active"
    pass "unit_active=$unit"
done

http_status=$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/)
case "$http_status" in
    200|302|303) pass "api_http_status=$http_status" ;;
    *) die "unexpected API HTTP status $http_status" ;;
esac

mapfile -t container_names < <(podman ps --all --filter label=com.kitpro.managed=true --format '{{.Names}}' | sort)
observe kitpro_container_count "${#container_names[@]}"
if [[ "$REQUIRE_APP" == true && ${#container_names[@]} -eq 0 ]]; then
    die "no KITPro application container is present"
fi

for name in "${container_names[@]}"; do
    state=$(podman inspect --format '{{.State.Status}}' "$name")
    [[ "$state" == "running" ]] || die "$name is $state"
    process_label=$(podman inspect --format '{{.ProcessLabel}}' "$name")
    mount_label=$(podman inspect --format '{{.MountLabel}}' "$name")
    [[ -n "$process_label" && "$process_label" == *:container_t:* ]] || die "$name lacks a confined container_t process label"
    [[ -n "$mount_label" ]] || die "$name lacks an SELinux mount label"
    unit=$name.service
    systemctl is-active --quiet "$unit" || die "$unit is not active"
    pass "container=$name state=running unit=$unit selinux=container_t"
done

mapfile -t network_names < <(podman network ls --filter label=com.kitpro.managed=true --format '{{.Name}}' | sort)
observe kitpro_network_count "${#network_names[@]}"
for network in "${network_names[@]}"; do
    podman network inspect "$network" >/dev/null
    pass "network=$network"
done

if [[ "$EXERCISE_RECOVERY" == true ]]; then
    [[ ${#container_names[@]} -gt 0 ]] || die "recovery exercise requires an installed application"
    victim=${container_names[0]}
    before=$(podman inspect --format '{{.State.Pid}}' "$victim")
    systemctl kill --kill-whom=main --signal=KILL "$victim.service"
    recovered=false
    for _ in {1..30}; do
        after=$(podman inspect --format '{{.State.Pid}}' "$victim" 2>/dev/null || true)
        if [[ -n "$after" && "$after" != 0 && "$after" != "$before" ]] && systemctl is-active --quiet "$victim.service"; then
            recovered=true
            break
        fi
        sleep 1
    done
    [[ "$recovered" == true ]] || die "$victim did not recover after process termination"
    pass "failure_recovery=$victim"
fi

if command -v ausearch >/dev/null 2>&1; then
    avc_output=$(ausearch -m AVC,USER_AVC -ts recent -i 2>/dev/null || true)
    if [[ -n "$avc_output" && "$avc_output" != *"<no matches>"* ]]; then
        printf '%s\n' "$avc_output" >&2
        die "SELinux AVCs were recorded in the recent validation window"
    fi
    pass "selinux_avc=none_recent"
else
    die "ausearch is unavailable"
fi

pass "rocky_runtime_validation=complete"
