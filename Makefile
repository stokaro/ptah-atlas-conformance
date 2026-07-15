.PHONY: probe gate verify build vet clean

# Regenerate the gap report from the vendored corpus. Always exits 0 — use this
# to refresh gaps.md / gaps.json.
probe:
	go run ./cmd/gap-probe

# The conformance gate: regenerate the report AND fail if any unwaived gap
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
