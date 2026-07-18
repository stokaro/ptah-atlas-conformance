.PHONY: probe budget gate probe-live gate-live verify build vet clean

# Regenerate the gap report from the vendored corpus. Always exits 0 — use this
# to refresh gaps.md / gaps.json.
probe:
	go run ./cmd/gap-probe

# The live behavioral tier: apply first-party schemas to a real database,
# introspect them back, and diff. Kept separate from the offline probes so the
# offline report stays deterministic and DB-free. Needs CONFORMANCE_DB_URL.
# Regenerates gaps-live.md / gaps-live.json and always exits 0.
probe-live:
	go run ./cmd/gap-probe-live

# The live conformance gate: regenerate the live report AND fail if any schema
# does not survive Ptah's generate -> apply -> introspect loop. Red until that
# loop is lossless. Needs CONFORMANCE_DB_URL.
gate-live:
	go run ./cmd/gap-probe-live -gate

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
