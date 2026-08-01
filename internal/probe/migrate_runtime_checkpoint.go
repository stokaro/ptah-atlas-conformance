package probe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Checkpoint and apply-time integrity checks measured against Atlas trial
// v1.2.4 (stokaro/ptah#954, stokaro/ptah#955): a migration whose first line is
// the `-- atlas:checkpoint` directive bootstraps a fresh database on its own
// (single type=2 revision row, pre-checkpoint files never replayed) and is
// silently skipped on a database that already applied pre-checkpoint history;
// `migrate apply` on a hashed directory whose files were edited after hashing
// refuses with a checksum mismatch before executing anything.

const (
	checkpointRuntimeVersion   = "20260801100335"
	checkpointRuntimePreOne    = "20250801000001"
	checkpointRuntimePreTwo    = "20250801000002"
	atlasRevisionTypeExecuted  = 2
	checksumMismatchNeedle     = "checksum mismatch"
	checkpointRuntimeTamperSQL = "\n-- tampered comment, sum not rehashed\n"
)

func checkpointRuntimePreFiles() map[string]string {
	return map[string]string{
		checkpointRuntimePreOne + "_create_users.sql": "-- create \"users\" table\nCREATE TABLE `users` (\n  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,\n  `name` text NOT NULL\n);\n",
		checkpointRuntimePreTwo + "_add_email.sql":    "-- add \"email\" column to \"users\"\nALTER TABLE `users` ADD COLUMN `email` text NULL;\n",
	}
}

func checkpointRuntimeFiles() map[string]string {
	files := checkpointRuntimePreFiles()
	files[checkpointRuntimeVersion+"_checkpoint.sql"] = "-- atlas:checkpoint\n\n-- Create \"users\" table\nCREATE TABLE `users` (\n  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,\n  `name` text NOT NULL,\n  `email` text NULL\n);\n"
	return files
}

// sqliteMigrateApplyCheckpointFreshBootstrap verifies that applying a
// checkpointed Atlas directory onto a fresh SQLite database executes only the
// checkpoint: measured Atlas leaves exactly one revision row
// (`20260801100335|checkpoint|2|1|1`) and never replays the squashed
// pre-checkpoint files.
func sqliteMigrateApplyCheckpointFreshBootstrap(bin string) Result {
	const fixture = "sqlite/checkpoint-fresh-bootstrap"
	root, migrations, cleanup, err := migrateRuntimeDir(checkpointRuntimeFiles())
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "fresh.db")
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
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: checkpointRuntimeVersion, Description: "checkpoint", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	if detail := compareSQLiteRevisionType(db, checkpointRuntimeVersion, atlasRevisionTypeExecuted); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	status, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "status", status, err)
	}
	if !strings.Contains(status, "Pending Migrations: 0") {
		return migrateRuntimeGap(fixture, "status", "status did not report 0 pending migrations after checkpoint bootstrap: "+oneLine(status))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"fresh-database apply executed only the checkpoint, wrote the single type=2 Atlas revision row, and reported a clean status", ""}
}

// sqliteMigrateApplyCheckpointPreExistingSkips verifies that a database that
// already applied the pre-checkpoint history skips the checkpoint silently:
// measured Atlas prints "No migration files to execute", writes no revision
// row, and keeps status OK.
func sqliteMigrateApplyCheckpointPreExistingSkips(bin string) Result {
	const fixture = "sqlite/checkpoint-pre-existing-skip"
	preRoot, preMigrations, preCleanup, err := migrateRuntimeDir(checkpointRuntimePreFiles())
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer preCleanup()
	if result := migrateRuntimeHash(bin, preMigrations, fixture); result != nil {
		return *result
	}
	_, fullMigrations, fullCleanup, err := migrateRuntimeDir(checkpointRuntimeFiles())
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer fullCleanup()
	if result := migrateRuntimeHash(bin, fullMigrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(preRoot, "pre.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(preMigrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "seed", output, err)
	}

	output, err = commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(fullMigrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}
	if !strings.Contains(output, "No migration files to execute") {
		return migrateRuntimeGap(fixture, "apply", "apply on a pre-checkpoint database did not skip the checkpoint: "+oneLine(output))
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: checkpointRuntimePreOne, Description: "create_users", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: checkpointRuntimePreTwo, Description: "add_email", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	status, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(fullMigrations),
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "status", status, err)
	}
	if !strings.Contains(status, "Pending Migrations: 0") {
		return migrateRuntimeGap(fixture, "status", "status still reports the skipped checkpoint as pending: "+oneLine(status))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"pre-checkpoint database skipped the checkpoint silently with no new revision row and a clean status", ""}
}

// sqliteMigrateApplyTamperedSumRefuses verifies apply-time integrity
// enforcement: a hashed directory edited after hashing must refuse with a
// checksum mismatch before executing anything, exactly like official Atlas.
// The SQLite database file must not even be created.
func sqliteMigrateApplyTamperedSumRefuses(bin string) Result {
	const fixture = "sqlite/tampered-sum-apply-refusal"
	root, migrations, cleanup, err := migrateRuntimeDir(checkpointRuntimeFiles())
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	tamperTarget := filepath.Join(migrations, checkpointRuntimeVersion+"_checkpoint.sql")
	tampered, err := os.ReadFile(tamperTarget)
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	if err := os.WriteFile(tamperTarget, append(tampered, []byte(checkpointRuntimeTamperSQL)...), 0o600); err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}

	dbPath := filepath.Join(root, "tamper.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err == nil {
		return migrateRuntimeGap(fixture, "apply", "apply on a tampered hashed directory succeeded without checksum verification: "+oneLine(output))
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return migrateRuntimeFail(fixture, "apply", err)
	}
	if !strings.Contains(output, checksumMismatchNeedle) || !strings.Contains(output, "was edited") {
		return migrateRuntimeGap(fixture, "apply", "tampered-directory refusal did not use the Atlas checksum-mismatch shape: "+oneLine(output))
	}
	if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
		return migrateRuntimeGap(fixture, "inspect", "apply touched the target database before refusing the tampered directory")
	}
	return Result{migrateRuntimeProbeName, fixture, "apply", OK,
		"apply refused the tampered hashed directory with the Atlas checksum-mismatch shape before creating or touching the target database", ""}
}

// compareSQLiteRevisionType checks the Atlas revision `type` bit flag of one
// revision row. Measured Atlas records a checkpoint bootstrap with type=2, the
// same executed flag as an ordinary applied migration.
func compareSQLiteRevisionType(db *sql.DB, version string, want int) string {
	var got int
	err := db.QueryRowContext(context.Background(),
		"SELECT type FROM atlas_schema_revisions WHERE version = ?", version).Scan(&got)
	if err != nil {
		return "inspect Atlas revision type failed: " + oneLine(err.Error())
	}
	if got != want {
		return fmt.Sprintf("Atlas revision type for version %s = %d, want %d", version, got, want)
	}
	return ""
}
