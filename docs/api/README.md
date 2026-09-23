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

## Panel REST API (planned — Phase 22)

The public API will be versioned under `/api/v1`, authenticated with scoped,
revocable API tokens, and documented with OpenAPI:

```text
GET    /api/v1/websites
POST   /api/v1/websites
GET    /api/v1/websites/{id}
PUT    /api/v1/websites/{id}
DELETE /api/v1/websites/{id}
```

The session-authenticated JSON endpoint `/dashboard/live` used by the UI is
internal and not part of the public API.
