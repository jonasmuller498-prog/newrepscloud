# Voxco CATI / Phone Dialing — API Discovery Report

Base URL explored: `https://us1.voxco.com`  
CATI / Interviewer host: `https://us1intweb.voxco.com`  
Auth that works: `Authorization: Client <API_KEY>` (+ `Accept-Version: 1.0`, `Accept: application/json`)

## Executive verdict

**CATI/Interviewer Web exists on this tenant, but telephony dialing capacity is not licensed (quota = 0).**  
The REST API on `us1.voxco.com` is the **Acuity Survey WebAPI** (online/multimode survey platform). It exposes phone-related *data* helpers and telephony license counters, but **no dialer/call-center control endpoints**.

---

## STEP 1 — Endpoint discovery

| URL | Status | Notes |
|-----|--------|-------|
| `https://us1.voxco.com/api/` | **200 SUCCESS** | Swagger UI for **Acuity4 WebAPI**; discovery path `V1.0/swagger/docs` |
| `https://us1.voxco.com/api/V1.0/swagger/docs` | **200 SUCCESS** | Full OpenAPI: 121 paths, title `Acuity.WebAPI 1.0` |
| `https://us1.voxco.com/api/v1/` | 404 | Wrong version style (`v1` ≠ `V1.0` / unversioned resource paths) |
| `https://us1.voxco.com/api/v2/` | 404 | Same |
| `https://us1.voxco.com/A4S/api/v1/` | 404 | A4S is the Survey Platform UI, not this API root |
| `https://us1.voxco.com/A4S/` | 200 (HTML) | Login — Voxco Survey Platform **7.4.26106.2** |
| `https://us1.voxco.com/A4S/MultiMode` | 200 (HTML) | MultiMode login; asks for **Context** first |

**Correct API shape:**

```http
GET https://us1.voxco.com/api/users/user
Authorization: Client <API_KEY>
Accept: application/json
Accept-Version: 1.0
```

Not `/api/v1/surveys` — use `/api/users/user/surveys`, `/api/survey/{id}`, etc.

---

## STEP 2 — Surveys / phone fields

| URL | Status |
|-----|--------|
| `/api/v1/surveys?mode=phone` | 404 |
| `/api/v1/surveys?type=cati` | 404 |
| `/api/v1/surveys` | 404 |
| `/api/v2/surveys` | 404 |
| **`/api/users/user/surveys`** | **200 SUCCESS** — **998 surveys** |
| `/api/survey/{id}` | 200 for existing IDs |

Survey objects expose: `Id`, `Name`, `FolderId`, `Status`, `Token`, `Link`, `UseS2`, `CustomProperties`, …  
**No `mode` / `cati` / `dialer` / `telephony` fields** on the survey list payload.

Links point at the online survey engine (`us1se.voxco.com/S2` or `/SE`), not the dialer.

Surveys whose **names** mention CATI/phone (content/demos, not proof of dialer enablement):

- `Personalized Quote Voxco CATI` (Id 1736)
- `SDR CATI IVR AND DIALER QUIZ` (Id 1681)
- `Testing phone numbers` (Id 2425)
- `Mobile or cell phone survey template` (Id 1947)

`GET /api/survey/metadata?surveyToken=...` returned `ClientId: 1004` but **no Mode field** in the live JSON (docs mention Mode can be Web/CATI).

---

## STEP 3 — CATI-specific REST paths

All of these under `https://us1.voxco.com/api/v1/...` and `/api/v2/...` returned **404**:

`cati/`, `dialer/`, `interviewer/`, `callcenter/`, `phone/`, `projects/`, `cases/`

Swagger tags present: Analyze, Authentication, Distribution, Email, Library, **License**, Panel, Quotas, Respondent(s), Results, Salesforce, Sample, Survey, Users.  
**No Dialer / CATI / CallCenter tag.**

Phone-adjacent API capabilities that *do* exist:

- Sample import formats: `CommandCenter`, `InterviewerSQL`
- `ValidateWithDNC`, `DuplicatePhoneAction`, `LinkByPhone`
- Panelist / unsubscribe `Phone` fields
- License types: `ConcurrentAgents`, `PhoneCompletedInterviews` (category `TelephonySurveys`)

---

## STEP 4 — Licenses (critical)

`GET /api/license/counter` → **200 SUCCESS**

Telephony-related counters on this account:

| CounterType | Category | MaxValue | Used | Remaining |
|-------------|----------|----------|------|-----------|
| **ConcurrentAgents** | TelephonySurveys | **0** | 0 | 0 |
| **PhoneCompletedInterviews** | TelephonySurveys | **0** | 0 | 0 |

Compare with online:

| CounterType | Category | MaxValue | Used |
|-------------|----------|----------|------|
| ConcurrentRespondents | OnlineSurveys | 500 | 0 |
| Responses | OnlineSurveys | 500000 | 137 |
| MobileOfflineResponses | OfflineSurveys | 500000 | 17 |

**Interpretation:** the platform knows about telephony licensing, but this tenant currently has **no CATI agent / phone-interview capacity** (`MaxValue = 0`).

---

## STEP 5 — User / permissions

| URL | Status |
|-----|--------|
| `/api/v1/users/me` | 404 |
| `/api/v1/users/current` | 404 |
| `/api/v1/account/me` | 404 |
| **`/api/users/user`** | **200 SUCCESS** |

Authenticated user:

```json
{
  "Id": 7,
  "UserName": "voxcodemo@voxco.com",
  "Email": "voxcodemo@voxco.com",
  "DisplayName": "Administrator VoxcoServices",
  "Title": "Administrator",
  "Active": true,
  "IsSystemUser": true,
  "RemoteAgentPhoneNumber": null,
  "DefaultAudioMonitoringProject": null
}
```

Notes:

- Key maps to **`voxcodemo@voxco.com`**, not `dhanashree.badhe@voxco.com`.
- `RemoteAgentPhoneNumber` / `DefaultAudioMonitoringProject` are CATI-oriented user fields (both null).
- No permission array listing `cati` / `dialer` / `callcenter` in this payload.
- Client id from survey metadata: **1004**.

---

## STEP 6 — CATI server (`us1intweb.voxco.com`)

| URL | Status | Notes |
|-----|--------|-------|
| `https://us1intweb.voxco.com/` | **200** | Redirects to Interviewer Web logon |
| `https://us1intweb.voxco.com/Survey/Intweb.dll/vcc` | **200 SUCCESS** | **Interviewer Web Logon** + CATI logo |
| `.../api/`, `.../Voxco.Web/api/`, `.../survey/api/` | 404 | No Survey WebAPI mirror here |
| `.../survey/` | 403 | Directory listing forbidden |

Interviewer login form fields:

- `intid` — Username  
- `passwd` — Password  
- `context` — **Context**  
- Hidden: `IMODE=3`, `iaction=17`  
- Branding: `Images/Logos/cati_logo_green.png`  
- Version: **Interviewer Web 7.4.26106.2**

The Survey Platform API key does **not** unlock a REST dialer API on this host; access is the Interviewer Web UI (username / password / context).

---

## STEP 7 — Contexts / portals

| URL | Status |
|-----|--------|
| `/api/v1/contexts` | 404 |
| `/api/v1/portals` | 404 |
| `/api/v1/organizations` | 404 |
| `/api/v1/account` | 404 |

Discovered outside those paths:

- Survey Platform MultiMode login asks for **Context** (`/A4S/MultiMode`).
- Interviewer Web login asks for **Context**.
- Multi-context selector is present but disabled in HTML (`IsDisplayLoginMultiContextSelector=False`).
- No context name list is exposed via this API key.
- Related hosts: `us1.voxco.com` (platform/API), `us1intweb.voxco.com` (Interviewer/CATI UI), `us1se.voxco.com` (survey engine).

---

## What the API key can access

Working auth: **`Authorization: Client ...`**

Successful JSON/API access includes:

- OpenAPI docs: `/api/V1.0/swagger/docs`
- Current user: `/api/users/user`
- Folders: `/api/users/user/folders`
- Surveys list/detail: `/api/users/user/surveys`, `/api/survey/{id}`
- Licenses: `/api/license/counter`, `/api/license/counter/{type}`
- Survey token metadata: `/api/survey/metadata?surveyToken=...`

Product surface: **Voxco Survey Platform / Acuity WebAPI** for client **1004**, admin system user `voxcodemo@voxco.com`.

---

## Is CATI/Phone dialing available?

| Signal | Finding |
|--------|---------|
| Interviewer / CATI UI | **Yes** — live at `us1intweb.voxco.com` |
| Telephony license capacity | **No** — `ConcurrentAgents` & `PhoneCompletedInterviews` max = **0** |
| Dialer REST API on Survey WebAPI | **No** — not in swagger |
| Online / offline survey API | **Yes** — fully usable |

**Bottom line:** CATI software (Interviewer Web) is deployed, but this account does not currently have telephony dialing seats/interview quota. Dialing is not controllable through the Survey WebAPI key you provided.

---

## Recommended next steps

1. **Ask Voxco to enable Telephony licenses** for client `1004`: `ConcurrentAgents` and/or `PhoneCompletedInterviews` under `TelephonySurveys`.
2. **Obtain Interviewer credentials + Context name** (UI login at `https://us1intweb.voxco.com/Survey/Intweb.dll/vcc`). The API does not list contexts.
3. Confirm whether dialing is managed only in **Interviewer / Command Center UI**, or if a separate CATI/integration API exists for your contract (not present in Acuity WebAPI 1.0 swagger).
4. If you need API-driven sample/case loading for phone work, use existing Survey API sample import (`CommandCenter` / `InterviewerSQL`, DNC/phone options) **after** telephony is licensed.
5. If the intended user is `dhanashree.badhe@voxco.com`, generate/use that user’s API Access Key (Setup → Users & Permissions → Security); the key tested here is `voxcodemo@voxco.com`.

---

## How to reproduce

```bash
export VOXCO_API_KEY='YOUR_KEY'
python3 voxco_cati_discovery.py
```

Follow-up probes (real paths) are documented in this report; Swagger remains the source of truth:

`https://us1.voxco.com/api/V1.0/swagger/docs`
