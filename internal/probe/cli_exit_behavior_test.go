package probe

import "testing"

// TestCLIExitBehaviorMatrix runs the exit/error matrix and asserts the drop-in
// contract holds across both surfaces: every row is OK (success paths exit 0,
// failure paths exit non-zero on stderr), with no build/timeout failure and no
// exit-contract gap. A regression (a failure path exiting 0) turns this red.
func TestCLIExitBehaviorMatrix(t *testing.T) {
	results := AtlasCLIExitBehaviorProbe{}.Run(Fixture{Name: cliExitSentinel})
	want := len(cliExitSurfaces()) * len(cliExitCatalog)
	if len(results) != want {
		t.Fatalf("expected %d matrix rows, got %d", want, len(results))
	}
	for _, r := range results {
		if r.Outcome != OK {
			t.Errorf("row %q is not OK (%s): %s", r.Fixture, r.Outcome, r.Detail)
		}
	}
}

func TestCLIExitBehaviorIgnoresNonSentinel(t *testing.T) {
	if got := (AtlasCLIExitBehaviorProbe{}).Run(Fixture{Name: "some/fixture.sql"}); got != nil {
		t.Fatalf("expected nil for a non-sentinel fixture, got %d results", len(got))
	}
}
