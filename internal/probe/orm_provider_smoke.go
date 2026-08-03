package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"go.5x5.cz/ptah/atlascompat"
)

const (
	// GORMProviderVersion is the provider version exercised by this tier.
	GORMProviderVersion = "v0.6.1"
	// SQLAlchemyProviderVersion is the provider version exercised by this tier.
	SQLAlchemyProviderVersion = "0.5.0"
	// SQLAlchemyVersion is the ORM version exercised by this tier.
	SQLAlchemyVersion = "2.0.51"

	ormProviderProbeName = "orm-provider-smoke"
	ormProviderIssue     = "stokaro/ptah#669"
)

const (
	gormProviderName       = "gorm"
	sqlAlchemyProviderName = "sqlalchemy"
)

// ORMProviderSmokeProbe exercises pinned external ORM providers through the
// real Ptah CLI. Each fixture is copied before provider dependencies are added,
// keeping the conformance repository's root module and source fixtures clean.
//
// Command overrides skip provider dependency setup and run from the copied
// fixture. They are intended for focused tests and local harness development.
type ORMProviderSmokeProbe struct {
	FixtureRoot            string
	Binary                 string
	GORMCommand            []string
	SQLAlchemyCommand      []string
	SQLAlchemyPython       string
	ProviderCommandTimeout time.Duration
	PtahCommandTimeout     time.Duration
}

// Run executes the GORM and SQLAlchemy provider smoke workflows.
func (p ORMProviderSmokeProbe) Run() []Result {
	root, err := p.fixturePath()
	if err != nil {
		return []Result{ormProviderHarnessFailure("orm providers", "fixture setup", err)}
	}
	bin, err := p.binary()
	if err != nil {
		return []Result{ormProviderHarnessFailure("orm providers", "binary build", err)}
	}
	runRoot, err := os.MkdirTemp("", "ptah-orm-providers-*")
	if err != nil {
		return []Result{ormProviderHarnessFailure("orm providers", "runtime setup", err)}
	}
	defer func() { _ = os.RemoveAll(runRoot) }()

	results := p.runGORM(bin, root, runRoot)
	results = append(results, p.runSQLAlchemy(bin, root, runRoot)...)
	return results
}

func (p ORMProviderSmokeProbe) fixturePath() (string, error) {
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

func (p ORMProviderSmokeProbe) binary() (string, error) {
	if strings.TrimSpace(p.Binary) != "" {
		return p.Binary, nil
	}
	return ptahBinary()
}

func (p ORMProviderSmokeProbe) runGORM(bin, fixtureRoot, runRoot string) []Result {
	dir, err := copyORMProviderFixture(fixtureRoot, runRoot, gormProviderName)
	if err != nil {
		return []Result{ormProviderHarnessFailure(gormProviderName, "fixture copy", err)}
	}

	command := p.GORMCommand
	if len(command) == 0 {
		err = p.runSetupCommand(dir, []string{
			"go", "get",
			"ariga.io/atlas-provider-gorm@" + GORMProviderVersion,
			"golang.org/x/text@v0.40.0",
		})
		if err != nil {
			return []Result{ormProviderHarnessFailure(gormProviderName, "provider setup", err)}
		}
		command = []string{
			"go", "run", "-mod=mod", "ariga.io/atlas-provider-gorm",
			"load", "--path", "./models", "--dialect", "sqlite",
		}
	}
	return p.runProvider(bin, gormProviderName, dir, command)
}

func (p ORMProviderSmokeProbe) runSQLAlchemy(bin, fixtureRoot, runRoot string) []Result {
	dir, err := copyORMProviderFixture(fixtureRoot, runRoot, sqlAlchemyProviderName)
	if err != nil {
		return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "fixture copy", err)}
	}

	command := p.SQLAlchemyCommand
	if len(command) == 0 {
		if err := validateSQLAlchemyRequirements(dir); err != nil {
			return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "provider setup", err)}
		}
		python, err := p.pythonBinary()
		if err != nil {
			return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "provider setup", err)}
		}
		venvRoot := filepath.Join(runRoot, "sqlalchemy-venv")
		if err := p.runSetupCommand(dir, []string{python, "-m", "venv", venvRoot}); err != nil {
			return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "provider setup", err)}
		}
		if err := p.runSetupCommand(dir, []string{
			virtualenvExecutable(venvRoot, "python"),
			"-m", "pip", "install",
			"--disable-pip-version-check",
			"--require-hashes",
			"--requirement", "requirements.txt",
		}); err != nil {
			return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "provider setup", err)}
		}
		command = []string{
			virtualenvExecutable(filepath.Join("..", "sqlalchemy-venv"), "python"),
			"load_models.py",
		}
	}
	return p.runProvider(bin, sqlAlchemyProviderName, dir, command)
}

func (p ORMProviderSmokeProbe) pythonBinary() (string, error) {
	if python := strings.TrimSpace(p.SQLAlchemyPython); python != "" {
		return python, nil
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("find python3: %w", err)
	}
	return python, nil
}

func (p ORMProviderSmokeProbe) runSetupCommand(dir string, args []string) error {
	result, err := runORMCommand(dir, p.providerTimeout(), args)
	if err != nil {
		return fmt.Errorf("execute %q: %w; %s", strings.Join(args, " "), err, result.diagnostic())
	}
	if result.exitCode != 0 {
		return fmt.Errorf(
			"execute %q: exit code %d; %s",
			strings.Join(args, " "), result.exitCode, result.diagnostic(),
		)
	}
	return nil
}

func (p ORMProviderSmokeProbe) runProvider(
	bin, provider, dir string,
	command []string,
) []Result {
	providerResult, err := runORMCommand(dir, p.providerTimeout(), command)
	if err != nil {
		return []Result{ormProviderHarnessFailure(provider, "provider execution", fmt.Errorf(
			"execute %q: %w; %s", strings.Join(command, " "), err, providerResult.diagnostic(),
		))}
	}
	if providerResult.exitCode != 0 {
		return []Result{ormProviderHarnessFailure(provider, "provider execution", fmt.Errorf(
			"execute %q: exit code %d; %s",
			strings.Join(command, " "), providerResult.exitCode, providerResult.diagnostic(),
		))}
	}

	outputResult := validateORMProviderSchema(provider, "provider output", providerResult.stdout)
	results := []Result{outputResult}
	if outputResult.Outcome != OK {
		return results
	}

	ptahArgs := []string{
		bin,
		"schema", "render",
		"--schema-cmd", strings.Join(command, " "),
		"--schema-format", "sql",
		"--dialect", "sqlite",
	}
	ptahResult, err := runORMCommand(dir, p.ptahTimeout(), ptahArgs)
	if err != nil {
		return append(results, ormProviderHarnessFailure(provider, "ptah execution", fmt.Errorf(
			"execute ptah schema render: %w; %s", err, ptahResult.diagnostic(),
		)))
	}
	if ptahResult.exitCode != 0 {
		return append(results, ormProviderGap(provider, "ptah schema render", fmt.Sprintf(
			"provider succeeded, but Ptah exited with code %d: %s",
			ptahResult.exitCode, ptahResult.diagnostic(),
		)))
	}
	return append(results, validateORMProviderRender(provider, ptahResult))
}

func (p ORMProviderSmokeProbe) providerTimeout() time.Duration {
	if p.ProviderCommandTimeout > 0 {
		return p.ProviderCommandTimeout
	}
	return 10 * time.Minute
}

func (p ORMProviderSmokeProbe) ptahTimeout() time.Duration {
	if p.PtahCommandTimeout > 0 {
		return p.PtahCommandTimeout
	}
	return 5 * time.Minute
}

func copyORMProviderFixture(fixtureRoot, runRoot, provider string) (string, error) {
	source := filepath.Join(fixtureRoot, provider)
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("stat %s fixture: %w", provider, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s fixture is not a directory: %s", provider, source)
	}
	destination := filepath.Join(runRoot, provider)
	if err := os.CopyFS(destination, os.DirFS(source)); err != nil {
		return "", fmt.Errorf("copy %s fixture: %w", provider, err)
	}
	return destination, nil
}

func validateSQLAlchemyRequirements(dir string) error {
	required := []string{
		"atlas-provider-sqlalchemy==" + SQLAlchemyProviderVersion,
		"SQLAlchemy==" + SQLAlchemyVersion,
	}
	for _, name := range []string{"requirements.in", "requirements.txt"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		requirements := strings.Fields(string(data))
		for _, pin := range required {
			if !slices.ContainsFunc(requirements, func(requirement string) bool {
				return strings.EqualFold(requirement, pin)
			}) {
				return fmt.Errorf("%s is missing exact pin %q", name, pin)
			}
		}
	}
	return nil
}

func virtualenvExecutable(root, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "Scripts", name+".exe")
	}
	return filepath.Join(root, "bin", name)
}

type ormCommandResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func (r ormCommandResult) diagnostic() string {
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

func runORMCommand(dir string, timeout time.Duration, args []string) (ormCommandResult, error) {
	if len(args) == 0 {
		return ormCommandResult{exitCode: -1}, fmt.Errorf("command is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(ptahCommandEnvironment(), "GOWORK=off")
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := ormCommandResult{
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
		if result.exitCode >= 0 {
			return result, nil
		}
	}
	result.exitCode = -1
	return result, err
}

type ormSchemaFact struct {
	name     string
	fragment string
}

var ormProviderSchemaFacts = []ormSchemaFact{
	{name: "users table", fragment: "createtableusers"},
	{name: "pets table", fragment: "createtablepets"},
	{name: "pets-to-users foreign key", fragment: "foreignkey(user_id)referencesusers(id)"},
	{name: "unique index declaration", fragment: "createuniqueindex"},
	{name: "users email index identity", fragment: "idx_users_email"},
}

func validateORMProviderSchema(provider, stage, output string) Result {
	compact := compactORMProviderOutput(output)
	var missing []string
	for _, fact := range ormProviderSchemaFacts {
		if !strings.Contains(compact, fact.fragment) {
			missing = append(missing, fact.name)
		}
	}
	if strings.Count(compact, "primarykey") < 2 {
		missing = append(missing, "primary keys on users and pets")
	}
	if len(missing) > 0 {
		return ormProviderGap(provider, stage,
			"missing expected schema facts: "+strings.Join(missing, ", "))
	}
	return ormProviderOK(provider, stage)
}

func validateORMProviderRender(provider string, result ormCommandResult) Result {
	const stage = "ptah schema render"
	compactStdout := compactORMProviderOutput(result.stdout)
	if strings.Contains(compactStdout, "found2tables") || strings.Contains(result.stdout, "=== ") {
		return ormProviderGap(provider, stage, "render stdout contains non-SQL progress text")
	}
	if _, err := atlascompat.ParseSQL(result.stdout, atlascompat.ParseSQLOptions{Dialect: "sqlite"}); err != nil {
		return ormProviderGap(provider, stage, "render stdout is not valid SQL: "+oneLine(err.Error()))
	}
	if schemaResult := validateORMProviderSchema(provider, stage, result.stdout); schemaResult.Outcome != OK {
		return schemaResult
	}
	if !strings.Contains(compactORMProviderOutput(result.stderr), "found2tables") {
		return ormProviderGap(provider, stage, "render stderr is missing the Ptah two-table progress summary")
	}
	return ormProviderOK(provider, stage)
}

func compactORMProviderOutput(output string) string {
	replacer := strings.NewReplacer(
		"`", "",
		`"`, "",
		"[", "",
		"]", "",
	)
	return strings.Join(strings.Fields(strings.ToLower(replacer.Replace(output))), "")
}

func ormProviderOK(provider, stage string) Result {
	return Result{
		Probe:   ormProviderProbeName,
		Fixture: provider,
		Stage:   stage,
		Outcome: OK,
		Detail:  ormProviderSuccessDetail(provider, stage),
	}
}

func ormProviderSuccessDetail(provider, stage string) string {
	switch provider {
	case gormProviderName:
		return fmt.Sprintf(
			"GORM provider %s %s preserved two tables, primary keys, a unique index, and a foreign key",
			GORMProviderVersion, stage,
		)
	case sqlAlchemyProviderName:
		return fmt.Sprintf(
			"SQLAlchemy provider %s with SQLAlchemy %s %s preserved two tables, primary keys, a unique index, and a foreign key",
			SQLAlchemyProviderVersion, SQLAlchemyVersion, stage,
		)
	default:
		return provider + " " + stage + " preserved the expected ORM schema facts"
	}
}

func ormProviderGap(provider, stage, detail string) Result {
	return Result{
		Probe:   ormProviderProbeName,
		Fixture: provider,
		Stage:   stage,
		Outcome: Gap,
		Detail:  detail,
		Issue:   ormProviderIssue,
	}
}

func ormProviderHarnessFailure(provider, stage string, err error) Result {
	return Result{
		Probe:   ormProviderProbeName,
		Fixture: provider,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}

// RenderORMProviderMarkdown renders the report for this non-deterministic tier.
func RenderORMProviderMarkdown(results []Result, ptahVersion, command string) string {
	summary := summarize(results)
	nonOK := NonOK(results)
	var b strings.Builder

	b.WriteString("# Ptah ORM provider conformance report\n\n")
	fmt.Fprintf(&b, "This file is generated by `%s`. Do not edit by hand.\n\n", command)
	b.WriteString("This separate, non-deterministic tier runs external provider toolchains and\n")
	b.WriteString("then feeds their SQL output through Ptah's `--schema-cmd` contract. Provider\n")
	b.WriteString("installation and execution failures are harness failures; schema behavior\n")
	b.WriteString("mismatches are tracked against `stokaro/ptah#669`.\n\n")

	if len(nonOK) == 0 {
		b.WriteString("## Status: PROVIDER CONFORMANCE on the pinned fixtures\n\n")
	} else {
		fmt.Fprintf(&b, "## Status: NOT DONE - %d non-OK observation(s)\n\n", len(nonOK))
	}
	fmt.Fprintf(&b, "- GORM provider: `ariga.io/atlas-provider-gorm@%s`\n", GORMProviderVersion)
	fmt.Fprintf(&b, "- SQLAlchemy provider: `atlas-provider-sqlalchemy==%s`\n", SQLAlchemyProviderVersion)
	fmt.Fprintf(&b, "- SQLAlchemy: `SQLAlchemy==%s`\n", SQLAlchemyVersion)
	fmt.Fprintf(&b, "- Ptah at `%s`\n", ptahVersion)
	fmt.Fprintf(&b, "- Outcomes: **%d ok**, **%d gap**, **%d fail**, **%d panic**\n\n",
		summary.OK, summary.Gap, summary.Fail, summary.Panic)

	b.WriteString("## Findings\n\n")
	b.WriteString("| Gate | Outcome | Provider | Stage | Detail | Related |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, result := range results {
		gate := "**RED**"
		if result.Outcome == OK {
			gate = "-"
		}
		issue := ""
		if result.Issue != "" {
			issue = "#" + strings.TrimPrefix(result.Issue, "stokaro/ptah#")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			gate,
			badge(result.Outcome),
			result.Fixture,
			result.Stage,
			escapePipe(result.Detail),
			issue,
		)
	}
	return b.String()
}
