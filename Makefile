.PHONY: probe budget gate probe-live budget-live gate-live probe-diff budget-diff gate-diff probe-migrate-runtime check-budget-migrate-runtime budget-migrate-runtime gate-migrate-runtime probe-orm-providers budget-orm-providers gate-orm-providers probe-cli-surface budget-cli-surface gate-cli-surface probe-ce-gating budget-ce-gating gate-ce-gating probe-docs-surface budget-docs-surface gate-docs-surface verify-cli-exit-oracle atlas verify test build vet clean

GO ?= go
GO_OFF := GOWORK=off $(GO)

# Regenerate the gap report from the vendored corpus. Always exits 0 — use this
# to refresh gaps.md / gaps.json.
probe:
	$(GO_OFF) run ./cmd/gap-probe

# The live behavioral tier: apply first-party schemas to a real database,
# introspect them back, and diff. Kept separate from the offline probes so the
# offline report stays deterministic and DB-free. Networked dialects run when
# CONFORMANCE_POSTGRES_URL, CONFORMANCE_MYSQL_URL, and/or
# CONFORMANCE_MARIADB_URL are configured. SQLite always runs against
# CONFORMANCE_SQLITE_URL or a fresh local temp database. Regenerates
# gaps-live.md / gaps-live.json and always exits 0.
probe-live:
	$(GO_OFF) run ./cmd/gap-probe-live

# CI progress gate for the live behavioral tier: fail only when the current
# live report exceeds the committed budget or has stale waivers. Full live
# corpus parity is still `make gate-live`.
budget-live: probe-live
	$(GO_OFF) run ./cmd/gap-budget -report gaps-live.json -budget gap-live-budget.txt

# The live conformance gate: regenerate the live report AND fail if any schema
# does not survive Ptah's generate -> apply -> introspect loop. Needs
# any configured CONFORMANCE_POSTGRES_URL, CONFORMANCE_MYSQL_URL, and
# CONFORMANCE_MARIADB_URL targets, plus SQLite against CONFORMANCE_SQLITE_URL or
# a fresh local temp database.
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

# The differential-vs-Atlas tier: apply first-party schemas to real databases,
# then compare what Atlas CE and Ptah each report about the schema. Networked
# dialects run when CONFORMANCE_POSTGRES_URL and/or CONFORMANCE_MYSQL_URL are
# configured; SQLite always runs against CONFORMANCE_SQLITE_URL or a fresh local
# temp database. MySQL may also set CONFORMANCE_MYSQL_ATLAS_URL when Atlas needs
# a different URL spelling than Ptah's Go-driver URL. Needs ATLAS_BIN (or
# `atlas` on PATH). Regenerates gaps-diff.md / gaps-diff.json and always exits 0.
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

# The Atlas migrate runtime tier: run selected Atlas-form `migrate ...`
# workflows on the ptah-compat binary against real local databases and inspect
# the resulting schema and Atlas revision rows. Kept separate from offline
# txtar simulation so runtime parity cannot be hidden by fixture parsing
# success. The Goose checksum differential requires ATLAS_BIN (or `atlas` on
# PATH; `make atlas` builds ./bin/atlas from the pinned tag). Regenerates
# gaps-migrate-runtime.md / gaps-migrate-runtime.json and always exits 0.
probe-migrate-runtime:
	$(GO_OFF) run ./cmd/gap-probe-migrate-runtime

# Check an already generated runtime report without rerunning stateful live
# database probes. CI uses this after its explicit freshness check.
check-budget-migrate-runtime:
	$(GO_OFF) run ./cmd/gap-budget -report gaps-migrate-runtime.json -budget gap-migrate-runtime-budget.txt

# Developer-facing progress gate: regenerate the runtime report, then fail only
# when it exceeds the committed budget. Full migrate runtime parity is still
# `make gate-migrate-runtime`.
budget-migrate-runtime: probe-migrate-runtime check-budget-migrate-runtime

# The migrate runtime conformance gate: regenerate the report AND fail if any
# supported runtime check disagrees with Atlas-compatible semantics.
gate-migrate-runtime:
	$(GO_OFF) run ./cmd/gap-probe-migrate-runtime -gate

# The external ORM provider tier installs pinned GORM and SQLAlchemy provider
# toolchains in temporary isolated environments, validates their direct output,
# and sends the same commands through Ptah's external-schema CLI contract.
# Regenerates gaps-orm-providers.md / gaps-orm-providers.json and always exits 0.
probe-orm-providers:
	$(GO_OFF) run ./cmd/gap-probe-orm-providers

# CI progress gate for provider integration: fail only when the current report
# exceeds the committed budget. Full provider conformance remains a separate
# `make gate-orm-providers` signal.
budget-orm-providers: probe-orm-providers
	$(GO_OFF) run ./cmd/gap-budget -report gaps-orm-providers.json -budget gap-orm-providers-budget.txt

# The full ORM provider gate: regenerate the report AND fail on any provider
# setup, execution, output, or Ptah behavior mismatch.
gate-orm-providers:
	$(GO_OFF) run ./cmd/gap-probe-orm-providers -gate

# The CLI surface tier: build/read the pinned Atlas CE binary, compare its
# command help/usage/flag inventory to the ptah-compat binary named `atlas`,
# and separately require the public documented Pro-only flags. Regenerates
# cli-surface.md / cli-surface.json and always exits 0.
probe-cli-surface:
	$(GO_OFF) run ./cmd/cli-surface-probe

# CI progress gate for the CLI surface tier: fail only when the current
# CLI-surface report exceeds the committed budget. Full help/flag parity is
# still `make gate-cli-surface`.
budget-cli-surface: probe-cli-surface
	$(GO_OFF) run ./cmd/gap-budget -report cli-surface.json -budget cli-surface-budget.txt

# The full CLI surface gate: fail while any Atlas CE OSS command, usage string,
# CE long flag, or public documented Pro-only flag is missing from ptah-compat.
gate-cli-surface:
	$(GO_OFF) run ./cmd/cli-surface-probe -gate

# The CE gating tier: execute the pinned Atlas CE binary, logged out, through
# the fixed capability scenarios Ptah's feature matrix asserts about the CE
# column, and classify each observed outcome (works / community-abort / absent /
# unregistered-command / unknown-flag / named-error / silent-unenforced).
# Every scenario runs under a scratch HOME so a developer's real Atlas login
# can never leak in. SQLite only, no external databases. Needs ATLAS_BIN or
# `atlas` on PATH; `make atlas` builds ./bin/atlas from the pinned tag.
# Regenerates ce-gating.md /
# ce-gating.json and always exits 0.
probe-ce-gating:
	$(GO_OFF) run ./cmd/gap-probe-ce-gating

# CI progress gate for the CE gating tier: fail when the current report
# exceeds the committed budget or has stale waivers. The budget is zero —
# every scenario must match the measured Atlas CE gating baseline, so an
# atlas.version bump that changes gating goes red here.
budget-ce-gating: probe-ce-gating
	$(GO_OFF) run ./cmd/gap-budget -report ce-gating.json -budget ce-gating-budget.txt

# The full CE gating gate: regenerate the report AND fail while any scenario
# diverges from the measured Atlas CE gating baseline.
gate-ce-gating:
	$(GO_OFF) run ./cmd/gap-probe-ce-gating -gate

# The docs surface tier: index every atlasgo.io documentation page against the
# committed triage registry (docs-surface-registry.json). The universe defaults
# to the committed docs-surface-snapshot.txt so the run is offline and
# deterministic; FETCH=1 re-fetches the live https://atlasgo.io/sitemap.xml and
# rewrites the snapshot so docs drift shows up as a git diff (the weekly
# conformance-docs-surface workflow does this). Regenerates docs-surface.md /
# docs-surface.json and always exits 0.
probe-docs-surface:
	$(GO_OFF) run ./cmd/docs-surface-probe $(if $(filter 1,$(FETCH)),-fetch)

# CI progress gate for the docs surface tier: fail only when the current
# docs-surface report exceeds the committed budget or has stale waivers. Full
# docs triage is still `make gate-docs-surface`.
budget-docs-surface: probe-docs-surface
	$(GO_OFF) run ./cmd/gap-budget -report docs-surface.json -budget docs-surface-budget.txt

# The full docs surface gate: fail while any atlasgo.io docs page is untriaged,
# missing from the registry, or vanished from the sitemap universe. Red until
# the full documentation surface carries an explicit Ptah stance.
gate-docs-surface:
	$(GO_OFF) run ./cmd/docs-surface-probe -gate

# Verify that the static process-level exit/output expectations still match the
# pinned Atlas CE binary. ATLAS_BIN is required and normally points to bin/atlas.
verify-cli-exit-oracle:
	ATLAS_BIN="$(abspath $(ATLAS_BIN))" $(GO_OFF) test -tags atlasoracle ./internal/probe -run '^TestCLIExitCatalogMatchesAtlasCE$$'

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

test:
	$(GO_OFF) test ./...

vet:
	$(GO_OFF) vet ./...

# Guard the one-way boundary: this repo may depend on ptah, but the Apache-2.0
# fixtures must stay confined to third_party/. Fails if an Apache header leaks
# into the harness source.
verify: test build vet
	@echo "checking no Apache-licensed material outside third_party/ ..."
	@! grep -rIl "Apache License" --include='*.go' . | grep -v '/third_party/' || \
		{ echo "Apache-licensed material found outside third_party/"; exit 1; }
	@echo "ok"

clean:
	rm -f gaps.md gaps.json gaps-orm-providers.md gaps-orm-providers.json
