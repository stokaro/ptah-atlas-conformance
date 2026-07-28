package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestManagedDataWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "managed-data")
	t.Setenv("PTAH_DB_URL", "invalid://must-not-reach-the-probed-command")

	results := probe.ManagedDataWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/managed-data-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 8)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "managed-data-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah migrations generate|schema generation",
		"ptah migrations up|schema application",
		"ptah migrations data|data migration generation",
		"ptah migrations up|data application",
		"managed rows|row introspection",
		"ptah migrations data|convergence re-diff",
		"ptah migrations data|destructive gate",
		"ptah migrations down|data reversibility",
	})
}

func TestManagedDataWorkflowProbe_FailurePath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join(t.TempDir(), "missing")

	results := probe.ManagedDataWorkflowProbe{
		FixtureRoot: fixtureRoot,
		Binary:      "unused",
	}.Run(probe.Fixture{Name: "_capability/managed-data-workflow/SENTINEL"})

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "managed-data-workflow")
	c.Check(results[0].Fixture, qt.Equals, "_capability/managed-data-workflow/SENTINEL")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Detail, qt.Contains, fixtureRoot)
	c.Check(results[0].Issue, qt.Equals, "")
}

func TestManagedDataWorkflowProbe_IgnoresUnrelatedFixtures(t *testing.T) {
	c := qt.New(t)

	results := probe.ManagedDataWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"})

	c.Assert(results, qt.IsNil)
}
