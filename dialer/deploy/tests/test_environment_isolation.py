#!/usr/bin/env python3
import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]


def decode(rendered):
    raw = subprocess.run(
        ["kubectl", "create", "--dry-run=client", "--validate=false",
         "-f", "-", "-o", "json"],
        input=rendered, check=True, text=True, capture_output=True,
    ).stdout
    decoder, items, offset = json.JSONDecoder(), [], 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset < len(raw):
            item, offset = decoder.raw_decode(raw, offset)
            items.append(item)
    return items


class EnvironmentIsolationTests(unittest.TestCase):
    def test_production_has_distinct_identity_and_public_address(self):
        with tempfile.TemporaryDirectory() as temp:
            copy = pathlib.Path(temp) / "deploy"
            shutil.copytree(ROOT, copy)
            inputs = copy / "overlays/production/inputs"
            subprocess.run(
                ["bash", str(copy / "tests/create-test-inputs.sh"), str(inputs)],
                check=True,
            )
            for overlay in ("production", "production-backups"):
                rendered = subprocess.run(
                    ["kubectl", "kustomize", str(copy / "overlays" / overlay)],
                    check=True, text=True, capture_output=True,
                ).stdout
                items = decode(rendered)
                namespaces = [
                    item["metadata"]["name"] for item in items
                    if item["kind"] == "Namespace"
                ]
                self.assertEqual(namespaces, ["voice-dialer-production"])
                workload = next(item for item in items if item["kind"] == "StatefulSet")
                self.assertEqual(workload["metadata"]["namespace"], "voice-dialer-production")
                ingress = next(item for item in items if item["kind"] == "Ingress")
                self.assertEqual(
                    ingress["spec"]["rules"][0]["host"],
                    "dialer-production.example.test",
                )
                self.assertEqual(
                    ingress["spec"]["tls"][0]["hosts"],
                    ["dialer-production.example.test"],
                )
                configs = [
                    item for item in items if item["kind"] == "ConfigMap"
                    and item["metadata"]["name"].startswith("asterisk-config-")
                ]
                network = next(
                    item for item in items if item["kind"] == "ConfigMap"
                    and item["metadata"]["name"].startswith("dialer-network-values-")
                )
                self.assertEqual(network["data"]["DIALER_PUBLIC_IPV4"], "203.0.113.10")
                self.assertEqual(len(configs), 1)
                pjsip = configs[0]["data"]["pjsip.conf"]
                self.assertEqual(pjsip.count("@@DIALER_PUBLIC_IPV4@@"), 2)
                self.assertNotIn("5.196.90.231", pjsip)


if __name__ == "__main__":
    unittest.main(verbosity=2)
