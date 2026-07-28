package probe_test

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestExternalSchemaWorkflowProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "external-schema")
	t.Setenv("PTAH_DB_URL", "invalid://must-not-reach-the-probed-command")

	results := probe.ExternalSchemaWorkflowProbe{FixtureRoot: fixtureRoot}.Run(probe.Fixture{
		Name: "_capability/external-schema-workflow/SENTINEL",
	})

	c.Assert(results, qt.HasLen, 20)
	gotContours := make([]string, 0, len(results))
	for _, result := range results {
		gotContours = append(gotContours, result.Fixture+"|"+result.Stage)
		c.Check(result.Probe, qt.Equals, "external-schema-workflow")
		c.Check(result.Outcome, qt.Equals, probe.OK, qt.Commentf("%s: %s", result.Fixture, result.Detail))
		c.Check(result.Issue, qt.Equals, "")
	}
	c.Assert(gotContours, qt.DeepEquals, []string{
		"static SQL schema|offline render",
		"external sql schema|explicit command render",
		"external hcl schema|explicit command render",
		"external yaml schema|explicit command render",
		"schema render config trust gate|config trust denial",
		"schema compare config trust gate|config trust denial",
		"schema drift config trust gate|config trust denial",
		"migrations plan config trust gate|config trust denial",
		"migrations generate config trust gate|config trust denial",
		"configured external schema|allowed config render",
		"external schema versus empty database|initial compare",
		"external schema drift from empty database|initial drift",
		"external schema migration plan|initial plan",
		"external schema migration generation|migration generation",
		"external schema migration application|migration application",
		"SQLite external schema facts|live schema facts",
		"external schema compare convergence|converged compare",
		"external schema drift convergence|converged drift",
		"external schema plan convergence|converged plan",
		"external schema generate convergence|converged generate",
	})
}

func TestExternalSchemaWorkflowProbe_FailurePath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join(t.TempDir(), "missing")

	results := probe.ExternalSchemaWorkflowProbe{
		FixtureRoot: fixtureRoot,
		Binary:      "unused",
	}.Run(probe.Fixture{Name: "_capability/external-schema-workflow/SENTINEL"})

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "external-schema-workflow")
	c.Check(results[0].Fixture, qt.Equals, "_capability/external-schema-workflow/SENTINEL")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Detail, qt.Contains, fixtureRoot)
	c.Check(results[0].Issue, qt.Equals, "")
}

func TestExternalSchemaWorkflowProbe_IgnoresUnrelatedFixtures(t *testing.T) {
	c := qt.New(t)

	results := probe.ExternalSchemaWorkflowProbe{}.Run(probe.Fixture{Name: "unrelated"})

	c.Assert(results, qt.IsNil)
}

func TestExternalSchemaWorkflowProbe_BrokenExpectedRenderTurnsRed(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join("..", "..", "testdata", "workflows", "external-schema")
	brokenRoot := filepath.Join(t.TempDir(), "external-schema")
	c.Assert(os.CopyFS(brokenRoot, os.DirFS(fixtureRoot)), qt.IsNil)
	c.Assert(
		os.WriteFile(filepath.Join(brokenRoot, "expected.sqlite.sql"), []byte("-- deliberately wrong\n"), 0o600),
		qt.IsNil,
	)

	results := probe.ExternalSchemaWorkflowProbe{FixtureRoot: brokenRoot}.Run(probe.Fixture{
		Name: "_capability/external-schema-workflow/SENTINEL",
	})

	c.Assert(results, qt.IsNotNil)
	c.Check(results[0].Probe, qt.Equals, "external-schema-workflow")
	c.Check(results[0].Fixture, qt.Equals, "static SQL schema")
	c.Check(results[0].Stage, qt.Equals, "offline render")
	c.Check(results[0].Outcome, qt.Equals, probe.Gap)
	c.Check(results[0].Detail, qt.Equals, "rendered SQL differs from the expected SQLite snapshot")
	c.Check(results[0].Issue, qt.Equals, "stokaro/ptah#669")
}
