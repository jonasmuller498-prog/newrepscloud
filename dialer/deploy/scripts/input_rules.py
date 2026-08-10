import ipaddress
import re
import stat
import urllib.parse

KEY_RE = re.compile(r"^[A-Z][A-Z0-9_]*$")
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
    return network


def sip_target(uri):
    if not SIP_RE.fullmatch(uri):
        fail("trunk SIP URIs must be exact sip:[account@]IPv4:port targets")
    parsed = urllib.parse.urlsplit("//" + uri.removeprefix("sip:"))
    try:
        address, port = ipaddress.ip_address(parsed.hostname), parsed.port
    except (TypeError, ValueError):
        fail("trunk SIP URIs must use exact IPv4 SBC targets and valid ports")
    if address.version != 4 or not 1 <= port <= 65535:
        fail("trunk SIP URIs must use exact IPv4 SBC targets and valid ports")
    return address
