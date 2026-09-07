package probe_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// txtarProbeName is the probe whose waivers this file refuses. It is spelled
// out rather than imported because the constant is unexported, and the point is
// the string a waiver line would carry.
const txtarProbeName = "txtar-script"

// TestWaivers_TheTxtarCorpusIsNotBlanketWaived refuses the one waiver shape
// that hides more than it declares.
//
// Every failure and every unsupported command of a txtar fixture is stamped with
// the single stage "script-runtime" (txtar_script.go). A waiver key is
// (probe, fixture, stage), so ONE line silences the whole fixture -- including
// gaps that have nothing to do with the difference its reason names. That is
// the opposite of what a waiver is for: it is supposed to record a tracked,
// specific divergence, and here it records "stop looking at this file".
//
// The alternative a real divergence needs is a declaration in code -- the
// expected fragment and the Ptah-side text it stands in for, checked per
// comparison -- which stokaro/ptah-atlas-conformance#290 specifies. That
// mechanism is deliberately not built yet: with no divergence to declare it
// would land as an empty table, and `unused` counts a test as a use, so it
// would be green everywhere while being in effect nowhere. Build it with the
// first real divergence.
//
// Until then this is the half that has a caller. It is a zero assertion, so it
// is worth nothing without its inverse: adding a `txtar-script … script-runtime`
// line to waivers.txt must redden it, and the sibling test below is that proof
// run against a temporary file.
func TestWaivers_TheTxtarCorpusIsNotBlanketWaived(t *testing.T) {
	c := qt.New(t)
	repoRoot := filepath.Join("..", "..")

	body, err := os.ReadFile(filepath.Join(repoRoot, "waivers.txt"))

	c.Assert(err, qt.IsNil)
	c.Assert(waivedProbes(string(body)), qt.Not(qt.Contains), txtarProbeName)
}

// TestWaivers_ARealTxtarWaiverWouldBeVisible is the inverse control. Without
// it, the assertion above passes on an empty file, on a file this test cannot
// parse, and on a reader that returns nothing -- three ways to hold while
// checking nothing.
func TestWaivers_ARealTxtarWaiverWouldBeVisible(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "a txtar waiver is seen",
			body: "# comment\n" +
				txtarProbeName + " postgres/column-enum-array.txtar script-runtime   reason (#1)\n",
			want: []string{txtarProbeName},
		},
		{
			name: "another probe's waiver is not mistaken for one",
			body: "migrate-runtime sqlite/tx-mode-all stderr   reason (#2)\n",
			want: []string{"migrate-runtime"},
		},
		{
			name: "comments and blank lines carry no probe",
			body: "# " + txtarProbeName + " looks like a waiver but is prose\n\n",
			want: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(waivedProbes(test.body), qt.DeepEquals, test.want)
		})
	}
}

// TestWaivers_ATxtarWaiverStillLoads pins that this is a policy this repository
// states, not one the loader enforces.
//
// LoadWaivers must keep accepting the line: refusing it in the loader before
// the declaration mechanism exists would leave a real divergence no escape at
// all, which is worse than the blanket waiver it replaces. The refusal moves
// into the loader in the same change that gives divergences somewhere else to
// go.
func TestWaivers_ATxtarWaiverStillLoads(t *testing.T) {
	c := qt.New(t)
	path := filepath.Join(c.TempDir(), "waivers.txt")
	c.Assert(os.WriteFile(path,
		[]byte(txtarProbeName+" some/fixture.txtar script-runtime   tracked (#290)\n"), 0o600), qt.IsNil)

	waivers, err := probe.LoadWaivers(path)

	c.Assert(err, qt.IsNil)
	c.Assert(waivers, qt.IsNotNil)
}

// waivedProbes lists the probe of every waiver line, in file order. Comments and
// blank lines carry none.
func waivedProbes(body string) []string {
	probes := make([]string, 0)
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || len(fields) < 3 {
			continue
		}
		probes = append(probes, fields[0])
	}
	return probes
}
