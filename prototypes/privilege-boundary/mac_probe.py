#!/usr/bin/python3
"""Fixed negative probes for the disposable helper MAC profiles."""

from __future__ import annotations

import json
import os
import socket
import subprocess
from pathlib import Path


def attempt(name, action):
    try:
        action()
    except (OSError, PermissionError, subprocess.SubprocessError) as error:
        return {"probe": name, "denied": True, "error": type(error).__name__, "errno": getattr(error, "errno", None)}
    return {"probe": name, "denied": False, "error": None, "errno": None}


def read_one(path):
    with Path(path).open("rb") as stream:
        stream.read(1)


def write_marker(path):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(descriptor)


def execute(path):
    subprocess.run([path], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def connect_ip():
    connection = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        connection.settimeout(0.2)
        connection.connect(("127.0.0.1", 9))
    finally:
        connection.close()


def main():
    probes = [
        attempt("read_home", lambda: read_one("/home/josh/kitpro-pb-mac-private")),
        attempt("read_ssh_path", lambda: read_one("/home/josh/.ssh/kitpro-pb-mac-private")),
        attempt("read_root", lambda: read_one("/root/kitpro-pb-mac-private")),
        attempt("read_unrelated_storage", lambda: read_one("/var/lib/kitpro-pb-unrelated/marker")),
        attempt("read_docker_state", lambda: read_one("/var/lib/docker/kitpro-pb-mac-private")),
        attempt("write_etc", lambda: write_marker("/etc/kitpro-pb-mac-denied")),
        attempt("write_root", lambda: write_marker("/root/kitpro-pb-mac-denied")),
        attempt("write_firewalld", lambda: write_marker("/etc/firewalld/kitpro-pb-mac-denied")),
        attempt("execute_shell", lambda: execute("/bin/sh")),
        attempt("execute_binary", lambda: execute("/usr/bin/id")),
        attempt("outbound_ip_socket", connect_ip),
    ]
    print(json.dumps(probes, sort_keys=True))
    return 0 if all(item["denied"] for item in probes) else 1


if __name__ == "__main__":
    raise SystemExit(main())
