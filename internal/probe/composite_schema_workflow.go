package probe

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	compositeSchemaSentinel = "_capability/composite-schema/SENTINEL"
	compositeSchemaIssue    = "stokaro/ptah#666"
)

// CompositeSchemaWorkflowProbe verifies that independently owned desired-schema
// sources compose consistently across Ptah's render, migration, and live
// comparison command paths.
type CompositeSchemaWorkflowProbe struct {
	// FixtureRoot contains the committed Go, YAML, and conflict fixtures.
	// Relative paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (CompositeSchemaWorkflowProbe) Name() string { return "composite-schema-workflow" }

func (p CompositeSchemaWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != compositeSchemaSentinel {
		return nil
	}

	root, err := p.fixturePath()
	if err != nil {
		return []Result{compositeSchemaHarnessFailure("fixture setup", err)}
	}
	bin, err := p.binary()
	if err != nil {
		return []Result{compositeSchemaHarnessFailure("binary build", err)}
	}
	runRoot, err := os.MkdirTemp("", "ptah-composite-schema-*")
	if err != nil {
		return []Result{compositeSchemaHarnessFailure("runtime setup", err)}
	}
	defer os.RemoveAll(runRoot)

	models := filepath.Join(root, "models")
	orders := filepath.Join(root, "orders.yaml")
	handMerged := filepath.Join(root, "hand-merged.yaml")
	conflict := filepath.Join(root, "conflict.yaml")
	drift := filepath.Join(root, "drift.yaml")
	expectedSQL, err := os.ReadFile(filepath.Join(root, "expected.sqlite.sql"))
	if err != nil {
		return []Result{compositeSchemaHarnessFailure("fixture setup", err)}
	}
	replacements := []compositePathReplacement{
		{path: root, marker: "<fixture>"},
		{path: runRoot, marker: "<run>"},
	}
	runCommand := func(args []string) compositeCommandResult {
		return runPtahCommandResult(bin, args, runRoot, replacements)
	}

	compositeRender := runCommand([]string{
		"schema", "render",
		"--root-dir", models,
		"--schema-file", orders,
		"--dialect", "sqlite",
	})
	handMergedRender := runCommand([]string{
		"schema", "render",
		"--schema-file", handMerged,
		"--dialect", "sqlite",
	})
	conflictRender := runCommand([]string{
		"schema", "render",
		"--root-dir", models,
		"--schema-file", conflict,
		"--dialect", "sqlite",
	})

	migrationsDir := filepath.Join(runRoot, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o700); err != nil {
		return []Result{compositeSchemaHarnessFailure("runtime setup", err)}
	}
	handMergedMigrationsDir := filepath.Join(runRoot, "hand-merged-migrations")
	if err := os.MkdirAll(handMergedMigrationsDir, 0o700); err != nil {
		return []Result{compositeSchemaHarnessFailure("runtime setup", err)}
	}
	databasePath := filepath.Join(runRoot, "composite.db")
	databaseURL := sqliteURL(databasePath)
	generate := runCommand([]string{
		"migrations", "generate",
		"--root-dir", models,
		"--schema-file", orders,
		"--db-url", databaseURL,
		"--migrations-dir", migrationsDir,
		"--name", "composite_schema",
	})
	handMergedGenerate := runCommand([]string{
		"migrations", "generate",
		"--schema-file", handMerged,
		"--db-url", sqliteURL(filepath.Join(runRoot, "hand-merged.db")),
		"--migrations-dir", handMergedMigrationsDir,
		"--name", "composite_schema",
	})
	apply := runCommand([]string{
		"migrations", "up",
		"--db-url", databaseURL,
		"--migrations-dir", migrationsDir,
		"--dir-format", "ptah",
	})
	compare := runCommand([]string{
		"schema", "compare",
		"--root-dir", models,
		"--schema-file", orders,
		"--db-url", databaseURL,
		"--exit-code",
	})
	handMergedCompare := runCommand([]string{
		"schema", "compare",
		"--schema-file", handMerged,
		"--db-url", databaseURL,
		"--exit-code",
	})
	driftCompare := runCommand([]string{
		"schema", "compare",
		"--schema-file", drift,
		"--db-url", databaseURL,
		"--exit-code",
	})

	return []Result{
		compositeRender.result(
			"mixed render",
			"render",
			"Go and YAML sources rendered users and orders exactly once",
			compositeRenderExpectation{wantSQL: strings.TrimSpace(string(expectedSQL))},
		),
		compositeRenderEquivalenceResult(compositeRender, handMergedRender),
		conflictRender.result(
			"conflicting sources",
			"conflict detection",
			"conflicting database identities failed before rendering",
			compositeConflictExpectation{},
		),
		generate.result(
			"ptah migrations generate",
			"migration generation",
			"the mixed desired schema generated a Ptah migration pair against an empty SQLite database",
			compositeGenerateExpectation{migrationsDir: migrationsDir},
		),
		compositeMigrationEquivalenceResult(
			generate,
			handMergedGenerate,
			migrationsDir,
			handMergedMigrationsDir,
		),
		apply.result(
			"ptah migrations up",
			"migration application",
			"the generated composite-schema migration applied to SQLite",
			compositeSuccessExpectation{},
		),
		compositeSQLiteStateResult(apply, databasePath),
		compositeLiveCompareResult(compare, handMergedCompare, driftCompare),
	}
}

func (p CompositeSchemaWorkflowProbe) fixturePath() (string, error) {
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

func (p CompositeSchemaWorkflowProbe) binary() (string, error) {
	if strings.TrimSpace(p.Binary) != "" {
		return p.Binary, nil
	}
	return ptahBinary()
}

type compositeCommandResult struct {
	command ptahCommandResult
	err     error
	args    []string
}

type compositePathReplacement struct {
	path   string
	marker string
}

func runPtahCommandResult(
	bin string,
	args []string,
	workingDir string,
	replacements []compositePathReplacement,
) compositeCommandResult {
	result, err := runPtahCommandInDir(bin, args, workingDir)
	result.stdout = normalizeCompositePaths(result.stdout, replacements)
	result.stderr = normalizeCompositePaths(result.stderr, replacements)
	normalizedArgs := make([]string, len(args))
	for index, arg := range args {
		normalizedArgs[index] = normalizeCompositePaths(arg, replacements)
	}
	return compositeCommandResult{command: result, err: err, args: normalizedArgs}
}

func normalizeCompositePaths(value string, replacements []compositePathReplacement) string {
	for _, replacement := range replacements {
		paths := []string{
			replacement.path,
			filepath.Clean(replacement.path),
			filepath.ToSlash(replacement.path),
		}
		resolved, err := filepath.EvalSymlinks(replacement.path)
		if err == nil {
			paths = append(paths, resolved, filepath.ToSlash(resolved))
		}
		for _, path := range paths {
			value = strings.ReplaceAll(value, path, replacement.marker)
		}
	}
	return value
}

type compositeExpectation interface {
	validate(compositeCommandResult) error
}

func (r compositeCommandResult) result(
	fixture string,
	stage string,
	detail string,
	expectation compositeExpectation,
) Result {
	if r.err != nil {
		return compositeSchemaHarnessFailure(stage, fmt.Errorf(
			"execute `ptah %s`: %w; %s",
			strings.Join(r.args, " "),
			r.err,
			r.command.diagnostic(),
		))
	}
	if err := expectation.validate(r); err != nil {
		return compositeSchemaGap(fixture, stage, err.Error()+": "+r.command.diagnostic())
	}
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: OK,
		Detail:  detail,
	}
}

type compositeSuccessExpectation struct{}

func (compositeSuccessExpectation) validate(result compositeCommandResult) error {
	if result.command.exitCode != 0 {
		return fmt.Errorf("expected exit code 0, got %d", result.command.exitCode)
	}
	return nil
}

type compositeRenderExpectation struct {
	wantSQL string
}

func (e compositeRenderExpectation) validate(result compositeCommandResult) error {
	if result.command.exitCode != 0 {
		return fmt.Errorf("expected exit code 0, got %d", result.command.exitCode)
	}
	rendered, err := renderedSQLSection(result.command.stdout)
	if err != nil {
		return err
	}
	if rendered != e.wantSQL {
		return fmt.Errorf("rendered SQL differs from expected SQLite snapshot")
	}
	return nil
}

type compositeConflictExpectation struct{}

func (compositeConflictExpectation) validate(result compositeCommandResult) error {
	if result.command.exitCode != 2 {
		return fmt.Errorf("expected exit code 2, got %d", result.command.exitCode)
	}
	const wanted = `error merging composite schema: conflicting field "id" definitions on table "users"`
	if !strings.Contains(result.command.stderr, wanted) {
		return fmt.Errorf("stderr does not contain %q", wanted)
	}
	if strings.Contains(result.command.stderr, "panic:") {
		return fmt.Errorf("stderr contains a panic")
	}
	if strings.Contains(result.command.stdout, "CREATE TABLE") {
		return fmt.Errorf("conflicting sources emitted partial schema SQL")
	}
	return nil
}

type compositeGenerateExpectation struct {
	migrationsDir string
}

func (e compositeGenerateExpectation) validate(result compositeCommandResult) error {
	if result.command.exitCode != 0 {
		return fmt.Errorf("expected exit code 0, got %d", result.command.exitCode)
	}
	files, err := filepath.Glob(filepath.Join(e.migrationsDir, "*.sql"))
	if err != nil {
		return fmt.Errorf("list generated migration files: %w", err)
	}
	if len(files) != 2 {
		return fmt.Errorf("expected one generated migration pair, got %d SQL files", len(files))
	}
	return nil
}

func compositeRenderEquivalenceResult(
	composite compositeCommandResult,
	handMerged compositeCommandResult,
) Result {
	if composite.err != nil {
		return compositeSchemaHarnessFailure("render equivalence", composite.err)
	}
	if handMerged.err != nil {
		return compositeSchemaHarnessFailure("render equivalence", handMerged.err)
	}
	if composite.command.exitCode != 0 || handMerged.command.exitCode != 0 {
		return compositeSchemaGap(
			"hand-merged equivalence",
			"render equivalence",
			fmt.Sprintf(
				"render exit mismatch: composite=%d, hand-merged=%d",
				composite.command.exitCode,
				handMerged.command.exitCode,
			),
		)
	}
	compositeSQL, err := renderedSQLSection(composite.command.stdout)
	if err != nil {
		return compositeSchemaGap("hand-merged equivalence", "render equivalence", err.Error())
	}
	handMergedSQL, err := renderedSQLSection(handMerged.command.stdout)
	if err != nil {
		return compositeSchemaGap("hand-merged equivalence", "render equivalence", err.Error())
	}
	if compositeSQL != handMergedSQL {
		return compositeSchemaGap(
			"hand-merged equivalence",
			"render equivalence",
			"mixed-source SQL differs from the hand-merged desired schema",
		)
	}
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: "hand-merged equivalence",
		Stage:   "render equivalence",
		Outcome: OK,
		Detail:  "mixed and hand-merged desired schemas rendered identical SQLite SQL",
	}
}

func compositeMigrationEquivalenceResult(
	composite compositeCommandResult,
	handMerged compositeCommandResult,
	compositeDir string,
	handMergedDir string,
) Result {
	if composite.err != nil {
		return compositeSchemaHarnessFailure("migration equivalence", composite.err)
	}
	if handMerged.err != nil {
		return compositeSchemaHarnessFailure("migration equivalence", handMerged.err)
	}
	if composite.command.exitCode != 0 || handMerged.command.exitCode != 0 {
		return compositeSchemaGap(
			"hand-merged migration equivalence",
			"migration equivalence",
			fmt.Sprintf(
				"generate exit mismatch: composite=%d, hand-merged=%d",
				composite.command.exitCode,
				handMerged.command.exitCode,
			),
		)
	}
	compositePair, err := readGeneratedMigrationPair(compositeDir)
	if err != nil {
		return compositeSchemaGap("hand-merged migration equivalence", "migration equivalence", err.Error())
	}
	handMergedPair, err := readGeneratedMigrationPair(handMergedDir)
	if err != nil {
		return compositeSchemaGap("hand-merged migration equivalence", "migration equivalence", err.Error())
	}
	if compositePair != handMergedPair {
		return compositeSchemaGap(
			"hand-merged migration equivalence",
			"migration equivalence",
			"mixed-source migration SQL differs from the hand-merged desired schema",
		)
	}
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: "hand-merged migration equivalence",
		Stage:   "migration equivalence",
		Outcome: OK,
		Detail:  "mixed and hand-merged desired schemas generated identical SQLite up/down SQL",
	}
}

type generatedMigrationPair struct {
	up   string
	down string
}

func readGeneratedMigrationPair(dir string) (generatedMigrationPair, error) {
	up, err := readOneGeneratedMigration(dir, "*.up.sql")
	if err != nil {
		return generatedMigrationPair{}, err
	}
	down, err := readOneGeneratedMigration(dir, "*.down.sql")
	if err != nil {
		return generatedMigrationPair{}, err
	}
	return generatedMigrationPair{
		up:   normalizeGeneratedMigration(up),
		down: normalizeGeneratedMigration(down),
	}, nil
}

func readOneGeneratedMigration(dir, pattern string) (string, error) {
	files, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", fmt.Errorf("list generated migrations matching %q: %w", pattern, err)
	}
	if len(files) != 1 {
		return "", fmt.Errorf("expected one generated migration matching %q, got %d", pattern, len(files))
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		return "", fmt.Errorf("read generated migration %q: %w", files[0], err)
	}
	return string(content), nil
}

func normalizeGeneratedMigration(content string) string {
	lines := strings.Split(content, "\n")
	normalized := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(line, "-- Generated on:") {
			continue
		}
		normalized = append(normalized, line)
	}
	return strings.Join(normalized, "\n")
}

func compositeSQLiteStateResult(
	apply compositeCommandResult,
	databasePath string,
) Result {
	if apply.err != nil {
		return compositeSchemaHarnessFailure("live schema facts", apply.err)
	}
	if apply.command.exitCode != 0 {
		return compositeSchemaGap(
			"SQLite schema facts",
			"live schema facts",
			"migration application failed: "+apply.command.diagnostic(),
		)
	}

	db, err := openSQLiteRuntimeDB(databasePath)
	if err != nil {
		return compositeSchemaHarnessFailure("live schema facts", err)
	}
	defer func() { _ = db.Close() }()

	tables, err := sqliteTableNames(db)
	if err != nil {
		return compositeSchemaHarnessFailure("live schema facts", err)
	}
	wantTables := []string{"orders", "schema_migrations", "users"}
	if !slices.Equal(tables, wantTables) {
		return compositeSchemaGap(
			"SQLite schema facts",
			"live schema facts",
			fmt.Sprintf("SQLite tables = %v, want %v", tables, wantTables),
		)
	}

	checks := []struct {
		objectType string
		name       string
		fragments  []string
	}{
		{
			objectType: "table",
			name:       "users",
			fragments: []string{
				`"id" INTEGER PRIMARY KEY`,
				`"email" TEXT NOT NULL UNIQUE`,
			},
		},
		{
			objectType: "table",
			name:       "orders",
			fragments: []string{
				`"user_id" INTEGER NOT NULL`,
				`CONSTRAINT "fk_orders_user" REFERENCES "users" ("id")`,
			},
		},
		{
			objectType: "index",
			name:       "idx_orders_reference",
			fragments:  []string{`ON "orders" ("reference")`},
		},
	}
	for _, check := range checks {
		definition, err := sqliteObjectDefinition(db, check.objectType, check.name)
		if err != nil {
			return compositeSchemaGap(
				"SQLite schema facts",
				"live schema facts",
				fmt.Sprintf("inspect %s %q: %v", check.objectType, check.name, err),
			)
		}
		for _, fragment := range check.fragments {
			if !strings.Contains(definition, fragment) {
				return compositeSchemaGap(
					"SQLite schema facts",
					"live schema facts",
					fmt.Sprintf("%s %q is missing %q", check.objectType, check.name, fragment),
				)
			}
		}
	}
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: "SQLite schema facts",
		Stage:   "live schema facts",
		Outcome: OK,
		Detail:  "SQLite contained both tables, every expected column attribute, the cross-source foreign key, and the index",
	}
}

func sqliteObjectDefinition(db *sql.DB, objectType, name string) (string, error) {
	var definition string
	err := db.QueryRowContext(
		context.Background(),
		"SELECT sql FROM sqlite_schema WHERE type = ? AND name = ?",
		objectType,
		name,
	).Scan(&definition)
	if err != nil {
		return "", err
	}
	return definition, nil
}

func compositeLiveCompareResult(
	composite compositeCommandResult,
	handMerged compositeCommandResult,
	drift compositeCommandResult,
) Result {
	results := []struct {
		name   string
		result compositeCommandResult
	}{
		{name: "mixed sources", result: composite},
		{name: "hand-merged source", result: handMerged},
	}
	for _, check := range results {
		if check.result.err != nil {
			return compositeSchemaHarnessFailure("live end state", check.result.err)
		}
		if err := validateCleanSchemaComparison(check.result); err != nil {
			return compositeSchemaGap(
				"live comparison controls",
				"live end state",
				check.name+": "+err.Error()+": "+check.result.command.diagnostic(),
			)
		}
	}
	if drift.err != nil {
		return compositeSchemaHarnessFailure("live end state", drift.err)
	}
	if drift.command.exitCode != 1 {
		return compositeSchemaGap(
			"live comparison controls",
			"live end state",
			fmt.Sprintf(
				"intentionally drifted desired schema exit code = %d, want 1: %s",
				drift.command.exitCode,
				drift.command.diagnostic(),
			),
		)
	}
	driftDiff, err := schemaComparisonSection(drift.command.stdout)
	if err != nil {
		return compositeSchemaGap("live comparison controls", "live end state", err.Error())
	}
	if driftDiff == "[]" {
		return compositeSchemaGap(
			"live comparison controls",
			"live end state",
			"intentionally drifted desired schema unexpectedly reported an empty diff",
		)
	}
	if strings.Contains(drift.command.stderr, "panic:") {
		return compositeSchemaGap(
			"live comparison controls",
			"live end state",
			"intentionally drifted desired schema comparison panicked",
		)
	}
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: "live comparison controls",
		Stage:   "live end state",
		Outcome: OK,
		Detail:  "mixed and hand-merged sources reported an empty diff; an intentionally drifted source reported changes with exit code 1",
	}
}

func validateCleanSchemaComparison(result compositeCommandResult) error {
	if result.command.exitCode != 0 {
		return fmt.Errorf("expected exit code 0, got %d", result.command.exitCode)
	}
	diff, err := schemaComparisonSection(result.command.stdout)
	if err != nil {
		return err
	}
	if diff != "[]" {
		return fmt.Errorf("expected empty [] diff, got %q", diff)
	}
	if strings.Contains(result.command.stderr, "panic:") {
		return fmt.Errorf("comparison panicked")
	}
	return nil
}

func schemaComparisonSection(output string) (string, error) {
	const marker = "=== SCHEMA COMPARISON ==="
	_, diff, ok := strings.Cut(output, marker)
	if !ok {
		return "", fmt.Errorf("comparison output is missing %q", marker)
	}
	return strings.TrimSpace(diff), nil
}

// renderedSQLSection returns the SQL from a `ptah schema render` run.
//
// It used to cut the output at an `=== SQLITE SCHEMA ===` banner. stokaro/ptah#1007
// deliberately removed that banner and moved every diagnostic to stderr --
// asserting `stdout` does NOT contain it, and that `Found N tables` and
// `Table Dependencies:` appear on stderr instead -- so stdout is now pure SQL
// and there is nothing to cut.
//
// This tier did not notice for a day: it builds ptah from the pinned module
// version rather than from a checkout, and the pin predated that change.
func renderedSQLSection(output string) (string, error) {
	sql := strings.TrimSpace(output)
	if sql == "" {
		return "", fmt.Errorf("render output is empty")
	}
	return sql, nil
}

func compositeSchemaGap(fixture, stage, detail string) Result {
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: Gap,
		Detail:  detail,
		Issue:   compositeSchemaIssue,
	}
}

func compositeSchemaHarnessFailure(stage string, err error) Result {
	return Result{
		Probe:   "composite-schema-workflow",
		Fixture: compositeSchemaSentinel,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}
