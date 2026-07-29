package probe

import (
	"os"
	"path/filepath"
	"slices"
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
// (stokaro/ptah#811, stokaro/ptah#842) through the real `ptah atlas ...` CLI on
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
	if err := execSQLiteStatement(sourceDB, "CREATE TABLE users (id INTEGER PRIMARY KEY)"); err != nil {
		failure := d.harnessFailure(stage, err)
		return "", &failure
	}
	return sourceDB, nil
}

func (d *desiredStateWorkflow) databaseURLDiffSource() Result {
	const (
		fixture = "ptah atlas schema diff"
		stage   = "database-url --from source"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	result, failure := d.runCLI(stage,
		"atlas", "schema", "diff",
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
		fixture = "ptah atlas schema apply"
		stage   = "database-url --to source"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	targetDB := filepath.Join(d.runRoot, "target-db.db")
	result, failure := d.runCLI(stage,
		"atlas", "schema", "apply",
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
		fixture = "ptah atlas schema apply"
		stage   = "migration-dir source replay"
	)
	if failure := d.hashAtlasMigrations(stage); failure != nil {
		return *failure
	}
	targetDB := filepath.Join(d.runRoot, "target-mig.db")
	result, failure := d.runCLI(stage,
		"atlas", "schema", "apply",
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
		fixture = "ptah atlas schema apply"
		stage   = "migration-dir source without dev database"
	)
	targetDB := filepath.Join(d.runRoot, "target-nodev.db")
	result, failure := d.runCLI(stage,
		"atlas", "schema", "apply",
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
		fixture = "ptah atlas schema apply"
		stage   = "env:// source resolution"
	)
	targetDB := filepath.Join(d.runRoot, "target-env.db")
	result, failure := d.runCLI(stage,
		"atlas", "schema", "apply",
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

func (d *desiredStateWorkflow) migrateDiffDatabaseURLSource() Result {
	const (
		fixture = "ptah atlas migrate diff"
		stage   = "database-url --to source converges"
		dirName = "migrate-database"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	devURL := sqliteURL(filepath.Join(d.runRoot, "migrate-database-dev.db"))
	runDiff := func(name string) (ptahCommandResult, *Result) {
		return d.runCLI(stage,
			"atlas", "migrate", "diff", name,
			"--to", sqliteURL(sourceDB),
			"--dev-url", devURL,
			"--dir", "file://"+dirName,
		)
	}
	result, failure := runDiff("add_users")
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Created migration file:",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectMigrateDiffArtifacts(fixture, stage, dirName, "users"); gap != nil {
		return *gap
	}

	result, failure = runDiff("noop")
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"The migration directory is synced with the desired state",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectMigrateDiffArtifacts(fixture, stage, dirName, "users"); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, sourceDB, []string{"users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`migrate diff --to sqlite://...` introspected the live desired database, wrote one integrity-covered migration, converged on replay, and left the source database unchanged")
}

func (d *desiredStateWorkflow) migrateDiffEnvURLSource() Result {
	const (
		fixture = "ptah atlas migrate diff"
		stage   = "env://url source with project defaults"
		dirName = "migrate-env"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	result, failure := d.runCLI(stage,
		"atlas", "migrate", "diff", "add_env_users",
		"--config", "file://migrate.hcl",
		"--env", "dev",
		"--to", "env://url",
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Created migration file:",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectMigrateDiffArtifacts(fixture, stage, dirName, "users"); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, sourceDB, []string{"users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`migrate diff --to env://url` resolved the live desired database and took dev/migration defaults from the evaluated atlas.hcl environment without mutating the source")
}

func (d *desiredStateWorkflow) migrateDiffRejectsDesiredDevAlias() Result {
	const (
		fixture = "ptah atlas migrate diff"
		stage   = "desired and dev database alias rejected"
		dirName = "migrate-alias"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	sourceURL := sqliteURL(sourceDB)
	result, failure := d.runCLI(stage,
		"atlas", "migrate", "diff", "unsafe",
		"--to", sourceURL,
		"--dev-url", sourceURL,
		"--dir", "file://"+dirName,
	)
	if failure != nil {
		return *failure
	}
	if gap := d.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"--to database must differ from --dev-url because the dev database is reset during planning",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, sourceDB, []string{"users"}); gap != nil {
		return *gap
	}
	if gap := d.expectFileNeverCreated(
		fixture,
		stage,
		filepath.Join(d.runRoot, dirName),
		"migration directory",
	); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"a desired database aliased to --dev-url was rejected before destructive replay; the source table survived and no migration directory was created")
}

func (d *desiredStateWorkflow) expectMigrateDiffArtifacts(
	fixture string,
	stage string,
	dirName string,
	table string,
) *Result {
	files, err := relativeFilesUnder(filepath.Join(d.runRoot, dirName))
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return &failure
	}
	var sqlFiles []string
	for _, file := range files {
		if strings.HasSuffix(file, ".sql") {
			sqlFiles = append(sqlFiles, file)
		}
	}
	if len(files) != 2 || len(sqlFiles) != 1 || !slices.Contains(files, "atlas.sum") {
		gap := d.gap(fixture, stage,
			"migrate diff artifacts are not one SQL migration plus atlas.sum: "+strings.Join(files, ", "))
		return &gap
	}
	content, err := readRunFile(d.runRoot, filepath.Join(dirName, sqlFiles[0]))
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return &failure
	}
	if !strings.Contains(content, "CREATE TABLE") || !strings.Contains(content, table) {
		gap := d.gap(fixture, stage,
			"generated migration does not create the desired table: "+oneLine(content))
		return &gap
	}
	return nil
}
