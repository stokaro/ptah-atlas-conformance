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
| `txtar-script` | Does the harness parse Atlas integration txtar scripts, execute the command subset currently mapped to Ptah APIs, publish the fixture-level script surface, and keep unsupported runtime commands red? | harness, `core/parser`, `core/renderer` |
| `txtar-down` | Does Ptah load Atlas txtar migrations with an embedded `down.sql` section? | `migration/migrator` |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does Ptah's own hash reproduce it? | `migration/migratesum` |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content, or only its file names? | `migration/lint` |
| `atlas-cli-surface` | Does `ptah atlas <verb>` resolve for every OSS Atlas CLI verb? This is the `ptah atlas ...` drop-in surface; it builds the real Ptah CLI and checks each command, so it flips to green on its own when Ptah registers the command. | the built `ptah` binary |
| `atlas-cli-flags` | Beyond resolving, does each `ptah atlas <verb>` accept the Atlas flags a drop-in caller passes (`--url`, `--dev-url`, `--to`, `--dir`, `--format`, …)? A resolving stub is not a drop-in. | the built `ptah` binary |
| `cli-exit-behavior` | Do `ptah atlas ...` and `ptah-compat` match Atlas CE's exact process exit code and stdout/stderr contract for representative success, argument, configuration, and migration-checksum paths? Stable checksum and unknown-command output is byte-checked. The catalog is also run directly against the pinned Atlas binary so Ptah-specific expectations cannot make the probe false-green. | `bin/atlas`, the built `ptah` binary, the built `ptah-compat` binary |
| `atlas-cli-surface-inventory` / `atlas-cli-surface-ptah-*` | Dedicated CLI surface report over the current pinned Atlas CE binary: command paths, help usage, and long flags, compared separately against `ptah atlas ...` and binary-level `ptah-compat` drop-in behavior. | `bin/atlas`, the built `ptah` binary, the built `ptah-compat` binary |
| `migrate-runtime` | Does `ptah atlas migrate ...` preserve Atlas-compatible runtime state against real databases: applied schema objects, Atlas revision rows, `set` repair behavior, and transaction-mode rollback/partial-apply semantics? | the built `ptah` binary, live SQLite databases |
| `lint-analyzer-catalog` | For each Atlas sqlcheck analyzer concern, does Ptah's linter flag the same dangerous change? Behavioral, one synthetic migration per analyzer, so it flips green when Ptah gains the rule. | `migration/lint` |
| `lex-split-parity` | Does Ptah split a migration into the same statements Atlas does? A differential check against Atlas's own `.golden` lexer outputs (no live Atlas needed), normalized so it measures statement boundaries, not comment preservation. Surfaces real drop-in blockers: function bodies, `BEGIN ATOMIC`, MySQL `DELIMITER`. | `core/sqlutil` dialect-aware statement splitting |
| `dbtest-workflow` | Do the native `ptah migrations test` and `ptah schema test` commands execute committed migration/schema/seed fixtures against isolated SQLite databases, filter cases, repair schema drift, render text/JSON/HTML reports, and preserve exit codes 1 (assertion failure) and 2 (setup failure)? | the built `ptah` binary, `migration/dbtest`, ephemeral SQLite |

Each probe recovers from panics: a panic in Ptah on Atlas input is reported as its
own (strongest) outcome rather than aborting the run.

## Live tiers (real database)

The probes above are deterministic and require no external services.
`dbtest-workflow` intentionally executes local ephemeral SQLite databases; the
remaining offline probes do not open a database. Three further tiers run against
real networked databases in their own workflows, kept separate from the
deterministic report.

| Tier | Workflow | Question | Needs |
| --- | --- | --- | --- |
| `roundtrip-consistency` | [`conformance-live`](./.github/workflows/conformance-live.yml) | Does a first-party Ptah schema survive Ptah's own generate → apply → introspect → diff loop on a live database? Ptah-vs-Ptah, so no Pro/OSS ambiguity. CI runs Postgres, MySQL, MariaDB, and SQLite over basic tables, enums, views, indexes/FKs, composite keys, constraints/actions, generated columns, self-references, and richer default/type cases. | `CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL` / `CONFORMANCE_MARIADB_URL`; optional `CONFORMANCE_SQLITE_URL` |
| `atlas-differential` | [`conformance-diff`](./.github/workflows/conformance-diff.yml) | Applied to the same live schema, do **Atlas CE and Ptah agree** about it? Atlas's `schema inspect` HCL is parsed by Ptah's own `core/atlashcl` into a typed schema and compared against Ptah's introspected schema by schema facts: columns, type/null/default/primary-key state, generated columns, foreign keys and actions, unique/check constraints, and indexes. Both sides are typed `goschema.Database`, so there is no fragile SQL-text parsing. CI runs Postgres, MySQL, and SQLite. | `CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL`; optional `CONFORMANCE_SQLITE_URL`; a real Atlas binary (`ATLAS_BIN`) |
| `migrate-runtime` | [`conformance-migrate-runtime`](./.github/workflows/conformance-migrate-runtime.yml) | Do real `ptah atlas migrate apply/status/set` executions leave the same supported migration state Atlas callers rely on? This tier inspects schema objects and `atlas_schema_revisions` rows directly. CI covers SQLite `--revisions-schema main` and `--tx-mode all/file/none`, PostgreSQL custom revision schemas and `CREATE INDEX CONCURRENTLY` with `atlas:txmode none`, and MySQL apply/status revision state. | built `ptah` binary; local SQLite databases; `CONFORMANCE_POSTGRES_URL` / `CONFORMANCE_MYSQL_URL` |

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

The migrate-runtime tier always runs SQLite checks. Set Postgres/MySQL URLs to
include the networked runtime contours:

```
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
- community-version unsupported behavior for Atlas CE commands that are present
  only as Cloud/commercial features, or explicit resolution when Ptah provides
  an open capability beyond CE; dedicated workflow probes, not help output, own
  behavioral evidence for those extra capabilities;
- separate compatibility findings for `ptah atlas ...` and for a `ptah-compat`
  binary named `atlas`.

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
