# NimVor telemetry

Telemetry is **off by default and always optional**. An installation reports only after its administrator turns
it on (Settings → Telemetry in the panel, or the question the installer asks), and can turn it off, unregister
or ask for everything to be deleted at any time. It is never used for licensing, never required for any feature,
and never sent by a build that is not asked to.

This page is the contract between the three parts:

| Part | Where | Does |
|---|---|---|
| Central interface and developer dashboard | `website/` (`/api/v1/telemetry/*`, `/admin/telemetry`) | Validates, stores, aggregates |
| Collector | `agent/` (read-only action `telemetry.collect`) | Reads the OS, hardware and software facts on the server |
| Consent, settings and sender | `panel/` (Settings → Telemetry) | Asks, stores the choice, sends reports, queues retries |

## What is sent (and what never is)

| Report | Content | Rounded / limited by |
|---|---|---|
| Registration | random installation id, NimVor and agent version | |
| Heartbeat (every 24 h) | versions, days since the server booted, install / upgrade / rollback events (kind, from, to, success) | |
| Inventory (every 7 days) | OS name and major version, kernel `major.minor`, architecture, virtualisation type, init system, CPU core count, memory and disk size, and the version and running state of a fixed list of components (nginx, php, mariadb, …) | memory and disk are rounded **up** to a bucket |
| Features (every 7 days) | **how many** of each thing exists (websites, mailboxes, databases, certificates, …), from a fixed list | counts only, never names |
| Health (every 24 h) | number of services and failed services, disk and memory use, updates pending, last backup ok, certificate expiring, failed jobs | percentages rounded down to tens |

**Never sent:** IP address, host name, domain names, website names, user names, e-mail addresses, file paths,
file contents, logs, passwords, keys, tokens, the machine id or MAC address, anything a customer typed. The
installation id is random (made on the server, not derived from anything on it) and stays the same across
upgrades. The central interface does not store the sender's address; it only counts requests per address
for an hour in a cache to limit abuse.

Every field has a fixed type and range on the receiving side; text that is not on an allow-list has nowhere to go.

## Choices an administrator has

- **Off** (default): nothing is sent, nothing is queued, no id is made.
- **On**: registers, then reports on the schedule above. Settings → Telemetry shows the id, the time of the last report and exactly what the next one contains.
- **Turn off / unregister**: stops the reports; data already sent stays in aggregate counts.
- **Delete my data**: erases everything stored for the installation; only a hash of the id is kept so a late retry cannot register it again.

## Interface (schema version 1)

`POST https://nimvor.com/api/v1/telemetry/{register|heartbeat|inventory|features|health|unregister}`, JSON body of at
most 16 KB. Every body carries `schema` (1), `installation_uuid` (UUID v4) and `event_id` (a fresh UUID per report: a
retry with the same `event_id` is recognised and counted once). All but `register` need
`Authorization: Bearer <token>`; the token is returned once by `register` and only its hash is stored.
`register` can be repeated until the token has been used once (the answer may have been lost); afterwards an id
cannot be taken over (`409`).

Answers: `200` ok / `duplicate`, `201` registered, `400 unsupported_schema` (with the list that is supported), `401`
unauthorised (the same for an unknown id and a wrong token), `410` unregistered or deleted, `413`, `415`, `422
invalid_report` (with the field), `429` (with `Retry-After`). Limits per installation and hour: register 5,
heartbeat 12, health 12, inventory 6, features 6; 600 requests per address and hour.

A client queues what it could not send and retries with exponential back-off and jitter; it never blocks the
panel (reports are sent from the background scheduler) and gives up on a report after 7 days.

## Retention

Heartbeats 90 days, feature and health snapshots 180 days, the request log (endpoint, answer code, no address)
30 days, an installation that has been silent for 400 days. `php bin/site telemetry:maintain` (daily from cron)
applies this.

## Developer dashboard

Administrators of the website only: Admin → Telemetry. Active installations, registrations, versions, systems,
hardware buckets, software and feature adoption, health, install and upgrade success, interface health; filters for
period, version and system; CSV export of every aggregate. Nothing is shown when there is no data ("N/A"), nothing
is estimated, and a single installation's id is never displayed or exportable. Only installations that opted in are
counted, so the numbers are lower bounds.

## The old installer report

Earlier installers sent one anonymous report (`installer/telemetry.sh` → `/api/v1/installs`: version, OS name and
version, CPU type, random id) without asking. That is replaced by the consent question; the interface still accepts
reports from servers installed before, and the public statistics page keeps showing them.
