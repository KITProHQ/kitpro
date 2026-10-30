from __future__ import annotations

import copy
import json
import os
import tempfile
import threading
import time
import unittest
import uuid
from pathlib import Path

from fixture.docker_api import DockerObject, build_test_container_request
from fixture.protocol import ProtocolError, parse_request
from fixture.safe_fs import TestStorage
from fixture.service import BoundaryService, TEST_LABEL_NAMESPACE
from fixture.state import StateStore


IMAGE = "docker.io/library/busybox@sha256:" + "a" * 64
RUN_ID = "12345678-1234-4234-9234-123456789abc"


class FakeDocker:
    def __init__(self):
        self.containers: dict[str, DockerObject] = {}
        self.networks: dict[str, DockerObject] = {}
        self.counter = 0
        self.last_container_request = None

    def _id(self, prefix: str) -> str:
        self.counter += 1
        return f"{prefix}-{self.counter}"

    def inspect_runtime(self):
        return {"engine_version": "test", "api_version": "1.99"}

    def list_containers(self, labels):
        return [item for item in self.containers.values() if _matches(item, labels)]

    def list_network_members(self, network_name):
        return tuple(
            sorted(
                item.object_id
                for item in self.containers.values()
                if network_name in item.networks
            )
        )

    def list_networks(self, labels):
        return [item for item in self.networks.values() if _matches(item, labels)]

    def create_network(self, name, labels):
        resource = DockerObject(self._id("net"), name, copy.deepcopy(labels), "present")
        self.networks[resource.object_id] = resource
        return resource

    def inspect_network(self, object_id):
        try:
            return self.networks[object_id]
        except KeyError as exc:
            raise ProtocolError("NotFound", "network not found") from exc

    def remove_network(self, object_id):
        if object_id not in self.networks:
            raise ProtocolError("NotFound", "network not found")
        del self.networks[object_id]

    def create_container(self, name, image, network_name, labels):
        self.last_container_request = build_test_container_request(
            image, network_name, labels
        )
        resource = DockerObject(
            self._id("ctr"), name, copy.deepcopy(labels), "created", (network_name,), image
        )
        self.containers[resource.object_id] = resource
        return resource

    def inspect_container(self, object_id):
        try:
            return self.containers[object_id]
        except KeyError as exc:
            raise ProtocolError("NotFound", "container not found") from exc

    def start_container(self, object_id):
        current = self.inspect_container(object_id)
        self.containers[object_id] = _replace(current, state="running")
        for network_id, network in self.networks.items():
            if network.name in current.networks:
                self.networks[network_id] = _replace(
                    network, members=(current.object_id,)
                )

    def stop_container(self, object_id):
        current = self.inspect_container(object_id)
        self.containers[object_id] = _replace(current, state="exited")
        for network_id, network in self.networks.items():
            self.networks[network_id] = _replace(
                network,
                members=tuple(member for member in network.members if member != object_id),
            )

    def remove_container(self, object_id):
        if object_id not in self.containers:
            raise ProtocolError("NotFound", "container not found")
        del self.containers[object_id]
        for network_id, network in self.networks.items():
            self.networks[network_id] = _replace(
                network,
                members=tuple(member for member in network.members if member != object_id),
            )


def _matches(item, labels):
    return all(item.labels.get(key) == value for key, value in labels.items())


def _replace(resource, **changes):
    values = {
        "object_id": resource.object_id,
        "name": resource.name,
        "labels": resource.labels,
        "state": resource.state,
        "networks": resource.networks,
        "image": resource.image,
        "members": resource.members,
    }
    values.update(changes)
    return DockerObject(**values)


def request(operation, parameters, operation_id=None):
    return parse_request(
        json.dumps(
            {
                "protocol_major": 1,
                "protocol_minor": 0,
                "request_id": str(uuid.uuid4()),
                "operation_id": operation_id or str(uuid.uuid4()),
                "operation_kind": operation,
                "operation_revision": 1,
                "expires_at": int(time.time()) + 60,
                "parameters": parameters,
            }
        ).encode()
    )


class ServiceTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        root = Path(self.temporary.name)
        os.chmod(root, 0o700)
        self.state = StateStore(root / "state" / "state.json", RUN_ID)
        self.storage = TestStorage(root / "storage")
        self.docker = FakeDocker()
        self.audit = []
        self.service = BoundaryService(
            docker=self.docker,
            state=self.state,
            storage=self.storage,
            image_digest=IMAGE,
            run_id=RUN_ID,
            audit=self.audit.append,
        )

    def tearDown(self):
        self.storage.close()
        self.temporary.cleanup()

    def execute(self, operation, instance="demo", operation_id=None):
        return self.service.execute(
            request(operation, {"instance_id": instance}, operation_id), os.getuid()
        )

    def test_full_lifecycle_uses_fixed_safe_container_shape(self):
        created = self.execute("CreateTestContainer")
        self.assertEqual(created["container"]["state"], "created")
        config = self.docker.last_container_request
        self.assertFalse(config["HostConfig"]["Privileged"])
        self.assertEqual(config["HostConfig"]["NetworkMode"], created["network"]["name"])
        self.assertEqual(config["HostConfig"]["Binds"], [])
        self.assertNotIn("PidMode", config["HostConfig"])
        self.assertNotIn("IpcMode", config["HostConfig"])
        self.assertEqual(config["HostConfig"]["CapAdd"], [])
        self.assertEqual(config["HostConfig"]["CapDrop"], ["ALL"])
        self.assertEqual(config["HostConfig"]["Devices"], [])
        self.assertNotIn("Sysctls", config["HostConfig"])
        self.assertEqual(config["HostConfig"]["SecurityOpt"], ["no-new-privileges=true"])
        self.assertEqual(
            config["Cmd"],
            ["sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"],
        )
        self.assertNotIn("ExposedPorts", config)
        self.assertEqual(self.execute("StartTestContainer")["container"]["state"], "running")
        self.assertEqual(self.execute("StopTestContainer")["container"]["state"], "exited")
        removed = self.execute("RemoveTestContainer")
        self.assertTrue(removed["removed"])
        self.assertEqual(self.docker.containers, {})
        self.assertEqual(self.docker.networks, {})

    def test_same_operation_id_replays_and_changed_content_conflicts(self):
        operation_id = str(uuid.uuid4())
        first = self.execute("CreateTestContainer", operation_id=operation_id)
        second = self.execute("CreateTestContainer", operation_id=operation_id)
        self.assertEqual(first, second)
        self.assertEqual(len(self.docker.containers), 1)
        with self.assertRaises(ProtocolError) as caught:
            self.execute("CreateTestContainer", "different", operation_id)
        self.assertEqual(caught.exception.code, "OperationConflict")

    def test_concurrent_mutations_do_not_create_duplicate_resources(self):
        results = []

        def create():
            results.append(self.execute("CreateTestContainer"))

        threads = [threading.Thread(target=create) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(len(results), 8)
        self.assertEqual(len(self.docker.containers), 1)
        self.assertEqual(len(self.docker.networks), 1)

    def test_unrelated_container_cannot_be_named_or_operated_on(self):
        foreign = DockerObject("foreign-id", "manual-unrelated", {}, "running")
        self.docker.containers[foreign.object_id] = foreign
        with self.assertRaises(ProtocolError) as caught:
            self.execute("StopTestContainer", "manual-unrelated")
        self.assertEqual(caught.exception.code, "OwnershipUnproven")
        self.assertEqual(self.docker.containers[foreign.object_id].state, "running")

    def test_label_only_container_fails_closed(self):
        labels = self.service._labels("demo", "container")
        foreign = DockerObject("label-only", self.service._name("demo", "container"), labels, "created", (), IMAGE)
        self.docker.containers[foreign.object_id] = foreign
        with self.assertRaises(ProtocolError) as caught:
            self.execute("CreateTestContainer")
        self.assertEqual(caught.exception.code, "OwnershipConflict")

    def test_record_only_unknown_object_fails_closed(self):
        labels = self.service._labels("demo", "container")
        self.state.put_resource(
            "demo",
            "container",
            {
                "kind": "container",
                "docker_id": "missing",
                "name": self.service._name("demo", "container"),
                "expected_labels": labels,
                "image_digest": IMAGE,
            },
        )
        with self.assertRaises(ProtocolError) as caught:
            self.execute("InspectTestContainer")
        self.assertIn(caught.exception.code, {"OwnershipConflict", "OwnershipUnproven"})

    def test_missing_label_wrong_instance_and_wrong_resource_type_fail_closed(self):
        self.execute("CreateTestContainer")
        record = self.state.get_resource("demo", "container")
        object_id = record["docker_id"]
        original = self.docker.containers[object_id]
        cases = (
            {key: value for key, value in original.labels.items() if not key.endswith(".managed")},
            {**original.labels, f"{TEST_LABEL_NAMESPACE}.instance": "other"},
            {**original.labels, f"{TEST_LABEL_NAMESPACE}.resource-type": "network"},
        )
        for index, labels in enumerate(cases):
            with self.subTest(index=index):
                self.docker.containers[object_id] = _replace(original, labels=labels)
                with self.assertRaises(ProtocolError) as caught:
                    self.execute("InspectTestContainer")
                self.assertEqual(caught.exception.code, "OwnershipUnproven")
        self.docker.containers[object_id] = original

    def test_wrong_record_kind_and_incorrect_instance_id_fail_closed(self):
        self.execute("CreateTestContainer")
        record = self.state.get_resource("demo", "container")
        record["kind"] = "network"
        self.state.put_resource("demo", "container", record)
        with self.assertRaises(ProtocolError) as caught:
            self.execute("StopTestContainer")
        self.assertEqual(caught.exception.code, "OwnershipUnproven")
        with self.assertRaises(ProtocolError):
            self.execute("StopTestContainer", "other")

    def test_extra_network_attachment_fails_policy(self):
        self.execute("CreateTestContainer")
        record = self.state.get_resource("demo", "container")
        current = self.docker.containers[record["docker_id"]]
        self.docker.containers[current.object_id] = _replace(
            current, networks=tuple(sorted((*current.networks, "manual-network")))
        )
        with self.assertRaises(ProtocolError) as caught:
            self.execute("StopTestContainer")
        self.assertEqual(caught.exception.code, "PolicyDenied")
        self.assertEqual(self.docker.containers[current.object_id].state, "created")

    def test_foreign_member_on_application_network_fails_policy(self):
        self.execute("CreateTestContainer")
        record = self.state.get_resource("demo", "network")
        network = self.docker.networks[record["docker_id"]]
        self.docker.containers["foreign-container-id"] = DockerObject(
            "foreign-container-id",
            "foreign-stopped-container",
            {},
            "exited",
            (network.name,),
        )
        with self.assertRaises(ProtocolError) as caught:
            self.execute("StopTestContainer")
        self.assertEqual(caught.exception.code, "PolicyDenied")

    def test_remove_verifies_every_resource_before_deleting_any(self):
        self.execute("CreateTestContainer")
        network_record = self.state.get_resource("demo", "network")
        network = self.docker.networks[network_record["docker_id"]]
        self.docker.networks[network.object_id] = _replace(network, labels={})
        with self.assertRaises(ProtocolError):
            self.execute("RemoveTestContainer")
        self.assertEqual(len(self.docker.containers), 1)
        self.assertEqual(len(self.docker.networks), 1)

    def test_ping_states_that_human_authorization_is_not_verified(self):
        result = self.service.execute(request("Ping", {}), os.getuid())
        self.assertFalse(result["human_authorization_verified"])

    def test_audit_events_are_helper_derived_and_exclude_parameters(self):
        self.service.execute(request("Ping", {}), os.getuid())
        self.assertEqual(
            [event["event"] for event in self.audit],
            ["operation_started", "operation_completed"],
        )
        for event in self.audit:
            self.assertNotIn("parameters", event)
            self.assertNotIn("secret", json.dumps(event).lower())

    def test_unknown_operation_receipt_is_not_found(self):
        query = request(
            "GetOperation", {"subject_operation_id": str(uuid.uuid4())}
        )
        with self.assertRaises(ProtocolError) as caught:
            self.service.execute(query, os.getuid())
        self.assertEqual(caught.exception.code, "NotFound")


if __name__ == "__main__":
    unittest.main()
