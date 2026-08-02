package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

const proPlanWorkflowSentinel = "_capability/pro-plan-workflow/SENTINEL"

// ProPlanWorkflowProbe executes the local half of Atlas's Pro `schema plan`
// workflow that Ptah implements as an open capability (stokaro/ptah#809 and
// stokaro/ptah#951) through the real `atlas ...` CLI: the parent and `new`
// create plan files against real SQLite targets, `validate` checks a plan
// without changing the target, `schema apply --plan file://...` replays the
// reviewed plan, and stale targets are refused without mutation.
//
// THIS PROBE HAS NO CE ORACLE AND PINS PTAH-SIDE BEHAVIOR. Atlas binds plan
// storage and approval to its Cloud registry, and Atlas CE v1.3.0 answers
// `'atlas schema plan' is not supported by the community version`, so nothing
// here can be differentialed against CE. Its rows are regression guards on
// Ptah's own contract, not parity evidence.
//
// stokaro/ptah#965 made Atlas's `.plan.hcl` the default encoding and kept the
// native fingerprinted JSON plan reachable through an explicit .json --output
// path. Both are measured: the HCL shape against the Atlas-authored artifact
// captured in that PR, the JSON document against Ptah's own format contract.
// The two formats are guarded differently on apply — the JSON plan by its
// fingerprint binding, the Atlas format by a dev-database replay verified
// against --to — so the apply and stale-refusal stages stay on the JSON plan.
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
		pl.planNewCreation,
		pl.planValidation,
		pl.stalePlanValidation,
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
		fixture = "atlas schema plan"
		stage   = "plan creation"
	)
	// Since stokaro/ptah#965 the DEFAULT encoding is Atlas's `.plan.hcl` shape.
	// The expected structure is taken from the Atlas-authored artifact captured
	// in that PR (ptah cmd/atlas/testdata/atlas.plan.hcl, written by Atlas):
	// a single `plan` block, labeled, carrying `from`,
	// `to` and a `migration` heredoc.
	result, harness := p.runCLI(stage,
		"schema", "plan",
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
		"Plan saved to file://conformance.plan.hcl",
	}); gap != nil {
		return *gap
	}
	if gap := p.checkAtlasPlanHCL(fixture, stage, "conformance.plan.hcl"); gap != nil {
		return *gap
	}

	// The native fingerprinted JSON plan stayed reachable through an explicit
	// .json --output path, so its document contract is still measured here
	// rather than being dropped along with the default.
	jsonResult, harness := p.runCLI(stage,
		"schema", "plan",
		"--from", sqliteURL(filepath.Join(p.runRoot, "target.db")),
		"--to", "file://schema.sql",
		"--output", "conformance.plan.json",
		"--name", "conformance",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, jsonResult, 0); gap != nil {
		return *gap
	}
	if gap := p.expectFragments(fixture, stage, "stdout", jsonResult.stdout, []string{
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
	// Names no Atlas release: the assertion is that CE gates the verb at all,
	// which is stable across the pin (measured identical on v1.2.0 and
	// v1.3.0). See the cli_exit_behavior sibling for the same reasoning.
	return p.ok(fixture, stage,
		"PTAH-SIDE PIN (no CE oracle — the pinned Atlas CE answers \"'atlas schema plan' is not supported by the community version\"): `schema plan --save` wrote the Atlas-shaped `.plan.hcl` by default (single labeled `plan` block with from/to and a migration heredoc, per the Atlas-authored artifact captured in stokaro/ptah#965), and an explicit .json --output still wrote the native format_version-1 plan binding sha256 fingerprints to the reviewed CREATE TABLE statement with a per-statement severity")
}

// checkAtlasPlanHCL asserts the saved plan file has the Atlas plan-file shape.
//
// The structure asserted here is what the Atlas-authored artifact settles: one
// `plan` block with a single label, plus `from`, `to` and `migration`
// attributes. The sha256: prefix on from/to is deliberately NOT asserted as
// Atlas parity — Ptah writes its own fingerprints there, and ptah's own help
// text records that the official Atlas binary parses the file but verifies its
// own hashes, which have no local recipe.
func (p *proPlanWorkflow) checkAtlasPlanHCL(fixture, stage, name string) *Result {
	data, err := os.ReadFile(filepath.Join(p.runRoot, name))
	if err != nil {
		gap := p.gap(fixture, stage, "the saved Atlas-format plan file is missing: "+oneLine(err.Error()))
		return &gap
	}
	file, diags := hclsyntax.ParseConfig(data, name, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		gap := p.gap(fixture, stage, "the saved plan file is not parseable HCL: "+oneLine(diags.Error()))
		return &gap
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		gap := p.gap(fixture, stage, "the saved plan file has no HCL body")
		return &gap
	}
	var planBlocks []*hclsyntax.Block
	for _, block := range body.Blocks {
		if block.Type == "plan" {
			planBlocks = append(planBlocks, block)
		}
	}
	if len(planBlocks) != 1 {
		gap := p.gap(fixture, stage, fmt.Sprintf("plan file has %d `plan` block(s), want exactly 1", len(planBlocks)))
		return &gap
	}
	block := planBlocks[0]
	if len(block.Labels) != 1 || block.Labels[0] != "conformance" {
		gap := p.gap(fixture, stage, fmt.Sprintf("plan block labels = %v, want exactly [conformance]", block.Labels))
		return &gap
	}
	attrs := map[string]string{}
	for _, want := range []string{"from", "to", "migration"} {
		attr, present := block.Body.Attributes[want]
		if !present {
			gap := p.gap(fixture, stage, "plan block has no `"+want+"` attribute")
			return &gap
		}
		value, valDiags := attr.Expr.Value(nil)
		if valDiags.HasErrors() || value.Type() != cty.String {
			gap := p.gap(fixture, stage, "plan block attribute `"+want+"` is not a literal string")
			return &gap
		}
		attrs[want] = value.AsString()
	}
	switch {
	case strings.TrimSpace(attrs["from"]) == "" || strings.TrimSpace(attrs["to"]) == "":
		gap := p.gap(fixture, stage, "plan block from/to fingerprints are empty")
		return &gap
	case attrs["from"] == attrs["to"]:
		gap := p.gap(fixture, stage, "plan block from/to fingerprints are identical although the plan creates a table")
		return &gap
	case !strings.Contains(attrs["migration"], `CREATE TABLE "users"`):
		gap := p.gap(fixture, stage, "plan block migration does not create the desired users table: "+oneLine(attrs["migration"]))
		return &gap
	}
	return nil
}

func (p *proPlanWorkflow) planNewCreation() Result {
	const (
		fixture = "atlas schema plan new"
		stage   = "plan file creation"
	)
	result, harness := p.runCLI(stage,
		"schema", "plan", "new",
		"--from", sqliteURL(filepath.Join(p.runRoot, "new-target.db")),
		"--to", "file://schema.sql",
		"--output", "new.plan.hcl",
		"--name", "conformance",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := p.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`CREATE TABLE "users"`,
		"Plan saved to file://new.plan.hcl",
	}); gap != nil {
		return *gap
	}
	if result.stderr != "" {
		return p.gap(fixture, stage, "successful plan creation wrote unexpected stderr: "+oneLine(result.stderr))
	}
	if gap := p.checkAtlasPlanHCL(fixture, stage, "new.plan.hcl"); gap != nil {
		return *gap
	}
	if gap := p.expectTables(fixture, stage, "new-target.db", nil); gap != nil {
		return *gap
	}
	return p.ok(fixture, stage,
		"PTAH-SIDE PIN (documented, no executable Atlas oracle): `schema plan new` wrote the Atlas-shaped plan without --save, kept stderr empty, and left the target database unchanged")
}

func (p *proPlanWorkflow) planValidation() Result {
	const (
		fixture = "atlas schema plan validate"
		stage   = "non-mutating validation"
	)
	result, harness := p.runCLI(stage,
		"schema", "plan", "validate",
		"--from", sqliteURL(filepath.Join(p.runRoot, "new-target.db")),
		"--to", "file://schema.sql",
		"--file", "file://new.plan.hcl",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if result.stdout != "" {
		return p.gap(fixture, stage, "successful validation wrote unexpected stdout: "+oneLine(result.stdout))
	}
	if result.stderr != "" {
		return p.gap(fixture, stage, "successful validation wrote unexpected stderr: "+oneLine(result.stderr))
	}
	if gap := p.expectTables(fixture, stage, "new-target.db", nil); gap != nil {
		return *gap
	}
	return p.ok(fixture, stage,
		"PTAH-SIDE PIN (documented, no executable Atlas oracle): `schema plan validate` exited 0 with empty stdout and stderr and left the target database unchanged")
}

func (p *proPlanWorkflow) stalePlanValidation() Result {
	const (
		fixture = "atlas schema plan validate"
		stage   = "stale target refusal"
	)
	dbPath := filepath.Join(p.runRoot, "new-target.db")
	if err := p.execSQL(dbPath, "CREATE TABLE drift (id INTEGER PRIMARY KEY)"); err != nil {
		return p.harnessFailure(stage, err)
	}
	result, harness := p.runCLI(stage,
		"schema", "plan", "validate",
		"--from", sqliteURL(dbPath),
		"--to", "file://schema.sql",
		"--file", "file://new.plan.hcl",
	)
	if harness != nil {
		return *harness
	}
	if gap := p.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if result.stdout != "" {
		return p.gap(fixture, stage, "stale validation wrote unexpected stdout: "+oneLine(result.stdout))
	}
	if gap := p.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		"pre-planned migration is stale",
		"source fingerprint",
	}); gap != nil {
		return *gap
	}
	if gap := p.expectTables(fixture, stage, "new-target.db", []string{"drift"}); gap != nil {
		return *gap
	}
	return p.ok(fixture, stage,
		"PTAH-SIDE PIN (documented, no executable Atlas oracle): validation rejected a target changed after planning, named the source fingerprint mismatch, and preserved the drift table without applying the plan")
}

func (p *proPlanWorkflow) planApplication() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "plan application"
	)
	result, harness := p.runCLI(stage,
		"schema", "apply",
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
		"PTAH-SIDE PIN (no CE oracle): `schema apply --plan file://...` replayed the saved native JSON plan against the planned target, creating exactly the desired users table")
}

func (p *proPlanWorkflow) stalePlanRefusal() Result {
	const (
		fixture = "atlas schema apply"
		stage   = "stale plan refusal"
	)
	staleDB := filepath.Join(p.runRoot, "stale-target.db")
	// Explicitly the native JSON encoding: fingerprint staleness is the JSON
	// plan's guard. stokaro/ptah#965 gave Atlas-format plans a different one —
	// a dev-database replay verified against --to — precisely because the
	// fingerprint is public and forgeable.
	planned, harness := p.runCLI(stage,
		"schema", "plan",
		"--from", sqliteURL(staleDB),
		"--to", "file://schema.sql",
		"--output", "stale.plan.json",
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
		"schema", "apply",
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
		"PTAH-SIDE PIN (no CE oracle): a target mutated after planning was refused: apply --plan on the native JSON plan exited 1 naming the fingerprint mismatch and left the database untouched")
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
