# Parity status — what this does and does not test

**This is not a full feature-set parity test, and no number in this repository
should be read as one.**

It is a deterministic coverage probe over the full vendored Atlas
`*/testdata/*` snapshot plus first-party Atlas-compatible regression and
workflow fixtures, run through Ptah's public API and real CLI. Most observations
are structural and database-free; the `dbtest-workflow`,
`composite-schema-workflow`, `managed-data-workflow`, `checkpoint-workflow`,
`external-schema-workflow`, `pro-test-workflow`, `pro-maint-workflow`,
`pro-plan-workflow`, `pro-down-workflow`, `desired-state-workflow`,
`apply-simulation-workflow`, `schema-scope-workflow`,
`inspect-source-workflow`, and `qualifier-txmode-workflow`
capability probes use fresh local SQLite databases (or no database at all) and
no external service. It
exists to turn "are we there yet" from an opinion
into a number that moves over time. Treat the results as a floor on the distance
to Atlas, never a ceiling.

Generated snapshot: 286 vendored upstream testdata files plus first-party
regression and capability fixtures, grouped into 158 imported Atlas fixtures,
17 first-party capability sentinels, and 805 deterministic observations, with
**0 unwaived non-OK observations**. Every imported fixture and capability
sentinel is measured by at least one current probe. This means the
deterministic report is green; it does **not** mean full Atlas OSS runtime
parity, because several runtime dimensions remain unmeasured.

## What the probe found broken

Corpus probes are offline and structural. The `dbtest-workflow`,
`composite-schema-workflow`, `managed-data-workflow`, `checkpoint-workflow`,
`external-schema-workflow`, `pro-test-workflow`, `pro-maint-workflow`,
`pro-plan-workflow`, `pro-down-workflow`, `desired-state-workflow`,
`apply-simulation-workflow`, `schema-scope-workflow`,
`inspect-source-workflow`, and `qualifier-txmode-workflow`
probes also execute committed first-party fixtures through the real Ptah CLI
against ephemeral SQLite databases (`pro-maint-workflow` is fully offline).

| Probe | What it checks | Result |
| --- | --- | --- |
| `corpus-inventory` | Is every imported Atlas test artifact visible in the report? | All 158 imported Atlas fixtures are measured by at least one current probe; 17 first-party capability sentinels are reported separately. |
| `sql-parse` | Can Ptah's DDL parser represent Atlas SQL in its AST (the `read-db` / `compare` round-trip path — **not** apply)? | Runs over all vendored `.sql` files covered by the offline probe set and is currently green on the imported corpus. |
| `migdir-ingest` | Does Ptah's migrator recognize the files in an Atlas migration directory? | Current measured Atlas migration directories are recognized. |
| `txtar-script` | Can the harness consume Atlas integration `.txtar` scripts? | Imported `.txtar` scripts are parsed and reported; supported command contours are green in the current offline corpus, and each OK row lists the script surface exercised or asserted by that fixture. |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does its own hash reproduce it? | Current measured fixtures pass the parsed/recomputed sum compatibility probe. |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content? | Current analyzer catalog and fixture-level lint parity probes are green on the committed corpus. |
| `dbtest-workflow` | Do Ptah's native declarative migration and schema test commands preserve their key end-to-end CLI contracts? | Both commands execute committed fixtures against isolated SQLite databases; numeric/latest/zero migration targets, desired-schema application and drift repair, seed steps, all assertion kinds, `--run`, text/JSON/HTML reports, invalid schema steps, and command-specific exit codes 1/2 are checked. |
| `composite-schema-workflow` | Do multiple desired-schema sources behave exactly like one hand-merged source? | Complete executable-SQL stdout snapshots, source conflicts, generated up/down equivalence, direct live SQLite schema facts, clean mixed/hand-merged comparisons, and a drift-detecting negative control are checked. |
| `managed-data-workflow` | Does Ptah's declarative reference/seed data round-trip apply, introspect, and converge? | A model's `//ptah:schema:data` rows are applied via `ptah migrations data`, the seeded rows are introspected and matched to the declared desired state, a re-diff converges to "no data changes", a divergent desired set is refused by the destructive gate (exit 2), and rolling the data migration back removes exactly the inserted rows. |
| `checkpoint-workflow` | Does Ptah's migration checkpoint round-trip squash, bootstrap, and stay safe? | A three-migration history is squashed by `ptah migrations checkpoint` into a deterministic cumulative-schema pair covered by a rewritten `ptah.sum`; a fresh database bootstraps from the checkpoint alone to a schema structurally identical to the full replay; an already-migrated database ignores the checkpoint; a tampered checkpoint fails `validate` (exit 1); rolling back below the boundary is refused (exit 2) while rolling back to zero runs the checkpoint's down body; and a post-checkpoint migration bootstraps on top of the checkpoint. |
| `external-schema-workflow` | Do Ptah's native static and external desired-schema sources work end to end? | Twenty observations cover static SQL; external SQL/HCL/YAML providers; complete executable-SQL render stdout; trust denial without side effects for render, compare, drift, plan, and generate; allowed configuration; generated migration application; table, primary-key, unique-index, and cascading-foreign-key facts; and converged no-op results on ephemeral SQLite. |
| `pro-test-workflow` | Do the forwarded Atlas Pro test verbs preserve their end-to-end CLI contracts? | `atlas migrate test` applies the Atlas-format directory to a real SQLite dev database and runs the committed case set (exit 0); a deliberately failing assertion exits 1 with a structured FAIL report; `atlas schema test` provisions the desired schema from a local Go-annotation source and holds the same pass/fail exit contract. |
| `pro-maint-workflow` | Do the forwarded Atlas Pro directory-maintenance verbs keep the directory valid? | `atlas migrate edit` applies a hermetic scripted `$EDITOR` change and rewrites `atlas.sum`; `atlas migrate rebase` moves a migration to the end of history under the deterministic next version; `atlas migrate rm` removes a migration and its checksum entry; `ptah migrations validate` passes after every verb. |
| `pro-plan-workflow` | Does the local `schema plan` workflow bind plans to fingerprints? | `atlas schema plan --save` and `atlas schema plan new` write fingerprinted local plan files against real SQLite targets; `atlas schema plan validate` accepts a fresh plan without mutating the target and rejects a stale one; `atlas schema apply --plan file://...` replays the reviewed plan and refuses a target mutated after planning. |
| `pro-down-workflow` | Do default-output and formatted `atlas migrate down` runs revert Atlas-format revisions non-interactively? | `atlas migrate apply` records two Atlas-format revision rows; `atlas migrate down --format` reverts one with a checked JSON report and live database end state, then a bare `atlas migrate down` — no stdin, Ptah-only `--confirm`, or `--revision-format` flag — reverts the other and clears the revision history (stokaro/ptah#810 and #971; previously a silent no-op or an interactive rollback). |
| `desired-state-workflow` | Do Atlas desired-state source URLs drive `schema diff`, `schema apply`, and `migrate diff`? | A live SQLite URL works as the `--from` diff source and the `--to` apply/diff source; an atlas.sum-covered migration directory replays on a dev database; evaluated `env://` references supply source, dev, and directory defaults; generated migration directories validate, replay on fresh targets, and converge without rewriting artifacts; and textually different desired/dev path aliases are refused before source data is mutated or artifacts are created (stokaro/ptah#811, stokaro/ptah#842). |
| `apply-simulation-workflow` | Do the `schema apply` locking and dev-simulation guard rails hold? | `--lock-timeout` on lockless SQLite is an explicit no-op with a deterministic stderr note and the apply proceeds; `--dev-url` resets a pre-littered dev database and rehearses the exact plan there before the target is touched; a rehearsal made to fail (via a hermetic scripted `$EDITOR` and `--edit`) refuses the apply with exit 1 and zero user tables on the target; `--dev-url` pointing at the target is refused before the destructive dev reset (stokaro/ptah#812). |
| `schema-scope-workflow` | Does `--schema`/`--include` scoping hold end to end? | A scoped apply creates only the selected table while desired-but-unselected tables stay uncreated and a pre-existing out-of-scope table survives; repeated `--include` values union; selecting a table whose foreign key points at an unselected table is refused with the cross-scope dependency diagnostic and remediation guidance; a malformed selector fails before the dev database file exists (stokaro/ptah#813). |
| `inspect-source-workflow` | Does the `schema inspect` source and export model hold? | A local schema file is materialized on a dev database and introspected to HCL, with a scheme-less path classifying as the identical local-file source; `{{ hcl . \| split \| write "dir" }}` exports the deterministic per-object tree whose written files reload as a multi-file desired state diffing as synced; `--exclude` filters resource selectors, accepts the documented `[type=extension].version` field selector, and refuses unsupported field-selector forms (stokaro/ptah#814). |
| `qualifier-txmode-workflow` | Do the `migrate diff` qualifier and txmode-metadata contracts hold? | An invalid `--qualifier` is refused before the dev database file exists and before any artifact is written; a valid qualifier on SQLite is refused pre-artifact with the documented dialect-scope diagnostic (qualified artifact content needs a live PostgreSQL/MySQL dev database and belongs to the database-backed tiers); the atlas.hcl concurrent-index diff policy plans the documented single plain transactional file on SQLite and that artifact replays through `migrate apply`; a `-- atlas:txmode none` migration executes outside a transaction (the statement before a failure persists) while an identical transactional control rolls back (stokaro/ptah#815). |

Each probe recovers from panics; none panicked on this corpus.

## What is NOT tested at all

This is the important half of "what doesn't work": most of Atlas's open-source
(Apache-2.0) surface is not exercised here, so for those areas the honest answer
is **"unknown — not measured"**, not "works".

| Atlas open-source capability | Tested here? | Where it would be measured |
| --- | --- | --- |
| Schema **introspection** breadth (types, defaults, generated/partial/expression indexes, sequences, domains, composite types, FK actions, exclusion constraints, partitioning, collations, comments) | **Partially** — live/diff fixtures now cover basic tables, enums, views, indexes/FKs, composite keys, constraints/actions, generated columns, self-references, and richer defaults/types | broader dedicated introspection probes against live DBs |
| Schema **diff / plan** (desired A → desired B → migration) | **Partially** — the PostgreSQL `schema-planning` matrix applies paired A/B schemas and compares converged end states across add/drop/modify and precision changes | broader paired-schema coverage across every supported dialect |
| **End-state equivalence** (apply a schema, then compare what Atlas and Ptah each report about the result) | **Partially** — the `conformance-diff` differential tier compares Atlas CE's `schema inspect` against Ptah's introspect → render on live Postgres, MySQL, and SQLite targets, scoped to CE-visible object kinds | `conformance-diff` workflow; deeper apply-with-each-tool equivalence is still ptah#285 |
| **HCL** schema language | **Partially** — Atlas CE inspect HCL is parsed in the differential tier and the imported offline corpus is green, but this is not broad Atlas HCL language parity | broader HCL schema fixtures and runtime command probes |
| Versioned-migrate **runtime semantics** (tx-mode, execution order, baseline, advisory lock, revision-table shape) | **Partially** — the migrate-runtime tier checks real SQLite/PostgreSQL/MySQL apply/status/set state, `all`/`file`/`none` transaction behavior, apply dry-run against stored revision state on all three dialects, SQLite down dry-run through both formatted and default-output paths, fresh-target dry-run without metadata mutation, and missing-down rollback atomicity; it also covers externally produced `-- atlas:checkpoint` directories (fresh-database bootstrap from the checkpoint alone, silent skip on a pre-checkpoint database, and Atlas's own vendored `partial-checkpoint` fixture for latest-checkpoint selection plus post-checkpoint continuation), byte-identical Goose checksum generation and cross-validation against Atlas CE with an explicitly measured Atlas first-error advisory and exact stable tamper rejection, and apply-time `atlas.sum` integrity on both Atlas branches (a hashed directory edited after hashing, and a directory that was never hashed); its project-config apply oracle clones an Atlas CE-created brownfield database, independently applies the remainder with Atlas CE and Ptah, then compares status, end schema, stable full revision metadata, producer-specific dynamic-field validity, and Atlas readback | broader baseline, lock-contention, failure-recovery, and multi-process contours |
| Atlas CLI **help and flag surface** | **Measured separately** — `cli-surface.md` compares the pinned Atlas CE binary's command tree, usage strings, and long flags against the `ptah-compat` binary named `atlas`, Ptah's single Atlas-shaped surface since stokaro/ptah#850; the current committed report is green on the pinned Atlas CE surface | `cli-surface.md` / `cli-surface.json`, `make gate-cli-surface` |
| The full Atlas **documentation surface** (every atlasgo.io docs page) | **Inventoried, triage in progress** — `docs-surface.md` indexes every atlasgo.io docs page from the committed sitemap snapshot and requires each to carry an explicit Ptah stance (`open`/`partial`/`gap`/`pro`/`cloud`/`out-of-scope`); the seed registry triages 29 Cloud pages and leaves 322 untriaged, and a weekly sitemap re-fetch fails CI on docs drift. This is a triage-coverage inventory, not behavioral evidence: a page marked `open` is a stance, not a probe | `docs-surface.md` / `docs-surface.json`, `docs-surface-registry.json`, `make gate-docs-surface` |
| **sqlcheck analyzers**, rule by rule | **Yes** — the `lint-analyzer-catalog` fidelity matrix maps every default-firing Atlas concern to the covering Ptah rule, severity, and line, classified covered/mapped/unsupported/missing, and enforces suppression, config disable/severity-override, attribution, and SARIF-shape fidelity | `lint-analyzer-catalog` rows in `gaps.md` + `fidelity: sarif output shape` in `gaps-migrate-runtime.md` |
| **Multi-dialect** depth (MySQL, SQLite, MariaDB) | **Partially measured** — live round trips run on Postgres, MySQL, MariaDB, and SQLite in CI; Atlas CE differential runs on Postgres, MySQL, and SQLite | deeper dialect runtime probes |
| DDL parse/round-trip **breadth** | **Measured over all vendored `.sql` files** | still parser-only, not apply/runtime equivalence |
| The migration **apply** path | **Partially** — migrate-runtime checks Atlas-compatible apply behavior, including an Atlas CE control versus Ptah project-config apply differential from identical brownfield state, checkpoint-directive bootstrap-or-skip semantics on both first-party and Atlas-authored directories, and apply-time `atlas.sum` verification on both hashed and never-hashed directories; `dbtest-workflow` checks native migration up/down on SQLite | broader Atlas fixture execution and failure-state equivalence; parser gaps remain distinct because apply executes migration SQL directly |

The SQLite transaction-mode runtime matrix now covers all 12 Atlas CE v1.3
global/file-directive combinations, malformed and misplaced directives,
exact LF, CRLF, space, tab, mixed-whitespace, and missing-separator header
boundaries, amount and baseline selection, complete stable revision metadata,
converted golang-migrate pairs, txtar section controls, and the native
`+ptah no_transaction` control. It leaves nine observations red: the eight
exact diagnostic differences tracked by `stokaro/ptah#1076` and the failed-file
revision row tracked by `stokaro/ptah#887`. The `\r`-sensitive and
missing-separator differences tracked by `stokaro/ptah#1077` and
`stokaro/ptah#1081` are explicit green `ptah-better` observations:
Atlas CE drops the user's `none` directive, while Ptah intentionally honors it.
The converted golang-migrate `.up.sql` control applies the same classification
when Atlas CE discards an explicit source directive but Ptah preserves it.
Atlas CE `migrate down` is a measured community-version boundary that rejects
execution before runtime flags can be supplied, so split-file and txtar down
behavior is reported as Ptah-side evidence rather than mislabeled Atlas parity.

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
  first-party behavioral probes rather than falsely compared with CE.
  Declarative database testing (ptah#659) is measured here by
  `dbtest-workflow`, which runs committed fixtures through both native commands
  on ephemeral SQLite and verifies reports and process exits; the Atlas-shaped
  forwards `atlas migrate test` and `atlas schema test` (ptah#805) are measured
  by `pro-test-workflow` through the same real CLI. The local `schema plan`
  plan-file workflow and `schema apply --plan file://` (ptah#809) are measured
  by `pro-plan-workflow`, including `schema plan new`, `schema plan validate`,
  and stale-fingerprint refusals; the seven Cloud-only plan registry sub-verbs
  stay CE-boundary stubs. The bare
  `atlas migrate down` non-interactive execution and Atlas revision-format
  default (ptah#810 and #971) are measured by `pro-down-workflow`. Composite desired
  schemas (ptah#666) are measured by `composite-schema-workflow`, which proves
  source composition against a hand-merged oracle and a live SQLite end state.
  Declarative reference/seed data management (ptah#663) is measured by
  `managed-data-workflow`, which proves the full round-trip on ephemeral SQLite:
  declared rows are applied through `ptah migrations data`, introspected back,
  and re-diffed to a converged "no data changes" state, with a destructive-gate
  refusal and a reversible rollback. Atlas CE offers no declarative
  reference-data management or inspection, so there is no CE oracle.
  Migration checkpoints (ptah#660) are measured by `checkpoint-workflow`, which
  proves the squash → fresh-bootstrap → equivalence round-trip on ephemeral
  SQLite databases, plus the integrity, rollback-boundary, and post-checkpoint
  continuation contracts; Atlas keeps the checkpoint-*generating* `migrate
  checkpoint` command in its proprietary Pro build, so there is no CE oracle for
  producing a checkpoint. Reading one is a different matter: the pinned Atlas CE does
  honor the `-- atlas:checkpoint` directive, applying only the latest checkpoint
  on a fresh database and skipping it on a database that already ran the
  pre-checkpoint history, so the read half is measured against CE directly by
  the migrate-runtime tier.
  Pre-migration
  assertion checks (`-- +ptah check`, ptah#661) remain covered in Ptah's own
  behavioral tests: Atlas CE offers neither the check execution nor the Cloud
  approval half, so there is no CE oracle. This is scope, not a gap.
- **Migration directory maintenance** (`ptah migrations edit`, `rebase`, and `rm`,
  ptah#662) is another: Atlas keeps directory-maintenance commands in its
  proprietary (Pro) build, so Atlas CE has no `migrate edit`/`rebase`/`rm` to
  differential against. Ptah provides the capability through
  its **native** command tree (mutate a migration and atomically rewrite
  `ptah.sum`/`atlas.sum`, refusing already-applied history), verified by unit tests
  in `migration` and `cmd`, and since ptah#807 also forwards the Atlas-shaped
  verbs `atlas migrate edit`/`rebase`/`rm` as open capabilities — measured here
  end to end by `pro-maint-workflow`, with the CLI-surface report requiring the
  verbs to keep resolving instead of regressing to the CE abort stub. There is
  still no CE oracle; the usage/flag contract is first-party.
- Native migration **import** (`ptah migrations import`, ptah#667) is Atlas OSS
  `migrate import` parity, but it emits **Ptah-native** format (not Atlas format),
  so it is not a schema-object round-trip. It is measured directly by the
  `golang-migrate/import-roundtrip`, `goose/import-roundtrip`,
  `flyway/import-roundtrip`, and `liquibase/import-roundtrip` migrate-runtime
  probes: import a source directory, then assert the output passes
  `ptah migrations validate`. The Flyway probe also exercises dotted versions, an
  undo (`U__`) file mapped to a down, and a repeatable (`R__`) migration imported
  as a one-time migration; the Liquibase probe exercises formatted-SQL changesets
  (`--changeset` / `--rollback`). All four documented source tools —
  golang-migrate, Goose, Flyway, and Liquibase (formatted SQL) — are covered,
  closing ptah#667; Liquibase XML/YAML/JSON changelogs remain a follow-up.

## What focused probes measure

The following probes measure bounded dimensions. CLI help/flag inventory and the
default analyzer catalog are exhaustive within their pinned scopes; lexer and
workflow probes are fixture-driven and make only the claims listed below.

- **`atlas-compat-binary-surface`** in the offline fixture report checks,
  against the real ptah-compat binary (built as `atlas`), whether important OSS
  Atlas command paths resolve. Since stokaro/ptah#850 removed the
  `ptah atlas ...` namespace, this is Ptah's only Atlas-shaped surface; the
  offline `cli-exit-behavior` probe pins that the main `ptah` binary keeps
  rejecting the `atlas` namespace with exit 2.
- **`cli-surface.md`** is the stricter dedicated CLI-surface yardstick. It builds
  or reads the pinned Atlas CE binary, discovers the current `atlas schema ...`
  and `atlas migrate ...` command tree from Cobra help, records usage lines and
  long flags, and compares them against the `ptah-compat` binary named `atlas`. This report also lists Cloud/commercial
  commands explicitly with **per-verb expectations**: verbs Ptah has implemented
  as open capabilities (`migrate test`, `schema test`, `migrate
  edit`/`rebase`/`rm`, `migrate down`, `schema plan`, `schema plan new`,
  `schema plan validate`, `migrate checkpoint`) must resolve with a
  first-party usage line and required long-flag set — regressing to Atlas CE's
  community-version abort stub is a gap — while still-stubbed Cloud/registry
  verbs (`migrate push`, `schema push`, and the seven `schema plan` registry
  sub-verbs) must preserve two byte-exact Ptah-owned boundaries: bare execution
  exits 1 with empty stdout and a command-specific diagnostic on stderr, while
  `--help` exits 0 with command-specific text on stdout and empty stderr. This
  remains strict without copying Atlas CE prose; success, exit-code, stream, or
  whitespace drift, generic or wrong-command output, and the old copied CE
  message are all gaps. Resolution proves only the CLI surface; dedicated workflow probes
  own behavioral evidence for extra Ptah capabilities. The current full gate is green on the
  pinned Atlas CE surface; future Atlas changes should either keep this green
  by implementing Ptah parity, or create explicit tracked gaps instead of
  dropping commands from the inventory.
- **`ce-gating.md`** is the **executed** counterpart to the CLI-surface
  inventory: help text says what the pinned Atlas CE binary registers, this
  tier measures what it actually does. Every scenario runs the real binary
  logged out (scratch `HOME`/XDG dirs per scenario) through the capability set
  Ptah's feature matrix asserts about the CE column, and classifies the
  observed outcome: works / community-abort (registered stub refusing with the
  community-version sentence) / absent (never-registered verb falling through
  to the parent group help, exit 0) / unknown-flag / named-error /
  silent-unenforced. The last class is the dangerous one and is pinned by
  measurement: an HCL `role` block applies as a silent no-op ("Schema is
  synced"), and a failing txtar `checks.sql` assertion executes as an ordinary
  statement while the guarded migration applies anyway. Expected classes are a
  hand-measured baseline with a zero budget, so a renovate bump of
  `atlas.version` that changes gating turns `gate-ce-gating` red instead of
  silently invalidating the matrix. This is a claim about the Atlas CE binary,
  not about Ptah — it changes only when the pin changes.
- **`lint-analyzer-catalog`** is an **analyzer fidelity matrix** over the full set
  of Atlas analyzer concerns that fire by default in an OSS build — the DS, MF
  (data-dependent), BC, CD, PG1, PG3, PG110, MY, LT and TX families. This is the
  "lint matrix" listed below as a requirement. It goes beyond "some warning fired":
  each concern row records **which Ptah rule covers it, at what severity, and on
  which line**, classified as a `covered (exact)` code match, a `covered (mapped)`
  code with a documented reason (Ptah's `PG102` is an enum-in-transaction rule, not
  Atlas's drop-index rule — Ptah reports drop-index under `PG106`; PostgreSQL
  constraint drops use the untyped ANSI `DROP CONSTRAINT`, so they read `DS105`
  while the typed `CD1xx` codes fire on the MySQL forms), or — for a concern a
  SQL-only linter cannot reach (a data-dependent check needing a dev database) or
  that Ptah does not yet cover — an intentionally `unsupported` row or a `missing`
  gap linked to a Ptah issue. Today every default-firing concern is covered, so an
  uncovered concern added later fails closed to a red `missing` gap. Because the
  covering code, severity, and line are all committed to the report, any drift —
  a rule renumbered, a severity lowered, a covered concern regressing to silence —
  turns the gate red. Alongside the per-concern rows, enforced cross-cutting
  fidelity checks assert the analyzer behaviors CI automation depends on: inline
  `-- ptah:nolint` **suppression**, configuration-driven **disable** and
  **severity override**, **line attribution**, and the **SARIF 2.1.0 output shape**
  (`ruleId`, `level`, and a file:line location) emitted by `migrations lint
  --format sarif` (this last one lives in the `migrate-runtime` tier because it
  runs the real CLI). Removing any of those behaviors flips its check red, which is
  the drop-in-safety guarantee automation consumers need. Deliberately excluded:
  NM (naming) fires only under a configured policy, and SA (injection) / OW
  (ownership) are policy/enterprise analyzers — none run in a default OSS pass, so
  their absence is not a default drop-in gap.
- **`lex-split-parity`** is a differential check against Atlas's own recorded
  output: for every Atlas lexer fixture that ships a `.golden`, it asks whether
  Ptah's statement splitter breaks the SQL into the same statements Atlas does.
  This is real drop-in behavior — if Ptah splits a stored function body, a
  `BEGIN ATOMIC` block or a MySQL `DELIMITER` section differently, the migration
  executes differently. It uses Atlas's committed goldens, so it needs no live
  Atlas binary (which does not build cleanly on current Go and whose release is
  proprietary). SQL Server delimiting (GO / BEGIN TRY) is out of scope — SQL
  Server is a Pro Atlas driver.
- **`dbtest-workflow`** is an end-to-end capability probe over Ptah's open
  replacement for Atlas's proprietary declarative test workflows. It builds the
  go.mod-pinned `ptah` binary and executes committed migration, schema, seed,
  assertion-failure, setup-failure, and isolation fixtures. Both native command
  paths must filter deliberately failing cases, run on ephemeral SQLite, render
  text/JSON/HTML reports without external HTML assets, repair desired-schema
  drift, reject migration-only steps in schema tests, and preserve stdout,
  stderr, assertion-failure exit 1, and malformed-input exit 2 on both commands.
  This is representative workflow coverage, not an exhaustive CLI matrix:
  explicit shared `--db-url`, every directory format and invalid flag, step-local
  seed directories, connection/interruption failures, and report-write failures
  remain owned by Ptah's package and command tests. These fixtures live under
  `testdata/workflows/dbtest`, outside the Atlas round-trip corpus, because they
  measure a Ptah-native capability rather than an Atlas CE schema object.
- **`managed-data-workflow`** is an end-to-end capability probe over Ptah's
  declarative reference/seed data feature, which Atlas CE does not offer. It
  builds the go.mod-pinned `ptah` binary and drives the whole round-trip
  ptah#663 asks for on an ephemeral SQLite database: a committed model declares
  managed rows via `//ptah:schema:data` pointing at a YAML row-data file;
  `ptah migrations generate` and `up` create the empty table; `ptah migrations
  data` generates a reversible data migration (the up body inserts every
  declared row, the down body deletes exactly those keys); `up` applies it; the
  seeded rows are introspected directly from SQLite and matched to the declared
  desired state; a re-run of `ptah migrations data` against the seeded database
  converges to "no data changes"; a divergent desired set that would delete a
  row is refused by the generation-time destructive gate with exit code 2 and
  writes no files; and rolling the data migration back with `ptah migrations
  down` removes exactly the inserted rows while leaving the schema intact. Row
  application and diffing run entirely through the real CLI — the probe only
  reads the resulting SQLite rows to verify them. These fixtures live under
  `testdata/workflows/managed-data`, outside the Atlas round-trip corpus,
  because they measure a Ptah-native capability with no Atlas CE oracle.
- **`checkpoint-workflow`** is an end-to-end capability probe over Ptah's
  migration checkpoint feature (ptah#660), the open counterpart of Atlas's
  Pro-only `migrate checkpoint`. It builds the go.mod-pinned `ptah` binary and
  drives the full round-trip on ephemeral SQLite databases: a committed
  three-migration history is applied in full to one database; `ptah migrations
  checkpoint` replays the directory into an ephemeral shadow database and
  writes the deterministic version-4 checkpoint pair whose up body is the
  cumulative schema (including the column added by the middle migration),
  rewriting `ptah.sum` so `ptah migrations validate` passes; a fresh database
  bootstraps from the checkpoint alone, recording only the checkpoint revision
  with the squashed history satisfied, and its live schema — compared
  structurally via SQLite pragma facts (columns, defaults, primary keys,
  foreign keys, indexes), because the hand-written and rendered DDL legitimately
  differ in spelling — is identical to the full replay; `ptah migrations
  status` shows the checkpoint as not pending on the already-migrated database
  and complete on the bootstrapped one, and re-running `up` on the
  already-migrated database applies nothing; a single tampered byte in the
  checkpoint file fails `validate` with exit 1 naming the file; `ptah
  migrations down` below the checkpoint boundary is refused with exit 2 and an
  actionable error while rolling back to zero runs the checkpoint's down body;
  and after a post-checkpoint migration is added, a fresh database applies
  exactly the checkpoint plus that migration, converging with the full-history
  path to structurally identical schemas. All migration execution runs through
  the real CLI — the probe reads SQLite directly only to verify revision rows
  and schema facts. These fixtures live under `testdata/workflows/checkpoint`,
  outside the Atlas round-trip corpus, because they measure a Ptah-native
  capability with no Atlas CE oracle.
- **`external-schema-workflow`** is a deterministic end-to-end probe over
  Ptah's native external-program desired-schema source (ptah#669). It invokes
  the real CLI for SQL, HCL, and YAML provider output, proves the configuration
  trust gate prevents execution and filesystem side effects across every
  consumer, generates and applies a migration to ephemeral SQLite, verifies
  live schema facts directly, and requires compare, drift, plan, and generate
  to converge. A separate `orm-provider-smoke` report installs pinned GORM and
  SQLAlchemy providers and checks both their direct output and Ptah-rendered
  output. Neither probe claims Atlas HCL `data.external_schema` evaluation;
  that is a separate Atlas project-language capability.
- **`pro-test-workflow`**, **`pro-maint-workflow`**, **`pro-plan-workflow`**,
  and **`pro-down-workflow`** are end-to-end capability probes over the Atlas
  Pro verbs Ptah forwards as open capabilities (ptah#758: the `migrate
  test`/`schema test` forwards from ptah#805, the `migrate
  edit`/`rebase`/`rm` forwards from ptah#807, the local `schema plan` /
  `schema apply --plan file://` workflow from ptah#809, and the bare
  `migrate down` Atlas revision-format default from ptah#810). Each builds the
  go.mod-pinned ptah-compat binary and drives the real drop-in `atlas ...`
  CLI over committed fixtures under `testdata/workflows/pro-*`: the test probe runs
  passing and deliberately failing case sets against real SQLite dev
  databases and enforces the exit-0/exit-1 report contract; the maintenance
  probe edits (via a hermetic scripted `$EDITOR`), rebases, and removes
  migrations fully offline, requiring `ptah migrations validate` to pass
  after every verb; the plan probe creates local plans through both the parent
  command and `schema plan new`, validates fresh and stale plans without target
  mutation, replays a reviewed plan with `schema apply --plan`, and proves a post-plan target
  mutation is refused as stale with the database untouched; the down probe
  proves both formatted and default-output `atlas migrate down` read no stdin,
  accept no Ptah-only `--confirm`, read the Atlas-format revision rows
  `atlas migrate apply` wrote, and actually revert. These are representative
  workflow contours, not exhaustive CLI matrices — flag-by-flag coverage
  stays owned by Ptah's own command tests, and the CLI-surface tier owns the
  usage/flag contracts. Atlas keeps every one of these verbs outside CE, so
  there is no CE oracle.
- **`desired-state-workflow`**, **`apply-simulation-workflow`**,
  **`schema-scope-workflow`**, **`inspect-source-workflow`**, and
  **`qualifier-txmode-workflow`** are end-to-end capability probes over the
  Atlas-compatible `schema`/`migrate` surface batch (ptah#811 desired-state
  source URLs, ptah#812 apply locking and dev-database plan simulation,
  ptah#813 `--schema`/`--include` scoping, ptah#814 inspect sources and
  split/write exports with exclude field selectors, ptah#815 `migrate diff
  --qualifier` and concurrent-index txmode metadata). Each builds the
  go.mod-pinned ptah-compat binary and drives the real drop-in `atlas ...`
  CLI over committed fixtures under `testdata/workflows/`, asserting database and
  filesystem end states directly (tables read straight from SQLite, exported
  trees walked on disk) rather than trusting command output. Failure-order
  contracts are pinned as sharply as the happy paths: refusals must happen
  before the target or dev database file exists where the CLI documents
  pre-target validation. Qualified `migrate diff --qualifier` artifact
  content requires a live PostgreSQL/MySQL dev database, so the offline tier
  pins the validation-order and dialect-scope contracts and leaves artifact
  content to the database-backed tiers. The `-s` shorthand parity for the
  #813 scoping is asserted by the `atlas-cli-shorthands` probe, which now
  requires the shorthand to scope identically to the long flag instead of
  merely parsing.
- **`cli-exit-behavior`** is an **exit and error-behavior matrix** over
  representative Atlas OSS success and failure paths (invalid URL, missing
  migration directory, missing/malformed/duplicate `atlas.sum`, clean checksum
  success, valid edited/added/removed migration drift, directory-hash-only
  drift, unknown flag, unknown subcommand, accepted-but-unimplemented flag,
  missing project config, plus `--help`), run against the `ptah-compat`
  drop-in `atlas` binary. It also pins the stokaro/ptah#850 namespace removal:
  the main `ptah` binary must keep rejecting `ptah atlas` with exit 2 and the
  exact unknown-command diagnostic. It enforces the drop-in
  exit **contract** — success exits 0, a failure exits 1, and each case uses
  Atlas CE's verified stdout/stderr pattern. Stable checksum guidance and the
  unknown-command diagnostic are byte-checked. A wrong exit code, moved output,
  changed error class, extra success banner, or incomplete recovery message
  turns the full conformance gate red. The static catalog is also executed
  directly against the binary pinned by `atlas.version` in regression and full
  conformance CI, with Atlas's network-dependent update notifier disabled,
  preventing Ptah-specific strings from becoming a false-green oracle. The
  matrix drove fixes for unknown-command exit behavior (ptah#687),
  exact failure codes (ptah#688), checksum mismatch output (ptah#714), missing
  checksum output (ptah#723), unknown-command diagnostics (ptah#725), and silent
  checksum success (ptah#727).
- **`schema-planning`** is a **paired-schema planning matrix** (PostgreSQL, in the
  `migrate-runtime` tier). Correct introspection does not guarantee a correct
  *plan*: for each paired desired schema (A, B) it applies A then B to one reset
  database — exercising the A→B plan produced by ptah-compat `atlas schema apply` — and B
  alone to another, then asserts the two introspect to the **same canonical
  schema**. This proves the generated plan reaches the intended end state, and a
  plan that drops an add/drop/modify operation turns the row red. Covered and
  green: add/drop table, add/drop column, nullability changes, cross-category
  type changes (`INTEGER`→`TEXT`), column precision changes — integer width
  (`INTEGER`→`BIGINT`), `VARCHAR` length (including growing to an unbounded
  `VARCHAR`), and decimal scale (`NUMERIC(10,2)`→`NUMERIC(10)`) — plus a mixed
  case. The precision cases were originally omitted because Ptah's diff
  normalizer collapsed those distinctions, so `schema apply` did not plan them
  and reported "synced" (the divergence this matrix surfaced); ptah#691 and
  ptah#693 fixed that, and they now hold as green rows.

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
about which objects Atlas itself inspects. PostgreSQL-only fixtures also prove
clean round-trips for schema-qualified objects, standalone sequences, domains,
composite types, and range types. Each successful report row enumerates every
non-empty object family checked by the round-trip diff, so support beyond Atlas
CE is visible rather than hidden behind a table count. The deeper differential
correctness of each declarative command (does `schema inspect` emit equivalent
HCL) remains the domain of the end-state conformance in ptah#285.

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
so it measures Ptah against a known Atlas release. Differential, CE-gating, and
CLI-surface generators reject a binary whose Community version differs from
the pin. The differential is deliberately scoped to CE-visible object kinds:
Atlas CE silently omits Pro-gated objects (views,
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
   inventory. *In progress:* the sqlite `cli-migrate-lint-*` fixtures are now
   executed end to end by the real `atlas migrate lint` CLI (ptah-compat) against an
   ephemeral SQLite dev database — default analysis text report,
   destructive/data-dependent diagnostics, `-- atlas:nolint` suppression, the
   exit-1 failure threshold, and atlas.hcl `--env`/`lint.log` project-config
   resolution. The harness evaluates the assertions in those upstream scripts
   directly against the actual CLI streams and files. It does not translate
   Atlas prose into Ptah-specific semantic codes or maintain a second
   expected-output model (ptah#651, ptah#747).
2. An **introspection** probe: apply a schema with each tool, introspect with
   one reader, diff the canonical states (this is ptah#285 and needs a live DB).
   *Partially built:* the `conformance-diff` tier already compares Atlas CE's and
   Ptah's introspection of the same live schema, scoped to CE-visible objects.
3. A **diff/plan** probe over paired before/after schemas.
4. A **lint matrix** comparing Atlas analyzer codes against Ptah rule codes,
   fixture by fixture. *Built:* the `lint-analyzer-catalog` fidelity matrix records
   the covering Ptah rule, severity, and line per concern and enforces
   suppression, config, attribution, and SARIF-shape fidelity (ptah#649).
5. Broader **multi-dialect** runtime coverage: Postgres, MySQL, MariaDB, and
   SQLite now run in the live tier; Postgres, MySQL, and SQLite now run in the
   Atlas CE differential tier, but deeper live/runtime probes are still needed.
6. A declared, justified scope for what is deliberately **out** of parity (HCL
   schema, Cloud, Pro drivers), so "parity" has an explicit boundary.

Until those exist, this repository answers a broader but still bounded question
honestly — *where, across Atlas's vendored testdata snapshot, does Ptah visibly
fail to ingest what Atlas produced, and which imported fixtures are not measured
yet* — and nothing wider.
