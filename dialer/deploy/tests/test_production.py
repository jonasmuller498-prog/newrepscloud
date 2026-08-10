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


def decode_resources(rendered):
    command = ["kubectl", "create", "--dry-run=client", "--validate=false",
               "-f", "-", "-o", "json"]
    raw = subprocess.run(
        command, input=rendered, check=True, text=True, capture_output=True
    ).stdout
    decoder, resources, offset = json.JSONDecoder(), [], 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset < len(raw):
            resource, offset = decoder.raw_decode(raw, offset)
            resources.append(resource)
    return resources


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
                self.assertIn("voice-dialer.obvious.tech/backup-status: unconfigured-suspended", rendered)
                resources = {
                    (item["kind"], item["metadata"]["name"]): item
                    for item in decode_resources(rendered)
                }
                carrier = resources[("NetworkPolicy", "allow-carrier-ingress")]["spec"]["ingress"]
                engine = resources[("NetworkPolicy", "allow-engine-egress")]["spec"]["egress"]
                self.assertEqual(
                    [item["ipBlock"]["cidr"] for item in carrier[0]["from"]],
                    ["192.0.2.10/32", "192.0.2.11/32"],
                )
                self.assertEqual(
                    [item["ipBlock"]["cidr"] for item in engine[3]["to"]],
                    ["192.0.2.10/32", "192.0.2.11/32"],
                )
                self.assertEqual(carrier[1]["from"][0]["ipBlock"]["cidr"], "198.51.100.0/24")
                self.assertEqual(engine[4]["to"][0]["ipBlock"]["cidr"], "198.51.100.0/24")
                if overlay == "production-backups":
                    self.assertIn("name: postgres-logical-restore", rendered)
                    self.assertRegex(
                        rendered, r"name: dialer-postgres-runtime-[a-z0-9]+\n"
                    )

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

    def test_all_enablement_gates_are_required_together(self):
        with tempfile.TemporaryDirectory() as temp:
            copy, inputs = self.make_copy(temp)
            runtime = inputs / "runtime.env"
            runtime.write_text(
                runtime.read_text()
                .replace("DIALING_ENABLED=false", "DIALING_ENABLED=true")
                .replace("CPS=0", "CPS=1")
            )
            (inputs / "safety.env").write_text(
                "BACKUP_STATUS=configured-suspended\n"
                "BACKUP_DESTINATION=NONPRODUCTION_TEST_DESTINATION\n"
                "BACKUP_ACKNOWLEDGED=true\n"
            )
            (inputs / "trunk.env").write_text(
                "DIALER_TRUNK_ENABLED=true\n"
                "DIALER_TRUNK_AUTH_MODE=ip\n"
                "DIALER_TRUNK_SIP_URI_PRIMARY=sip:account@192.0.2.10:5060\n"
                "DIALER_TRUNK_SIP_URI_SECONDARY=sip:account@192.0.2.11:5060\n"
            )
            result = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            trunk = inputs / "trunk.env"
            valid = trunk.read_text()
            trunk.write_text(valid.replace("192.0.2.11", "192.0.2.10"))
            duplicate = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(duplicate.returncode, 0)
            self.assertIn("SBC URIs must be distinct", duplicate.stderr)
            trunk.write_text(valid.replace("192.0.2.11", "192.0.2.12"))
            mismatched = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(mismatched.returncode, 0)
            self.assertIn("paired signaling /32", mismatched.stderr)
            trunk.write_text(valid)
            network = inputs / "network.env"
            network.write_text(network.read_text().replace(
                "192.0.2.10/32", "192.0.2.10/31"
            ))
            broad = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(broad.returncode, 0)
            self.assertIn("exact IPv4 /32", broad.stderr)

    def test_ari_dial_context_must_be_plain_name(self):
        with tempfile.TemporaryDirectory() as temp:
            copy, inputs = self.make_copy(temp)
            runtime = inputs / "runtime.env"
            runtime.write_text(runtime.read_text().replace(
                "ARI_DIAL_CONTEXT=dialer-outbound",
                "ARI_DIAL_CONTEXT=Local/%s@dialer-outbound",
            ))
            result = subprocess.run(
                ["python3", str(copy / "scripts/check-production-inputs.py"),
                 "--allow-test-net", str(inputs)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("direct ARI settings changed", result.stderr)

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
