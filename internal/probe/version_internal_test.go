package probe

// White-box testing required: whether a binary measures the pinned Ptah is
// decided from its build record, and a test binary's own record depends on the
// toolchain -- under Go 1.26 it does not carry the ptah.run version -- so the
// decision is driven with synthetic records through the unexported
// ptahVersionFrom.

import (
	"runtime/debug"
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestPtahVersionFrom(t *testing.T) {
	pinned := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "golang.org/x/sync", Version: "v0.23.0"},
		{Path: "ptah.run", Version: "v0.9.0"},
	}}
	tests := []struct {
		name      string
		info      *debug.BuildInfo
		overrides []string
		want      string
	}{
		{
			name: "pinned module",
			info: pinned,
			want: PinnedPtah,
		},
		{
			name:      "pinned module with an external binary",
			info:      pinned,
			overrides: []string{"PTAH_BIN sha256:abc"},
			want:      "ptah.run v0.9.0; external binary overrides: PTAH_BIN sha256:abc",
		},
		{
			name: "replaced by another version",
			info: &debug.BuildInfo{Deps: []*debug.Module{{
				Path:    "ptah.run",
				Version: "v0.9.0",
				Replace: &debug.Module{Path: "ptah.run", Version: "v0.9.1-0.20260926000000-abcdef012345"},
			}}},
			want: "ptah.run v0.9.1-0.20260926000000-abcdef012345",
		},
		{
			name: "replaced by a local checkout",
			info: &debug.BuildInfo{Deps: []*debug.Module{{
				Path:    "ptah.run",
				Version: "v0.9.0",
				Replace: &debug.Module{Path: "../ptah"},
			}}},
			want: "ptah.run (version unknown)",
		},
		{
			name: "module recorded without a version",
			info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "ptah.run"}}},
			want: "ptah.run (version unknown)",
		},
		{
			name: "module not linked",
			info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "golang.org/x/sync", Version: "v0.23.0"}}},
			want: "ptah.run (version unknown)",
		},
		{
			name: "no build record",
			want: "ptah.run (version unknown)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(ptahVersionFrom(tt.info, tt.overrides), qt.Equals, tt.want)
		})
	}
}
