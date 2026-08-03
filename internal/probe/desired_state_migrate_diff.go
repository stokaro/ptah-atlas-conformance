package probe

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (d *desiredStateWorkflow) migrateDiffDatabaseURLSource() Result {
	const (
		fixture = "atlas migrate diff"
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
			"migrate", "diff", name,
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
	if gap := d.expectMigrateDiffValidation(fixture, stage, dirName); gap != nil {
		return *gap
	}
	before, harness := d.migrateDiffArtifactSnapshot(stage, dirName)
	if harness != nil {
		return *harness
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
	if gap := d.expectMigrateDiffValidation(fixture, stage, dirName); gap != nil {
		return *gap
	}
	if gap := d.expectMigrateDiffReplay(fixture, stage, dirName); gap != nil {
		return *gap
	}
	after, harness := d.migrateDiffArtifactSnapshot(stage, dirName)
	if harness != nil {
		return *harness
	}
	if !slices.Equal(after, before) {
		return d.gap(fixture, stage, "the converged migrate diff rewrote an existing migration or atlas.sum")
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, sourceDB, []string{"users"}); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteInt64ColumnAt(fixture, stage, sourceDB, "SELECT id FROM users ORDER BY id", []int64{42}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`migrate diff --to sqlite://...` introspected the live desired database, wrote one validated migration that replayed on a fresh target, converged without rewriting artifacts, and preserved the source data")
}

func (d *desiredStateWorkflow) migrateDiffEnvURLSource() Result {
	const (
		fixture = "atlas migrate diff"
		stage   = "env://url source with project defaults"
		dirName = "migrate-env"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	result, failure := d.runCLI(stage,
		"migrate", "diff", "add_env_users",
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
	if gap := d.expectMigrateDiffValidation(fixture, stage, dirName); gap != nil {
		return *gap
	}
	if gap := d.expectMigrateDiffReplay(fixture, stage, dirName); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, sourceDB, []string{"users"}); gap != nil {
		return *gap
	}
	if gap := d.expectSQLiteInt64ColumnAt(fixture, stage, sourceDB, "SELECT id FROM users ORDER BY id", []int64{42}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`migrate diff --to env://url` resolved the live desired database, took dev and migration defaults from atlas.hcl, generated a validated migration that replayed on a fresh target, and preserved the source data")
}

func (d *desiredStateWorkflow) migrateDiffRejectsDesiredDevAlias() Result {
	const (
		fixture = "atlas migrate diff"
		stage   = "desired and dev path alias rejected"
		dirName = "migrate-alias"
	)
	sourceDB, harness := d.seedSourceDatabase(stage)
	if harness != nil {
		return *harness
	}
	sourceURL := sqliteURL(sourceDB)
	aliasPath := filepath.Dir(sourceDB) + string(os.PathSeparator) + "." +
		string(os.PathSeparator) + filepath.Base(sourceDB)
	devURL := sqliteURL(aliasPath) + "?mode=rwc"
	result, failure := d.runCLI(stage,
		"migrate", "diff", "unsafe",
		"--to", sourceURL,
		"--dev-url", devURL,
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
	if gap := d.expectSQLiteInt64ColumnAt(fixture, stage, sourceDB, "SELECT id FROM users ORDER BY id", []int64{42}); gap != nil {
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
		"textually different SQLite URLs resolving to the same desired/dev database were rejected before destructive replay; the source table and data survived and no migration directory was created")
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

func (d *desiredStateWorkflow) expectMigrateDiffValidation(fixture, stage, dirName string) *Result {
	result, harness := d.runCLI(stage, "migrate", "validate", "--dir", "file://"+dirName)
	if harness != nil {
		return harness
	}
	return d.expectExit(fixture, stage, result, 0)
}

func (d *desiredStateWorkflow) expectMigrateDiffReplay(fixture, stage, dirName string) *Result {
	targetDB := filepath.Join(d.runRoot, dirName+"-replay.db")
	result, harness := d.runCLI(stage,
		"migrate", "apply",
		"--url", sqliteURL(targetDB),
		"--dir", "file://"+dirName,
	)
	if harness != nil {
		return harness
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return gap
	}
	if gap := d.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"atlas_schema_revisions", "users"}); gap != nil {
		return gap
	}
	return d.expectSQLiteColumnFactsAt(fixture, stage, targetDB, "users", []sqliteColumnFact{
		{Name: "id", Type: "integer", PrimaryKey: 1},
	})
}

func (d *desiredStateWorkflow) migrateDiffArtifactSnapshot(stage, dirName string) ([]string, *Result) {
	files, err := relativeFilesUnder(filepath.Join(d.runRoot, dirName))
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return nil, &failure
	}
	snapshot := make([]string, 0, len(files))
	for _, file := range files {
		content, err := readRunFile(d.runRoot, filepath.Join(dirName, file))
		if err != nil {
			failure := d.harnessFailure(stage, err)
			return nil, &failure
		}
		snapshot = append(snapshot, file+"\x00"+content)
	}
	return snapshot, nil
}
