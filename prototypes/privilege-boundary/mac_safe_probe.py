#!/usr/bin/python3
"""Read-only disposable probes for a helper MAC domain or profile."""

from __future__ import annotations

import json
import socket
import subprocess


def attempt(name, action):
    try:
        action()
    except (OSError, PermissionError, subprocess.SubprocessError) as error:
        return {"probe": name, "denied": True, "error": type(error).__name__, "errno": getattr(error, "errno", None)}
    return {"probe": name, "denied": False, "error": None, "errno": None}


def main() -> int:
    def connect_ip():
        connection = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        connection.settimeout(0.2)
        try:
            connection.connect(("198.51.100.1", 9))
        finally:
            connection.close()

    probes = [
        attempt("unrelated_storage_read", lambda: open("/var/lib/kitpro-pb-unrelated/marker", "rb").read(1)),
        attempt("shell_execution", lambda: subprocess.run(["/bin/sh"], check=True)),
        attempt("ip_network_connect", connect_ip),
    ]
    print(json.dumps(probes, sort_keys=True))
    return 0 if all(item["denied"] for item in probes) else 1


if __name__ == "__main__":
    raise SystemExit(main())
