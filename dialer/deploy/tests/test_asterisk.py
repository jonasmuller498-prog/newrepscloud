#!/usr/bin/env python3
import pathlib
import re
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
ASTERISK = ROOT / "base/asterisk"
REQUIRED_MODULES = (
    "pbx_config.so",
    "app_dial.so",
    "res_sorcery_config.so",
    "res_sorcery_memory.so",
    "res_sorcery_astdb.so",
    "res_pjproject.so",
    "res_rtp_asterisk.so",
    "codec_ulaw.so",
    "codec_alaw.so",
    "format_wav.so",
    "res_pjsip.so",
    "res_pjsip_session.so",
    "res_pjsip_pubsub.so",
    "res_pjsip_sdp_rtp.so",
    "res_pjsip_caller_id.so",
    "res_pjsip_nat.so",
    "res_pjsip_outbound_authenticator_digest.so",
    "res_pjsip_endpoint_identifier_ip.so",
    "chan_pjsip.so",
    "res_stasis.so",
    "res_stasis_answer.so",
    "res_stasis_recording.so",
    "res_stasis_playback.so",
    "res_stasis_snoop.so",
    "res_http_websocket.so",
    "res_websocket_client.so",
    "res_ari_model.so",
    "res_ari.so",
    "res_ari_events.so",
    "res_ari_channels.so",
    "res_ari_playbacks.so",
    "app_stasis.so",
)
NEW_CONFIGS = {
    "acl.conf",
    "ccss.conf",
    "cdr.conf",
    "cel.conf",
    "features.conf",
    "indications.conf",
    "manager.conf",
    "pjproject.conf",
    "stasis.conf",
    "udptl.conf",
    "websocket_client.conf",
}


def read(relative):
    return (ROOT / relative).read_text(encoding="utf-8")


def option(relative, name):
    matches = re.findall(
        rf"(?m)^\s*{re.escape(name)}\s*(?:=>|=)\s*([^;\s]+)", read(relative)
    )
    if len(matches) != 1:
        raise AssertionError(f"{relative}: expected one {name}, found {matches}")
    return matches[0]


class AsteriskHardeningTests(unittest.TestCase):
    def test_required_module_list_is_exact_and_fail_closed(self):
        body = read("base/asterisk/modules.conf")
        self.assertRegex(body, r"(?m)^autoload\s*=\s*no\s*$")
        self.assertNotRegex(body, r"(?m)^autoload\s*=\s*yes\s*$")
        directives = re.findall(
            r"(?m)^\s*(require|load|preload(?:-require)?)\s*(?:=>|=)\s*(\S+)",
            body,
        )
        self.assertEqual({kind for kind, _ in directives}, {"require"})
        self.assertEqual(tuple(module for _, module in directives), REQUIRED_MODULES)

    def test_deterministic_support_configs_are_generated(self):
        expected = {path.name for path in ASTERISK.glob("*.conf")}
        self.assertTrue(NEW_CONFIGS.issubset(expected))
        rendered = subprocess.run(
            ["kubectl", "kustomize", str(ROOT / "base")],
            check=True,
            text=True,
            capture_output=True,
        ).stdout
        config_map = next(
            doc
            for doc in rendered.split("---")
            if "kind: ConfigMap" in doc and "name: asterisk-config-" in doc
        )
        generated = set(re.findall(r"(?m)^  ([a-z0-9_.-]+\.conf):", config_map))
        self.assertEqual(generated, expected)
        for name in ("pjproject.conf", "websocket_client.conf"):
            lines = (ASTERISK / name).read_text().splitlines()
            self.assertTrue(lines)
            self.assertTrue(all(not line or line.lstrip().startswith(";") for line in lines))

    def test_optional_telephony_subsystems_are_disabled(self):
        self.assertEqual(option("base/asterisk/manager.conf", "enabled"), "no")
        self.assertEqual(option("base/asterisk/manager.conf", "webenabled"), "no")
        self.assertEqual(option("base/asterisk/cdr.conf", "enable"), "no")
        self.assertEqual(
            option("base/asterisk/cdr.conf", "channeldefaultenabled"), "no"
        )
        self.assertEqual(option("base/asterisk/cel.conf", "enable"), "no")
        ccss = read("base/asterisk/ccss.conf")
        self.assertIn("[general]", ccss)
        self.assertNotRegex(ccss, r"(?m)^\s*enabled\s*=")

    def test_stasis_pool_is_bounded(self):
        body = read("base/asterisk/stasis.conf")
        self.assertIn("[taskpool]", body)
        self.assertGreater(int(option("base/asterisk/stasis.conf", "initial_size")), 0)
        self.assertNotRegex(body, r"(?m)^\s*minimum_size\s*=")
        maximum = int(option("base/asterisk/stasis.conf", "max_size"))
        self.assertGreater(maximum, 0)
        self.assertLessEqual(maximum, 150)

    def test_warning_prone_blank_options_are_absent(self):
        body = read("base/asterisk/rtp.conf") + read("base/asterisk/http.conf")
        self.assertNotRegex(
            body, r"(?m)^\s*(?:stunaddr|turnaddr|redirect)\s*=\s*(?:;.*)?$"
        )
        self.assertEqual(
            option("base/asterisk/asterisk.conf", "astkeydir"),
            "/var/lib/asterisk",
        )

    def test_reject_dialplan_uses_numeric_patterns(self):
        body = read("base/asterisk/extensions.conf")
        self.assertNotIn("exten => _.,", body)
        for extension in ("_X!", "_+X!", "s", "i", "h"):
            self.assertRegex(body, rf"(?m)^exten => {re.escape(extension)},1,")

    def test_outbound_dialplan_fails_over_sequentially(self):
        body = read("base/asterisk/extensions.conf")
        primary = "Dial(PJSIP/${EXTEN}@outbound-primary,45)"
        secondary = "Dial(PJSIP/${EXTEN}@outbound-secondary,45)"
        self.assertLess(body.index(primary), body.index(secondary))
        self.assertIn('"${DIALSTATUS}"="CONGESTION"', body)
        self.assertIn('"${DIALSTATUS}"="CHANUNAVAIL"', body)
        self.assertNotIn(f"{primary}&", body)

    def test_asterisk_has_no_broad_state_mount(self):
        body = "\n".join(path.read_text() for path in ROOT.rglob("*.yaml"))
        self.assertNotRegex(
            body, r"(?m)^\s*mountPath:\s*/var/lib/asterisk\s*$"
        )

    def test_renderer_allows_only_pcmu_and_pcma(self):
        body = read("base/scripts/render-config.go")
        self.assertEqual(re.findall(r"(?m)^allow=([a-z0-9,]+)$", body), ["ulaw,alaw"])
        self.assertIn('required("DIALER_TRUNK_SIP_URI_PRIMARY")', body)
        self.assertIn('required("DIALER_TRUNK_SIP_URI_SECONDARY")', body)
        self.assertIn("[outbound-primary-aor]", body)
        self.assertIn("[outbound-secondary-aor]", body)
        self.assertIn("aors=outbound-primary-aor", body)
        self.assertIn("aors=outbound-secondary-aor", body)
        self.assertNotIn("aors=outbound-primary,outbound-secondary", body)
        self.assertEqual(body.count("max_contacts=1"), 2)
        self.assertIn('signalTarget("TRUNK_SIGNAL_CIDR_PRIMARY")', body)
        self.assertIn('signalTarget("TRUNK_SIGNAL_CIDR_SECONDARY")', body)
        self.assertIn(
            "name: dialer-network-values",
            read("base/engine/statefulset-init.yaml"),
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
