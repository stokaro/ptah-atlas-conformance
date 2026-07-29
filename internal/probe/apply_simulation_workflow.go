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
// stokaro/ptah#812 through the real `ptah atlas ...` CLI on ephemeral SQLite:
// `--lock-timeout` is accepted (an explicit noted no-op on lockless SQLite),
// `--dev-url` rehearses the exact plan on a reset dev database before the
// target is touched, a failing rehearsal refuses the apply with the target
// left unchanged, and pointing `--dev-url` at the target itself is refused
// before the destructive dev reset.
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
		s.simulationFailureRefusesTarget,
		s.devURLMustDifferFromTarget,
	})
}

type applySimulationWorkflow struct {
	*proWorkflowRuntime
}

func (s *applySimulationWorkflow) lockTimeoutNotedNoOp() Result {
	const (
		fixture = "ptah atlas schema apply --lock-timeout"
		stage   = "lockless dialect note"
	)
	targetDB := filepath.Join(s.runRoot, "lock-target.db")
	result, failure := s.runCLI(stage,
		"atlas", "schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
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
		fixture = "ptah atlas schema apply --dev-url"
		stage   = "plan simulation success"
	)
	targetDB := filepath.Join(s.runRoot, "sim-target.db")
	devDB := filepath.Join(s.runRoot, "sim-dev.db")
	// Pre-litter the dev database: the simulation must reset it first.
	if err := execSQLiteStatement(devDB, "CREATE TABLE sim_stale (id INTEGER PRIMARY KEY)"); err != nil {
		return s.harnessFailure(stage, err)
	}
	result, failure := s.runCLI(stage,
		"atlas", "schema", "apply",
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
	// The dev database ends at the rehearsed desired state with the stale
	// litter gone, proving the plan was actually executed there.
	if gap := s.expectSQLiteTablesAt(fixture, stage, devDB, []string{"users"}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"`schema apply --dev-url` reset the pre-littered dev database, rehearsed the plan on it, and only then applied the plan to the target")
}

func (s *applySimulationWorkflow) simulationFailureRefusesTarget() Result {
	const (
		fixture = "ptah atlas schema apply --dev-url"
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
		"atlas", "schema", "apply",
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
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"dev database simulation failed during plan",
		"the target database was left unchanged",
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, nil); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"a plan whose rehearsal fails on the dev database refuses the apply with exit 1, naming the simulation failure, and leaves the target without any user table")
}

func (s *applySimulationWorkflow) devURLMustDifferFromTarget() Result {
	const (
		fixture = "ptah atlas schema apply --dev-url"
		stage   = "dev database must differ from target"
	)
	targetDB := filepath.Join(s.runRoot, "sim-same.db")
	// The marker table proves afterwards that the target was not reset.
	if err := execSQLiteStatement(targetDB, "CREATE TABLE keepme (id INTEGER PRIMARY KEY)"); err != nil {
		return s.harnessFailure(stage, err)
	}
	result, failure := s.runCLI(stage,
		"atlas", "schema", "apply",
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
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"keepme"}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"pointing --dev-url at the target database is refused before the destructive dev reset: the target's existing table survived untouched")
}
