# API

## Agent API (available)

Local-only HTTP over `/run/nimvor/agent.sock`, described in
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

The full reference, with every endpoint's parameters, request and response examples and errors, is
on the website: **<https://nimvor.com/api-reference>**. It is generated from the API's OpenAPI 3.1
description, which every panel also serves at `/api/v1/openapi.json` (the old `/api/v1/openapi.yaml`
address returns the same document; JSON is valid YAML).

Versioned under `/api/v1`. Create a token under *Profile → API tokens* (permission *Create personal
API tokens*); it is shown once.

```bash
curl -s -H "Authorization: Bearer nimvor_…" https://panel.example.com/api/v1/websites
```

- Tokens act with their owner's permissions and resource scope; read-only tokens are refused on
  modifying requests. They can expire and be revoked; every use updates *last used*, and changes are
  audited.
- The API is sessionless: no cookies, no CSRF token. 120 requests per minute per token.
- Errors use `{"success": false, "error": {"code", "message", …}}` (with `reason`/`action` or
  `fields` where relevant); long-running operations return a `job_id` to follow with
  `GET /api/v1/jobs/{id}`.

The session-authenticated JSON endpoints used by the UI (for example `/dashboard/live`) are internal
and not part of the public API.

### Changing the API

The description is written in PHP under `panel/resources/api/spec/` (one file per area) and built into
the committed `panel/resources/api/openapi.json`:

```bash
cd panel && bin/build-openapi      # after changing resources/api/spec/*.php
```

`tests/Http/OpenApiTest.php` fails when the committed file is stale, when a route under `/api/v1` has no
operation (or an operation has no route), when an operation's `x-permission` differs from the router's,
when an operation lacks an id, summary, tag or a success example, or when a `$ref` does not resolve. The
website reads the file from the repository (refreshed every six hours), so a merged API change appears
in the reference without deploying the website.
