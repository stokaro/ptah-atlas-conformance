package probe

import (
	"os"
	"path/filepath"
)

const proTestWorkflowSentinel = "_capability/pro-test-workflow/SENTINEL"

// ProTestWorkflowProbe executes the Atlas Pro test verbs Ptah implements as
// open capabilities — `atlas migrate test` and `atlas schema test`
// (stokaro/ptah#805) — end to end through the real `ptah atlas ...` CLI
// against real SQLite dev databases. Atlas keeps both verbs in its
// proprietary Pro/Cloud build, so this is a first-party capability probe
// rather than an Atlas-corpus round-trip fixture: a passing committed case
// set must exit 0, and a deliberately failing assertion must exit 1 with a
// structured failure report.
type ProTestWorkflowProbe struct {
	// FixtureRoot contains the committed Atlas-format migrations, the Go
	// schema-annotation models, and the passing/failing test-case sets.
	// Relative paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ProTestWorkflowProbe) Name() string { return "pro-test-workflow" }

func (p ProTestWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != proTestWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("pro-test-workflow", proTestWorkflowSentinel, p.FixtureRoot, p.Binary)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	t := &proTestWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		t.migrationTestsPass,
		t.migrationTestFailure,
		t.schemaTestsPass,
		t.schemaTestFailure,
	})
}

type proTestWorkflow struct {
	*proWorkflowRuntime
}

func (t *proTestWorkflow) migrationTestsPass() Result {
	const (
		fixture = "ptah atlas migrate test"
		stage   = "migration tests pass"
	)
	if harness := t.hashAtlasMigrations(stage); harness != nil {
		return *harness
	}
	devDB := filepath.Join(t.runRoot, "migrate-dev.db")
	result, harness := t.runCLI(stage,
		"atlas", "migrate", "test",
		"--dir", "file://migrations",
		"--dev-url", sqliteURL(devDB),
		"tests-pass",
	)
	if harness != nil {
		return *harness
	}
	if gap := t.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := t.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`PASS  case "migration workflow"`,
		"migrated to latest",
		"1 cases, 1 passed, 0 failed",
	}); gap != nil {
		return *gap
	}
	// The dev URL must point at a real SQLite database the runner actually
	// used, not an ignored flag.
	if _, err := os.Stat(devDB); err != nil {
		return t.gap(fixture, stage, "the --dev-url SQLite database was never created: "+oneLine(err.Error()))
	}
	return t.ok(fixture, stage,
		"the Atlas Pro test verb applied the Atlas-format migration directory to a real SQLite dev database and passed the committed case set: migrate_to latest, exec, and row-count/scalar assertions")
}

func (t *proTestWorkflow) migrationTestFailure() Result {
	const (
		fixture = "ptah atlas migrate test"
		stage   = "migration test failure exit contract"
	)
	result, harness := t.runCLI(stage,
		"atlas", "migrate", "test",
		"--dir", "file://migrations",
		"--dev-url", sqliteURL(filepath.Join(t.runRoot, "migrate-dev-fail.db")),
		"tests-fail",
	)
	if harness != nil {
		return *harness
	}
	if gap := t.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := t.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`FAIL  case "failing assertion"`,
		"expected row_count 1, got 0",
		"1 cases, 0 passed, 1 failed",
	}); gap != nil {
		return *gap
	}
	return t.ok(fixture, stage,
		"a deliberately failing assertion produced a structured FAIL report naming the step divergence and process exit code 1")
}

func (t *proTestWorkflow) schemaTestsPass() Result {
	const (
		fixture = "ptah atlas schema test"
		stage   = "schema tests pass"
	)
	devDB := filepath.Join(t.runRoot, "schema-dev.db")
	result, harness := t.runCLI(stage,
		"atlas", "schema", "test",
		"--url", "file://models",
		"--dev-url", sqliteURL(devDB),
		"schema-pass",
	)
	if harness != nil {
		return *harness
	}
	if gap := t.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := t.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`PASS  case "schema workflow"`,
		"1 cases, 1 passed, 0 failed",
	}); gap != nil {
		return *gap
	}
	if _, err := os.Stat(devDB); err != nil {
		return t.gap(fixture, stage, "the --dev-url SQLite database was never created: "+oneLine(err.Error()))
	}
	return t.ok(fixture, stage,
		"the Atlas Pro schema-test verb provisioned the desired schema from the local Go-annotation source on a real SQLite dev database and passed the committed case set")
}

func (t *proTestWorkflow) schemaTestFailure() Result {
	const (
		fixture = "ptah atlas schema test"
		stage   = "schema test failure exit contract"
	)
	result, harness := t.runCLI(stage,
		"atlas", "schema", "test",
		"--url", "file://models",
		"--dev-url", sqliteURL(filepath.Join(t.runRoot, "schema-dev-fail.db")),
		"schema-fail",
	)
	if harness != nil {
		return *harness
	}
	if gap := t.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := t.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`FAIL  case "failing schema assertion"`,
		"expected row_count 5, got 0",
		"1 cases, 0 passed, 1 failed",
	}); gap != nil {
		return *gap
	}
	return t.ok(fixture, stage,
		"a deliberately failing schema assertion produced a structured FAIL report and process exit code 1")
}
