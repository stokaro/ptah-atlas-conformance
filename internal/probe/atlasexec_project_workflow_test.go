package probe_test

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const (
	versionedBasicConfig = "atlasexec/internal/e2e/testdata/versioned-basic/atlas.hcl"
	multiTenantsConfig   = "atlasexec/internal/e2e/testdata/multi-tenants/atlas.hcl"
)

func TestAtlasExecProjectWorkflowProbe_VersionedBasicHappyPath(t *testing.T) {
	c := qt.New(t)
	root := filepath.Join("..", "..", "third_party", "atlas", "upstream", "atlasexec", "internal", "e2e", "testdata", "versioned-basic")
	fixture := probe.Fixture{
		Name:  versionedBasicConfig,
		Kind:  probe.FixtureKindHCL,
		Dir:   root,
		Files: []string{filepath.Join(root, "atlas.hcl")},
	}

	results := probe.AtlasExecProjectWorkflowProbe{}.Run(fixture)

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "atlasexec-project-workflow")
	c.Check(results[0].Fixture, qt.Equals, versionedBasicConfig)
	c.Check(results[0].Stage, qt.Equals, "workflow")
	c.Check(results[0].Outcome, qt.Equals, probe.OK, qt.Commentf("%s", results[0].Detail))
	c.Check(results[0].Detail, qt.Contains, "reported one pending migration")
	c.Check(results[0].Detail, qt.Contains, "empty Applied result")
	c.Check(results[0].Issue, qt.Equals, "")
	_, sourceDatabaseErr := os.Stat(filepath.Join(root, "file.db"))
	c.Check(sourceDatabaseErr, qt.ErrorIs, os.ErrNotExist)
}

func TestAtlasExecProjectWorkflowProbe_MultiTenantsHappyPath(t *testing.T) {
	c := qt.New(t)
	root := filepath.Join("..", "..", "third_party", "atlas", "upstream", "atlasexec", "internal", "e2e", "testdata", "multi-tenants")
	fixture := probe.Fixture{
		Name:  multiTenantsConfig,
		Kind:  probe.FixtureKindHCL,
		Dir:   root,
		Files: []string{filepath.Join(root, "atlas.hcl")},
	}

	results := probe.AtlasExecProjectWorkflowProbe{}.Run(fixture)

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "atlasexec-project-workflow")
	c.Check(results[0].Fixture, qt.Equals, multiTenantsConfig)
	c.Check(results[0].Stage, qt.Equals, "workflow")
	c.Check(results[0].Outcome, qt.Equals, probe.OK, qt.Commentf("%s", results[0].Detail))
	c.Check(results[0].Detail, qt.Contains, "produced two ordered reports per apply")
	c.Check(results[0].Detail, qt.Contains, "retry left bar a no-op while retrying foo")
	c.Check(results[0].Issue, qt.Equals, "")
	_, barDatabaseErr := os.Stat(filepath.Join(root, "bar.db"))
	c.Check(barDatabaseErr, qt.ErrorIs, os.ErrNotExist)
	_, fooDatabaseErr := os.Stat(filepath.Join(root, "foo.db"))
	c.Check(fooDatabaseErr, qt.ErrorIs, os.ErrNotExist)
}

func TestAtlasExecProjectWorkflowProbe_IgnoresUnrelatedFixture(t *testing.T) {
	c := qt.New(t)

	results := probe.AtlasExecProjectWorkflowProbe{}.Run(probe.Fixture{
		Name: "schemahcl/testdata/schema.hcl",
		Kind: probe.FixtureKindHCL,
	})

	c.Assert(results, qt.IsNil)
}

func TestAtlasExecProjectConfigsUseWorkflowClassification(t *testing.T) {
	c := qt.New(t)
	fixtures := []probe.Fixture{
		{Name: multiTenantsConfig, Kind: probe.FixtureKindHCL},
		{Name: versionedBasicConfig, Kind: probe.FixtureKindHCL},
	}

	for _, fixture := range fixtures {
		c.Run(fixture.Name, func(c *qt.C) {
			c.Check(probe.AtlasHCLProbe{}.Run(fixture), qt.IsNil)
			inventory := probe.CorpusProbe{}.Run(fixture)
			c.Assert(inventory, qt.HasLen, 1)
			c.Check(inventory[0].Detail, qt.Equals, "imported Atlas project config; execution surface is measured by atlasexec-project-workflow")
		})
	}
}

func TestAtlasExecProjectWorkflowProbe_IsRegistered(t *testing.T) {
	c := qt.New(t)
	var names []string
	for _, registered := range probe.AllProbes() {
		names = append(names, registered.Name())
	}

	c.Assert(names, qt.Contains, "atlasexec-project-workflow")
}
