package probe

import (
	"os"
	"path/filepath"
	"strings"
)

const desiredStateWorkflowSentinel = "_capability/desired-state-workflow/SENTINEL"

// desiredStateIssue tracks the Atlas desired-state source-URL batch
// (stokaro/ptah#811): database URLs, migration directories, and env://
// references as `schema diff`/`schema apply` desired-state sources.
const desiredStateIssue = "stokaro/ptah#811"

// DesiredStateWorkflowProbe executes the Atlas desired-state source model
// Ptah implements for `schema diff` and `schema apply` (stokaro/ptah#811)
// through the real `atlas ...` CLI on ephemeral SQLite: a database URL
// as the `--from` diff source and as the `--to` apply source, a migration
// directory replayed on a dev database (and refused deterministically before
// the target is contacted when no dev database is configured), and an env://
// reference resolved through an evaluated atlas.hcl environment.
type DesiredStateWorkflowProbe struct {
	// FixtureRoot contains the committed desired-schema sources. Relative
	// paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and
	// local development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (DesiredStateWorkflowProbe) Name() string { return "desired-state-workflow" }

func (p DesiredStateWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != desiredStateWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("desired-state-workflow", desiredStateWorkflowSentinel, p.FixtureRoot, p.Binary, desiredStateIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	d := &desiredStateWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		d.databaseURLDiffSource,
		d.databaseURLApplySource,
		d.migrationDirReplay,
		d.migrationDirWithoutDevDatabase,
		d.envSourceResolution,
	})
}

type desiredStateWorkflow struct {
	*proWorkflowRuntime
}

// seedSourceDatabase creates the database-URL desired-state source outside
// the measured CLI.
func (d *desiredStateWorkflow) seedSourceDatabase(stage string) (string, *Result) {
	sourceDB := filepath.Join(d.runRoot, "source.db")
	if _, err := os.Stat(sourceDB); err == nil {
		return sourceDB, nil
	}
	if err := execSQLiteStatement(sourceDB, "CREATE TABLE users (id INTEGER PRIMARY KEY)"); err != nil {
		failure := d.harnessFailure(stage, err)
		return "", &failure
	}
	return sourceDB, nil
}

func (d *desiredStateWorkflow) databaseURLDiffSource() Result {
	const (
		fixture = "atlas schema diff"
		stage   = "database-url --from source"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	result, failure := d.runCLI(stage,
		"schema", "diff",
		"--from", sqliteURL(sourceDB),
		"--to", "file://to.sql",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`CREATE TABLE "audit_logs"`,
	}); gap != nil {
		return *gap
	}
	if strings.Contains(result.stdout, `CREATE TABLE "users"`) {
		return d.gap(fixture, stage, "the diff re-creates the users table the database-URL source already holds: "+oneLine(result.stdout))
	}
	return d.ok(fixture, stage,
		"`schema diff --from sqlite://...` introspected the live source database and planned only the missing audit_logs table against the local desired file")
}

func (d *desiredStateWorkflow) databaseURLApplySource() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "database-url --to source"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	targetDB := filepath.Join(d.runRoot, "target-db.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", sqliteURL(sourceDB),
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schema apply completed successfully.",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`schema apply --to sqlite://...` mirrored the live source database onto the target: the desired state was another database's introspected schema")
}

func (d *desiredStateWorkflow) migrationDirReplay() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "migration-dir source replay"
	)
	if failure := d.hashAtlasMigrations(stage); failure != nil {
		return *failure
	}
	targetDB := filepath.Join(d.runRoot, "target-mig.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://migrations",
		"--dev-url", sqliteURL(filepath.Join(d.runRoot, "dev-mig.db")),
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schema apply completed successfully.",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"replayed_users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`schema apply --to file://migrations` replayed the atlas.sum-covered migration directory on the dev database and applied the materialized schema to the target")
}

func (d *desiredStateWorkflow) migrationDirWithoutDevDatabase() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "migration-dir source without dev database"
	)
	targetDB := filepath.Join(d.runRoot, "target-nodev.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://migrations",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"is a migration directory; --dev-url is required to replay it on a dev database",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectFileNeverCreated(fixture, stage, targetDB, "target database"); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"a migration-directory desired state without --dev-url was refused with the deterministic diagnostic before the target database was contacted")
}

func (d *desiredStateWorkflow) envSourceResolution() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "env:// source resolution"
	)
	targetDB := filepath.Join(d.runRoot, "target-env.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "env://src",
		"--env", "dev",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`schema apply --to env://src` resolved the desired state through the evaluated atlas.hcl environment's src attribute and applied it to the target")
}
