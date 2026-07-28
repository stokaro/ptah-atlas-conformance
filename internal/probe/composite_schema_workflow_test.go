package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestCompositeSchemaWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "composite-schema")
	t.Setenv("PTAH_DB_URL", "invalid://must-not-reach-the-probed-command")

	results := probe.CompositeSchemaWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/composite-schema/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 8)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "composite-schema-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s: %s", result.Fixture, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"mixed render|render",
		"hand-merged equivalence|render equivalence",
		"conflicting sources|conflict detection",
		"ptah migrations generate|migration generation",
		"hand-merged migration equivalence|migration equivalence",
		"ptah migrations up|migration application",
		"SQLite schema facts|live schema facts",
		"live comparison controls|live end state",
	})
}

func TestCompositeSchemaWorkflowProbe_FailurePath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join(t.TempDir(), "missing")

	results := probe.CompositeSchemaWorkflowProbe{
		FixtureRoot: fixtureRoot,
		Binary:      "unused",
	}.Run(probe.Fixture{Name: "_capability/composite-schema/SENTINEL"})

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "composite-schema-workflow")
	c.Check(results[0].Fixture, qt.Equals, "_capability/composite-schema/SENTINEL")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Detail, qt.Contains, fixtureRoot)
	c.Check(results[0].Issue, qt.Equals, "")
}

func TestCompositeSchemaWorkflowProbe_IgnoresUnrelatedFixtures(t *testing.T) {
	c := qt.New(t)

	results := probe.CompositeSchemaWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"})

	c.Assert(results, qt.IsNil)
}
