#!/usr/bin/env bash

# Run the core Docker-backed privilege fixture on an acknowledged disposable VM.
# Snapshot-based ownership fault injection remains a manual RUNBOOK step.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
FIXTURE_SOURCE="$REPO_ROOT/prototypes/privilege-boundary"
FIXTURE_DESTINATION="/opt/kitpro-privilege-boundary-test"
STATE_ROOT="/var/lib/kitpro-pb-test"
SOCKET_PATH="/run/kitpro-pb-test/helper.sock"
LABEL_NAMESPACE="invalid.kitpro.privilege-boundary-test"
API_USER="kitpro-pb-api-test"
API_GROUP="kitpro-pb-api-test"
PLATFORM=""
ACKNOWLEDGED=false
DOCKER_STOPPED_BY_SCRIPT=false

usage() {
    cat <<'EOF'
Usage: sudo ./run-privilege-fixture.sh \
  --platform debian13|rocky10 --acknowledge-disposable-vm

This runs only on an exact Debian 13 or Rocky Linux 10 disposable VM. It
creates the fixture user/group, stages test files, installs test-only systemd
units, pulls busybox:1.37.0 for digest discovery, and creates only objects in
the invalid.kitpro.privilege-boundary-test namespace.

It refuses to begin if fixture-labelled Docker resources or fixed test paths
already exist. Complete the snapshot-based disagreement tests and cleanup in
prototypes/privilege-boundary/RUNBOOK.md after this core run.
EOF
}

die() { printf 'FAIL %s\n' "$*" >&2; exit 1; }
pass() { printf 'PASS %s\n' "$*"; }
observe() { printf 'OBSERVATION %s=%s\n' "$1" "$2"; }

restore_docker_after_failure() {
    if [[ "$DOCKER_STOPPED_BY_SCRIPT" == true ]]; then
        printf 'OBSERVATION recovery=attempting to restart Docker after interrupted runner\n' >&2
        systemctl start docker.socket docker.service || true
    fi
}

trap restore_docker_after_failure EXIT

while (($#)); do
    case "$1" in
        --platform)
            (($# >= 2)) || die "--platform requires a value"
            PLATFORM=$2
            shift
            ;;
        --acknowledge-disposable-vm) ACKNOWLEDGED=true ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

case "$PLATFORM" in
    debian13|rocky10) ;;
    *) die "--platform must be debian13 or rocky10" ;;
esac
[[ "$ACKNOWLEDGED" == true ]] || die "--acknowledge-disposable-vm is required"
[[ "$EUID" -eq 0 ]] || die "run as root, normally with sudo"

"$SCRIPT_DIR/verify-host.sh" --platform "$PLATFORM" --require-docker

for required in python3 runuser systemctl systemd-analyze systemd-tmpfiles docker install getent; do
    command -v "$required" >/dev/null 2>&1 || die "required command not found: $required"
done

mapfile -t existing_containers < <(docker ps -aq --filter "label=$LABEL_NAMESPACE.managed=true")
mapfile -t existing_networks < <(docker network ls -q --filter "label=$LABEL_NAMESPACE.managed=true")
((${#existing_containers[@]} == 0)) || die "fixture-labelled containers already exist; inspect and restore the VM snapshot"
((${#existing_networks[@]} == 0)) || die "fixture-labelled networks already exist; inspect and restore the VM snapshot"
[[ ! -e "$FIXTURE_DESTINATION" && ! -e "$STATE_ROOT" ]] || die "fixed fixture paths already exist; inspect and restore the VM snapshot"
[[ ! -e /etc/systemd/system/kitpro-pb-test.service && ! -e /usr/local/lib/systemd/system/kitpro-pb-test.service ]] || die "fixture unit already exists"

getent group "$API_GROUP" >/dev/null && die "test group already exists; use a clean snapshot"
getent passwd "$API_USER" >/dev/null && die "test user already exists; use a clean snapshot"

groupadd --system "$API_GROUP"
nologin_shell=$(command -v nologin)
useradd --system --gid "$API_GROUP" --home-dir /nonexistent --shell "$nologin_shell" "$API_USER"
pass "test_identity_created"

install -d -o root -g root -m 0755 "$FIXTURE_DESTINATION/fixture" "$FIXTURE_DESTINATION/tests"
for source_file in "$FIXTURE_SOURCE"/fixture/*.py; do
    install -o root -g root -m 0644 "$source_file" "$FIXTURE_DESTINATION/fixture/"
done
for source_file in "$FIXTURE_SOURCE"/tests/*.py; do
    install -o root -g root -m 0644 "$source_file" "$FIXTURE_DESTINATION/tests/"
done
install -o root -g root -m 0644 "$FIXTURE_SOURCE/README.md" "$FIXTURE_DESTINATION/README.md"
install -o root -g root -m 0644 "$FIXTURE_SOURCE/PROTOCOL.md" "$FIXTURE_DESTINATION/PROTOCOL.md"
install -d -o root -g root -m 0700 "$STATE_ROOT"
python3 -c 'import uuid; print(uuid.uuid4())' >"$STATE_ROOT/run-id.pending"
install -o root -g root -m 0600 "$STATE_ROOT/run-id.pending" "$STATE_ROOT/run-id"
rm -f -- "$STATE_ROOT/run-id.pending"
run_id=$(<"$STATE_ROOT/run-id")
observe fixture_run_id "$run_id"

discovery_image="docker.io/library/busybox:1.37.0"
docker pull --platform linux/amd64 "$discovery_image"
mapfile -t repo_digests < <(docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$discovery_image")
selected_image=""
for candidate in "${repo_digests[@]}"; do
    case "$candidate" in
        docker.io/library/busybox@sha256:*) selected_image=$candidate; break ;;
        busybox@sha256:*) selected_image="docker.io/library/$candidate"; break ;;
    esac
done
[[ "$selected_image" =~ ^docker\.io/library/busybox@sha256:[0-9a-f]{64}$ ]] || die "could not resolve a fully qualified busybox digest"
printf '%s\n' "$selected_image" >"$STATE_ROOT/image.pending"
install -o root -g root -m 0600 "$STATE_ROOT/image.pending" "$STATE_ROOT/image"
rm -f -- "$STATE_ROOT/image.pending"
observe image_discovery_tag "$discovery_image"
observe deployed_image "$selected_image"
docker image inspect --format 'OBSERVATION image_platform={{.Os}}/{{.Architecture}} image_id={{.Id}}' "$selected_image"

install -d -o root -g root -m 0755 /usr/local/lib/systemd/system
install -o root -g root -m 0644 "$FIXTURE_SOURCE/systemd/kitpro-pb-test.socket" /usr/local/lib/systemd/system/
install -o root -g root -m 0644 "$FIXTURE_SOURCE/systemd/kitpro-pb-test.service" /usr/local/lib/systemd/system/
install -d -o root -g root -m 0755 /usr/local/lib/tmpfiles.d
install -o root -g root -m 0644 "$FIXTURE_SOURCE/systemd/kitpro-pb-test.tmpfiles" /usr/local/lib/tmpfiles.d/kitpro-pb-test.conf
systemd-analyze verify /usr/local/lib/systemd/system/kitpro-pb-test.socket /usr/local/lib/systemd/system/kitpro-pb-test.service
systemd-tmpfiles --create /usr/local/lib/tmpfiles.d/kitpro-pb-test.conf
systemctl daemon-reload
systemctl enable --now kitpro-pb-test.socket
pass "systemd_socket_enabled"

stat -c 'OBSERVATION helper_path=%A mode=%a owner=%U group=%G path=%n' /run/kitpro-pb-test "$SOCKET_PATH"
socket_mode=$(stat -c %a "$SOCKET_PATH")
socket_owner=$(stat -c %U "$SOCKET_PATH")
socket_group=$(stat -c %G "$SOCKET_PATH")
[[ "$socket_mode" == "660" && "$socket_owner" == "root" && "$socket_group" == "$API_GROUP" ]] || die "helper socket ownership or mode is wrong"
pass "helper_socket_permissions"

id "$API_USER"
[[ $(id -u "$API_USER") -ne 0 ]] || die "API test identity unexpectedly has UID 0"
for privileged_group in docker sudo wheel; do
    if getent group "$privileged_group" >/dev/null && id -nG "$API_USER" | tr ' ' '\n' | grep -Fxq "$privileged_group"; then
        die "API test identity unexpectedly belongs to $privileged_group"
    fi
done
runuser -u "$API_USER" -- sh -c 'test ! -w /etc/passwd && test ! -w /root'
pass "api_identity_has_no_root_file_or_admin_group_authority"
if runuser -u "$API_USER" -- python3 -c 'import socket; s=socket.socket(socket.AF_UNIX); s.connect("/var/run/docker.sock")'; then
    die "API test identity connected directly to Docker"
else
    pass "api_identity_denied_docker_socket"
fi
docker version >/dev/null
pass "root_can_reach_docker"

cd "$FIXTURE_DESTINATION"
root_peer_output=$(mktemp /tmp/kitpro-pb-root-peer.XXXXXX)
if python3 -m fixture.client --socket "$SOCKET_PATH" ping >"$root_peer_output" 2>&1; then
    cat "$root_peer_output"
    rm -f -- "$root_peer_output"
    die "helper accepted root instead of the fixed API identity"
elif grep -Fq 'UnauthorizedCaller' "$root_peer_output"; then
    cat "$root_peer_output"
    rm -f -- "$root_peer_output"
    pass "so_peercred_rejected_wrong_uid_after_socket_access"
else
    cat "$root_peer_output"
    rm -f -- "$root_peer_output"
    die "root peer failed without the expected UnauthorizedCaller result"
fi

if getent passwd nobody >/dev/null && ! id -nG nobody | tr ' ' '\n' | grep -Fxq docker; then
    if runuser -u nobody -- python3 -c 'import socket; s=socket.socket(socket.AF_UNIX); s.connect("/var/run/docker.sock")'; then
        die "ordinary nobody identity connected directly to Docker"
    else
        pass "ordinary_identity_denied_docker_socket"
    fi
else
    observe ordinary_user_docker_test "BLOCKED: no suitable nobody identity"
fi

if getent passwd nobody >/dev/null && ! id -nG nobody | tr ' ' '\n' | grep -Fxq "$API_GROUP"; then
    if runuser -u nobody -- python3 -m fixture.client --socket "$SOCKET_PATH" ping; then
        die "unrelated nobody identity reached the helper"
    else
        pass "unrelated_identity_denied_helper_socket"
    fi
else
    observe unauthorized_user_test "BLOCKED: no suitable nobody identity"
fi

api_client() {
    runuser -u "$API_USER" -- python3 -m fixture.client --socket "$SOCKET_PATH" "$@"
}

ping_output=$(mktemp /tmp/kitpro-pb-ping.XXXXXX)
api_client ping >"$ping_output"
cat "$ping_output"
expected_uid=$(id -u "$API_USER")
python3 - "$ping_output" "$expected_uid" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    response = json.load(handle)
assert response["status"] == "ok"
assert response["result"]["authenticated_peer_uid"] == int(sys.argv[2])
assert response["result"]["human_authorization_verified"] is False
PY
rm -f -- "$ping_output"
pass "so_peercred_matches_api_identity"

api_client inspect-runtime
pass "helper_can_reach_docker_engine_api"
python3 -m unittest discover -s tests -v
negative_output=$(mktemp /tmp/kitpro-pb-negative.XXXXXX)
runuser -u "$API_USER" -- python3 -m fixture.negative_probe --socket "$SOCKET_PATH" >"$negative_output"
cat "$negative_output"
rm -f -- "$negative_output"
pass "malformed_oversized_and_forbidden_requests_rejected"

api_client prepare-directory data-slot
directory_mode=$(stat -c %a "$STATE_ROOT/storage/data-slot")
directory_owner=$(stat -c %U "$STATE_ROOT/storage/data-slot")
[[ "$directory_mode" == "700" && "$directory_owner" == "root" ]] || die "prepared test directory has unexpected ownership or mode"
pass "descriptor_relative_directory_prepared_within_fixed_root"

api_client create demo
api_client start demo
mapfile -t managed_container_ids < <(docker ps -aq \
    --filter "label=$LABEL_NAMESPACE.managed=true" \
    --filter "label=$LABEL_NAMESPACE.instance=demo" \
    --filter "label=$LABEL_NAMESPACE.fixture-run=$run_id")
mapfile -t managed_network_ids < <(docker network ls -q \
    --filter "label=$LABEL_NAMESPACE.managed=true" \
    --filter "label=$LABEL_NAMESPACE.instance=demo" \
    --filter "label=$LABEL_NAMESPACE.fixture-run=$run_id")
((${#managed_container_ids[@]} == 1)) || die "expected exactly one managed test container"
((${#managed_network_ids[@]} == 1)) || die "expected exactly one managed test network"
container_id=${managed_container_ids[0]}
network_id=${managed_network_ids[0]}

inspect_file=$(mktemp /tmp/kitpro-pb-inspect.XXXXXX)
network_file=$(mktemp /tmp/kitpro-pb-network.XXXXXX)
docker inspect "$container_id" >"$inspect_file"
docker network inspect "$network_id" >"$network_file"
python3 - "$inspect_file" "$network_file" "$selected_image" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    container = json.load(handle)[0]
with open(sys.argv[2], encoding="utf-8") as handle:
    network = json.load(handle)[0]
expected_image = sys.argv[3]
assert container["Config"]["Image"] == expected_image
assert container["Config"]["User"] == "65534:65534"
assert container["HostConfig"]["ReadonlyRootfs"] is True
assert container["HostConfig"]["CapDrop"] == ["ALL"]
security_options = container["HostConfig"]["SecurityOpt"] or []
assert any(
    option in {"no-new-privileges", "no-new-privileges=true"}
    for option in security_options
)
assert not container["HostConfig"].get("Binds")
assert not container["HostConfig"].get("Devices")
assert not container["HostConfig"].get("PortBindings")
assert not container.get("Mounts")
assert not container["NetworkSettings"]["Ports"]
assert network["Internal"] is True
assert network["Attachable"] is False
assert network["Ingress"] is False
PY
rm -f -- "$inspect_file" "$network_file"
pass "fixed_container_shape_digest_and_internal_network"
docker port "$container_id"
pass "no_published_host_ports"

if [[ "$PLATFORM" == "rocky10" ]]; then
    [[ $(getenforce) == "Enforcing" ]] || die "SELinux did not remain Enforcing"
    docker_security_options=$(docker info --format '{{json .SecurityOptions}}')
    observe docker_security_options "$docker_security_options"
    [[ "$docker_security_options" == *'"name=selinux"'* ]] || \
        die "Docker SELinux integration is not enabled; Enforcing host mode alone is insufficient"
    getenforce
    # ps -Z is required here because pgrep does not report SELinux contexts.
    # shellcheck disable=SC2009
    ps -eZ | grep kitpro-pb-test || true
    ls -ldZ "$FIXTURE_DESTINATION" "$STATE_ROOT" /run/kitpro-pb-test
    ls -lZ "$SOCKET_PATH" /var/run/docker.sock
    container_process_label=$(docker inspect --format '{{.ProcessLabel}}' "$container_id")
    container_mount_label=$(docker inspect --format '{{.MountLabel}}' "$container_id")
    observe container_process_label "$container_process_label"
    observe container_mount_label "$container_mount_label"
    [[ -n "$container_process_label" && -n "$container_mount_label" ]] || \
        die "Docker created a container without SELinux process or mount labels"
    container_pid=$(docker inspect --format '{{.State.Pid}}' "$container_id")
    host_process_label=$(ps -p "$container_pid" -o label=)
    observe container_host_process_label "$host_process_label"
    [[ "$host_process_label" != *":spc_t:"* ]] || \
        die "Docker created an unconfined spc_t container"
    ausearch -m AVC,USER_AVC -ts recent || true
    pass "selinux_live_container_confinement_verified"
fi

unrelated_name="kitpro-pb-unrelated-${run_id:0:8}"
unrelated_id=$(docker create --name "$unrelated_name" --network none "$selected_image")
if api_client stop unrelated; then
    die "helper accepted an unrelated semantic instance"
else
    pass "unrelated_container_rejected"
fi
unrelated_status=$(docker inspect --format '{{.State.Status}}' "$unrelated_id")
[[ "$unrelated_status" == "created" ]] || die "unrelated container changed state"
pass "unrelated_container_unchanged"

operation_id=$(python3 -c 'import uuid; print(uuid.uuid4())')
api_client --operation-id "$operation_id" ping
api_client --operation-id "$operation_id" ping
systemctl restart kitpro-pb-test.service
api_client --operation-id "$operation_id" ping
if api_client --operation-id "$operation_id" inspect-runtime; then
    die "duplicate operation ID with different semantics was accepted"
else
    pass "duplicate_operation_conflict"
fi
pass "operation_receipt_survived_helper_restart"

api_client stop demo
DOCKER_STOPPED_BY_SCRIPT=true
systemctl stop docker.service docker.socket
if api_client inspect-runtime; then
    systemctl start docker.socket docker.service
    die "helper reported Docker available while service and socket were stopped"
else
    pass "docker_outage_returned_bounded_failure"
fi
systemctl start docker.socket docker.service
systemctl is-active --quiet docker.service || die "Docker did not recover"
DOCKER_STOPPED_BY_SCRIPT=false
api_client inspect-runtime
pass "helper_recovered_after_docker_restart"

api_client remove demo
docker rm "$unrelated_id"
mapfile -t remaining_containers < <(docker ps -aq --filter "label=$LABEL_NAMESPACE.managed=true")
mapfile -t remaining_networks < <(docker network ls -q --filter "label=$LABEL_NAMESPACE.managed=true")
((${#remaining_containers[@]} == 0)) || die "managed test containers remain"
((${#remaining_networks[@]} == 0)) || die "managed test networks remain"
pass "core_fixture_resources_removed"

if [[ "$PLATFORM" == "rocky10" ]]; then
    [[ $(getenforce) == "Enforcing" ]] || die "SELinux did not remain Enforcing"
    getenforce
    # ps -Z is required here because pgrep does not report SELinux contexts.
    # shellcheck disable=SC2009
    ps -eZ | grep kitpro-pb-test || true
    ls -ldZ "$FIXTURE_DESTINATION" "$STATE_ROOT" /run/kitpro-pb-test
    ls -lZ "$SOCKET_PATH" /var/run/docker.sock
    ausearch -m AVC,USER_AVC -ts recent || true
    pass "selinux_remained_enforcing"
fi

printf '\nCore run complete. Snapshot-based ownership disagreement, mount-boundary,\n'
printf 'high-frequency race, interrupted-operation, and final SELinux review remain\n'
printf 'manual RUNBOOK steps. Revert the clean VM snapshot after evidence capture.\n'
