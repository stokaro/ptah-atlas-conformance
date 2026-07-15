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

**This is not a full feature-set parity test.** It is a small offline probe over
a seed corpus, and most of Atlas's open-source surface is not exercised at all.
The corpus is mostly vendored Atlas material plus a small first-party
Atlas-compatible regression fixture for features absent from the current vendored
sample. [`PARITY.md`](./PARITY.md) states exactly what is and is not tested, and
what a real parity test would require — read it before quoting any number from
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
| `sql-parse` | Can Ptah's DDL parser represent Atlas's SQL in its AST? (round-trip / `read-db` / `compare` — **not** apply, which execs raw SQL) | `core/parser` |
| `migdir-ingest` | Does Ptah's migrator recognize the files in an Atlas migration directory? | `migration/migrator` |
| `txtar-down` | Does Ptah load Atlas txtar migrations with an embedded `down.sql` section? | `migration/migrator` |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does Ptah's own hash reproduce it? | `migration/migratesum` |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content, or only its file names? | `migration/lint` |

Each probe recovers from panics: a panic in Ptah on Atlas input is reported as its
own (strongest) outcome rather than aborting the run.

## The gate is red until done

This is a spec Ptah has not met, not a passing test log. The `conformance-gate`
CI job **fails while any gap remains unwaived**, and stays red until Ptah covers
everything Atlas expresses in the corpus. A red gate means "not done yet", not
"broken" — which is why the harness's own health is a separate, always-green job.

- `make probe` regenerates the report and always exits 0.
- `make gate` regenerates it **and exits non-zero if any unwaived gap remains**.

A gap can be excused only by an explicit line in [`waivers.txt`](./waivers.txt),
keyed on `probe fixture stage`, with a reason and a tracking issue. A waiver means
"consciously tracked, do not fail on it yet" — not "fine forever". The file is
intentionally empty: nothing is skipped, so every open gap is red. A waiver that
matches no finding is itself a failure, forcing cleanup when a gap closes.

The gate goes green only when every fixture is covered or waived. `git log gaps.md`
shows the unwaived count moving toward zero as Ptah closes issues.

```
make probe        # regenerate gaps.md / gaps.json (exit 0)
make gate         # the yardstick: red until parity on the corpus
make verify       # build, vet, and assert Ptah's tree would gain no Apache file
```

## Pinning

Both sides are pinned for reproducibility:

- Atlas fixtures: `ariga/atlas@a5e0aecc2bb64143bf522734f8ad88e04885fca6`, vendored
  under `third_party/atlas/` (never fetched at run time).
- Ptah: pinned in `go.mod`. Bump it to measure a newer Ptah.

## Relationship to Ptah issues

- [ptah#289](https://github.com/stokaro/ptah/issues/289) — the issue this repo implements.
- [ptah#285](https://github.com/stokaro/ptah/issues/285) — the complementary scoreboard (end-state equivalence over Ptah's *own* fixtures).
- [ptah#273](https://github.com/stokaro/ptah/issues/273) / [#274](https://github.com/stokaro/ptah/issues/274) — the migration-directory and `atlas.sum` gaps this probe currently surfaces.
