package probe_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// vendoredSQLiteLintDir holds the imported Atlas cli-migrate-lint-* fixtures.
const vendoredSQLiteLintDir = "../../third_party/atlas/upstream/internal/integration/testdata/sqlite"

// rowidAliasIssue is the issue that records the engine-version decision.
const rowidAliasIssue = "stokaro/ptah#5952"

// observation is the part of a result these tests compare exactly. The detail
// names the engine and driver versions, which move with dependency updates, so
// it is matched separately.
type observation struct {
	Probe   string
	Stage   string
	Outcome probe.Outcome
	Issue   string
}

func observationsOf(results []probe.Result) []observation {
	out := make([]observation, 0, len(results))
	for _, result := range results {
		out = append(out, observation{result.Probe, result.Stage, result.Outcome, result.Issue})
	}
	return out
}

// refusedRun is the observation of one lint command recognized as the
// engine-version difference.
var refusedRun = observation{"txtar-script", "script-runtime", probe.OK, rowidAliasIssue}

// rowidAliasRefusalDetail matches the detail reported for command when the
// engine refuses the rename in migrations/1.sql.
func rowidAliasRefusalDetail(command string) string {
	return "engine-version difference: `" + regexp.QuoteMeta(command) + "` exited 1 because " +
		`SQLite 3\.\d+\.\d+ \(modernc\.org/sqlite v\S+, which ptah-compat links\) refuses migrations/1\.sql: ` +
		`SQL logic error: cannot rename to rowid alias: oid \(1\); ` +
		`Atlas CE v1\.3\.0 embeds SQLite 3\.49\.1, which accepts the rename`
}

// runTxtar runs the txtar probe over script written to a temporary file under
// the fixture name.
func runTxtar(c *qt.C, name, script string) []probe.Result {
	dir := c.TempDir()
	path := filepath.Join(dir, filepath.Base(name))
	c.Assert(os.WriteFile(path, []byte(script), 0o600), qt.IsNil)
	return probe.TxtarScriptProbe{}.Run(probe.Fixture{
		Name:  name,
		Kind:  probe.FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})
}

// TestTxtarScriptProbe_SQLiteRowidAliasRefusal_IsRecognized drives the two
// vendored fixtures whose first migration renames a column to oid. The SQLite
// that ptah-compat links refuses that rename, Atlas CE's older SQLite accepts
// it, and each lint command is reported OK as that difference.
func TestTxtarScriptProbe_SQLiteRowidAliasRefusal_IsRecognized(t *testing.T) {
	tests := []struct {
		fixture  string
		commands []string
	}{
		{
			fixture: "cli-migrate-lint-destructive.txtar",
			commands: []string{
				"atlas migrate lint --dir file://migrations --dev-url URL --latest=1",
				"atlas migrate lint --dir file://migrations --dev-url URL --latest=2",
			},
		},
		{
			fixture: "cli-migrate-lint-project.txtar",
			commands: []string{
				"atlas migrate lint --dir file://migrations --dev-url URL --env=log_name > got.txt",
				"atlas migrate lint --dir file://migrations --dev-url URL --env=log_count > got.txt",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			c := qt.New(t)
			path := filepath.Join(vendoredSQLiteLintDir, test.fixture)

			results := probe.TxtarScriptProbe{}.Run(probe.Fixture{
				Name:  "internal/integration/testdata/sqlite/" + test.fixture,
				Kind:  probe.FixtureKindTxtar,
				Dir:   filepath.Dir(path),
				Files: []string{path},
			})

			c.Assert(observationsOf(results), qt.DeepEquals, []observation{refusedRun, refusedRun})
			for i, command := range test.commands {
				c.Assert(results[i].Detail, qt.Matches, rowidAliasRefusalDetail(command))
			}
		})
	}
}

// TestTxtarScriptProbe_SQLiteRowidAliasRefusal_IgnoresTheRefusedOutput pins
// that the recognition does not depend on what the refused command printed.
// ptah-compat v0.25.0 writes the analyzed file, 2.sql, through the log
// template; the community report shape names only the failing file, 1.sql.
// The fixture expects the second, and the refused run is still OK, because no
// comparison judges its output.
func TestTxtarScriptProbe_SQLiteRowidAliasRefusal_IgnoresTheRefusedOutput(t *testing.T) {
	c := qt.New(t)
	command := "atlas migrate lint --dir file://migrations --dev-url URL --env=log_name > got.txt"

	results := runTxtar(c, "sqlite/cli-migrate-lint-refused-log.txtar", command+`
cmp got.txt expected.txt

-- migrations/1.sql --
CREATE TABLE users (id int);

ALTER TABLE users RENAME COLUMN id TO oid;

-- migrations/2.sql --
DROP TABLE users;

-- expected.txt --
1.sql
-- atlas.hcl --
lint {
    latest = 1
    destructive {
        error = false
    }
}

env "log_name" {
    lint {
        log = "{{ range .Files }}{{ println .Name }}{{ end }}"
    }
}
`)

	c.Assert(observationsOf(results), qt.DeepEquals, []observation{refusedRun})
	c.Assert(results[0].Detail, qt.Matches, rowidAliasRefusalDetail(command))
}

// TestTxtarScriptProbe_SQLiteRowidAliasRefusal_UnrelatedFailureStaysRed keeps
// the recognition to the command the engine refuses. The second command lints
// a directory whose replay fails on a missing table, which the engine-version
// difference does not explain, so it stays red under the issue.
func TestTxtarScriptProbe_SQLiteRowidAliasRefusal_UnrelatedFailureStaysRed(t *testing.T) {
	c := qt.New(t)
	refused := "atlas migrate lint --dir file://migrations --dev-url URL --latest=1"

	results := runTxtar(c, "internal/integration/testdata/sqlite/cli-migrate-lint-destructive.txtar", refused+`
atlas migrate lint --dir file://other --dev-url URL --latest=1

-- migrations/1.sql --
CREATE TABLE users (id int);

ALTER TABLE users RENAME COLUMN id TO oid;

-- migrations/2.sql --
DROP TABLE users;

-- other/1.sql --
CREATE TABLE users (id int);

INSERT INTO missing VALUES (1);

-- other/2.sql --
DROP TABLE users;
`)

	c.Assert(observationsOf(results), qt.DeepEquals, []observation{
		refusedRun,
		{"txtar-script", "script-runtime", probe.Fail, rowidAliasIssue},
	})
	c.Assert(results[0].Detail, qt.Matches, rowidAliasRefusalDetail(refused))
	c.Assert(results[1].Detail, qt.Contains, "no such table: missing")
}

// TestTxtarScriptProbe_SQLiteRenameTheEngineAccepts_IsJudgedByTheFixture is the
// control from the issue: with the column renamed to uid, which the engine
// accepts, the destructive fixture's own assertions decide, and they pass.
func TestTxtarScriptProbe_SQLiteRenameTheEngineAccepts_IsJudgedByTheFixture(t *testing.T) {
	c := qt.New(t)
	data, err := os.ReadFile(filepath.Join(vendoredSQLiteLintDir, "cli-migrate-lint-destructive.txtar"))
	c.Assert(err, qt.IsNil)
	c.Assert(strings.Count(string(data), "RENAME COLUMN id TO oid;"), qt.Equals, 1)
	script := strings.Replace(string(data), "RENAME COLUMN id TO oid;", "RENAME COLUMN id TO uid;", 1)

	results := runTxtar(c, "internal/integration/testdata/sqlite/cli-migrate-lint-destructive.txtar", script)

	c.Assert(results, qt.DeepEquals, []probe.Result{{
		Probe:   "txtar-script",
		Fixture: "internal/integration/testdata/sqlite/cli-migrate-lint-destructive.txtar",
		Stage:   "script-runtime",
		Outcome: probe.OK,
		Detail:  "script surface: atlas migrate lint=2, executed 2 supported command(s), checked 35 assertion(s)",
	}})
}

// unverifiedDriver matches the note a command carries when the engine refuses
// migrations/1.sql but the binary has no Go build record to show its driver.
const unverifiedDriver = ` \(SQLite 3\.\d+\.\d+ refuses migrations/1\.sql, ` +
	`driver version unverified: reading the ptah-compat build record: .+\)`

// TestTxtarScriptProbe_SQLiteRowidAliasRefusal_UnverifiedDriverStaysRed fails
// closed. The stand-in binary is a shell script, so nothing shows that it
// links the engine the harness measured: the run is judged as if nothing was
// measured, and its red row says why the difference was not recognized.
func TestTxtarScriptProbe_SQLiteRowidAliasRefusal_UnverifiedDriverStaysRed(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		binary     string
		wantDetail string
	}{
		{
			name:       "the command's own failure",
			script:     "atlas migrate lint --dir file://migrations --dev-url URL --latest=1\n",
			binary:     "#!/bin/sh\necho boom >&2\nexit 1\n",
			wantDetail: `atlas migrate lint exited 1: boom` + unverifiedDriver,
		},
		{
			name: "an assertion on the command's report",
			script: "! atlas migrate lint --dir file://migrations --dev-url URL --latest=1\n" +
				"stdout 'Dropping table \"users\"'\n",
			binary: "#!/bin/sh\n" +
				"printf 'Analyzing changes from version 1 to 2 (1 migration in total):\\n\\n" +
				"  Error: executing statement: cannot rename to rowid alias: oid\\n'\n" +
				"exit 1\n",
			wantDetail: `stdout assertion .+ did not match .+` + unverifiedDriver,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			dir := c.TempDir()
			binary := filepath.Join(dir, "atlas")
			c.Assert(os.WriteFile(binary, []byte(test.binary), 0o700), qt.IsNil)
			path := filepath.Join(dir, "cli-migrate-lint-destructive.txtar")
			c.Assert(os.WriteFile(path, []byte(test.script+`
-- migrations/1.sql --
CREATE TABLE users (id int);

ALTER TABLE users RENAME COLUMN id TO oid;

-- migrations/2.sql --
DROP TABLE users;
`), 0o600), qt.IsNil)

			results := probe.TxtarScriptProbe{Binary: binary}.Run(probe.Fixture{
				Name:  "internal/integration/testdata/sqlite/cli-migrate-lint-destructive.txtar",
				Kind:  probe.FixtureKindTxtar,
				Dir:   dir,
				Files: []string{path},
			})

			c.Assert(observationsOf(results), qt.DeepEquals, []observation{
				{"txtar-script", "script-runtime", probe.Fail, rowidAliasIssue},
			})
			c.Assert(results[0].Detail, qt.Matches, test.wantDetail)
		})
	}
}
