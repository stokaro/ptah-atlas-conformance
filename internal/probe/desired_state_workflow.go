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

// migrateDiffDesiredStateIssue tracks the follow-up that extended the same
// source model to `migrate diff`.
const migrateDiffDesiredStateIssue = "stokaro/ptah#842"

// DesiredStateWorkflowProbe executes the Atlas desired-state source model
// Ptah implements for `schema diff`, `schema apply`, and `migrate diff`
// (stokaro/ptah#811, stokaro/ptah#842) through the real ptah-compat CLI on
// ephemeral SQLite: database URLs, migration directories, and env://
// references; migrate-diff convergence; and desired/dev alias rejection before
// source mutation or artifact creation.
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
	migrateDiffRuntime := *w
	migrateDiffRuntime.issue = migrateDiffDesiredStateIssue
	m := &desiredStateWorkflow{proWorkflowRuntime: &migrateDiffRuntime}
	return w.runSteps([]func() Result{
		d.databaseURLDiffSource,
		d.databaseURLApplySource,
		d.migrationDirReplay,
		d.migrationDirWithoutDevDatabase,
		d.declarativeFileWithoutDevDatabase,
		d.sqlFileWithoutDevDatabase,
		d.mixedSourcesWithoutDevDatabase,
		d.sqlFileWithoutDevDatabaseIsAPolicy,
		d.envSourceResolution,
		m.migrateDiffDatabaseURLSource,
		m.migrateDiffEnvURLSource,
		m.migrateDiffRejectsDesiredDevAlias,
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
	if err := execSQLiteStatement(sourceDB, "CREATE TABLE users (id INTEGER PRIMARY KEY); INSERT INTO users (id) VALUES (42)"); err != nil {
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

// The three rows below and their control measure one axis the corpus never
// touched: what `schema apply --to file://...` does when no dev database is
// configured. Every other real-CLI apply in this repository passes --dev-url,
// and every local file it names is SQL, so the declarative half was inferred
// rather than measured.
//
// The axis is real. A declarative file describes the desired state directly, so
// Ptah can diff it against the target without replaying anything; a SQL file has
// to be executed somewhere first, and that somewhere is the dev database. One
// row on either format is the minimum that pins the rule: with only the HCL row,
// a change that dropped the requirement for every local file stays green; with
// only the SQL row, a change that re-narrowed it to all files stays green.
func (d *desiredStateWorkflow) declarativeFileWithoutDevDatabase() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "declarative file source without dev database"
	)
	targetDB := filepath.Join(d.runRoot, "target-hcl-nodev.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://to.hcl",
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
	// The table, not only the exit code. A gate that let the invocation past
	// without applying anything would satisfy every assertion above.
	if gap := d.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"a declarative desired state applied without --dev-url and the target carries the table it describes")
}

func (d *desiredStateWorkflow) sqlFileWithoutDevDatabase() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "SQL file source without dev database"
	)
	targetDB := filepath.Join(d.runRoot, "target-sql-nodev.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://to.sql",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"--dev-url cannot be empty",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectFileNeverCreated(fixture, stage, targetDB, "target database"); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"a SQL desired state without --dev-url was refused before the target database was created")
}

func (d *desiredStateWorkflow) mixedSourcesWithoutDevDatabase() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "mixed declarative and SQL sources without dev database"
	)
	targetDB := filepath.Join(d.runRoot, "target-mixed-nodev.db")
	result, failure := d.runCLI(stage,
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://to.hcl",
		"--to", "file://to.sql",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"--dev-url cannot be empty",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectFileNeverCreated(fixture, stage, targetDB, "target database"); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"a desired state mixing declarative and SQL sources needs a dev database: the set must be declarative in full, not in part")
}

func (d *desiredStateWorkflow) sqlFileWithoutDevDatabaseIsAPolicy() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "SQL file source without dev database, capability restored"
	)
	targetDB := filepath.Join(d.runRoot, "target-sql-nodev-allowed.db")
	result, failure := d.runCLIWithEnv(stage,
		[]string{"PTAH_ATLAS_APPLY_WITHOUT_DEV_URL=1"},
		"schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://to.sql",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"audit_logs", "users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"the SQL refusal is a policy an operator can lift, not a lost capability: the same invocation applies both tables when it is")
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
		"--dev-url", sqliteURL(filepath.Join(d.runRoot, "dev-env.db")),
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
