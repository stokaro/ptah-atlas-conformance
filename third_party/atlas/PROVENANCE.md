# Vendored Atlas testdata — provenance and attribution

The files under `third_party/atlas/upstream/` are **not original work of this
repository**. They are copied byte-for-byte from the Atlas project and are used
here, under the terms of their license, as inputs to the coverage probe.

- **Upstream:** https://github.com/ariga/atlas
- **Copyright:** 2021-present The Atlas Authors
- **License:** Apache License 2.0 (see `LICENSE` in this directory)
- **Pinned commit:** `a5e0aecc2bb64143bf522734f8ad88e04885fca6`
- **Scope:** every file under Atlas `*/testdata/*` at the pinned commit (286 files)
- **Manifest:** `third_party/atlas/MANIFEST.txt`

No upstream fixture file has been modified. The directory layout under
`third_party/atlas/upstream/` preserves the upstream-relative paths so report rows
can be traced back to the Atlas repository directly.

## Why this is license-clean

Atlas's `ariga/atlas` repository is Apache-2.0, and its source files carry the
header `Copyright 2021-present The Atlas Authors ... licensed under the Apache 2.0
license`. Apache-2.0 permits redistribution provided the license and attribution
are retained, which this file and the sibling `LICENSE` do. There is no `NOTICE`
file upstream, so Apache-2.0 section 4(d) NOTICE-propagation does not apply.

The Atlas material is confined to this `third_party/` subtree. Nothing outside it
is Apache-licensed, and this repository never becomes a dependency of Ptah, so
Ptah's own tree stays MIT with no attribution obligation. See the repository
README for the one-way dependency rule.

## Updating the pin

To move to a newer Atlas commit:

1. Clone or fetch the new `ariga/atlas` commit.
2. Replace `third_party/atlas/upstream/` with every upstream file whose path
   contains `/testdata/`.
3. Regenerate `third_party/atlas/MANIFEST.txt` from those copied paths.
4. Update the pinned SHA here and in `cmd/gap-probe/main.go` (`atlasSHA`).
5. Confirm the upstream license is still Apache-2.0.
6. Run `make probe` and commit the regenerated `gaps.md` / `gaps.json`.
