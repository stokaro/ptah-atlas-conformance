package probe

// White-box testing required: splitWaiver is unexported and is the whole of the
// waiver file's grammar. The exported Waivers API reports a mis-parsed line as a
// stale waiver, which is the same answer it gives for a line that is genuinely
// stale -- so a black-box test could not tell a wrong key from a missing one,
// which is exactly the failure this parser was changed to end.

import "testing"

func TestSplitWaiver_HappyPath(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantProbe   string
		wantFixture string
		wantStage   string
		wantReason  string
	}{
		{
			name:        "three bare tokens, the shape every existing entry uses",
			line:        "txtar-script some/fixture.txtar script-runtime   because (issue#1)",
			wantProbe:   "txtar-script",
			wantFixture: "some/fixture.txtar",
			wantStage:   "script-runtime",
			wantReason:  "because (issue#1)",
		},
		{
			name:        "a fixture whose name contains the separator",
			line:        `atlas-cli-shorthands "atlas schema inspect -s" parse   why (issue#2)`,
			wantProbe:   "atlas-cli-shorthands",
			wantFixture: "atlas schema inspect -s",
			wantStage:   "parse",
			wantReason:  "why (issue#2)",
		},
		{
			name:        "a stage whose name contains the separator",
			line:        `dbtest-workflow "ptah schema test/html" "HTML report"   why (issue#3)`,
			wantProbe:   "dbtest-workflow",
			wantFixture: "ptah schema test/html",
			wantStage:   "HTML report",
			wantReason:  "why (issue#3)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, fixture, stage, reason, ok := splitWaiver(test.line)
			if !ok {
				t.Fatalf("line was refused: %q", test.line)
			}
			if probe != test.wantProbe || fixture != test.wantFixture ||
				stage != test.wantStage || reason != test.wantReason {
				t.Fatalf("got (%q, %q, %q, %q), want (%q, %q, %q, %q)",
					probe, fixture, stage, reason,
					test.wantProbe, test.wantFixture, test.wantStage, test.wantReason)
			}
		})
	}
}

func TestSplitWaiver_FailurePath(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "fewer than three fields", line: "only two"},
		{name: "an unterminated quote", line: `probe "never closed parse   reason`},
		{name: "empty", line: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, _, ok := splitWaiver(test.line); ok {
				t.Fatalf("line was accepted: %q", test.line)
			}
		})
	}
}
