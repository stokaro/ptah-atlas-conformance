# ptah-atlas-conformance

A conformance harness that drives [Atlas](https://github.com/ariga/atlas)'s own
fixtures through [Ptah](https://github.com/stokaro/ptah)'s public API, compares
selected runtime behavior with Atlas CE, and executes first-party workflows for
open Ptah capabilities that Atlas keeps outside CE.

It answers two questions Ptah's own test suite cannot answer alone: **what does
Atlas express that Ptah does not yet cover, and do Ptah's beyond-CE workflows
still work through the shipped CLI?** Pointing Atlas's corpus at Ptah surfaces
blind spots; separate capability fixtures prevent open workflow features from
being inferred from schema parsing alone.

The generated report is [`gaps.md`](./gaps.md). It is a coverage map, not a
quality score: a `gap` is either an Atlas construct Ptah does not model yet or a
first-party workflow contract Ptah failed to preserve. Each row links the Ptah
issue that tracks closing it.

**This is not a full feature-set parity test.** The repository now vendors every
file under Atlas's open-source `*/testdata/*` tree at the pinned commit (286
files, grouped into report fixtures), plus first-party Atlas-compatible
regression fixtures for gaps not present in the upstream snapshot. The offline
report distinguishes fixtures that are actually measured from fixtures that are
merely imported and still lack a probe; the live reports separately show whether
Ptah survives real database round trips, agrees with Atlas CE inspection, and
preserves Atlas migrate runtime state.
[`PARITY.md`](./PARITY.md) states exactly what is and is not tested — read it
before quoting any number from here.

## Why this is a separate repository

This lives outside `stokaro/ptah` on purpose, and the boundary is one-way:

- **The dependency direction is `conformance → ptah`, never the reverse.** This
  repo imports Ptah; Ptah never imports this repo, and this repo never appears in
  Ptah's `go.mod`, CI, or releases.
- **Ptah stays MIT-clean.** The Atlas fixtures are Apache-2.0. Keeping them here,
  confined to `third_party/atlas/`, means Ptah's tree contains zero Apache-licensed
  files and carries no third-party attribution obligation.

This repository is Apache-2.0 (see [`LICENSE`](./LICENSE)), matching the upstream
material it vendors. The licensing rationale and the exact provenance of every
vendored file are in [`third_party/atlas/PROVENANCE.md`](./third_party/atlas/PROVENANCE.md).

## What it checks

| Probe | Question | Ptah API exercised |
| --- | --- | --- |
| `corpus-inventory` | Is every vendored Atlas test artifact visible in the generated report, including still-unmeasured `.hcl`/other fixtures? | harness |
| `sql-parse` | Can Ptah's DDL parser represent Atlas's SQL in its AST? (round-trip / `read-db` / `compare` — **not** apply, which execs raw SQL) | `core/parser` |
| `migdir-ingest` | Does Ptah's migrator recognize the files in an Atlas migration directory? | `migration/migrator` |
| `txtar-script` | Does the harness parse Atlas integration txtar scripts, execute the command subset currently mapped to Ptah APIs, publish the fixture-level script surface, and keep unsupported runtime commands red? The sqlite `cli-migrate-lint-*` fixtures are executed end to end by the real `atlas migrate lint` CLI (the ptah-compat binary) against an ephemeral SQLite dev database, so their green proves Ptah's own Atlas lint report, not a harness reimplementation. | harness, `core/parser`, `core/renderer`, the built `ptah-compat` binary (`migrate lint`) |
| `txtar-down` | Does Ptah load Atlas txtar migrations with an embedded `down.sql` section? | `migration/migrator` |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does Ptah's own hash reproduce it? | `migration/migratesum` |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content, or only its file names? | `migration/lint` |
| `atlas-compat-binary-surface` | Does `atlas <verb>` resolve on the ptah-compat binary for every OSS Atlas CLI verb? Since stokaro/ptah#850 removed the `ptah atlas ...` namespace, the ptah-compat binary named `atlas` is Ptah's only Atlas-shaped surface; the probe builds it and checks each command, so it flips to green on its own when Ptah registers the command. | the built `ptah-compat` binary |
| `atlas-cli-flags` | Beyond resolving, does each `atlas <verb>` on the ptah-compat binary accept the Atlas flags a drop-in caller passes (`--url`, `--dev-url`, `--to`, `--dir`, `--format`, …)? A resolving stub is not a drop-in. | the built `ptah-compat` binary |
| `cli-exit-behavior` | Does `ptah-compat` match Atlas CE's exact process exit code and stdout/stderr contract for representative success, argument, configuration, and migration-checksum paths? Stable checksum and unknown-command output is byte-checked. The catalog is also run directly against the pinned Atlas binary so Ptah-specific expectations cannot make the probe false-green. The probe additionally pins the stokaro/ptah#850 removal: the main `ptah` binary must keep rejecting the `atlas` namespace with exit 2 and the unknown-command diagnostic. | `bin/atlas`, the built `ptah` binary, the built `ptah-compat` binary |
| `atlas-cli-surface-inventory` / `atlas-cli-surface-ptah-compat` | Dedicated CLI surface report over the current pinned Atlas CE binary: command paths, help usage, and long flags, compared against the binary-level `ptah-compat` drop-in surface. | `bin/atlas`, the built `ptah-compat` binary |
| `migrate-runtime` | Do Atlas-form `migrate ...` commands on the ptah-compat binary preserve Atlas-compatible runtime state against real databases: applied schema objects, Atlas revision rows, `set` repair behavior, transaction-mode rollback/partial-apply semantics, and project-config apply equivalence against pinned Atlas CE? | the built `ptah-compat` and `ptah` binaries, pinned Atlas CE, live SQLite databases |
| `lint-analyzer-catalog` | For each Atlas sqlcheck analyzer concern, does Ptah's linter flag the same dangerous change? Behavioral, one synthetic migration per analyzer, so it flips green when Ptah gains the rule. | `migration/lint` |
| `lex-split-parity` | Does Ptah split a migration into the same statements Atlas does? A differential check against Atlas's own `.golden` lexer outputs (no live Atlas needed), normalized so it measures statement boundaries, not comment preservation. Surfaces real drop-in blockers: function bodies, `BEGIN ATOMIC`, MySQL `DELIMITER`. | `core/sqlutil` dialect-aware statement splitting |
| `dbtest-workflow` | Do the native `ptah migrations test` and `ptah schema test` commands execute committed migration/schema/seed fixtures against isolated SQLite databases, filter cases, repair schema drift, render text/JSON/HTML reports, and preserve exit codes 1 (assertion failure) and 2 (setup failure)? | the built `ptah` binary, `migration/dbtest`, ephemeral SQLite |
| `composite-schema-workflow` | Do independently owned Go and YAML desired-schema sources render exactly like a committed hand-merged schema, reject conflicting identities, generate identical up/down migrations, and converge on verified SQLite schema facts? | the built `ptah` binary, `core/goschema`, ephemeral SQLite |
| `managed-data-workflow` | Does Ptah's declarative reference/seed data (`//ptah:schema:data` plus `ptah migrations data`) round-trip: apply declared rows, introspect them back, re-diff to a converged "no data changes" state, refuse a destructive divergent set (exit 2), and reverse cleanly on rollback? Atlas CE cannot declaratively manage or inspect reference data. | the built `ptah` binary, `internal/datamigrate`/`migration/datadiff`, ephemeral SQLite |
| `checkpoint-workflow` | Does `ptah migrations checkpoint` squash a migration history into a deterministic cumulative-schema pair covered by `ptah.sum`, from which a fresh database bootstraps to a schema structurally identical to the full replay while an already-migrated database ignores it — with tamper detection (`validate` exit 1), a below-boundary rollback refusal (exit 2), a working rollback-to-zero, and post-checkpoint continuation? Atlas keeps `migrate checkpoint` in its proprietary Pro build. | the built `ptah` binary, `migration/generator`/`migration/migrator`, ephemeral SQLite |
| `external-schema-workflow` | Do static SQL and external SQL/HCL/YAML desired schemas preserve the trust boundary and drive render, compare, drift, plan, generate, apply, live SQLite facts, and converged no-op results through the real Ptah CLI? | the built `ptah` binary, external fixture provider, ephemeral SQLite |
| `pro-test-workflow` | Do the Atlas Pro test verbs Ptah implements as open capabilities — `atlas migrate test` and `atlas schema test` — run committed case sets against a real SQLite dev database, passing sets with exit 0 and deliberately failing assertions with a structured FAIL report and exit 1? Atlas keeps both verbs in its proprietary Pro/Cloud build. | the built `ptah` binary (`atlas migrate test` / `atlas schema test`), ephemeral SQLite |
| `pro-maint-workflow` | Do the Atlas Pro directory-maintenance verbs Ptah implements as open capabilities — `atlas migrate edit` (via a hermetic scripted `$EDITOR`), `atlas migrate rebase`, and `atlas migrate rm` — mutate an Atlas-format directory offline while rewriting `atlas.sum` so `ptah migrations validate` stays green after every verb? | the built `ptah` binary (`atlas migrate edit`/`rebase`/`rm`), no database |
| `pro-plan-workflow` | Does the local half of Atlas's Pro `schema plan` workflow hold: `atlas schema plan --save` writes a fingerprinted format_version-1 plan file against a real SQLite target, `atlas schema apply --plan file://...` replays exactly that reviewed plan, and a target mutated after planning is refused as stale without touching the database? Atlas binds plan storage/approval to its Cloud registry. | the built `ptah` binary (`atlas schema plan` / `schema apply --plan`), ephemeral SQLite |
| `pro-down-workflow` | Does bare `atlas migrate down` — no `--revision-format` flag — read the Atlas-format revision rows `atlas migrate apply` wrote and actually revert (the stokaro/ptah#810 default; previously a silent no-op against Ptah's native revision table)? | the built `ptah` binary (`atlas migrate apply`/`down`), ephemeral SQLite |
| `desired-state-workflow` | Do the Atlas desired-state source URLs work for `schema diff`/`schema apply` (stokaro/ptah#811): a live database URL as the `--from` diff source and the `--to` apply source, a migration directory replayed on a dev database (refused before the target is contacted without `--dev-url`), and an `env://src` reference resolved through an evaluated `atlas.hcl` environment? | the built `ptah` binary (`atlas schema diff`/`apply`), ephemeral SQLite |
| `apply-simulation-workflow` | Do the `schema apply` guard rails hold (stokaro/ptah#812): `--lock-timeout` accepted as an explicit noted no-op on lockless SQLite, `--dev-url` resetting the dev database and rehearsing the exact plan there before the target is touched, a failing rehearsal refusing the apply with the target left unchanged, and `--dev-url` pointing at the target refused before the destructive dev reset? | the built `ptah` binary (`atlas schema apply`), ephemeral SQLite |
| `schema-scope-workflow` | Does `--schema`/`--include` scoping hold end to end (stokaro/ptah#813): a scoped apply creates only the selected objects and leaves out-of-scope objects (desired and pre-existing) untouched, repeated `--include` values union, a selection depending on unselected objects is refused with the cross-scope foreign-key diagnostic, and a malformed selector fails before the dev database file exists? | the built `ptah` binary (`atlas schema apply`/`diff`), ephemeral SQLite |
| `inspect-source-workflow` | Does the `schema inspect` source/export model hold (stokaro/ptah#814): a local schema file (file:// or scheme-less) materialized on a dev database and introspected back, `{{ hcl . \| split \| write "dir" }}` exporting the deterministic per-object tree whose files reload as a synced multi-file desired state, and `--exclude` supporting resource selectors plus the documented extension field selector while refusing unsupported field-selector forms? | the built `ptah` binary (`atlas schema inspect`/`diff`), ephemeral SQLite |
| `qualifier-txmode-workflow` | Do the `migrate diff` qualifier and txmode contracts hold (stokaro/ptah#815): an invalid `--qualifier` refused before the dev database exists, a valid qualifier scoped to schema-qualified dialects (refused pre-artifact on SQLite), the `diff { concurrent_index { create = true } }` policy planning the documented single plain transactional file on SQLite that replays through `migrate apply`, and a `-- atlas:txmode none` migration executing outside a transaction while a transactional control rolls back? | the built `ptah` binary (`atlas migrate diff`/`apply`/`hash`), ephemeral SQLite |

Each probe recovers from panics: a panic in Ptah on Atlas input is reported as its
own (strongest) outcome rather than aborting the run.

## Live And External-Toolchain Tiers

The probes above are deterministic and require no external services.
`dbtest-workflow`, `composite-schema-workflow`, `managed-data-workflow`,
`checkpoint-workflow`, `external-schema-workflow`, `pro-test-workflow`,
`pro-plan-workflow`, `pro-down-workflow`, `desired-state-workflow`,
`apply-simulation-workflow`, `schema-scope-workflow`,
`inspect-source-workflow`, and `qualifier-txmode-workflow` intentionally
execute local
ephemeral SQLite databases; the remaining offline probes do not open a
database. Three further tiers run against real networked databases in their
own workflows. The ORM provider tier installs external pinned toolchains but
does not require a database service. These tiers stay separate from the
deterministic report.

| Tier | Workflow | Question | Needs |
| --- | --- | --- | --- |
| `roundtrip-consistency` | [`conformance-live`](./.github/workflows/conformance-live.yml) | Does a first-party Ptah schema survive Ptah's own generate → apply → introspect → diff loop on a live database? Ptah-vs-Ptah, so no Pro/OSS ambiguity. CI runs Postgres, MySQL, MariaDB, and SQLite over basic tables, enums, views, indexes/FKs, composite keys, constraints/actions, generated columns, self-references, and richer default/type cases. PostgreSQL-only fixtures additionally cover schema-qualified objects, standalone sequences, domains, composite types, and range types. Successful rows list every non-empty object family checked by the round-trip diff. | `CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL` / `CONFORMANCE_MARIADB_URL`; optional `CONFORMANCE_SQLITE_URL` |
| `atlas-differential` | [`conformance-diff`](./.github/workflows/conformance-diff.yml) | Applied to the same live schema, do **Atlas CE and Ptah agree** about it? Atlas's `schema inspect` HCL is parsed by Ptah's own `core/atlashcl` into a typed schema and compared against Ptah's introspected schema by schema facts: columns, type/null/default/primary-key state, generated columns, foreign keys and actions, unique/check constraints, and indexes. Both sides are typed `goschema.Database`, so there is no fragile SQL-text parsing. CI runs Postgres, MySQL, and SQLite. | `CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL`; optional `CONFORMANCE_SQLITE_URL`; a real Atlas binary (`ATLAS_BIN`) |
| `migrate-runtime` | [`conformance-migrate-runtime`](./.github/workflows/conformance-migrate-runtime.yml) | Do real ptah-compat `atlas migrate apply/status/set` executions leave the same supported migration state Atlas callers rely on? This tier inspects schema objects and all Atlas revision metadata directly. For project configuration, Atlas CE applies the first migration, the harness clones that exact brownfield database, and Atlas CE and Ptah independently apply the remainder from the same untouched `atlas.hcl` without explicit URL or directory flags. The oracle compares status facts, end schema, stable revision fields, storage classes, producer identity, measured timing bounds, and Atlas CE readback. Exact dynamic timing equality is intentionally not claimed: pinned Atlas CE v1.2.0 rewrites `executed_at` during per-statement revision writes, while Ptah records the migration start and full elapsed duration. CI also covers SQLite `--revisions-schema main` and `--tx-mode all/file/none`, PostgreSQL custom revision schemas and `CREATE INDEX CONCURRENTLY` with `atlas:txmode none`, and MySQL apply/status revision state. | built `ptah-compat` and `ptah` binaries; pinned Atlas CE (`ATLAS_BIN`); local SQLite databases; `CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL` |
| `orm-provider-smoke` | [`conformance-orm-providers`](./.github/workflows/conformance-orm-providers.yml) | Do pinned GORM and SQLAlchemy providers emit the expected schema facts, and does Ptah preserve those facts when it executes the providers through `--schema-cmd`? | Go and Python package downloads; isolated temporary Go module and Python virtual environment; the built `ptah` binary |

The differential builds a **real Atlas CE binary** from the release tag pinned in
[`atlas.version`](./atlas.version) (`make atlas`), so it measures Ptah against a
known Atlas release rather than a moving target. It is scoped to the object kinds
Atlas CE can actually inspect: tables, columns, constraints, indexes and enums.
Atlas CE silently omits Pro-gated objects (views, triggers, stored procedures,
sequences) from inspection, so Ptah's support for those is a strength beyond CE,
not a differential gap — that fidelity is covered by the Ptah-vs-Ptah round-trip
tier instead. Today the differential tier agrees with Atlas CE on all committed
first-party live fixtures across Postgres, MySQL, and SQLite, including
enum/default spellings, generated columns, self-references, indexes, checks,
unique constraints, and foreign-key actions. The live round-trip tier also
agrees on the committed Postgres/MySQL/MariaDB/SQLite fixture corpus. Parsing
Atlas's HCL through Ptah's `core/atlashcl` also exercises a real
drop-in path — a parse failure there is itself reported as a gap, not mistaken
for a schema disagreement.

Live fixture directories may include an optional `fixture.json` manifest:

```json
{"dialects":["postgres"]}
```

Fixtures without the manifest run on every live dialect supported by the tier.
When `dialects` is set, the fixture runs only for those dialect names; use this
for schema shapes that are intentionally dialect-specific, such as PostgreSQL
multi-schema fixtures.

```
make probe-live   # regenerate gaps-live.md / gaps-live.json; SQLite runs without external DB
make budget-live  # live progress gate: red only on regression/stale waivers
make gate-live    # live corpus-parity yardstick: fails if any live non-OK remains
make atlas        # build Atlas CE from the atlas.version tag into ./bin/atlas
make probe-diff   # regenerate gaps-diff.md / gaps-diff.json (exit 0)
make budget-diff  # differential progress gate: red only on regression/stale waivers
make gate-diff    # differential corpus-parity yardstick: fails if any diff non-OK remains
make probe-migrate-runtime   # regenerate gaps-migrate-runtime.md / gaps-migrate-runtime.json
make budget-migrate-runtime  # migrate-runtime progress gate
make gate-migrate-runtime    # migrate-runtime full-parity yardstick
make probe-orm-providers     # regenerate gaps-orm-providers.md / gaps-orm-providers.json
make budget-orm-providers    # ORM provider progress gate
make gate-orm-providers      # ORM provider full-conformance gate
```

Local live runs are explicit per networked dialect. Set whichever service URLs
you want to exercise; unset networked dialects are skipped. SQLite always runs,
using `CONFORMANCE_SQLITE_URL` when set or a fresh temporary database otherwise:

```
CONFORMANCE_POSTGRES_URL='postgres://postgres:pw@localhost:5432/conf?sslmode=disable' \
CONFORMANCE_MYSQL_URL='mysql://root:pw@tcp(localhost:3306)/conf' \
CONFORMANCE_MARIADB_URL='mariadb://root:pw@tcp(localhost:3307)/conf' \
make probe-live
```

The migrate-runtime tier always runs SQLite checks and requires the pinned Atlas
CE binary for its independent project-config apply oracle. Set Postgres/MySQL
URLs to include the networked runtime contours:

```
ATLAS_BIN="$PWD/bin/atlas" \
CONFORMANCE_POSTGRES_URL='postgres://postgres:pw@localhost:5432/conf?sslmode=disable' \
CONFORMANCE_MYSQL_URL='mysql://root:pw@tcp(localhost:3306)/conf' \
make probe-migrate-runtime
```

The differential tier uses the same first-party fixtures but compares Ptah
against Atlas CE's own `schema inspect` output. MySQL uses Ptah's Go-driver URL
for Ptah and an Atlas-native authority URL for Atlas:

```
CONFORMANCE_POSTGRES_URL='postgres://postgres:pw@localhost:5432/conf?sslmode=disable' \
CONFORMANCE_MYSQL_URL='mysql://root:pw@tcp(localhost:3306)/conf' \
CONFORMANCE_MYSQL_ATLAS_URL='mysql://root:pw@localhost:3306/conf' \
ATLAS_BIN="$PWD/bin/atlas" \
make probe-diff
```

## CLI surface tier

The dedicated CLI surface report is [`cli-surface.md`](./cli-surface.md). It is
generated from the real Atlas CE binary pinned by [`atlas.version`](./atlas.version),
not from a static table. The probe recursively reads `atlas --help`,
`atlas schema --help`, `atlas migrate --help`, and every discovered leaf command's
help, then records:

- Atlas CE command paths;
- help `Usage:` lines;
- long flags from command and global help;
- explicit OSS vs out-of-scope classification;
- per-verb expectations for the out-of-scope (Pro/Cloud) commands: verbs Ptah
  has implemented as open capabilities (`migrate test`, `schema test`,
  `migrate edit`/`rebase`/`rm`, `schema plan`, `migrate checkpoint`) **must**
  resolve with a first-party usage/flag contract — regressing to Atlas CE's
  community-version abort stub is a gap — while still-stubbed Cloud/registry
  verbs (`migrate push`, `schema push`, the `schema plan` registry sub-verbs)
  **must** keep the CE abort boundary; dedicated workflow probes, not help
  output, own behavioral evidence for the implemented capabilities;
- compatibility findings for the `ptah-compat` binary named `atlas` — Ptah's
  single Atlas-shaped surface since stokaro/ptah#850 removed the
  `ptah atlas ...` namespace (the offline `cli-exit-behavior` probe pins that
  the main `ptah` binary keeps rejecting the namespace with exit 2).

The regression budget is [`cli-surface-budget.txt`](./cli-surface-budget.txt).
`make budget-cli-surface` must stay green when Ptah preserves the current known
CLI surface. `make gate-cli-surface` is the full-parity signal for the pinned
Atlas CE OSS help/flag surface and is green on the current committed report.

Refresh this tier whenever [`atlas.version`](./atlas.version) changes, or after
bumping Ptah in `go.mod`:

```
make atlas              # rebuild ./bin/atlas from the pinned Atlas CE tag
make probe-cli-surface  # regenerate cli-surface.md / cli-surface.json
make budget-cli-surface # verify the committed regression budget
```

If the report shows a new Atlas CE OSS command or flag, either implement the Ptah
gap and lower the budget, or keep the report red and raise/refresh the budget
only as an explicit measurement-baseline change. Do not remove commands from the
inventory to make the report green.

## CI regression budget and full-parity gate

This is a measured corpus, not a claim of complete Atlas feature parity. CI
publishes two separate pipelines:

- [`conformance-regression`](./.github/workflows/conformance-regression.yml)
  uses a committed offline corpus gap budget so progress PRs fail only when the
  current report gets worse or stale. The budget counts all unwaived non-OK
  observations: `gap`, `fail`, and `panic`.
- [`conformance-live`](./.github/workflows/conformance-live.yml),
  [`conformance-diff`](./.github/workflows/conformance-diff.yml), and
  [`conformance-migrate-runtime`](./.github/workflows/conformance-migrate-runtime.yml)
  use the same regression-budget model for their real-database reports. They
  must stay green when a PR preserves the current reports, and fail when
  `gaps-live.*`, `gaps-diff.*`, or `gaps-migrate-runtime.*` is stale or worse
  than its committed budget.
- [`conformance-orm-providers`](./.github/workflows/conformance-orm-providers.yml)
  has independent regression-budget and full-gate jobs. The regression job
  requires `gaps-orm-providers.*` to be current and within its committed
  budget; the full job fails on every non-OK provider observation.
- The CLI surface job in
  [`conformance-regression`](./.github/workflows/conformance-regression.yml)
  uses [`cli-surface-budget.txt`](./cli-surface-budget.txt) the same way for
  `cli-surface.*`.
- [`full-conformance`](./.github/workflows/full-conformance.yml) runs
  `make gate`, `make gate-live`, `make gate-diff`, and
  `make gate-migrate-runtime`, and `make gate-cli-surface` as separate jobs. It
  is green only when Ptah covers the committed offline corpus, live round-trip
  corpus, Atlas CE differential corpus, Atlas migrate runtime contours, and
  Atlas CE CLI help/flag surface. When probes become stricter, a generated
  report may expose more non-OK observations even without a Ptah code change;
  that is a measurement hardening and must be committed explicitly with the new
  report/budget baseline. This workflow is a visible yardstick, not the
  regression/merge gate; branch protection should require the regression-budget
  workflows above, and may keep full conformance as a separate status signal.

- `make probe` regenerates the report and always exits 0.
- `make budget` fails if the generated report exceeds [`gap-budget.txt`](./gap-budget.txt)
  or if a waiver became stale. `make budget-live`, `make budget-diff`, and
  `make budget-migrate-runtime` do the same for
  [`gap-live-budget.txt`](./gap-live-budget.txt),
  [`gap-diff-budget.txt`](./gap-diff-budget.txt), and
  [`gap-migrate-runtime-budget.txt`](./gap-migrate-runtime-budget.txt).
- `make gate`, `make gate-live`, `make gate-diff`, and
  `make gate-migrate-runtime` regenerate their reports **and exit non-zero if
  any non-OK observation remains**, including waived findings. These are the
  corpus-parity yardsticks for their matching reports.
- `make budget-orm-providers` and `make gate-orm-providers` apply the same
  split to the external provider report.

A gap can be excused only by an explicit line in [`waivers.txt`](./waivers.txt),
keyed on `probe fixture stage`, with a reason and a tracking issue. A waiver means
"consciously tracked, do not fail on it yet" — not "fine forever". The file is
intentionally empty: nothing is skipped, so every open gap is red. A waiver that
matches no finding is itself a CI failure, forcing cleanup when a gap closes.

Lower `gap-budget.txt`, `gap-live-budget.txt`, `gap-diff-budget.txt`, and
`gap-migrate-runtime-budget.txt` whenever Ptah closes gaps in the matching
report. `git log gaps.md` shows the unwaived count moving toward zero as Ptah
closes issues. The full gates go green only when every fixture and live contour
is covered.

```
make probe        # regenerate gaps.md / gaps.json (exit 0)
make budget       # offline progress gate: red only on regression/stale waivers
make probe-live   # regenerate gaps-live.md / gaps-live.json; SQLite runs without external DB
make budget-live  # live progress gate: red only on regression/stale waivers
make gate-live    # live corpus-parity yardstick: fails if any live non-OK remains
make probe-diff   # regenerate gaps-diff.md / gaps-diff.json (exit 0)
make budget-diff  # differential progress gate: red only on regression/stale waivers
make gate-diff    # differential corpus-parity yardstick: fails if any diff non-OK remains
make probe-migrate-runtime   # regenerate gaps-migrate-runtime.md / gaps-migrate-runtime.json
make budget-migrate-runtime  # migrate-runtime progress gate
make gate-migrate-runtime    # migrate-runtime corpus-parity yardstick
make probe-orm-providers     # regenerate gaps-orm-providers.md / gaps-orm-providers.json
make budget-orm-providers    # ORM provider progress gate
make gate-orm-providers      # ORM provider full-conformance gate
make probe-cli-surface   # regenerate cli-surface.md / cli-surface.json (exit 0)
make budget-cli-surface  # CLI progress gate: red only on regression/stale waivers
make gate-cli-surface    # CLI corpus-parity yardstick: fails if any CLI non-OK remains
ATLAS_BIN=./bin/atlas make verify-cli-exit-oracle  # audit static exit/output expectations against Atlas CE
make gate         # offline corpus-parity yardstick: fails if any offline non-OK remains
make verify       # test, build, vet, and assert Ptah's tree would gain no Apache file
```

## Pinning

Both sides are pinned for reproducibility:

- Atlas fixtures: every file under `*/testdata/*` from
  `ariga/atlas@a5e0aecc2bb64143bf522734f8ad88e04885fca6`, vendored under
  `third_party/atlas/upstream/` (never fetched at run time). The exact file list
  is `third_party/atlas/MANIFEST.txt`.
- Atlas binary (differential, CLI-surface, and exit-oracle tiers): the release tag in
  [`atlas.version`](./atlas.version), built from source by `make atlas`.
  [`renovate.json`](./renovate.json) carries a custom manager that bumps this pin
  automatically when Atlas cuts a new release, so the differential and CLI
  surface probes follow upstream on their next report refresh.
- Ptah: pinned in `go.mod`. Bump it to measure a newer Ptah.

## Relationship to Ptah issues

- [ptah#289](https://github.com/stokaro/ptah/issues/289) — the issue this repo implements.
- [ptah#285](https://github.com/stokaro/ptah/issues/285) — the complementary scoreboard (end-state equivalence over Ptah's *own* fixtures).
- [ptah#273](https://github.com/stokaro/ptah/issues/273) / [#274](https://github.com/stokaro/ptah/issues/274) — the migration-directory and `atlas.sum` gaps this probe currently surfaces.
