package probe

// White-box testing required: whether a failed lint run is the SQLite
// engine-version difference is decided by the unexported classify,
// replaySQLiteMigrations, sqliteDriverLink and sqliteDriverMatch. The exported
// TxtarScriptProbe can drive only the pinned ptah-compat, whose replay failure
// has one output shape, on the one engine this module links, which refuses the
// rename, with one go command that lists one driver version. The other output
// shapes, exit codes, engine answers and build records can only be driven here.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	qt "github.com/frankban/quicktest"
)

// refusedOID is the engine's answer to the vendored fixtures' first migration.
var refusedOID = sqliteEngineRefusal{
	engine: "3.54.1",
	module: "v1.62.0",
	file:   "migrations/1.sql",
	reason: "SQL logic error: cannot rename to rowid alias: oid (1)",
}

// TestSQLiteEngineRefusalClassify_RecognizesExitOne covers both report shapes:
// ptah-compat v0.25.0 writes the replay failure to stderr, and the community
// shape puts it inside the lint report and lets the log template name only
// the failing file. Exit 1 is recognized whatever was printed.
func TestSQLiteEngineRefusalClassify_RecognizesExitOne(t *testing.T) {
	const command = "atlas migrate lint --dir file://migrations --dev-url URL --latest=1"
	tests := []struct {
		name   string
		stdout string
		stderr string
	}{
		{
			name:   "replay failure on stderr",
			stderr: "Error: error validating migration SQL on dev database: replay migration 1 on dev database: failed\n",
		},
		{
			name: "replay failure inside the lint report",
			stdout: "Analyzing changes from version 2 to 3 (1 migration in total):\n\n" +
				"  Error: executing statement: cannot rename to rowid alias: oid\n",
		},
		{
			name:   "log template naming the analyzed file",
			stdout: "3.sql\n",
		},
		{
			name:   "log template naming the failing file",
			stdout: "1.sql\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got := refusedOID.classify(command, txtarMigrateLintRun{
				stdout: test.stdout, stderr: test.stderr, failed: true, exitCode: 1,
			})

			c.Assert(got.engineRefusal, qt.Equals, "engine-version difference: "+
				"`atlas migrate lint --dir file://migrations --dev-url URL --latest=1` exited 1 because "+
				"SQLite 3.54.1 (modernc.org/sqlite v1.62.0, which ptah-compat links) refuses migrations/1.sql: "+
				"SQL logic error: cannot rename to rowid alias: oid (1); "+
				"Atlas CE v1.3.0 embeds SQLite 3.49.1, which accepts the rename")
			c.Assert(got.err, qt.IsNil)
			c.Assert(got.failed, qt.IsTrue)
			c.Assert(got.assertionsBlocked, qt.IsTrue)
		})
	}
}

// TestSQLiteEngineRefusalClassify_KeepsOtherExitsRed: a clean exit means Ptah
// passed a migration its own engine refuses, and another code is a crash or a
// timeout. Neither is the difference, and both stay failures.
func TestSQLiteEngineRefusalClassify_KeepsOtherExitsRed(t *testing.T) {
	tests := []struct {
		name    string
		run     txtarMigrateLintRun
		wantErr string
	}{
		{
			name: "clean exit",
			run:  txtarMigrateLintRun{stdout: "3.sql\n"},
			wantErr: `atlas migrate lint exited 0, but SQLite 3\.54\.1 refuses migrations/1\.sql: ` +
				`SQL logic error: cannot rename to rowid alias: oid \(1\)`,
		},
		{
			name: "crash",
			run: txtarMigrateLintRun{
				stderr: "panic: boom\n", failed: true, exitCode: 2,
				err: errors.New("atlas migrate lint exited 2: panic: boom"),
			},
			wantErr: `atlas migrate lint exited 2: panic: boom ` +
				`\(SQLite 3\.54\.1 refuses migrations/1\.sql, but that difference exits 1\)`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got := refusedOID.classify("atlas migrate lint --dir file://migrations --dev-url URL --latest=1", test.run)

			c.Assert(got.engineRefusal, qt.Equals, "")
			c.Assert(got.err, qt.ErrorMatches, test.wantErr)
			c.Assert(got.failed, qt.IsTrue)
		})
	}
}

// TestReplaySQLiteMigrations_RefusesTheRowidAliasRename replays the vendored
// fixtures' migrations on the engine this module links.
func TestReplaySQLiteMigrations_RefusesTheRowidAliasRename(t *testing.T) {
	c := qt.New(t)

	got, refused, err := replaySQLiteMigrations([]sqliteMigrationFile{
		{"migrations/1.sql", "CREATE TABLE users (id int);\n\nCREATE TABLE pets (id int);\n\nALTER TABLE users RENAME COLUMN id TO oid;\n"},
		{"migrations/2.sql", "DROP TABLE users;\n"},
		{"migrations/3.sql", "DROP TABLE pets;\n"},
	})

	c.Assert(err, qt.IsNil)
	c.Assert(refused, qt.IsTrue)
	c.Assert(got.engine, qt.Matches, `3\.\d+\.\d+`)
	c.Assert(got.file, qt.Equals, "migrations/1.sql")
	c.Assert(got.reason, qt.Equals, "SQL logic error: cannot rename to rowid alias: oid (1)")
}

// TestReplaySQLiteMigrations_OtherAnswersAreNotARefusal: an accepted rename and
// a replay that stops on an unrelated error first are not the difference.
func TestReplaySQLiteMigrations_OtherAnswersAreNotARefusal(t *testing.T) {
	tests := []struct {
		name  string
		files []sqliteMigrationFile
	}{
		{
			name: "rename the engine accepts",
			files: []sqliteMigrationFile{
				{"migrations/1.sql", "CREATE TABLE users (id int);\nALTER TABLE users RENAME COLUMN id TO uid;\n"},
				{"migrations/2.sql", "DROP TABLE users;\n"},
			},
		},
		{
			name: "unrelated error before the rename",
			files: []sqliteMigrationFile{
				{"migrations/1.sql", "CREATE TABLE users (id int);\nINSERT INTO missing VALUES (1);\n"},
				{"migrations/2.sql", "ALTER TABLE users RENAME COLUMN id TO oid;\n"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got, refused, err := replaySQLiteMigrations(test.files)

			c.Assert(err, qt.IsNil)
			c.Assert(refused, qt.IsFalse)
			c.Assert(got, qt.Equals, sqliteEngineRefusal{})
		})
	}
}

// TestSQLiteDriverMatch_HappyPath: a build record that lists the driver at the
// harness's version, unreplaced, links the engine the harness measured.
func TestSQLiteDriverMatch_HappyPath(t *testing.T) {
	c := qt.New(t)

	got, err := sqliteDriverMatch([]*debug.Module{
		{Path: "ptah.run", Version: "v0.25.0"},
		{Path: "modernc.org/sqlite", Version: "v1.62.0"},
	}, "v1.62.0")

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.Equals, "v1.62.0")
}

// TestSQLiteDriverMatch_FailurePath: each answer that cannot show the binary
// embeds the measured engine is refused with its own reason.
func TestSQLiteDriverMatch_FailurePath(t *testing.T) {
	tests := []struct {
		name    string
		deps    []*debug.Module
		wantErr string
	}{
		{
			name:    "another version",
			deps:    []*debug.Module{{Path: "modernc.org/sqlite", Version: "v1.50.0"}},
			wantErr: `ptah-compat links modernc\.org/sqlite v1\.50\.0, this harness links v1\.62\.0`,
		},
		{
			name: "replaced module",
			deps: []*debug.Module{{
				Path: "modernc.org/sqlite", Version: "v1.62.0",
				Replace: &debug.Module{Path: "../sqlite"},
			}},
			wantErr: `ptah-compat replaces modernc\.org/sqlite v1\.62\.0 with \.\./sqlite`,
		},
		{
			name:    "no driver",
			deps:    []*debug.Module{{Path: "ptah.run", Version: "v0.25.0"}},
			wantErr: `ptah-compat links no modernc\.org/sqlite`,
		},
		{
			name:    "no version",
			deps:    []*debug.Module{{Path: "modernc.org/sqlite"}},
			wantErr: `ptah-compat records no modernc\.org/sqlite version`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got, err := sqliteDriverMatch(test.deps, "v1.62.0")

			c.Assert(err, qt.ErrorMatches, test.wantErr)
			c.Assert(got, qt.Equals, "")
		})
	}
}

// errGoList stands in for a go command that cannot list the module, as when
// the probe runs outside this module.
var errGoList = errors.New("go list failed")

// TestSQLiteDriverLink_FailurePath: neither a failed listing of the harness's
// driver nor a file without a Go build record verifies the link.
func TestSQLiteDriverLink_FailurePath(t *testing.T) {
	tests := []struct {
		name    string
		harness func() (string, error)
		wantErr string
	}{
		{
			name:    "go list fails",
			harness: func() (string, error) { return "", errGoList },
			wantErr: `go list failed`,
		},
		{
			name:    "no build record",
			harness: func() (string, error) { return "v1.62.0", nil },
			wantErr: `reading the ptah-compat build record: .+`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			bin := filepath.Join(c.TempDir(), "atlas")
			c.Assert(os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o600), qt.IsNil)

			got, err := sqliteDriverLink(bin, test.harness)

			c.Assert(err, qt.ErrorMatches, test.wantErr)
			c.Assert(got, qt.Equals, "")
		})
	}
}

// TestListSQLiteModule_FailurePath: a go command that cannot run says so.
func TestListSQLiteModule_FailurePath(t *testing.T) {
	c := qt.New(t)

	got, err := listSQLiteModule(filepath.Join(c.TempDir(), "go"))

	c.Assert(err, qt.ErrorMatches, `reading the linked modernc\.org/sqlite module: .+`)
	c.Assert(got, qt.Equals, "")
}

// TestParseSQLiteModuleList_HappyPath reads the version the go command lists.
func TestParseSQLiteModuleList_HappyPath(t *testing.T) {
	c := qt.New(t)

	got, err := parseSQLiteModuleList([]byte(`{"Path": "modernc.org/sqlite", "Version": "v1.62.0"}`))

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.Equals, "v1.62.0")
}

// TestParseSQLiteModuleList_FailurePath: a listing that names no engine
// release is refused.
func TestParseSQLiteModuleList_FailurePath(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{
			name:    "not JSON",
			payload: `go: not a module`,
			wantErr: `decoding the modernc\.org/sqlite module list: .+`,
		},
		{
			name:    "replaced module",
			payload: `{"Path": "modernc.org/sqlite", "Version": "v1.62.0", "Replace": {"Path": "../sqlite"}}`,
			wantErr: `this module replaces modernc\.org/sqlite`,
		},
		{
			name:    "no version",
			payload: `{"Path": "modernc.org/sqlite"}`,
			wantErr: `this module lists no modernc\.org/sqlite version`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			got, err := parseSQLiteModuleList([]byte(test.payload))

			c.Assert(err, qt.ErrorMatches, test.wantErr)
			c.Assert(got, qt.Equals, "")
		})
	}
}

// TestTxtarRunSummaryResults_KeepsWhatTheRefusalDoesNotExplainRed: a recognized
// refusal is reported OK beside a failure of the same fixture, which stays red
// under the issue that records the decision.
func TestTxtarRunSummaryResults_KeepsWhatTheRefusalDoesNotExplainRed(t *testing.T) {
	c := qt.New(t)
	const name = "internal/integration/testdata/sqlite/cli-migrate-lint-project.txtar"

	got := txtarRunSummary{
		engineRefusals: []string{"refused by the engine"},
		failures:       []string{"unrelated failure"},
	}.results(Fixture{Name: name})

	c.Assert(got, qt.DeepEquals, []Result{
		{"txtar-script", name, "script-runtime", OK, "refused by the engine", "stokaro/ptah#5952"},
		{"txtar-script", name, "script-runtime", Fail, "unrelated failure", "stokaro/ptah#5952"},
	})
}
