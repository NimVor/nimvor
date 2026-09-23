# Contributing to WhaCP

Thank you for helping. WhaCP runs with root-level consequences on real
servers, so contributions are held to a high safety bar. Please read this
whole page before opening a pull request.

## Getting started

```bash
scripts/dev.sh        # agent (on the fixture server) + panel on http://127.0.0.1:8090
make lint test smoke  # everything CI runs
```

See [docs/development](docs/development/README.md) for the code layout and
conventions.

## Engineering rules (mandatory)

1. **Never break existing websites.** Every change to the host follows
   *Detect → Backup → Validate → Apply → Verify*, with rollback on failure.
2. **No arbitrary command execution.** The panel never shells out. The agent
   runs only binaries in the `execx` allowlist, with fixed argument shapes and
   no shell. New capabilities are new, named agent actions.
3. **Never trust paths.** Use `filesystem.Root` in the agent; validate and
   confine every user-supplied path.
4. **Never reload an invalid configuration.** Generate → validate
   (e.g. `nginx -t`) → reload, keeping the previous working file.
5. **Idempotency.** Running an action twice must not corrupt anything.
6. **Never expose secrets** in responses, logs or audit records. Use the
   redaction helpers (`logging.Redact`, `Redactor`).
7. **Audit** every privileged or destructive action.
8. **Mark modifying agent actions `Mutating: true`** so they stay behind the
   `allow_write_actions` gate.
9. **Users see friendly errors** (what failed, why, what to do); technical
   details go to the logs.
10. **Original work only.** Do not copy code, UI, assets or wording from other
    control panels.

## Pull requests

- One logical change per PR, with tests. Parsers need fixture-based tests;
  panel features need unit/integration tests and, for pages, an HTTP test.
- Document new features in `docs/` and add a line to `CHANGELOG.md`.
- Describe any operation that touches the host in the PR, including how it
  is validated and how it rolls back.
- `make lint test smoke` must pass.

## Style

- **Go:** `gofmt`, small packages under `internal/<domain>`, `context.Context`
  on anything that can block, `log/slog` structured logging, stdlib first.
- **PHP:** PSR-12, `declare(strict_types=1)`, constructor injection, thin
  controllers, repositories for SQL, services for behaviour. Escape all output
  with `e()`. No inline `<script>`/`style` (the CSP forbids them).
- **Commits:** imperative subject line (≤ 72 chars), body explaining *why*.
