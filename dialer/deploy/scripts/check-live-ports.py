#!/usr/bin/env python3
import json
import pathlib
import sys

DESIRED = {31100, *range(32300, 32500)}


def expected_owner(port):
    if port == 31100:
        return ("voice-dialer-production", "dialer-sip")
    start = 32300 + ((port - 32300) // 20) * 20
    return ("voice-dialer-production", f"dialer-rtp-{start}-{start + 19}")


def main():
    if len(sys.argv) != 2:
        print("usage: check-live-ports.py SERVICES_JSON", file=sys.stderr)
        return 64
    payload = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    conflicts = []
    for service in payload.get("items", []):
        owner = (
            service.get("metadata", {}).get("namespace", "default"),
            service.get("metadata", {}).get("name", ""),
        )
        spec = service.get("spec", {})
        for port_spec in spec.get("ports", []):
            port = port_spec.get("nodePort")
            if port in DESIRED and owner != expected_owner(port):
                conflicts.append(f"{port} nodePort is held by {owner[0]}/{owner[1]}")
        health_port = spec.get("healthCheckNodePort")
        if health_port in DESIRED:
            conflicts.append(
                f"{health_port} healthCheckNodePort is held by {owner[0]}/{owner[1]}"
            )
    if conflicts:
        print("ERROR: live cluster port conflicts:", file=sys.stderr)
        for conflict in sorted(set(conflicts)):
            print(f"  - {conflict}", file=sys.stderr)
        return 1
    print("PASS: live nodePort and healthCheckNodePort claims are conflict-free")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
