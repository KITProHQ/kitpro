"""Narrow Docker Engine API adapter for the disposable fixture."""

from __future__ import annotations

import http.client
import json
import re
import socket
import urllib.parse
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .protocol import ProtocolError


MAX_DOCKER_RESPONSE_BYTES = 2 * 1024 * 1024
DIGEST_IMAGE = re.compile(
    r"^[a-z0-9.-]+(?::[0-9]+)?/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$"
)
API_VERSION = re.compile(r"^1\.[0-9]{1,3}$")


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, socket_path: Path, timeout: float = 15.0):
        super().__init__("localhost", timeout=timeout)
        self.socket_path = socket_path

    def connect(self) -> None:
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.settimeout(self.timeout)
        connection.connect(str(self.socket_path))
        self.sock = connection


@dataclass(frozen=True)
class DockerObject:
    object_id: str
    name: str
    labels: dict[str, str]
    state: str
    networks: tuple[str, ...] = ()
    image: str = ""
    members: tuple[str, ...] = ()


class DockerAPI:
    """Only the Docker methods named here can be used by the fixture."""

    def __init__(self, socket_path: Path):
        self.socket_path = socket_path
        self._api_version: str | None = None

    def _connection(self) -> UnixHTTPConnection:
        return UnixHTTPConnection(self.socket_path)

    def _request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
        *,
        expected: frozenset[int] = frozenset({200}),
    ) -> tuple[int, dict[str, Any] | list[Any] | None]:
        encoded = None
        headers: dict[str, str] = {"Accept": "application/json"}
        if body is not None:
            encoded = json.dumps(
                body, sort_keys=True, separators=(",", ":"), ensure_ascii=True
            ).encode("utf-8")
            headers["Content-Type"] = "application/json"
        connection = self._connection()
        try:
            connection.request(method, path, body=encoded, headers=headers)
            response = connection.getresponse()
            payload = response.read(MAX_DOCKER_RESPONSE_BYTES + 1)
            if len(payload) > MAX_DOCKER_RESPONSE_BYTES:
                raise ProtocolError("DockerFailure", "Docker response exceeded limit")
            parsed: dict[str, Any] | list[Any] | None = None
            if payload:
                try:
                    parsed = json.loads(payload.decode("utf-8", errors="strict"))
                except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                    raise ProtocolError(
                        "DockerFailure", "Docker returned an invalid JSON response"
                    ) from exc
            if response.status not in expected:
                message = "Docker request failed"
                if isinstance(parsed, dict) and isinstance(parsed.get("message"), str):
                    message = parsed["message"][:300]
                code = "NotFound" if response.status == 404 else "DockerFailure"
                raise ProtocolError(code, message, retryable=response.status >= 500)
            return response.status, parsed
        except (OSError, http.client.HTTPException) as exc:
            raise ProtocolError(
                "DockerUnavailable", "Docker Engine is unavailable", retryable=True
            ) from exc
        finally:
            connection.close()

    def inspect_runtime(self) -> dict[str, Any]:
        _, payload = self._request("GET", "/version")
        if not isinstance(payload, dict):
            raise ProtocolError("DockerFailure", "Docker version response is invalid")
        api_version = payload.get("ApiVersion")
        if not isinstance(api_version, str) or not API_VERSION.fullmatch(api_version):
            raise ProtocolError("DockerIncompatible", "Docker API version is invalid")
        if payload.get("Os") != "linux":
            raise ProtocolError("DockerIncompatible", "fixture requires Linux containers")
        self._api_version = api_version
        return {
            "engine_version": _bounded_string(payload.get("Version")),
            "api_version": api_version,
            "minimum_api_version": _bounded_string(payload.get("MinAPIVersion")),
            "operating_system": _bounded_string(payload.get("Os")),
            "architecture": _bounded_string(payload.get("Arch")),
        }

    def _versioned(self, path: str) -> str:
        if self._api_version is None:
            self.inspect_runtime()
        assert self._api_version is not None
        return f"/v{self._api_version}{path}"

    def inspect_image(self, image: str) -> dict[str, Any]:
        if not DIGEST_IMAGE.fullmatch(image):
            raise ProtocolError("PolicyDenied", "approved image is not digest-pinned")
        encoded = urllib.parse.quote(image, safe="")
        _, payload = self._request(
            "GET", self._versioned(f"/images/{encoded}/json")
        )
        if not isinstance(payload, dict):
            raise ProtocolError("DockerFailure", "Docker image response is invalid")
        if payload.get("Architecture") != "amd64" or payload.get("Os") != "linux":
            raise ProtocolError("PolicyDenied", "image platform is not linux/amd64")
        return {
            "id": _bounded_string(payload.get("Id")),
            "repo_digests": [
                item[:300]
                for item in payload.get("RepoDigests", [])
                if isinstance(item, str)
            ][:20],
            "architecture": "amd64",
            "operating_system": "linux",
        }

    def list_containers(self, labels: dict[str, str]) -> list[DockerObject]:
        filters = {"label": [f"{key}={value}" for key, value in sorted(labels.items())]}
        query = urllib.parse.urlencode(
            {"all": "1", "filters": json.dumps(filters, separators=(",", ":"))}
        )
        _, payload = self._request(
            "GET", self._versioned(f"/containers/json?{query}")
        )
        if not isinstance(payload, list):
            raise ProtocolError("DockerFailure", "Docker container list is invalid")
        result: list[DockerObject] = []
        for item in payload:
            if not isinstance(item, dict):
                continue
            names = item.get("Names", [])
            name = names[0].lstrip("/") if names and isinstance(names[0], str) else ""
            result.append(
                DockerObject(
                    object_id=_bounded_string(item.get("Id")),
                    name=name[:128],
                    labels=_string_map(item.get("Labels")),
                    state=_bounded_string(item.get("State")),
                )
            )
        return result

    def list_network_members(self, network_name: str) -> tuple[str, ...]:
        if not network_name or len(network_name) > 128:
            raise ProtocolError("PolicyDenied", "network name is invalid")
        filters = {"network": [network_name]}
        query = urllib.parse.urlencode(
            {"all": "1", "filters": json.dumps(filters, separators=(",", ":"))}
        )
        _, payload = self._request(
            "GET", self._versioned(f"/containers/json?{query}")
        )
        if not isinstance(payload, list):
            raise ProtocolError("DockerFailure", "Docker network member list is invalid")
        if len(payload) > 1000:
            raise ProtocolError(
                "PolicyDenied", "Docker network member list exceeded policy limit"
            )
        members = [
            _bounded_string(item.get("Id"))
            for item in payload
            if isinstance(item, dict) and isinstance(item.get("Id"), str)
        ]
        return tuple(sorted(member for member in members if member))

    def list_networks(self, labels: dict[str, str]) -> list[DockerObject]:
        filters = {"label": [f"{key}={value}" for key, value in sorted(labels.items())]}
        query = urllib.parse.urlencode(
            {"filters": json.dumps(filters, separators=(",", ":"))}
        )
        _, payload = self._request("GET", self._versioned(f"/networks?{query}"))
        if not isinstance(payload, list):
            raise ProtocolError("DockerFailure", "Docker network list is invalid")
        return [
            DockerObject(
                object_id=_bounded_string(item.get("Id")),
                name=_bounded_string(item.get("Name")),
                labels=_string_map(item.get("Labels")),
                state="present",
            )
            for item in payload
            if isinstance(item, dict)
        ]

    def create_network(self, name: str, labels: dict[str, str]) -> DockerObject:
        body = {
            "Name": name,
            "CheckDuplicate": True,
            "Driver": "bridge",
            "Internal": True,
            "Attachable": False,
            "Ingress": False,
            "EnableIPv6": False,
            "Labels": labels,
        }
        _, payload = self._request(
            "POST",
            self._versioned("/networks/create"),
            body,
            expected=frozenset({201}),
        )
        if not isinstance(payload, dict) or not isinstance(payload.get("Id"), str):
            raise ProtocolError("DockerFailure", "Docker network create response is invalid")
        return self.inspect_network(payload["Id"])

    def inspect_network(self, object_id: str) -> DockerObject:
        encoded = urllib.parse.quote(object_id, safe="")
        _, payload = self._request(
            "GET", self._versioned(f"/networks/{encoded}")
        )
        if not isinstance(payload, dict):
            raise ProtocolError("DockerFailure", "Docker network inspect is invalid")
        members = (
            payload.get("Containers")
            if isinstance(payload.get("Containers"), dict)
            else {}
        )
        return DockerObject(
            object_id=_bounded_string(payload.get("Id")),
            name=_bounded_string(payload.get("Name")),
            labels=_string_map(payload.get("Labels")),
            state="present",
            members=tuple(sorted(str(member)[:128] for member in members)),
        )

    def remove_network(self, object_id: str) -> None:
        encoded = urllib.parse.quote(object_id, safe="")
        self._request(
            "DELETE",
            self._versioned(f"/networks/{encoded}"),
            expected=frozenset({204}),
        )

    def create_container(
        self,
        name: str,
        image: str,
        network_name: str,
        labels: dict[str, str],
    ) -> DockerObject:
        self.inspect_image(image)
        body = build_test_container_request(image, network_name, labels)
        query = urllib.parse.urlencode({"name": name})
        _, payload = self._request(
            "POST",
            self._versioned(f"/containers/create?{query}"),
            body,
            expected=frozenset({201}),
        )
        if not isinstance(payload, dict) or not isinstance(payload.get("Id"), str):
            raise ProtocolError(
                "DockerFailure", "Docker container create response is invalid"
            )
        return self.inspect_container(payload["Id"])

    def inspect_container(self, object_id: str) -> DockerObject:
        encoded = urllib.parse.quote(object_id, safe="")
        _, payload = self._request(
            "GET", self._versioned(f"/containers/{encoded}/json")
        )
        if not isinstance(payload, dict):
            raise ProtocolError("DockerFailure", "Docker container inspect is invalid")
        config = payload.get("Config") if isinstance(payload.get("Config"), dict) else {}
        state = payload.get("State") if isinstance(payload.get("State"), dict) else {}
        network_settings = (
            payload.get("NetworkSettings")
            if isinstance(payload.get("NetworkSettings"), dict)
            else {}
        )
        networks = (
            network_settings.get("Networks")
            if isinstance(network_settings.get("Networks"), dict)
            else {}
        )
        return DockerObject(
            object_id=_bounded_string(payload.get("Id")),
            name=_bounded_string(payload.get("Name")).lstrip("/")[:128],
            labels=_string_map(config.get("Labels")),
            state=_bounded_string(state.get("Status")),
            networks=tuple(sorted(str(name)[:128] for name in networks)),
            image=_bounded_string(config.get("Image")),
        )

    def start_container(self, object_id: str) -> None:
        encoded = urllib.parse.quote(object_id, safe="")
        self._request(
            "POST",
            self._versioned(f"/containers/{encoded}/start"),
            expected=frozenset({204, 304}),
        )

    def stop_container(self, object_id: str) -> None:
        encoded = urllib.parse.quote(object_id, safe="")
        self._request(
            "POST",
            self._versioned(f"/containers/{encoded}/stop?t=10"),
            expected=frozenset({204, 304}),
        )

    def remove_container(self, object_id: str) -> None:
        encoded = urllib.parse.quote(object_id, safe="")
        self._request(
            "DELETE",
            self._versioned(f"/containers/{encoded}?force=0&v=0&link=0"),
            expected=frozenset({204}),
        )


def build_test_container_request(
    image: str, network_name: str, labels: dict[str, str]
) -> dict[str, Any]:
    """Return the only container shape that this fixture can create."""
    if not DIGEST_IMAGE.fullmatch(image):
        raise ProtocolError("PolicyDenied", "approved image is not digest-pinned")
    return {
        "Image": image,
        "User": "65534:65534",
        "AttachStdin": False,
        "AttachStdout": False,
        "AttachStderr": False,
        "OpenStdin": False,
        "StdinOnce": False,
        "Tty": False,
        "Env": ["KITPRO_PRIVILEGE_BOUNDARY_TEST=1"],
        # Use a short wait interval so BusyBox PID 1 processes TERM promptly.
        # A long foreground sleep delayed the shell trap until Docker's forced
        # stop timeout and obscured graceful-stop evidence.
        "Cmd": ["sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"],
        "Labels": labels,
        "HostConfig": {
            "AutoRemove": False,
            "Binds": [],
            "CapAdd": [],
            "CapDrop": ["ALL"],
            "Devices": [],
            "NetworkMode": network_name,
            "PortBindings": {},
            "Privileged": False,
            "PublishAllPorts": False,
            "ReadonlyRootfs": True,
            "SecurityOpt": ["no-new-privileges=true"],
        },
        "NetworkingConfig": {"EndpointsConfig": {network_name: {}}},
    }


def _bounded_string(value: Any) -> str:
    return value[:300] if isinstance(value, str) else ""


def _string_map(value: Any) -> dict[str, str]:
    if not isinstance(value, dict):
        return {}
    return {
        str(key)[:128]: str(item)[:300]
        for key, item in list(value.items())[:100]
        if isinstance(key, str) and isinstance(item, str)
    }
