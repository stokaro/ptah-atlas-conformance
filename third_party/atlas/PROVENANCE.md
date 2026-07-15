# Vendored Atlas fixtures — provenance and attribution

The files under this directory are **not original work of this repository**. They
are test fixtures copied verbatim from the Atlas project and are used here, under
the terms of their license, as inputs to the coverage probe.

- **Upstream:** https://github.com/ariga/atlas
- **Copyright:** 2021-present The Atlas Authors
- **License:** Apache License 2.0 (see `LICENSE` in this directory)
- **Pinned commit:** `a5e0aecc2bb64143bf522734f8ad88e04885fca6`

No fixture file has been modified. They are reproduced byte-for-byte so the probe
measures Ptah against exactly what Atlas ships.

## Files and their upstream paths

| Vendored path | Upstream path (at the pinned commit) |
| --- | --- |
| `migrations/atlasexec-basic/20230727105553_init.sql` | `atlasexec/testdata/migrations/20230727105553_init.sql` |
| `migrations/atlasexec-basic/20230727105615_t2.sql` | `atlasexec/testdata/migrations/20230727105615_t2.sql` |
| `migrations/atlasexec-basic/20230926085734_destructive-change.sql` | `atlasexec/testdata/migrations/20230926085734_destructive-change.sql` |
| `migrations/atlasexec-basic/atlas.sum` | `atlasexec/testdata/migrations/atlas.sum` |
| `migrations/postgres-schema/1_initial.sql` | `internal/integration/testdata/migrations/postgres/1_initial.sql` |
| `directives/10_delimiter_comment.sql` | `sql/migrate/testdata/lex/10_delimiter_comment.sql` |

## Why this is license-clean

Atlas's `ariga/atlas` repository is Apache-2.0, and its source files carry the
header `Copyright 2021-present The Atlas Authors ... licensed under the Apache 2.0
license`. Apache-2.0 permits redistribution provided the license and attribution
are retained, which this file and the sibling `LICENSE` do. There is no `NOTICE`
file upstream, so Apache-2.0 §4(d) NOTICE-propagation does not apply.

The Atlas material is confined to this `third_party/` subtree. Nothing outside it
is Apache-licensed, and this repository never becomes a dependency of Ptah, so
Ptah's own tree stays MIT with no attribution obligation. See the repository
README for the one-way dependency rule.

## Updating the pin

To move to a newer Atlas commit: re-copy the files above from the new commit,
update the pinned SHA here and in `cmd/gap-probe/main.go` (`atlasSHA`), confirm
the upstream license is still Apache-2.0, and re-run `make probe`.
