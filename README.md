# Voxco CATI API Discovery

Scripts and findings from probing the Voxco US1 tenant for CATI / phone dialing access via API.

## Quick start

```bash
export VOXCO_API_KEY='YOUR_API_ACCESS_KEY'
python3 voxco_cati_discovery.py
```

Optional:

```bash
export VOXCO_BASE_URL='https://us1.voxco.com'
export VOXCO_CATI_BASE_URL='https://us1intweb.voxco.com'
```

## Auth

Use:

```http
Authorization: Client <API_ACCESS_KEY>
Accept: application/json
Accept-Version: 1.0
```

## Key finding

See [VOXCO_CATI_DISCOVERY_REPORT.md](./VOXCO_CATI_DISCOVERY_REPORT.md).

- Survey WebAPI lives at `https://us1.voxco.com/api/` (Swagger: `/api/V1.0/swagger/docs`).
- Interviewer / CATI UI lives at `https://us1intweb.voxco.com/Survey/Intweb.dll/vcc`.
- Telephony license counters exist but were **MaxValue = 0** for this account at discovery time.
