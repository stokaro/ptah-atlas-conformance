# Parity status — what this does and does not test

**This is not a full feature-set parity test, and no number in this repository
should be read as one.**

It is an offline, structural coverage probe over the full vendored Atlas
`*/testdata/*` snapshot plus first-party Atlas-compatible regression fixtures,
run through narrow entry points of Ptah's public API. It exists to turn "are we
there yet" from an opinion into a number that moves over time. Treat the results
as a floor on the distance to Atlas, never a ceiling.

Generated snapshot: 286 vendored upstream testdata files grouped into 160
fixtures, 697 offline observations, **0 unwaived non-OK offline observations**.
The corpus inventory imports 160 fixtures: every imported fixture is measured by
at least one current offline probe. This means the deterministic imported-corpus
report is green; it does **not** mean full Atlas OSS runtime parity, because
several runtime dimensions remain unmeasured.

## What the probe found broken

All probes are offline and static — nothing is applied to a real database.

| Probe | What it checks | Result |
| --- | --- | --- |
| `corpus-inventory` | Is every imported Atlas test artifact visible in the report? | All 160 imported fixtures are measured by at least one current offline probe. |
| `sql-parse` | Can Ptah's DDL parser represent Atlas SQL in its AST (the `read-db` / `compare` round-trip path — **not** apply)? | Runs over all vendored `.sql` files covered by the offline probe set and is currently green on the imported corpus. |
| `migdir-ingest` | Does Ptah's migrator recognize the files in an Atlas migration directory? | Current measured Atlas migration directories are recognized. |
| `txtar-script` | Can the harness consume Atlas integration `.txtar` scripts? | Imported `.txtar` scripts are parsed and reported; supported command contours are green in the current offline corpus, and each OK row lists the script surface exercised or asserted by that fixture. |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does its own hash reproduce it? | Current measured fixtures pass the parsed/recomputed sum compatibility probe. |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content? | Current analyzer catalog and fixture-level lint parity probes are green on the committed corpus. |

Each probe recovers from panics; none panicked on this corpus.

## What is NOT tested at all

This is the important half of "what doesn't work": most of Atlas's open-source
(Apache-2.0) surface is not exercised here, so for those areas the honest answer
is **"unknown — not measured"**, not "works".

| Atlas open-source capability | Tested here? | Where it would be measured |
| --- | --- | --- |
| Schema **introspection** breadth (types, defaults, generated/partial/expression indexes, sequences, domains, composite types, FK actions, exclusion constraints, partitioning, collations, comments) | **Partially** — live/diff fixtures now cover basic tables, enums, views, indexes/FKs, composite keys, constraints/actions, generated columns, self-references, and richer defaults/types | broader dedicated introspection probes against live DBs |
| Schema **diff / plan** (desired A → desired B → migration) | **No** | a diff probe over paired schema fixtures |
| **End-state equivalence** (apply a schema, then compare what Atlas and Ptah each report about the result) | **Partially** — the `conformance-diff` differential tier compares Atlas CE's `schema inspect` against Ptah's introspect → render on live Postgres, MySQL, and SQLite targets, scoped to CE-visible object kinds | `conformance-diff` workflow; deeper apply-with-each-tool equivalence is still ptah#285 |
| **HCL** schema language | **Partially** — Atlas CE inspect HCL is parsed in the differential tier and the imported offline corpus is green, but this is not broad Atlas HCL language parity | broader HCL schema fixtures and runtime command probes |
| Versioned-migrate **runtime semantics** (tx-mode, execution order, baseline, advisory lock, revision-table shape) | **No** | a runtime probe; several are open Ptah issues (#124, #265, #275) |
| Atlas CLI **help and flag surface** | **Measured separately** — `cli-surface.md` compares the pinned Atlas CE binary's command tree, usage strings, and long flags against both `ptah atlas ...` and `ptah-compat`; the current committed report is green on the pinned Atlas CE surface | `cli-surface.md` / `cli-surface.json`, `make gate-cli-surface` |
| **sqlcheck analyzers**, rule by rule | **No** | a lint matrix mapping Atlas codes ↔ Ptah rules |
| **Multi-dialect** depth (MySQL, SQLite, MariaDB) | **Partially measured** — live round trips run on Postgres, MySQL, MariaDB, and SQLite in CI; Atlas CE differential runs on Postgres, MySQL, and SQLite | deeper dialect runtime probes |
| DDL parse/round-trip **breadth** | **Measured over all vendored `.sql` files** | still parser-only, not apply/runtime equivalence |
| The migration **apply** path | **No** | Ptah applies migration SQL via raw `ExecContext`, bypassing its parser — a parse gap here is *not* an apply gap |

## Honest framing, both directions

- A `sql-parse` gap is a **round-trip** gap, not "Ptah cannot do it". Ptah
  generates views, materialized views, triggers, RLS and grants from Go
  annotations and renders them, and reads them back from a live database through
  its `dbschema` readers. What its text SQL parser does not do is re-parse those
  constructs from raw SQL.
- Ptah is a real engine (PostgreSQL / MySQL / MariaDB / ClickHouse, diff,
  planner, linter, safety gate, seeder, online DDL). "Does everything Atlas's
  open-source core does" is false; "is a toy" is also false. The truth is in
  between and, for most of the surface, **not yet measured here**.
- Some Ptah capabilities are the **local, open half of an Atlas *Pro* feature**
  and have **no CE oracle to differential against**, so they are covered by
  Ptah's own behavioral tests rather than a conformance probe here. Pre-migration
  assertion checks (`-- +ptah check`, ptah#661) are one: Atlas keeps pre-migration
  checks in its proprietary build, and Atlas CE offers neither the check
  execution nor the Cloud approval half, so there is nothing to compare against.
  This is scope, not a gap — the check parser and apply-abort behavior are
  verified in Ptah's `migration/migrator` package.
- Native migration **import** (`ptah migrations import`, ptah#667) is Atlas OSS
  `migrate import` parity, but it emits **Ptah-native** format (not Atlas format),
  so it is not a schema-object round-trip. It is measured directly by the
  `golang-migrate/import-roundtrip`, `goose/import-roundtrip`, and
  `flyway/import-roundtrip` migrate-runtime probes: import a source directory,
  then assert the output passes `ptah migrations validate`. The Flyway probe also
  exercises dotted versions, an undo (`U__`) file mapped to a down, and a
  repeatable (`R__`) migration imported as a one-time migration. golang-migrate,
  Goose, and Flyway are covered; the Liquibase parser (and its probe) is a phased
  follow-up.

## What the `ptah atlas` and analyzer probes now measure exhaustively

Two behavioral probes make part of the drop-in surface exhaustively measured, so
that when they go green Ptah genuinely covers that dimension:

- **`atlas-cli-surface`** in the offline fixture report checks, against the real
  Ptah binary, whether important OSS Atlas command paths resolve.
- **`cli-surface.md`** is the stricter dedicated CLI-surface yardstick. It builds
  or reads the pinned Atlas CE binary, discovers the current `atlas schema ...`
  and `atlas migrate ...` command tree from Cobra help, records usage lines and
  long flags, and compares both Ptah surfaces separately: `ptah atlas ...` and a
  `ptah-compat` binary named `atlas`. This report also lists Cloud/commercial or
  intentionally unsupported commands explicitly and checks that Ptah reports the
  same community-version unsupported boundary instead of silently omitting them
  or falling back to parent command help. The current full gate is green on the
  pinned Atlas CE surface; future Atlas changes should either keep this green
  by implementing Ptah parity, or create explicit tracked gaps instead of
  dropping commands from the inventory.
- **`lint-analyzer-catalog`** covers the full set of Atlas analyzer concerns that
  fire by default in an OSS build — the DS, MF (data-dependent), BC, CD, PG1, PG3,
  PG110, MY, LT and TX families. This is the "lint matrix" listed below as a
  requirement. Its criterion is **behavioral**: a concern reads green when Ptah's
  linter emits any substantive finding on the change, with the actual Ptah rule
  code shown, so it measures "does Ptah warn about this change" and flips green on
  its own when Ptah adds an equivalent rule. Note that Ptah's rule *codes* collide
  with Atlas's (Ptah `PG102` is an enum-in-transaction rule, not Atlas's
  drop-index rule), which is exactly why the probe matches on behavior, not codes.
  Deliberately excluded: NM (naming) fires only under a configured policy, and
  SA (injection) / OW (ownership) are policy/enterprise analyzers — none run in a
  default OSS pass, so their absence is not a default drop-in gap.
- **`lex-split-parity`** is a differential check against Atlas's own recorded
  output: for every Atlas lexer fixture that ships a `.golden`, it asks whether
  Ptah's statement splitter breaks the SQL into the same statements Atlas does.
  This is real drop-in behavior — if Ptah splits a stored function body, a
  `BEGIN ATOMIC` block or a MySQL `DELIMITER` section differently, the migration
  executes differently. It uses Atlas's committed goldens, so it needs no live
  Atlas binary (which does not build cleanly on current Go and whose release is
  proprietary). SQL Server delimiting (GO / BEGIN TRY) is out of scope — SQL
  Server is a Pro Atlas driver.

A fifth, **live** tier now measures behavioral self-consistency on a real
database (`conformance-live` workflow, separate from the offline report):
`roundtrip-consistency` applies a first-party Ptah schema to **Postgres, MySQL,
MariaDB, and SQLite** (`CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL` /
`CONFORMANCE_MARIADB_URL` plus a fresh local SQLite database by default),
introspects it back, and diffs. A clean diff guarantees Ptah's generate → apply
→ introspect loop is lossless for that schema on that dialect — behavior a
drop-in needs. Running the same fixtures on MySQL immediately found
dialect-specific rendering defects Postgres alone missed, including
enum-ordering, generated-column, default/type, and constraint/action bugs that
are now closed. The current committed live corpus is green on Postgres, MySQL,
MariaDB, and SQLite. It is Ptah-vs-Ptah, so it carries no Pro/OSS ambiguity
about which objects Atlas itself inspects. The deeper differential correctness
of each declarative command (does `schema inspect` emit equivalent HCL) remains
the domain of the end-state conformance in ptah#285.

A sixth, **differential** tier (`conformance-diff` workflow) closes part of that
end-state question against a **real Atlas CE binary**. It applies a first-party
Ptah schema to Postgres, MySQL, and SQLite, then reads what both tools understand
as a *typed* schema and compares them by typed schema facts: columns, type/nullability/default/
primary-key state, generated columns, foreign keys with referential actions,
unique/check constraints, and indexes. Atlas's view comes from `schema inspect` in
its native HCL, parsed by Ptah's own `core/atlashcl` into a `goschema.Database`;
Ptah's view comes from its introspect → convert chain. Because both sides are the
same typed structure, there is no SQL-text parsing — the comparison folds
semantically-equivalent representations on typed fields (serial ≡ integer+nextval,
`character varying`/`character_varying` ≡ `varchar`, `timestamp` ≡ `timestamp
without time zone`, inline ≡ table-level PRIMARY KEY, simple default constants,
and `NO_ACTION` ≡ `NO ACTION`).
Notably Ptah's SQL parser cannot ingest Atlas's SQL inspect output (schema-
qualified `REFERENCES`, enum `CREATE TYPE`) — the very subset limit the `sql-parse`
probe measures — which is why the HCL path is used; a failure to parse Atlas's HCL
is itself reported as a gap (ptah#276), distinct from a schema disagreement.
Atlas is built from the release tag pinned in `atlas.version` (renovate-bumped),
so it measures Ptah against a known Atlas release. It is deliberately scoped to
CE-visible object kinds: Atlas CE silently omits Pro-gated objects (views,
triggers, functions, sequences) from inspection — no error, exit 0 — so those
cannot be compared apples-to-apples and Ptah's support for them is a strength
beyond CE rather than a differential gap (they stay covered by the Ptah-vs-Ptah
round-trip tier). The committed differential corpus is currently green across
Postgres, MySQL, and SQLite fixtures, including constraints/actions, generated
columns, self-references, and default/type cases. The folding logic is locked by offline unit tests so it
cannot silently start passing on genuine differences.

## What a real full-parity test would require

To earn the phrase "feature-set parity test", this repo would need, at minimum:

1. Deeper runtime probes for the imported Atlas `.txtar` integration fixtures,
   beyond the current virtual command runner and fixture-level script-surface
   inventory.
2. An **introspection** probe: apply a schema with each tool, introspect with
   one reader, diff the canonical states (this is ptah#285 and needs a live DB).
   *Partially built:* the `conformance-diff` tier already compares Atlas CE's and
   Ptah's introspection of the same live schema, scoped to CE-visible objects.
3. A **diff/plan** probe over paired before/after schemas.
4. A **lint matrix** comparing Atlas analyzer codes against Ptah rule codes,
   fixture by fixture.
5. Broader **multi-dialect** runtime coverage: Postgres, MySQL, MariaDB, and
   SQLite now run in the live tier; Postgres, MySQL, and SQLite now run in the
   Atlas CE differential tier, but deeper live/runtime probes are still needed.
6. A declared, justified scope for what is deliberately **out** of parity (HCL
   schema, Cloud, Pro drivers), so "parity" has an explicit boundary.

Until those exist, this repository answers a broader but still bounded question
honestly — *where, across Atlas's vendored testdata snapshot, does Ptah visibly
fail to ingest what Atlas produced, and which imported fixtures are not measured
yet* — and nothing wider.
