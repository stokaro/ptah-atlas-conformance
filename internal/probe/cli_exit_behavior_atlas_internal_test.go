//go:build atlasoracle

package probe

// White-box testing required: the Atlas oracle must execute the same internal
// catalog used by the Ptah probes so static expectations cannot drift from the
// pinned Atlas CE binary.

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestCLIExitCatalogMatchesAtlasCE(t *testing.T) {
	c := qt.New(t)
	// Atlas's update notifier is network- and time-dependent metadata, not
	// command output. Atlas's own execution harness disables it as well.
	t.Setenv("ATLAS_NO_UPDATE_NOTIFIER", "true")

	atlasBin := os.Getenv("ATLAS_BIN")
	c.Assert(atlasBin, qt.Not(qt.Equals), "")
	c.Assert(filepath.IsAbs(atlasBin), qt.IsTrue)

	results := runCLIExitCatalog(atlasBin, cliExitSurface{label: "atlas-oracle"})
	c.Assert(results, qt.HasLen, len(cliExitCatalog))
	for _, result := range results {
		c.Check(
			result.Outcome,
			qt.Equals,
			OK,
			qt.Commentf("row %q: %s", result.Fixture, result.Detail),
		)
	}
}
