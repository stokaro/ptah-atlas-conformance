package probe

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/stokaro/ptah/dbschema"
)

const (
	compatInfoLog = "compat"
	nativeInfoLog = "native"
)

// These checks keep stokaro/ptah#937 covered at the external CLI and database
// boundary. They inspect SQLite, PostgreSQL, and MySQL state directly after
// each command so a successful exit or plausible report cannot hide mutation.

func sqliteMigrateApplyDryRunReadsStoredState(bin string) Result {
	const fixture = "sqlite/apply-dry-run-stored-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql":    "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_pets.sql":     "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
		"3_comments.sql": "CREATE TABLE comments (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "stored-state.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"2",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", output, err)
	}
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	beforePendingDryRun, err := sqliteRevisionStateSnapshot(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot", err)
	}

	stdout, stderr, err := commandStreams(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--dry-run",
		"--format", "{{ json . }}",
	}, "")
	if err != nil {
		return migrateRuntimeExit(fixture, "dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-stderr", detail)
	}
	var report atlasMigrateApplyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return migrateRuntimeGap(fixture, "report", "dry-run did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := compareMigrationVersions(report.Pending, []string{"3"}); detail != "" {
		return migrateRuntimeGap(fixture, "report", "pending "+detail)
	}
	if len(report.Applied) != 0 {
		return migrateRuntimeGap(fixture, "report", fmt.Sprintf("dry-run reported %d applied migration(s), want 0", len(report.Applied)))
	}

	if detail := compareSQLiteRevisionStateSnapshot(db, beforePendingDryRun); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-mutation", detail)
	}
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "pets", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	output, err = commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"1",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "complete", output, err)
	}
	beforeNoopDryRun, err := sqliteRevisionStateSnapshot(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "no-op-snapshot", err)
	}

	stdout, stderr, err = commandStreams(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--dry-run",
		"--format", "{{ json . }}",
	}, "")
	if err != nil {
		return migrateRuntimeExit(fixture, "no-op-dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "no-op-stderr", detail)
	}
	report = atlasMigrateApplyReport{}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return migrateRuntimeGap(fixture, "no-op-report", "fully applied dry-run did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := compareMigrationVersions(report.Pending, nil); detail != "" {
		return migrateRuntimeGap(fixture, "no-op-report", "pending "+detail)
	}
	if len(report.Applied) != 0 {
		return migrateRuntimeGap(fixture, "no-op-report", fmt.Sprintf("fully applied dry-run reported %d applied migration(s), want 0", len(report.Applied)))
	}
	if detail := compareSQLiteRevisionStateSnapshot(db, beforeNoopDryRun); detail != "" {
		return migrateRuntimeGap(fixture, "no-op-mutation", detail)
	}
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "comments", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "no-op-inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "pets", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "3", Description: "comments", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "no-op-revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply dry-run read stored revisions without mutation, planned only version 3 when pending, and planned nothing once fully applied", ""}
}

func sqliteNativeMigrateUpDryRunReadsStoredState(bin string) Result {
	const fixture = "sqlite/native-up-dry-run-stored-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_pets.sql":  "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()

	dbPath := filepath.Join(root, "native-stored-state.db")
	baseArgs := []string{
		"migrations", "up",
		"--db-url", sqliteURL(dbPath),
		"--migrations-dir", migrations,
		"--dir-format", "atlas",
		"--revision-format", "atlas",
	}
	stdout, stderr, err := commandStreams(bin, append(slices.Clone(baseArgs), "--limit", "1"), "")
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, nativeInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "seed-stderr", detail)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	before, err := sqliteRevisionStateSnapshot(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot", err)
	}
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "seed-schema", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "seed-revision", detail)
	}
	if detail := validateSQLiteAppliedRevisionMetadata(before.Revisions, "1", "users", 1); detail != "" {
		return migrateRuntimeGap(fixture, "seed-metadata", detail)
	}

	stdout, stderr, err = commandStreams(bin, append(slices.Clone(baseArgs), "--dry-run"), "")
	if err != nil {
		return migrateRuntimeExit(fixture, "dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, nativeInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-stderr", detail)
	}
	for _, expected := range []string{
		"Current version: 1",
		"Pending migrations: 1",
		"Would have applied 1 migrations",
	} {
		if !strings.Contains(stdout, expected) {
			return migrateRuntimeGap(fixture, "output", fmt.Sprintf("native dry-run output is missing %q: %s", expected, oneLine(stdout)))
		}
	}
	if detail := compareSQLiteRevisionStateSnapshot(db, before); detail != "" {
		return migrateRuntimeGap(fixture, "mutation", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"native ptah migrations up dry-run read version 1, planned only version 2, and preserved the complete SQLite schema and revision state", ""}
}

func sqliteMigrateApplyDryRunLeavesFreshTargetUninitialized(bin string) Result {
	const fixture = "sqlite/apply-dry-run-fresh-target"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "fresh-target.db")
	stdout, stderr, err := commandStreams(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--dry-run",
		"--format", "{{ json . }}",
	}, "")
	if err != nil {
		return migrateRuntimeExit(fixture, "dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-stderr", detail)
	}
	var report atlasMigrateApplyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return migrateRuntimeGap(fixture, "report", "dry-run did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := compareMigrationVersions(report.Pending, []string{"1"}); detail != "" {
		return migrateRuntimeGap(fixture, "report", "pending "+detail)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteTables(db, nil); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply dry-run planned version 1 on a fresh target without creating schema objects or revision metadata", ""}
}

func sqliteMigrateDownDryRunReadsStoredState(bin string) Result {
	const fixture = "sqlite/down-dry-run-stored-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql":      "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"1_users.down.sql": "DROP TABLE users;\n",
		"2_pets.sql":       "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
		"2_pets.down.sql":  "DROP TABLE pets;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "down-dry-run.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", output, err)
	}
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	before, err := sqliteRevisionStateSnapshot(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot", err)
	}

	stdout, stderr, err := commandStreams(bin, []string{
		"migrate", "down",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--dry-run",
		"--format", "{{ json . }}",
	}, "")
	if err != nil {
		return migrateRuntimeExit(fixture, "dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-stderr", detail)
	}
	var report atlasMigrateDownRevisionStateReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return migrateRuntimeGap(fixture, "report", "down dry-run did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := compareMigrationVersions(report.Planned, []string{"2", "1"}); detail != "" {
		return migrateRuntimeGap(fixture, "report", "planned "+detail)
	}
	if len(report.Reverted) != 0 {
		return migrateRuntimeGap(fixture, "report", fmt.Sprintf("dry-run reported %d reverted migration(s), want 0", len(report.Reverted)))
	}
	if report.Current != "2" {
		return migrateRuntimeGap(fixture, "report", fmt.Sprintf("current version = %q, want 2", report.Current))
	}

	if detail := compareSQLiteRevisionStateSnapshot(db, before); detail != "" {
		return migrateRuntimeGap(fixture, "formatted-mutation", detail)
	}

	stdout, stderr, err = commandStreams(bin, []string{
		"migrate", "down",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--dry-run",
		"--to-version", "1",
	}, "")
	if err != nil {
		return migrateRuntimeExit(fixture, "default-dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, nativeInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "default-dry-run-stderr", detail)
	}
	for _, expected := range []string{
		"Current version: 2",
		"Target version: 1",
		"Migrations to roll back: 1",
		"Would have rolled back these migrations: [2]",
	} {
		if !strings.Contains(stdout, expected) {
			return migrateRuntimeGap(fixture, "default-output", fmt.Sprintf("default-output down dry-run is missing %q: %s", expected, oneLine(stdout)))
		}
	}
	if detail := compareSQLiteRevisionStateSnapshot(db, before); detail != "" {
		return migrateRuntimeGap(fixture, "default-mutation", detail)
	}
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "pets", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"formatted and default-output down dry-runs read stored version 2, honored --to-version, and preserved the complete SQLite schema and revision state", ""}
}

func sqliteMigrateDownMissingBodyPreservesState(bin string) Result {
	const fixture = "sqlite/down-missing-body-atomicity"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql":     "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_pets.sql":      "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
		"2_pets.down.sql": "DROP TABLE pets;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "missing-down.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", output, err)
	}
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	before, err := sqliteRevisionStateSnapshot(db)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot", err)
	}

	for _, attempt := range []struct {
		stage string
		args  []string
	}{
		{
			stage: "dry-run",
			args:  []string{"migrate", "down", "--url", sqliteURL(dbPath), "--dir", fileURL(migrations), "--dry-run"},
		},
		{
			stage: "rollback",
			args:  []string{"migrate", "down", "--url", sqliteURL(dbPath), "--dir", fileURL(migrations), "--confirm"},
		},
	} {
		stdout, stderr, err := commandStreams(bin, attempt.args, "")
		if err == nil {
			return migrateRuntimeGap(fixture, attempt.stage, "rollback with a missing version-1 down body unexpectedly succeeded")
		}
		if detail := compareProcessExitCode(err, 1); detail != "" {
			return migrateRuntimeGap(fixture, attempt.stage+"-exit", detail)
		}
		if !strings.Contains(stderr, "migration 1 has no Atlas down migration") {
			return migrateRuntimeGap(fixture, attempt.stage+"-stderr", "expected rollback diagnostic on stderr: "+oneLine(stderr))
		}
		if strings.Contains(stdout, "migration 1 has no Atlas down migration") {
			return migrateRuntimeGap(fixture, attempt.stage+"-stdout", "rollback diagnostic leaked to stdout: "+oneLine(stdout))
		}
		if detail := compareSQLiteRevisionStateSnapshot(db, before); detail != "" {
			return migrateRuntimeGap(fixture, attempt.stage+"-mutation", detail)
		}
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"dry-run and real down exited 1 with the diagnostic on stderr and rejected the incomplete rollback set before any SQLite schema or revision mutation", ""}
}

func sqliteMigrateTxModeAllDiagnosticUsesAvailableFlags(bin string) Result {
	const fixture = "sqlite/tx-mode-all-diagnostic"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql": "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_checked.sql": "-- +ptah check name=\"users_empty\" assert=\"SELECT count(*) = 0 FROM users\" on_fail=abort\n" +
			"DROP TABLE users;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "tx-mode-all.db")
	stdout, stderr, err := commandStreams(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--dry-run",
		"--tx-mode", "all",
	}, "")
	if err == nil {
		return migrateRuntimeGap(fixture, "diagnostic", "tx-mode all unexpectedly accepted a migration with a pre-migration check")
	}
	if detail := compareProcessExitCode(err, 1); detail != "" {
		return migrateRuntimeGap(fixture, "exit", detail)
	}
	if strings.Contains(stderr, "--skip-checks") {
		return migrateRuntimeGap(fixture, "diagnostic", "tx-mode all diagnostic suggests unavailable compat flag --skip-checks: "+oneLine(stderr))
	}
	const expected = "migration 2 declares pre-migration checks, which cannot run with tx-mode all; use the default per-file transaction mode"
	if !strings.Contains(stderr, expected) {
		return migrateRuntimeGap(fixture, "stderr", "expected check-specific tx-mode diagnostic on stderr: "+oneLine(stderr))
	}
	if strings.Contains(stdout, expected) {
		return migrateRuntimeGap(fixture, "stdout", "tx-mode diagnostic leaked to stdout: "+oneLine(stdout))
	}
	return Result{migrateRuntimeProbeName, fixture, "diagnostic", OK,
		"tx-mode all rejected a pre-migration check with exit 1 and the diagnostic on stderr without suggesting unavailable compat flag --skip-checks", ""}
}

func postgresMigrateApplyDryRunReadsStoredState(bin, dbURL string) Result {
	const fixture = "postgres/apply-dry-run-stored-state"
	schema := migrateRuntimeIdentifier("ptah_rt_pg_dry_run")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql": "CREATE TABLE " + quotePostgresIdentifier(schema) + ".users (id integer PRIMARY KEY);\n",
		"2_pets.sql":  "CREATE TABLE " + quotePostgresIdentifier(schema) + ".pets (id integer PRIMARY KEY);\n",
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

	baseArgs := []string{
		"migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	}
	stdout, stderr, err := commandStreams(bin, append(slices.Clone(baseArgs), "1"), "")
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "seed-stderr", detail)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := comparePostgresRelations(conn, schema, []string{"atlas_schema_revisions", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "seed-relations", detail)
	}
	if detail := comparePostgresRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "seed-revisions", detail)
	}
	beforeRelations, err := postgresRelationNames(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-relations", err)
	}
	beforeRevisions, err := postgresRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-revisions", err)
	}
	beforeSchema, err := typedDatabaseSchemaSnapshot(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-schema", err)
	}
	beforeFullRevisions, err := postgresCompleteRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-complete-revisions", err)
	}
	if detail := validateCompleteAppliedRevisionFacts(beforeFullRevisions, "1", "users"); detail != "" {
		return migrateRuntimeGap(fixture, "seed-complete-revisions", detail)
	}

	stdout, stderr, err = commandStreams(bin, append(slices.Clone(baseArgs), "--dry-run", "--format", "{{ json . }}"), "")
	if err != nil {
		return migrateRuntimeExit(fixture, "dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-stderr", detail)
	}
	if detail := inspectStoredStateApplyReport(stdout, []string{"2"}); detail != "" {
		return migrateRuntimeGap(fixture, "report", detail)
	}
	afterRelations, err := postgresRelationNames(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-relations", err)
	}
	afterRevisions, err := postgresRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-revisions", err)
	}
	afterSchema, err := typedDatabaseSchemaSnapshot(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-schema", err)
	}
	afterFullRevisions, err := postgresCompleteRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-complete-revisions", err)
	}
	if !slices.Equal(afterRelations, beforeRelations) ||
		!slices.Equal(afterRevisions, beforeRevisions) ||
		afterSchema != beforeSchema ||
		!slices.Equal(afterFullRevisions, beforeFullRevisions) {
		return migrateRuntimeGap(fixture, "mutation", fmt.Sprintf(
			"PostgreSQL state changed during dry-run: relations %v -> %v, revisions %v -> %v, typed schema equal=%t, complete revisions %v -> %v",
			beforeRelations, afterRelations, beforeRevisions, afterRevisions, beforeSchema == afterSchema,
			beforeFullRevisions, afterFullRevisions,
		))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"PostgreSQL apply dry-run read the custom-schema stored revision, planned only version 2, and left relations and revision identity state unchanged", ""}
}

func mysqlMigrateApplyDryRunReadsStoredState(bin, dbURL string) Result {
	const fixture = "mysql/apply-dry-run-stored-state"
	schema := migrateRuntimeIdentifier("ptah_rt_mysql_dry_run")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_users.sql": "CREATE TABLE " + quoteMySQLIdentifier(schema) + "." + quoteMySQLIdentifier("users") + " (id integer PRIMARY KEY);\n",
		"2_pets.sql":  "CREATE TABLE " + quoteMySQLIdentifier(schema) + "." + quoteMySQLIdentifier("pets") + " (id integer PRIMARY KEY);\n",
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

	baseArgs := []string{
		"migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	}
	stdout, stderr, err := commandStreams(bin, append(slices.Clone(baseArgs), "1"), "")
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "seed-stderr", detail)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := compareMySQLTables(conn, schema, []string{"atlas_schema_revisions", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "seed-tables", detail)
	}
	if detail := compareMySQLRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "seed-revisions", detail)
	}
	beforeTables, err := mysqlTableNames(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-tables", err)
	}
	beforeRevisions, err := mysqlRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-revisions", err)
	}
	beforeSchema, err := typedDatabaseSchemaSnapshot(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-schema", err)
	}
	beforeFullRevisions, err := mysqlCompleteRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "snapshot-complete-revisions", err)
	}
	if detail := validateCompleteAppliedRevisionFacts(beforeFullRevisions, "1", "users"); detail != "" {
		return migrateRuntimeGap(fixture, "seed-complete-revisions", detail)
	}

	stdout, stderr, err = commandStreams(bin, append(slices.Clone(baseArgs), "--dry-run", "--format", "{{ json . }}"), "")
	if err != nil {
		return migrateRuntimeExit(fixture, "dry-run", stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog); detail != "" {
		return migrateRuntimeGap(fixture, "dry-run-stderr", detail)
	}
	if detail := inspectStoredStateApplyReport(stdout, []string{"2"}); detail != "" {
		return migrateRuntimeGap(fixture, "report", detail)
	}
	afterTables, err := mysqlTableNames(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-tables", err)
	}
	afterRevisions, err := mysqlRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-revisions", err)
	}
	afterSchema, err := typedDatabaseSchemaSnapshot(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-schema", err)
	}
	afterFullRevisions, err := mysqlCompleteRevisionFacts(conn, schema)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect-complete-revisions", err)
	}
	if !slices.Equal(afterTables, beforeTables) ||
		!slices.Equal(afterRevisions, beforeRevisions) ||
		afterSchema != beforeSchema ||
		!slices.Equal(afterFullRevisions, beforeFullRevisions) {
		return migrateRuntimeGap(fixture, "mutation", fmt.Sprintf(
			"MySQL state changed during dry-run: tables %v -> %v, revisions %v -> %v, typed schema equal=%t, complete revisions %v -> %v",
			beforeTables, afterTables, beforeRevisions, afterRevisions, beforeSchema == afterSchema,
			beforeFullRevisions, afterFullRevisions,
		))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"MySQL apply dry-run read the custom-schema stored revision, planned only version 2, and left tables and revision identity state unchanged", ""}
}

type atlasMigrateDownRevisionStateReport struct {
	Planned  []atlasMigrationFileReport
	Reverted []atlasMigrationFileReport
	Current  string
}

func compareMigrationVersions(files []atlasMigrationFileReport, want []string) string {
	got := make([]string, len(files))
	for i, file := range files {
		got[i] = file.Version
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("versions = %v, want %v", got, want)
}

func inspectStoredStateApplyReport(stdout string, wantPending []string) string {
	var report atlasMigrateApplyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return "dry-run did not emit valid JSON: " + oneLine(err.Error())
	}
	if detail := compareMigrationVersions(report.Pending, wantPending); detail != "" {
		return "pending " + detail
	}
	if len(report.Applied) != 0 {
		return fmt.Sprintf("dry-run reported %d applied migration(s), want 0", len(report.Applied))
	}
	return ""
}

type completeRevisionFact struct {
	Version         string
	Description     string
	Type            string
	Applied         string
	Total           string
	ExecutedAt      string
	ExecutionTime   string
	ErrorIsNull     bool
	Error           string
	ErrorStmtIsNull bool
	ErrorStmt       string
	Hash            string
	PartialIsNull   bool
	PartialHashes   string
	OperatorVersion string
}

// typedDatabaseSchemaSnapshot renders the introspected schema as canonical
// JSON so two reads of an unchanged database compare equal.
//
// Introspection does not guarantee a stable order: reading the same untouched
// MySQL schema repeatedly yields byte-identical LENGTH but differently ordered
// collections. Comparing raw json.Marshal output therefore reported "state
// changed during dry-run" at random, an intermittent false positive that could
// fail the migrate-runtime gate on a database nothing had touched. Tracked as
// stokaro/ptah-atlas-conformance#247.
//
// Order-insensitivity is the correct semantics here: the probe asks whether
// the logical schema changed, and introspection row order is not part of that.
// Element CONTENT is still compared exactly, so a real change still shows up.
func typedDatabaseSchemaSnapshot(conn *dbschema.DatabaseConnection, schema string) (string, error) {
	state, err := dbschema.ReadSchemaWithSchemas(conn, []string{schema})
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(canonicalizeJSONOrder(generic))
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// canonicalizeJSONOrder recursively sorts every array by its elements'
// marshaled form. Object keys already marshal in sorted order via encoding/json,
// so only arrays need normalizing.
func canonicalizeJSONOrder(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, element := range typed {
			typed[key] = canonicalizeJSONOrder(element)
		}
		return typed
	case []any:
		for i, element := range typed {
			typed[i] = canonicalizeJSONOrder(element)
		}
		sort.SliceStable(typed, func(i, j int) bool {
			left, errLeft := json.Marshal(typed[i])
			right, errRight := json.Marshal(typed[j])
			if errLeft != nil || errRight != nil {
				// Unmarshaled generic JSON always re-marshals; on the
				// impossible error path keep the existing order.
				return false
			}
			return string(left) < string(right)
		})
		return typed
	default:
		return value
	}
}

func postgresCompleteRevisionFacts(conn *dbschema.DatabaseConnection, schema string) ([]completeRevisionFact, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT
    CAST(version AS text), description, CAST(type AS text), CAST(applied AS text), CAST(total AS text),
    CAST(executed_at AS text), CAST(execution_time AS text),
    error IS NULL, COALESCE(error, ''), error_stmt IS NULL, COALESCE(error_stmt, ''),
    hash, partial_hashes IS NULL, COALESCE(CAST(partial_hashes AS text), ''), operator_version
FROM `+quotePostgresIdentifier(schema)+`.atlas_schema_revisions
ORDER BY CAST(version AS integer)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCompleteRevisionFacts(rows)
}

func mysqlCompleteRevisionFacts(conn *dbschema.DatabaseConnection, schema string) ([]completeRevisionFact, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT
    CAST(version AS CHAR), description, CAST(type AS CHAR), CAST(applied AS CHAR), CAST(total AS CHAR),
    CAST(executed_at AS CHAR), CAST(execution_time AS CHAR),
    error IS NULL, COALESCE(error, ''), error_stmt IS NULL, COALESCE(error_stmt, ''),
    hash, partial_hashes IS NULL, COALESCE(CAST(partial_hashes AS CHAR), ''), operator_version
FROM `+quoteMySQLIdentifier(schema)+`.atlas_schema_revisions
ORDER BY CAST(version AS SIGNED)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCompleteRevisionFacts(rows)
}

func scanCompleteRevisionFacts(rows *sql.Rows) ([]completeRevisionFact, error) {
	var facts []completeRevisionFact
	for rows.Next() {
		var fact completeRevisionFact
		if err := rows.Scan(
			&fact.Version,
			&fact.Description,
			&fact.Type,
			&fact.Applied,
			&fact.Total,
			&fact.ExecutedAt,
			&fact.ExecutionTime,
			&fact.ErrorIsNull,
			&fact.Error,
			&fact.ErrorStmtIsNull,
			&fact.ErrorStmt,
			&fact.Hash,
			&fact.PartialIsNull,
			&fact.PartialHashes,
			&fact.OperatorVersion,
		); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func validateCompleteAppliedRevisionFacts(facts []completeRevisionFact, version, description string) string {
	if len(facts) != 1 {
		return fmt.Sprintf("complete revision row count = %d, want 1", len(facts))
	}
	fact := facts[0]
	switch {
	case fact.Version != version:
		return fmt.Sprintf("complete revision version = %q, want %q", fact.Version, version)
	case fact.Description != description:
		return fmt.Sprintf("complete revision description = %q, want %q", fact.Description, description)
	case fact.Type != "2" || fact.Applied != "1" || fact.Total != "1":
		return fmt.Sprintf("complete revision type/applied/total = %s/%s/%s, want 2/1/1", fact.Type, fact.Applied, fact.Total)
	case fact.ExecutedAt == "" || fact.ExecutionTime == "":
		return fmt.Sprintf("complete revision timing fields are empty: executed_at=%q execution_time=%q", fact.ExecutedAt, fact.ExecutionTime)
	case fact.Error != "" || fact.ErrorStmt != "":
		return fmt.Sprintf("complete revision error fields = %q/%q, want empty", fact.Error, fact.ErrorStmt)
	case fact.ErrorIsNull || fact.ErrorStmtIsNull:
		return fmt.Sprintf("complete revision error NULL state = %t/%t, want false/false", fact.ErrorIsNull, fact.ErrorStmtIsNull)
	case fact.Hash == "":
		return "complete revision hash is empty"
	case fact.PartialIsNull || fact.PartialHashes != "null":
		return fmt.Sprintf("complete revision partial_hashes NULL/value = %t/%q, want false/null", fact.PartialIsNull, fact.PartialHashes)
	case fact.OperatorVersion != "Ptah":
		return fmt.Sprintf("complete revision operator_version = %q, want Ptah", fact.OperatorVersion)
	}
	return ""
}

type sqliteSchemaStateFact struct {
	Type      string
	Name      string
	Table     string
	RootPage  int64
	SQLIsNull bool
	SQL       string
}

type sqliteRevisionState struct {
	Schema    []sqliteSchemaStateFact
	Revisions []projectConfigRevisionMetadata
}

func sqliteRevisionStateSnapshot(db *sql.DB) (sqliteRevisionState, error) {
	schema, err := sqliteSchemaStateFacts(db)
	if err != nil {
		return sqliteRevisionState{}, fmt.Errorf("inspect complete SQLite schema: %w", err)
	}
	revisions, err := projectConfigRevisionMetadataFacts(db)
	if err != nil {
		return sqliteRevisionState{}, fmt.Errorf("inspect complete Atlas revision rows: %w", err)
	}
	return sqliteRevisionState{Schema: schema, Revisions: revisions}, nil
}

func sqliteSchemaStateFacts(db *sql.DB) ([]sqliteSchemaStateFact, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT type, name, tbl_name, rootpage, sql IS NULL, COALESCE(sql, '')
FROM sqlite_schema
ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var facts []sqliteSchemaStateFact
	for rows.Next() {
		var fact sqliteSchemaStateFact
		if err := rows.Scan(&fact.Type, &fact.Name, &fact.Table, &fact.RootPage, &fact.SQLIsNull, &fact.SQL); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func compareSQLiteRevisionStateSnapshot(db *sql.DB, before sqliteRevisionState) string {
	after, err := sqliteRevisionStateSnapshot(db)
	if err != nil {
		return "inspect complete SQLite revision state failed: " + oneLine(err.Error())
	}
	if slices.Equal(after.Schema, before.Schema) && slices.Equal(after.Revisions, before.Revisions) {
		return ""
	}
	return fmt.Sprintf(
		"SQLite state changed: schema %v -> %v, revisions %v -> %v",
		before.Schema, after.Schema, before.Revisions, after.Revisions,
	)
}

func validateSQLiteAppliedRevisionMetadata(
	revisions []projectConfigRevisionMetadata,
	version, description string,
	statements int64,
) string {
	if len(revisions) != 1 {
		return fmt.Sprintf("complete revision row count = %d, want 1", len(revisions))
	}
	revision := revisions[0]
	switch {
	case revision.Version != version:
		return fmt.Sprintf("complete revision version = %q, want %q", revision.Version, version)
	case revision.Description != description:
		return fmt.Sprintf("complete revision description = %q, want %q", revision.Description, description)
	case revision.Type != 2:
		return fmt.Sprintf("complete revision type = %d, want 2", revision.Type)
	case revision.Applied != statements || revision.Total != statements:
		return fmt.Sprintf("complete revision applied/total = %d/%d, want %d/%d", revision.Applied, revision.Total, statements, statements)
	case revision.ExecutedAt == "":
		return "complete revision executed_at is empty"
	case revision.ExecutionTime < 0:
		return fmt.Sprintf("complete revision execution_time = %d, want non-negative", revision.ExecutionTime)
	case revision.Error != "" || revision.ErrorStatement != "":
		return fmt.Sprintf("complete revision error fields = %q/%q, want empty", revision.Error, revision.ErrorStatement)
	case revision.ErrorIsNull || revision.ErrorStatementIsNull:
		return fmt.Sprintf("complete revision error NULL state = %t/%t, want false/false", revision.ErrorIsNull, revision.ErrorStatementIsNull)
	case revision.Hash == "":
		return "complete revision hash is empty"
	case revision.PartialHashesIsNull || revision.PartialHashes != "null":
		return fmt.Sprintf("complete revision partial_hashes NULL/value = %t/%q, want false/null", revision.PartialHashesIsNull, revision.PartialHashes)
	case revision.OperatorVersion != "Ptah":
		return fmt.Sprintf("complete revision operator_version = %q, want Ptah", revision.OperatorVersion)
	}
	return ""
}

func compareProcessExitCode(err error, want int) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Sprintf("process error type = %T, want *exec.ExitError", err)
	}
	if got := exitErr.ExitCode(); got != want {
		return fmt.Sprintf("process exit code = %d, want %d", got, want)
	}
	return ""
}

func inspectInfoOnlyStderr(stderr, style string, forbidden ...string) string {
	for line := range strings.SplitSeq(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !isInfoLogLine(line, style) {
			return "successful command wrote a non-INFO stderr line: " + oneLine(line)
		}
		for _, value := range forbidden {
			if value != "" && strings.Contains(line, value) {
				return "successful command leaked a forbidden value to stderr: " + oneLine(value)
			}
		}
	}
	return ""
}

func isInfoLogLine(line, style string) bool {
	switch style {
	case compatInfoLog:
		if len(line) < len("2006/01/02 15:04:05 INFO ") {
			return false
		}
		if _, err := time.Parse("2006/01/02 15:04:05", line[:19]); err != nil {
			return false
		}
		return strings.HasPrefix(line[19:], " INFO ")
	case nativeInfoLog:
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "level=INFO" {
			return false
		}
		timestamp, ok := strings.CutPrefix(fields[0], "time=")
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, timestamp)
		return err == nil
	default:
		return false
	}
}
