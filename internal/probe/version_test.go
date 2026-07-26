package probe_test

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestPtahVersion_LinkedModuleOnly(t *testing.T) {
	c := qt.New(t)
	t.Setenv("PTAH_BIN", "")
	t.Setenv("PTAH_COMPAT_BIN", "")

	got := probe.PtahVersion()

	c.Assert(got, qt.Contains, "github.com/stokaro/ptah ")
	c.Assert(got, qt.Not(qt.Contains), "external binary overrides")
}

func TestPtahVersion_ExternalBinaryOverrides(t *testing.T) {
	c := qt.New(t)
	t.Setenv("PTAH_BIN", " /tmp/local ptah ")
	t.Setenv("PTAH_COMPAT_BIN", "/tmp/local-atlas")

	got := probe.PtahVersion()

	c.Assert(got, qt.Contains, "github.com/stokaro/ptah ")
	c.Assert(got, qt.Contains,
		`external binary overrides: PTAH_BIN="/tmp/local ptah", PTAH_COMPAT_BIN="/tmp/local-atlas"`)
}
