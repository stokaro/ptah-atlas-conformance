package probe

import (
	"strings"
	"testing"
)

// TestLintAnalyzerMatrixHasNoHardFailures asserts Ptah's linter never errors or
// panics on any synthetic analyzer migration. It deliberately tolerates a
// `missing` Gap (a documented, issue-linked uncovered concern) so a legitimate
// gap does not break `go test`; classification drift itself (a covered concern
// regressing to missing, or exact↔mapped) is caught by the committed
// gaps.md/gaps.json report diff and `make gate`, which treat any change as red.
func TestLintAnalyzerMatrixHasNoHardFailures(t *testing.T) {
	results := AtlasLintAnalyzerProbe{}.Run(Fixture{Name: lintAnalyzerSentinel})
	wantRows := len(atlasAnalyzerCatalog) + len(lintFidelityBehaviorChecks())
	if len(results) != wantRows {
		t.Fatalf("expected %d matrix rows, got %d", wantRows, len(results))
	}
	for _, r := range results {
		if r.Outcome == Fail || r.Outcome == Panic {
			t.Errorf("row %q is a hard failure (%s): %s", r.Fixture, r.Outcome, r.Detail)
		}
	}
}

// TestLintFidelityChecksEnforceBehaviors is the enforced core of the matrix: the
// suppression, disable, severity-override, and attribution behaviors CI consumers
// rely on must hold. If Ptah drops one, its check flips to Fail here and in the
// gated report — the "deliberately removed behavior makes the gate red" contract.
func TestLintFidelityChecksEnforceBehaviors(t *testing.T) {
	checks := lintFidelityBehaviorChecks()
	if len(checks) != 4 {
		t.Fatalf("expected 4 fidelity checks, got %d", len(checks))
	}
	for _, r := range checks {
		if r.Outcome != OK {
			t.Errorf("fidelity check %q did not hold: %s", r.Fixture, r.Detail)
		}
		if !strings.HasPrefix(r.Fixture, "fidelity: ") {
			t.Errorf("fidelity check has unexpected fixture label %q", r.Fixture)
		}
	}
}

func TestLintAnalyzerProbeIgnoresNonSentinel(t *testing.T) {
	if got := (AtlasLintAnalyzerProbe{}).Run(Fixture{Name: "some/fixture.sql"}); got != nil {
		t.Fatalf("expected nil for a non-sentinel fixture, got %d results", len(got))
	}
}
