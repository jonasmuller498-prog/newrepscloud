#!/usr/bin/env python3
import pathlib
import re
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]


def read(relative):
    return (ROOT / relative).read_text(encoding="utf-8")


def env_keys(relative):
    return {
        line.split("=", 1)[0]
        for line in read(relative).splitlines()
        if line and not line.startswith("#")
    }


class DeploymentTests(unittest.TestCase):
    def test_every_file_is_under_200_lines(self):
        for path in ROOT.rglob("*"):
            if path.is_file() and path.suffix != ".pyc":
                count = len(path.read_text(encoding="utf-8").splitlines())
                self.assertLess(count, 200, f"{path.relative_to(ROOT)} has {count} lines")

    def test_base_renders_but_has_no_generated_credentials(self):
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
        self.assertNotIn("\nkind: Secret\n", rendered)
        missing = subprocess.run(
            ["kubectl", "kustomize", str(ROOT / "overlays/production")],
            text=True, capture_output=True,
        )
        self.assertNotEqual(missing.returncode, 0)

    def test_nodeports_are_exact_and_nonconflicting(self):
        manifests = "\n".join(path.read_text() for path in (ROOT / "base/engine").glob("*.yaml"))
        ports = [int(value) for value in re.findall(r"nodePort:\s*(\d+)", manifests)]
        expected = {31100, *range(32300, 32500)}
        self.assertEqual(set(ports), expected)
        self.assertEqual(len(ports), len(expected))
        self.assertTrue(expected.isdisjoint({32061, *range(32200, 32220)}))
        for path in (ROOT / "base/engine").glob("rtp-*.yaml"):
            body = path.read_text()
            self.assertIn("externalTrafficPolicy: Local", body)
            self.assertNotIn("protocol: TCP", body)

    def test_final_app_environment_contract(self):
        defaults = read("base/config/app.env")
        for setting in ("DIALING_ENABLED=false", "CPS=0", "MAX_CONCURRENCY=20"):
            self.assertIn(setting, defaults)
        fixed = {
            "HTTP_ADDR=:8080", "METRICS_ADDR=:9090", "MEDIA_DIR=/media",
            "EVENT_JOURNAL_DIR=/media/ari-journal",
            "ARI_URL=http://127.0.0.1:8088/ari", "ARI_APP=voice-dialer",
            "ARI_ENDPOINT=outbound",
        }
        self.assertTrue(fixed.issubset(set(defaults.splitlines())))
        self.assertEqual(env_keys("base/secrets/app.env.example"), {
            "OPERATOR_API_TOKEN", "APPROVER_API_TOKEN", "PHONE_HASH_KEY",
            "FIELD_ENCRYPTION_KEY", "AUDIT_HMAC_KEY",
        })
        self.assertEqual(env_keys("base/secrets/ari.env.example"), {"ARI_USER", "ARI_PASSWORD"})
        self.assertIn(
            "DATABASE_URL", env_keys("base/secrets/postgres-runtime.env.example")
        )
        build = read("base/scripts/build-app.sh")
        self.assertIn("cd /workspace/source/dialer/app", build)
        self.assertRegex(build, r"go build [^\n]* \.")
        self.assertIn("chmod 0555 /app-bin/dialer.tmp", build)
        statefulset = read("base/engine/statefulset.yaml")
        self.assertIn("static-debian12:nonroot@", statefulset)
        self.assertIn("sizeLimit: 1Gi", statefulset)
        self.assertIn("GOMODCACHE", read("base/engine/statefulset-init.yaml"))
        self.assertIn("immutable: true", read("overlays/production/kustomization.yaml"))

    def test_direct_ari_and_shared_media_are_coherent(self):
        self.assertIn("bindaddr=127.0.0.1", read("base/asterisk/http.conf"))
        self.assertIn("sessionlimit=150", read("base/asterisk/http.conf"))
        core = read("base/asterisk/asterisk.conf")
        self.assertIn("maxcalls = 150", core)
        self.assertIn("astdbdir => /var/lib/asterisk-state", core)
        dialplan = read("base/asterisk/extensions.conf")
        self.assertIn("[reject-inbound]", dialplan)
        self.assertNotRegex(dialplan, r"\b(?:Dial|Background|Playback|Stasis)\s*\(")
        renderer = read("base/scripts/render-config.go")
        self.assertIn("[outbound]", renderer)
        self.assertIn("dtmf_mode=rfc4733", renderer)
        self.assertIn("media_encryption=no", renderer)
        self.assertNotIn("callerid=", renderer.lower())
        app = read("base/engine/statefulset-app.yaml")
        asterisk = read("base/engine/statefulset-asterisk.yaml")
        self.assertIn("runAsUser: 1000", app)
        self.assertIn("runAsUser: 1000", asterisk)
        self.assertNotIn("preStop:", asterisk)
        self.assertIn("mountPath: /media", app)
        init = read("base/engine/statefulset-init.yaml")
        self.assertIn("mkdir -p /media/ari-journal", init)
        self.assertIn("name: prepare-shared-storage", init)
        self.assertIn("mountPath: /var/lib/asterisk/sounds/campaigns", asterisk)
        self.assertIn("mountPath: /var/lib/asterisk-state", asterisk)
        self.assertNotIn("mountPath: /var/lib/asterisk\n", asterisk)
        services = "\n".join(path.read_text() for path in (ROOT / "base/engine").glob("service-*.yaml"))
        self.assertNotIn("8088", services)

    def test_http_metrics_and_ingress_paths(self):
        app = read("base/engine/statefulset-app.yaml")
        for path in ("/health/live", "/health/ready"):
            self.assertIn(f"path: {path}", app)
        metrics = read("base/engine/service-metrics.yaml")
        self.assertIn("port: 9090", metrics)
        self.assertIn("targetPort: metrics", metrics)
        ingress = read("base/ingress.yaml")
        self.assertIn("proxy-body-size: 22m", ingress)
        self.assertIn("name: dialer-api", ingress)
        self.assertNotIn("dialer-metrics", ingress)

    def test_database_backup_and_disruption_guards(self):
        postgres = read("base/postgres/statefulset.yaml")
        self.assertIn("name: dialer-postgres-admin", postgres)
        self.assertIn("name: dialer-postgres-runtime", postgres)
        self.assertNotIn("preStop:", postgres)
        self.assertIn("postgres/init-runtime.sh", read("base/kustomization.yaml"))
        self.assertIn("suspend: true", read("base/postgres/backup-cronjob.yaml"))
        for pdb in ("base/engine/pdb.yaml", "base/postgres/pdb.yaml"):
            self.assertIn("minAvailable: 1", read(pdb))
        service = read("base/postgres/service.yaml")
        self.assertRegex(
            service,
            r"selector:\n\s+app\.kubernetes\.io/name: dialer-postgres\n"
            r"\s+app\.kubernetes\.io/component: database",
        )
        database_network = read("base/network/database.yaml")
        self.assertGreaterEqual(
            database_network.count("app.kubernetes.io/component: database"), 2
        )
        self.assertIn(
            "app.kubernetes.io/component: database",
            read("base/network/engine-egress.yaml"),
        )
        self.assertFalse((ROOT / "optional/backups/volume-snapshot.yaml").exists())
        self.assertFalse((ROOT / "optional/backups/snapshot-restore-pvc.yaml").exists())

    def test_network_namespaces_and_private_metrics(self):
        self.assertIn("name: default-deny-all", read("base/network/default-deny.yaml"))
        app_ingress = read("base/network/app-ingress.yaml")
        self.assertIn("kubernetes.io/metadata.name: kube-system", app_ingress)
        self.assertIn("app.kubernetes.io/name: rke2-ingress-nginx", app_ingress)
        acme = read("base/network/acme-solver.yaml")
        self.assertIn('acme.cert-manager.io/http01-solver: "true"', acme)
        self.assertIn("port: 8089", acme)
        monitoring = read("optional/monitoring/networkpolicy.yaml")
        self.assertIn("kubernetes.io/metadata.name: cattle-monitoring-system", monitoring)
        carrier = read("base/network/carrier-ingress.yaml")
        self.assertIn("port: 32300", carrier)
        self.assertIn("endPort: 32499", carrier)

    def test_monitoring_rules_use_exported_private_metrics(self):
        metrics = (ROOT.parent / "app/metrics.go").read_text()
        rules = read("optional/monitoring/prometheusrule.yaml")
        names = {
            "dialer_scheduler_enabled", "dialer_slot_mismatch",
            "dialer_cps_throttled_total", "dialer_sip_attempts_total",
            "dialer_opt_out_persistence_failures_total",
        }
        for name in names:
            self.assertIn(name, metrics)
            self.assertIn(name, rules)
        self.assertIn('result=\\"accepted\\"', metrics)
        self.assertIn('result=\\"failed\\"', metrics)
        self.assertNotIn("dialer-metrics", read("base/ingress.yaml"))

    def test_all_workload_images_are_tag_and_digest_pinned(self):
        image_re = re.compile(r"^\s*image:\s+\S+:[^@\s]+@sha256:[0-9a-f]{64}\s*$")
        images = []
        for path in ROOT.rglob("*.yaml"):
            images.extend(line for line in path.read_text().splitlines() if "image:" in line)
        self.assertTrue(images)
        for line in images:
            self.assertRegex(line, image_re)


if __name__ == "__main__":
    unittest.main(verbosity=2)
