"""Disposable Unix-socket helper used only by the privilege-boundary fixture."""

from __future__ import annotations

import argparse
import json
import logging
import os
import pwd
import signal
import socket
import stat
import struct
import sys
import threading
import uuid
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any

from .docker_api import DIGEST_IMAGE, DockerAPI
from .protocol import (
    ProtocolError,
    encode_message,
    error_response,
    parse_request,
    read_frame,
    success_response,
)
from .safe_fs import TestStorage
from .service import BoundaryService
from .state import StateStore


LOG = logging.getLogger("kitpro-privilege-boundary-test")
SO_PEERCRED_FORMAT = "3i"
SO_PEERCRED_SIZE = struct.calcsize(SO_PEERCRED_FORMAT)
MAX_WORKERS = 8


def _audit(event: dict[str, Any]) -> None:
    LOG.info(json.dumps(event, sort_keys=True, separators=(",", ":")))


def _read_single_line(path: Path, name: str) -> str:
    info = path.stat(follow_symlinks=False)
    if not stat.S_ISREG(info.st_mode):
        raise RuntimeError(f"{name} is not a regular file")
    if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise RuntimeError(f"{name} must be private and owned by the helper identity")
    value = path.read_text(encoding="utf-8").strip()
    if not value or "\n" in value:
        raise RuntimeError(f"{name} must contain exactly one nonempty line")
    return value


def _activated_socket() -> socket.socket:
    try:
        listen_pid = int(os.environ.get("LISTEN_PID", "0"))
        listen_fds = int(os.environ.get("LISTEN_FDS", "0"))
    except ValueError as exc:
        raise RuntimeError("invalid systemd socket-activation environment") from exc
    if listen_pid != os.getpid() or listen_fds != 1:
        raise RuntimeError("exactly one systemd-activated socket is required")
    listener = socket.socket(fileno=3)
    if listener.family != socket.AF_UNIX or listener.type & socket.SOCK_STREAM == 0:
        raise RuntimeError("activated descriptor is not a Unix stream socket")
    if listener.getsockopt(socket.SOL_SOCKET, socket.SO_ACCEPTCONN) != 1:
        raise RuntimeError("activated Unix socket is not listening")
    return listener


def _standalone_socket(path: Path) -> socket.socket:
    parent = path.parent
    info = parent.stat(follow_symlinks=False)
    if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise RuntimeError("standalone socket directory must be private and owned")
    if path.exists() or path.is_symlink():
        raise RuntimeError("standalone socket path already exists")
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    listener.bind(str(path))
    os.chmod(path, 0o600)
    listener.listen(64)
    return listener


def _peer_credentials(connection: socket.socket) -> tuple[int, int, int]:
    raw = connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, SO_PEERCRED_SIZE)
    return struct.unpack(SO_PEERCRED_FORMAT, raw)


def _handle_connection(
    connection: socket.socket, service: BoundaryService, allowed_uid: int
) -> None:
    request_id: str | None = None
    operation_id: str | None = None
    peer_pid = peer_uid = peer_gid = -1
    try:
        connection.settimeout(10.0)
        peer_pid, peer_uid, peer_gid = _peer_credentials(connection)
        if peer_uid != allowed_uid:
            raise ProtocolError("UnauthorizedCaller", "socket peer is not authorized")
        payload = read_frame(connection)
        if payload is None:
            _audit(
                {
                    "event": "client_disconnected",
                    "peer_pid": peer_pid,
                    "peer_uid": peer_uid,
                    "before_request": True,
                }
            )
            return
        request = parse_request(payload)
        request_id = request.request_id
        operation_id = request.operation_id
        result = service.execute(request, peer_uid)
        connection.sendall(encode_message(success_response(request, result)))
    except ProtocolError as error:
        _audit(
            {
                "event": "request_rejected",
                "peer_pid": peer_pid,
                "peer_uid": peer_uid,
                "peer_gid": peer_gid,
                "request_id": request_id,
                "operation_id": operation_id,
                "error_code": error.code,
            }
        )
        try:
            connection.sendall(
                encode_message(error_response(error, request_id=request_id, operation_id=operation_id))
            )
        except OSError:
            pass
    except (OSError, TimeoutError):
        _audit(
            {
                "event": "client_disconnected",
                "peer_pid": peer_pid,
                "peer_uid": peer_uid,
                "request_id": request_id,
                "operation_id": operation_id,
            }
        )
    except Exception:
        LOG.exception("unexpected helper error")
        try:
            error = ProtocolError("InternalFailure", "internal helper failure")
            connection.sendall(
                encode_message(error_response(error, request_id=request_id, operation_id=operation_id))
            )
        except OSError:
            pass
    finally:
        connection.close()


def serve(listener: socket.socket, service: BoundaryService, allowed_uid: int) -> None:
    stopping = threading.Event()
    slots = threading.BoundedSemaphore(MAX_WORKERS)

    def stop(_signum: int, _frame: object) -> None:
        stopping.set()
        listener.close()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)

    def handle(connection: socket.socket) -> None:
        try:
            _handle_connection(connection, service, allowed_uid)
        finally:
            slots.release()

    with ThreadPoolExecutor(max_workers=MAX_WORKERS) as workers:
        while not stopping.is_set():
            slots.acquire()
            try:
                connection, _address = listener.accept()
            except OSError:
                slots.release()
                if stopping.is_set():
                    break
                raise
            workers.submit(handle, connection)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    socket_group = parser.add_mutually_exclusive_group(required=True)
    socket_group.add_argument("--socket-activation", action="store_true")
    socket_group.add_argument("--standalone-test-socket", type=Path)
    identity_group = parser.add_mutually_exclusive_group(required=True)
    identity_group.add_argument("--allowed-user")
    identity_group.add_argument("--allowed-uid", type=int)
    parser.add_argument("--state-file", type=Path, required=True)
    parser.add_argument("--run-id-file", type=Path, required=True)
    parser.add_argument("--image-file", type=Path, required=True)
    parser.add_argument("--storage-root", type=Path, required=True)
    parser.add_argument("--docker-socket", type=Path, default=Path("/var/run/docker.sock"))
    arguments = parser.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(message)s", stream=sys.stdout)
    if arguments.allowed_user:
        allowed_uid = pwd.getpwnam(arguments.allowed_user).pw_uid
    else:
        allowed_uid = arguments.allowed_uid
    if allowed_uid is None or allowed_uid < 0:
        raise RuntimeError("allowed UID is invalid")

    run_id = _read_single_line(arguments.run_id_file, "run ID")
    try:
        parsed_run_id = uuid.UUID(run_id)
    except ValueError as exc:
        raise RuntimeError("run ID must be a canonical UUID") from exc
    if str(parsed_run_id) != run_id.lower():
        raise RuntimeError("run ID must use canonical UUID form")
    run_id = run_id.lower()
    image_digest = _read_single_line(arguments.image_file, "image identity")
    if not DIGEST_IMAGE.fullmatch(image_digest):
        raise RuntimeError("image identity must be a fully qualified sha256 digest")

    listener = (
        _activated_socket()
        if arguments.socket_activation
        else _standalone_socket(arguments.standalone_test_socket)
    )
    state = StateStore(arguments.state_file, run_id)
    storage = TestStorage(arguments.storage_root)
    service = BoundaryService(
        docker=DockerAPI(arguments.docker_socket),
        state=state,
        storage=storage,
        image_digest=image_digest,
        run_id=run_id,
        audit=_audit,
    )
    _audit(
        {
            "event": "helper_started",
            "allowed_uid": allowed_uid,
            "run_id": run_id,
            "socket_activation": arguments.socket_activation,
        }
    )
    try:
        serve(listener, service, allowed_uid)
    finally:
        storage.close()
        if arguments.standalone_test_socket:
            try:
                arguments.standalone_test_socket.unlink()
            except FileNotFoundError:
                pass
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
