#!/usr/bin/env python3
import pathlib
import re
import stat
import sys

FILES = {
    "runtime.env": {
        "DIALING_ENABLED", "CPS", "MAX_CONCURRENCY", "HTTP_ADDR",
        "METRICS_ADDR", "MEDIA_DIR", "EVENT_JOURNAL_DIR", "ARI_URL",
        "ARI_APP", "ARI_ENDPOINT", "DIALER_SOURCE_REPOSITORY",
        "DIALER_SOURCE_REF",
    },
    "safety.env": {
        "BACKUP_STATUS", "BACKUP_DESTINATION", "BACKUP_ACKNOWLEDGED",
    },
    "postgres-admin.env": {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"},
    "postgres-runtime.env": {"DB_USER", "DB_PASSWORD", "DB_NAME", "DATABASE_URL"},
    "app.env": {
        "OPERATOR_API_TOKEN", "APPROVER_API_TOKEN", "PHONE_HASH_KEY",
        "FIELD_ENCRYPTION_KEY", "AUDIT_HMAC_KEY",
    },
    "ari.env": {"ARI_USER", "ARI_PASSWORD"},
    "trunk.env": {"DIALER_TRUNK_ENABLED"},
}
RUNTIME = {
    "DIALING_ENABLED": "false",
    "CPS": "0",
    "MAX_CONCURRENCY": "20",
    "HTTP_ADDR": ":8080",
    "METRICS_ADDR": ":9090",
    "MEDIA_DIR": "/media",
    "EVENT_JOURNAL_DIR": "/media/ari-journal",
    "ARI_URL": "http://127.0.0.1:8088/ari",
    "ARI_APP": "voice-dialer",
    "ARI_ENDPOINT": "outbound",
    "DIALER_SOURCE_REPOSITORY":
        "https://github.com/jonasmuller498-prog/newrepscloud.git",
}
HEX64 = re.compile(r"^[0-9a-f]{64}$")
SHA = re.compile(r"^[0-9a-f]{40}$")


def fail(message):
    raise ValueError(message)


def load(path):
    if not path.is_file() or path.is_symlink():
        fail(f"{path.name} is missing or not a regular file")
    if stat.S_IMODE(path.stat().st_mode) != 0o600:
        fail(f"{path.name} must have mode 0600")
    values = {}
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            fail(f"{path.name}:{number} is not KEY=VALUE")
        key, value = line.split("=", 1)
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or key in values:
            fail(f"{path.name}:{number} has an invalid or duplicate key")
        if not value or re.search(r"[\x00\r\n]", value):
            fail(f"{path.name}:{number} has an unsafe value")
        values[key] = value
    if set(values) != FILES[path.name]:
        fail(f"{path.name} has an unexpected environment contract")
    return values


def check(directory):
    present = {path.name for path in directory.glob("*.env")}
    if present != set(FILES):
        fail(f"expected only {sorted(FILES)}, got {sorted(present)}")
    data = {name: load(directory / name) for name in FILES}
    runtime = data["runtime.env"]
    if any(runtime.get(key) != value for key, value in RUNTIME.items()):
        fail("fixed staging runtime values changed")
    source_ref = runtime["DIALER_SOURCE_REF"]
    if not SHA.fullmatch(source_ref) or len(set(source_ref)) == 1:
        fail("source ref must be a lowercase, non-placeholder commit")
    if data["safety.env"] != {
        "BACKUP_STATUS": "unconfigured-suspended",
        "BACKUP_DESTINATION": "UNCONFIGURED",
        "BACKUP_ACKNOWLEDGED": "false",
    }:
        fail("backups must remain explicitly unconfigured and unacknowledged")
    admin, db = data["postgres-admin.env"], data["postgres-runtime.env"]
    if admin["POSTGRES_USER"] != "postgres" or admin["POSTGRES_DB"] != "dialer_staging":
        fail("PostgreSQL admin identity changed")
    if db["DB_USER"] != "dialer_staging_app" or db["DB_NAME"] != "dialer_staging":
        fail("PostgreSQL runtime identity changed")
    expected_url = (
        f"postgres://dialer_staging_app:{db['DB_PASSWORD']}"
        "@postgres:5432/dialer_staging?sslmode=disable"
    )
    if db["DATABASE_URL"] != expected_url:
        fail("runtime database URL changed")
    if data["ari.env"]["ARI_USER"] != "dialer_ari":
        fail("ARI user changed")
    if data["trunk.env"] != {"DIALER_TRUNK_ENABLED": "false"}:
        fail("trunk input must contain only disabled enablement")
    random_values = [
        admin["POSTGRES_PASSWORD"], db["DB_PASSWORD"],
        *data["app.env"].values(), data["ari.env"]["ARI_PASSWORD"],
    ]
    if any(not HEX64.fullmatch(value) for value in random_values):
        fail("all generated credentials and keys must be 256-bit lowercase hex")
    if len(set(random_values)) != len(random_values):
        fail("generated credentials and keys must be independent")
    encoded = "\n".join(value for values in data.values() for value in values.values())
    if re.search(r"(?:192\.0\.2|198\.51\.100|203\.0\.113)\.", encoded):
        fail("staging inputs must not contain TEST-NET or carrier substitutes")


def main():
    try:
        check(pathlib.Path(sys.argv[1]))
    except (IndexError, OSError, ValueError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("PASS: staging-disabled inputs are strong and fixed fail-closed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
