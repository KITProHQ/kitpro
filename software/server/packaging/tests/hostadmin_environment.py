#!/usr/bin/env python3
"""Sanitized, read-only alpha.15 host qualification readiness checks.

All modes are observational. Guest preparation and package installation are
separate operator actions requiring the authorization recorded in the runbook.
"""

from __future__ import annotations

import argparse
import ipaddress
import json
import os
from pathlib import Path
import pwd
import re
import select
import signal
import subprocess
import sys
import time


MAX_OUTPUT = 262_144
TIMEOUT = 5
PRIVATE = tuple(ipaddress.ip_network(cidr) for cidr in (
    "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"
))


def command(argv: list[str]) -> tuple[bool, bytes]:
    """Run a fixed read-only command, never surfacing raw output or stderr."""
    executable = next((str(path) for base in ("/usr/sbin", "/usr/bin", "/sbin", "/bin")
                       if (path := Path(base) / argv[0]).is_file() and os.access(path, os.X_OK)), None)
    if executable is None:
        return False, b""
    try:
        process = subprocess.Popen(
            [executable, *argv[1:]], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, start_new_session=True,
            env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"},
        )
    except OSError:
        return False, b""
    output = bytearray()
    deadline = time.monotonic() + TIMEOUT
    try:
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False, b""
            readable, _, _ = select.select([process.stdout], [], [], remaining)
            if not readable:
                return False, b""
            chunk = os.read(process.stdout.fileno(), min(65_536, MAX_OUTPUT + 1 - len(output)))
            if not chunk:
                break
            output.extend(chunk)
            if len(output) > MAX_OUTPUT:
                return False, b""
        return process.wait(timeout=max(0.1, deadline - time.monotonic())) == 0, bytes(output)
    except (OSError, subprocess.TimeoutExpired):
        return False, b""
    finally:
        if process.poll() is None:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
        process.stdout.close()


def checked_json(raw: bytes) -> object | None:
    if len(raw) > MAX_OUTPUT:
        return None
    try:
        return json.loads(raw)
    except (UnicodeError, ValueError):
        return None


def pool_status(config: object, routes: object, docker_networks: object) -> str:
    """Classify current Docker pool without publishing any host CIDRs."""
    if not isinstance(config, dict):
        return "unavailable"
    pools = config.get("default-address-pools")
    if not isinstance(pools, list) or not pools:
        return "missing"
    if not isinstance(routes, list) or not isinstance(docker_networks, list):
        return "unverified"
    try:
        route_networks = [ipaddress.ip_network(value, strict=False) for value in routes]
        docker_networks_parsed = [network for value in docker_networks
                                  if (network := ipaddress.ip_network(value, strict=False)).version == 4]
        available = 0
        selected = []
        for item in pools:
            if not isinstance(item, dict) or type(item.get("size")) is not int:
                return "invalid"
            base = ipaddress.ip_network(item.get("base"), strict=True)
            size = item["size"]
            if base.version != 4 or not any(base.subnet_of(space) for space in PRIVATE):
                return "invalid"
            if not (base.prefixlen <= size <= 24):
                return "invalid"
            if any(base.overlaps(other) for other in selected):
                return "conflict"
            # Routes created by Docker for its own networks are expected inside
            # an active pool. Other host routes must remain outside the pool.
            if any(route.version == 4 and base.overlaps(route) and route not in docker_networks_parsed
                   for route in route_networks):
                return "conflict"
            used_children = set()
            for network in docker_networks_parsed:
                if not base.overlaps(network):
                    continue
                if not network.subnet_of(base) or network.prefixlen < size:
                    return "conflict"
                used_children.add(network if network.prefixlen == size else network.supernet(new_prefix=size))
            selected.append(base)
            available += (1 << (size - base.prefixlen)) - len(used_children)
        return "ready" if available >= 64 else "insufficient_capacity"
    except (ValueError, TypeError, KeyError):
        return "invalid"


def networks_from_routes(raw: bytes) -> list[str] | None:
    data = checked_json(raw)
    if not isinstance(data, list):
        return None
    result = []
    for entry in data:
        if not isinstance(entry, dict):
            return None
        dst = entry.get("dst")
        if dst and dst != "default":
            result.append(dst)
    return result


def networks_from_docker(raw: bytes) -> list[str] | None:
    data = checked_json(raw)
    if not isinstance(data, list):
        return None
    result = []
    for network in data:
        if not isinstance(network, dict):
            return None
        ipam = network.get("IPAM", {})
        if not isinstance(ipam, dict):
            return None
        config = ipam.get("Config")
        if config is not None and not isinstance(config, list):
            return None
        for item in config or []:
            if isinstance(item, dict) and item.get("Subnet"):
                result.append(item["Subnet"])
    return result


def os_identity() -> tuple[str, str]:
    try:
        with Path("/etc/os-release").open(encoding="utf-8") as stream:
            raw = stream.read(8192)
    except (OSError, UnicodeError):
        return "unknown", "unknown"
    values = {}
    for line in raw.splitlines():
        key, sep, value = line.partition("=")
        if sep and key in {"ID", "VERSION_ID"}:
            values[key] = value.strip().strip('"').strip("'")
    def clean(value: str) -> str:
        return value if re.fullmatch(r"[A-Za-z0-9._-]{1,32}", value) else "unknown"
    return clean(values.get("ID", "unknown")), clean(values.get("VERSION_ID", "unknown"))


def file_json(path: Path) -> object | None:
    try:
        with path.open("rb") as stream:
            raw = stream.read(MAX_OUTPUT + 1)
    except OSError:
        return None
    return checked_json(raw)


def executable_present(name: str) -> bool:
    return any((Path(base) / name).is_file() for base in ("/usr/sbin", "/usr/bin", "/sbin", "/bin"))


def state(argv: list[str]) -> str:
    ok, _ = command(argv)
    return "pass" if ok else "fail"


def preflight() -> dict[str, object]:
    distro, version = os_identity()
    if distro in {"ubuntu", "debian"}:
        package_format = "deb"
    elif distro in {"arch", "kitpro-linux"} or Path("/usr/bin/pacman").exists():
        package_format = "arch"
    else:
        package_format = "unsupported"
    route_ok, route_raw = command(["ip", "-j", "-4", "route", "show"])
    docker_ok, docker_raw = False, b""
    all_ok, ids = command(["docker", "network", "ls", "--quiet"])
    network_ids = ids.split()
    if all_ok and 0 < len(network_ids) <= 64 and all(re.fullmatch(rb"[0-9a-f]{12,64}", item) for item in network_ids):
        docker_ok, docker_raw = command(["docker", "network", "inspect", *[item.decode("ascii") for item in network_ids]])
    pool = pool_status(
        file_json(Path("/etc/docker/daemon.json")) if Path("/etc/docker/daemon.json").exists() else {},
        networks_from_routes(route_raw) if route_ok else None,
        networks_from_docker(docker_raw) if docker_ok else None,
    )
    return {
        "evidence_class": "host_probe_target_unverified",
        "mode": "preflight",
        "os_id": distro, "os_version": version,
        "architecture": os.uname().machine,
        "package_format": package_format,
        "systemd": state(["systemctl", "--version"]),
        "dbus": state(["busctl", "--system", "call", "org.freedesktop.systemd1", "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "ListUnitFiles"]),
        "docker_package": "present" if executable_present("dockerd") else "missing",
        "docker_active": state(["systemctl", "is-active", "--quiet", "docker.service"]),
        "docker_socket": "present" if any(Path(p).is_socket() for p in ("/run/docker.sock", "/var/run/docker.sock")) else "missing",
        "docker_pool": pool,
        "nginx_package": "present" if executable_present("nginx") else "missing",
        "nginx_active": state(["systemctl", "is-active", "--quiet", "nginx.service"]),
        "apparmor_enabled": state(["aa-enabled"]),
        "api_account": "present" if account_exists() else "missing",
        "api_unit": "present" if unit_exists() else "missing",
    }


def account_exists() -> bool:
    try:
        pwd.getpwnam("kitpro-api")
        return True
    except KeyError:
        return False


def unit_exists() -> bool:
    return any(Path(p).exists() for p in (
        "/usr/lib/systemd/system/kitpro-api.service",
        "/etc/systemd/system/kitpro-api.service",
        "/lib/systemd/system/kitpro-api.service",
    ))


def installed_version(package_format: str) -> str | None:
    if package_format == "deb":
        ok, raw = command(["dpkg-query", "-W", "-f=${Version}", "kitpro-server"])
        value = raw.decode("ascii", "ignore").strip() if ok else ""
    elif package_format == "arch":
        ok, raw = command(["pacman", "-Q", "kitpro-server"])
        parts = raw.decode("ascii", "ignore").split() if ok else []
        value = parts[1] if len(parts) == 2 and parts[0] == "kitpro-server" else ""
    else:
        return None
    return value if re.fullmatch(r"[0-9A-Za-z.+:~_-]{1,64}", value) else None


def installed_checks(package_format: str, expected_version: str) -> dict[str, str]:
    if os.geteuid() != 0:
        return {"runtime_identity_checks": "blocked_requires_root_on_approved_guest"}
    if not account_exists() or not unit_exists():
        return {"runtime_identity_checks": "blocked_package_not_installed"}
    if installed_version(package_format) != expected_version:
        return {"runtime_identity_checks": "blocked_package_version_mismatch"}
    # No journal content or command output is returned to the caller.
    bus = state(["runuser", "-u", "kitpro-api", "--", "busctl", "--system", "call", "org.freedesktop.systemd1", "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "ListUnitFiles"])
    journal_ok, raw = command(["runuser", "-u", "kitpro-api", "--", "journalctl", "--system", "--no-pager", "--quiet", "--output=json", "--lines=1"])
    link_ok, link_raw = command(["runuser", "-u", "kitpro-api", "--", "ip", "-json", "-details", "link", "show"])
    route_ok, route_raw = command(["runuser", "-u", "kitpro-api", "--", "ip", "-json", "route", "show", "table", "all"])
    links = checked_json(link_raw) if link_ok else None
    routes = checked_json(route_raw) if route_ok else None
    resolver = state(["runuser", "-u", "kitpro-api", "--", "test", "-r", "/etc/resolv.conf"])
    return {
        "runtime_identity_checks": "performed",
        "api_service_active": state(["systemctl", "is-active", "--quiet", "kitpro-api.service"]),
        "api_dbus": bus,
        "api_journal": "readable" if journal_ok and raw else "inconclusive" if journal_ok else "denied_or_unavailable",
        "api_network_links": "pass" if isinstance(links, list) and len(links) <= 512 else "fail",
        "api_network_routes": "pass" if isinstance(routes, list) and len(routes) <= 4096 else "fail",
        "api_resolver_config": resolver,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("preflight", "preparation", "qualification", "evidence"))
    parser.add_argument("--expected-version", help="Exact installed KITPro package version for qualification/evidence")
    args = parser.parse_args()
    if args.mode in {"qualification", "evidence"} and not args.expected_version:
        parser.error("qualification/evidence require --expected-version")
    if args.expected_version and not re.fullmatch(r"[0-9A-Za-z.+:~_-]{1,64}", args.expected_version):
        parser.error("invalid expected package version")
    result = preflight()
    result["mode"] = args.mode
    if args.mode == "preparation":
        result["action"] = "review_only_no_guest_modifications"
        result["next"] = "use documented platform procedure after target-specific approval"
    elif args.mode in {"qualification", "evidence"}:
        result.update(installed_checks(result["package_format"], args.expected_version))
        if result.get("runtime_identity_checks") == "performed":
            result["evidence_class"] = "installed_service_identity_probe"
        result["http_ui_tests"] = "separate_authenticated_browser_procedure_required"
    print(json.dumps(result, sort_keys=True))
    return 0 if args.mode in {"preflight", "preparation"} else (0 if result.get("runtime_identity_checks") == "performed" else 3)


if __name__ == "__main__":
    sys.exit(main())
