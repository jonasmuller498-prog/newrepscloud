#!/usr/bin/env python3
import argparse
import pathlib
import re
import sys
import urllib.parse

from input_rules import exact, fail, load, public_cidr, sip_target

FILES = {
    "runtime.env": {
        "DIALING_ENABLED", "CPS", "MAX_CONCURRENCY", "HTTP_ADDR",
        "METRICS_ADDR", "MEDIA_DIR", "EVENT_JOURNAL_DIR",
        "ARI_URL", "ARI_APP", "ARI_DIAL_CONTEXT",
        "DIALER_SOURCE_REPOSITORY", "DIALER_SOURCE_REF",
    },
    "network.env": {"TRUNK_SIGNAL_CIDR_PRIMARY", "TRUNK_SIGNAL_CIDR_SECONDARY", "TRUNK_MEDIA_CIDR"},
    "safety.env": {"BACKUP_STATUS", "BACKUP_DESTINATION", "BACKUP_ACKNOWLEDGED"},
    "postgres-admin.env": {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"},
    "postgres-runtime.env": {"DB_USER", "DB_PASSWORD", "DB_NAME", "DATABASE_URL"},
    "app.env": {
        "OPERATOR_API_TOKEN", "APPROVER_API_TOKEN", "PHONE_HASH_KEY",
        "FIELD_ENCRYPTION_KEY", "AUDIT_HMAC_KEY",
    },
    "ari.env": {"ARI_USER", "ARI_PASSWORD"},
}
TRUNK_KEYS = {
    "DIALER_TRUNK_ENABLED", "DIALER_TRUNK_AUTH_MODE",
    "DIALER_TRUNK_SIP_URI_PRIMARY", "DIALER_TRUNK_SIP_URI_SECONDARY",
    "DIALER_TRUNK_USERNAME", "DIALER_TRUNK_PASSWORD", "DIALER_TRUNK_REALM",
}
HEX64_RE = re.compile(r"^[0-9a-fA-F]{64}$")
NAME_RE = re.compile(r"^[A-Za-z0-9_.-]+$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
REPO_RE = re.compile(r"^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$")


def check_database(admin, runtime):
    if admin["POSTGRES_USER"] != "postgres":
        fail("POSTGRES_USER must be postgres for local peer authentication")
    for key in ("POSTGRES_PASSWORD", "DB_PASSWORD"):
        if not HEX64_RE.fullmatch((admin | runtime)[key]):
            fail(f"{key} must be generated 64-character hex")
    if admin["POSTGRES_USER"] == runtime["DB_USER"]:
        fail("PostgreSQL superuser and runtime role must differ")
    if admin["POSTGRES_DB"] != runtime["DB_NAME"]:
        fail("admin and runtime database names must match")
    for key in ("POSTGRES_USER", "POSTGRES_DB", "DB_USER", "DB_NAME"):
        if not NAME_RE.fullmatch((admin | runtime)[key]):
            fail(f"{key} has unsupported characters")
    url = urllib.parse.urlsplit(runtime["DATABASE_URL"])
    if (url.scheme, url.hostname, url.port) != ("postgres", "postgres", 5432):
        fail("DATABASE_URL must target postgres:5432")
    if url.username != runtime["DB_USER"] or url.password != runtime["DB_PASSWORD"]:
        fail("DATABASE_URL must use the runtime account")
    if url.path != "/" + runtime["DB_NAME"] or url.query != "sslmode=disable":
        fail("DATABASE_URL database or sslmode is invalid")


def check_trunk(values, signals):
    if values.get("DIALER_TRUNK_ENABLED") not in {"true", "false"}:
        fail("DIALER_TRUNK_ENABLED must be true or false")
    if not set(values).issubset(TRUNK_KEYS):
        fail("trunk.env contains unsupported keys")
    if values["DIALER_TRUNK_ENABLED"] == "false":
        return
    for key in ("DIALER_TRUNK_AUTH_MODE", "DIALER_TRUNK_SIP_URI_PRIMARY",
                "DIALER_TRUNK_SIP_URI_SECONDARY"):
        if not values.get(key):
            fail(f"{key} is required when the trunk is enabled")
    if values["DIALER_TRUNK_AUTH_MODE"] not in {"digest", "ip"}:
        fail("trunk auth mode must be digest or ip")
    uris = (values["DIALER_TRUNK_SIP_URI_PRIMARY"], values["DIALER_TRUNK_SIP_URI_SECONDARY"])
    if uris[0] == uris[1]:
        fail("outbound SBC URIs must be distinct")
    targets = tuple(sip_target(uri) for uri in uris)
    if any(target != signal.network_address for target, signal in zip(targets, signals)):
        fail("each trunk SIP target must match its paired signaling /32")
    if values["DIALER_TRUNK_AUTH_MODE"] == "digest":
        for key in ("DIALER_TRUNK_USERNAME", "DIALER_TRUNK_PASSWORD", "DIALER_TRUNK_REALM"):
            if not values.get(key):
                fail(f"{key} is required for digest auth")
    elif values.keys() & {"DIALER_TRUNK_USERNAME", "DIALER_TRUNK_PASSWORD"}:
        fail("IP auth must not include digest credentials")


def check(directory, allow_test):
    data = {}
    for filename, keys in FILES.items():
        data[filename] = load(directory / filename)
        exact(data[filename], keys, filename)
    trunk = load(directory / "trunk.env")
    runtime, network = data["runtime.env"], data["network.env"]
    fixed = {
        "HTTP_ADDR": ":8080", "METRICS_ADDR": ":9090", "MEDIA_DIR": "/media",
        "EVENT_JOURNAL_DIR": "/media/ari-journal",
        "ARI_URL": "http://127.0.0.1:8088/ari", "ARI_APP": "voice-dialer",
        "ARI_DIAL_CONTEXT": "dialer-outbound",
    }
    if any(runtime[key] != value for key, value in fixed.items()):
        fail("runtime addresses, media path, or direct ARI settings changed")
    if runtime["DIALING_ENABLED"] not in {"true", "false"}:
        fail("DIALING_ENABLED must be true or false")
    cps, concurrency = float(runtime["CPS"]), int(runtime["MAX_CONCURRENCY"])
    if cps < 0 or cps > 100 or concurrency < 1 or concurrency > 100:
        fail("CPS or MAX_CONCURRENCY is outside its safety limit")
    if not REPO_RE.fullmatch(runtime["DIALER_SOURCE_REPOSITORY"]):
        fail("source repository must be public GitHub HTTPS")
    ref = runtime["DIALER_SOURCE_REF"]
    if not SHA_RE.fullmatch(ref) or ref == "0" * 40:
        fail("DIALER_SOURCE_REF must be a lowercase immutable commit SHA")
    if not allow_test and len(set(ref)) == 1:
        fail("DIALER_SOURCE_REF looks like a placeholder")
    signals = tuple(public_cidr(network[key], allow_test) for key in (
        "TRUNK_SIGNAL_CIDR_PRIMARY", "TRUNK_SIGNAL_CIDR_SECONDARY"))
    public_cidr(network["TRUNK_MEDIA_CIDR"], allow_test)
    if any(signal.prefixlen != 32 for signal in signals):
        fail("carrier signaling CIDRs must be exact IPv4 /32 networks")
    if signals[0] == signals[1]:
        fail("carrier signaling CIDRs must be distinct")
    check_trunk(trunk, signals)
    app, ari = data["app.env"], data["ari.env"]
    if any(not HEX64_RE.fullmatch(value) for value in app.values()):
        fail("application secrets must be independent 64-character hex values")
    if len(set(app.values())) != len(app):
        fail("application secrets must be independent")
    if not NAME_RE.fullmatch(ari["ARI_USER"]) or not HEX64_RE.fullmatch(ari["ARI_PASSWORD"]):
        fail("ARI credentials are malformed")
    check_database(data["postgres-admin.env"], data["postgres-runtime.env"])
    safety = data["safety.env"]
    if safety["BACKUP_ACKNOWLEDGED"] not in {"true", "false"}:
        fail("BACKUP_ACKNOWLEDGED must be true or false")
    if safety["BACKUP_STATUS"] not in {"unconfigured-suspended", "configured-suspended"}:
        fail("BACKUP_STATUS must explicitly report configured or unconfigured suspension")
    if safety["BACKUP_DESTINATION"] != "UNCONFIGURED" and re.search(r"[@?#]", safety["BACKUP_DESTINATION"]):
        fail("BACKUP_DESTINATION must be a non-secret target identifier")
    if safety["BACKUP_ACKNOWLEDGED"] == "true":
        if safety["BACKUP_STATUS"] != "configured-suspended":
            fail("acknowledged backups require configured-suspended status")
        if safety["BACKUP_DESTINATION"] == "UNCONFIGURED":
            fail("acknowledged backups require an external destination")
    if runtime["DIALING_ENABLED"] == "true":
        if cps <= 0 or trunk["DIALER_TRUNK_ENABLED"] != "true":
            fail("production dialing requires positive CPS and an enabled exact trunk")
        if safety["BACKUP_ACKNOWLEDGED"] != "true" or safety["BACKUP_DESTINATION"] == "UNCONFIGURED":
            fail("production dialing requires an acknowledged external backup destination")
        if not safety["BACKUP_STATUS"].startswith("configured"):
            fail("production dialing requires configured backup status")

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=pathlib.Path)
    parser.add_argument("--allow-test-net", action="store_true")
    args = parser.parse_args()
    try:
        check(args.directory, args.allow_test_net)
    except (ValueError, OSError, TypeError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("PASS: production inputs satisfy fail-closed policy")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
