package probe

import "testing"

func TestParseGapBudget(t *testing.T) {
	budget, err := ParseGapBudget([]byte("# current allowed gaps\n177\n"))
	if err != nil {
		t.Fatal(err)
	}
	if budget != 177 {
		t.Fatalf("budget = %d, want 177", budget)
	}
}

func TestParseGapBudgetRejectsInvalidBudget(t *testing.T) {
	for _, input := range []string{"", "nope\n", "-1\n"} {
		if _, err := ParseGapBudget([]byte(input)); err == nil {
			t.Fatalf("ParseGapBudget(%q) succeeded, want error", input)
		}
	}
}

func TestCheckGapBudget(t *testing.T) {
	results := []Result{
		{Probe: "sql-parse", Fixture: "ok.sql", Stage: "round-trip", Outcome: OK},
		{Probe: "sql-parse", Fixture: "gap.sql", Stage: "round-trip", Outcome: Gap},
		{Probe: "sql-parse", Fixture: "fail.sql", Stage: "round-trip", Outcome: Fail},
	}
	waivers := &Waivers{byKey: map[string]string{
		waiverKey("sql-parse", "gap.sql", "round-trip"): "tracked",
	}}

	status := CheckGapBudget(results, waivers, 1)
	if status.Unwaived != 1 {
		t.Fatalf("unwaived = %d, want 1", status.Unwaived)
	}
	if status.OverBudget() {
		t.Fatal("status should be within budget")
	}
}
