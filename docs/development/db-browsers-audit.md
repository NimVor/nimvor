# Database browsers: audit, gap analysis and delivery plan

Status of the PostgreSQL, MySQL/MariaDB and MongoDB browsers, checked against the code (not against the buttons).
Written as Phase 1 (audit) and Phase 2 (gap analysis) of the "complete database browser" work; Phase 3 onwards is
delivered in slices, each its own pull request with tests, and this file is updated by every slice.

## How features were judged

A feature counts as **Implemented and working** only when all of these exist: an agent action, the panel
endpoint and permission, the UI, and an automated test that runs it against a real database. Anything weaker is
labelled for what it is:

| Status | Meaning |
|---|---|
| **Working** | Agent action, panel endpoint, UI and a real-database test. |
| **Partial** | Present, but narrower than the requirement (stated in the row). |
| **Script-tested** | Agent action and UI exist; the tests only check the generated command or the parsed output against a fake runner, never a real server. |
| **SQL editor only** | Possible today only by typing the statement in the SQL tab (PostgreSQL: runs as the database owner); there is no structured, validated, audited control. |
| **API only** | Agent action exists, no UI. |
| **Missing** | Nothing exists. |
| **Unsupported** | The engine has no such thing; the UI must say so and not fake it. |

## What was inspected

- Agent: `internal/pgmgr` (22 actions `pg.*`), `internal/dbmgr` (`db.browse_*`, `db.query`, MySQL wire client
  `internal/mysqlwire`), `internal/mongomgr` (`mongo.*`, runs `mongosh`).
- Panel: `PostgresController`, `DbBrowserController`, `MongoController`, `SiteMongoController`, their views and
  scripts (`postgres-*.js`, `dbbrowser.js` 759 lines, `mongobrowser.js` 1,084 lines), permissions and routes.
- Tests: `pgmgr/*_test.go` and `realhost_test.go`, `dbmgr/browse*_test.go` and `realhost_browse_test.go`,
  `mongomgr/*_test.go`, the panel's `Postgres*Test`, database and Mongo HTTP tests.

## Test infrastructure (what can be verified)

| Engine | Real server in CI | Real server in this development environment |
|---|---|---|
| PostgreSQL | Yes: real-host job and the seven-system VM matrix (each system's own PostgreSQL version, so several majors) | PostgreSQL 16 |
| MySQL/MariaDB | Yes: MariaDB in the real-host job and the VMs (`TestRealHostDatabaseBrowser`) | MariaDB |
| **MongoDB** | **No.** No test installs or starts `mongod`; `mongomgr` tests use a fake `mongosh` runner | **No `mongod`/`mongosh`** |

**Consequence for MongoDB:** every MongoDB statement below stays *Script-tested* until a real `mongod` is part of the
real-host tests. The agent already has `mongo.install`, so the real-host test can install MongoDB through it (as the
PostgreSQL test does) on the systems MongoDB supports. That is part of the MongoDB slices, and MongoDB features are not
called finished before they pass there. MongoDB versions and server editions differ (for example time-series needs
5.0+, hidden indexes 4.4+, wildcard indexes 4.2+), so capabilities are detected from the server, not assumed.

## Architecture the new work follows

- Every database change is a **named agent action with structured input** (never free SQL or a free `mongosh`
  string): identifiers are checked against the catalog, quoted with the engine's own rules, values are parameters, the
  action is `Mutating: true` (behind the write gate), audited and redacted.
- PostgreSQL DDL runs as the **database owner** through the per-database account (never as `nimvor_admin`), exactly as
  the table browser and the SQL editor do; MySQL runs through the existing browser account (`withBrowser`); MongoDB
  through the existing `mongosh` client. Reuse of connection management, permissions (`databases.manage`,
  `databases.view`) and ownership checks stays as it is.
- One panel frame per engine with the same structure (sidebar objects, tab strip, toolbar), as the PostgreSQL browser
  now has. Destructive actions confirm; bulk actions return one result per object and never stop at the first failure.
- Engine capabilities (server version, MariaDB vs MySQL, storage engine, MongoDB edition) are read from the server and
  returned with the listing, so the UI shows only controls the server supports.

## Feature matrix

### 1. PostgreSQL

| # | Requirement | Status today | Evidence / gap |
|---|---|---|---|
| 1.1 | Multi-select tables/views, bulk empty, drop, analyze, maintenance | **Missing** | The table list is links only. `TRUNCATE`, `DROP` and `ANALYZE` are possible only in the SQL editor. No `pg.*` action for them. |
| 1.2 | Complete tables/views listing with statistics | **Partial** | `pg.tables`: schema, name, kind (table/view), estimated rows, total size. Missing: object types (partitioned, materialized view, foreign), data/index size, counts of indexes/columns/constraints/triggers/partitions, last analyze/vacuum, search by type, sort, pagination. |
| 1.3 | Index management | **Partial** | Indexes are listed read-only (`pg.table_structure`, definition text). Create, drop, rename, methods, partial/expression/concurrent, validity, size, unused-index detection: **SQL editor only**. |
| 1.4 | Constraints | **Missing** | Only the primary key is reported (to address rows). No list, create, drop, rename, validate. SQL editor only. |
| 1.5 | Triggers | **Missing** | SQL editor only. |
| 1.6 | Sequences / identity | **Missing** | SQL editor only. |
| 1.7 | Procedures & functions | **Missing** | SQL editor only; the editor has syntax highlighting and completion but no routine-aware validation. |
| 1.8 | Partitions | **Missing** | SQL editor only. |
| 1.9 | Columns | **Partial** | Columns are listed read-only (name, type, null, default, key). Add/drop/rename/change type/default/NOT NULL/identity/generated/comment: SQL editor only. |
| 1.10 | Table management | **Partial** | Browse, edit rows (key tables), CSV export work (real-database tested: `TestBrowserAsOwner`, `TestRealHostPostgres`). Create/drop/rename/empty/clone/owner/comment/move schema/dependencies: SQL editor only. |
| – | Dashboard (version, size, counts) | **Partial** | `pg.overview` and `pg.monitor` exist for the server page, not per database. |

PostgreSQL `REPAIR`: **Unsupported** (no equivalent). The requirement itself says to offer diagnostics instead:
`VACUUM (ANALYZE)`, `REINDEX`, `amcheck` where the extension is present, bloat estimate, invalid indexes.

### 2. MySQL / MariaDB

| # | Requirement | Status today | Evidence / gap |
|---|---|---|---|
| 2.1 | Multi-select, bulk empty/drop/analyze/optimize/check/repair | **Partial** | `db.browse_alter` runs `truncate/drop/analyze/optimize/check/repair` **one table at a time** (real-database test `TestRealHostDatabaseBrowser`, SQL generation `TestAlterSQL`). No multi-select, no per-object bulk result. Engine limits (REPAIR on InnoDB) are not explained in the UI. |
| 2.2 | Listing with statistics | **Partial** | `db.browse_tables`: name, type, engine, estimated rows, **combined** data+index size, collation, comment. Missing: separate data/index size, column/index/constraint/trigger/partition counts, create/update time in the list, filters, sort, pagination, database summary. |
| 2.3 | Indexes | **Partial** | Add/drop index incl. unique, fulltext (`add_index`, `drop_index`). Not verified (to check in S5): spatial, prefix lengths, rename, visibility (MySQL 8 invisible / MariaDB ignored), cardinality and size display. |
| 2.4 | Constraints | **Partial** | Foreign keys add/drop with ON DELETE/UPDATE (`add_foreign_key`). Missing: check constraints, unique as constraint, rename, definitions list beyond FKs. |
| 2.5 | Triggers | **Missing** | Not listed or managed by any action (not verified whether `db.query` can run `CREATE TRIGGER`; it is not a structured trigger manager either way). |
| 2.6 | Auto-increment / sequences | **Partial** | The table statistics query reads `AUTO_INCREMENT`; there is no action to change or reset it; MariaDB sequences **Missing** (engine-specific: MariaDB 10.3+ only). |
| 2.7 | Procedures & functions | **Missing** | |
| 2.8 | Events | **Missing** | Needs a scheduler-enabled warning (`event_scheduler`). |
| 2.9 | Partitions | **Missing** | |
| 2.10 | Columns | **Partial** | Add/modify/drop column with position (`ColumnSpec`, `add_column`, `modify_column`, `drop_column`; `TestColumnSpecSQL`). Not verified (to check in S4): rename as a first-class operation, comments, generated columns, AUTO_INCREMENT toggle. |
| 2.11 | Table management | **Partial** | Create (`create_table`), rename, drop, drop view, truncate, optimize/analyze/check/repair, CSV import/export, row CRUD. Missing: clone structure / with data, change engine, charset/collation, table comment, dependencies, view creation. |

### 3. MongoDB

| # | Requirement | Status today | Evidence / gap |
|---|---|---|---|
| 3.1 | Multi-select and bulk | **Missing** | Collection actions are one at a time (`collection_empty`, `collection_drop`, `collection_stats`). |
| 3.2 | Listing with statistics | **Partial, script-tested** | `mongo.collections` (counts), `mongo.collection_stats` (count, size, storage size, average size, index count and size, capped, type, validator, level, action). The list view does not show them all together; no filter/sort/pagination; time-series flag missing. |
| 3.3 | Indexes | **Script-tested, Partial** | `mongo.index_create` takes keys and options as EJSON, so every index type the server supports can be requested through the API (`TestIndexesListCreateDrop`); the UI form covers unique/sparse/TTL (to verify the rest). Missing: type-specific forms (text, geo, hashed, wildcard, partial, hidden), usage statistics, rename. |
| 3.4 | Validation / schema | **Partial (read-only)** | Validator, level and action are *read* by `collection_stats`. No `collMod` action, no editor, no `validate` command. |
| 3.5 | Views | **Missing** | |
| 3.6 | Collection management | **Partial** | Create (plain only), drop, rename, empty exist. Missing: capped, time-series, options, clone. |
| 3.7 | Documents | **Script-tested** | Find, insert, update, delete, aggregate, explain, import/export exist (`TestBrowserCollectionsDocumentsAndDelete`). **BSON fidelity is unverified**: the agent uses EJSON, which round-trips ObjectId/Date/Decimal128/Binary/Timestamp when relaxed mode is off; the stats call uses `relaxed:true`, so each path must be checked. Bulk delete, duplicate: **Missing**. |
| 3.8 | Database level | **Partial** | `mongo.overview`, `database_create/drop`. Missing: per-database statistics panel, replica set / sharding information. |
| 3.9 | Advanced (change streams, TTL, replica set, shards) | **Missing** | Detect and display only; do not expose controls the server cannot run. |

## Gap analysis (summary)

| Area | PostgreSQL | MySQL/MariaDB | MongoDB |
|---|---|---|---|
| New agent actions needed (estimate) | ~30 (list, bulk, DDL for tables, columns, indexes, constraints, triggers, sequences, routines, partitions, diagnostics) | ~20 (stats listing, bulk, triggers, routines, events, partitions, auto-increment, clone, engine/charset, views, check constraints, rename ops) | ~12 (list with stats, bulk, validation, collMod, validate, views, capped/time-series create, clone, db stats, replica/shard info) |
| New panel endpoints | ~12 (grouped by object type, like the row actions) | ~8 | ~8 |
| UI | tabs and dialogs on the existing frame | extend `dbbrowser.js` (sidebar objects, dialogs) | extend `mongobrowser.js` |
| Security | DDL as the database owner; identifiers validated against `pg_catalog` then quoted; destructive confirms; audit names the object, never data | existing browser account, identifiers validated against `information_schema`; no free SQL for DDL | collection/db names validated; options from a fixed schema, never raw EJSON for administrative commands |
| Version limits | partitions and generated columns need 10+/12+; `REINDEX CONCURRENTLY` 12+; identity 10+; pre-15 public schema | check constraints MySQL 8.0.16+/MariaDB 10.2+; invisible indexes MySQL 8/MariaDB 10.6 `IGNORED`; sequences MariaDB 10.3+; events need the scheduler | time-series 5.0+, hidden indexes 4.4+, wildcard 4.2+, `validate` options vary |
| Tests | real-database tests per action (CI already has PostgreSQL and MariaDB) | same | **needs a real `mongod` in the real-host tests first** |

## Delivery plan

Each slice is one or more pull requests: agent actions + panel + UI + real-database tests + docs + changelog, merged
only when CI is green. Order follows the requested priority; every slice covers the engines it applies to before the
next slice starts.

| Slice | Content | Engines |
|---|---|---|
| S1 | Database dashboard, complete object listing with statistics, search/filter/sort/pagination | PG, MySQL, Mongo |
| S2 | Multi-select and bulk operations with per-object results (engine-appropriate maintenance) | PG, MySQL, Mongo |
| S3 | Table/collection management (create, rename, clone, comments, owner/schema, capped/time-series) | all |
| S4 | Columns (PG), column gaps (MySQL), document management and BSON fidelity (Mongo) | all |
| S5 | Index management | all |
| S6 | Constraints and validation | all |
| S7 | Triggers, procedures and functions, events | PG, MySQL |
| S8 | Sequences / auto-increment | PG, MySQL |
| S9 | Partitions | PG, MySQL |
| S10 | Mongo views, advanced features, replica/shard metadata | Mongo |
| S0 | Real-host MongoDB test harness (installs through `mongo.install`) | Mongo; prerequisite for calling any MongoDB slice finished |

## Open limits stated up front

- PostgreSQL has no `REPAIR TABLE`; it gets diagnostics instead. MySQL `REPAIR TABLE` works only for engines that
  support it (MyISAM, Aria, ARCHIVE, CSV); the UI says so per table.
- MariaDB sequences are MariaDB-only; MySQL gets `AUTO_INCREMENT` control and an explanation, not a fake sequence.
- MongoDB features that depend on edition or topology (time-series, change streams, sharding) are shown only when the
  server reports them.

## Implementation status

Verified means a test ran against a real server in the development session (PostgreSQL 16, MariaDB 10.11). MongoDB
could not be installed there; its agent actions are unit-tested against a fake `mongosh` (validation, quoting, the
script text) and have a live test, `TestMongoLiveBrowser`, that CI runs in the `mongo-live` job against a real
`mongod`. That job is `continue-on-error` until it has been seen passing; until then MongoDB behaviour against a real
server is **not verified**.

| Slice | PostgreSQL | MySQL / MariaDB | MongoDB |
|---|---|---|---|
| S1 overview, object list with statistics | done, verified | done, verified | done, fake-runner tested |
| S2 multi-select, bulk, per-object results | done, verified | done, verified | done, fake-runner tested |
| S3 table / collection management | create, rename, duplicate, comment, move schema: verified | comment, engine, charset, row format, AUTO_INCREMENT, duplicate: verified | capped, time-series, copy (with indexes): fake-runner tested |
| S4 columns and documents | add, change type/null/default/comment, rename, drop: verified | already present before this work | delete-many, duplicate, structured update-many: fake-runner tested |
| S5 indexes | create (all methods, unique, desc, partial, include, concurrent), rebuild, rename, drop; size, scans, validity: verified | listing and add/drop already present; rename added: verified | usage ($indexStats), hide/unhide: fake-runner tested |
| S6 constraints / validation | primary, unique, check, foreign, NOT VALID, validate, rename, drop: verified | structured CHECK add/drop and listing: verified | `$jsonSchema` rules, level, action: fake-runner tested |
| S7 triggers, routines, events | triggers enable/disable/drop, routines list/definition/drop: verified | routines, triggers, events: list, definition, drop, event enable/disable: verified | not applicable |
| S8 sequences | create, alter, set value, restart, sync to column, drop: verified | MariaDB sequences: verified; MySQL shows a note | not applicable |
| S9 partitions | list, create, attach, detach: verified | list, add, drop, truncate, analyze, rebuild, remove: verified | not applicable |
| S10 views, advanced | not applicable | not applicable | views (no `$out`/`$merge`), db stats, replica/sharded role: fake-runner tested |

### Deliberate limits (not placeholders)

- Creating or editing the *body* of a routine, trigger or event is code and stays on the SQL tab; the browsers list,
  show, enable/disable and drop them.
- Partitioning an existing MySQL/MariaDB table (`PARTITION BY`) and partition bounds that need an expression
  (`TO_DAYS(...)`) are written on the SQL tab; bounds entered in the browser are literal values.
- PostgreSQL column types are the built-in ones (plus sizes and arrays); other types use the SQL tab.
- PostgreSQL has no repair statement; VACUUM, ANALYZE and REINDEX are offered. MySQL/MariaDB REPAIR is refused for
  engines that do not support it.
- Browser-side JavaScript (MySQL and MongoDB tabs, dialogs) is checked for syntax and exercised through the HTTP
  contract it calls; it was not driven in a real browser in this session.
- Extending the PostgreSQL panel to remote servers uses the same actions; remote-server DDL was not separately tested.
