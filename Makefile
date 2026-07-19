.PHONY: probe budget gate probe-live budget-live gate-live probe-diff budget-diff gate-diff atlas verify build vet clean

# Regenerate the gap report from the vendored corpus. Always exits 0 — use this
# to refresh gaps.md / gaps.json.
probe:
	go run ./cmd/gap-probe

# The live behavioral tier: apply first-party schemas to a real database,
# introspect them back, and diff. Kept separate from the offline probes so the
# offline report stays deterministic and DB-free. Needs CONFORMANCE_POSTGRES_URL
# and/or CONFORMANCE_MYSQL_URL. Regenerates gaps-live.md / gaps-live.json and
# always exits 0.
probe-live:
	go run ./cmd/gap-probe-live

# CI progress gate for the live behavioral tier: fail only when the current
# live report exceeds the committed budget or has stale waivers. Full live
# parity is still `make gate-live`.
budget-live: probe-live
	go run ./cmd/gap-budget -report gaps-live.json -budget gap-live-budget.txt

# The live conformance gate: regenerate the live report AND fail if any schema
# does not survive Ptah's generate -> apply -> introspect loop. Red until that
# loop is lossless. Needs CONFORMANCE_POSTGRES_URL and/or CONFORMANCE_MYSQL_URL.
gate-live:
	go run ./cmd/gap-probe-live -gate

# Build Atlas CE from the tag pinned in atlas.version, into ./bin/atlas, so the
# differential tier compares against a known release (renovate bumps the pin).
# The Atlas CLI lives in its own nested module (ariga.io/atlas/cmd/atlas), so the
# build runs from that directory with GOWORK=off. The version is injected via
# ldflags so `atlas version` reports the release, not "- (canary)".
ATLAS_TAG := $(shell cat atlas.version)
atlas:
	@echo "building Atlas CE $(ATLAS_TAG) from source ..."
	rm -rf build/atlas-src && git clone --depth 1 --branch $(ATLAS_TAG) https://github.com/ariga/atlas build/atlas-src
	cd build/atlas-src/cmd/atlas && GOWORK=off go build \
		-ldflags "-X ariga.io/atlas/cmd/atlas/internal/cmdapi.version=$(ATLAS_TAG)" \
		-o $(CURDIR)/bin/atlas .
	@$(CURDIR)/bin/atlas version | head -1

# The differential-vs-Atlas tier: apply first-party schemas to a real Postgres,
# then compare what Atlas CE and Ptah each report about the schema. Needs
# CONFORMANCE_POSTGRES_URL and ATLAS_BIN (or `atlas` on PATH). Regenerates
# gaps-diff.md / gaps-diff.json and always exits 0.
probe-diff:
	go run ./cmd/gap-probe-diff

# CI progress gate for the differential-vs-Atlas tier: fail only when the
# current differential report exceeds the committed budget or has stale waivers.
# Full Atlas agreement is still `make gate-diff`.
budget-diff: probe-diff
	go run ./cmd/gap-budget -report gaps-diff.json -budget gap-diff-budget.txt

# The differential gate: regenerate the report AND fail while Ptah disagrees with
# Atlas CE on any CE-visible construct. Red until Ptah's introspect -> render is
# a faithful drop-in for `atlas schema inspect`.
gate-diff:
	go run ./cmd/gap-probe-diff -gate

# CI progress gate: fail only when the current report exceeds the committed
# unwaived non-OK observation budget or has stale waivers. Full parity is still
# `make gate`.
budget: probe
	go run ./cmd/gap-budget

# The conformance gate: regenerate the report AND fail if any non-OK observation
# remains. Red until Ptah covers everything Atlas expresses in the corpus.
gate:
	go run ./cmd/gap-probe -gate

build:
	go build ./...

vet:
	go vet ./...

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
