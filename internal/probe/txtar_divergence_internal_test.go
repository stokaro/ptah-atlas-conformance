package probe

import (
	"strings"
	"testing"
)

// A declared divergence rewrites one fragment of the expected document and then
// compares as usual. That is the whole point of it being a rewrite rather than a
// pass: the fixture keeps checking everything else it checks, so a second
// difference in the same file is still a finding.
func TestTxtarDeclaredDivergenceCoversOnlyWhatItDeclares(t *testing.T) {
	const (
		fixture  = "postgres/column-enum-array.txtar"
		file     = "5.inspect.hcl"
		declared = `type = sql("status[]")`
		written  = `type = sql("script_column_enum_array.status[]")`
	)
	expected := "table \"enums\" {\n  column \"statuses\" {\n    " + declared + "\n  }\n}\n"

	tests := []struct {
		name    string
		fixture string
		file    string
		actual  string
		want    bool
	}{
		{
			name:    "the declared difference alone is covered",
			fixture: fixture, file: file,
			actual: strings.ReplaceAll(expected, declared, written),
			want:   true,
		},
		{
			// The declared rewrite applies, and the document still differs.
			// Covering this would turn one named divergence into a blanket
			// pass for the fixture.
			name:    "a second difference in the same file is not covered",
			fixture: fixture, file: file,
			actual: strings.ReplaceAll(expected, declared, written) +
				"enum \"status\" {\n}\n",
			want: false,
		},
		{
			name:    "an unchanged document does not need the divergence",
			fixture: fixture, file: file,
			actual: expected,
			want:   false,
		},
		{
			name:    "another fixture is not covered",
			fixture: "postgres/column-int.txtar", file: file,
			actual: strings.ReplaceAll(expected, declared, written),
			want:   false,
		},
		{
			name:    "another file in the same fixture is not covered",
			fixture: fixture, file: "3.inspect.hcl",
			actual: strings.ReplaceAll(expected, declared, written),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pin, ok := txtarDeclaredDivergence(Fixture{Name: tt.fixture}, tt.file, tt.actual, expected)
			if ok != tt.want {
				t.Fatalf("covered = %v, want %v (pin=%q)", ok, tt.want, pin)
			}
			if ok && !strings.Contains(pin, "PTAH-SIDE PIN") {
				t.Fatalf("a covered divergence must name itself in the report, got %q", pin)
			}
		})
	}
}
