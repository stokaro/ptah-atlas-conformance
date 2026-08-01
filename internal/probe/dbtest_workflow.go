package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	dbTestWorkflowSentinel = "_capability/dbtest-workflow/SENTINEL"
	dbTestWorkflowIssue    = "stokaro/ptah#659"
)

// DBTestWorkflowProbe executes Ptah's native declarative database test commands
// through the real CLI. Its committed fixtures are separate from the Atlas
// schema corpus because they measure a Ptah capability beyond Atlas OSS.
type DBTestWorkflowProbe struct {
	// FixtureRoot contains the committed migration, schema, seed, and test-case
	// fixtures. Relative paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build. It is intended for focused
	// tests and local development; the zero value builds the go.mod-pinned CLI.
	Binary string
}

func (DBTestWorkflowProbe) Name() string { return "dbtest-workflow" }

func (p DBTestWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != dbTestWorkflowSentinel {
		return nil
	}

	root, err := p.fixturePath()
	if err != nil {
		return []Result{dbTestHarnessFailure("fixture setup", err)}
	}
	bin, err := p.binary()
	if err != nil {
		return []Result{dbTestHarnessFailure("binary build", err)}
	}

	checks := dbTestWorkflowChecks(root)
	results := make([]Result, 0, len(checks))
	for _, check := range checks {
		results = append(results, check.run(bin))
	}
	return results
}

func (p DBTestWorkflowProbe) fixturePath() (string, error) {
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

func (p DBTestWorkflowProbe) binary() (string, error) {
	if strings.TrimSpace(p.Binary) != "" {
		return p.Binary, nil
	}
	return ptahBinary()
}

type dbTestWorkflowCheck struct {
	fixture string
	stage   string
	detail  string
	args    []string
	exit    int
	stdout  dbTestOutputValidator
	stderr  dbTestOutputValidator
}

type dbTestOutputValidator interface {
	validate(string) error
}

func (c dbTestWorkflowCheck) run(bin string) Result {
	commandResult, err := runPtahCommand(bin, c.args)
	if err != nil {
		return Result{
			Probe:   "dbtest-workflow",
			Fixture: c.fixture,
			Stage:   c.stage,
			Outcome: Fail,
			Detail: "could not execute `ptah " + strings.Join(c.args, " ") + "`: " +
				oneLine(err.Error()) + "; " + commandResult.diagnostic(),
		}
	}
	if commandResult.exitCode != c.exit {
		return Result{
			Probe:   "dbtest-workflow",
			Fixture: c.fixture,
			Stage:   c.stage,
			Outcome: Gap,
			Detail: fmt.Sprintf(
				"expected exit code %d, got %d: %s",
				c.exit,
				commandResult.exitCode,
				commandResult.diagnostic(),
			),
			Issue: dbTestWorkflowIssue,
		}
	}
	if err := c.stdout.validate(commandResult.stdout); err != nil {
		return Result{
			Probe:   "dbtest-workflow",
			Fixture: c.fixture,
			Stage:   c.stage,
			Outcome: Gap,
			Detail:  "stdout " + err.Error() + ": " + commandResult.diagnostic(),
			Issue:   dbTestWorkflowIssue,
		}
	}
	if err := c.stderr.validate(commandResult.stderr); err != nil {
		return Result{
			Probe:   "dbtest-workflow",
			Fixture: c.fixture,
			Stage:   c.stage,
			Outcome: Gap,
			Detail:  "stderr " + err.Error() + ": " + commandResult.diagnostic(),
			Issue:   dbTestWorkflowIssue,
		}
	}
	return Result{
		Probe:   "dbtest-workflow",
		Fixture: c.fixture,
		Stage:   c.stage,
		Outcome: OK,
		Detail:  c.detail,
	}
}

type ptahCommandResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func (r ptahCommandResult) diagnostic() string {
	var parts []string
	if output := strings.TrimSpace(r.stdout); output != "" {
		parts = append(parts, "stdout: "+oneLine(output))
	}
	if output := strings.TrimSpace(r.stderr); output != "" {
		parts = append(parts, "stderr: "+oneLine(output))
	}
	if len(parts) == 0 {
		return "no output"
	}
	return strings.Join(parts, "; ")
}

func runPtahCommand(bin string, args []string) (ptahCommandResult, error) {
	return runPtahCommandInDir(bin, args, "")
}

func runPtahCommandInDir(bin string, args []string, workingDir string) (ptahCommandResult, error) {
	return runPtahCommandInDirWithEnv(bin, args, workingDir, nil)
}

// runPtahCommandInDirWithEnv runs a Ptah CLI command with extra environment
// entries appended to the filtered probe environment. Probes use it to inject
// hermetic tool settings such as a scripted $EDITOR without leaking the
// invoking user's environment into the measured command.
func runPtahCommandInDirWithEnv(bin string, args []string, workingDir string, extraEnv []string) (ptahCommandResult, error) {
	return runCommandHermetic(bin, args, workingDir, append(ptahCommandEnvironment(), extraEnv...))
}

// runCommandHermetic runs a binary with exactly the provided environment —
// nothing from the invoking process leaks in. The CE gating tier uses it to
// execute the Atlas binary logged out under a scratch HOME.
func runCommandHermetic(bin string, args []string, workingDir string, env []string) (ptahCommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workingDir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := ptahCommandResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: 0,
	}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		result.exitCode = -1
		return result, ctx.Err()
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		result.exitCode = exitErr.ExitCode()
		if result.exitCode < 0 {
			return result, fmt.Errorf("process terminated abnormally: %w", err)
		}
		return result, nil
	}
	result.exitCode = -1
	return result, err
}

func ptahCommandEnvironment() []string {
	environment := os.Environ()
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PTAH_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func dbTestHarnessFailure(stage string, err error) Result {
	return Result{
		Probe:   "dbtest-workflow",
		Fixture: dbTestWorkflowSentinel,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}

type fragmentExpectation struct {
	required  []string
	forbidden []string
}

func (e fragmentExpectation) validate(output string) error {
	for _, fragment := range e.required {
		if !strings.Contains(output, fragment) {
			return fmt.Errorf("output does not contain %q", fragment)
		}
	}
	for _, fragment := range e.forbidden {
		if strings.Contains(output, fragment) {
			return fmt.Errorf("output unexpectedly contains %q", fragment)
		}
	}
	return nil
}

type exactOutputExpectation string

func (e exactOutputExpectation) validate(output string) error {
	if output != string(e) {
		return fmt.Errorf("output mismatch: got %q, want %q", output, string(e))
	}
	return nil
}

type dbTestJSONReport struct {
	Kind   string           `json:"kind"`
	Total  int              `json:"total"`
	Passed int              `json:"passed"`
	Failed int              `json:"failed"`
	Cases  []dbTestJSONCase `json:"cases"`
}

type dbTestJSONCase struct {
	Name   string           `json:"name"`
	Steps  []dbTestJSONStep `json:"steps"`
	Passed bool             `json:"passed"`
}

type dbTestJSONStep struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type jsonReportExpectation struct {
	kind        string
	caseName    string
	total       int
	passed      int
	failed      int
	casePassed  bool
	stepNames   []string
	stepDetails []string
	stepPassed  []bool
}

func (e jsonReportExpectation) validate(output string) error {
	var report dbTestJSONReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		return fmt.Errorf("decode JSON report: %w", err)
	}
	if report.Kind != e.kind ||
		report.Total != e.total ||
		report.Passed != e.passed ||
		report.Failed != e.failed {
		return fmt.Errorf(
			"JSON summary mismatch: got kind=%q total=%d passed=%d failed=%d",
			report.Kind,
			report.Total,
			report.Passed,
			report.Failed,
		)
	}
	if len(report.Cases) != 1 {
		return fmt.Errorf("JSON case count mismatch: got %d", len(report.Cases))
	}
	testCase := report.Cases[0]
	if testCase.Name != e.caseName || testCase.Passed != e.casePassed {
		return fmt.Errorf(
			"JSON case mismatch: got name=%q passed=%t",
			testCase.Name,
			testCase.Passed,
		)
	}
	if len(testCase.Steps) != len(e.stepDetails) {
		return fmt.Errorf(
			"JSON step count mismatch: got %d, want %d",
			len(testCase.Steps),
			len(e.stepDetails),
		)
	}
	if len(e.stepNames) != len(e.stepDetails) || len(e.stepPassed) != len(e.stepDetails) {
		return fmt.Errorf(
			"invalid JSON expectation: %d names, %d details, and %d pass states",
			len(e.stepNames),
			len(e.stepDetails),
			len(e.stepPassed),
		)
	}
	for index, detail := range e.stepDetails {
		if testCase.Steps[index].Name != e.stepNames[index] {
			return fmt.Errorf(
				"JSON step %d name mismatch: got %q, want %q",
				index+1,
				testCase.Steps[index].Name,
				e.stepNames[index],
			)
		}
		if testCase.Steps[index].Detail != detail {
			return fmt.Errorf(
				"JSON step %d detail mismatch: got %q, want %q",
				index+1,
				testCase.Steps[index].Detail,
				detail,
			)
		}
		if testCase.Steps[index].Passed != e.stepPassed[index] {
			return fmt.Errorf(
				"JSON step %d pass state mismatch: got %t, want %t",
				index+1,
				testCase.Steps[index].Passed,
				e.stepPassed[index],
			)
		}
	}
	return nil
}

func dbTestWorkflowChecks(root string) []dbTestWorkflowCheck {
	migrations := filepath.Join(root, "migrations")
	seeds := filepath.Join(root, "seeds")
	models := filepath.Join(root, "models")
	migrationPass := filepath.Join(root, "migration-pass")
	schemaPass := filepath.Join(root, "schema-pass")

	migrationArgs := []string{
		"migrations", "test",
		"--dir", migrationPass,
		"--migrations-dir", migrations,
		"--root-dir", models,
		"--seed-dir", seeds,
		"--dir-format", "ptah",
		"--run", "^migration workflow$",
	}
	schemaArgs := []string{
		"schema", "test",
		"--dir", schemaPass,
		"--root-dir", models,
		"--seed-dir", seeds,
		"--run", "^schema workflow$",
	}

	return slices.Concat(
		dbTestMigrationReportChecks(migrationArgs),
		dbTestSchemaReportChecks(schemaArgs),
		dbTestIsolationChecks(root, models),
		dbTestFailureChecks(root, models),
	)
}

func dbTestMigrationReportChecks(migrationArgs []string) []dbTestWorkflowCheck {
	return []dbTestWorkflowCheck{
		{
			fixture: "ptah migrations test/text",
			stage:   "migration execution",
			detail:  "latest/numeric/zero migration targets, desired schema, seed, assertions, and case filtering passed",
			args:    append(append([]string{}, migrationArgs...), "--report", "text"),
			stdout: fragmentExpectation{
				required: []string{
					`PASS  case "migration workflow"`,
					"migrated to latest",
					"seeded 1 file(s)",
					"desired schema already applied",
					"row_count 1",
					`scalar "ada"`,
					`error contained "missing_table"`,
					"migrated to 0",
					`error contained "users"`,
					"migrated to 1",
					"row_count 0",
					"1 cases, 1 passed, 0 failed",
				},
				forbidden: []string{"excluded migration failure"},
			},
			stderr: exactOutputExpectation(""),
		},
		{
			fixture: "ptah migrations test/json",
			stage:   "JSON report",
			detail:  "structured migration JSON report preserved exact summary, case, and step results",
			args:    append(append([]string{}, migrationArgs...), "--report", "json"),
			stdout: jsonReportExpectation{
				kind:       "MIGRATION",
				caseName:   "migration workflow",
				total:      1,
				passed:     1,
				casePassed: true,
				stepNames: []string{
					"migrate up",
					"seed user",
					"apply desired schema",
					"one user exists",
					"user name matches",
					"expected query error",
					"migrate down",
					"table was removed",
					"migrate to numeric version",
					"restored table is empty",
				},
				stepDetails: []string{
					"migrated to latest",
					"seeded 1 file(s)",
					"desired schema already applied",
					"row_count 1",
					`scalar "ada"`,
					`error contained "missing_table"`,
					"migrated to 0",
					`error contained "users"`,
					"migrated to 1",
					"row_count 0",
				},
				stepPassed: []bool{true, true, true, true, true, true, true, true, true, true},
			},
			stderr: exactOutputExpectation(""),
		},
		{
			fixture: "ptah migrations test/html",
			stage:   "HTML report",
			detail:  "self-contained migration HTML report preserved the passing case and summary",
			args:    append(append([]string{}, migrationArgs...), "--report", "html"),
			stdout: fragmentExpectation{
				required: []string{
					"<!doctype html>",
					"<title>MIGRATION test report</title>",
					"1 cases, 1 passed, 0 failed",
					"migration workflow",
				},
				forbidden: []string{
					"excluded migration failure",
					"<script src",
					"<link rel",
					"http://",
					"https://",
				},
			},
			stderr: exactOutputExpectation(""),
		},
	}
}

func dbTestSchemaReportChecks(schemaArgs []string) []dbTestWorkflowCheck {
	return []dbTestWorkflowCheck{
		{
			fixture: "ptah schema test/text",
			stage:   "schema execution",
			detail:  "desired-schema provisioning, seed, drift repair, assertions, and case filtering passed",
			args:    append(append([]string{}, schemaArgs...), "--report", "text"),
			stdout: fragmentExpectation{
				required: []string{
					`PASS  case "schema workflow"`,
					"row_count 0",
					"seeded 1 file(s)",
					`scalar "ada"`,
					"desired schema applied",
					"1 cases, 1 passed, 0 failed",
				},
				forbidden: []string{"excluded schema failure"},
			},
			stderr: exactOutputExpectation(""),
		},
		{
			fixture: "ptah schema test/json",
			stage:   "JSON report",
			detail:  "structured schema JSON report preserved exact summary, case, and step results",
			args:    append(append([]string{}, schemaArgs...), "--report", "json"),
			stdout: jsonReportExpectation{
				kind:       "SCHEMA",
				caseName:   "schema workflow",
				total:      1,
				passed:     1,
				casePassed: true,
				stepNames: []string{
					"schema starts empty",
					"seed user",
					"user name matches",
					"introduce drift",
					"repair drift",
					"repaired table is empty",
				},
				stepDetails: []string{
					"row_count 0",
					"seeded 1 file(s)",
					`scalar "ada"`,
					"exec ok",
					"desired schema applied",
					"row_count 0",
				},
				stepPassed: []bool{true, true, true, true, true, true},
			},
			stderr: exactOutputExpectation(""),
		},
		{
			fixture: "ptah schema test/html",
			stage:   "HTML report",
			detail:  "self-contained schema HTML report preserved the passing case and summary",
			args:    append(append([]string{}, schemaArgs...), "--report", "html"),
			stdout: fragmentExpectation{
				required: []string{
					"<!doctype html>",
					"<title>SCHEMA test report</title>",
					"1 cases, 1 passed, 0 failed",
					"schema workflow",
				},
				forbidden: []string{
					"excluded schema failure",
					"<script src",
					"<link rel",
					"http://",
					"https://",
				},
			},
			stderr: exactOutputExpectation(""),
		},
	}
}

func dbTestIsolationChecks(root, models string) []dbTestWorkflowCheck {
	return []dbTestWorkflowCheck{
		{
			fixture: "ptah migrations test/isolation",
			stage:   "ephemeral isolation",
			detail:  "two cases created the same table independently in separate ephemeral SQLite databases",
			args: []string{
				"migrations", "test",
				"--dir", filepath.Join(root, "isolation"),
				"--report", "text",
			},
			stdout: fragmentExpectation{
				required: []string{
					`PASS  case "first isolated database"`,
					`PASS  case "second isolated database"`,
					"2 cases, 2 passed, 0 failed",
				},
			},
			stderr: exactOutputExpectation(""),
		},
		{
			fixture: "ptah schema test/isolation",
			stage:   "ephemeral isolation",
			detail:  "two schema cases inserted the same primary key independently in separate ephemeral SQLite databases",
			args: []string{
				"schema", "test",
				"--dir", filepath.Join(root, "schema-isolation"),
				"--root-dir", models,
				"--report", "text",
			},
			stdout: fragmentExpectation{
				required: []string{
					`PASS  case "first isolated schema database"`,
					`PASS  case "second isolated schema database"`,
					"2 cases, 2 passed, 0 failed",
				},
			},
			stderr: exactOutputExpectation(""),
		},
	}
}

func dbTestFailureChecks(root, models string) []dbTestWorkflowCheck {
	return []dbTestWorkflowCheck{
		{
			fixture: "ptah migrations test/assertion failure",
			stage:   "assertion exit contract",
			detail:  "failed assertion produced a structured report and process exit code 1",
			exit:    1,
			args: []string{
				"migrations", "test",
				"--dir", filepath.Join(root, "assertion-failure"),
				"--report", "json",
			},
			stdout: jsonReportExpectation{
				kind:       "MIGRATION",
				caseName:   "expected assertion failure",
				total:      1,
				failed:     1,
				casePassed: false,
				stepNames:  []string{"", ""},
				stepDetails: []string{
					"exec ok",
					"expected row_count 1, got 0",
				},
				stepPassed: []bool{true, false},
			},
			stderr: exactOutputExpectation(""),
		},
		dbTestMigrationSetupFailureCheck(root),
		{
			fixture: "ptah schema test/assertion failure",
			stage:   "assertion exit contract",
			detail:  "failed schema assertion produced a structured report and process exit code 1",
			exit:    1,
			args: []string{
				"schema", "test",
				"--dir", filepath.Join(root, "assertion-failure"),
				"--root-dir", models,
				"--report", "json",
			},
			stdout: jsonReportExpectation{
				kind:       "SCHEMA",
				caseName:   "expected assertion failure",
				total:      1,
				failed:     1,
				casePassed: false,
				stepNames:  []string{"", ""},
				stepDetails: []string{
					"exec ok",
					"expected row_count 1, got 0",
				},
				stepPassed: []bool{true, false},
			},
			stderr: exactOutputExpectation(""),
		},
		dbTestSchemaSetupFailureCheck(root, models),
		{
			fixture: "ptah schema test/invalid migration step",
			stage:   "step validation",
			detail:  "schema tests rejected migrate_to with process exit code 1 and an actionable result",
			exit:    1,
			args: []string{
				"schema", "test",
				"--dir", filepath.Join(root, "schema-invalid-step"),
				"--root-dir", models,
				"--report", "json",
			},
			stdout: jsonReportExpectation{
				kind:       "SCHEMA",
				caseName:   "schema rejects migrate_to",
				total:      1,
				failed:     1,
				casePassed: false,
				stepNames:  []string{"migration step is invalid"},
				stepDetails: []string{
					`migrate_to is not valid in a schema test; use "ptah migrations test"`,
				},
				stepPassed: []bool{false},
			},
			stderr: exactOutputExpectation(""),
		},
	}
}

func dbTestMigrationSetupFailureCheck(root string) dbTestWorkflowCheck {
	return dbTestWorkflowCheck{
		fixture: "ptah migrations test/setup failure",
		stage:   "setup exit contract",
		detail:  "invalid declarative input produced process exit code 2 and an actionable diagnostic",
		exit:    2,
		args: []string{
			"migrations", "test",
			"--dir", filepath.Join(root, "setup-failure"),
			"--report", "text",
		},
		stdout: exactOutputExpectation(""),
		stderr: dbTestSetupFailureExpectation(),
	}
}

func dbTestSchemaSetupFailureCheck(root, models string) dbTestWorkflowCheck {
	return dbTestWorkflowCheck{
		fixture: "ptah schema test/setup failure",
		stage:   "setup exit contract",
		detail:  "invalid schema-test input produced process exit code 2 and an actionable diagnostic",
		exit:    2,
		args: []string{
			"schema", "test",
			"--dir", filepath.Join(root, "setup-failure"),
			"--root-dir", models,
			"--report", "text",
		},
		stdout: exactOutputExpectation(""),
		stderr: dbTestSetupFailureExpectation(),
	}
}

func dbTestSetupFailureExpectation() fragmentExpectation {
	return fragmentExpectation{
		required: []string{
			"failed to load test cases",
			"field unexpected not found",
		},
		forbidden: []string{
			"panic:",
			"goroutine ",
		},
	}
}
