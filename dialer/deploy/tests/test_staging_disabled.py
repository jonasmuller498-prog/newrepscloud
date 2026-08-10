#!/usr/bin/env python3
import os
import pathlib
import shutil
import stat
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SOURCE_REF = "0123456789abcdef0123456789abcdef01234567"


class StagingDisabledTests(unittest.TestCase):
    def make_copy(self, temp):
        copy = pathlib.Path(temp) / "deploy"
        shutil.copytree(ROOT, copy)
        inputs = copy / "overlays/staging-disabled/inputs"
        for path in inputs.glob("*.env"):
            path.unlink()
        subprocess.run(
            ["bash", str(copy / "scripts/init-staging-inputs.sh"), SOURCE_REF],
            check=True, text=True, capture_output=True,
        )
        return copy, inputs

    def render(self, copy):
        return subprocess.run(
            ["kubectl", "kustomize", str(copy / "overlays/staging-disabled")],
            check=True, text=True, capture_output=True,
        ).stdout

    def json_stream(self, rendered):
        return subprocess.run(
            ["kubectl", "create", "--dry-run=client", "--validate=false",
             "-f", "-", "-o", "json"],
            input=rendered, check=True, text=True, capture_output=True,
        ).stdout

    def test_generated_inputs_and_render_pass_fail_closed_checks(self):
        with tempfile.TemporaryDirectory() as temp:
            copy, inputs = self.make_copy(temp)
            expected = {
                "runtime.env", "safety.env", "postgres-admin.env",
                "postgres-runtime.env", "app.env", "ari.env", "trunk.env",
            }
            self.assertEqual({path.name for path in inputs.glob("*.env")}, expected)
            for path in inputs.glob("*.env"):
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            checked = subprocess.run(
                ["python3", str(copy / "scripts/check-staging-inputs.py"), str(inputs)],
                text=True, capture_output=True,
            )
            self.assertEqual(checked.returncode, 0, checked.stderr)
            rendered = self.render(copy)
            manifest_json = pathlib.Path(temp) / "staging.json"
            manifest_json.write_text(self.json_stream(rendered), encoding="utf-8")
            checked = subprocess.run(
                ["python3", str(copy / "scripts/check-staging-render.py"),
                 str(manifest_json)],
                text=True, capture_output=True,
            )
            self.assertEqual(checked.returncode, 0, checked.stderr)
            self.assertNotIn("type: NodePort", rendered)
            self.assertNotIn("name: dialer-sip", rendered)
            self.assertNotIn("name: dialer-rtp-", rendered)

    def test_helper_is_noninteractive_safe_and_prompts_for_nothing_else(self):
        source = (ROOT / "scripts/init-staging-inputs.sh").read_text()
        self.assertEqual(source.count("read -r -p"), 1)
        self.assertNotIn("CIDR", source)
        result = subprocess.run(
            ["bash", str(ROOT / "scripts/init-staging-inputs.sh")],
            stdin=subprocess.DEVNULL, text=True, capture_output=True, timeout=5,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source commit argument", result.stderr)

    def test_input_checker_rejects_scheduler_or_trunk_enablement(self):
        with tempfile.TemporaryDirectory() as temp:
            copy, inputs = self.make_copy(temp)
            runtime = inputs / "runtime.env"
            runtime.write_text(
                runtime.read_text().replace("DIALING_ENABLED=false",
                                            "DIALING_ENABLED=true")
            )
            os.chmod(runtime, 0o600)
            result = subprocess.run(
                ["python3", str(copy / "scripts/check-staging-inputs.py"), str(inputs)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("fixed staging runtime", result.stderr)

    def test_render_checker_rejects_a_public_service(self):
        with tempfile.TemporaryDirectory() as temp:
            copy, _ = self.make_copy(temp)
            rendered = self.render(copy).replace(
                "type: ClusterIP", "type: NodePort", 1)
            manifest_json = pathlib.Path(temp) / "unsafe.json"
            manifest_json.write_text(self.json_stream(rendered), encoding="utf-8")
            result = subprocess.run(
                ["python3", str(copy / "scripts/check-staging-render.py"),
                 str(manifest_json)],
                text=True, capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("public service type", result.stderr)

    def test_inputs_directory_commits_no_env_files(self):
        inputs = ROOT / "overlays/staging-disabled/inputs"
        tracked = subprocess.run(
            ["git", "-C", str(ROOT), "ls-files", "--",
             "overlays/staging-disabled/inputs"],
            check=True, text=True, capture_output=True,
        )
        self.assertEqual(
            {pathlib.Path(path).name for path in tracked.stdout.splitlines()},
            {".gitignore", "README.md"},
        )
        self.assertIn("*.env", (inputs / ".gitignore").read_text())


if __name__ == "__main__":
    unittest.main(verbosity=2)
