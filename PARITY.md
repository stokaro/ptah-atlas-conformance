# Parity status — what this does and does not test

**This is not a full feature-set parity test, and no number in this repository
should be read as one.**

It is an offline, structural coverage probe over the full vendored Atlas
`*/testdata/*` snapshot plus first-party Atlas-compatible regression fixtures,
run through narrow entry points of Ptah's public API. It exists to turn "are we
there yet" from an opinion into a number that moves over time. Treat the results
as a floor on the distance to Atlas, never a ceiling.

Generated snapshot: 286 vendored upstream testdata files grouped into 158 fixtures, 415 observations, **275 unwaived gaps**. The
corpus inventory imports 158 fixtures: 47 are measured by at least one current
probe, and 111 are explicitly red as imported-but-unmeasured (`.txtar`, `.hcl`,
and other Atlas test artifacts that still need dedicated probes).

## What the probe found broken

All probes are offline and static — nothing is applied to a real database.

| Probe | What it checks | Result |
| --- | --- | --- |
| `corpus-inventory` | Is every imported Atlas test artifact visible in the report? | 47 SQL/sum fixtures are measured by concrete probes; 111 imported `.txtar`, `.hcl`, and other fixtures are deliberately red until probes consume them. |
| `sql-parse` | Can Ptah's DDL parser represent Atlas SQL in its AST (the `read-db` / `compare` round-trip path — **not** apply)? | Runs over all 125 vendored `.sql` files. Plain `CREATE TABLE` passes; many Atlas SQL dialect/lexer fixtures fail or gap because Ptah's parser only accepts a limited DDL subset. |
| `migdir-ingest` | Does Ptah's migrator recognize the files in an Atlas migration directory? | **0 of N** files recognized in every fixture. Atlas names files `NNNNNNNNNNNNNN_desc.sql` (14-digit, single file); Ptah requires `NNNNNNNNNN_desc.(up\|down).sql`. (ptah#273) |
| `sum-compat` | Can Ptah parse `atlas.sum`, and does its own hash reproduce it? | Parses the format (good), but its recomputed directory hash **differs** — Ptah only hashes files it recognizes, so it hashes zero of Atlas's and produces a different sum. (ptah#274) |
| `lint-parity` | Does Ptah's linter analyze an Atlas migration's content? | On a directory containing `DROP TABLE`, Ptah emits only `MF103` ("non-conventional file name") and never the destructive finding (`DS101`) Atlas would. It flags Atlas's file names instead of reading their content. |

Each probe recovers from panics; none panicked on this corpus.

## What is NOT tested at all

This is the important half of "what doesn't work": most of Atlas's open-source
(Apache-2.0) surface is not exercised here, so for those areas the honest answer
is **"unknown — not measured"**, not "works".

| Atlas open-source capability | Tested here? | Where it would be measured |
| --- | --- | --- |
| Schema **introspection** breadth (types, defaults, generated/partial/expression indexes, sequences, domains, composite types, FK actions, exclusion constraints, partitioning, collations, comments) | **No** | a dedicated introspection probe against a live DB |
| Schema **diff / plan** (desired A → desired B → migration) | **No** | a diff probe over paired schema fixtures |
| **End-state equivalence** (apply with Atlas and with Ptah, compare the resulting databases) | **No** | ptah#285 — not built yet; needs a live DB |
| **HCL** schema language | **Imported, red/unmeasured** | HCL files are vendored and reported by `corpus-inventory`; Ptah largely does not read HCL (only the planned limited C3 subset, ptah#276) |
| Versioned-migrate **runtime semantics** (tx-mode, execution order, baseline, advisory lock, revision-table shape) | **No** | a runtime probe; several are open Ptah issues (#124, #265, #275) |
| **sqlcheck analyzers**, rule by rule | **No** | a lint matrix mapping Atlas codes ↔ Ptah rules |
| **Multi-dialect** depth (MySQL, SQLite, MariaDB) | **Imported, partially measured** | SQL files from MySQL/SQLite-oriented Atlas fixtures are parsed/linted structurally; dialect runtime semantics need dedicated probes |
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

## What a real full-parity test would require

To earn the phrase "feature-set parity test", this repo would need, at minimum:

1. Runtime probes for the imported Atlas `.txtar` integration fixtures, not just
   inventory rows.
2. An **introspection** probe: apply a schema with each tool, introspect with
   one reader, diff the canonical states (this is ptah#285 and needs a live DB).
3. A **diff/plan** probe over paired before/after schemas.
4. A **lint matrix** comparing Atlas analyzer codes against Ptah rule codes,
   fixture by fixture.
5. **Multi-dialect** coverage (MySQL, SQLite, MariaDB), not PostgreSQL only.
6. A declared, justified scope for what is deliberately **out** of parity (HCL
   schema, Cloud, Pro drivers), so "parity" has an explicit boundary.

Until those exist, this repository answers a broader but still bounded question
honestly — *where, across Atlas's vendored testdata snapshot, does Ptah visibly
fail to ingest what Atlas produced, and which imported fixtures are not measured
yet* — and nothing wider.
