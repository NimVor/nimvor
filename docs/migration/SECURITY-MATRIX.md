# NimVor Security Controls — must be preserved and re-tested in Go

Each control is ported and gets negative tests (unauthorized / cross-tenant /
CSRF / invalid session / manipulated id) before its area is marked done.

| # | Control | PHP location | Go port | Negative tests |
|---|---|---|---|---|
| 1 | Password hashing (argon2id, bcrypt fallback) | app/Security/PasswordHasher, AuthService | internal/identity | wrong password; dummy-hash timing; PHP-hash cross-verify | ☐ |
| 2 | Login throttling / cPHulk brute-force | app/Security/* , brute_force_protection | internal/identity + middleware | lockout after N fails; per-IP | ☐ |
| 3 | Sessions (rotation, expiry, epoch invalidation) | app/Http/Session, 0042_session_epoch | internal/session | stale epoch rejected; expired cookie | ☐ |
| 4 | CSRF on state-changing browser requests | app/Security/Csrf | middleware/csrf | missing/invalid token → 419/403 | ☐ |
| 5 | Auth middleware (routes auth=true) | app/Http/Middleware/Authenticate | middleware/auth | no session → redirect/401 | ☐ |
| 6 | RBAC permission checks | app/Security/Authorizer, Permissions | internal/authorization | lacking permission → 403 | ☐ |
| 7 | Feature gating per plan | app/Services/FeatureService | internal/authorization | disabled feature → 403 + hidden | ☐ |
| 8 | Tenant isolation / ownership (`scope=own`) | SiteController::site(), assertOwned | services + authorization | cross-customer id → 404/403 | ☐ |
| 9 | Agent trust boundary (authz before privileged op) | app/Agent/AgentClient + controllers | internal/agent + services | panel cannot bypass agent allowlist | ☐ |
| 10 | Secrets at rest (libsodium) | app/Security/Crypto | internal/crypto (nacl) | decrypt with wrong key fails | ☐ |
| 11 | Audit logging of privileged/destructive actions | app/Services/AuditLogger | internal/audit | action recorded; secrets redacted | ☐ |
| 12 | Security headers + CSP (script-src 'self') | app/Http/Middleware/SecurityHeaders | middleware | header present; no inline script | ☐ |
| 13 | API token auth (separate from session) | app/Services/ApiTokenService, ApiAuthenticate | internal/identity | bad token → 401; feature gate applies | ☐ |
| 14 | Signed webhooks (HMAC, csrf=false) | DeployController::webhook | handlers | bad signature → 401 | ☐ |
| 15 | Path confinement (no traversal) — mostly agent-side | agent filesystem.Root | agent (unchanged) | traversal rejected | ☑ (agent) |

Rule carried over verbatim: **never trust a user-supplied tenant id, resource id
or `X-NimVor-Server` header**; derive access from the authenticated principal and
server-side state.
