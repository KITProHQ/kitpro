from __future__ import annotations

import tempfile
import threading
import unittest
from pathlib import Path

from fixture.fake_docker import FakeDocker
from fixture.model import (
    ReconciliationError,
    SimulatedCrash,
    expected_config,
)
from fixture.service import ReconciliationService
from fixture.store import ControlPlaneState, HelperState


DIGEST_A = "sha256:" + "a" * 64
DIGEST_B = "sha256:" + "b" * 64
OP_A = "00000000-0000-4000-8000-000000000001"
OP_B = "00000000-0000-4000-8000-000000000002"


class ReconciliationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.state_path = self.root / "helper.json"
        self.api_path = self.root / "api.json"
        self.docker = FakeDocker()
        self.state = HelperState(self.state_path, "installation-a")
        self.api = ControlPlaneState(self.api_path)
        self.service = ReconciliationService(self.state, self.docker)

    def tearDown(self):
        self.temp.cleanup()

    def install(self, operation=OP_A, instance="instance-a", **kwargs):
        return self.service.install(operation, instance, DIGEST_A, **kwargs)

    def test_01_operation_succeeds_normally(self):
        receipt = self.install()
        self.assertEqual(receipt["state"], "succeeded")
        self.assertEqual(receipt["outcome"], "confirmed_applied")
        observed = self.service.inspect_instance("instance-a")
        self.assertEqual(observed["classification"], "Healthy")
        self.assertTrue(observed["ownership_proven"])
        self.assertFalse(observed["automatic_mutation_allowed"])

    def test_02_crash_before_docker_mutation_is_proven_absent(self):
        with self.assertRaises(SimulatedCrash):
            self.install(fault="crash_before_docker")
        recovered = ReconciliationService(
            HelperState(self.state_path, "installation-a"), self.docker
        ).startup_reconcile()[0]
        self.assertEqual(recovered["state"], "failed")
        self.assertEqual(recovered["recovery"], "retry_after_validation")
        self.assertEqual(self.docker.create_calls, 0)

    def test_03_docker_success_then_crash_is_reconciled(self):
        with self.assertRaises(SimulatedCrash):
            self.install(fault="crash_after_docker")
        self.assertEqual(self.docker.create_calls, 1)
        recovered = ReconciliationService(
            HelperState(self.state_path, "installation-a"), self.docker
        ).startup_reconcile()[0]
        self.assertEqual(recovered["state"], "succeeded")
        self.assertEqual(self.docker.create_calls, 1)

    def test_04_docker_failure_before_mutation_is_recorded(self):
        self.docker.mode = "fail_before"
        receipt = self.install()
        self.assertEqual(receipt["state"], "failed")
        self.assertEqual(receipt["outcome"], "confirmed_not_applied")
        self.assertEqual(receipt["recovery"], "retry_after_validation")

    def test_05_unknown_outcome_reconciles_before_retry(self):
        self.docker.mode = "unknown_after"
        receipt = self.install()
        self.assertEqual(receipt["state"], "reconciling")
        self.assertEqual(receipt["recovery"], "reconcile_first")
        self.docker.mode = "success"
        recovered = self.service.reconcile_operation(OP_A)
        self.assertEqual(recovered["state"], "succeeded")
        self.assertEqual(self.docker.create_calls, 1)

    def test_06_exact_operation_replay_does_not_mutate(self):
        first = self.install()
        second = self.install()
        self.assertEqual(first, second)
        self.assertEqual(self.docker.create_calls, 1)

    def test_07_same_operation_id_with_different_request_is_rejected(self):
        self.install()
        with self.assertRaises(ReconciliationError) as caught:
            self.service.install(OP_A, "instance-a", DIGEST_B)
        self.assertEqual(caught.exception.code, "OperationConflict")

    def test_08_two_mutations_on_same_instance_are_excluded(self):
        entered = threading.Event()
        release = threading.Event()
        self.docker.pause_entered = entered
        self.docker.pause_release = release
        results = []

        thread = threading.Thread(target=lambda: results.append(self.install()))
        thread.start()
        self.assertTrue(entered.wait(timeout=2))
        with self.assertRaises(ReconciliationError) as caught:
            self.service.install(OP_B, "instance-a", DIGEST_A)
        self.assertEqual(caught.exception.code, "InstanceBusy")
        release.set()
        thread.join(timeout=2)
        self.assertEqual(results[0]["state"], "succeeded")

    def test_09_different_instances_can_mutate_concurrently(self):
        results = []
        barrier = threading.Barrier(3)

        def run(operation, instance):
            barrier.wait()
            results.append(self.service.install(operation, instance, DIGEST_A))

        threads = [
            threading.Thread(target=run, args=(OP_A, "instance-a")),
            threading.Thread(target=run, args=(OP_B, "instance-b")),
        ]
        for thread in threads:
            thread.start()
        barrier.wait()
        for thread in threads:
            thread.join(timeout=2)
        self.assertEqual({item["state"] for item in results}, {"succeeded"})
        self.assertEqual(len(results), 2)

    def test_10_missing_resource_is_reported_without_recreation(self):
        self.install()
        owner = self.state.read()["ownership"]["instance-a"]
        self.docker.remove_name(owner["expected_name"])
        result = self.service.inspect_instance("instance-a")
        self.assertEqual(result["classification"], "MissingResource")
        self.assertFalse(result["automatic_mutation_allowed"])
        self.assertEqual(self.docker.create_calls, 1)

    def test_11_foreign_resource_replacement_is_blocked(self):
        self.install()
        owner = self.state.read()["ownership"]["instance-a"]
        self.docker.replace(owner["expected_name"], {"labels": {"foreign": "true"}})
        result = self.service.inspect_instance("instance-a")
        self.assertEqual(result["classification"], "OwnershipConflict")
        self.assertFalse(result["automatic_mutation_allowed"])

    def test_12_matching_labels_without_helper_state_do_not_establish_ownership(self):
        config = expected_config("installation-a", "instance-a", DIGEST_A)
        self.docker.create_unrecorded(config)
        result = self.service.inspect_instance("instance-a")
        self.assertEqual(result["classification"], "OwnershipConflict")
        self.assertFalse(result["automatic_mutation_allowed"])

    def test_13_helper_state_without_docker_labels_is_blocked(self):
        self.install()
        owner = self.state.read()["ownership"]["instance-a"]
        self.docker.replace(owner["expected_name"], {"labels": {}})
        result = self.service.inspect_instance("instance-a")
        self.assertEqual(result["classification"], "OwnershipConflict")

    def test_14_helper_state_loss_removes_destructive_authority(self):
        self.install()
        self.state_path.unlink()
        recovered = ReconciliationService(
            HelperState(self.state_path, "installation-a"), self.docker
        )
        result = recovered.inspect_instance("instance-a")
        self.assertEqual(result["classification"], "OwnershipConflict")
        self.assertFalse(result["automatic_mutation_allowed"])

    def test_15_api_state_loss_does_not_remove_helper_ownership(self):
        self.api.set_desired("instance-a", {"exists": True, "running": True})
        self.install()
        self.api_path.unlink()
        empty_api = ControlPlaneState(self.api_path)
        self.assertIsNone(empty_api.get_desired("instance-a"))
        self.assertEqual(self.service.inspect_instance("instance-a")["classification"], "Healthy")

    def test_16_stale_running_operation_reconciles_on_startup(self):
        with self.assertRaises(SimulatedCrash):
            self.install(fault="crash_after_docker")
        restarted = ReconciliationService(
            HelperState(self.state_path, "installation-a"), self.docker
        )
        results = restarted.startup_reconcile()
        self.assertEqual(results[0]["state"], "succeeded")
        self.assertEqual(restarted.startup_reconcile(), [])

    def test_17_security_drift_blocks_automatic_mutation(self):
        self.install()
        owner = self.state.read()["ownership"]["instance-a"]
        self.docker.replace(
            owner["expected_name"],
            {"privileged": True, "networks": ["host", "foreign"]},
        )
        result = self.service.inspect_instance("instance-a")
        self.assertEqual(result["classification"], "SecurityDrift")
        self.assertFalse(result["automatic_mutation_allowed"])

    def test_18_administrator_supersession_is_appended(self):
        self.docker.mode = "fail_before"
        failed = self.install()
        superseded = self.service.mark_superseded(OP_A, "admin-a")
        self.assertEqual(failed["state"], "failed")
        self.assertEqual(superseded["state"], "superseded")
        events = self.state.read()["events"]
        self.assertEqual(events[-1]["kind"], "operation_superseded")
        self.assertEqual(events[-1]["facts"], {"administrator": "admin-a"})

    def test_19_unknown_before_mutation_becomes_safe_only_after_observation(self):
        self.docker.mode = "unknown_before"
        receipt = self.install()
        self.assertEqual(receipt["state"], "reconciling")
        recovered = self.service.reconcile_operation(OP_A)
        self.assertEqual(recovered["failure"]["class"], "ConfirmedAbsent")
        self.assertEqual(recovered["recovery"], "retry_after_validation")
        self.assertEqual(self.docker.create_calls, 1)

    def test_20_exact_replay_of_uncertain_operation_does_not_dispatch(self):
        self.docker.mode = "unknown_before"
        first = self.install()
        self.docker.mode = "success"
        replay = self.install()
        self.assertEqual(first, replay)
        self.assertEqual(self.docker.create_calls, 1)


if __name__ == "__main__":
    unittest.main()
