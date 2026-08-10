#!/usr/bin/env python3
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]


def read(relative):
    return (ROOT / relative).read_text(encoding="utf-8")


class DeploymentTests(unittest.TestCase):
    def test_every_file_is_under_200_lines(self):
        for path in ROOT.rglob("*"):
            if path.is_file():
                count = len(path.read_text(encoding="utf-8").splitlines())
                self.assertLess(count, 200, f"{path.relative_to(ROOT)} has {count} lines")

    def test_base_kustomize_renders(self):
        result = subprocess.run(
            ["kubectl", "kustomize", str(ROOT)],
            check=True,
            text=True,
            capture_output=True,
        )
        rendered = result.stdout
        self.assertIn("namespace: voice-dialer", rendered)
        self.assertIn("kind: StatefulSet", rendered)
        self.assertIn("@@TRUNK_BLOCK@@", rendered)
        self.assertIn("- /scripts/render-config.go", rendered)

    def test_production_overlay_renders_with_untracked_inputs(self):
        with tempfile.TemporaryDirectory() as temp:
            copy = pathlib.Path(temp) / "deploy"
            shutil.copytree(ROOT, copy)
            inputs = copy / "overlays/production/inputs"
            values = {
                "runtime.env": read("config/app.env")
                .replace("REQUIRED_40_CHARACTER_GIT_COMMIT_SHA", "1" * 40)
                .replace("REQUIRED_64_CHARACTER_SHA256", "2" * 64),
                "network.env": read("config/network.env.example"),
                "postgres.env": "POSTGRES_USER=dialer\nPOSTGRES_PASSWORD=" + "3" * 64 + "\nPOSTGRES_DB=dialer\n",
                "app.env": "DIALER_API_TOKEN=" + "4" * 64 + "\nDIALER_ORIGIN_TOKEN=" + "5" * 64 + "\n",
                "ari.env": "ARI_USERNAME=dialer_app\nARI_PASSWORD=" + "6" * 64 + "\n",
                "trunk.env": (
                    "DIALER_TRUNK_ENABLED=false\nDIALER_TRUNK_AUTH_MODE=ip\n"
                    "DIALER_TRUNK_SIP_URI=sip:account@sbc.example:5060\n"
                    "DIALER_TRUNK_USERNAME=\nDIALER_TRUNK_PASSWORD=\n"
                    "DIALER_TRUNK_REALM=*\nDIALER_CALLER_ID=+12025550123\n"
                ),
            }
            for name, value in values.items():
                (inputs / name).write_text(value, encoding="utf-8")
            subprocess.run(
                ["kubectl", "kustomize", str(copy / "overlays/production")],
                check=True,
                text=True,
                capture_output=True,
            )

    def test_nodeports_are_exact_and_nonconflicting(self):
        manifests = "\n".join(path.read_text() for path in (ROOT / "engine").glob("*.yaml"))
        ports = [int(value) for value in re.findall(r"nodePort:\s*(\d+)", manifests)]
        expected = {31100, *range(31500, 31700)}
        self.assertEqual(set(ports), expected)
        self.assertEqual(len(ports), len(expected))
        self.assertTrue(expected.isdisjoint({32061, *range(32200, 32220)}))
        for path in (ROOT / "engine").glob("rtp-*.yaml"):
            body = path.read_text()
            self.assertIn("externalTrafficPolicy: Local", body)
            self.assertNotIn("protocol: TCP", body)

    def test_safe_defaults_and_secret_examples(self):
        defaults = read("config/app.env")
        for setting in ("DIALING_ENABLED=false", "CPS=0", "MAX_CONCURRENCY=20"):
            self.assertIn(setting, defaults)
        self.assertIn("DIALER_SOURCE_REF=REQUIRED_40_CHARACTER_GIT_COMMIT_SHA", defaults)
        trunk = read("secrets/trunk.env.example")
        self.assertIn("DIALER_TRUNK_ENABLED=false", trunk)
        self.assertIn("disabled.invalid", trunk)
        for path in (ROOT / "secrets").glob("*.example"):
            self.assertNotRegex(path.read_text(), r"(?i)(password|token)=[A-Fa-f0-9]{20,}")

    def test_asterisk_is_outbound_only_and_loopback_ari(self):
        required = {
            "pjsip.conf", "extensions.conf", "http.conf", "ari.conf",
            "rtp.conf", "logger.conf", "modules.conf",
        }
        self.assertTrue(required.issubset({p.name for p in (ROOT / "asterisk").glob("*.conf")}))
        self.assertIn("bindaddr=127.0.0.1", read("asterisk/http.conf"))
        pjsip = read("asterisk/pjsip.conf")
        self.assertIn("external_signaling_address=5.196.90.231", pjsip)
        self.assertIn("bind=0.0.0.0:31100", pjsip)
        dialplan = read("asterisk/extensions.conf")
        for marker in (
            r'REGEX("^\+1[0-9]{10}$"', "HARD_EXTERNAL_LIMIT=100",
            "STAT(e,${CAMPAIGN_FILE}.wav)", "DialerAnswer", "DialerOptOut",
            "DialerPlaybackComplete", "DialerCompletion", "Background(",
        ):
            self.assertIn(marker, dialplan)
        self.assertNotRegex(dialplan.lower(), r"\b(mixmonitor|monitor|amd|record)\s*\(")
        services = "\n".join(path.read_text() for path in (ROOT / "engine").glob("service-*.yaml"))
        self.assertNotIn("8088", services)

    def test_storage_security_and_paused_restore(self):
        engine = read("engine/statefulset.yaml") + read("engine/statefulset-app.yaml")
        self.assertIn("kubernetes.io/hostname: runners", engine)
        self.assertIn("claimName: dialer-media", engine)
        self.assertIn("mountPath: /media\n              readOnly: true", read("engine/statefulset-asterisk.yaml"))
        self.assertIn("storageClassName: longhorn", read("postgres/statefulset.yaml"))
        self.assertIn("suspend: true", read("postgres/backup-cronjob.yaml"))
        restore = read("optional/backups/logical-restore-job.yaml")
        self.assertIn("suspend: true", restore)
        self.assertIn("I_UNDERSTAND_DATA_WILL_BE_REPLACED", restore)

    def test_network_and_monitoring_guards(self):
        self.assertIn("name: default-deny-all", read("network/default-deny.yaml"))
        self.assertIn("0.0.0.0/0", read("network/engine-egress.yaml"))
        self.assertIn("except:", read("network/engine-egress.yaml"))
        ingress = read("ingress.yaml")
        self.assertIn("dialer.playground.obvious.tech", ingress)
        self.assertIn("letsencrypt-prod", ingress)
        rules = read("optional/monitoring/prometheusrule.yaml")
        for alert in (
            "DialerEnginePodDown", "DialerSchedulerPaused", "DialerSlotMismatch",
            "DialerCPSThrottling", "DialerHighSIPFailureRate",
            "DialerOptOutPersistenceFailure",
        ):
            self.assertIn(f"alert: {alert}", rules)


if __name__ == "__main__":
    unittest.main(verbosity=2)
