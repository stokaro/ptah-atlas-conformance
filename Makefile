.PHONY: probe budget gate probe-live budget-live gate-live probe-diff budget-diff gate-diff probe-cli-surface budget-cli-surface gate-cli-surface atlas verify build vet clean

GO ?= go
GO_OFF := GOWORK=off $(GO)

# Regenerate the gap report from the vendored corpus. Always exits 0 — use this
# to refresh gaps.md / gaps.json.
probe:
	$(GO_OFF) run ./cmd/gap-probe

# The live behavioral tier: apply first-party schemas to a real database,
# introspect them back, and diff. Kept separate from the offline probes so the
# offline report stays deterministic and DB-free. Needs CONFORMANCE_POSTGRES_URL
# and/or CONFORMANCE_MYSQL_URL. Regenerates gaps-live.md / gaps-live.json and
# always exits 0.
probe-live:
	$(GO_OFF) run ./cmd/gap-probe-live

# CI progress gate for the live behavioral tier: fail only when the current
# live report exceeds the committed budget or has stale waivers. Full live
# corpus parity is still `make gate-live`.
budget-live: probe-live
	$(GO_OFF) run ./cmd/gap-budget -report gaps-live.json -budget gap-live-budget.txt

# The live conformance gate: regenerate the live report AND fail if any schema
# does not survive Ptah's generate -> apply -> introspect loop. Needs
# CONFORMANCE_POSTGRES_URL and/or CONFORMANCE_MYSQL_URL.
gate-live:
	$(GO_OFF) run ./cmd/gap-probe-live -gate

# Build Atlas CE from the tag pinned in atlas.version, into ./bin/atlas, so the
# differential tier compares against a known release (renovate bumps the pin).
# The Atlas CLI lives in its own nested module (ariga.io/atlas/cmd/atlas), so the
# build runs from that directory with GOWORK=off. The version is injected via
# ldflags so `atlas version` reports the release, not "- (canary)".
ATLAS_TAG := $(shell cat atlas.version)
atlas:
	@echo "building Atlas CE $(ATLAS_TAG) from source ..."
	rm -rf build/atlas-src && git clone --depth 1 --branch $(ATLAS_TAG) https://github.com/ariga/atlas build/atlas-src
	cd build/atlas-src/cmd/atlas && $(GO_OFF) build \
		-ldflags "-X ariga.io/atlas/cmd/atlas/internal/cmdapi.version=$(ATLAS_TAG)" \
		-o $(CURDIR)/bin/atlas .
	@$(CURDIR)/bin/atlas version | head -1

# The differential-vs-Atlas tier: apply first-party schemas to a real Postgres,
# then compare what Atlas CE and Ptah each report about the schema. Needs
# CONFORMANCE_POSTGRES_URL and ATLAS_BIN (or `atlas` on PATH). Regenerates
# gaps-diff.md / gaps-diff.json and always exits 0.
probe-diff:
	$(GO_OFF) run ./cmd/gap-probe-diff

# CI progress gate for the differential-vs-Atlas tier: fail only when the
# current differential report exceeds the committed budget or has stale waivers.
# Corpus-level Atlas agreement is still `make gate-diff`.
budget-diff: probe-diff
	$(GO_OFF) run ./cmd/gap-budget -report gaps-diff.json -budget gap-diff-budget.txt

# The differential gate: regenerate the report AND fail while Ptah disagrees with
# Atlas CE on any committed CE-visible construct.
gate-diff:
	$(GO_OFF) run ./cmd/gap-probe-diff -gate

# The CLI surface tier: build/read the pinned Atlas CE binary and compare its
# command help/usage/flag inventory to both `ptah atlas ...` and a ptah-compat
# binary named `atlas`. Regenerates cli-surface.md / cli-surface.json and
# always exits 0.
probe-cli-surface:
	$(GO_OFF) run ./cmd/cli-surface-probe

# CI progress gate for the CLI surface tier: fail only when the current
# CLI-surface report exceeds the committed budget. Corpus help/flag parity is
# still `make gate-cli-surface`.
budget-cli-surface: probe-cli-surface
	$(GO_OFF) run ./cmd/gap-budget -report cli-surface.json -budget cli-surface-budget.txt

# The full CLI surface gate: fail while any committed Atlas CE OSS command,
# usage string, or long flag is not mirrored by both Ptah compatibility surfaces.
gate-cli-surface:
	$(GO_OFF) run ./cmd/cli-surface-probe -gate

# CI progress gate: fail only when the current report exceeds the committed
# unwaived non-OK observation budget or has stale waivers. Corpus parity is still
# `make gate`.
budget: probe
	$(GO_OFF) run ./cmd/gap-budget

# The conformance gate: regenerate the report AND fail if any non-OK observation
# remains in the committed offline corpus.
gate:
	$(GO_OFF) run ./cmd/gap-probe -gate

build:
	$(GO_OFF) build ./...

vet:
	$(GO_OFF) vet ./...

# Guard the one-way boundary: this repo may depend on ptah, but the Apache-2.0
# fixtures must stay confined to third_party/. Fails if an Apache header leaks
# into the harness source.
verify: build vet
	@echo "checking no Apache-licensed material outside third_party/ ..."
	@! grep -rIl "Apache License" --include='*.go' . | grep -v '/third_party/' || \
		{ echo "Apache-licensed material found outside third_party/"; exit 1; }
	@echo "ok"

clean:
	rm -f gaps.md gaps.json
