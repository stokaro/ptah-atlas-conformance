package probe

// White-box testing required: the scrubbers are unexported and shape every
// recorded detail. Their failure mode is invisible from outside -- a report that
// carries a per-run value simply looks stale on the next regeneration, which is
// the same thing an out-of-date report looks like.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestScrubRunIdentifiers_HappyPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a database named for one run",
			in:   "references database ptah_rt_mysql_1788731234567890123",
			want: "references database ptah_rt_mysql_<run>",
		},
		{
			name: "two identifiers in one detail",
			in:   "ptah_rt_mysql_dry_run_1788731234567890123 and ptah_rt_pg_schema_1788731234567890124",
			want: "ptah_rt_mysql_dry_run_<run> and ptah_rt_pg_schema_<run>",
		},
		{
			name: "a multi-word label keeps every word",
			in:   "ptah_rt_mysql_check_comments_1788731234567890123",
			want: "ptah_rt_mysql_check_comments_<run>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(scrubRunIdentifiers(test.in), qt.Equals, test.want)
		})
	}
}

// TestScrubRunIdentifiers_LeavesOtherDigitsAlone is the control. A rule that
// removed every long number would also erase a migration version, which is
// exactly the value some fixtures assert on.
func TestScrubRunIdentifiers_LeavesOtherDigitsAlone(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "a migration version", in: "Rebased migration 20260101000001 to 20260101000003"},
		{name: "no identifier at all", in: "nothing to scrub here"},
		{name: "a similar prefix that is not one", in: "ptah_runtime_1788731234567890123"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			c.Assert(scrubRunIdentifiers(test.in), qt.Equals, test.in)
		})
	}
}
