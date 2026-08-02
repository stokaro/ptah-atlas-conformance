package probe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Checkpoint and apply-time integrity checks measured against Atlas
// (stokaro/ptah#954, stokaro/ptah#955): a migration whose first line is
// the `-- atlas:checkpoint` directive bootstraps a fresh database on its own
// (single type=2 revision row, pre-checkpoint files never replayed) and is
// silently skipped on a database that already applied pre-checkpoint history;
// `migrate apply` on a hashed directory whose files were edited after hashing
// refuses with a checksum mismatch before executing anything.
//
// Apply-time integrity has two branches and Ptah now implements both.
// stokaro/ptah#955 scoped its fix to *hashed* directories, so the post-hash
// edit branch was enforced while the missing-atlas.sum branch was not;
// sqliteMigrateApplyUnhashedDirRefuses pinned that divergence as a red row
// until stokaro/ptah#972 closed it. Both branches are now green.

const (
	checkpointRuntimeVersion   = "20260801100335"
	checkpointRuntimePreOne    = "20250801000001"
	checkpointRuntimePreTwo    = "20250801000002"
	atlasRevisionTypeExecuted  = 2
	checksumMismatchNeedle     = "checksum mismatch"
	checksumNotFoundNeedle     = "checksum file not found"
	checkpointRuntimeTamperSQL = "\n-- tampered comment, sum not rehashed\n"

	// upstreamPartialCheckpointDir is Atlas's own multi-checkpoint fixture,
	// vendored verbatim under third_party/. Using it for execution semantics —
	// not just parsing — is what makes this family pin Atlas rather than pin
	// Ptah: the files, their versions, and atlas.sum are all upstream-authored.
	upstreamPartialCheckpointDir = "third_party/atlas/upstream/sql/migrate/testdata/partial-checkpoint"
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
	if !migrateStatusReportsNoPending(status) {
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
	if !migrateStatusReportsNoPending(status) {
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
	if detail := compareProcessExitCode(err, 1); detail != "" {
		return migrateRuntimeGap(fixture, "apply", "tampered-directory refusal: "+detail)
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

// sqliteMigrateApplyUnhashedDirRefuses measures the second branch of Atlas's
// apply-time integrity contract: a directory with no atlas.sum at all.
//
// Measured on pinned Atlas CE v1.2.0, apply refuses an unhashed directory with
// exit 1 and "Error: checksum file not found", and never creates the target
// database.
//
// This check landed RED: ptah-compat applied such a directory and exited 0,
// because stokaro/ptah#955 scoped its fix to hashed directories and gated only
// the mismatch branch. It was carried as a migrate-runtime budget of 1 rather
// than a waiver, so the divergence stayed visible as a red row. stokaro/ptah#972
// closed it, and stdout, stderr, exit code and the absence of the target
// database are now byte-identical to CE.
//
// It cites its issue directly rather than going through migrateRuntimeGap,
// whose hardcoded umbrella issue would misattribute a future regression.
func sqliteMigrateApplyUnhashedDirRefuses(bin string) Result {
	const fixture = "sqlite/unhashed-dir-apply-refusal"
	gap := func(stage, detail string) Result {
		// Points at the fix rather than the original issue: this row is green
		// now, so any future red is a regression of stokaro/ptah#972.
		return Result{migrateRuntimeProbeName, fixture, stage, Gap, detail, "stokaro/ptah#972"}
	}

	// Deliberately NOT hashed: no migrateRuntimeHash call, so the directory has
	// no atlas.sum. That absence is the whole fixture.
	root, migrations, cleanup, err := migrateRuntimeDir(checkpointRuntimeFiles())
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if _, statErr := os.Stat(filepath.Join(migrations, "atlas.sum")); !errors.Is(statErr, os.ErrNotExist) {
		return migrateRuntimeFail(fixture, "setup", errors.New("fixture directory unexpectedly contains atlas.sum"))
	}

	dbPath := filepath.Join(root, "unhashed.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
	})
	if err == nil {
		return gap("apply", "apply on an unhashed directory succeeded (exit 0) instead of refusing with the Atlas checksum-file-not-found shape: "+oneLine(output))
	}
	if detail := compareProcessExitCode(err, 1); detail != "" {
		return gap("apply", "unhashed-directory refusal: "+detail)
	}
	if !strings.Contains(output, checksumNotFoundNeedle) {
		return gap("apply", "unhashed-directory refusal did not use the Atlas checksum-file-not-found shape: "+oneLine(output))
	}
	if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
		return gap("inspect", "apply created the target database before refusing the unhashed directory")
	}
	return Result{migrateRuntimeProbeName, fixture, "apply", OK,
		"apply refused the unhashed directory with the Atlas checksum-file-not-found shape before creating or touching the target database", ""}
}

// sqliteMigrateApplyUpstreamPartialCheckpoint runs Atlas's own vendored
// multi-checkpoint fixture, pre-hashed upstream, through a real apply.
//
// It covers what the hand-written fixtures cannot: two checkpoints in one
// directory plus a post-checkpoint migration. Measured Atlas CE v1.2.0 applies
// only the LATEST checkpoint (version 5, three statements) and the migration
// after it (version 6), leaving tbl_1..tbl_4 and exactly two revision rows.
// Ptah matches that end state, so this pins Atlas-authored semantics rather
// than a first-party restatement of them.
func sqliteMigrateApplyUpstreamPartialCheckpoint(bin string) Result {
	const fixture = "sqlite/upstream-partial-checkpoint"
	files, err := upstreamPartialCheckpointFiles()
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	// Copied into a temp directory, atlas.sum included and never re-hashed, so
	// the upstream checksums are what gate the apply.
	root, migrations, cleanup, err := migrateRuntimeDir(files)
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()

	dbPath := filepath.Join(root, "upstream.db")
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
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "tbl_1", "tbl_2", "tbl_3", "tbl_4"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	// Only the latest checkpoint and what follows it: versions 1-4 are squashed
	// by version 5 and must never appear as revision rows.
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "5", Description: "checkpoint", Applied: 3, Total: 3, OperatorVersion: "Ptah"},
		{Version: "6", Description: "sixth", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	if detail := compareSQLiteRevisionType(db, "5", atlasRevisionTypeExecuted); detail != "" {
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
	if !migrateStatusReportsNoPending(status) {
		return migrateRuntimeGap(fixture, "status", "status did not report 0 pending migrations after the upstream partial-checkpoint apply: "+oneLine(status))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"Atlas's own partial-checkpoint fixture applied only the latest checkpoint plus the migration after it, matching the measured Atlas CE end schema and revision rows", ""}
}

// upstreamPartialCheckpointFiles reads Atlas's vendored partial-checkpoint
// fixture, atlas.sum included, so the copy stays byte-identical to upstream.
func upstreamPartialCheckpointFiles() (map[string]string, error) {
	entries, err := os.ReadDir(upstreamPartialCheckpointDir)
	if err != nil {
		return nil, fmt.Errorf("reading vendored Atlas partial-checkpoint fixture: %w", err)
	}
	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(upstreamPartialCheckpointDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		files[entry.Name()] = string(content)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("vendored Atlas partial-checkpoint fixture %s is empty", upstreamPartialCheckpointDir)
	}
	return files, nil
}

// atlasPendingFilesZero matches Atlas CE's status wording for "nothing pending"
// ("-- Pending Files:   0"), which the repo also models in txtar_script.go.
var atlasPendingFilesZero = regexp.MustCompile(`Pending Files:\s+0\b`)

// migrateStatusReportsNoPending reports whether `migrate status` says nothing
// is pending, accepting either tool's spelling.
//
// Ptah currently prints "Pending Migrations: 0"; Atlas CE v1.2.0 prints
// "-- Pending Files:   0". Accepting both keeps these fixtures from turning red
// for a *parity improvement* — if Ptah ever aligns its status wording with
// Atlas, the checkpoint family must not be what blocks it. The revision-row
// assertions beside this one carry the fixtures' real weight.
func migrateStatusReportsNoPending(status string) bool {
	return strings.Contains(status, "Pending Migrations: 0") || atlasPendingFilesZero.MatchString(status)
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
