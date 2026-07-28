package probe_test

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestRenderMarkdown_SeparatesAtlasAndCapabilityFixtures(t *testing.T) {
	c := qt.New(t)

	report := probe.RenderMarkdown([]probe.Result{
		{
			Probe:   "corpus-inventory",
			Fixture: "sql/migrate/testdata/example",
			Stage:   "import",
			Outcome: probe.OK,
			Detail:  "imported SQL directory",
		},
		{
			Probe:   "corpus-inventory",
			Fixture: "_capability/example/SENTINEL",
			Stage:   "capability",
			Outcome: probe.OK,
			Detail:  "first-party capability sentinel",
		},
	}, &probe.Waivers{}, "atlas-sha", "ptah-version")

	c.Assert(report, qt.Contains, "**1 imported Atlas fixture(s)**")
	c.Assert(report, qt.Contains, "**1 first-party capability sentinel(s)**")
	c.Assert(report, qt.Not(qt.Contains), "**2 imported Atlas fixture(s)**")
}
