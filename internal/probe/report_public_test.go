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

func TestRenderMigrateRuntimeMarkdown_EscapesMultilineDetails(t *testing.T) {
	c := qt.New(t)

	report := probe.RenderMigrateRuntimeMarkdownWithCommand([]probe.Result{
		{
			Probe:   "migrate-runtime",
			Fixture: "sqlite/per-file-txmode/matrix/misplaced-directive-is-ignored",
			Stage:   "compare",
			Outcome: probe.OK,
			Detail:  "misplaced directive\nwas ignored | safely",
		},
	}, "atlas community version v9.9.9", "ptah-version", "make probe-migrate-runtime")

	c.Assert(report, qt.Contains, "misplaced directive<br>was ignored \\| safely")
	c.Assert(report, qt.Not(qt.Contains), "misplaced directive\nwas ignored")
}

// TestRenderMigrateRuntimeMarkdown_StampsTheOracleItMeasured pins the audit
// property: the committed artifact names the binary that produced the numbers.
// The measured version and the pin are given different values on purpose --
// a header rendered from the pin would satisfy an assertion that only asked
// for a version-shaped string, which is the header this replaced.
func TestRenderMigrateRuntimeMarkdown_StampsTheOracleItMeasured(t *testing.T) {
	c := qt.New(t)

	report := probe.RenderMigrateRuntimeMarkdownWithCommand(nil,
		"atlas community version v9.9.9", "ptah-version", "make probe-migrate-runtime")

	c.Assert(report, qt.Contains, "measured against `atlas community version v9.9.9`")
	c.Assert(report, qt.Not(qt.Contains), "pinned by atlas.version")
}
