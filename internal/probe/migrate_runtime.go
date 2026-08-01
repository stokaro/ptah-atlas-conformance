package probe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stokaro/ptah/dbschema"

	_ "modernc.org/sqlite"
)

const migrateRuntimeProbeName = "migrate-runtime"

type migrateRuntimeCheck func(string) Result

type sqliteRevisionFact struct {
	Version         string
	Description     string
	Applied         int
	Total           int
	OperatorVersion string
}

type migrateRuntimeTarget struct {
	Label string
	URL   string
}

// RunMigrateRuntime runs live migration-runtime conformance checks against
// deterministic local databases. These checks inspect database state directly,
// rather than treating successful CLI exit as sufficient evidence. Atlas-form
// commands run on the ptah-compat binary (the only Atlas-shaped surface since
// stokaro/ptah#850); Ptah-native `migrations ...` checks run on the main
// `ptah` binary.
func RunMigrateRuntime() []Result {
	compatBin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{migrateRuntimeProbeName, "atlas migrate", "build", Fail,
			"could not build the Ptah compatibility CLI to probe migrate runtime behavior: " + oneLine(err.Error()), ""}}
	}
	nativeBin, err := ptahBinary()
	if err != nil {
		return []Result{{migrateRuntimeProbeName, "ptah migrations", "build", Fail,
			"could not build the Ptah CLI to probe migrate runtime behavior: " + oneLine(err.Error()), ""}}
	}

	checks := []migrateRuntimeCheck{
		func(bin string) Result {
			return atlasProjectConfigApplyOracle(bin, DefaultAtlasBinary())
		},
		sqliteMigrateApplyRecordsState,
		sqliteMigrateApplyDryRunReadsStoredState,
		func(string) Result { return sqliteNativeMigrateUpDryRunReadsStoredState(nativeBin) },
		sqliteMigrateApplyDryRunLeavesFreshTargetUninitialized,
		sqliteMigrateDownDryRunReadsStoredState,
		sqliteMigrateDownMissingBodyPreservesState,
		sqliteMigrateTxModeAllDiagnosticUsesAvailableFlags,
		sqliteMigrateSetRepairsRevisionState,
		sqliteMigrateApplyTxModeAllRollsBack,
		sqliteMigrateApplyTxModeFileKeepsPriorFiles,
		sqliteMigrateApplyTxModeNoneKeepsPartialStatement,
		sqliteMigrateApplyTxtarChecksGate,
		sqliteMigrateApplyTxtarChecksErrorGate,
		sqliteMigrateStatusToleratesRevisionMetadataRow,
		sqliteMigrateDownFailureLeavesRevisionsIntact,
		func(string) Result { return migrationsImportGolangMigrate(nativeBin) },
		func(string) Result { return migrationsImportGoose(nativeBin) },
		func(string) Result { return migrationsImportFlyway(nativeBin) },
		func(string) Result { return migrationsImportLiquibase(nativeBin) },
		func(string) Result { return lintSarifShape(nativeBin) },
	}
	for _, target := range configuredMigrateRuntimeTargets(os.Getenv) {
		switch target.Label {
		case "postgres":
			checks = append(checks,
				func(bin string) Result { return postgresMigrateApplyCustomRevisionsSchema(bin, target.URL) },
				func(bin string) Result { return postgresMigrateApplyDryRunReadsStoredState(bin, target.URL) },
				func(bin string) Result { return postgresMigrateNoTransactionConcurrentIndex(bin, target.URL) },
				func(string) Result { return postgresGenerateDiffSkipDropTable(nativeBin, target.URL) },
			)
			pgURL := target.URL
			for i := range planningCatalog {
				c := planningCatalog[i]
				checks = append(checks, func(bin string) Result { return c.runPostgres(bin, pgURL) })
			}
		case "mysql":
			checks = append(checks,
				func(bin string) Result { return mysqlMigrateApplyRecordsState(bin, target.URL) },
				func(bin string) Result { return mysqlMigrateApplyDryRunReadsStoredState(bin, target.URL) },
			)
		}
	}
	out := make([]Result, 0, len(checks))
	for _, check := range checks {
		out = append(out, check(compatBin))
	}
	return out
}

func configuredMigrateRuntimeTargets(getenv func(string) string) []migrateRuntimeTarget {
	targets := []struct{ label, env string }{
		{"postgres", "CONFORMANCE_POSTGRES_URL"},
		{"mysql", "CONFORMANCE_MYSQL_URL"},
	}
	configured := make([]migrateRuntimeTarget, 0, len(targets))
	for _, target := range targets {
		if url := getenv(target.env); url != "" {
			configured = append(configured, migrateRuntimeTarget{Label: target.label, URL: url})
		}
	}
	return configured
}

func sqliteMigrateApplyRecordsState(bin string) Result {
	const fixture = "sqlite/apply-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "apply.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "second", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	status, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "status", status, err)
	}
	if !strings.Contains(status, "Applied Migrations: 2") || !strings.Contains(status, "Pending Migrations: 0") {
		return migrateRuntimeGap(fixture, "status", "status did not report 2 applied and 0 pending migrations: "+oneLine(status))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply created expected SQLite tables, Atlas revision rows, and applied status", ""}
}

func sqliteMigrateSetRepairsRevisionState(bin string) Result {
	const fixture = "sqlite/set-repair-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "set.db")
	output, err := commandOutput(bin, []string{
		"migrate", "set", "1",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "set", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 0, Total: 0, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	output, err = commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "pets"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 0, Total: 0, OperatorVersion: "Ptah"},
		{Version: "2", Description: "second", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"set recorded repair state and apply executed only the remaining migration", ""}
}

func sqliteMigrateApplyTxModeAllRollsBack(bin string) Result {
	return sqliteMigrateApplyTxMode(bin, "sqlite/tx-mode-all", "all", []string{"atlas_schema_revisions"})
}

func sqliteMigrateApplyTxModeFileKeepsPriorFiles(bin string) Result {
	return sqliteMigrateApplyTxMode(bin, "sqlite/tx-mode-file", "file", []string{"atlas_schema_revisions", "pets", "users"})
}

func sqliteMigrateApplyTxModeNoneKeepsPartialStatement(bin string) Result {
	return sqliteMigrateApplyTxMode(bin, "sqlite/tx-mode-none", "none", []string{"atlas_schema_revisions", "broken", "pets", "users"})
}

func sqliteMigrateApplyTxMode(bin, fixture, mode string, wantTables []string) Result {
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
		"3_third.sql":  "CREATE TABLE broken (id INTEGER);\nTHIS IS A FAILING STATEMENT;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, mode+".db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--tx-mode", mode,
	})
	if err == nil {
		return migrateRuntimeGap(fixture, "apply", "broken migration unexpectedly succeeded")
	}
	if !strings.Contains(output, "THIS IS A FAILING STATEMENT") {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteTables(db, wantTables); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"`--tx-mode " + mode + "` leaves the expected SQLite state after a failed migration", ""}
}

// migrateRuntimeStatusFacts holds the three numbers both binaries report from
// `migrate status`, parsed out of whichever vocabulary the binary uses.
type migrateRuntimeStatusFacts struct {
	Current string
	Applied int
	Pending int
}

// parseMigrateRuntimeStatus reads status facts from either spelling.
//
// Ptah prints "Current Version:", "Applied Migrations:" and "Pending
// Migrations:"; the pinned Atlas CE v1.2.0 prints "-- Current Version:",
// "-- Executed Files:" and "-- Pending Files:" for the same database. Asserting
// Ptah's wording would pin a Ptah-ism and go red the day the compat surface
// aligns with Atlas, so probes compare the parsed values instead.
//
// Parsing also makes the comparison exact. `strings.Contains(status,
// "Current Version: 2")` is satisfied by "Current Version: 20260801000002" —
// the pre-fix output recorded in stokaro/ptah#957 — so a substring check cannot
// tell the fix from the bug it pins.
func parseMigrateRuntimeStatus(output string) (migrateRuntimeStatusFacts, string) {
	facts := migrateRuntimeStatusFacts{Applied: -1, Pending: -1}
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "--"))
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Current Version":
			facts.Current = value
		case "Applied Migrations", "Executed Files":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return facts, fmt.Sprintf("unparsable applied count %q", value)
			}
			facts.Applied = parsed
		case "Pending Migrations", "Pending Files":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return facts, fmt.Sprintf("unparsable pending count %q", value)
			}
			facts.Pending = parsed
		}
	}
	if facts.Current == "" || facts.Applied < 0 || facts.Pending < 0 {
		return facts, "status output did not carry current/applied/pending facts in either vocabulary: " + oneLine(output)
	}
	return facts, ""
}

// compareMigrateRuntimeStatus checks parsed status facts against expectations.
func compareMigrateRuntimeStatus(output, wantCurrent string, wantApplied, wantPending int) string {
	facts, detail := parseMigrateRuntimeStatus(output)
	if detail != "" {
		return detail
	}
	if facts.Current != wantCurrent || facts.Applied != wantApplied || facts.Pending != wantPending {
		return fmt.Sprintf("status = current %q, applied %d, pending %d; want current %q, applied %d, pending %d",
			facts.Current, facts.Applied, facts.Pending, wantCurrent, wantApplied, wantPending)
	}
	return ""
}

// sqliteMigrateApplyTxtarChecksGate proves an Atlas txtar checks.sql section is
// enforced as a pre-migration gate on the compat surface (stokaro/ptah#956): a
// failing assertion must abort the apply with exit 1 before any migration.sql
// statement runs, and must record nothing, so the retry after fixing the data
// works with no flags.
//
// Those are the Atlas-observable facts. The measured licensed Atlas build
// (v1.2.4) prints `Error: check assertion "..." returned false`, so Ptah's
// "checks.sql#N ... was not satisfied" wording is NOT Atlas parity; the token
// assertion below is a Ptah self-consistency pin that the abort names the
// failing check. Atlas CE, by contrast, executes the section as plain SQL and
// applies anyway.
func sqliteMigrateApplyTxtarChecksGate(bin string) Result {
	const fixture = "sqlite/txtar-checks-gate"
	const issue = "stokaro/ptah#956"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY);\nINSERT INTO users (id) VALUES (1);\n",
		// The assertion text matches the one recorded in stokaro/ptah#956.
		"2_second.sql": "-- atlas:txtar\n\n" +
			"-- checks.sql --\n" +
			"SELECT NOT EXISTS (SELECT 1 FROM users);\n\n" +
			"-- migration.sql --\n" +
			"ALTER TABLE users ADD COLUMN email TEXT;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "checks.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err == nil {
		return Result{migrateRuntimeProbeName, fixture, "apply", Gap,
			"failing txtar checks.sql unexpectedly applied: " + oneLine(output), issue}
	}
	if detail := compareProcessExitCode(err, 1); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "apply", Gap,
			detail + ": " + oneLine(output), issue}
	}
	// Ptah self-consistency, not Atlas wording: see the function comment.
	if !strings.Contains(output, "checks.sql#1") || !strings.Contains(output, "was not satisfied") {
		return Result{migrateRuntimeProbeName, fixture, "apply", Gap,
			"Ptah's abort did not name the failing checks.sql assertion: " + oneLine(output), issue}
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	var emailColumns int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pragma_table_info('users') WHERE name = 'email'`).Scan(&emailColumns); err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	if emailColumns != 0 {
		return Result{migrateRuntimeProbeName, fixture, "inspect", Gap,
			"migration.sql body ran despite the failing checks.sql gate: users.email exists", issue}
	}
	// The blocked migration must leave no revision row at all: checks run before
	// any bookkeeping write, so the apply is recorded as never started. Atlas
	// behaves the same way, and it is what lets the retry below work with no
	// flags. The compat surface does register --allow-dirty, but that flag
	// cannot clear a dirty row: the retry fails on the revision re-insert with
	// a UNIQUE violation (stokaro/ptah#966), so a recorded failure here would
	// leave no working in-band recovery.
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 2, Total: 2, OperatorVersion: "Ptah"},
	}); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "revisions", Gap, detail, issue}
	}

	// Recovery half: once the guarded data is fixed, the retry succeeds with no
	// flags and no revision repair.
	if _, err := db.ExecContext(context.Background(), "DELETE FROM users"); err != nil {
		return migrateRuntimeFail(fixture, "fix-data", err)
	}
	retry, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return Result{migrateRuntimeProbeName, fixture, "retry", Gap,
			"retry after fixing the checked data did not apply: " + oneLine(retry), issue}
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pragma_table_info('users') WHERE name = 'email'`).Scan(&emailColumns); err != nil {
		return migrateRuntimeFail(fixture, "retry", err)
	}
	if emailColumns != 1 {
		return Result{migrateRuntimeProbeName, fixture, "retry", Gap,
			"retry reported success but did not add users.email", issue}
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"failing txtar checks.sql aborted the apply before the body (exit 1, names checks.sql#1, no schema change, no revision row) and the retry after fixing the data succeeded", ""}
}

// sqliteMigrateApplyTxtarChecksErrorGate covers the other branch of the check
// engine: an assertion that cannot RUN, rather than one that runs and returns
// false. It fails closed with a different message ("could not run"), which the
// falsy-assertion probe's tokens would not match, and it must block the body
// and record nothing just the same (stokaro/ptah#956).
func sqliteMigrateApplyTxtarChecksErrorGate(bin string) Result {
	const fixture = "sqlite/txtar-checks-error-gate"
	const issue = "stokaro/ptah#956"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		// The assertion references a table that does not exist, so the query
		// itself errors instead of returning a falsy scalar.
		"2_second.sql": "-- atlas:txtar\n\n" +
			"-- checks.sql --\n" +
			"SELECT NOT EXISTS (SELECT 1 FROM table_that_does_not_exist);\n\n" +
			"-- migration.sql --\n" +
			"ALTER TABLE users ADD COLUMN email TEXT;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "checks-error.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err == nil {
		return Result{migrateRuntimeProbeName, fixture, "apply", Gap,
			"an erroring checks.sql assertion did not block the migration: " + oneLine(output), issue}
	}
	if detail := compareProcessExitCode(err, 1); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "apply", Gap,
			detail + ": " + oneLine(output), issue}
	}
	// Ptah self-consistency, not Atlas wording: the erroring branch must be
	// distinguishable from the falsy branch, which says "was not satisfied".
	if !strings.Contains(output, "could not run") {
		return Result{migrateRuntimeProbeName, fixture, "apply", Gap,
			"an erroring assertion did not report that the check could not run: " + oneLine(output), issue}
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	var emailColumns int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pragma_table_info('users') WHERE name = 'email'`).Scan(&emailColumns); err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	if emailColumns != 0 {
		return Result{migrateRuntimeProbeName, fixture, "inspect", Gap,
			"migration.sql body ran despite the erroring checks.sql gate: users.email exists", issue}
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "revisions", Gap, detail, issue}
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"an erroring checks.sql assertion failed closed: exit 1, the body did not run, and no revision row was recorded", ""}
}

// migrateRuntimeRollbackConfirmation is what `ptah-compat migrate down` reads
// from stdin at its confirmation prompt. Atlas's own `migrate down` has no
// prompt at all, which is tracked as a drop-in divergence in
// stokaro/ptah#971; supplying it here only stops the probe from measuring the
// prompt instead of the rollback.
const migrateRuntimeRollbackConfirmation = "YES\n"

// migrateRuntimePromptAbortMarker appears when the child reached the prompt and
// found no answer. Any probe that sees it measured nothing.
const migrateRuntimePromptAbortMarker = "read rollback confirmation"

// migrateRuntimeFailingDownStatement is the deliberately invalid statement in
// the failed-down fixture's down.sql. Ptah echoes the failing statement in its
// error, so seeing it is proof the down body actually executed rather than the
// run failing earlier for an unrelated reason.
const migrateRuntimeFailingDownStatement = "THIS IS A FAILING STATEMENT"

// sqliteMigrateDownFailureLeavesRevisionsIntact pins the Atlas-shaped surface's
// failed-down bookkeeping (stokaro/ptah#957): measured against Atlas CLI v1.2.4,
// a down whose statement fails rolls the body back and leaves the revision rows
// byte-identical, so `atlas migrate status` and `ptah-compat migrate status`
// agree that the version is still applied.
//
// The database also carries an Atlas Pro `.atlas_cloud_identifier` row, which
// is the shape a real Pro user's database has when this happens: Pro writes
// that row on `migrate down`, so a failed down and a dot row co-occur.
func sqliteMigrateDownFailureLeavesRevisionsIntact(bin string) Result {
	const fixture = "sqlite/down-failure-revisions"
	const issue = "stokaro/ptah#957"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "-- atlas:txtar\n\n" +
			"-- migration.sql --\n" +
			"CREATE TABLE pets (id INTEGER PRIMARY KEY);\n\n" +
			"-- down.sql --\n" +
			"DROP TABLE pets;\n" +
			migrateRuntimeFailingDownStatement + ";\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "down.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if err := seedAtlasCloudIdentifierRow(db, "d66dcf67-8da4-4996-8157-926c77d073be"); err != nil {
		return migrateRuntimeFail(fixture, "seed-metadata-row", err)
	}
	before, err := projectConfigRevisionMetadataFacts(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}

	down, err := commandOutputStdin(bin, []string{
		"migrate", "down",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--to-version", "1",
	}, migrateRuntimeRollbackConfirmation)
	if err == nil {
		return Result{migrateRuntimeProbeName, fixture, "down", Gap,
			"the broken down migration unexpectedly succeeded: " + oneLine(down), issue}
	}
	// Liveness guards. Several unrelated failures also exit 1 while writing
	// nothing, which would make every assertion below trivially true:
	//   - an unanswered confirmation prompt (no stdin),
	//   - a down.sql section that is missing entirely, which fails with
	//     "no Atlas down migration" before any statement runs.
	// "Rolling back migration" is logged before the body executes, so it is not
	// enough on its own; the down body is only proven to have run by the failing
	// statement appearing in the error.
	if strings.Contains(down, migrateRuntimePromptAbortMarker) {
		return migrateRuntimeFail(fixture, "down",
			fmt.Errorf("the rollback aborted at the confirmation prompt, so nothing was measured: %s", oneLine(down)))
	}
	if strings.Contains(down, "no Atlas down migration") {
		return migrateRuntimeFail(fixture, "down",
			fmt.Errorf("the fixture lost its down.sql section, so no down body ran: %s", oneLine(down)))
	}
	if !strings.Contains(down, migrateRuntimeFailingDownStatement) {
		return migrateRuntimeFail(fixture, "down",
			fmt.Errorf("the down body did not run to its failing statement, so nothing was measured: %s", oneLine(down)))
	}
	if detail := compareProcessExitCode(err, 1); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "down", Gap,
			detail + ": " + oneLine(down), issue}
	}

	after, err := projectConfigRevisionMetadataFacts(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	if !slices.Equal(before, after) {
		return Result{migrateRuntimeProbeName, fixture, "revisions", Gap,
			fmt.Sprintf("failed down rewrote revision rows: before %v, after %v", before, after), issue}
	}

	status, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "status", status, err)
	}
	if detail := compareMigrateRuntimeStatus(status, "2", 2, 0); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "status", Gap,
			"after the failed down, " + detail, issue}
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"a failed down left all 12 Atlas revision columns byte-identical, including an Atlas Pro metadata row, and status still reports the version applied, matching Atlas", ""}
}

// seedAtlasCloudIdentifierRow inserts the measured Atlas Pro metadata row:
// UUID description, applied=0, total=0, empty hash, NULL error/error_stmt and
// partial_hashes. Pro writes one of these on `migrate down` even in purely
// local mode.
func seedAtlasCloudIdentifierRow(db *sql.DB, identifier string) error {
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO atlas_schema_revisions
(version, description, type, applied, total, executed_at, execution_time, error, error_stmt, hash, partial_hashes, operator_version)
VALUES (?, ?, 2, 0, 0, '2026-08-01 12:04:21.291103+02:00', 0, NULL, NULL, '', NULL, 'Atlas CLI v1.2.4-e282f76-canary')`,
		"."+identifierSuffix(identifier), identifier)
	return err
}

// identifierSuffix keeps the seeded versions distinct while all starting with
// the dot that marks them as metadata. The first one keeps Atlas's own
// `.atlas_cloud_identifier` name.
func identifierSuffix(identifier string) string {
	if identifier == "d66dcf67-8da4-4996-8157-926c77d073be" || identifier == "472fecf4-5a9c-431f-8ff1-8e1facd1d50b" {
		return "atlas_cloud_identifier"
	}
	return "atlas_" + identifier
}

// sqliteMigrateStatusToleratesRevisionMetadataRow proves the dot-prefixed
// metadata row Atlas Pro `migrate down` writes (`.atlas_cloud_identifier`,
// inserted even in purely local mode) does not break Ptah's revision readers
// (stokaro/ptah#957): status stays clean, version math skips the row, and the
// row survives byte-identically.
func sqliteMigrateStatusToleratesRevisionMetadataRow(bin string) Result {
	const fixture = "sqlite/revision-metadata-row"
	const issue = "stokaro/ptah#957"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "metadata.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	// Two dot rows, not one: the readers must skip every metadata row, and a
	// single-row probe cannot tell "skips dot rows" from "skips one dot row".
	if err := seedAtlasCloudIdentifierRow(db, "472fecf4-5a9c-431f-8ff1-8e1facd1d50b"); err != nil {
		return migrateRuntimeFail(fixture, "seed-metadata-row", err)
	}
	if err := seedAtlasCloudIdentifierRow(db, "9b1c0f52-2f77-4a1e-9a0e-2f2f1b7c4d10"); err != nil {
		return migrateRuntimeFail(fixture, "seed-metadata-row", err)
	}
	rowsBefore, err := sqliteMetadataRowLiterals(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "seed-metadata-row", err)
	}
	if len(rowsBefore) != 2 {
		return migrateRuntimeFail(fixture, "seed-metadata-row",
			fmt.Errorf("seeded metadata rows = %d, want 2", len(rowsBefore)))
	}

	status, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return Result{migrateRuntimeProbeName, fixture, "status", Gap,
			"status aborted on the metadata rows: " + oneLine(status), issue}
	}
	if detail := compareMigrateRuntimeStatus(status, "2", 2, 0); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "status", Gap,
			"status math did not skip the metadata rows: " + detail, issue}
	}

	// The write path stokaro/ptah#957 explicitly asks about: `migrate set`
	// deletes revisions above its target, and must not take the dot rows with
	// them.
	set, err := commandOutput(bin, []string{
		"migrate", "set", "1",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return Result{migrateRuntimeProbeName, fixture, "set", Gap,
			"migrate set aborted with metadata rows present: " + oneLine(set), issue}
	}
	setStatus, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "set", setStatus, err)
	}
	if detail := compareMigrateRuntimeStatus(setStatus, "1", 1, 1); detail != "" {
		return Result{migrateRuntimeProbeName, fixture, "set", Gap,
			"after migrate set 1, " + detail, issue}
	}

	// `migrate set` rewinds revision history only; the objects migration 2
	// created are still there. Drop the table so replaying it is meaningful,
	// which is also what an operator rewinding history by hand would do.
	if _, err := db.ExecContext(context.Background(), "DROP TABLE pets"); err != nil {
		return migrateRuntimeFail(fixture, "reapply", err)
	}

	// Re-apply the remainder without a migration transaction, so the dot rows
	// are also exercised against the non-transactional write path.
	reapply, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--tx-mode", "none",
	})
	if err != nil {
		return Result{migrateRuntimeProbeName, fixture, "reapply", Gap,
			"tx-mode none apply aborted with metadata rows present: " + oneLine(reapply), issue}
	}

	rowsAfter, err := sqliteMetadataRowLiterals(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	if !slices.Equal(rowsBefore, rowsAfter) {
		return Result{migrateRuntimeProbeName, fixture, "inspect", Gap,
			fmt.Sprintf("metadata rows changed: before %v, after %v", rowsBefore, rowsAfter), issue}
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"status, migrate set and a tx-mode none apply all skip two `.atlas_cloud_identifier` metadata rows in their version math, and both rows survive byte-identically", ""}
}

// sqliteMetadataRowLiterals renders every dot-prefixed metadata row as one
// quote()-rendered tuple over all 12 columns, so survival comparisons are
// byte-precise including NULL versus the empty string.
//
// It returns a slice rather than scanning a single row: a write path that
// deleted the rows then shows up as a shorter slice — a legible gap naming
// what changed — instead of sql.ErrNoRows, which the harness would report as
// an opaque `fail`. Returning all of them also stops a second metadata row
// from being silently ignored.
func sqliteMetadataRowLiterals(db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(context.Background(),
		`SELECT quote(version) || '|' || quote(description) || '|' || quote(type) || '|' ||
quote(applied) || '|' || quote(total) || '|' || quote(executed_at) || '|' ||
quote(execution_time) || '|' || quote(error) || '|' || quote(error_stmt) || '|' ||
quote(hash) || '|' || quote(partial_hashes) || '|' || quote(operator_version)
FROM atlas_schema_revisions WHERE version LIKE '.%' ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var literals []string
	for rows.Next() {
		var literal string
		if err := rows.Scan(&literal); err != nil {
			return nil, err
		}
		literals = append(literals, literal)
	}
	return literals, rows.Err()
}

func postgresMigrateApplyCustomRevisionsSchema(bin, dbURL string) Result {
	const fixture = "postgres/custom-revisions-schema"
	schema := migrateRuntimeIdentifier("ptah_rt_pg_schema")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE " + quotePostgresIdentifier(schema) + ".users (id integer PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	if result := cleanupPostgresRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupPostgresRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck

	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := comparePostgresRelations(conn, schema, []string{"atlas_schema_revisions", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := comparePostgresRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply created expected PostgreSQL schema objects and Atlas revision rows in a custom revisions schema", ""}
}

func postgresMigrateNoTransactionConcurrentIndex(bin, dbURL string) Result {
	const fixture = "postgres/no-transaction-concurrent-index"
	schema := migrateRuntimeIdentifier("ptah_rt_pg_notx")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE " + quotePostgresIdentifier(schema) + ".users (id integer PRIMARY KEY, email text);\n",
		"2_index.sql": "-- atlas:txmode none\n" +
			"CREATE INDEX CONCURRENTLY users_email_idx ON " + quotePostgresIdentifier(schema) + ".users (email);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	if result := cleanupPostgresRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupPostgresRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck

	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := comparePostgresIndexes(conn, schema, "users", []string{"users_email_idx", "users_pkey"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := comparePostgresRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "index", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"`-- atlas:txmode none` applied PostgreSQL CREATE INDEX CONCURRENTLY outside the migration transaction", ""}
}

// postgresGenerateDiffSkipDropTable exercises the ptah.yaml diff policy end to
// end: given a database that still has a table the desired schema drops, plus a
// `diff.skip: [drop_table]` policy, `ptah migrations generate` must omit the
// DROP TABLE statement, record the omission as a comment, and still emit the
// non-skipped change (an added column). This is Atlas-OSS diff-policy parity
// (stokaro/ptah#668).
func postgresGenerateDiffSkipDropTable(bin, dbURL string) Result {
	const fixture = "postgres/generate-diff-skip-drop-table"
	schema := migrateRuntimeIdentifier("ptah_rt_pg_diffskip")

	if result := cleanupPostgresRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupPostgresRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck
	if result := seedPostgresDiffSkipSchema(dbURL, schema, fixture); result != nil {
		return *result
	}

	root, cleanup, err := diffSkipWorkdir(schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()

	output, err := commandOutput(bin, []string{
		"migrations", "generate",
		"--config", filepath.Join(root, "ptah.yaml"),
		"--root-dir", filepath.Join(root, "models"),
		"--db-url", dbURL,
		"--migrations-dir", filepath.Join(root, "out"),
		"--schemas", schema,
		"--name", "diffskip",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "generate", output, err)
	}

	up, err := readGeneratedUpSQL(filepath.Join(root, "out"))
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	// The DROP TABLE statement always carries IF EXISTS; the omission comment
	// mentions "DROP TABLE" too, so match the statement form specifically.
	if strings.Contains(up, "DROP TABLE IF EXISTS") {
		return migrateRuntimeGap(fixture, "generate", "skipped DROP TABLE was still emitted: "+oneLine(up))
	}
	if !strings.Contains(up, "omitted by diff policy") {
		return migrateRuntimeGap(fixture, "generate", "skip omission comment was not emitted: "+oneLine(up))
	}
	if !strings.Contains(up, "ADD COLUMN") {
		return migrateRuntimeGap(fixture, "generate", "non-skipped ADD COLUMN change was not emitted: "+oneLine(up))
	}
	return Result{migrateRuntimeProbeName, fixture, "generate", OK,
		"`diff.skip: [drop_table]` omitted the DROP TABLE, recorded the omission comment, and kept the ADD COLUMN change", ""}
}

func seedPostgresDiffSkipSchema(dbURL, schema, fixture string) *Result {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		result := migrateRuntimeFail(fixture, "seed", err)
		return &result
	}
	defer func() { _ = conn.Close() }()
	stmts := []string{
		"CREATE SCHEMA " + quotePostgresIdentifier(schema),
		"CREATE TABLE " + quotePostgresIdentifier(schema) + ".legacy (id integer PRIMARY KEY)",
		"CREATE TABLE " + quotePostgresIdentifier(schema) + ".keep (id integer PRIMARY KEY)",
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			result := migrateRuntimeFail(fixture, "seed", err)
			return &result
		}
	}
	return nil
}

// diffSkipWorkdir writes a Go entity describing the desired schema (the kept
// table gains a note column; the legacy table is absent) and a ptah.yaml that
// skips table drops.
func diffSkipWorkdir(schema string) (string, func(), error) {
	root, err := os.MkdirTemp("", "diff-skip-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	models := filepath.Join(root, "models")
	if err := os.MkdirAll(filepath.Join(root, "out"), 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.MkdirAll(models, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	model := "package models\n\n" +
		"//ptah:schema:table name=\"keep\" schema=\"" + schema + "\"\n" +
		"type Keep struct {\n" +
		"\t//ptah:schema:field name=\"id\" type=\"INTEGER\" primary=\"true\"\n" +
		"\tID int\n" +
		"\t//ptah:schema:field name=\"note\" type=\"TEXT\"\n" +
		"\tNote string\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(models, "models.go"), []byte(model), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "ptah.yaml"), []byte("diff:\n  skip: [drop_table]\n"), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return root, cleanup, nil
}

func readGeneratedUpSQL(outDir string) (string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return "", err
	}
	var sql strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(outDir, entry.Name()))
		if err != nil {
			return "", err
		}
		sql.Write(data)
		sql.WriteByte('\n')
	}
	if sql.Len() == 0 {
		return "", errors.New("no generated .up.sql file was produced")
	}
	return sql.String(), nil
}

func mysqlMigrateApplyRecordsState(bin, dbURL string) Result {
	const fixture = "mysql/apply-state"
	schema := migrateRuntimeIdentifier("ptah_rt_mysql")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE " + quoteMySQLIdentifier(schema) + "." + quoteMySQLIdentifier("users") + " (id integer PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE " + quoteMySQLIdentifier(schema) + "." + quoteMySQLIdentifier("pets") + " (id integer PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	if result := cleanupMySQLRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupMySQLRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck

	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := compareMySQLTables(conn, schema, []string{"atlas_schema_revisions", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareMySQLRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "first", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "second", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply created expected MySQL tables and Atlas revision rows", ""}
}

// migrationsImportGolangMigrate exercises `ptah migrations import` end to end:
// a golang-migrate source directory is converted to Ptah's native format and the
// result must pass `ptah migrations validate`. This is Atlas OSS `migrate import`
// parity (stokaro/ptah#667). It needs no database.
func migrationsImportGolangMigrate(bin string) Result {
	return importRoundtripProbe(bin, "golang-migrate/import-roundtrip", map[string]string{
		"1_init.up.sql":      "CREATE TABLE users (id integer PRIMARY KEY);\n",
		"1_init.down.sql":    "DROP TABLE users;\n",
		"2_add_email.up.sql": "ALTER TABLE users ADD email text;\n", // no down -> placeholder
	}, 4, "golang-migrate import produced Ptah up/down pairs and a ptah.sum that validate accepts")
}

func migrationsImportGoose(bin string) Result {
	return importRoundtripProbe(bin, "goose/import-roundtrip", map[string]string{
		"20230101_init.sql": "-- +goose Up\n" +
			"-- +goose StatementBegin\nCREATE TABLE users (id integer PRIMARY KEY);\n-- +goose StatementEnd\n" +
			"-- +goose Down\nDROP TABLE users;\n",
		"20230102_add_email.sql": "-- +goose Up\nALTER TABLE users ADD email text;\n", // no down -> placeholder
	}, 4, "goose import produced Ptah up/down pairs (StatementBegin/End stripped) and a ptah.sum that validate accepts")
}

// migrationsImportFlyway exercises the Flyway-specific parts of the importer: a
// dotted version (V1.1, reassigned to a sequential Ptah version), an undo file
// (U1, imported as the versioned migration's down), and a repeatable (R__,
// imported as a one-time migration ordered last). Three source migrations ->
// six Ptah files.
func migrationsImportFlyway(bin string) Result {
	return importRoundtripProbe(bin, "flyway/import-roundtrip", map[string]string{
		"V1__init.sql":        "CREATE TABLE users (id integer PRIMARY KEY);\n",
		"U1__init.sql":        "DROP TABLE users;\n",                                 // undo -> down for V1
		"V1.1__add_email.sql": "ALTER TABLE users ADD email text;\n",                 // dotted -> remap; no undo -> placeholder
		"R__active_users.sql": "CREATE VIEW active_users AS SELECT id FROM users;\n", // repeatable -> one-time
	}, 6, "flyway import mapped dotted versions, paired the undo as a down, and imported the repeatable as a one-time migration that validate accepts")
}

// migrationsImportLiquibase exercises the Liquibase formatted-SQL parser: a
// single changelog whose `--changeset author:id` markers become migrations
// (sequential Ptah versions, author:id in the name) and whose `--rollback` lines
// become the down. Two changesets -> four Ptah files.
func migrationsImportLiquibase(bin string) Result {
	return importRoundtripProbe(bin, "liquibase/import-roundtrip", map[string]string{
		"changelog.sql": "--liquibase formatted sql\n" +
			"--changeset alice:create-users\n" +
			"CREATE TABLE users (id integer PRIMARY KEY);\n" +
			"--rollback DROP TABLE users;\n" +
			"--changeset bob:add-email\n" +
			"ALTER TABLE users ADD email text;\n", // no rollback -> placeholder down
	}, 4, "liquibase import split formatted-SQL changesets into Ptah up/down pairs (rollback as down) that validate accepts")
}

// importRoundtripProbe writes a source migration directory, runs
// `ptah migrations import` on it, and asserts the emitted Ptah directory passes
// `ptah migrations validate` and produces expectedFiles up/down files. It needs
// no database.
func importRoundtripProbe(bin, fixture string, sourceFiles map[string]string, expectedFiles int, okDetail string) Result {
	root, err := os.MkdirTemp("", "import-*")
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()

	source := filepath.Join(root, "source")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(source, 0o755); err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	for name, sql := range sourceFiles {
		if err := os.WriteFile(filepath.Join(source, name), []byte(sql), 0o600); err != nil {
			return migrateRuntimeFail(fixture, "setup", err)
		}
	}

	if output, err := commandOutput(bin, []string{
		"migrations", "import", "--source-dir", source, "--migrations-dir", out,
	}); err != nil {
		return migrateRuntimeExit(fixture, "import", output, err)
	}

	validateOut, err := commandOutput(bin, []string{"migrations", "validate", "--dir", out})
	if err != nil {
		return migrateRuntimeExit(fixture, "validate", validateOut, err)
	}
	if !strings.Contains(validateOut, "matches ptah.sum") {
		return migrateRuntimeGap(fixture, "validate", "imported directory did not validate: "+oneLine(validateOut))
	}

	imported, err := os.ReadDir(out)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	sqlFiles := 0
	for _, entry := range imported {
		if strings.HasSuffix(entry.Name(), ".sql") {
			sqlFiles++
		}
	}
	// Each source migration -> an up + a down Ptah file (a missing down is
	// filled with a placeholder).
	if sqlFiles != expectedFiles {
		return migrateRuntimeGap(fixture, "inspect", fmt.Sprintf("expected %d imported .sql files, got %d", expectedFiles, sqlFiles))
	}
	return Result{migrateRuntimeProbeName, fixture, "import", OK, okDetail, ""}
}

func migrateRuntimeDir(files map[string]string) (string, string, func(), error) {
	root, err := os.MkdirTemp("", "migrate-runtime-*")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	migrations := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		cleanup()
		return "", "", nil, err
	}
	for name, sql := range files {
		if err := os.WriteFile(filepath.Join(migrations, name), []byte(sql), 0o600); err != nil {
			cleanup()
			return "", "", nil, err
		}
	}
	return root, migrations, cleanup, nil
}

func migrateRuntimeHash(bin, migrations, fixture string) *Result {
	output, err := commandOutput(bin, []string{"migrate", "hash", "--dir", fileURL(migrations)})
	if err != nil {
		result := migrateRuntimeExit(fixture, "hash", output, err)
		return &result
	}
	return nil
}

func openMigrateRuntimeConnection(dbURL string) (*dbschema.DatabaseConnection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return dbschema.ConnectToDatabase(ctx, dbURL)
}

func openSQLiteRuntimeDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func sqliteTableNames(db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT name
FROM sqlite_schema
WHERE type = 'table'
  AND name NOT LIKE 'sqlite_%'
ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func sqliteRevisionFacts(db *sql.DB) ([]sqliteRevisionFact, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT version, description, applied, total, operator_version
FROM atlas_schema_revisions
ORDER BY CAST(version AS INTEGER)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var revisions []sqliteRevisionFact
	for rows.Next() {
		var fact sqliteRevisionFact
		if err := rows.Scan(&fact.Version, &fact.Description, &fact.Applied, &fact.Total, &fact.OperatorVersion); err != nil {
			return nil, err
		}
		revisions = append(revisions, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func postgresRelationNames(conn *dbschema.DatabaseConnection, schema string) ([]string, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT tablename
FROM pg_tables
WHERE schemaname = $1
UNION
SELECT relname
FROM pg_class
JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
WHERE pg_namespace.nspname = $1
  AND pg_class.relkind = 'S'
ORDER BY 1`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func postgresRevisionFacts(conn *dbschema.DatabaseConnection, schema string) ([]sqliteRevisionFact, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT version, description, applied, total, operator_version
FROM `+quotePostgresIdentifier(schema)+`.atlas_schema_revisions
ORDER BY CAST(version AS integer)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRevisionFacts(rows)
}

func postgresIndexNames(conn *dbschema.DatabaseConnection, schema, table string) ([]string, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT indexname
FROM pg_indexes
WHERE schemaname = $1
  AND tablename = $2
ORDER BY indexname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func mysqlTableNames(conn *dbschema.DatabaseConnection, schema string) ([]string, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT table_name
FROM information_schema.tables
WHERE table_schema = ?
ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func mysqlRevisionFacts(conn *dbschema.DatabaseConnection, schema string) ([]sqliteRevisionFact, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT version, description, applied, total, operator_version
FROM `+quoteMySQLIdentifier(schema)+`.atlas_schema_revisions
ORDER BY CAST(version AS SIGNED)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRevisionFacts(rows)
}

func scanRevisionFacts(rows *sql.Rows) ([]sqliteRevisionFact, error) {
	var revisions []sqliteRevisionFact
	for rows.Next() {
		var fact sqliteRevisionFact
		if err := rows.Scan(&fact.Version, &fact.Description, &fact.Applied, &fact.Total, &fact.OperatorVersion); err != nil {
			return nil, err
		}
		revisions = append(revisions, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func compareSQLiteTables(db *sql.DB, want []string) string {
	got, err := sqliteTableNames(db)
	if err != nil {
		return "inspect SQLite tables failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("SQLite tables = %v, want %v", got, want)
}

func compareSQLiteRevisions(db *sql.DB, want []sqliteRevisionFact) string {
	got, err := sqliteRevisionFacts(db)
	if err != nil {
		return "inspect Atlas revision rows failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("Atlas revision rows = %v, want %v", got, want)
}

func comparePostgresRelations(conn *dbschema.DatabaseConnection, schema string, want []string) string {
	got, err := postgresRelationNames(conn, schema)
	if err != nil {
		return "inspect PostgreSQL relations failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("PostgreSQL relations in schema %s = %v, want %v", schema, got, want)
}

func comparePostgresRevisions(conn *dbschema.DatabaseConnection, schema string, want []sqliteRevisionFact) string {
	got, err := postgresRevisionFacts(conn, schema)
	if err != nil {
		return "inspect PostgreSQL Atlas revision rows failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("PostgreSQL Atlas revision rows = %v, want %v", got, want)
}

func comparePostgresIndexes(conn *dbschema.DatabaseConnection, schema, table string, want []string) string {
	got, err := postgresIndexNames(conn, schema, table)
	if err != nil {
		return "inspect PostgreSQL indexes failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("PostgreSQL indexes on %s.%s = %v, want %v", schema, table, got, want)
}

func compareMySQLTables(conn *dbschema.DatabaseConnection, schema string, want []string) string {
	got, err := mysqlTableNames(conn, schema)
	if err != nil {
		return "inspect MySQL tables failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("MySQL tables in schema %s = %v, want %v", schema, got, want)
}

func compareMySQLRevisions(conn *dbschema.DatabaseConnection, schema string, want []sqliteRevisionFact) string {
	got, err := mysqlRevisionFacts(conn, schema)
	if err != nil {
		return "inspect MySQL Atlas revision rows failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("MySQL Atlas revision rows = %v, want %v", got, want)
}

func sqliteURL(path string) string {
	return "sqlite://" + filepath.ToSlash(path)
}

func migrateRuntimeIdentifier(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteMySQLIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func cleanupPostgresRuntimeSchema(dbURL, schema, fixture string) *Result {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quotePostgresIdentifier(schema)+" CASCADE")
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	return nil
}

func cleanupMySQLRuntimeSchema(dbURL, schema, fixture string) *Result {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quoteMySQLIdentifier(schema))
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	return nil
}

func migrateRuntimeFail(fixture, stage string, err error) Result {
	return Result{migrateRuntimeProbeName, fixture, stage, Fail, err.Error(), ""}
}

func migrateRuntimeGap(fixture, stage, detail string) Result {
	return Result{migrateRuntimeProbeName, fixture, stage, Gap, detail, "stokaro/ptah#648"}
}

func migrateRuntimeExit(fixture, stage, output string, err error) Result {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return migrateRuntimeGap(fixture, stage, oneLine(output))
	}
	return migrateRuntimeFail(fixture, stage, err)
}
