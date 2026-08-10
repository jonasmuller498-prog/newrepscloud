#!/usr/bin/env python3
import base64
import binascii
import json
import pathlib
import re
import sys

SECRET_KEYS = {
    "dialer-postgres-admin-": {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"},
    "dialer-postgres-runtime-": {"DB_USER", "DB_PASSWORD", "DB_NAME", "DATABASE_URL"},
    "dialer-app-secrets-": {
        "OPERATOR_API_TOKEN", "APPROVER_API_TOKEN", "PHONE_HASH_KEY",
        "FIELD_ENCRYPTION_KEY", "AUDIT_HMAC_KEY",
    },
    "dialer-ari-secrets-": {"ARI_USER", "ARI_PASSWORD"},
    "dialer-trunk-secrets-": {"DIALER_TRUNK_ENABLED"},
}
RUNTIME = {
    "DIALING_ENABLED": "false", "CPS": "0", "MAX_CONCURRENCY": "20",
    "HTTP_ADDR": ":8080", "METRICS_ADDR": ":9090", "MEDIA_DIR": "/media",
    "EVENT_JOURNAL_DIR": "/media/ari-journal",
    "ARI_URL": "http://127.0.0.1:8088/ari", "ARI_APP": "voice-dialer",
    "ARI_ENDPOINT": "outbound",
    "DIALER_SOURCE_REPOSITORY":
        "https://github.com/jonasmuller498-prog/newrepscloud.git",
}


def fail(message):
    raise ValueError(message)


def documents(path):
    raw = path.read_text(encoding="utf-8")
    decoder, offset, result = json.JSONDecoder(), 0, []
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset == len(raw):
            break
        value, offset = decoder.raw_decode(raw, offset)
        result.extend(value.get("items", [])) if value.get("kind") == "List" else result.append(value)
    return result


def one(items, kind, name=None, prefix=None):
    matches = [
        item for item in items
        if item.get("kind") == kind
        and (name is None or item["metadata"]["name"] == name)
        and (prefix is None or item["metadata"]["name"].startswith(prefix))
    ]
    if len(matches) != 1:
        fail(f"expected one {kind} {name or prefix}, found {len(matches)}")
    return matches[0]


def secret_data(secret):
    try:
        return {
            key: base64.b64decode("".join(value.split()), validate=True).decode("utf-8")
            for key, value in secret.get("data", {}).items()
        }
    except (binascii.Error, UnicodeDecodeError) as error:
        fail(f"{secret['metadata']['name']} contains invalid secret data: {error}")


def ports(service):
    return service.get("spec", {}).get("ports", [])


def check(items):
    encoded = json.dumps(items, sort_keys=True)
    blocked = (
        "192.0.2.", "198.51.100.", "203.0.113.",
        "TRUNK_SIGNAL_CIDR", "TRUNK_MEDIA_CIDR",
    )
    if any(value in encoded for value in blocked):
        fail("render contains TEST-NET or carrier CIDR values")
    services = [item for item in items if item.get("kind") == "Service"]
    for service in services:
        spec, name = service.get("spec", {}), service["metadata"]["name"]
        if spec.get("type", "ClusterIP") in {"NodePort", "LoadBalancer"}:
            fail(f"{name} exposes a public service type")
        if spec.get("healthCheckNodePort") or any("nodePort" in port for port in ports(service)):
            fail(f"{name} retains a public port")
        if name == "dialer-sip" or name.startswith("dialer-rtp-"):
            fail(f"{name} was not deleted")
    policies = [item for item in items if item.get("kind") == "NetworkPolicy"]
    if any(item["metadata"]["name"] == "allow-carrier-ingress" for item in policies):
        fail("carrier ingress policy remains")
    egress = one(items, "NetworkPolicy", name="allow-engine-egress")["spec"]["egress"]
    if len(egress) != 3:
        fail("engine egress must contain only DNS, PostgreSQL, and HTTPS rules")
    for rule in egress:
        for port in rule.get("ports", []):
            if port.get("endPort") is not None:
                fail("engine egress retains a media port range")
            if port.get("protocol", "TCP") == "UDP" and port.get("port") != 53:
                fail("engine egress retains non-DNS UDP")

    runtime = one(items, "ConfigMap", prefix="dialer-runtime-").get("data", {})
    if any(runtime.get(key) != value for key, value in RUNTIME.items()):
        fail("rendered runtime differs from fixed staging contract")
    if not re.fullmatch(r"[0-9a-f]{40}", runtime.get("DIALER_SOURCE_REF", "")):
        fail("rendered source ref is not an immutable commit")
    safety = one(items, "ConfigMap", prefix="dialer-safety-status-").get("data", {})
    if safety != {
        "BACKUP_STATUS": "unconfigured-suspended",
        "BACKUP_DESTINATION": "UNCONFIGURED",
        "BACKUP_ACKNOWLEDGED": "false",
    }:
        fail("rendered backup state is not unconfigured and suspended")
    network = one(items, "ConfigMap", prefix="dialer-network-values-").get("data", {})
    if network != {"NETWORK_MODE": "staging-disabled"}:
        fail("carrier-free network marker changed")

    decoded = {}
    for prefix, keys in SECRET_KEYS.items():
        secret = one(items, "Secret", prefix=prefix)
        if secret.get("immutable") is not True:
            fail(f"{prefix} Secret must be immutable")
        decoded[prefix] = secret_data(secret)
        if set(decoded[prefix]) != keys:
            fail(f"{prefix} Secret keys changed")
    if decoded["dialer-trunk-secrets-"] != {"DIALER_TRUNK_ENABLED": "false"}:
        fail("rendered trunk is not disabled-only")
    hex_values = [
        decoded["dialer-postgres-admin-"]["POSTGRES_PASSWORD"],
        decoded["dialer-postgres-runtime-"]["DB_PASSWORD"],
        *decoded["dialer-app-secrets-"].values(),
        decoded["dialer-ari-secrets-"]["ARI_PASSWORD"],
    ]
    if any(not re.fullmatch(r"[0-9a-f]{64}", value) for value in hex_values):
        fail("rendered credentials are not generated 256-bit values")
    if len(set(hex_values)) != len(hex_values):
        fail("rendered credentials are not independent")

    asterisk = one(items, "ConfigMap", prefix="asterisk-config-").get("data", {})
    pjsip = asterisk.get("pjsip.conf", "")
    if any(value in pjsip for value in ("[outbound]", "type=endpoint", "type=transport", "bind=")):
        fail("Asterisk staging config contains a network transport or endpoint")
    if "@@" in pjsip or "bindaddr=127.0.0.1" not in asterisk.get("http.conf", ""):
        fail("Asterisk config has unresolved tokens or non-loopback ARI")
    engine = one(items, "StatefulSet", name="dialer-engine")
    pod = engine["spec"]["template"]["spec"]
    containers = {item["name"]: item for item in pod["containers"]}
    if set(containers) != {"app", "asterisk"}:
        fail("engine must retain the app and local Asterisk containers")
    if containers["asterisk"].get("ports"):
        fail("Asterisk container advertises a SIP port")
    app_mounts = {item["mountPath"]: item["name"] for item in containers["app"]["volumeMounts"]}
    ast_mounts = {item["mountPath"]: item["name"] for item in containers["asterisk"]["volumeMounts"]}
    if app_mounts.get("/media") != "media":
        fail("app media path is not on the shared volume")
    if ast_mounts.get("/var/lib/asterisk/sounds/campaigns") != "media":
        fail("Asterisk media path is not on the shared volume")
    init_names = {item["name"] for item in pod.get("initContainers", [])}
    if not {"render-asterisk-config", "build-dialer-app"} <= init_names:
        fail("engine no longer renders Asterisk and builds the app")
    engine_secrets = ("dialer-postgres-runtime-", "dialer-app-secrets-",
                      "dialer-ari-secrets-", "dialer-trunk-secrets-")
    if any(one(items, "Secret", prefix=prefix)["metadata"]["name"] not in json.dumps(engine)
           for prefix in engine_secrets):
        fail("engine does not reference every required runtime Secret")
    postgres = one(items, "StatefulSet", name="postgres")
    postgres_secrets = ("dialer-postgres-admin-", "dialer-postgres-runtime-")
    if any(one(items, "Secret", prefix=prefix)["metadata"]["name"]
           not in json.dumps(postgres) for prefix in postgres_secrets):
        fail("PostgreSQL does not reference admin and runtime Secrets")
    api, metrics = one(items, "Service", name="dialer-api"), one(
        items, "Service", name="dialer-metrics")
    if not any(port.get("port") == 8080 for port in ports(api)):
        fail("API service is missing")
    if not any(port.get("port") == 9090 for port in ports(metrics)):
        fail("private metrics service is missing")
    if any(port.get("port") == 8088 for service in services for port in ports(service)):
        fail("ARI is exposed by a Service")
    ingress = one(items, "Ingress", name="dialer")
    if not ingress.get("spec", {}).get("tls") or "dialer-metrics" in json.dumps(ingress):
        fail("HTTPS ingress or private metrics boundary changed")
    backup = one(items, "CronJob", name="postgres-logical-backup")
    if backup["spec"].get("suspend") is not True:
        fail("logical backup CronJob must remain suspended")

def main():
    try:
        check(documents(pathlib.Path(sys.argv[1])))
    except (IndexError, KeyError, OSError, ValueError, json.JSONDecodeError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("PASS: staging-disabled render has no carrier or public dialing path")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
