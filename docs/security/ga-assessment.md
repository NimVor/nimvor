# NimVor 1.0 — Security GA Assessment

- **Version:** 1.0.0-rc1
- **Branch:** `claude/sleepy-ptolemy-bdv6ib`
- **Date:** 2026-10-12

## GA decision

**NOT YET APPROVED — blocked on the real-host matrix.**

Every Phase 1 critical control and every Phase 2 high-priority control is
implemented and passing its tests locally (unit tests, and the real-host
tests that run without KVM were run as root on an Ubuntu host). GA is held
only because the cross-OS real-host matrix (Ubuntu 22.04/24.04, AlmaLinux 9,
Rocky 9) has not run: GitHub Actions is refusing to start jobs for a billing
reason. GA must not be declared on unit tests alone, so sign-off waits for
that matrix to pass once billing is restored.

No security control was disabled or weakened, and no test was skipped, to
reach this state.

## Phase 1 — critical blockers (if any fails, GA is blocked)

| Item | Control | Status | Evidence |
|---|---|---|---|
| 50 | Cross-site PHP isolation | Implemented · tested | `agent/internal/sites/realhost_crosssite_test.go` |
| 181 | Filesystem TOCTOU / race | Implemented · tested | `agent/internal/files/race_test.go` (+ `files.go` nil-deref fix) |
| 90–91 | Sandbox escape (DAC layer) | Implemented · tested | `agent/internal/security/realhost_sandbox_test.go` |
| — | Agent RPC adversarial audit | Implemented · tested | `agent/internal/validate/validate_test.go` |

## Phase 2 — high-priority hardening

| Item | Control | Status | Evidence |
|---|---|---|---|
| 152 | Automatic restore rollback | Implemented · tested | `agent/internal/backup/realhost_rollback_test.go` |
| 104–106 | File integrity monitoring | Implemented · tested | `agent/internal/security/integrity.go` (+ real-host test) |
| 70–71 | TLS configuration audit | Implemented · tested | `agent/internal/tlsaudit/` |
| 80 | Loopback / shared-cache control | Implemented · tested | `agent/internal/egress/` (+ real-host nft test) |
| 33–35 | Secure PHP profiles | Implemented · tested | `agent/internal/sites/phpprofile.go`, panel `SitePhpProfileTest` |
| 164 | OS vulnerability advisories | Implemented · tested | `agent/internal/pkgs/advisories.go` |

## Phase 4 — negative tests

`panel/tests/Security/NegativeInputTest.php` sweeps every parameterised route
with bad/malformed/traversal ids, destructive POSTs on missing objects,
unauthenticated requests and invalid API tokens, asserting a clean deny and
never a 500. Complements `RbacMatrixTest`, `TenantIsolationTest` and
`ApiIsolationTest`.

## Cross-platform real-host matrix (remaining gate)

The `realhost-vm` CI job boots each OS under KVM and runs the full real-host
security suite. It has not run — blocked only by GitHub Actions billing, not
by any code change. All the tests above are written to run in it.

| OS | Status |
|---|---|
| Ubuntu 22.04 | pending CI |
| Ubuntu 24.04 | pending CI |
| AlmaLinux 9 | pending CI |
| Rocky 9 | pending CI |

## Remaining before sign-off

1. **Restore GitHub Actions billing** (external blocker) so the matrix and the
   regression gate can run.
2. **Panel Security-Audit cards** for the file-integrity, TLS-audit and
   advisory agent actions — a UI follow-up; the capabilities and their tests
   are done and do not gate the security sign-off.
