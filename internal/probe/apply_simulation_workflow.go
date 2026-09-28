package probe

import (
	"fmt"
	"os"
	"path/filepath"
)

const applySimulationWorkflowSentinel = "_capability/apply-simulation-workflow/SENTINEL"

// applySimulationIssue tracks the `schema apply` locking and dev-database
// plan-simulation batch (stokaro/ptah#812).
const applySimulationIssue = "stokaro/ptah#812"

// schemaApplyLockUnsupportedNote is the deterministic note `schema apply
// --lock-timeout` prints on a dialect without advisory-lock support; the flag
// is accepted as an explicit no-op instead of being rejected.
const schemaApplyLockUnsupportedNote = `note: schema apply locking is not supported for dialect "sqlite"; --lock-timeout is ignored and the apply proceeds without a database lock`

// ApplySimulationWorkflowProbe executes the `schema apply` guard rails from
// stokaro/ptah#812 through the real `atlas ...` CLI on ephemeral SQLite:
// `--lock-timeout` is accepted (an explicit noted no-op on lockless SQLite),
// `--dev-url` rehearses the exact plan on a clean dev database before the
// target is touched, a dev database that still holds a table is refused before
// anything is reset, a failing rehearsal refuses the apply with the target
// left unchanged, and pointing `--dev-url` at the target itself is refused
// before the destructive dev reset, whether the target holds a table or not.
type ApplySimulationWorkflowProbe struct {
	// FixtureRoot contains the committed desired-schema source file. Relative
	// paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and
	// local development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ApplySimulationWorkflowProbe) Name() string { return "apply-simulation-workflow" }

func (p ApplySimulationWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != applySimulationWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("apply-simulation-workflow", applySimulationWorkflowSentinel, p.FixtureRoot, p.Binary, applySimulationIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	s := &applySimulationWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		s.lockTimeoutNotedNoOp,
		s.simulationSuccess,
		s.dirtyDevDatabaseRefused,
		s.simulationFailureRefusesTarget,
		s.devURLIsTargetHoldingTable,
		s.devURLIsEmptyTarget,
	})
}

type applySimulationWorkflow struct {
	*proWorkflowRuntime
}

func (s *applySimulationWorkflow) lockTimeoutNotedNoOp() Result {
	const (
		fixture = "atlas schema apply --lock-timeout"
		stage   = "lockless dialect note"
	)
	targetDB := filepath.Join(s.runRoot, "lock-target.db")
	result, failure := s.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(s.runRoot, "lock-dev.db")),
		"--lock-timeout", "10s",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	// The note is diagnostic metadata: it goes to stderr while the apply
	// output stays on stdout.
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		schemaApplyLockUnsupportedNote,
	}); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schema apply completed successfully.",
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"users"}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"`schema apply --lock-timeout` is accepted on lockless SQLite as an explicit no-op with a deterministic stderr note, and the apply proceeds")
}

func (s *applySimulationWorkflow) simulationSuccess() Result {
	const (
		fixture = "atlas schema apply --dev-url"
		stage   = "plan simulation success"
	)
	targetDB := filepath.Join(s.runRoot, "sim-target.db")
	devDB := filepath.Join(s.runRoot, "sim-dev.db")
	result, failure := s.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(devDB),
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schema apply completed successfully.",
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"users"}); gap != nil {
		return *gap
	}
	// Atlas CE v1.3.0 cleans the dev database after a successful rehearsal.
	// The failed-rehearsal step below proves plan execution independently; this
	// assertion pins the successful cleanup contract and proves the stale table
	// was not restored.
	if gap := s.expectSQLiteTablesAt(fixture, stage, devDB, nil); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"`schema apply --dev-url` rehearsed the plan on the clean dev database before applying it to the target, and cleaned the dev database afterwards like Atlas CE v1.3.0")
}

// dirtyDevDatabaseRefused pins what Atlas CE v1.3.0 does with a dev database
// that still holds a table: it refuses before taking its snapshot, with the
// error below, and neither database changes. Ptah refuses it the same way
// since stokaro/ptah#3827.
func (s *applySimulationWorkflow) dirtyDevDatabaseRefused() Result {
	const (
		fixture = "atlas schema apply --dev-url"
		stage   = "dirty dev database refused"
	)
	targetDB := filepath.Join(s.runRoot, "dirty-target.db")
	devDB := filepath.Join(s.runRoot, "dirty-dev.db")
	if err := execSQLiteStatement(devDB, "CREATE TABLE sim_stale (id INTEGER PRIMARY KEY)"); err != nil {
		return s.harnessFailure(stage, err)
	}
	result, failure := s.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(devDB),
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		`sql/migrate: taking database snapshot: sql/migrate: connected database is not clean: found table "sim_stale"`,
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, devDB, []string{"sim_stale"}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, nil); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"`schema apply --dev-url` refused a dev database that still held a table with Atlas CE v1.3.0's error, left the table in place, and created nothing on the target")
}

func (s *applySimulationWorkflow) simulationFailureRefusesTarget() Result {
	const (
		fixture = "atlas schema apply --dev-url"
		stage   = "failed simulation refuses the target"
	)
	// A hermetic scripted $EDITOR appends a statement that collides with the
	// planned one, so the rehearsal on the dev database fails
	// deterministically — the same technique Ptah's own tests use.
	editorPath := filepath.Join(s.runRoot, "append-editor.sh")
	editorScript := "#!/bin/sh\nfor f in \"$@\"; do\n  printf '%s\\n' 'CREATE TABLE users (id INTEGER PRIMARY KEY);' >> \"$f\"\ndone\n"
	if err := os.WriteFile(editorPath, []byte(editorScript), 0o700); err != nil { //nolint:gosec // the editor script must be executable
		return s.harnessFailure(stage, fmt.Errorf("write scripted editor: %w", err))
	}
	targetDB := filepath.Join(s.runRoot, "sim-fail-target.db")
	result, failure := s.runCLIWithEnv(stage, []string{"EDITOR=" + editorPath, "VISUAL="},
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(s.runRoot, "sim-fail-dev.db")),
		"--edit",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	// Wording is a PTAH-SIDE PIN with no Atlas artifact behind it:
	// stokaro/ptah#965 reworded this diagnostic's reassurance clause from "the
	// target database was left unchanged" to "the plan was not applied to the
	// target database". The substantive guarantee is asserted independently
	// below by reading the target database directly, so this fragment check
	// only guards Ptah's own message contract.
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"dev database simulation failed during plan",
		"the plan was not applied to the target database",
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, nil); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"PTAH-SIDE PIN (diagnostic wording has no Atlas artifact behind it): a plan whose rehearsal fails on the dev database refuses the apply with exit 1, naming the simulation failure, and leaves the target without any user table (verified by reading the target directly)")
}

// devURLIsTargetHoldingTable points --dev-url at a target that holds a
// table. Atlas CE v1.3.0 refuses it with the clean check that also guards any
// dirty dev database, and Ptah refuses it the same way: the table survives.
func (s *applySimulationWorkflow) devURLIsTargetHoldingTable() Result {
	const (
		fixture = "atlas schema apply --dev-url"
		stage   = "dev database is the target, holding a table"
	)
	targetDB := filepath.Join(s.runRoot, "sim-same.db")
	// The marker table proves afterwards that the target was not reset.
	if err := execSQLiteStatement(targetDB, "CREATE TABLE keepme (id INTEGER PRIMARY KEY)"); err != nil {
		return s.harnessFailure(stage, err)
	}
	result, failure := s.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(targetDB),
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		`sql/migrate: taking database snapshot: sql/migrate: connected database is not clean: found table "keepme"`,
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"keepme"}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"pointing --dev-url at a target that holds a table is refused before any reset with Atlas CE v1.3.0's clean check: the target's existing table survived untouched")
}

// devURLIsEmptyTarget points --dev-url at an empty target, where the clean
// check passes. Atlas CE v1.3.0 applies the schema there, using the target as
// its own dev database. Ptah refuses it by name instead, deliberately stricter:
// its dev-database rehearsal resets the dev database destructively, and the
// feature matrix row "Dev-database rehearsal before apply" records the abort.
func (s *applySimulationWorkflow) devURLIsEmptyTarget() Result {
	const (
		fixture = "atlas schema apply --dev-url"
		stage   = "dev database is the target, empty"
	)
	targetDB := filepath.Join(s.runRoot, "sim-same-empty.db")
	result, failure := s.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(targetDB),
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"--dev-url must not point at the target database",
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, nil); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"pointing --dev-url at an empty target is refused by name before the destructive dev reset, and nothing is applied to the target")
}
