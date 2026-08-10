#!/usr/bin/env python3
import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
CREATE = ROOT / "tests/create-test-inputs.sh"
CHECK = ROOT / "scripts/check-production-inputs.py"


class ProductionOverlayTests(unittest.TestCase):
    def make_copy(self, temp):
        copy = pathlib.Path(temp) / "deploy"
        shutil.copytree(ROOT, copy)
        inputs = copy / "overlays/production/inputs"
        subprocess.run(["bash", str(copy / "tests/create-test-inputs.sh"), str(inputs)], check=True)
        return copy, inputs

    def test_generated_inputs_render_every_production_overlay(self):
        with tempfile.TemporaryDirectory() as temp:
            copy, inputs = self.make_copy(temp)
            checked = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertEqual(checked.returncode, 0, checked.stderr)
            for overlay in ("production", "production-backups"):
                rendered = subprocess.run(
                    ["kubectl", "kustomize", str(copy / "overlays" / overlay)],
                    check=True, text=True, capture_output=True,
                ).stdout
                self.assertIn("kind: Secret", rendered)
                self.assertNotIn("DIALER_SOURCE_REF: REQUIRED_", rendered)
                self.assertIn("name: dialer-postgres-admin-", rendered)
                self.assertIn("name: dialer-postgres-runtime-", rendered)
                self.assertIn("name: dialer-app-secrets-", rendered)
                if overlay == "production-backups":
                    self.assertIn("name: postgres-logical-restore", rendered)

    def test_missing_or_unsafe_inputs_fail_validation(self):
        missing = subprocess.run(
            ["python3", str(CHECK), str(ROOT / "overlays/production/inputs")],
            text=True, capture_output=True,
        )
        self.assertNotEqual(missing.returncode, 0)
        with tempfile.TemporaryDirectory() as temp:
            copy, inputs = self.make_copy(temp)
            runtime = inputs / "runtime.env"
            body = runtime.read_text().replace("DIALING_ENABLED=false", "DIALING_ENABLED=true")
            body = body.replace("CPS=0", "CPS=1")
            runtime.write_text(body)
            unsafe = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(unsafe.returncode, 0)
            self.assertIn("enabled exact trunk", unsafe.stderr)

    def test_live_preflight_checks_both_port_fields(self):
        payload = {
            "items": [
                {
                    "metadata": {"namespace": "other", "name": "node"},
                    "spec": {
                        "ports": [{"nodePort": 32300}],
                        "healthCheckNodePort": 31100,
                    },
                }
            ]
        }
        with tempfile.TemporaryDirectory() as temp:
            source = pathlib.Path(temp) / "services.json"
            source.write_text(json.dumps(payload))
            result = subprocess.run(
                ["python3", str(ROOT / "scripts/check-live-ports.py"), str(source)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("32300 nodePort", result.stderr)
            self.assertIn("31100 healthCheckNodePort", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
