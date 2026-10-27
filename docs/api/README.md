# API

## Agent API (available)

Local-only HTTP over `/run/whacp/agent.sock`, described in
[`agent/api/openapi.yaml`](../../agent/api/openapi.yaml).

Every response uses one envelope:

```json
{
  "success": true,
  "action": "discovery.php",
  "message": "OK",
  "data": { "versions": [ ... ], "pools": [ ... ] },
  "request_id": "5f0c1a9e2b7d4c11",
  "duration_ms": 3.2
}
```

Failures set `success: false` and an `error` with a stable `code`
(`unauthorized`, `unknown_action`, `write_disabled`, `invalid_params`,
`internal_error`, `timeout`). The `request_id` matches the agent log.

## Panel REST API

Versioned under `/api/v1` and described by OpenAPI at `/api/v1/openapi.yaml`
(source: `panel/resources/api/openapi.yaml`). Create a token under *Profile →
API tokens* (permission *Create personal API tokens*); it is shown once.

```bash
curl -s -H "Authorization: Bearer whacp_…" https://panel.example.com/api/v1/websites
```

- Tokens act with their owner's permissions and resource scope; read-only
  tokens are refused on modifying requests. They can expire and be revoked;
  every use updates *last used*, and changes are audited.
- The API is sessionless: no cookies, no CSRF token.
- Errors use `{"success": false, "error": {"code", "message", …}}` (with
  `reason`/`action` or `fields` where relevant);
  long-running operations return a `job_id` to follow with
  `GET /api/v1/jobs/{id}`.

```text
GET    /api/v1/me
GET    /api/v1/server
GET    /api/v1/websites                POST /api/v1/websites
GET    /api/v1/websites/{id}           DELETE /api/v1/websites/{id}
POST   /api/v1/websites/{id}/enable    POST /api/v1/websites/{id}/disable
POST   /api/v1/websites/{id}/ssl
GET    /api/v1/websites/{id}/backups   POST /api/v1/websites/{id}/backups
POST   /api/v1/websites/{id}/deploy
GET    /api/v1/databases               POST /api/v1/databases
GET    /api/v1/jobs/{id}
GET    /api/v1/notifications           POST /api/v1/notifications/read
```

The session-authenticated JSON endpoints used by the UI (for example
`/dashboard/live`) are internal and not part of the public API.
