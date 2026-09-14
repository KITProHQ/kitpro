"""Descriptor-relative filesystem experiment for the disposable fixture."""

from __future__ import annotations

import ctypes
import errno
import os
import platform
import stat
from dataclasses import dataclass
from pathlib import Path

from .protocol import ProtocolError, SLOT_ID


RESOLVE_NO_XDEV = 0x01
RESOLVE_NO_MAGICLINKS = 0x02
RESOLVE_NO_SYMLINKS = 0x04
RESOLVE_BENEATH = 0x08

OPENAT2_SYSCALLS = {
    "aarch64": 437,
    "x86_64": 437,
}


class OpenHow(ctypes.Structure):
    _fields_ = [
        ("flags", ctypes.c_uint64),
        ("mode", ctypes.c_uint64),
        ("resolve", ctypes.c_uint64),
    ]


@dataclass(frozen=True)
class PreparedDirectory:
    slot_id: str
    created: bool
    device: int
    inode: int
    mode: int


def _openat2(directory_fd: int, relative_path: str) -> int:
    syscall_number = OPENAT2_SYSCALLS.get(platform.machine())
    if syscall_number is None:
        raise ProtocolError("UnsupportedHost", "openat2 test supports amd64 and arm64")

    how = OpenHow(
        flags=os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC | os.O_NOFOLLOW,
        mode=0,
        resolve=(
            RESOLVE_BENEATH
            | RESOLVE_NO_MAGICLINKS
            | RESOLVE_NO_SYMLINKS
            | RESOLVE_NO_XDEV
        ),
    )
    libc = ctypes.CDLL(None, use_errno=True)
    result = libc.syscall(
        ctypes.c_long(syscall_number),
        ctypes.c_int(directory_fd),
        ctypes.c_char_p(relative_path.encode("utf-8")),
        ctypes.byref(how),
        ctypes.c_size_t(ctypes.sizeof(how)),
    )
    if result < 0:
        error_number = ctypes.get_errno()
        if error_number == errno.ENOSYS:
            raise ProtocolError(
                "UnsupportedHost",
                "openat2 is unavailable or blocked by the service sandbox",
            )
        if error_number in {errno.ELOOP, errno.EXDEV, errno.ENOTDIR}:
            raise ProtocolError(
                "ForbiddenPath", "path crossed a link or unexpected mount"
            )
        raise ProtocolError(
            "FilesystemFailure", f"safe path open failed with errno {error_number}"
        )
    return int(result)


class TestStorage:
    def __init__(self, root: Path):
        self.root = root
        self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
        root_info = self.root.stat(follow_symlinks=False)
        if not stat.S_ISDIR(root_info.st_mode):
            raise RuntimeError("test storage root is not a directory")
        if root_info.st_uid != os.geteuid():
            raise RuntimeError("test storage root has an unexpected owner")
        if stat.S_IMODE(root_info.st_mode) & 0o022:
            raise RuntimeError("test storage root is group-writable or world-writable")
        self._root_fd = os.open(
            self.root, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC | os.O_NOFOLLOW
        )

    def close(self) -> None:
        if self._root_fd >= 0:
            os.close(self._root_fd)
            self._root_fd = -1

    def prepare(self, slot_id: str) -> PreparedDirectory:
        if not isinstance(slot_id, str) or not SLOT_ID.fullmatch(slot_id):
            raise ProtocolError("InvalidIdentifier", "invalid slot_id")

        created = False
        try:
            os.mkdir(slot_id, mode=0o700, dir_fd=self._root_fd)
            created = True
        except FileExistsError:
            pass

        descriptor = _openat2(self._root_fd, slot_id)
        try:
            info = os.fstat(descriptor)
            if not stat.S_ISDIR(info.st_mode):
                raise ProtocolError("ForbiddenPath", "storage slot is not a directory")
            if info.st_uid != os.geteuid():
                raise ProtocolError("ForbiddenPath", "storage slot owner changed")
            if stat.S_IMODE(info.st_mode) & 0o077:
                os.fchmod(descriptor, 0o700)
                info = os.fstat(descriptor)
            return PreparedDirectory(
                slot_id=slot_id,
                created=created,
                device=info.st_dev,
                inode=info.st_ino,
                mode=stat.S_IMODE(info.st_mode),
            )
        finally:
            os.close(descriptor)

    def __enter__(self) -> "TestStorage":
        return self

    def __exit__(self, *_args: object) -> None:
        self.close()
