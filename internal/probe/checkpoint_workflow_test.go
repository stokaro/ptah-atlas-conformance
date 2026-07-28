package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestCheckpointWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "checkpoint")
	t.Setenv("PTAH_DB_URL", "invalid://must-not-reach-the-probed-command")

	results := probe.CheckpointWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/checkpoint-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 12)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "checkpoint-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah migrations up|full history application",
		"ptah migrations checkpoint|checkpoint creation",
		"ptah migrations validate|checkpoint integrity",
		"ptah migrations up|fresh bootstrap",
		"SQLite schema facts|bootstrap schema equivalence",
		"ptah migrations status|status convergence",
		"ptah migrations up|already-migrated no-op",
		"ptah migrations validate|tamper detection",
		"ptah migrations down|rollback boundary guard",
		"ptah migrations down|rollback to zero",
		"ptah migrations up|post-checkpoint continuation",
		"SQLite schema facts|post-checkpoint schema equivalence",
	})
}

func TestCheckpointWorkflowProbe_FailurePath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join(t.TempDir(), "missing")

	results := probe.CheckpointWorkflowProbe{
		FixtureRoot: fixtureRoot,
		Binary:      "unused",
	}.Run(probe.Fixture{Name: "_capability/checkpoint-workflow/SENTINEL"})

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "checkpoint-workflow")
	c.Check(results[0].Fixture, qt.Equals, "_capability/checkpoint-workflow/SENTINEL")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Detail, qt.Contains, fixtureRoot)
	c.Check(results[0].Issue, qt.Equals, "")
}

func TestCheckpointWorkflowProbe_IgnoresUnrelatedFixtures(t *testing.T) {
	c := qt.New(t)

	results := probe.CheckpointWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"})

	c.Assert(results, qt.IsNil)
}
