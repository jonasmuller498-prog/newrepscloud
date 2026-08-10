#!/usr/bin/env python3
import argparse
import ipaddress
import pathlib
import re
import stat
import sys
import urllib.parse

FILES = {
    "runtime.env": {
        "DIALING_ENABLED", "CPS", "MAX_CONCURRENCY", "HTTP_ADDR",
        "METRICS_ADDR", "MEDIA_DIR", "ARI_URL", "ARI_APP", "ARI_ENDPOINT",
        "DIALER_SOURCE_REPOSITORY", "DIALER_SOURCE_REF",
    },
    "network.env": {"TRUNK_SIGNAL_CIDR", "TRUNK_MEDIA_CIDR"},
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
    "DIALER_TRUNK_ENABLED", "DIALER_TRUNK_AUTH_MODE", "DIALER_TRUNK_SIP_URI",
    "DIALER_TRUNK_USERNAME", "DIALER_TRUNK_PASSWORD", "DIALER_TRUNK_REALM",
}
KEY_RE = re.compile(r"^[A-Z][A-Z0-9_]*$")
HEX64_RE = re.compile(r"^[0-9a-fA-F]{64}$")
NAME_RE = re.compile(r"^[A-Za-z0-9_.-]+$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
REPO_RE = re.compile(r"^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$")
SIP_RE = re.compile(r"^sip:(?:[A-Za-z0-9+_.%-]+@)?[A-Za-z0-9.-]+:[0-9]{2,5}$")


def fail(message):
    raise ValueError(message)


def load(path):
    if not path.is_file() or path.is_symlink():
        fail(f"{path.name} is missing or not a regular file")
    if stat.S_IMODE(path.stat().st_mode) & 0o077:
        fail(f"{path.name} must not be group/world accessible")
    values = {}
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            fail(f"{path.name}:{number} is not KEY=VALUE")
        key, value = line.split("=", 1)
        if not KEY_RE.fullmatch(key) or key in values:
            fail(f"{path.name}:{number} has an invalid or duplicate key")
        if not value or "\x00" in value or "\r" in value:
            fail(f"{path.name}:{number} has an empty or unsafe value")
        if re.search(r"REQUIRED_|CHANGEME|disabled\.invalid", value, re.I):
            fail(f"{path.name}:{number} contains a placeholder")
        values[key] = value
    return values


def exact(values, expected, filename):
    if set(values) != expected:
        fail(f"{filename} keys differ: expected {sorted(expected)}, got {sorted(values)}")


def public_cidr(value, allow_test):
    network = ipaddress.ip_network(value, strict=True)
    if network.version != 4:
        fail("carrier CIDRs must be IPv4")
    if not allow_test and not network.is_global:
        fail("carrier CIDRs must be canonical public networks")


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


def check_trunk(values):
    if values.get("DIALER_TRUNK_ENABLED") not in {"true", "false"}:
        fail("DIALER_TRUNK_ENABLED must be true or false")
    if not set(values).issubset(TRUNK_KEYS):
        fail("trunk.env contains unsupported keys")
    if values["DIALER_TRUNK_ENABLED"] == "false":
        return
    for key in ("DIALER_TRUNK_AUTH_MODE", "DIALER_TRUNK_SIP_URI"):
        if not values.get(key):
            fail(f"{key} is required when the trunk is enabled")
    if values["DIALER_TRUNK_AUTH_MODE"] not in {"digest", "ip"}:
        fail("trunk auth mode must be digest or ip")
    uri = values["DIALER_TRUNK_SIP_URI"]
    blocked = r"(?:\.(?:invalid|example|test|localhost)|@localhost|sip:localhost)(?::|$)"
    if not SIP_RE.fullmatch(uri) or re.search(blocked, uri):
        fail("trunk SIP URI must be exact sip:[account@]host:port")
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
    check_trunk(trunk)
    runtime, network = data["runtime.env"], data["network.env"]
    fixed = {
        "HTTP_ADDR": ":8080", "METRICS_ADDR": ":9090", "MEDIA_DIR": "/media",
        "ARI_URL": "http://127.0.0.1:8088/ari", "ARI_APP": "voice-dialer",
        "ARI_ENDPOINT": "PJSIP/%s@outbound",
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
    for value in network.values():
        public_cidr(value, allow_test)
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
