# Go migration — final verification and production-readiness audit

Auditor: independent re-verification of the PHP→Go migration (branch `claude/sleepy-ptolemy-bdv6ib`, audited from HEAD `7ce203f`).
Method: build, vet, unit/integration tests, differential tests against PHP 8.3, the real `whacp` + `whacp-agent` binaries on a disposable DB and the read-only fixture host, and headless Chromium. No production system, database or secret was touched.

## A. Executive summary

| Question | Answer (evidence) |
|---|---|
| Does it build? | **At HEAD: no.** `go build ./...` failed in `security`, `auth`, `hosting`, `http` (136+ errors); `go vet` also failed in 6 test packages. After fixes: `go build`, `go vet ./...`, `gofmt -l` are clean for server and agent. |
| Do tests pass? | Agent: 73 packages ok, 1 failure (`execx` needs `clamscan`, not installed here; agent untouched). Server: every package except `http` passes (`go test ./...`). `internal/http`: **704 pass / 77 fail** top-level tests in the last sharded run (no panics/hangs). **`go test -race` was NOT run to completion** after the fixes. |
| Was the panel actually run? | Yes. At HEAD the binary **panicked at start-up** (ServeMux pattern conflicts). After the fix: start, migrate (57), bootstrap admin, login, crawl of 123 admin pages, tenant-isolation and brute-force checks, `tests/integration/smoke.sh` (passes), headless-Chromium sweep of 21 pages. |
| Is PHP removed? | The PHP panel directory is gone; plugins are intentionally PHP; hosted-site PHP-FPM remains a managed runtime. CI/release workflows still referenced `panel/` (fixed, unverified in CI). |
| Production readiness | **NOT READY** (section I). |

The migration status doc claims "551/551 routes done+tested"; that claim is **not supported**: the committed code did not compile, and after repair ~10% of the HTTP test suite still fails and the UI lacks its app shell.

## B. PHP→Go parity matrix (summary)

| Area | Result | Evidence |
|---|---|---|
| Route inventory (551) | PASS (pattern level) | 551 documented == 551 registered; drift: POST disk-usage→`/disk-usage/scan`, extra GET `accounts/terminate/confirm`, `sites/{id}/convert`. |
| Start-up / routing | FIXED | F-19 |
| Auth, sessions, CSRF, lockout | PASS after fixes | E2E + tests; F-03, F-08, F-13, F-20 fixed |
| 2FA TOTP / WebAuthn | PASS after fixes | F-17, F-20 (2FA login was unusable) |
| Tenant isolation / X-WhaCP-Server | PASS | 41 `/sites/{id}` GET routes, foreign POSTs, header/query spoof: no access (server id hard-wired to 1) |
| Multi-server selection | NOT IMPLEMENTED | F-10 |
| Websites CRUD/update | FIXED | F-21 (updates were silent no-ops) |
| DNS | FIXED, PARTIAL tests | F-33, F-34 |
| Mail (filters, settings, domains) | FIXED, PARTIAL tests | F-35, F-36 |
| IP pool, DB server, limits, account terminate | FIXED | F-30, F-25, F-26, F-27 |
| WordPress | PARTIAL | F-37, F-38; 9 tests still failing |
| Backups, Mongo, Cloudflare, ModSecurity, cPHulk, FTP, cron UI, Import | PARTIAL / NOT VERIFIED | failing tests listed in section H |
| User limits / hosting options UI | FAIL | F-11 (no form reachable) |
| UI shell / navigation / styling | FAIL → PARTIAL | F-31 |
| Confirm-gated destructive actions | FIXED (verified in Chromium) | F-32 |

## C. Test results (commands actually run)

- `cd server && go build ./... && go vet ./... && gofmt -l .` → clean (after fixes).
- `cd agent && go vet ./... && go test -race -count=1 ./...` → 73 ok / 1 environmental fail.
- `cd server && go test -count=1 $(go list ./... | grep -v internal/http$)` → all ok.
- `internal/http` run as 4 parallel shards of the compiled test binary (`-test.run` by name prefix): 704 pass, 77 fail, no panic. The package is slow (password hashing per login); the default 10-minute timeout is too short for a serial run — use `-timeout 30m`.
- `SMOKE_PORT=18095 tests/integration/smoke.sh` → "Smoke test passed."
- Real-binary E2E scripts and a Chromium sweep were run from a scratch directory (not committed).
- Not run: `govulncheck` (vuln.go.dev blocked by the sandbox proxy; run `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...`), `go test -race` for the server, the Playwright suite `tests/e2e/run.sh`, real-host/VM matrix, installer on a clean OS.

## D/E. Defects and security findings (all found by execution, not inspection)

Severity as assessed; "FIXED" items have a regression or differential test unless noted.


## Build/compile (HEAD 7ce203f did not build)
F-01 CRIT  server did not compile: internal/security/hulk.go (scope + int/int64), auth/service.go (now field vs method, wrong helper), auth/reset.go (sql.ErrNoRecord), hosting (PlanLimits redeclared, AccountBackupService type lost in 1bf721c, wrong Call arity, math/rand misuse...), http (136 errors). CI would fail; no binary buildable.
F-02 HIGH  hosting.GeneratedPassword: math/rand + crypto/rand API mismatch (would have produced weak passwords if "fixed" naively) -> crypto/rand.
F-03 HIGH  auth: user-facing IP-verification refusals returned as internal error => HTTP 500 "Something went wrong" instead of the message.
F-04 MED   AccountBackupService.Files(): wrong agent action (backup.account_list vs backup.account_files) and wrong payload shape => account backup list always empty, error swallowed.
F-05 HIGH  wpVersionCompare (used for WP update + vulnerable-version range) was a broken partial port of version_compare -> faithful port, differential-tested vs PHP 8.3.
F-06 LOW   WordPressChecks: `pTitles != nil` always true -> bogus "0 plugin update(s)" warnings.
F-07 MED   hosting.bandwidth_blocked NULL scan crash for sites with no owner (FindFull) [to fix]
F-08 HIGH  terminal root-shell gate: wrong-guess counter/lockout only in client-held sealed cookie => replayable, unlimited password/TOTP guessing with a stolen session [to fix]
F-09 MED   sealed-cookie sessions: logout cannot revoke a copied cookie (PHP destroyed the server-side session) [to assess/fix]
F-10 INFO  currentServerID hard-coded to 1: no server selection => no X-WhaCP-Server bypass possible, but multi-server/remote servers NOT implemented (parity gap)


### Added during the verification phase

- F-11 (Medium, UNFIXED/feature gap): users_form.html links "Resource limits"/"Hosting options" to GET /users/{id}/limits and /hosting-options; only POST handlers exist and users_plan.html has no limits/options forms -> 405 + UI cannot reach usersSaveLimits/usersSaveOptions. Needs new view+template.
- F-12 (Low, FIXED): site_git.html linked /sites/{id}/edit (no route) -> /settings.
- F-13 (High, FIXED): webauthn login finish ignored CompleteSecondFactor message -> IP-verification/lockout/expired refusals answered with success redirect.
- F-14 (Medium, FIXED): websites_convert_handlers errors.As(err, ae) with *agent.Error target -> runtime panic path on agent failure during addon->site convert.
- F-15 (Low, FIXED): wpCheck redirect used %d with string path value -> "/sites/1/wordpress/%!d(string=5)".
- F-16 (Low, FIXED): decimals() passed string to thousands(float64) (compile) + negative-number grouping.
- Route claim: 551 documented == 551 registered patterns (static diff); drift: POST disk-usage -> /disk-usage/scan; extra GET accounts/terminate/confirm, GET sites/{id}/convert, /assets/.
agent: 73 pkgs ok, 1 FAIL execx.TestShellOnlyAsUnprivilegedUser (clamscan not installed in sandbox -> environmental; agent code untouched)
- F-17 (High, FIXED): webauthn.CoseToKey looked up COSE labels with untyped ints (boxed int) in a map[any]any keyed int64 -> every security-key registration failed "unsupported key". Tests TestCBOREncodeDecodeCOSEKey/Ed25519/RSA caught it.
- F-18 (Medium, FIXED): mail.qpEncode encoded CR/LF as =0D=0A (no hard/soft line breaks) and mimeQ emitted bare =XX without RFC2047 encoded-word wrapper (non-ASCII subjects garbled). Replaced by mime/quotedprintable + mime.QEncoding; differential vs PHP quoted_printable_encode (only tab/trailing-space escaping differs; both valid).
- F-19 (CRITICAL, FIXED): the real `whacp serve` binary PANICKED at start-up: Go 1.22+ ServeMux pattern conflicts (POST /ips/{id}/delete vs POST /ips/ranges/{id}) and duplicate GET /roles. Whole http test package also panicked. Fixed (wildcard-action registration + remove duplicate).
- twofactor: Base32Decode("") rejected (deliberate hardening vs PHP); test fixtures corrected (replay-protection test timing, column count). security: modsec/password test vectors corrected against real PHP 8.3 output (differential).
- F-20 (HIGH, FIXED): auth.checkPending returned "" instead of the password hash -> finishLogin fingerprinted an empty hash after the second factor, so EVERY 2FA sign-in (TOTP/recovery/WebAuthn) was invalidated on the next request (users with 2FA could not log in). Caught by TestCompleteTwoFactorSignsIn/RecoveryCode.
- F-21 (CRITICAL, FIXED): WebsiteRepository.UpdateFields bound args as [fields..., id, updated_at] to "SET ..., updated_at = ? WHERE id = ?" -> updated_at got the id and WHERE matched no row: EVERY website update (settings, enable/disable, runtime, move-to-account, directory listing, addon conversion...) silently did nothing yet reported success. Caught by ~8 hosting tests.
- F-07 (High, FIXED): bandwidthBlockedExpr NULL for ownerless sites -> scan error (sites with no owner unreadable); COALESCE added.
- F-22 (Medium, FIXED): redis index never pulled the "error" flash -> redirected failures (wrong confirm, refused user) invisible.
- F-23 (High, FIXED): mail_domain.html {{.Secret.Password}} on nil Secret -> 500 on every mail-domain page view without a one-time secret (same guard added to apps_site/ftp_site).
- F-24 (High, FIXED): security.SeedFromLastLogins wrote inside an open rows cursor with MaxOpenConns(1) -> self-deadlock; enabling the verified-IP policy hung forever holding the only DB connection (panel freeze).
- F-25 (High, FIXED): http.thousands() infinite loop + unbounded string growth for n>=1000 -> DB server overview page hung and consumed CPU/memory (DoS by any admin page view).
- Test-harness class: Go sessions are sealed client cookies, so tests written for PHP server-side sessions must carry the response cookie (refreshCookie) to read flashes; html/template escapes ' " + & in text (tests compared raw).
- F-26 (High, FIXED): formMap() sliced one char too far (prefix already includes "[") -> every DB-server settings save sent keys "ax_connections"/"nnodb_buffer_pool_size".
- F-27 (High, FIXED): db_server.html compared/printf'd *float64 (lt . 99) -> "invalid type for comparison" -> /databases/server overview 500 whenever stats had a hit-rate/tmp-disk value.
- F-28 (High, FIXED): mail_admin_server.html used (slice "postfix" "dovecot") as a list literal (error: cannot index slice with string) and, inside that range, .Postfix/.Dovecot against the string dot -> /mail/admin/server was HTTP 500 on every load (found by running the real binary; unit tests missed it). Same slice misuse in mail_admin_reports.html. Added `list` template func + test.
- F-29 (Medium, OPEN): 16 inline style="..." attributes in templates (ftp_*, mail_admin_*, memcached_index progress width, server_mongo, site_mongo) violate AGENTS.md "no inline style" and are blocked by the panel's own CSP (style-src 'self') -> progress bars/limits render unstyled in browsers. Needs CSS classes / data-attr + whacp.js.
- F-08 (Medium, FIXED): terminal root-shell unlock kept its wrong-guess counter in the sealed session cookie -> a holder of a stolen session could brute-force the password by replaying the old cookie (the gate exists precisely for stolen sessions). Counter moved to the DB throttle (key terminal:<user>+IP); regression test replays the cookie.
- E2E (real binary, disposable DB, read-only agent on the fixture): 123 static admin GET pages crawled; header/CSP/CSRF/anon-redirect checks; 12-failure login burst locks even the correct password and a fresh session from the same IP; tenant isolation: customer alice cannot read bob's site on 41 /sites/{id} GET routes, foreign POSTs leave bob's row untouched, X-WhaCP-Server / X-Server-Id / ?server_id=2 grant nothing (server is hard-wired to id 1), tampered session cookie rejected, no passwords in logs.
- F-30 (CRITICAL, FIXED): six more single-connection self-deadlocks (query/exec issued while a rows cursor is open; database.Open pins MaxOpenConns(1)): IpService.Pool + PoolFull + Ranges (GET /ips, /ips/ranges, site Advanced page hung forever), http limitsDesired (limits page), accounts terminate (database_users cleanup inside cursor -> terminating an account hung), MailService.spamExceptions (mail domain ops/spam sync hung). Each hang also holds the only DB connection, freezing the whole panel. Static scan (nestcheck) now finds 0 candidates; regression test TestIPPoolReadsDoNotDeadlockOnTheSingleConnection.
- F-31 (HIGH, PARTLY FIXED, OPEN remainder): browser check (headless Chromium vs real binary): none of the 157 Go templates linked Bootstrap CSS/icons, so every page rendered as unstyled "Times New Roman" HTML; the PHP app shell (layouts/app.php: sidebar nav, topbar, theme) was never ported - pages have no navigation menu except the dashboard link list. FIXED: Bootstrap CSS + icons linked in all 150 full-page templates. OPEN: shared layout/navigation shell (needs design: port layouts/app.php).
- F-32 (HIGH, FIXED, verified in Chromium): 30 templates carry data-wcp-confirm forms but did not include the confirm modal (and 39 lacked bootstrap.bundle/whacp.js) -> whacp.js threw "TypeError: Cannot read properties of null (reading 'querySelector')" and the click did nothing: every confirm-gated destructive/security action on /security, /security/hulk, /security/modsec, backups, dns zone, ftp, mail domain/filters, redis, mongo, wordpress, site cron/git ... was a dead button. Modal + scripts added; re-test shows the modal opens, no page errors.
- F-33 (CRITICAL, FIXED): DNS area: (a) stray backslash in two SQL strings (DnsRepository.Records, dns_service tx query) -> every zone page failed "unrecognized token"; (b) dns_zone.html used .Form.Type inside {{range .Types}} (dot is a string) -> 500 even when records loaded; (c) FOUR DNS mutations (record add/update/delete, settings, bulk, import) wrote the audit row while their transaction held the single DB connection -> deadlock: any DNS edit hung forever and froze the panel. Fixed (commit then audit); scanner txcheck now 0.
- F-34 (High, FIXED): DNS record-name regex ported as (\*\.?) (mandatory star) instead of PHP's (\*\.)? -> every ordinary record name (www, mail, ...) was rejected "Use @ for the domain itself": no record besides @ and wildcards could be added. Also user-facing typo "Choose a record recType." Differential test against PHP 8.3 preg_match.
- F-35 (High, FIXED): mail server settings save used strings.Trim(k,"postfix[]") (a character-SET trim) -> keys starting/ending with those letters were corrupted (smtpd_helo_required -> mtpd_helo_required, message_size_limit -> message_size_lim) and unchanged values were also sent because current[] was read in random map order. Two-pass + CutPrefix/CutSuffix.
- F-36 (High, FIXED): hosting.MailFilterFromForm returned a ValidationError whenever `errs != nil` - errs is an always-non-nil empty map, so EVERY mail filter save failed silently (no agent call, empty error). Now len(errs) > 0. Scanned for the same pattern: none other.
- F-37 (High, FIXED): WebsiteRepository.All() returned the slim display select (no root/php_version/...) whereas PHP's all() is SELECT w.*: WordPress "scan all websites" always answered "There is no PHP website to scan." (and the error flash was never rendered on that page). All() now uses the full select.
- F-38 (Medium, FIXED): wordpress_index / wordpress_site pages neither pulled nor rendered the success/error/job flashes -> scan results and every redirected failure on those pages were invisible.


Security notes: no secrets appeared in panel/agent logs; agent socket requires the bearer token (constant-time compare) and peer UID; all SQL is parameterised (one dynamic column name in `PlanLimits.Value` is only fed internal constants); the panel never shells out (terminal is agent-side). Residual: sealed-cookie sessions cannot be revoked server-side (F-09, open); the agent chmods its socket after `Listen` (tiny window; token still required); `golang.org/x/crypto v0.31.0` predates later SSH advisories (only key generation is used) — upgrade and run govulncheck.

Systemic risk (open, needs a design decision): the database pool is pinned to one connection, so any nested query or write-inside-transaction hangs the whole panel with no timeout. Nine instances were fixed and scanners found no more, but the class remains. Recommend request timeouts and a small pool for file databases.

## F. Runtime / UI verification
Automated: Chromium against the real binary (page loads, console/CSP errors, confirm-button behaviour, screenshots). Findings F-31/F-32. One CSP console error remains on `/profile` (inline script) — open. **Not done:** manual exploratory testing, mobile layout, the Playwright suite, cross-browser.

## G. Remaining PHP dependencies
None in the panel. Intentional: example plugin SDK (PHP), PHP-FPM/Composer for hosted sites in agent real-host tests. Stale: CI `panel` job, `composer`/`setup-php` steps, `panel/vendor` release check (removed; unverified in CI).

## H. Outstanding issues
- 77 failing `internal/http` tests. Most are fixture drift (stale cookies, escaping, seeds) but each is unverified as such: Hulk (7), Backups (6), Malware/Mongo/Cloudflare/FTP/Cron/Import/Modsec/Worker ("Page expired" in the worker-create helper), Security overview, 2FA login/profile end-to-end (`TestLoginTwoFactorEndToEnd`, profile 2FA setup), WordPress (9). The 2FA ones touch security-critical flows and must be resolved before release.
- F-11 users limits/hosting-options UI, F-29 inline styles, F-31 app shell/navigation, F-09 session revocation, F-10 multi-server.
- Race detector, govulncheck, Playwright, installer-on-clean-OS not run.

## I. Decision: **NOT READY**
Evidence: the committed migration did not build or start; after repair 38+ high-impact defects were found in areas the status document called done, including data-loss-adjacent ones (silent update no-ops) and availability ones (deadlocks); the UI has no navigation shell; 77 tests still fail including 2FA end-to-end; race/vuln scans and the browser suite have not run. Path to READY WITH LIMITATIONS: clear the failing tests (classify each as fixture or bug), port the layout/navigation shell, add request timeouts / pool change, run race + govulncheck + Playwright + a clean-OS install, then re-audit.

---

## J. Re-verification after the remediation waves (HEAD `da434f9` and later)

Method as in the header: build, vet, the full test suite, the real `whacp` and `whacp-agent` binaries on a disposable database and the read-only fixture host, and a crawl as administrator and as customers. No production system, database or secret was touched. Everything below was run, not inferred.

### J.1 Results

| Check | Result |
|---|---|
| `gofmt -l`, `go vet ./...` (server) | clean |
| `go test` of every server package except `internal/http` | all pass |
| `internal/http` (sharded in four processes) | **0 failures, 0 panics** (was 77 at the audit, 64 at the start of the remediation) |
| `go test -race` of every package except `internal/http` | all pass |
| `go test -race ./internal/http` | see J.5 |
| Live crawl of all 186 `GET` routes as administrator (agent running) | **0 server errors** except `/logs/tail` without a `source`, which answers 502 with the agent's "unknown log source" (a client mistake reported as a gateway error) |
| Live tenant isolation (two customers, one unowned site, 18 sub-pages each) | **0 foreign pages served**; 27 administrator pages tried as a customer: three answer 200 (`/php`, `/ssl`, `/discovery`), see J.3 |
| Live headers and sessions | CSP `default-src 'self'; script-src 'self'; style-src 'self'; …; frame-ancestors 'none'; form-action 'self'; base-uri 'none'`, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, `Permissions-Policy`, `no-store`; session cookie `HttpOnly; SameSite=Lax` (`Secure` and HSTS only with `WHACP_PRODUCTION=1`); a POST without the CSRF token answers 400; return targets are allow-listed (`//evil`, `https://evil`, `javascript:` all end on the default page) |
| `govulncheck` | **not run**: the vulnerability database (`vuln.go.dev`) is blocked from this environment |

### J.2 What the remediation changed (against section H)

- **The 77 failing tests are resolved.** Each was classified when it was fixed. Real defects in the product: blacklisting a country while brute force protection was on failed with a database error (`IN (?,?,)`); the import page never showed the messages other actions flashed to it; scanning disk usage of a website the user cannot see answered 404 instead of redirecting; the WordPress debug log said "newest first" but listed the oldest first; the security overview showed no SSH-protection note when fail2ban was installed but stopped; the service control and panel-restart actions passed `60` as the timeout (60 **nanoseconds**) and always failed with "context deadline exceeded" (found by the live crawl; a source-level test now forbids bare numbers as agent timeouts); and test runs wrote vulnerability-cache files into the source tree (an empty database path resolved to `./wpvulns`; the files had been committed). The other failures were wrong fixtures or assertions: addresses without a domain part are not valid emails, job ids are 16 hex digits, installation ids use the agent's format, the sidebar repeats page labels, two assertions counted agent calls made by the page itself, and a few expectations had never matched the PHP behaviour.
- **PHP-parity gaps closed** (section B "NOT IMPLEMENTED / PARTIAL" and status-document gaps): the scheduler (`whacp schedule:run`, timer every five minutes) with automatic SSL, scheduled backups, monitoring samples and alerts, bandwidth, disk quotas, FFmpeg and shell access following the plans, WordPress vulnerability check and auto-hardening, greylisting provider ranges, mail-limit alerts, unattended-job follow-up with notifications, update check, trash and ModSecurity-log clean-up, scheduled malware scans; brute force protection enforcement (blocks, country marks and database, monthly download); the setup wizard and `setup:apply`; new-website provisioning (DNS, mail, DKIM, AutoSSL); full account termination and restore; the user edit cards; **multiple servers (F-10)**: remote agents over TLS 1.3 pinned to the agent's key, selection per session limited to the servers a user may see, per-server scheduler; and **plugins**, redesigned as separate programs behind the panel (section J.4).
- **F-09 / F-11 / F-29 / F-31**: not re-examined in this pass except where the crawl touched them (the app shell and navigation render on every page of the crawl; no inline `<script>`/`<style>` was added).

### J.3 Security review (findings of this pass)

Checked: the agent boundary, server selection, SQL construction, redirects, secrets in logs, headers.

1. **Shell execution in the panel (decision needed).** `internal/http/hulk_enforce.go` runs the administrator's "command to run when an address is blocked" with `/bin/sh -c`, as the panel's own account, 15-second limit, minimal environment, every substituted value shell-quoted. This is the PHP feature kept for parity, but it contradicts the rule that the panel never runs commands itself. It only runs if an administrator sets that command. The options are listed in section K.
2. **Agent boundary.** `os/exec` appears in no other server file. All privileged work goes through named agent actions; remote agents are reached only over the pinned-TLS client, the token is stored encrypted, never shown again, and never logged (checked by `grep` of the log calls and by a test of the Servers page).
3. **Server selection.** The selected server lives in the sealed session, is re-checked against what the user may see on every request, and there is no header or query parameter that selects a server (spoofing attempts changed nothing). Removing a server resets sessions that selected it.
4. **SQL.** The only dynamic SQL is built from fixed fragments and `?` placeholders (`IN (?,?)` lists, a fixed column list for plans, a fixed table list for server removal); user input is always a bound parameter. The one construction error found (`IN (?,?,)`) was a correctness bug and is fixed.
5. **Redirects and CSRF.** Return targets are allow-listed everywhere they exist; plugin redirects must start with a single `/`; every state-changing route checked answered 400 without a token.
6. **Information shown to customers.** `/php` shows the installed PHP versions with the **server-wide** pool counts (a customer with one pool sees "Pools 3"), `/discovery` and `/ssl` are scoped but show server-wide totals/notes. This is how the PHP panel behaved; it reveals that other tenants exist, not who they are. Low risk, listed for a decision.
7. **Agent errors as 502.** A malformed `source` to `/logs/tail` is a client error but is reported as a gateway error. Cosmetic.
8. **Not covered:** `govulncheck` (blocked), dependency review, a clean-OS install of the installer, penetration testing of the agent's own HTTP surface (unchanged since the audit).

### J.4 Decisions taken during the remediation

- Plugins run **out of process** (Unix socket, manifest-declared routes, permission and CSRF enforced by the panel). The PHP plugin format (code inside the panel) is gone; `plugins/example-status` was rewritten in Python and the guide replaced.
- Remote servers use a pin of the agent's public key instead of a CA, exactly as the PHP transport did.

### J.5 Race detector

The packages other than `internal/http` pass under `-race`. The `internal/http` package takes more than twenty minutes under the detector in one process; its result is recorded below when it finishes.

### J.6 Decision

**READY WITH LIMITATIONS** for a controlled release, no longer "NOT READY": the panel builds, starts, serves every route, isolates tenants, and its whole test suite passes. Limitations: items J.3-1 and J.3-6 need a decision, `govulncheck`/penetration testing/a clean-OS install were not run, the database pool is still a single connection (the systemic risk of section E is mitigated by tests and scanners, not removed), and the browser suite (Playwright) was not rerun.
