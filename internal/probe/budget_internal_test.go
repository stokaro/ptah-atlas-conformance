package probe

// White-box testing required: the budget's identity for a finding is
// `waiverKey`, which is unexported. Two findings that key the same way are
// one waiver, and nothing exported reports the key a finding produced.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestParseGapBudget_HappyPath(t *testing.T) {
	c := qt.New(t)

	budget, err := ParseGapBudget([]byte("# current allowed gaps\n177\n"))

	c.Assert(err, qt.IsNil)
	c.Assert(budget, qt.Equals, GapBudget(177))
}

// TestParseGapBudget_FailurePath keeps a budget file that says nothing usable
// from resolving to a number. The rows are the three shapes a hand-edited file
// takes: emptied, replaced with prose, and negative.
func TestParseGapBudget_FailurePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "not a number", input: "nope\n"},
		{name: "negative", input: "-1\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			budget, err := ParseGapBudget([]byte(test.input))

			c.Assert(err, qt.IsNotNil)
			c.Assert(budget, qt.Equals, GapBudget(0))
		})
	}
}

func TestCheckGapBudget(t *testing.T) {
	c := qt.New(t)
	results := []Result{
		{Probe: "sql-parse", Fixture: "ok.sql", Stage: "round-trip", Outcome: OK},
		{Probe: "sql-parse", Fixture: "gap.sql", Stage: "round-trip", Outcome: Gap},
		{Probe: "sql-parse", Fixture: "fail.sql", Stage: "round-trip", Outcome: Fail},
	}
	waivers := &Waivers{byKey: map[string]string{
		waiverKey("sql-parse", "gap.sql", "round-trip"): "tracked",
	}}

	status := CheckGapBudget(results, waivers, 1)

	c.Assert(status.Unwaived, qt.Equals, 1)
	c.Assert(status.OverBudget(), qt.IsFalse)
}
