package probe

// White-box testing required: canonicalizeJSONOrder is unexported, and the
// property under test is what it does to two documents that a database
// returned in different orders. No exported result reports it -- the probe
// consumes the canonical form and emits only whether two snapshots matched.

import (
	"encoding/json"
	"testing"

	qt "github.com/frankban/quicktest"
)

// canonicalJSON is the comparison the probe actually performs: unmarshal,
// canonicalize, re-marshal.
func canonicalJSON(c *qt.C, document string) string {
	c.Helper()
	var value any
	c.Assert(json.Unmarshal([]byte(document), &value), qt.IsNil)
	encoded, err := json.Marshal(canonicalizeJSONOrder(value))
	c.Assert(err, qt.IsNil)
	return string(encoded)
}

// TestCanonicalizeJSONOrder_CollapsesReordering pins what the canonicalizer is
// for: two introspection snapshots that differ only in the order a database
// happened to return a list are the same schema.
//
// Each row asserts the raw documents differ before asserting the canonical
// forms agree. Without that first assertion the test would also pass against a
// canonicalizer that had become a no-op, because the inputs would have to be
// identical for the second assertion to hold -- and it would pass against a row
// whose two documents were accidentally written the same.
//
// The shapes are the ones that produced intermittent "state changed during
// dry-run" gaps: a reordered top-level list, a reordered nested list, and a
// reordering deep enough that only a recursive walk reaches it.
func TestCanonicalizeJSONOrder_CollapsesReordering(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
	}{
		{
			name:  "a top-level list in either order",
			left:  `{"constraints":[{"name":"a"},{"name":"b"}]}`,
			right: `{"constraints":[{"name":"b"},{"name":"a"}]}`,
		},
		{
			name:  "an enum's values in either order",
			left:  `{"enums":[{"name":"status","values":["draft","live"]}]}`,
			right: `{"enums":[{"name":"status","values":["live","draft"]}]}`,
		},
		{
			name:  "a list nested under two objects",
			left:  `{"tables":[{"name":"t","columns":[{"name":"a"},{"name":"b"}]}]}`,
			right: `{"tables":[{"name":"t","columns":[{"name":"b"},{"name":"a"}]}]}`,
		},
		{
			name:  "reordering at two depths at once",
			left:  `{"tables":[{"name":"u","indexes":["x","y"]},{"name":"t","indexes":["p","q"]}]}`,
			right: `{"tables":[{"name":"t","indexes":["q","p"]},{"name":"u","indexes":["y","x"]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(test.left, qt.Not(qt.Equals), test.right,
				qt.Commentf("the row must start from two different documents, or it asserts nothing"))
			c.Assert(canonicalJSON(c, test.left), qt.Equals, canonicalJSON(c, test.right))
		})
	}
}

// TestCanonicalizeJSONOrder_KeepsContentDifferences is the control the table
// above needs. A canonicalizer that returned a constant, sorted away content,
// or dropped list elements would satisfy every row there and make the probe
// blind to the schema changes it exists to detect.
func TestCanonicalizeJSONOrder_KeepsContentDifferences(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
	}{
		{
			name:  "an added element",
			left:  `{"constraints":[{"name":"a"}]}`,
			right: `{"constraints":[{"name":"a"},{"name":"b"}]}`,
		},
		{
			name:  "a renamed element",
			left:  `{"constraints":[{"name":"a"},{"name":"b"}]}`,
			right: `{"constraints":[{"name":"a"},{"name":"c"}]}`,
		},
		{
			name:  "a changed value deep in the document",
			left:  `{"tables":[{"name":"t","columns":[{"name":"a","type":"int"}]}]}`,
			right: `{"tables":[{"name":"t","columns":[{"name":"a","type":"bigint"}]}]}`,
		},
		{
			name:  "a duplicate that a set-like collapse would lose",
			left:  `{"values":["x","x"]}`,
			right: `{"values":["x"]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(canonicalJSON(c, test.left), qt.Not(qt.Equals), canonicalJSON(c, test.right))
		})
	}
}

// TestCanonicalizeJSONOrder_IsStableAcrossRuns pins determinism. The probe
// compares a snapshot taken before a dry-run with one taken after, so a
// canonical form that depended on map iteration order would report a change
// that never happened -- which is the intermittent failure this guards.
func TestCanonicalizeJSONOrder_IsStableAcrossRuns(t *testing.T) {
	c := qt.New(t)
	document := `{"b":[{"z":1},{"a":2}],"a":{"nested":["q","p"]},"c":[3,1,2]}`

	first := canonicalJSON(c, document)
	for range 32 {
		c.Assert(canonicalJSON(c, document), qt.Equals, first)
	}
}
