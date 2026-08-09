#!/usr/bin/env python3
"""Explore a Voxco instance via API for CATI/Phone dialing capability.

Usage:
  export VOXCO_API_KEY='YOUR_KEY'
  python3 voxco_cati_discovery.py
"""

from __future__ import annotations

import json
import os
import sys
import time
from typing import Any

import requests

BASE = os.environ.get("VOXCO_BASE_URL", "https://us1.voxco.com").rstrip("/")
CATI_BASE = os.environ.get("VOXCO_CATI_BASE_URL", "https://us1intweb.voxco.com").rstrip("/")
API_KEY = os.environ.get("VOXCO_API_KEY", "").strip()

AUTH_METHODS = [
    ("Authorization: Client", lambda k: {"Authorization": f"Client {k}"}),
    ("x-api-key", lambda k: {"x-api-key": k}),
    ("Authorization: Bearer", lambda k: {"Authorization": f"Bearer {k}"}),
]

PHONE_FIELDS = ("mode", "phone", "cati", "dialer", "interviewer", "telephony")
LICENSE_KEYWORDS = (
    "cati",
    "phone",
    "dialer",
    "interviewer",
    "telephony",
    "voice",
    "pronto",
    "agent",
)
PERMISSION_KEYWORDS = ("cati", "phone", "dialer", "interviewer", "callcenter", "remoteagent")

SESSION = requests.Session()
SESSION.headers.update(
    {
        "Accept": "application/json, text/plain, */*",
        "Accept-Version": "1.0",
        "User-Agent": "voxco-cati-discovery/1.1",
    }
)

RESULTS: list[dict[str, Any]] = []
SUCCESS_URLS: list[str] = []
PHONE_SURVEYS: list[Any] = []
LICENSE_HITS: list[Any] = []
PERMISSION_HITS: list[Any] = []
CONTEXT_HITS: list[Any] = []
WORKING_AUTH: str | None = None


def pretty(data: Any) -> str:
    if isinstance(data, (dict, list)):
        return json.dumps(data, indent=2, ensure_ascii=False)
    return str(data)


def contains_keywords(obj: Any, keywords: tuple[str, ...]) -> list[str]:
    found: set[str] = set()
    blob = pretty(obj).lower()
    for kw in keywords:
        if kw in blob:
            found.add(kw)
    return sorted(found)


def extract_surveys(payload: Any) -> list[Any]:
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        for key in ("items", "surveys", "Surveys", "data", "results", "value"):
            if isinstance(payload.get(key), list):
                return payload[key]
        if any(k in payload for k in ("id", "Id", "surveyId", "name", "Name", "title")):
            return [payload]
    return []


def survey_has_phone_fields(survey: Any) -> dict[str, Any] | None:
    if not isinstance(survey, dict):
        return None
    hits = {}
    for key, value in survey.items():
        key_l = str(key).lower()
        if any(field in key_l for field in PHONE_FIELDS):
            hits[key] = value
    # Name/description only (avoid base64 token false positives)
    name_blob = f"{survey.get('Name', '')} {survey.get('Description', '')}".lower()
    name_hits = [f for f in PHONE_FIELDS if f in name_blob]
    if hits or name_hits:
        return {"fields": hits, "name_keywords": name_hits, "survey_id": survey.get("Id"), "name": survey.get("Name")}
    return None


def request_with_auth(url: str) -> dict[str, Any]:
    global WORKING_AUTH

    methods = AUTH_METHODS
    if WORKING_AUTH:
        methods = sorted(AUTH_METHODS, key=lambda m: 0 if m[0] == WORKING_AUTH else 1)

    last: dict[str, Any] = {
        "url": url,
        "status": None,
        "auth_tried": [],
        "body": None,
        "error": None,
        "success": False,
    }

    for auth_name, header_fn in methods:
        headers = header_fn(API_KEY)
        try:
            resp = SESSION.get(url, headers=headers, timeout=60)
            try:
                body: Any = resp.json()
            except Exception:
                body = resp.text

            entry = {
                "url": url,
                "status": resp.status_code,
                "auth": auth_name,
                "content_type": resp.headers.get("Content-Type", ""),
                "body": body if not isinstance(body, str) or len(body) < 20000 else body[:20000],
                "success": resp.status_code == 200,
                "auth_tried": [auth_name],
            }

            print("=" * 80)
            print(f"URL: {url}")
            print(f"AUTH: {auth_name}")
            print(f"STATUS: {resp.status_code}")
            if resp.status_code == 200:
                print(">>> SUCCESS <<<")
                WORKING_AUTH = auth_name
                SUCCESS_URLS.append(url)
                print(pretty(body)[:8000])
                RESULTS.append(entry)
                return entry

            if resp.status_code in (401, 403):
                print(f"AUTH FAILED ({resp.status_code}) with {auth_name}")
                print(pretty(body)[:2000])
                last = {
                    **entry,
                    "auth_tried": (last.get("auth_tried") or []) + [auth_name],
                }
                time.sleep(1)
                continue

            if resp.status_code == 404:
                print("404 Not Found — skipping")
                print(pretty(body)[:1000])
                RESULTS.append(entry)
                return entry

            print(pretty(body)[:4000])
            RESULTS.append(entry)
            return entry

        except requests.RequestException as exc:
            print("=" * 80)
            print(f"URL: {url}")
            print(f"AUTH: {auth_name}")
            print(f"EXCEPTION: {exc}")
            last = {
                "url": url,
                "status": None,
                "auth": auth_name,
                "body": None,
                "error": str(exc),
                "success": False,
                "auth_tried": (last.get("auth_tried") or []) + [auth_name],
            }
            time.sleep(1)
            continue

    RESULTS.append(last)
    return last


def step(title: str) -> None:
    print("\n")
    print("#" * 80)
    print(f"# {title}")
    print("#" * 80)


def main() -> int:
    if not API_KEY:
        print("ERROR: set VOXCO_API_KEY environment variable", file=sys.stderr)
        return 2

    # STEP 1 — requested probes + real swagger discovery
    step("STEP 1 — Discover available API endpoints")
    step1_urls = [
        f"{BASE}/api/v1/",
        f"{BASE}/api/v2/",
        f"{BASE}/api/",
        f"{BASE}/A4S/api/v1/",
        f"{BASE}/A4S/api/v2/",
        f"{BASE}/api/V1.0/swagger/docs",
    ]
    for url in step1_urls:
        request_with_auth(url)
        time.sleep(1)

    # STEP 2
    step("STEP 2 — Check survey modes and phone settings")
    step2_urls = [
        f"{BASE}/api/v1/surveys?mode=phone",
        f"{BASE}/api/v1/surveys?type=cati",
        f"{BASE}/api/v1/surveys",
        f"{BASE}/api/v2/surveys",
        f"{BASE}/api/users/user/surveys",
    ]
    for url in step2_urls:
        result = request_with_auth(url)
        if result.get("success"):
            for survey in extract_surveys(result.get("body")):
                hit = survey_has_phone_fields(survey)
                if hit:
                    PHONE_SURVEYS.append({"source": url, **hit})
        time.sleep(1)

    # STEP 3
    step("STEP 3 — Check for CATI specific endpoints")
    step3_urls = [
        f"{BASE}/api/v1/cati/",
        f"{BASE}/api/v1/dialer/",
        f"{BASE}/api/v1/interviewer/",
        f"{BASE}/api/v1/callcenter/",
        f"{BASE}/api/v1/phone/",
        f"{BASE}/api/v1/projects/",
        f"{BASE}/api/v1/cases/",
        f"{BASE}/api/v2/cati/",
        f"{BASE}/api/v2/projects/",
        f"{BASE}/api/license/counter",
        f"{BASE}/api/license/counter/ConcurrentAgents",
        f"{BASE}/api/license/counter/PhoneCompletedInterviews",
        f"{BASE}/api/users/user",
    ]
    for url in step3_urls:
        result = request_with_auth(url)
        if "license/counter" in url and result.get("success"):
            found = contains_keywords(result["body"], LICENSE_KEYWORDS)
            if found:
                LICENSE_HITS.append({"url": url, "keywords": found, "body": result["body"]})
        if url.endswith("/users/user") and result.get("success"):
            found = contains_keywords(result["body"], PERMISSION_KEYWORDS)
            if found:
                PERMISSION_HITS.append({"url": url, "keywords": found, "body": result["body"]})
        time.sleep(1)

    # STEP 4
    step("STEP 4 — Check licenses via API")
    step4_urls = [
        f"{BASE}/api/v1/licenses",
        f"{BASE}/api/v1/settings/licenses",
        f"{BASE}/api/v1/account/licenses",
        f"{BASE}/api/license/counter",
    ]
    for url in step4_urls:
        result = request_with_auth(url)
        if result.get("body") is not None:
            found = contains_keywords(result["body"], LICENSE_KEYWORDS)
            if found and result.get("success"):
                LICENSE_HITS.append({"url": url, "keywords": found, "body": result["body"]})
                print(f"\n*** LICENSE KEYWORDS FOUND: {found} ***")
        time.sleep(1)

    # STEP 5
    step("STEP 5 — Check user permissions via API")
    step5_urls = [
        f"{BASE}/api/v1/users/me",
        f"{BASE}/api/v1/users/current",
        f"{BASE}/api/v1/account/me",
        f"{BASE}/api/users/user",
    ]
    for url in step5_urls:
        result = request_with_auth(url)
        if result.get("success") and result.get("body") is not None:
            found = contains_keywords(result["body"], PERMISSION_KEYWORDS)
            if found:
                PERMISSION_HITS.append({"url": url, "keywords": found, "body": result["body"]})
                print(f"\n*** PERMISSION KEYWORDS FOUND: {found} ***")
        time.sleep(1)

    # STEP 6
    step("STEP 6 — Check the CATI server connection")
    step6_urls = [
        f"{CATI_BASE}/api/",
        f"{CATI_BASE}/Voxco.Web/api/",
        f"{CATI_BASE}/survey/api/",
        f"{CATI_BASE}/",
        f"{CATI_BASE}/Survey/Intweb.dll/vcc",
    ]
    for url in step6_urls:
        request_with_auth(url)
        time.sleep(1)

    # STEP 7
    step("STEP 7 — Look for context names")
    step7_urls = [
        f"{BASE}/api/v1/contexts",
        f"{BASE}/api/v1/portals",
        f"{BASE}/api/v1/organizations",
        f"{BASE}/api/v1/account",
        f"{BASE}/api/users/user/folders",
        f"{BASE}/A4S/MultiMode",
    ]
    for url in step7_urls:
        result = request_with_auth(url)
        if result.get("success") and result.get("body") is not None:
            CONTEXT_HITS.append({"url": url, "body": result["body"]})
        time.sleep(1)

    # SUMMARY
    step("SUMMARY")
    print(f"Working auth method: {WORKING_AUTH}")
    print(f"\nEndpoints that returned 200 ({len(SUCCESS_URLS)}):")
    for u in SUCCESS_URLS:
        print(f"  - {u}")

    print("\nCATI/Phone capability found:")
    print(f"  Phone/CATI surveys (by name/fields): {len(PHONE_SURVEYS)}")
    print(f"  License keyword hits: {len(LICENSE_HITS)}")
    print(f"  Permission keyword hits: {len(PERMISSION_HITS)}")
    for item in PHONE_SURVEYS[:30]:
        print("  SURVEY HIT:", pretty(item)[:500])
    for item in LICENSE_HITS:
        print("  LICENSE HIT keywords:", item["keywords"], "url:", item["url"])
    for item in PERMISSION_HITS:
        print("  PERMISSION HIT keywords:", item["keywords"], "url:", item["url"])

    print("\nContext / account discoveries:")
    if CONTEXT_HITS:
        for item in CONTEXT_HITS:
            print(f"  From {item['url']}:")
            print(pretty(item["body"])[:2000])
    else:
        print("  None via contexts/portals/organizations endpoints.")

    print("\nRecommended next steps:")
    print("  1. Open Interviewer Web: https://us1intweb.voxco.com/Survey/Intweb.dll/vcc")
    print("  2. Confirm TelephonySurveys license MaxValue > 0 via /api/license/counter")
    print("  3. Ask Voxco for Context name + Interviewer credentials if dialing seats are enabled")
    print("  4. Use Swagger at /api/V1.0/swagger/docs for Survey Platform API paths")

    out = {
        "working_auth": WORKING_AUTH,
        "success_urls": SUCCESS_URLS,
        "results": RESULTS,
        "phone_surveys": PHONE_SURVEYS,
        "license_hits": LICENSE_HITS,
        "permission_hits": PERMISSION_HITS,
        "context_hits": [
            {"url": c["url"], "body_preview": pretty(c["body"])[:2000]} for c in CONTEXT_HITS
        ],
    }
    with open("voxco_cati_discovery_results.json", "w", encoding="utf-8") as fh:
        json.dump(out, fh, indent=2, ensure_ascii=False)
    print("\nSaved: voxco_cati_discovery_results.json")
    return 0


if __name__ == "__main__":
    sys.exit(main())
