package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const proPlanWorkflowSentinel = "_capability/pro-plan-workflow/SENTINEL"

// ProPlanWorkflowProbe executes the local half of Atlas's Pro `schema plan`
// workflow that Ptah implements as an open capability (stokaro/ptah#809)
// through the real `ptah atlas ...` CLI: `schema plan --save` computes a
// fingerprinted local plan file against a real SQLite target, `schema apply
// --plan file://...` replays exactly that reviewed plan, and a target mutated
// after planning is refused as stale without touching the database. Atlas
// binds plan storage and approval to its Cloud registry; Ptah's open
// replacement is the local plan file, so this is a first-party capability
// probe with no CE oracle.
type ProPlanWorkflowProbe struct {
	// FixtureRoot contains the committed desired-schema source file.
	// Relative paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ProPlanWorkflowProbe) Name() string { return "pro-plan-workflow" }

func (p ProPlanWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != proPlanWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("pro-plan-workflow", proPlanWorkflowSentinel, p.FixtureRoot, p.Binary, proVerbsIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	pl := &proPlanWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		pl.planCreation,
		pl.planApplication,
		pl.stalePlanRefusal,
	})
}

type proPlanWorkflow struct {
	*proWorkflowRuntime
}

// proPlanDocument is the subset of the saved plan-file document the probe
// asserts on.
type proPlanDocument struct {
	FormatVersion   int    `json:"format_version"`
	Name            string `json:"name"`
	Dialect         string `json:"dialect"`
	FromFingerprint string `json:"from_fingerprint"`
	ToFingerprint   string `json:"to_fingerprint"`
	Destructive     bool   `json:"destructive"`
	Statements      []struct {
		SQL      string `json:"sql"`
		Severity string `json:"severity"`
	} `json:"statements"`
}

func (p *proPlanWorkflow) planCreation() Result {
	const (
		fixture = "ptah atlas schema plan"
		stage   = "plan creation"
	)
	result, harness := p.runCLI(stage,
		"atlas", "schema", "plan",
		"--from", sqliteURL(filepath.Join(p.runRoot, "target.db")),
		"--to", "file://schema.sql",
		"--save",
		"--name", "conformance",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := p.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Plan saved to file://conformance.plan.json",
	}); gap != nil {
		return *gap
	}
	plan, gap := p.readPlan(fixture, stage, "conformance.plan.json")
	if gap != nil {
		return *gap
	}
	switch {
	case plan.FormatVersion != 1:
		return p.gap(fixture, stage, fmt.Sprintf("plan format_version = %d, want 1", plan.FormatVersion))
	case plan.Name != "conformance" || plan.Dialect != "sqlite":
		return p.gap(fixture, stage, fmt.Sprintf("plan identity name=%q dialect=%q, want name=\"conformance\" dialect=\"sqlite\"", plan.Name, plan.Dialect))
	case !strings.HasPrefix(plan.FromFingerprint, "sha256:") || !strings.HasPrefix(plan.ToFingerprint, "sha256:"):
		return p.gap(fixture, stage, "plan fingerprints are not sha256-prefixed digests")
	case plan.FromFingerprint == plan.ToFingerprint:
		return p.gap(fixture, stage, "plan from/to fingerprints are identical although the plan creates a table")
	case plan.Destructive:
		return p.gap(fixture, stage, "a pure CREATE TABLE plan was classified destructive")
	case len(plan.Statements) != 1:
		return p.gap(fixture, stage, fmt.Sprintf("plan has %d statement(s), want 1", len(plan.Statements)))
	case !strings.Contains(plan.Statements[0].SQL, `CREATE TABLE "users"`):
		return p.gap(fixture, stage, "plan statement does not create the desired users table: "+oneLine(plan.Statements[0].SQL))
	case plan.Statements[0].Severity != "safe":
		return p.gap(fixture, stage, fmt.Sprintf("plan statement severity = %q, want \"safe\"", plan.Statements[0].Severity))
	}
	return p.ok(fixture, stage,
		"`schema plan --save` wrote the local format_version-1 plan file binding sha256 source/target fingerprints to the reviewed CREATE TABLE statement with a per-statement severity")
}

func (p *proPlanWorkflow) planApplication() Result {
	const (
		fixture = "ptah atlas schema apply"
		stage   = "plan application"
	)
	result, harness := p.runCLI(stage,
		"atlas", "schema", "apply",
		"--url", sqliteURL(filepath.Join(p.runRoot, "target.db")),
		"--plan", "file://conformance.plan.json",
		"--auto-approve",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := p.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schema apply completed successfully.",
	}); gap != nil {
		return *gap
	}
	if gap := p.expectTables(fixture, stage, "target.db", []string{"users"}); gap != nil {
		return *gap
	}
	return p.ok(fixture, stage,
		"`schema apply --plan file://...` replayed the saved plan against the planned target, creating exactly the desired users table")
}

func (p *proPlanWorkflow) stalePlanRefusal() Result {
	const (
		fixture = "ptah atlas schema apply"
		stage   = "stale plan refusal"
	)
	staleDB := filepath.Join(p.runRoot, "stale-target.db")
	planned, harness := p.runCLI(stage,
		"atlas", "schema", "plan",
		"--from", sqliteURL(staleDB),
		"--to", "file://schema.sql",
		"--save",
		"--name", "stale",
	)
	if harness != nil {
		return *harness
	}
	if planned.exitCode != 0 {
		return p.gap(fixture, stage, fmt.Sprintf(
			"planning against the second target failed with exit code %d: %s", planned.exitCode, planned.diagnostic()))
	}
	// Mutate the target after planning, exactly the drift the fingerprint
	// binding exists to catch.
	if err := p.execSQL(staleDB, "CREATE TABLE drift (id INTEGER PRIMARY KEY)"); err != nil {
		return p.harnessFailure(stage, err)
	}
	result, harness := p.runCLI(stage,
		"atlas", "schema", "apply",
		"--url", sqliteURL(staleDB),
		"--plan", "file://stale.plan.json",
		"--auto-approve",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := p.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"pre-planned migration is stale",
		"fingerprint",
	}); gap != nil {
		return *gap
	}
	if gap := p.expectTables(fixture, stage, "stale-target.db", []string{"drift"}); gap != nil {
		return *gap
	}
	return p.ok(fixture, stage,
		"a target mutated after planning was refused: apply --plan exited 1 naming the fingerprint mismatch and left the database untouched")
}

func (p *proPlanWorkflow) readPlan(fixture, stage, name string) (proPlanDocument, *Result) {
	var plan proPlanDocument
	data, err := os.ReadFile(filepath.Join(p.runRoot, name))
	if err != nil {
		gap := p.gap(fixture, stage, "the saved plan file is missing: "+oneLine(err.Error()))
		return plan, &gap
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		gap := p.gap(fixture, stage, "the saved plan file is not parseable JSON: "+oneLine(err.Error()))
		return plan, &gap
	}
	return plan, nil
}

// expectTables reads the SQLite database directly, independently of the CLI,
// and returns a gap when the user tables differ from want.
func (p *proPlanWorkflow) expectTables(fixture, stage, dbName string, want []string) *Result {
	db, err := openSQLiteRuntimeDB(filepath.Join(p.runRoot, dbName))
	if err != nil {
		failure := p.harnessFailure(stage, err)
		return &failure
	}
	defer func() { _ = db.Close() }()
	got, err := sqliteTableNames(db)
	if err != nil {
		failure := p.harnessFailure(stage, err)
		return &failure
	}
	if !slices.Equal(got, want) {
		gap := p.gap(fixture, stage, fmt.Sprintf("%s tables = %v, want %v", dbName, got, want))
		return &gap
	}
	return nil
}

func (p *proPlanWorkflow) execSQL(dbPath, statement string) error {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(), statement)
	return err
}
