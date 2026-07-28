package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	managedDataWorkflowSentinel = "_capability/managed-data-workflow/SENTINEL"
	managedDataWorkflowIssue    = "stokaro/ptah#663"
)

// ManagedDataWorkflowProbe exercises Ptah's declarative reference/seed data
// capability (`//migrator:schema:data` annotations plus `ptah migrations data`)
// end to end through the real CLI. Atlas CE cannot declaratively manage or
// inspect reference data, so this is a first-party capability sentinel measured
// against the built Ptah binary rather than an Atlas-corpus round-trip fixture.
//
// The probe proves the round-trip ptah#663 asks for on an ephemeral SQLite
// database: a model declares managed rows, Ptah applies the schema, generates
// and applies a reversible data migration for those rows, the resulting rows are
// introspected, and a re-run of the data diff converges to an empty ("no data
// changes") state. It additionally proves the generation-time destructive gate
// (a divergent desired set that would delete rows is refused unless
// --allow-destructive) and reversibility (rolling the data migration back
// removes exactly the rows it inserted).
type ManagedDataWorkflowProbe struct {
	// FixtureRoot contains the committed Go models, YAML row-data, and the
	// divergent desired set. Relative paths are resolved from the probe process
	// directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ManagedDataWorkflowProbe) Name() string { return "managed-data-workflow" }

func (p ManagedDataWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != managedDataWorkflowSentinel {
		return nil
	}

	root, err := p.fixturePath()
	if err != nil {
		return []Result{managedDataHarnessFailure("fixture setup", err)}
	}
	bin, err := p.binary()
	if err != nil {
		return []Result{managedDataHarnessFailure("binary build", err)}
	}
	runRoot, err := os.MkdirTemp("", "ptah-managed-data-*")
	if err != nil {
		return []Result{managedDataHarnessFailure("runtime setup", err)}
	}
	defer func() { _ = os.RemoveAll(runRoot) }()

	return runManagedDataWorkflow(bin, root, runRoot)
}

func (p ManagedDataWorkflowProbe) fixturePath() (string, error) {
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

func (p ManagedDataWorkflowProbe) binary() (string, error) {
	if strings.TrimSpace(p.Binary) != "" {
		return p.Binary, nil
	}
	return ptahBinary()
}

// managedRow is one reference row keyed by its declared primary key. The probe
// compares live rows against the desired rows declared in the committed YAML
// row-data file.
type managedRow struct {
	code string
	name string
}

// managedDataDesiredRows mirrors testdata/workflows/managed-data/models/countries.yaml
// (sorted by key, matching the deterministic ordering the data migration emits).
var managedDataDesiredRows = []managedRow{
	{code: "CZ", name: "Czechia"},
	{code: "US", name: "United States"},
}

func runManagedDataWorkflow(bin, root, runRoot string) []Result {
	models := filepath.Join(root, "models")
	divergent := filepath.Join(root, "divergent")
	migrationsDir := filepath.Join(runRoot, "migrations")
	divergentDir := filepath.Join(runRoot, "divergent-migrations")
	databasePath := filepath.Join(runRoot, "managed.db")
	databaseURL := sqliteURL(databasePath)

	for _, dir := range []string{migrationsDir, divergentDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return []Result{managedDataHarnessFailure("runtime setup", err)}
		}
	}

	w := &managedDataWorkflow{
		bin:           bin,
		runRoot:       runRoot,
		models:        models,
		divergent:     divergent,
		migrationsDir: migrationsDir,
		divergentDir:  divergentDir,
		databasePath:  databasePath,
		databaseURL:   databaseURL,
	}
	return w.run()
}

type managedDataWorkflow struct {
	bin           string
	runRoot       string
	models        string
	divergent     string
	migrationsDir string
	divergentDir  string
	databasePath  string
	databaseURL   string
}

// run executes the workflow steps in order. Each step depends on the database
// state the previous step established, so a non-OK step short-circuits: the
// slice returned always ends with the first divergence, which keeps the gate
// red on the real problem instead of a cascade of misleading follow-on results.
func (w *managedDataWorkflow) run() []Result {
	steps := []func() Result{
		w.generateSchema,
		w.applySchema,
		w.generateData,
		w.applyData,
		w.introspectRows,
		w.convergenceReDiff,
		w.destructiveGate,
		w.reversibility,
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

func (w *managedDataWorkflow) generateSchema() Result {
	const (
		fixture = "ptah migrations generate"
		stage   = "schema generation"
	)
	result, harness := w.runCLI(stage,
		"migrations", "generate",
		"--root-dir", w.models,
		"--db-url", w.databaseURL,
		"--migrations-dir", w.migrationsDir,
		"--name", "create_countries",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	ups, err := filepath.Glob(filepath.Join(w.migrationsDir, "*.up.sql"))
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	if len(ups) != 1 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exactly one schema migration, got %d up file(s)", len(ups)))
	}
	body, err := os.ReadFile(ups[0])
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	if !strings.Contains(string(body), `CREATE TABLE "countries"`) {
		return managedDataGap(fixture, stage,
			"generated schema migration does not create the countries table")
	}
	return managedDataOK(fixture, stage,
		"the managed-data model generated a schema migration that creates the countries table")
}

func (w *managedDataWorkflow) applySchema() Result {
	const (
		fixture = "ptah migrations up"
		stage   = "schema application"
	)
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", w.databaseURL,
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	rows, err := w.countryRows()
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	if len(rows) != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected an empty countries table after schema apply, got %d row(s)", len(rows)))
	}
	return managedDataOK(fixture, stage,
		"the schema migration applied to SQLite and left an empty countries table")
}

func (w *managedDataWorkflow) generateData() Result {
	const (
		fixture = "ptah migrations data"
		stage   = "data migration generation"
	)
	result, harness := w.runCLI(stage,
		"migrations", "data",
		"--root-dir", w.models,
		"--db-url", w.databaseURL,
		"--migrations-dir", w.migrationsDir,
		"--description", "seed_countries",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	up, err := readOneGeneratedMigration(w.migrationsDir, "*seed_countries.up.sql")
	if err != nil {
		return managedDataGap(fixture, stage, err.Error())
	}
	down, err := readOneGeneratedMigration(w.migrationsDir, "*seed_countries.down.sql")
	if err != nil {
		return managedDataGap(fixture, stage, err.Error())
	}
	// The desired state is insert-only against an empty table, so the up body
	// inserts every declared row and the down body deletes exactly those keys —
	// the reversible inverse the ptah#663 workflow promises.
	for _, row := range managedDataDesiredRows {
		insert := fmt.Sprintf(`INSERT INTO "countries" ("code", "name") VALUES ('%s', '%s')`, row.code, row.name)
		if !strings.Contains(up, insert) {
			return managedDataGap(fixture, stage,
				"data up migration is missing insert for key "+row.code)
		}
		del := fmt.Sprintf(`DELETE FROM "countries" WHERE "code" = '%s'`, row.code)
		if !strings.Contains(down, del) {
			return managedDataGap(fixture, stage,
				"data down migration is missing the inverse delete for key "+row.code)
		}
	}
	if strings.Contains(up, "DELETE") || strings.Contains(up, "UPDATE") {
		return managedDataGap(fixture, stage,
			"insert-only data up migration unexpectedly contains DELETE/UPDATE")
	}
	// The inverse of an insert-only up is a delete-only down; any INSERT or
	// UPDATE there would mean the down is not a pure inverse.
	if strings.Contains(down, "INSERT INTO") || strings.Contains(down, "UPDATE") {
		return managedDataGap(fixture, stage,
			"data down migration is not a pure inverse of the inserts (contains INSERT/UPDATE)")
	}
	return managedDataOK(fixture, stage,
		"the declared rows generated a reversible data migration (up inserts every row, down deletes exactly those keys)")
}

func (w *managedDataWorkflow) applyData() Result {
	const (
		fixture = "ptah migrations up"
		stage   = "data application"
	)
	result, harness := w.runCLI(stage,
		"migrations", "up",
		"--db-url", w.databaseURL,
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	return managedDataOK(fixture, stage,
		"the generated data migration applied to SQLite")
}

func (w *managedDataWorkflow) introspectRows() Result {
	const (
		fixture = "managed rows"
		stage   = "row introspection"
	)
	rows, err := w.countryRows()
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	if !slices.Equal(rows, managedDataDesiredRows) {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"introspected rows %v do not match the declared desired state %v", rows, managedDataDesiredRows))
	}
	return managedDataOK(fixture, stage,
		"the introspected countries rows match the declared managed reference data exactly")
}

func (w *managedDataWorkflow) convergenceReDiff() Result {
	const (
		fixture = "ptah migrations data"
		stage   = "convergence re-diff"
	)
	// Re-diffing the same desired data against the now-seeded database is Ptah
	// introspecting the live rows through the CLI and finding them equal to the
	// declared state — the empty/converged diff the ptah#663 round-trip requires.
	result, harness := w.runCLI(stage,
		"migrations", "data",
		"--root-dir", w.models,
		"--db-url", w.databaseURL,
		"--migrations-dir", w.migrationsDir,
		"--dry-run",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	if strings.TrimSpace(result.stdout) != "no data changes" {
		return managedDataGap(fixture, stage,
			"expected a converged \"no data changes\" diff: "+result.diagnostic())
	}
	for _, verb := range []string{"INSERT", "UPDATE", "DELETE"} {
		if strings.Contains(result.stdout, verb) {
			return managedDataGap(fixture, stage,
				"converged re-diff unexpectedly emitted a "+verb+" statement")
		}
	}
	return managedDataOK(fixture, stage,
		"re-running the data diff against the seeded database converged to \"no data changes\"")
}

func (w *managedDataWorkflow) destructiveGate() Result {
	const (
		fixture = "ptah migrations data"
		stage   = "destructive gate"
	)
	// The divergent desired set drops a row, so reconciling it would delete an
	// existing row. Without --allow-destructive the command must refuse at
	// generation time and write nothing — a safety gate Atlas OSS does not offer
	// for reference data.
	result, harness := w.runCLI(stage,
		"migrations", "data",
		"--root-dir", w.divergent,
		"--db-url", w.databaseURL,
		"--migrations-dir", w.divergentDir,
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 2 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected the destructive gate to refuse with exit code 2, got %d: %s",
			result.exitCode, result.diagnostic()))
	}
	if !strings.Contains(result.stderr, "refusing to generate a destructive data migration") {
		return managedDataGap(fixture, stage,
			"destructive refusal is missing its diagnostic: "+result.diagnostic())
	}
	if strings.Contains(result.stdout, "DELETE") || strings.Contains(result.stdout, "INSERT") {
		return managedDataGap(fixture, stage,
			"refused destructive migration still emitted SQL: "+result.diagnostic())
	}
	written, err := filepath.Glob(filepath.Join(w.divergentDir, "*.sql"))
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	if len(written) != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"destructive gate refused but still wrote %d migration file(s)", len(written)))
	}
	return managedDataOK(fixture, stage,
		"a divergent desired set that would delete rows was refused with exit code 2 and wrote no files")
}

func (w *managedDataWorkflow) reversibility() Result {
	const (
		fixture = "ptah migrations down"
		stage   = "data reversibility"
	)
	schemaVersion, err := managedDataSchemaVersion(w.migrationsDir)
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	result, harness := w.runCLI(stage,
		"migrations", "down",
		"--db-url", w.databaseURL,
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
		"--target", schemaVersion,
		"--confirm",
	)
	if harness != nil {
		return *harness
	}
	if result.exitCode != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"expected exit code 0, got %d: %s", result.exitCode, result.diagnostic()))
	}
	rows, err := w.countryRows()
	if err != nil {
		return managedDataHarnessFailure(stage, err)
	}
	if len(rows) != 0 {
		return managedDataGap(fixture, stage, fmt.Sprintf(
			"rolling back the data migration left %d row(s); down did not remove exactly what up added", len(rows)))
	}
	return managedDataOK(fixture, stage,
		"rolling back the data migration removed exactly the seeded rows, leaving the schema intact")
}

// runCLI runs a Ptah CLI command in the run directory. It returns either a
// harness Fail (process could not run) via the pointer, or the completed
// command result for the caller to validate.
func (w *managedDataWorkflow) runCLI(stage string, args ...string) (ptahCommandResult, *Result) {
	result, err := runPtahCommandInDir(w.bin, args, w.runRoot)
	if err != nil {
		failure := managedDataHarnessFailure(stage, fmt.Errorf(
			"execute `ptah %s`: %w; %s", strings.Join(args, " "), err, result.diagnostic()))
		return result, &failure
	}
	return result, nil
}

// countryRows introspects the live countries table directly, independently of
// the CLI, so the probe verifies the exact declared values landed rather than
// trusting the command's own report.
func (w *managedDataWorkflow) countryRows() ([]managedRow, error) {
	db, err := openSQLiteRuntimeDB(w.databasePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	sqlRows, err := db.QueryContext(context.Background(),
		`SELECT "code", "name" FROM "countries" ORDER BY "code"`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlRows.Close() }()

	var rows []managedRow
	for sqlRows.Next() {
		var row managedRow
		if err := sqlRows.Scan(&row.code, &row.name); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, sqlRows.Err()
}

var managedDataVersionPrefix = regexp.MustCompile(`^(\d+)_`)

// managedDataSchemaVersion returns the lowest migration version in dir, which is
// the schema migration: `ptah migrations data` always assigns a version above
// the newest existing migration, so the data migration sorts strictly after the
// schema one. Rolling back to the schema version reverts only the data
// migration.
func managedDataSchemaVersion(dir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return "", err
	}
	var versions []int64
	for _, file := range files {
		match := managedDataVersionPrefix.FindStringSubmatch(filepath.Base(file))
		if match == nil {
			continue
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", fmt.Errorf("parse migration version from %q: %w", file, err)
		}
		versions = append(versions, version)
	}
	if len(versions) < 2 {
		return "", fmt.Errorf("expected a schema and a data migration, found %d versioned up file(s)", len(versions))
	}
	return strconv.FormatInt(slices.Min(versions), 10), nil
}

func managedDataOK(fixture, stage, detail string) Result {
	return Result{
		Probe:   "managed-data-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: OK,
		Detail:  detail,
	}
}

func managedDataGap(fixture, stage, detail string) Result {
	return Result{
		Probe:   "managed-data-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: Gap,
		Detail:  detail,
		Issue:   managedDataWorkflowIssue,
	}
}

func managedDataHarnessFailure(stage string, err error) Result {
	return Result{
		Probe:   "managed-data-workflow",
		Fixture: managedDataWorkflowSentinel,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}
