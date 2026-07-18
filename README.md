# ptah-atlas-conformance

A coverage probe that drives [Atlas](https://github.com/ariga/atlas)'s own
migration fixtures through [Ptah](https://github.com/stokaro/ptah)'s public API
and reports, per fixture, the stage at which Ptah cannot ingest what Atlas
authored.

It answers a question Ptah's own test suite cannot: **what does Atlas express
that Ptah does not yet cover?** By construction, fixtures written by the Ptah
maintainer only exercise what the maintainer already thought to support. Pointing
Atlas's corpus at Ptah surfaces the blind spots.

The generated report is [`gaps.md`](./gaps.md). It is a coverage map, not a
quality score: a `gap` is a thing Atlas expresses that Ptah does not model yet,
and each row links the Ptah issue that tracks closing it.

**This is not a full feature-set parity test.** The repository now vendors every
file under Atlas's open-source `*/testdata/*` tree at the pinned commit (286 files, grouped into report fixtures), plus a
small first-party Atlas-compatible regression fixture for gaps not present in the
upstream snapshot. The report distinguishes fixtures that are actually measured
from fixtures that are merely imported and still lack a probe. [`PARITY.md`](./PARITY.md)
states exactly what is and is not tested — read it before quoting any number from
here.

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
| `txtar-script` | Does the harness parse Atlas integration txtar scripts, execute the narrow command subset currently mapped to Ptah APIs, and keep unsupported runtime commands red? | harness, `core/parser`, `core/renderer` |
| `txtar-down` | Does Ptah load Atlas txtar migrations with an embedded `down.sql` section? | `migration/migrator` |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does Ptah's own hash reproduce it? | `migration/migratesum` |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content, or only its file names? | `migration/lint` |
| `atlas-cli-surface` | Does `ptah atlas <verb>` resolve for every OSS Atlas CLI verb? This is the `ptah atlas ...` drop-in surface; it builds the real Ptah CLI and checks each command, so it flips to green on its own when Ptah registers the command. | the built `ptah` binary |
| `lint-analyzer-catalog` | For each Atlas sqlcheck analyzer concern, does Ptah's linter flag the same dangerous change? Behavioral, one synthetic migration per analyzer, so it flips green when Ptah gains the rule. | `migration/lint` |
| `lex-split-parity` | Does Ptah split a migration into the same statements Atlas does? A differential check against Atlas's own `.golden` lexer outputs (no live Atlas needed), normalized so it measures statement boundaries, not comment preservation. Surfaces real drop-in blockers: function bodies, `BEGIN ATOMIC`, MySQL `DELIMITER`. | `migration/migrator` |

Each probe recovers from panics: a panic in Ptah on Atlas input is reported as its
own (strongest) outcome rather than aborting the run.

## CI regression budget and full-parity gate

This is a spec Ptah has not met, not a passing test log. CI publishes two
separate pipelines:

- [`conformance-regression`](./.github/workflows/conformance-regression.yml)
  uses a committed gap budget so progress PRs fail only when the current report
  gets worse or stale. The budget counts all unwaived non-OK observations:
  `gap`, `fail`, and `panic`.
- [`full-conformance`](./.github/workflows/full-conformance.yml) runs
  `make gate` and stays red until Ptah covers everything Atlas expresses in the
  corpus. When probes become stricter, the generated report may expose more
  non-OK observations even without a Ptah code change; that is a measurement
  hardening and must be committed explicitly with the new report/budget
  baseline.

- `make probe` regenerates the report and always exits 0.
- `make budget` fails if the generated report exceeds [`gap-budget.txt`](./gap-budget.txt)
  or if a waiver became stale.
- `make gate` regenerates the report **and exits non-zero if any non-OK
  observation remains**, including waived findings.
  This is the full-parity yardstick and stays red until Ptah covers everything
  Atlas expresses in the corpus.

A gap can be excused only by an explicit line in [`waivers.txt`](./waivers.txt),
keyed on `probe fixture stage`, with a reason and a tracking issue. A waiver means
"consciously tracked, do not fail on it yet" — not "fine forever". The file is
intentionally empty: nothing is skipped, so every open gap is red. A waiver that
matches no finding is itself a CI failure, forcing cleanup when a gap closes.

Lower `gap-budget.txt` whenever Ptah closes gaps. `git log gaps.md` shows the
unwaived count moving toward zero as Ptah closes issues. The full gate goes green
only when every fixture is covered.

```
make probe        # regenerate gaps.md / gaps.json (exit 0)
make budget       # CI progress gate: red only on regression/stale waivers
make gate         # full-parity yardstick: red until parity on the corpus
make verify       # build, vet, and assert Ptah's tree would gain no Apache file
```

## Pinning

Both sides are pinned for reproducibility:

- Atlas fixtures: every file under `*/testdata/*` from
  `ariga/atlas@a5e0aecc2bb64143bf522734f8ad88e04885fca6`, vendored under
  `third_party/atlas/upstream/` (never fetched at run time). The exact file list
  is `third_party/atlas/MANIFEST.txt`.
- Ptah: pinned in `go.mod`. Bump it to measure a newer Ptah.

## Relationship to Ptah issues

- [ptah#289](https://github.com/stokaro/ptah/issues/289) — the issue this repo implements.
- [ptah#285](https://github.com/stokaro/ptah/issues/285) — the complementary scoreboard (end-state equivalence over Ptah's *own* fixtures).
- [ptah#273](https://github.com/stokaro/ptah/issues/273) / [#274](https://github.com/stokaro/ptah/issues/274) — the migration-directory and `atlas.sum` gaps this probe currently surfaces.
