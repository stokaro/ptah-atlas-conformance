package probe_test

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// ptahStampRenderers drives every report generator that stamps the Ptah it
// measured, so a generator that formats its own stamp line shows here.
var ptahStampRenderers = []struct {
	name   string
	render func(ptahVersion string) string
}{
	{"offline", func(v string) string {
		return probe.RenderMarkdown(nil, &probe.Waivers{}, "atlas-sha", v)
	}},
	{"live", func(v string) string {
		return probe.RenderLiveMarkdownWithCommand(nil, v, "make probe-live")
	}},
	{"differential", func(v string) string {
		return probe.RenderDifferentialMarkdownWithCommand(nil, "atlas-version", v, "make probe-diff")
	}},
	{"migrate-runtime", func(v string) string {
		return probe.RenderMigrateRuntimeMarkdownWithCommand(nil, "atlas-version", v, "make probe-migrate-runtime")
	}},
	{"third-party", func(v string) string {
		return probe.RenderThirdPartyMarkdownWithCommand(nil, "atlas-version", v, "make probe-third-party")
	}},
	{"cli-surface", func(v string) string {
		return probe.RenderCLISurfaceMarkdown(nil, &probe.Waivers{}, probe.CLISurfaceInventory{}, v, "make probe-cli-surface")
	}},
	{"docs-surface", func(v string) string {
		return probe.RenderDocsSurfaceMarkdown(nil, &probe.Waivers{}, nil, nil, v, "make probe-docs-surface")
	}},
	{"orm-providers", func(v string) string {
		return probe.RenderORMProviderMarkdown(nil, probe.SQLAlchemyPins{}, v, "go run ./cmd/gap-probe-orm-providers")
	}},
}

// The pinned version is not spelled out, so a ptah.run bump that moves no
// result leaves every report byte-identical.
func TestReportStamp_PinnedPtahNamesGoModNotAVersion(t *testing.T) {
	for _, r := range ptahStampRenderers {
		t.Run(r.name, func(t *testing.T) {
			c := qt.New(t)

			report := r.render(probe.PinnedPtah)

			c.Check(report, qt.Contains, "\n- Ptah at the `ptah.run` version `go.mod` requires\n")
			c.Check(report, qt.Not(qt.Contains), "Ptah at `")
		})
	}
}

// A departure from the pin is spelled out, so a report generated against a
// substituted Ptah differs from the pinned regeneration.
func TestReportStamp_DepartureFromThePinIsSpelledOut(t *testing.T) {
	const departure = "ptah.run v9.9.9; external binary overrides: PTAH_BIN sha256:abc"
	for _, r := range ptahStampRenderers {
		t.Run(r.name, func(t *testing.T) {
			c := qt.New(t)

			report := r.render(departure)

			c.Check(report, qt.Contains, "\n- Ptah at `"+departure+"`\n")
			c.Check(report, qt.Not(qt.Contains), "version `go.mod` requires")
		})
	}
}
