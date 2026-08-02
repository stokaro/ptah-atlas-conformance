package probe_test

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestAtlasVersionMatchesPin_HappyPath(t *testing.T) {
	c := qt.New(t)

	c.Assert(probe.AtlasVersionMatchesPin("atlas community version v1.3.0", "v1.3.0"), qt.IsTrue)
}

func TestAtlasVersionMatchesPin_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name     string
		observed string
		pinned   string
	}{
		{name: "empty pin", observed: "atlas community version v1.3.0"},
		{name: "wrong version", observed: "atlas community version v1.2.0", pinned: "v1.3.0"},
		{name: "untrusted prefix", observed: "wrapper atlas community version v1.3.0", pinned: "v1.3.0"},
		{name: "trailing text", observed: "atlas community version v1.3.0 modified", pinned: "v1.3.0"},
		{name: "official binary spelling", observed: "atlas version v1.3.0", pinned: "v1.3.0"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(probe.AtlasVersionMatchesPin(test.observed, test.pinned), qt.IsFalse)
		})
	}
}

func TestRunMigrateRuntime_RejectsMismatchedAtlasVersion(t *testing.T) {
	c := qt.New(t)
	t.Chdir(filepath.Join("..", ".."))
	bin := filepath.Join(t.TempDir(), "atlas")
	c.Assert(os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'atlas community version v0.0.0\\n'\n"), 0o600), qt.IsNil)
	c.Assert(os.Chmod(bin, 0o700), qt.IsNil)
	t.Setenv("ATLAS_BIN", bin)

	got := probe.RunMigrateRuntime()

	c.Assert(got, qt.DeepEquals, []probe.Result{{
		Probe:   "migrate-runtime",
		Fixture: "atlas-runtime-oracle",
		Stage:   "atlas-version",
		Outcome: probe.Fail,
		Detail:  `Atlas binary reports "atlas community version v0.0.0", want atlas.version "v1.3.0"`,
	}})
}
