#!/usr/bin/env python3
import json
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
CHECK = ROOT / "scripts/check-staging-live.py"


def run(payload):
    with tempfile.TemporaryDirectory() as temp:
        path = pathlib.Path(temp) / "resources.json"
        path.write_text(json.dumps(payload))
        return subprocess.run(
            ["python3", str(CHECK), str(path)],
            text=True, capture_output=True,
        )


class StagingLiveTests(unittest.TestCase):
    def test_private_staging_state_passes(self):
        payload = {"items": [
            {
                "kind": "Service",
                "metadata": {"name": "dialer-api"},
                "spec": {"type": "ClusterIP", "ports": [{"port": 8080}]},
            },
            {
                "kind": "NetworkPolicy",
                "metadata": {"name": "allow-engine-egress"},
                "spec": {"egress": [{"ports": [{"port": 53, "protocol": "UDP"}]}]},
            },
        ]}
        result = run(payload)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_residual_carrier_resources_fail(self):
        payload = {"items": [
            {
                "kind": "Service",
                "metadata": {
                    "name": "dialer-sip",
                    "labels": {"app.kubernetes.io/component": "sip"},
                },
                "spec": {
                    "type": "NodePort",
                    "ports": [{"port": 31100, "nodePort": 31100}],
                },
            },
            {
                "kind": "NetworkPolicy",
                "metadata": {"name": "allow-carrier-ingress"},
                "spec": {},
            },
        ]}
        result = run(payload)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("dialer-sip", result.stderr)
        self.assertIn("allow-carrier-ingress", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
