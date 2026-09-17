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

	"ptah.run/atlascompat"
)

const (
	// GORMProviderVersion is the provider version exercised by this tier. It is
	// an input rather than a copy: the probe passes it to `go get`, and the
	// fixture's go.mod pins the ORM it imports and nothing else.
	GORMProviderVersion = "v0.6.1"

	ormProviderProbeName = "orm-provider-smoke"
	ormProviderIssue     = "stokaro/ptah#669"
)

const (
	gormProviderName       = "gorm"
	sqlAlchemyProviderName = "sqlalchemy"

	sqlAlchemyProviderPackage = "atlas-provider-sqlalchemy"
	sqlAlchemyPackage         = "SQLAlchemy"
)

// SQLAlchemyPins are the versions one run of this tier installed.
//
// They are read out of the fixture rather than written down here. The probe
// installs `requirements.txt` with --require-hashes, so that file decides what
// actually ran; a version restated in Go is a third copy that only a person
// moves, and the two that a dependency bot does move leave it behind. That
// happened: the lock reached 2.0.53 and then 2.0.54 while the constant stayed
// at 2.0.52, and the tier was red on every run until someone noticed.
type SQLAlchemyPins struct {
	// Provider is the atlas-provider-sqlalchemy version.
	Provider string
	// ORM is the SQLAlchemy version.
	ORM string
}

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
	return p.runProvider(bin, gormProviderName, dir, command, SQLAlchemyPins{})
}

func (p ORMProviderSmokeProbe) runSQLAlchemy(bin, fixtureRoot, runRoot string) []Result {
	dir, err := copyORMProviderFixture(fixtureRoot, runRoot, sqlAlchemyProviderName)
	if err != nil {
		return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "fixture copy", err)}
	}

	// The override path skips installation, so it has no pins to report; the
	// detail then names the provider without a version rather than a version
	// nothing installed.
	var pins SQLAlchemyPins
	command := p.SQLAlchemyCommand
	if len(command) == 0 {
		resolved, err := readSQLAlchemyPins(dir)
		if err != nil {
			return []Result{ormProviderHarnessFailure(sqlAlchemyProviderName, "provider setup", err)}
		}
		pins = resolved
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
	return p.runProvider(bin, sqlAlchemyProviderName, dir, command, pins)
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
	pins SQLAlchemyPins,
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

	outputResult := validateORMProviderSchema(provider, "provider output", providerResult.stdout, pins)
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
	return append(results, validateORMProviderRender(provider, ptahResult, pins))
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

// readSQLAlchemyPins reads the versions the fixture installs, and refuses a
// fixture that does not say exactly which ones those are.
//
// requirements.txt is the answer because it is the file pip installs. What
// requirements.in adds is intent: the two disagreeing means a lock arrived that
// nobody asked for -- a bot that rewrote only the compiled file is the usual
// way -- and running it would publish a measurement of a version the fixture
// never chose. So the disagreement is refused here and named in both
// directions, rather than resolved in favor of either file.
func readSQLAlchemyPins(dir string) (SQLAlchemyPins, error) {
	intent, err := sqlAlchemyPinsIn(dir, "requirements.in")
	if err != nil {
		return SQLAlchemyPins{}, err
	}
	installed, err := sqlAlchemyPinsIn(dir, "requirements.txt")
	if err != nil {
		return SQLAlchemyPins{}, err
	}
	if intent != installed {
		return SQLAlchemyPins{}, fmt.Errorf(
			"requirements.in asks for %s==%s and %s==%s, and requirements.txt installs %s==%s and %s==%s;"+
				" recompile the lock from the source rather than running a version the fixture did not choose",
			sqlAlchemyProviderPackage, intent.Provider, sqlAlchemyPackage, intent.ORM,
			sqlAlchemyProviderPackage, installed.Provider, sqlAlchemyPackage, installed.ORM,
		)
	}
	return installed, nil
}

// sqlAlchemyPinsIn reads one requirements file.
//
// A requirement is taken only from an exact `==` pin, so a range, a marker or a
// missing entry is a refusal rather than a version the report would have to
// guess at. The lock wraps its lines with a trailing backslash and carries
// `--hash` fields, which is why the file is read as whitespace-separated fields
// and each field is matched whole.
func sqlAlchemyPinsIn(dir, name string) (SQLAlchemyPins, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return SQLAlchemyPins{}, fmt.Errorf("read %s: %w", name, err)
	}
	fields := strings.Fields(string(data))
	var pins SQLAlchemyPins
	for _, target := range []struct {
		pkg  string
		into *string
	}{
		{sqlAlchemyProviderPackage, &pins.Provider},
		{sqlAlchemyPackage, &pins.ORM},
	} {
		prefix := target.pkg + "=="
		index := slices.IndexFunc(fields, func(field string) bool {
			return len(field) > len(prefix) && strings.EqualFold(field[:len(prefix)], prefix)
		})
		if index < 0 {
			return SQLAlchemyPins{}, fmt.Errorf("%s carries no exact %q pin", name, target.pkg)
		}
		*target.into = fields[index][len(prefix):]
	}
	return pins, nil
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

func validateORMProviderSchema(provider, stage, output string, pins SQLAlchemyPins) Result {
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
	return ormProviderOK(provider, stage, pins)
}

func validateORMProviderRender(provider string, result ormCommandResult, pins SQLAlchemyPins) Result {
	const stage = "ptah schema render"
	compactStdout := compactORMProviderOutput(result.stdout)
	if strings.Contains(compactStdout, "found2tables") || strings.Contains(result.stdout, "=== ") {
		return ormProviderGap(provider, stage, "render stdout contains non-SQL progress text")
	}
	if _, err := atlascompat.ParseSQL(result.stdout, atlascompat.ParseSQLOptions{Dialect: "sqlite"}); err != nil {
		return ormProviderGap(provider, stage, "render stdout is not valid SQL: "+oneLine(err.Error()))
	}
	if schemaResult := validateORMProviderSchema(provider, stage, result.stdout, pins); schemaResult.Outcome != OK {
		return schemaResult
	}
	if !strings.Contains(compactORMProviderOutput(result.stderr), "found2tables") {
		return ormProviderGap(provider, stage, "render stderr is missing the Ptah two-table progress summary")
	}
	return ormProviderOK(provider, stage, pins)
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

func ormProviderOK(provider, stage string, pins SQLAlchemyPins) Result {
	return Result{
		Probe:   ormProviderProbeName,
		Fixture: provider,
		Stage:   stage,
		Outcome: OK,
		Detail:  ormProviderSuccessDetail(provider, stage, pins),
	}
}

func ormProviderSuccessDetail(provider, stage string, pins SQLAlchemyPins) string {
	switch {
	case provider == gormProviderName:
		return fmt.Sprintf(
			"GORM provider %s %s preserved two tables, primary keys, a unique index, and a foreign key",
			GORMProviderVersion, stage,
		)
	case provider == sqlAlchemyProviderName && pins == (SQLAlchemyPins{}):
		return fmt.Sprintf(
			"SQLAlchemy provider %s preserved two tables, primary keys, a unique index, and a foreign key",
			stage,
		)
	case provider == sqlAlchemyProviderName:
		return fmt.Sprintf(
			"SQLAlchemy provider %s with SQLAlchemy %s %s preserved two tables, primary keys, a unique index, and a foreign key",
			pins.Provider, pins.ORM, stage,
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

// SQLAlchemyPinsForFixtures reads the pins the SQLAlchemy fixture declares,
// without running anything. The report command needs them for its header, and a
// fixture it cannot read is a report that names no version rather than an
// error: the run's own setup stage reports that failure, with the detail.
func SQLAlchemyPinsForFixtures(fixtureRoot string) SQLAlchemyPins {
	pins, err := readSQLAlchemyPins(filepath.Join(fixtureRoot, sqlAlchemyProviderName))
	if err != nil {
		return SQLAlchemyPins{}
	}
	return pins
}

// RenderORMProviderMarkdown renders the report for this non-deterministic tier.
//
// pins are the versions the run installed, read from the fixture. A zero value
// means no run installed them -- an override path, or a run that stopped before
// setup -- and the header then says so rather than printing an empty pin.
func RenderORMProviderMarkdown(results []Result, pins SQLAlchemyPins, ptahVersion, command string) string {
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
	if pins == (SQLAlchemyPins{}) {
		b.WriteString("- SQLAlchemy provider: not installed by this run\n")
	} else {
		fmt.Fprintf(&b, "- SQLAlchemy provider: `%s==%s`\n", sqlAlchemyProviderPackage, pins.Provider)
		fmt.Fprintf(&b, "- SQLAlchemy: `%s==%s`\n", sqlAlchemyPackage, pins.ORM)
	}
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
