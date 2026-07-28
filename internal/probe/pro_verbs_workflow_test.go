package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestProTestWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "pro-test")
	t.Setenv("PTAH_DEV_URL", "invalid://must-not-reach-the-probed-command")

	results := probe.ProTestWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/pro-test-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 4)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "pro-test-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah atlas migrate test|migration tests pass",
		"ptah atlas migrate test|migration test failure exit contract",
		"ptah atlas schema test|schema tests pass",
		"ptah atlas schema test|schema test failure exit contract",
	})
}

func TestProMaintWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "pro-maint")
	// The probe must pin its own hermetic editor; an inherited interactive
	// editor would hang or corrupt the measured edit.
	t.Setenv("EDITOR", "false")
	t.Setenv("VISUAL", "false")

	results := probe.ProMaintWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/pro-maint-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 3)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "pro-maint-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah atlas migrate edit|editor round-trip",
		"ptah atlas migrate rebase|rebase to end of history",
		"ptah atlas migrate rm|remove migration",
	})
}

func TestProPlanWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "pro-plan")

	results := probe.ProPlanWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/pro-plan-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 3)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "pro-plan-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah atlas schema plan|plan creation",
		"ptah atlas schema apply|plan application",
		"ptah atlas schema apply|stale plan refusal",
	})
}

func TestProDownWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "pro-down")
	t.Setenv("PTAH_REVISION_FORMAT", "must-not-reach-the-probed-command")

	results := probe.ProDownWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/pro-down-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 2)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "pro-down-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"ptah atlas migrate apply|atlas-format application",
		"ptah atlas migrate down|bare rollback",
	})
}

func TestProVerbWorkflowProbes_FailurePath(t *testing.T) {
	c := qt.New(t)
	missing := filepath.Join(t.TempDir(), "missing")

	tests := []struct {
		name     string
		results  []probe.Result
		probe    string
		sentinel string
	}{
		{
			name: "pro-test",
			results: probe.ProTestWorkflowProbe{FixtureRoot: missing, Binary: "unused"}.Run(
				probe.Fixture{Name: "_capability/pro-test-workflow/SENTINEL"}),
			probe:    "pro-test-workflow",
			sentinel: "_capability/pro-test-workflow/SENTINEL",
		},
		{
			name: "pro-maint",
			results: probe.ProMaintWorkflowProbe{FixtureRoot: missing, Binary: "unused"}.Run(
				probe.Fixture{Name: "_capability/pro-maint-workflow/SENTINEL"}),
			probe:    "pro-maint-workflow",
			sentinel: "_capability/pro-maint-workflow/SENTINEL",
		},
		{
			name: "pro-plan",
			results: probe.ProPlanWorkflowProbe{FixtureRoot: missing, Binary: "unused"}.Run(
				probe.Fixture{Name: "_capability/pro-plan-workflow/SENTINEL"}),
			probe:    "pro-plan-workflow",
			sentinel: "_capability/pro-plan-workflow/SENTINEL",
		},
		{
			name: "pro-down",
			results: probe.ProDownWorkflowProbe{FixtureRoot: missing, Binary: "unused"}.Run(
				probe.Fixture{Name: "_capability/pro-down-workflow/SENTINEL"}),
			probe:    "pro-down-workflow",
			sentinel: "_capability/pro-down-workflow/SENTINEL",
		},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			c.Assert(tt.results, qt.HasLen, 1)
			c.Check(tt.results[0].Probe, qt.Equals, tt.probe)
			c.Check(tt.results[0].Fixture, qt.Equals, tt.sentinel)
			c.Check(tt.results[0].Stage, qt.Equals, "fixture setup")
			c.Check(tt.results[0].Outcome, qt.Equals, probe.Fail)
			c.Check(tt.results[0].Detail, qt.Contains, "stat fixture root")
			c.Check(tt.results[0].Detail, qt.Contains, missing)
			c.Check(tt.results[0].Issue, qt.Equals, "")
		})
	}
}

func TestProVerbWorkflowProbes_IgnoreUnrelatedFixtures(t *testing.T) {
	c := qt.New(t)

	c.Assert(probe.ProTestWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"}), qt.IsNil)
	c.Assert(probe.ProMaintWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"}), qt.IsNil)
	c.Assert(probe.ProPlanWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"}), qt.IsNil)
	c.Assert(probe.ProDownWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"}), qt.IsNil)
}
