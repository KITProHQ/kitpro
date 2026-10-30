import importlib.util
from pathlib import Path
import unittest


path = Path(__file__).with_name("hostadmin_environment.py")
spec = importlib.util.spec_from_file_location("hostadmin_environment", path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PoolStatusTests(unittest.TestCase):
    def test_docker_networks_without_ipam_subnets(self):
        raw = b'[{"IPAM":{"Config":null}},{"IPAM":{"Config":[{"Subnet":"172.17.0.0/16"}]}}]'
        self.assertEqual(module.networks_from_docker(raw), ["172.17.0.0/16"])
        self.assertIsNone(module.networks_from_docker(b'[{"IPAM":{"Config":"invalid"}}]'))

    def test_ready_pool(self):
        config = {"default-address-pools": [{"base": "172.24.0.0/16", "size": 24}]}
        self.assertEqual(module.pool_status(config, ["192.0.2.0/24"], ["172.17.0.0/16"]), "ready")

    def test_accepts_docker_allocations_and_their_matching_routes(self):
        config = {"default-address-pools": [{"base": "172.24.0.0/16", "size": 24}]}
        self.assertEqual(module.pool_status(config, ["172.24.1.0/24"], ["172.24.1.0/24"]), "ready")
        self.assertEqual(module.pool_status(config, [], ["2001:db8::/64"]), "ready")

    def test_rejects_external_routes_and_oversized_docker_networks(self):
        config = {"default-address-pools": [{"base": "172.24.0.0/16", "size": 24}]}
        for routes, networks in [(["172.24.5.0/24"], []), ([], ["172.24.0.0/16"])]:
            self.assertEqual(module.pool_status(config, routes, networks), "conflict")

    def test_rejects_invalid_or_insufficient_pools(self):
        self.assertEqual(module.pool_status({"default-address-pools": []}, [], []), "missing")
        self.assertEqual(module.pool_status({"default-address-pools": [{"base": "8.8.0.0/16", "size": 24}]}, [], []), "invalid")
        self.assertEqual(module.pool_status({"default-address-pools": [{"base": "172.24.0.0/20", "size": 24}]}, [], []), "insufficient_capacity")
        self.assertEqual(module.pool_status({"default-address-pools": [{"base": "172.24.0.0/16", "size": 25}]}, [], []), "invalid")
        self.assertEqual(module.pool_status({"default-address-pools": [{"base": "172.24.0.0/18", "size": 24}]}, [], ["172.24.0.0/24"]), "insufficient_capacity")

    def test_requires_complete_network_inventory(self):
        config = {"default-address-pools": [{"base": "172.24.0.0/16", "size": 24}]}
        self.assertEqual(module.pool_status(config, None, []), "unverified")
        self.assertEqual(module.pool_status(config, [], None), "unverified")

    def test_command_output_ceiling_discards_unbounded_output(self):
        if not Path("/usr/bin/yes").exists():
            self.skipTest("coreutils yes unavailable")
        success, output = module.command(["yes"])
        self.assertFalse(success)
        self.assertEqual(output, b"")


if __name__ == "__main__":
    unittest.main()
