package probe

import (
	"os"
	"path/filepath"
	"testing"
)

// The waivers file is shared by every tier, and each tier's budget run is handed
// only its own report. Staleness therefore has to distinguish "this gap closed"
// from "this report never ran that probe": the first is a waiver to delete, the
// second is every waiver in the file as seen from the other five tiers.
func TestWaiversUnusedIgnoresProbesTheReportDidNotRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "waivers.txt")
	// The three key tokens are split on whitespace, so a fixture name carrying
	// spaces cannot be keyed at all. Both rows here use space-free names, as
	// the real file does.
	contents := "# comment\n" +
		"txtar-script postgres/enum.txtar script-runtime   intentional divergence (ptah#1138)\n" +
		"ce-gating _capability/ce/SENTINEL gate   tracked (ptah#999)\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := LoadWaivers(path)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		results []Result
		want    []string
	}{
		{
			name: "a waiver whose finding is present is live",
			results: []Result{
				{Probe: "txtar-script", Fixture: "postgres/enum.txtar", Stage: "script-runtime", Outcome: Fail},
			},
			want: nil,
		},
		{
			name: "a waiver whose probe ran without that finding is stale",
			results: []Result{
				{Probe: "txtar-script", Fixture: "postgres/other.txtar", Stage: "script-runtime", Outcome: OK},
			},
			want: []string{"txtar-script postgres/enum.txtar script-runtime"},
		},
		{
			// The offline waiver seen from the ce-gating report. Calling it
			// stale there would fail a tier that never asked the question.
			name: "a waiver whose probe did not run at all is left alone",
			results: []Result{
				{Probe: "ce-gating", Fixture: "_capability/ce/SENTINEL", Stage: "gate", Outcome: Gap},
			},
			want: nil,
		},
		{
			name:    "an empty report leaves every waiver alone",
			results: nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.Unused(tt.results)
			if len(got) != len(tt.want) {
				t.Fatalf("stale waivers = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("stale waivers = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
