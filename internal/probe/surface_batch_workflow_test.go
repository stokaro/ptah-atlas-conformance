package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// assertWorkflowContours runs the shared all-OK assertion for one Atlas
// surface-batch workflow probe: every result belongs to probeName, is OK with
// no issue tag, and the fixture|stage contour matches exactly.
func assertWorkflowContours(c *qt.C, probeName string, results []probe.Result, wantContours []string) {
	c.Helper()
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, probeName)
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s/%s: %s", result.Fixture, result.Stage, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, wantContours)
}

func TestDesiredStateWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "desired-state")

	results := probe.DesiredStateWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/desired-state-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 8)
	assertWorkflowContours(c, "desired-state-workflow", results, []string{
		"atlas schema diff|database-url --from source",
		"atlas schema apply|database-url --to source",
		"atlas schema apply|migration-dir source replay",
		"atlas schema apply|migration-dir source without dev database",
		"atlas schema apply|env:// source resolution",
		"atlas migrate diff|database-url --to source converges",
		"atlas migrate diff|env://url source with project defaults",
		"atlas migrate diff|desired and dev path alias rejected",
	})
}

func TestApplySimulationWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "apply-simulation")
	// The probe pins its own hermetic scripted $EDITOR for the failing
	// simulation; an inherited interactive editor must never be reachable.
	t.Setenv("EDITOR", "false")
	t.Setenv("VISUAL", "false")

	results := probe.ApplySimulationWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/apply-simulation-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 4)
	assertWorkflowContours(c, "apply-simulation-workflow", results, []string{
		"atlas schema apply --lock-timeout|lockless dialect note",
		"atlas schema apply --dev-url|plan simulation success",
		"atlas schema apply --dev-url|failed simulation refuses the target",
		"atlas schema apply --dev-url|dev database must differ from target",
	})
}

func TestSchemaScopeWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "schema-scope")

	results := probe.SchemaScopeWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/schema-scope-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 4)
	assertWorkflowContours(c, "schema-scope-workflow", results, []string{
		"atlas schema apply --include|scoped apply leaves out-of-scope objects untouched",
		"atlas schema apply --include|repeated include values union",
		"atlas schema apply --include|cross-scope foreign key refusal",
		"atlas schema diff --include|malformed selector fails before the dev database",
	})
}

func TestInspectSourceWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "inspect-source")

	results := probe.InspectSourceWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/inspect-source-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 4)
	assertWorkflowContours(c, "inspect-source-workflow", results, []string{
		"atlas schema inspect|local schema file over dev database",
		"atlas schema inspect|split export writes a deterministic tree",
		"atlas schema inspect|written tree reloads to the same schema",
		"atlas schema inspect --exclude|resource and field selectors",
	})
}

func TestQualifierTxModeWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "qualifier-txmode")

	results := probe.QualifierTxModeWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/qualifier-txmode-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 5)
	assertWorkflowContours(c, "qualifier-txmode-workflow", results, []string{
		"atlas migrate diff --qualifier|invalid qualifier fails before the dev database",
		"atlas migrate diff --qualifier|qualified artifacts are scoped to qualified dialects",
		"atlas migrate diff|concurrent-index policy on sqlite plans one transactional file",
		"atlas migrate apply|concurrent-index artifact replays",
		"atlas migrate apply|txmode-none directive executes outside a transaction",
	})
}

func TestSurfaceBatchWorkflowProbes_IgnoreOtherFixtures(t *testing.T) {
	c := qt.New(t)
	other := probe.Fixture{Name: "_capability/checkpoint-workflow/SENTINEL"}

	c.Check(probe.DesiredStateWorkflowProbe{}.Run(other), qt.IsNil)
	c.Check(probe.ApplySimulationWorkflowProbe{}.Run(other), qt.IsNil)
	c.Check(probe.SchemaScopeWorkflowProbe{}.Run(other), qt.IsNil)
	c.Check(probe.InspectSourceWorkflowProbe{}.Run(other), qt.IsNil)
	c.Check(probe.QualifierTxModeWorkflowProbe{}.Run(other), qt.IsNil)
}
