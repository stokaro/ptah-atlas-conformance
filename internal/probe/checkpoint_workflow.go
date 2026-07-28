package probe

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	checkpointWorkflowSentinel = "_capability/checkpoint-workflow/SENTINEL"
	checkpointWorkflowIssue    = "stokaro/ptah#660"

	// checkpointUpFile and checkpointDownFile are deterministic: the fixture's
	// newest migration is version 3, `ptah migrations checkpoint` defaults the
	// checkpoint version to one above the newest migration, and the probe passes
	// `--description squash`.
	checkpointUpFile   = "0000000004_squash.checkpoint.up.sql"
	checkpointDownFile = "0000000004_squash.checkpoint.down.sql"
)

// CheckpointWorkflowProbe exercises Ptah's migration checkpoint capability
// (`ptah migrations checkpoint` plus checkpoint-aware `up`/`status`/`down`)
// end to end through the real CLI. Atlas keeps `migrate checkpoint` in its
// proprietary Pro build, so this is a first-party capability sentinel measured
// against the built Ptah binary rather than an Atlas-corpus round-trip fixture.
//
// The probe proves the round-trip ptah#660 asks for on ephemeral SQLite
// databases: a committed three-migration history is applied in full to one
// database; `ptah migrations checkpoint` squashes that history into a
// deterministic cumulative-schema checkpoint pair via a shadow-database replay
// and rewrites `ptah.sum`; a fresh database bootstraps from the checkpoint
// alone and converges to a schema structurally identical to the full replay;
// the already-migrated database ignores the checkpoint entirely; a tampered
// checkpoint fails `ptah migrations validate`; rolling back below the
// checkpoint boundary is refused with a clear error while rolling back to zero
// runs the checkpoint's down body; and a post-checkpoint migration bootstraps
// on top of the checkpoint, converging both paths to the same final schema.
type CheckpointWorkflowProbe struct {
	// FixtureRoot contains the committed migration history and the
	// post-checkpoint migration pair. Relative paths are resolved from the probe
	// process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (CheckpointWorkflowProbe) Name() string { return "checkpoint-workflow" }

func (p CheckpointWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != checkpointWorkflowSentinel {
		return nil
	}

	root, err := p.fixturePath()
	if err != nil {
		return []Result{checkpointHarnessFailure("fixture setup", err)}
	}
	bin, err := p.binary()
	if err != nil {
		return []Result{checkpointHarnessFailure("binary build", err)}
	}
	runRoot, err := os.MkdirTemp("", "ptah-checkpoint-*")
	if err != nil {
		return []Result{checkpointHarnessFailure("runtime setup", err)}
	}
	defer func() { _ = os.RemoveAll(runRoot) }()

	return runCheckpointWorkflow(bin, root, runRoot)
}

func (p CheckpointWorkflowProbe) fixturePath() (string, error) {
	root := strings.TrimSpace(p.FixtureRoot)
	if root == "" {
		return "", fmt.Errorf("fixture root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve fixture root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat fixture root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("fixture root is not a directory: %s", absolute)
	}
	return absolute, nil
}

func (p CheckpointWorkflowProbe) binary() (string, error) {
	if strings.TrimSpace(p.Binary) != "" {
		return p.Binary, nil
	}
	return ptahBinary()
}

func runCheckpointWorkflow(bin, root, runRoot string) []Result {
	migrationsDir := filepath.Join(runRoot, "migrations")
	// The probe copies the committed history because `ptah migrations checkpoint`
	// writes the checkpoint pair and the rewritten ptah.sum into the directory it
	// squashes; the committed fixture must stay pristine.
	if err := os.CopyFS(migrationsDir, os.DirFS(filepath.Join(root, "migrations"))); err != nil {
		return []Result{checkpointHarnessFailure("runtime setup", err)}
	}

	w := &checkpointWorkflow{
		bin:           bin,
		root:          root,
		runRoot:       runRoot,
		migrationsDir: migrationsDir,
		fullDB:        filepath.Join(runRoot, "full-history.db"),
		bootstrapDB:   filepath.Join(runRoot, "bootstrap.db"),
		continueDB:    filepath.Join(runRoot, "continue.db"),
		shadowDB:      filepath.Join(runRoot, "shadow.db"),
	}
	return w.run()
}

type checkpointWorkflow struct {
	bin           string
	root          string
	runRoot       string
	migrationsDir string
	// fullDB replays the entire migration history and later the post-checkpoint
	// migration — the oracle every checkpoint bootstrap is compared against.
	fullDB string
	// bootstrapDB starts fresh after the checkpoint exists and must bootstrap
	// from the checkpoint alone.
	bootstrapDB string
	// continueDB starts fresh after a post-checkpoint migration is added and
	// must apply the checkpoint plus the new migration.
	continueDB string
	// shadowDB is the ephemeral database the checkpoint command replays the
	// directory into.
	shadowDB string
}

// run executes the workflow steps in order. Each step depends on the state the
// previous step established, so a non-OK step short-circuits: the slice
// returned always ends with the first divergence, which keeps the gate red on
// the real problem instead of a cascade of misleading follow-on results.
func (w *checkpointWorkflow) run() []Result {
	steps := []func() Result{
		w.fullHistoryApplication,
		w.checkpointCreation,
		w.checkpointIntegrity,
		w.freshBootstrap,
		w.bootstrapEquivalence,
		w.statusConvergence,
		w.alreadyMigratedNoOp,
		w.tamperDetection,
		w.rollbackBoundaryGuard,
		w.rollbackToZero,
		w.postCheckpointContinuation,
		w.postCheckpointEquivalence,
	}
	results := make([]Result, 0, len(steps))
	for _, step := range steps {
		result := step()
		results = append(results, result)
		if result.Outcome != OK {
			break
		}
	}
	return results
}

func (w *checkpointWorkflow) fullHistoryApplication() Result {
	const (
		fixture = "ptah migrations up"
		stage   = "full history application"
	)
	if harness := w.hashMigrations(stage); harness != nil {
		return *harness
	}
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", sqliteURL(w.fullDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	if gap := w.expectAppliedVersions(fixture, stage, w.fullDB, []int64{1, 2, 3}); gap != nil {
		return *gap
	}
	return checkpointOK(fixture, stage,
		"the three-migration history applied in full, recording versions 1-3 individually")
}

func (w *checkpointWorkflow) checkpointCreation() Result {
	const (
		fixture = "ptah migrations checkpoint"
		stage   = "checkpoint creation"
	)
	result, harness := w.runCLI(stage,
		"migrations", "checkpoint",
		"--migrations-dir", w.migrationsDir,
		"--shadow-db", sqliteURL(w.shadowDB),
		"--description", "squash",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	// Version 4 is the documented default: one above the newest migration.
	if !strings.Contains(result.stdout, "Wrote checkpoint version 4") {
		return checkpointGap(fixture, stage,
			"checkpoint did not report writing default version 4 (one above the newest migration): "+result.diagnostic())
	}
	up, err := os.ReadFile(filepath.Join(w.migrationsDir, checkpointUpFile))
	if err != nil {
		return checkpointGap(fixture, stage, "checkpoint up file is missing: "+err.Error())
	}
	down, err := os.ReadFile(filepath.Join(w.migrationsDir, checkpointDownFile))
	if err != nil {
		return checkpointGap(fixture, stage, "checkpoint down file is missing: "+err.Error())
	}
	// The up body must be the *cumulative* schema from the shadow replay: the
	// users table already contains the bio column added by migration 2, and the
	// posts table plus its index from migration 3 are present.
	for _, fragment := range []string{
		`CREATE TABLE "users"`,
		`"bio" TEXT`,
		`CREATE TABLE "posts"`,
		`"idx_posts_user_id"`,
	} {
		if !strings.Contains(string(up), fragment) {
			return checkpointGap(fixture, stage,
				fmt.Sprintf("checkpoint up body is missing the cumulative-schema fragment %q", fragment))
		}
	}
	for _, fragment := range []string{
		`DROP TABLE IF EXISTS "posts"`,
		`DROP TABLE IF EXISTS "users"`,
	} {
		if !strings.Contains(string(down), fragment) {
			return checkpointGap(fixture, stage,
				fmt.Sprintf("checkpoint down body is missing %q", fragment))
		}
	}
	sum, err := os.ReadFile(filepath.Join(w.migrationsDir, "ptah.sum"))
	if err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	for _, file := range []string{checkpointUpFile, checkpointDownFile} {
		if !strings.Contains(string(sum), file+" h1:") {
			return checkpointGap(fixture, stage,
				"ptah.sum was not rewritten to cover "+file)
		}
	}
	return checkpointOK(fixture, stage,
		"the shadow replay produced the deterministic version-4 checkpoint pair with the cumulative schema, and ptah.sum was rewritten to cover it")
}

func (w *checkpointWorkflow) checkpointIntegrity() Result {
	const (
		fixture = "ptah migrations validate"
		stage   = "checkpoint integrity"
	)
	result, harness := w.runCLI(stage,
		"migrations", "validate",
		"--dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	if !strings.Contains(result.stdout, "matches ptah.sum") {
		return checkpointGap(fixture, stage,
			"validate did not confirm the directory matches ptah.sum: "+result.diagnostic())
	}
	return checkpointOK(fixture, stage,
		"the directory including the fresh checkpoint pair validates against the rewritten ptah.sum")
}

func (w *checkpointWorkflow) freshBootstrap() Result {
	const (
		fixture = "ptah migrations up"
		stage   = "fresh bootstrap"
	)
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", sqliteURL(w.bootstrapDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	// A fresh database runs the checkpoint alone: its single revision row
	// records the squashed history as satisfied, and the squashed migrations
	// 1-3 are never executed or recorded individually.
	if gap := w.expectAppliedVersions(fixture, stage, w.bootstrapDB, []int64{4}); gap != nil {
		return *gap
	}
	return checkpointOK(fixture, stage,
		"a fresh database bootstrapped from the checkpoint alone, recording only revision 4 with the squashed history satisfied")
}

func (w *checkpointWorkflow) bootstrapEquivalence() Result {
	const (
		fixture = "SQLite schema facts"
		stage   = "bootstrap schema equivalence"
	)
	return w.expectSchemaEquivalence(fixture, stage, w.fullDB, w.bootstrapDB,
		"the checkpoint-bootstrapped schema is structurally identical to the full-history replay (tables, columns, defaults, primary keys, foreign keys, and indexes)")
}

func (w *checkpointWorkflow) statusConvergence() Result {
	const (
		fixture = "ptah migrations status"
		stage   = "status convergence"
	)
	full, gap := w.migrationStatus(fixture, stage, w.fullDB)
	if gap != nil {
		return *gap
	}
	bootstrap, gap := w.migrationStatus(fixture, stage, w.bootstrapDB)
	if gap != nil {
		return *gap
	}
	// The already-migrated database must not see the checkpoint as pending; the
	// bootstrapped database must be complete with nothing pending either.
	if !slices.Equal(full.Applied, []int64{1, 2, 3}) || len(full.Pending) != 0 || full.HasPendingChanges {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"already-migrated status applied=%v pending=%v has_pending=%t, want applied=[1 2 3] with nothing pending",
			full.Applied, full.Pending, full.HasPendingChanges))
	}
	if !slices.Equal(bootstrap.Applied, []int64{4}) || len(bootstrap.Pending) != 0 || bootstrap.HasPendingChanges {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"bootstrapped status applied=%v pending=%v has_pending=%t, want applied=[4] with nothing pending",
			bootstrap.Applied, bootstrap.Pending, bootstrap.HasPendingChanges))
	}
	return checkpointOK(fixture, stage,
		"status reflects the bootstrap decision on both databases: the checkpoint is not pending on the already-migrated one, and the bootstrapped one is complete at revision 4")
}

func (w *checkpointWorkflow) alreadyMigratedNoOp() Result {
	const (
		fixture = "ptah migrations up"
		stage   = "already-migrated no-op"
	)
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", sqliteURL(w.fullDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	if !strings.Contains(result.stdout, "already up to date") {
		return checkpointGap(fixture, stage,
			"re-running up on the already-migrated database was not a no-op: "+result.diagnostic())
	}
	if gap := w.expectAppliedVersions(fixture, stage, w.fullDB, []int64{1, 2, 3}); gap != nil {
		return *gap
	}
	return checkpointOK(fixture, stage,
		"the already-migrated database ignored the checkpoint: re-running up applied nothing and left revisions 1-3 unchanged")
}

func (w *checkpointWorkflow) tamperDetection() Result {
	const (
		fixture = "ptah migrations validate"
		stage   = "tamper detection"
	)
	checkpointPath := filepath.Join(w.migrationsDir, checkpointUpFile)
	original, err := os.ReadFile(checkpointPath)
	if err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	if err := os.WriteFile(checkpointPath, append(slices.Clone(original), []byte("-- tampered\n")...), 0o600); err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	result, harness := w.runCLI(stage,
		"migrations", "validate",
		"--dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	// Restore before judging the outcome so a gap here never cascades into the
	// remaining steps for the wrong reason.
	if restoreErr := os.WriteFile(checkpointPath, original, 0o600); restoreErr != nil {
		return checkpointHarnessFailure(stage, restoreErr)
	}
	if harness != nil {
		return *harness
	}
	if result.exitCode != 1 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected the tampered checkpoint to fail validation with exit code 1, got %d: %s",
			result.exitCode, result.diagnostic()))
	}
	if !strings.Contains(result.stderr, "does not match ptah.sum") ||
		!strings.Contains(result.stderr, "changed: "+checkpointUpFile) {
		return checkpointGap(fixture, stage,
			"tampered-checkpoint failure did not name the changed checkpoint file: "+result.diagnostic())
	}
	restored, harness := w.runCLI(stage,
		"migrations", "validate",
		"--dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if restored.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"restored checkpoint should validate cleanly again, got exit code %d: %s",
			restored.exitCode, restored.diagnostic()))
	}
	return checkpointOK(fixture, stage,
		"a single tampered byte in the checkpoint file failed validation naming that file, and restoring the bytes validated cleanly again")
}

func (w *checkpointWorkflow) rollbackBoundaryGuard() Result {
	const (
		fixture = "ptah migrations down"
		stage   = "rollback boundary guard"
	)
	result, harness := w.runCLI(stage,
		"migrations", "down",
		"--db-url", sqliteURL(w.bootstrapDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
		"--target", "2",
		"--confirm",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 2 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected the below-checkpoint rollback to fail with exit code 2, got %d: %s",
			result.exitCode, result.diagnostic()))
	}
	for _, fragment := range []string{
		"cannot roll back to version 2",
		"below checkpoint 4",
		"roll back to version 4 (the checkpoint) or to 0 (drop everything)",
	} {
		if !strings.Contains(result.stderr, fragment) {
			return checkpointGap(fixture, stage,
				fmt.Sprintf("boundary refusal is missing %q: %s", fragment, result.diagnostic()))
		}
	}
	if gap := w.expectAppliedVersions(fixture, stage, w.bootstrapDB, []int64{4}); gap != nil {
		return *gap
	}
	return checkpointOK(fixture, stage,
		"rolling back below the checkpoint boundary was refused with exit code 2 and an actionable error, leaving the database untouched")
}

func (w *checkpointWorkflow) rollbackToZero() Result {
	const (
		fixture = "ptah migrations down"
		stage   = "rollback to zero"
	)
	result, harness := w.runCLI(stage,
		"migrations", "down",
		"--db-url", sqliteURL(w.bootstrapDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
		"--target", "0",
		"--confirm",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	if gap := w.expectAppliedVersions(fixture, stage, w.bootstrapDB, nil); gap != nil {
		return *gap
	}
	db, err := openSQLiteRuntimeDB(w.bootstrapDB)
	if err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	defer func() { _ = db.Close() }()
	tables, err := sqliteTableNames(db)
	if err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	if !slices.Equal(tables, []string{"schema_migrations"}) {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"rolling back to zero left tables %v, want only the empty revision table", tables))
	}
	return checkpointOK(fixture, stage,
		"rolling back to zero ran the checkpoint's down body, dropping the cumulative schema and clearing the revision history")
}

func (w *checkpointWorkflow) postCheckpointContinuation() Result {
	const (
		fixture = "ptah migrations up"
		stage   = "post-checkpoint continuation"
	)
	if err := os.CopyFS(w.migrationsDir, os.DirFS(filepath.Join(w.root, "post"))); err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	if harness := w.hashMigrations(stage); harness != nil {
		return *harness
	}
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", sqliteURL(w.continueDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	// A fresh database now applies exactly two migrations: the checkpoint and
	// the post-checkpoint migration 5 — never the squashed history.
	if gap := w.expectAppliedVersions(fixture, stage, w.continueDB, []int64{4, 5}); gap != nil {
		return *gap
	}
	return checkpointOK(fixture, stage,
		"a fresh database bootstrapped from the checkpoint and applied only the post-checkpoint migration, recording revisions 4 and 5")
}

func (w *checkpointWorkflow) postCheckpointEquivalence() Result {
	const (
		fixture = "SQLite schema facts"
		stage   = "post-checkpoint schema equivalence"
	)
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", sqliteURL(w.fullDB),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return checkpointGap(fixture, stage, fmt.Sprintf(
			"applying the post-checkpoint migration to the full-history database failed with exit code %d: %s",
			result.exitCode, result.diagnostic()))
	}
	// The already-migrated database applies only migration 5 — still no
	// checkpoint row — while the fresh one bootstrapped through the checkpoint.
	if gap := w.expectAppliedVersions(fixture, stage, w.fullDB, []int64{1, 2, 3, 5}); gap != nil {
		return *gap
	}
	return w.expectSchemaEquivalence(fixture, stage, w.fullDB, w.continueDB,
		"the full-history path (1-3 then 5) and the checkpoint bootstrap path (4 then 5) converged to structurally identical schemas")
}

// hashMigrations (re)writes ptah.sum through the CLI so the directory under
// test is always a valid, integrity-covered Ptah migration directory.
func (w *checkpointWorkflow) hashMigrations(stage string) *Result {
	result, harness := w.runCLI(stage,
		"migrations", "hash",
		"--dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return harness
	}
	if result.exitCode != 0 {
		failure := checkpointHarnessFailure(stage, fmt.Errorf(
			"hash migration directory: exit code %d: %s", result.exitCode, result.diagnostic()))
		return &failure
	}
	return nil
}

// runCLI runs a Ptah CLI command in the run directory. It returns either a
// harness Fail (process could not run) via the pointer, or the completed
// command result for the caller to validate.
func (w *checkpointWorkflow) runCLI(stage string, args ...string) (ptahCommandResult, *Result) {
	result, err := runPtahCommandInDir(w.bin, args, w.runRoot)
	if err != nil {
		failure := checkpointHarnessFailure(stage, fmt.Errorf(
			"execute `ptah %s`: %w; %s", strings.Join(args, " "), err, result.diagnostic()))
		return result, &failure
	}
	return result, nil
}

// expectAppliedVersions reads the revision table directly, independently of the
// CLI, and returns a gap when the recorded history differs from want.
func (w *checkpointWorkflow) expectAppliedVersions(fixture, stage, dbPath string, want []int64) *Result {
	got, err := checkpointAppliedVersions(dbPath)
	if err != nil {
		failure := checkpointHarnessFailure(stage, err)
		return &failure
	}
	if !slices.Equal(got, want) {
		gap := checkpointGap(fixture, stage, fmt.Sprintf(
			"recorded revision history %v, want %v", got, want))
		return &gap
	}
	return nil
}

func (w *checkpointWorkflow) expectSchemaEquivalence(fixture, stage, wantPath, gotPath, okDetail string) Result {
	want, err := sqliteSchemaFacts(wantPath)
	if err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	got, err := sqliteSchemaFacts(gotPath)
	if err != nil {
		return checkpointHarnessFailure(stage, err)
	}
	if len(want) == 0 {
		return checkpointGap(fixture, stage, "full-replay schema facts are unexpectedly empty")
	}
	if diff := firstFactDivergence(want, got); diff != "" {
		return checkpointGap(fixture, stage,
			"checkpoint-derived schema diverges from the full replay: "+diff)
	}
	return checkpointOK(fixture, stage, okDetail)
}

// checkpointAppliedVersions returns the versions recorded in the revision
// table, sorted, read straight from SQLite so the probe verifies what Ptah
// actually persisted rather than trusting the command's own report.
func checkpointAppliedVersions(dbPath string) ([]int64, error) {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(context.Background(),
		`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// checkpointMigrationStatus is the subset of `ptah migrations status --json`
// output the probe asserts on.
type checkpointMigrationStatus struct {
	CurrentVersion    int64   `json:"current_version"`
	Applied           []int64 `json:"applied_migrations"`
	Pending           []int64 `json:"pending_migrations"`
	HasPendingChanges bool    `json:"has_pending_changes"`
}

func (w *checkpointWorkflow) migrationStatus(fixture, stage, dbPath string) (checkpointMigrationStatus, *Result) {
	var status checkpointMigrationStatus
	result, harness := w.runCLI(stage,
		"migrations", "status",
		"--db-url", sqliteURL(dbPath),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
		"--json",
	)
	if harness != nil {
		return status, harness
	}
	if result.exitCode != 0 {
		gap := checkpointGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
		return status, &gap
	}
	if err := json.Unmarshal([]byte(result.stdout), &status); err != nil {
		gap := checkpointGap(fixture, stage,
			"status --json did not emit parseable JSON: "+oneLine(err.Error()))
		return status, &gap
	}
	return status, nil
}

// sqliteSchemaFacts reduces a live SQLite database to a sorted, canonical set
// of structural facts — object inventory, column definitions in physical
// order, foreign keys, and explicitly created indexes — so two databases
// compare by what their schemas *are* rather than how their DDL was spelled.
// The full replay executes the hand-written fixture SQL while the bootstrap
// executes Ptah's rendered checkpoint SQL, so the raw sqlite_schema text
// legitimately differs (quoting, whitespace, named constraints) even when the
// schemas are identical.
func sqliteSchemaFacts(dbPath string) ([]string, error) {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	objects, tables, err := sqliteObjectInventory(ctx, db)
	if err != nil {
		return nil, err
	}
	facts := []string{"objects: " + strings.Join(objects, ",")}
	for _, table := range tables {
		tableFacts, err := sqliteTableStructureFacts(ctx, db, table)
		if err != nil {
			return nil, err
		}
		facts = append(facts, tableFacts...)
	}
	slices.Sort(facts)
	return facts, nil
}

func sqliteObjectInventory(ctx context.Context, db *sql.DB) (objects, tables []string, err error) {
	rows, err := db.QueryContext(ctx, `
SELECT type, name
FROM sqlite_schema
WHERE name NOT LIKE 'sqlite_%'
ORDER BY type, name`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var objectType, name string
		if err := rows.Scan(&objectType, &name); err != nil {
			return nil, nil, err
		}
		objects = append(objects, objectType+":"+name)
		if objectType == "table" {
			tables = append(tables, name)
		}
	}
	return objects, tables, rows.Err()
}

func sqliteTableStructureFacts(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	var facts []string

	columns, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = columns.Close() }()
	for columns.Next() {
		var (
			cid, notNull, pk int
			name, columnType string
			defaultValue     sql.NullString
		)
		if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		normalizedDefault := "<none>"
		if defaultValue.Valid {
			normalizedDefault = defaultValue.String
		}
		facts = append(facts, fmt.Sprintf(
			"table %s column %d:%s type=%s notnull=%d default=%s pk=%d",
			table, cid, name, strings.ToUpper(columnType), notNull, normalizedDefault, pk))
	}
	if err := columns.Err(); err != nil {
		return nil, err
	}

	foreignKeys, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = foreignKeys.Close() }()
	for foreignKeys.Next() {
		var (
			id, seq                                int
			refTable, from, to, onUpdate, onDelete string
			match                                  string
		)
		if err := foreignKeys.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		facts = append(facts, fmt.Sprintf(
			"table %s fk %s->%s.%s on_update=%s on_delete=%s match=%s",
			table, from, refTable, to, onUpdate, onDelete, match))
	}
	if err := foreignKeys.Err(); err != nil {
		return nil, err
	}

	indexFacts, err := sqliteIndexStructureFacts(ctx, db, table)
	if err != nil {
		return nil, err
	}
	return append(facts, indexFacts...), nil
}

func sqliteIndexStructureFacts(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	indexes, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = indexes.Close() }()

	type indexEntry struct {
		name   string
		unique int
	}
	var explicit []indexEntry
	for indexes.Next() {
		var (
			seq, unique, partial int
			name, origin         string
		)
		if err := indexes.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		// Only indexes from explicit CREATE INDEX statements ("c") are schema
		// content; auto-indexes from PK/UNIQUE clauses are already covered by the
		// column facts.
		if origin == "c" {
			explicit = append(explicit, indexEntry{name: name, unique: unique})
		}
	}
	if err := indexes.Err(); err != nil {
		return nil, err
	}

	var facts []string
	for _, entry := range explicit {
		columnRows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%q)", entry.name))
		if err != nil {
			return nil, err
		}
		var columns []string
		for columnRows.Next() {
			var seqno, cid int
			var column sql.NullString
			if err := columnRows.Scan(&seqno, &cid, &column); err != nil {
				_ = columnRows.Close()
				return nil, err
			}
			columns = append(columns, column.String)
		}
		err = columnRows.Err()
		_ = columnRows.Close()
		if err != nil {
			return nil, err
		}
		facts = append(facts, fmt.Sprintf(
			"table %s index %s unique=%d columns=%s",
			table, entry.name, entry.unique, strings.Join(columns, ",")))
	}
	return facts, nil
}

// firstFactDivergence returns a human-readable description of the first
// difference between two sorted fact lists, or "" when they are equal. Only
// the first divergence is reported so the gate points at one concrete problem.
func firstFactDivergence(want, got []string) string {
	for i := range min(len(want), len(got)) {
		if want[i] != got[i] {
			return fmt.Sprintf("fact %q, want %q", got[i], want[i])
		}
	}
	switch {
	case len(want) > len(got):
		return fmt.Sprintf("missing fact %q", want[len(got)])
	case len(got) > len(want):
		return fmt.Sprintf("unexpected extra fact %q", got[len(want)])
	}
	return ""
}

func checkpointOK(fixture, stage, detail string) Result {
	return Result{
		Probe:   "checkpoint-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: OK,
		Detail:  detail,
	}
}

func checkpointGap(fixture, stage, detail string) Result {
	return Result{
		Probe:   "checkpoint-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: Gap,
		Detail:  detail,
		Issue:   checkpointWorkflowIssue,
	}
}

func checkpointHarnessFailure(stage string, err error) Result {
	return Result{
		Probe:   "checkpoint-workflow",
		Fixture: checkpointWorkflowSentinel,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}
