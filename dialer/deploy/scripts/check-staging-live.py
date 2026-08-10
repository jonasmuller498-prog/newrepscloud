#!/usr/bin/env python3
import json
import pathlib
import sys


def check(payload):
    violations = []
    for item in payload.get("items", []):
        kind = item.get("kind")
        metadata, spec = item.get("metadata", {}), item.get("spec", {})
        name = metadata.get("name", "")
        if kind == "Service":
            component = metadata.get("labels", {}).get("app.kubernetes.io/component")
            public = spec.get("type", "ClusterIP") != "ClusterIP"
            allocated = spec.get("healthCheckNodePort") or any(
                "nodePort" in port for port in spec.get("ports", [])
            )
            if public or allocated or component in {"sip", "rtp"}:
                violations.append(f"Service/{name} retains a carrier or public port")
        if kind != "NetworkPolicy":
            continue
        if name == "allow-carrier-ingress":
            violations.append("NetworkPolicy/allow-carrier-ingress still exists")
        if name == "allow-engine-egress":
            for rule in spec.get("egress", []):
                for port in rule.get("ports", []):
                    if port.get("protocol", "TCP") == "UDP" and port.get("port") != 53:
                        violations.append("NetworkPolicy/allow-engine-egress retains carrier UDP")
    if violations:
        raise ValueError("; ".join(sorted(set(violations))))


def main():
    if len(sys.argv) != 2:
        print("usage: check-staging-live.py RESOURCES_JSON", file=sys.stderr)
        return 64
    try:
        payload = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
        check(payload)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"ERROR: unsafe live staging state: {error}", file=sys.stderr)
        return 1
    print("PASS: live staging has no carrier or public telephony resources")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
