package probe_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// vendoredCmdapiTestdata holds the imported Atlas cmdapi fixtures, among them
// the two SQLite table rebuilds.
const vendoredCmdapiTestdata = "../../third_party/atlas/upstream/cmd/atlas/internal/cmdapi/testdata"

// orphanedFriendship is the detail of the sqlitetx2 row when Ptah refuses the
// orphaned friendship row that Atlas CE accepts.
const orphanedFriendship = "Ptah refused the orphaned friendship row before commit; Atlas CE accepts this invalid rebuild"

// rebuildFixture loads the vendored SQLite rebuild fixture name as the corpus
// names it.
func rebuildFixture(c *qt.C, name string) probe.Fixture {
	dir, err := filepath.Abs(filepath.Join(vendoredCmdapiTestdata, name))
	c.Assert(err, qt.IsNil)
	sqls, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	c.Assert(err, qt.IsNil)
	c.Assert(sqls, qt.HasLen, 3)
	return probe.Fixture{
		Name:     "cmd/atlas/internal/cmdapi/testdata/" + name,
		Kind:     probe.FixtureKindSQLDir,
		Dir:      dir,
		SQLFiles: sqls,
		SumFile:  filepath.Join(dir, "atlas.sum"),
	}
}

// stubLintBinary writes a stand-in for ptah-compat that prints stdout and
// stderr and exits with code, whatever it is asked.
func stubLintBinary(c *qt.C, stdout, stderr string, code int) string {
	dir := c.TempDir()
	stdoutFile := filepath.Join(dir, "stdout")
	stderrFile := filepath.Join(dir, "stderr")
	c.Assert(os.WriteFile(stdoutFile, []byte(stdout), 0o600), qt.IsNil)
	c.Assert(os.WriteFile(stderrFile, []byte(stderr), 0o600), qt.IsNil)
	bin := filepath.Join(dir, "atlas")
	script := fmt.Sprintf("#!/bin/sh\ncat '%s'\ncat '%s' >&2\nexit %d\n", stdoutFile, stderrFile, code)
	c.Assert(os.WriteFile(bin, []byte(script), 0o700), qt.IsNil)
	return bin
}

// TestLintProbe_SQLiteRebuild_PinnedPtah drives the pinned ptah-compat over
// both rebuild fixtures. sqlitetx loses the name column in its final rebuild,
// and Ptah reports the one DS103 and the one schema change Atlas CE reports.
// sqlitetx2 adds an orphaned friendship row, which Ptah refuses and Atlas CE
// accepts.
func TestLintProbe_SQLiteRebuild_PinnedPtah(t *testing.T) {
	tests := []struct {
		fixture    string
		wantDetail string
	}{
		{
			fixture:    "sqlitetx",
			wantDetail: "SQLite dev replay reported one DS103 for the name column lost by the final table rebuild",
		},
		{
			fixture:    "sqlitetx2",
			wantDetail: orphanedFriendship,
		},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			c := qt.New(t)
			fx := rebuildFixture(c, test.fixture)

			got := probe.LintProbe{}.Run(fx)

			c.Assert(got, qt.DeepEquals, []probe.Result{{
				Probe:   "lint-parity",
				Fixture: fx.Name,
				Stage:   "lint",
				Outcome: probe.OK,
				Detail:  test.wantDetail,
			}})
		})
	}
}

// orphanedFriendshipReport is the lint report ptah-compat prints when it
// refuses sqlitetx2's rebuild: the refusal on an Error line of the report, as
// Atlas CE reports a replay it cannot finish.
const orphanedFriendshipReport = "Analyzing changes from version 20220925094021 to 20220925094437 (1 migration in total):\n\n" +
	"  Error: failed to commit migration 20220925094437: sqlite: rebuild left 1 unresolved " +
	"foreign-key reference(s): friendships references missing users\n\n" +
	"  -------------------------\n  -- 39µs\n  -- 1 version with errors\n"

// TestLintProbe_SQLiteRebuildRefusal_HappyPath: the refusal inside the lint
// report, with nothing on stderr and exit 1, is the intentional safety
// difference.
func TestLintProbe_SQLiteRebuildRefusal_HappyPath(t *testing.T) {
	c := qt.New(t)
	fx := rebuildFixture(c, "sqlitetx2")

	got := probe.LintProbe{Binary: stubLintBinary(c, orphanedFriendshipReport, "", 1)}.Run(fx)

	c.Assert(got, qt.DeepEquals, []probe.Result{{
		Probe:   "lint-parity",
		Fixture: fx.Name,
		Stage:   "lint",
		Outcome: probe.OK,
		Detail:  orphanedFriendship,
	}})
}

// TestLintProbe_SQLiteRebuildRefusal_FailurePath keeps every other shape red:
// the bare error on stderr, which ptah-compat v0.25.0 prints, the report beside
// a stderr message, the report with a clean exit, a report whose error is
// another one, and a report naming the refusal outside an Error line.
func TestLintProbe_SQLiteRebuildRefusal_FailurePath(t *testing.T) {
	tests := []struct {
		name        string
		stdout      string
		stderr      string
		code        int
		wantOutcome probe.Outcome
		wantDetail  string
	}{
		{
			name: "bare error on stderr",
			stderr: "Error: error validating migration SQL on dev database: replay migration 20220925094437 " +
				"on dev database: failed to commit migration 20220925094437: sqlite: rebuild left 1 " +
				"unresolved foreign-key reference(s): friendships references missing users\n",
			code:        1,
			wantOutcome: probe.Fail,
			wantDetail:  `SQLite lint replay failed: Error: error validating migration SQL on dev database: .+`,
		},
		{
			name:        "report beside stderr",
			stdout:      orphanedFriendshipReport,
			stderr:      "warning\n",
			code:        1,
			wantOutcome: probe.Fail,
			wantDetail:  `SQLite lint replay failed: warning`,
		},
		{
			name:        "clean exit",
			stdout:      orphanedFriendshipReport,
			wantOutcome: probe.Gap,
			wantDetail: `SQLite dev replay did not match DS103 and the schema summary ` +
				`\(exit 0, DS103=false, one diagnostic=false, one schema change=false\)`,
		},
		{
			name: "another error",
			stdout: "Analyzing changes from version 20220925094021 to 20220925094437 (1 migration in total):\n\n" +
				"  Error: executing statement: SQL logic error: no such table: users (1)\n",
			code:        1,
			wantOutcome: probe.Gap,
			wantDetail: `SQLite dev replay did not match DS103 and the schema summary ` +
				`\(exit 1, DS103=false, one diagnostic=false, one schema change=false\)`,
		},
		{
			name: "refusal outside an Error line",
			stdout: "Analyzing changes from version 20220925094021 to 20220925094437 (1 migration in total):\n\n" +
				"  -- analyzing version 20220925094437\n" +
				"    -- rebuild left 1 unresolved foreign-key reference(s): friendships references missing users\n",
			code:        1,
			wantOutcome: probe.Gap,
			wantDetail: `SQLite dev replay did not match DS103 and the schema summary ` +
				`\(exit 1, DS103=false, one diagnostic=false, one schema change=false\)`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			fx := rebuildFixture(c, "sqlitetx2")

			got := probe.LintProbe{Binary: stubLintBinary(c, test.stdout, test.stderr, test.code)}.Run(fx)

			c.Assert(got, qt.HasLen, 1)
			c.Assert(got[0].Outcome, qt.Equals, test.wantOutcome)
			c.Assert(got[0].Detail, qt.Matches, test.wantDetail)
			c.Assert(got[0].Issue, qt.Equals, "stokaro/ptah#5953")
		})
	}
}
