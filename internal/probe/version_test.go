package probe_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// The test binary links the ptah.run version go.mod requires, with no replace
// directive, so it measures the pin.
func TestPtahVersion_PinnedModuleReportsGoMod(t *testing.T) {
	c := qt.New(t)
	t.Setenv("PTAH_BIN", "")
	t.Setenv("PTAH_COMPAT_BIN", "")

	got := probe.PtahVersion()

	c.Assert(got, qt.Equals, probe.PinnedPtah)
}

func TestPtahVersion_ExternalBinaryOverrides(t *testing.T) {
	c := qt.New(t)
	ptahBin := t.TempDir() + "/ptah"
	compatBin := t.TempDir() + "/ptah-compat"
	ptahContents := []byte("ptah external binary")
	compatContents := []byte("ptah-compat external binary")
	c.Assert(os.WriteFile(ptahBin, ptahContents, 0o600), qt.IsNil)
	c.Assert(os.WriteFile(compatBin, compatContents, 0o600), qt.IsNil)
	t.Setenv("PTAH_BIN", ptahBin)
	t.Setenv("PTAH_COMPAT_BIN", compatBin)
	ptahHash := sha256.Sum256(ptahContents)
	compatHash := sha256.Sum256(compatContents)

	got := probe.PtahVersion()

	c.Assert(got, qt.Contains, "ptah.run ")
	c.Assert(got, qt.Contains, "external binary overrides: PTAH_BIN sha256:"+
		hex.EncodeToString(ptahHash[:]))
	c.Assert(got, qt.Contains, "PTAH_COMPAT_BIN sha256:"+
		hex.EncodeToString(compatHash[:]))
}
