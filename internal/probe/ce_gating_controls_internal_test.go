package probe

// White-box testing required: the paired fixtures are unexported string
// constants, and the property under test is a relation between two of them
// rather than anything an invocation produces. The tier cannot see it: a
// control whose "nonsense" name is quietly replaced by the real one it
// controls still runs, still classifies as works, and still reports green,
// because absence of a token the fixture no longer contains is trivially true.
// That mutation was measured surviving the tier before this file existed.

import (
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"
)

// TestCEGatingControlFixturesDifferOnlyByName pins what each control claims in
// its comment: same document, one name changed. A control that drifts from the
// fixture it controls in any other way stops explaining that fixture's outcome,
// and nothing downstream would notice.
func TestCEGatingControlFixturesDifferOnlyByName(t *testing.T) {
	tests := []struct {
		name     string
		measured string
		control  string
		real     string
		nonsense string
	}{
		{
			name:     "annotation block",
			measured: ceGatingAnnotationHCL,
			control:  ceGatingNonsenseSchemaBlockHCL,
			real:     "annotation",
			nonsense: "zzz_nonsense_block",
		},
		{
			name:     "invisible column attribute",
			measured: ceGatingInvisibleColumnHCL,
			control:  ceGatingNonsenseColumnAttrHCL,
			real:     "invisible",
			nonsense: "zzz_nonsense_attr",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(strings.ReplaceAll(test.measured, test.real, test.nonsense),
				qt.Equals, test.control)
		})
	}
}

// TestCEGatingControlFixturesNameNothingAtlasKnows covers the third pair too,
// whose two documents legitimately differ by more than a name, so the relation
// above cannot express it. The weaker property still holds everywhere: a
// control must not carry a token from the announced vocabulary the paired
// fixture measures, because CE's indifference to a name it knows and its
// indifference to a name it does not are the two answers the pair exists to
// tell apart.
func TestCEGatingControlFixturesNameNothingAtlasKnows(t *testing.T) {
	controls := []struct {
		name    string
		fixture string
	}{
		{name: "nonsense schema HCL top-level block", fixture: ceGatingNonsenseSchemaBlockHCL},
		{name: "nonsense column attribute", fixture: ceGatingNonsenseColumnAttrHCL},
		{name: "nonsense atlas.hcl top-level block", fixture: ceGatingNonsenseBlockConfig},
	}
	// Every construct the v1.3.0 scenarios measure by name. A control that
	// contains one of these is measuring the announced construct instead of
	// the absence of knowledge about it.
	announced := []string{"annotation", "invisible", `check "migrate_apply"`, "drift"}

	for _, control := range controls {
		for _, token := range announced {
			t.Run(control.name+"/"+token, func(t *testing.T) {
				c := qt.New(t)

				c.Assert(control.fixture, qt.Not(qt.Contains), token)
			})
		}
	}
}
