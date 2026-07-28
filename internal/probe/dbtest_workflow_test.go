package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestDBTestWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "dbtest")
	t.Setenv("PTAH_DB_URL", "invalid://must-not-reach-the-probed-command")

	results := probe.DBTestWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/dbtest-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 13)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "dbtest-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s: %s", result.Fixture, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah migrations test/text|migration execution",
		"ptah migrations test/json|JSON report",
		"ptah migrations test/html|HTML report",
		"ptah schema test/text|schema execution",
		"ptah schema test/json|JSON report",
		"ptah schema test/html|HTML report",
		"ptah migrations test/isolation|ephemeral isolation",
		"ptah schema test/isolation|ephemeral isolation",
		"ptah migrations test/assertion failure|assertion exit contract",
		"ptah migrations test/setup failure|setup exit contract",
		"ptah schema test/assertion failure|assertion exit contract",
		"ptah schema test/setup failure|setup exit contract",
		"ptah schema test/invalid migration step|step validation",
	})
}

func TestDBTestWorkflowProbe_FailurePath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join(t.TempDir(), "missing")

	results := probe.DBTestWorkflowProbe{
		FixtureRoot: fixtureRoot,
		Binary:      "unused",
	}.Run(probe.Fixture{Name: "_capability/dbtest-workflow/SENTINEL"})

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "dbtest-workflow")
	c.Check(results[0].Fixture, qt.Equals, "_capability/dbtest-workflow/SENTINEL")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Detail, qt.Contains, fixtureRoot)
	c.Check(results[0].Issue, qt.Equals, "")
}

func TestDBTestWorkflowProbe_IgnoresUnrelatedFixtures(t *testing.T) {
	c := qt.New(t)

	results := probe.DBTestWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"})

	c.Assert(results, qt.IsNil)
}
