"""Offline fault injector for disposable ownership tests. Never use in production."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from .service import TEST_LABEL_NAMESPACE
from .state import StateStore


def _line(path: Path) -> str:
    return path.read_text(encoding="utf-8").strip()


def _labels(run_id: str, instance: str, kind: str) -> dict[str, str]:
    return {
        f"{TEST_LABEL_NAMESPACE}.managed": "true",
        f"{TEST_LABEL_NAMESPACE}.fixture-run": run_id,
        f"{TEST_LABEL_NAMESPACE}.instance": instance,
        f"{TEST_LABEL_NAMESPACE}.resource-type": kind,
        f"{TEST_LABEL_NAMESPACE}.disposable": "true",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-file", type=Path, required=True)
    parser.add_argument("--run-id-file", type=Path, required=True)
    parser.add_argument("--image-file", type=Path, required=True)
    parser.add_argument(
        "--acknowledge-helper-stopped",
        action="store_true",
        help="required because this fixture has no cross-process state lock",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("show")
    remove = subparsers.add_parser("remove-record")
    replace = subparsers.add_parser("replace-record")
    wrong_kind = subparsers.add_parser("wrong-record-kind")
    for child in (remove, replace, wrong_kind):
        child.add_argument("instance")
        child.add_argument("kind", choices=("container", "network"))
    replace.add_argument("docker_id")
    replace.add_argument("name")
    arguments = parser.parse_args()

    if not arguments.acknowledge_helper_stopped:
        parser.error("--acknowledge-helper-stopped is required")
    run_id = _line(arguments.run_id_file)
    state = StateStore(arguments.state_file, run_id)
    if arguments.command == "show":
        print(json.dumps(state.snapshot(), sort_keys=True, indent=2))
        return 0
    if arguments.command == "remove-record":
        state.remove_resource(arguments.instance, arguments.kind)
        return 0

    labels = _labels(run_id, arguments.instance, arguments.kind)
    if arguments.command == "replace-record":
        record = {
            "kind": arguments.kind,
            "docker_id": arguments.docker_id,
            "name": arguments.name,
            "expected_labels": labels,
        }
        if arguments.kind == "container":
            record["image_digest"] = _line(arguments.image_file)
        state.put_resource(arguments.instance, arguments.kind, record)
        return 0
    if arguments.command == "wrong-record-kind":
        record = state.get_resource(arguments.instance, arguments.kind)
        if record is None:
            raise SystemExit("record not found")
        record["kind"] = "network" if arguments.kind == "container" else "container"
        state.put_resource(arguments.instance, arguments.kind, record)
        return 0
    raise AssertionError("unreachable")


if __name__ == "__main__":
    raise SystemExit(main())
