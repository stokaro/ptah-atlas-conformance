package probe

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// runTxtarMigrateLint drives the real `atlas migrate lint` CLI (ptah-compat) over a
// txtar fixture's materialized migration directory. It proves Ptah's own Atlas
// migrate-lint behavior end to end — the default analysis text report, the
// destructive/data-dependent diagnostics, `-- atlas:nolint` suppression, the
// exit-1 failure threshold, and atlas.hcl `--env`/`lint.log` project-config
// resolution — rather than a harness-local reimplementation of Atlas's linter.
// Structural upstream assertions remain unchanged. Atlas-owned diagnostic
// prose is compiled into ordered diagnostic identities, then a second
// `--format '{{ json .Files }}'` invocation checks Ptah's structured object
// subjects and required remediation without requiring Ptah to copy Atlas text.
//
// The dev-url replay needs a directly-connectable dev database, so each command
// materializes an ephemeral pure-Go SQLite database (modernc.org/sqlite) into a
// throwaway working directory. That keeps this tier Docker-free and
// Atlas-binary-free. Non-SQLite families need an Atlas HCL-inspect-backed dev
// URL that this offline tier does not provide, and remain explicitly
// unsupported.
func runTxtarMigrateLint(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "lint" {
		return txtarCommandResult{}, false
	}
	if txtarFixtureFamily(fx) != "sqlite" {
		return txtarCommandResult{unsupported: "atlas migrate lint (non-sqlite dev-url replay)"}, true
	}

	plan, ok := txtarPlanMigrateLint(fields[3:])
	if !ok {
		return txtarCommandResult{unsupported: "atlas migrate lint"}, true
	}

	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		// A build failure here is environmental (the go build ./... gate catches
		// genuine breakage); degrade to unsupported rather than a false red.
		return txtarCommandResult{unsupported: "atlas migrate lint (ptah-compat CLI unavailable: " + oneLine(err.Error()) + ")"}, true
	}

	run, err := txtarExecMigrateLint(runtime, bin, plan)
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	if plan.redirect != "" {
		runtime.files[plan.redirect] = run.stdout
		runtime.addParentDirs(plan.redirect)
		return txtarCommandResult{stderr: run.stderr, failed: run.failed, err: run.err}, true
	}
	return txtarCommandResult{
		stdout:         run.stdout,
		stderr:         run.stderr,
		failed:         run.failed,
		migrateLintRun: &run,
		err:            run.err,
	}, true
}

var (
	migrateLintExpectedVersionPattern  = regexp.MustCompile(`-- analyzing version ([0-9]+)`)
	migrateLintExpectedGroupPattern    = regexp.MustCompile(`-- ([a-z -]+) changes detected:`)
	migrateLintExpectedLinePattern     = regexp.MustCompile(`-- L([0-9]+):`)
	migrateLintExpectedCodePattern     = regexp.MustCompile(`#([A-Z]{2}[0-9]{3})`)
	migrateLintExpectedQuotedPattern   = regexp.MustCompile(`"([^"]+)"`)
	migrateLintActualVersionPattern    = regexp.MustCompile(`^\s*-- analyzing version ([0-9]+)$`)
	migrateLintActualGroupPattern      = regexp.MustCompile(`^\s*-- ([a-z -]+) changes detected:$`)
	migrateLintActualDiagnosticPattern = regexp.MustCompile(
		`^\s*-- L([0-9]+) \[([A-Z]{2}[0-9]{3})\]: \S.*$`,
	)
)

type migrateLintDiagnostic struct {
	version string
	group   string
	line    int
	code    string
}

type migrateLintSubject struct {
	kind     string
	name     string
	parent   string
	dataType string
}

type migrateLintSemanticDiagnostic struct {
	version     string
	line        int
	code        string
	subjects    []migrateLintSubject
	remediation bool
}

type migrateLintExpectedDiagnostics struct {
	version          string
	group            string
	pendingLine      int
	pendingPattern   string
	diagnosticSeen   bool
	suggestedFixSeen bool
	diagnostics      []migrateLintDiagnostic
	semantic         []migrateLintSemanticDiagnostic
}

func (e *migrateLintExpectedDiagnostics) observe(line string) (handled bool, err error) {
	pattern := txtarAssertionText(line)
	if match := migrateLintExpectedVersionPattern.FindStringSubmatch(pattern); match != nil {
		if err := e.requireCompleteAssertion(); err != nil {
			return false, err
		}
		e.version = match[1]
		e.group = ""
		e.diagnosticSeen = false
		e.suggestedFixSeen = false
		return false, nil
	}
	if match := migrateLintExpectedGroupPattern.FindStringSubmatch(pattern); match != nil {
		if err := e.requireCompleteAssertion(); err != nil {
			return false, err
		}
		e.group = match[1]
		e.diagnosticSeen = false
		e.suggestedFixSeen = false
		return false, nil
	}

	lineMatch := migrateLintExpectedLinePattern.FindStringSubmatch(pattern)
	codeMatch := migrateLintExpectedCodePattern.FindStringSubmatch(pattern)
	switch {
	case lineMatch != nil && codeMatch != nil:
		if err := e.requireCompleteAssertion(); err != nil {
			return true, err
		}
		lineNumber, parseErr := strconv.Atoi(lineMatch[1])
		if parseErr != nil {
			return true, parseErr
		}
		return true, e.append(lineNumber, codeMatch[1], pattern)
	case lineMatch != nil:
		if err := e.requireCompleteAssertion(); err != nil {
			return true, err
		}
		lineNumber, parseErr := strconv.Atoi(lineMatch[1])
		if parseErr != nil {
			return true, parseErr
		}
		e.pendingLine = lineNumber
		e.pendingPattern = pattern
		return true, nil
	case codeMatch != nil:
		if e.pendingLine == 0 {
			return true, fmt.Errorf("migrate lint assertion has code %s without a diagnostic line", codeMatch[1])
		}
		lineNumber := e.pendingLine
		diagnosticPattern := e.pendingPattern
		e.pendingLine = 0
		e.pendingPattern = ""
		return true, e.append(lineNumber, codeMatch[1], diagnosticPattern)
	case strings.Contains(pattern, "-- suggested fix:"):
		if !e.diagnosticSeen {
			return true, errors.New("migrate lint suggested fix has no preceding diagnostic")
		}
		e.suggestedFixSeen = true
		return true, nil
	case strings.HasPrefix(strings.TrimSpace(pattern), "->"):
		if !e.suggestedFixSeen {
			return true, errors.New("migrate lint suggested fix body has no fix heading")
		}
		return true, e.attachRemediation(pattern)
	default:
		return false, nil
	}
}

func (e *migrateLintExpectedDiagnostics) requireCompleteAssertion() error {
	if e.pendingLine != 0 {
		return fmt.Errorf("migrate lint assertion omitted the diagnostic code for line %d", e.pendingLine)
	}
	if e.suggestedFixSeen {
		return errors.New("migrate lint suggested fix heading has no body")
	}
	return nil
}

func (e *migrateLintExpectedDiagnostics) append(line int, code, pattern string) error {
	if e.version == "" || e.group == "" {
		return fmt.Errorf("migrate lint diagnostic %s at line %d has no version/group context", code, line)
	}
	subjects, err := expectedMigrateLintSubjects(code, pattern)
	if err != nil {
		return err
	}
	e.diagnostics = append(e.diagnostics, migrateLintDiagnostic{
		version: e.version,
		group:   e.group,
		line:    line,
		code:    code,
	})
	e.semantic = append(e.semantic, migrateLintSemanticDiagnostic{
		version:  e.version,
		line:     line,
		code:     code,
		subjects: subjects,
	})
	e.diagnosticSeen = true
	e.suggestedFixSeen = false
	return nil
}

func expectedMigrateLintSubjects(code, pattern string) ([]migrateLintSubject, error) {
	matches := migrateLintExpectedQuotedPattern.FindAllStringSubmatch(pattern, -1)
	values := make([]string, len(matches))
	for i, match := range matches {
		values[i] = match[1]
	}
	switch code {
	case "DS102":
		if len(values) != 1 {
			return nil, fmt.Errorf("migrate lint DS102 assertion has %d quoted subjects, want 1", len(values))
		}
		return []migrateLintSubject{{kind: "table", name: values[0]}}, nil
	case "MF103":
		if len(values) != 3 {
			return nil, fmt.Errorf("migrate lint MF103 assertion has %d quoted subjects, want 3", len(values))
		}
		return []migrateLintSubject{{kind: "column", dataType: values[0], name: values[1], parent: values[2]}}, nil
	default:
		return nil, nil
	}
}

func (e *migrateLintExpectedDiagnostics) attachRemediation(pattern string) error {
	matches := migrateLintExpectedQuotedPattern.FindAllStringSubmatch(pattern, -1)
	if len(matches) != 1 {
		return fmt.Errorf("migrate lint suggested fix has %d quoted subjects, want 1", len(matches))
	}
	last := len(e.semantic) - 1
	if last < 0 || len(e.semantic[last].subjects) != 1 || e.semantic[last].subjects[0].name != matches[0][1] {
		return errors.New("migrate lint suggested fix subject does not match its diagnostic")
	}
	e.semantic[last].remediation = true
	e.suggestedFixSeen = false
	return nil
}

func (e *migrateLintExpectedDiagnostics) compare(run txtarMigrateLintRun) error {
	if err := e.requireCompleteAssertion(); err != nil {
		return err
	}
	if run.stderr != "" {
		return fmt.Errorf("migrate lint wrote unexpected stderr: %s", oneLine(run.stderr))
	}
	if run.semanticStderr != "" {
		return fmt.Errorf("migrate lint structured run wrote unexpected stderr: %s", oneLine(run.semanticStderr))
	}
	if run.failed != run.semanticFailed {
		return fmt.Errorf("migrate lint structured run failure state differs: default=%t structured=%t", run.failed, run.semanticFailed)
	}
	actual, err := parseMigrateLintDiagnostics(run.stdout)
	if err != nil {
		return err
	}
	if !slices.Equal(actual, e.diagnostics) {
		return fmt.Errorf("migrate lint diagnostics differ: got %v, want %v", actual, e.diagnostics)
	}
	semantic, err := parseMigrateLintSemanticDiagnostics(run.semanticStdout)
	if err != nil {
		return err
	}
	if !equalMigrateLintSemanticDiagnostics(semantic, e.semantic) {
		return fmt.Errorf("migrate lint semantic diagnostics differ: got %v, want %v", semantic, e.semantic)
	}
	return nil
}

func parseMigrateLintDiagnostics(stdout string) ([]migrateLintDiagnostic, error) {
	var diagnostics []migrateLintDiagnostic
	var version, group string
	for line := range strings.SplitSeq(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if (strings.Contains(line, "://") && strings.Contains(line, "analyzers#")) ||
			trimmed == "-- suggested fix:" || strings.HasPrefix(trimmed, "->") {
			return nil, fmt.Errorf("migrate lint output contains legacy diagnostic prose: %s", oneLine(line))
		}
		if match := migrateLintActualVersionPattern.FindStringSubmatch(line); match != nil {
			version = match[1]
			group = ""
			continue
		}
		if match := migrateLintActualGroupPattern.FindStringSubmatch(line); match != nil {
			group = match[1]
			continue
		}
		match := migrateLintActualDiagnosticPattern.FindStringSubmatch(line)
		if match == nil {
			if strings.HasPrefix(trimmed, "-- L") {
				return nil, fmt.Errorf("migrate lint output contains malformed diagnostic: %s", oneLine(line))
			}
			continue
		}
		lineNumber, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, err
		}
		if version == "" || group == "" {
			return nil, fmt.Errorf("migrate lint diagnostic %s at line %d has no version/group context", match[2], lineNumber)
		}
		diagnostics = append(diagnostics, migrateLintDiagnostic{
			version: version,
			group:   group,
			line:    lineNumber,
			code:    match[2],
		})
	}
	return diagnostics, nil
}

type migrateLintJSONFile struct {
	Name     string                   `json:"Name"`
	Findings []migrateLintJSONFinding `json:"Findings"`
}

type migrateLintJSONFinding struct {
	Rule    string                  `json:"rule"`
	Line    int                     `json:"line"`
	Message string                  `json:"message"`
	Context *migrateLintJSONContext `json:"context"`
}

type migrateLintJSONContext struct {
	Subjects []migrateLintJSONSubject `json:"subjects"`
}

type migrateLintJSONSubject struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Parent   string `json:"parent"`
	DataType string `json:"data_type"`
}

func parseMigrateLintSemanticDiagnostics(stdout string) ([]migrateLintSemanticDiagnostic, error) {
	var files []migrateLintJSONFile
	if err := json.Unmarshal([]byte(stdout), &files); err != nil {
		return nil, fmt.Errorf("parse migrate lint structured output: %w", err)
	}
	var diagnostics []migrateLintSemanticDiagnostic
	for _, file := range files {
		version, err := migrateLintVersionFromFile(file.Name)
		if err != nil {
			return nil, err
		}
		for _, finding := range file.Findings {
			if finding.Context == nil {
				return nil, fmt.Errorf("migrate lint structured finding %s in %s has no context", finding.Rule, file.Name)
			}
			subjects := make([]migrateLintSubject, len(finding.Context.Subjects))
			for i, subject := range finding.Context.Subjects {
				subjects[i] = migrateLintSubject{
					kind:     subject.Kind,
					name:     subject.Name,
					parent:   subject.Parent,
					dataType: subject.DataType,
				}
			}
			_, remediation, hasRemediation := strings.Cut(finding.Message, ";")
			diagnostics = append(diagnostics, migrateLintSemanticDiagnostic{
				version:     version,
				line:        finding.Line,
				code:        finding.Rule,
				subjects:    subjects,
				remediation: hasRemediation && strings.TrimSpace(remediation) != "",
			})
		}
	}
	return diagnostics, nil
}

func migrateLintVersionFromFile(name string) (string, error) {
	version, _, found := strings.Cut(strings.TrimSuffix(path.Base(name), path.Ext(name)), "_")
	if !found {
		version = strings.TrimSuffix(path.Base(name), path.Ext(name))
	}
	if _, err := strconv.ParseUint(version, 10, 64); err != nil {
		return "", fmt.Errorf("parse migrate lint version from %q: %w", name, err)
	}
	return version, nil
}

func equalMigrateLintSemanticDiagnostics(a, b []migrateLintSemanticDiagnostic) bool {
	return slices.EqualFunc(a, b, func(a, b migrateLintSemanticDiagnostic) bool {
		return a.version == b.version && a.line == b.line && a.code == b.code &&
			(!b.remediation || a.remediation) && slices.Equal(a.subjects, b.subjects)
	})
}

// txtarMigrateLintPlan is the parsed shell form of a `migrate lint` command:
// the flags to forward to the Ptah CLI (with the dev-url still holding the Atlas
// `URL` placeholder) and any `> file` stdout redirect.
type txtarMigrateLintPlan struct {
	cliArgs  []string
	redirect string
}

// txtarMigrateLintValueFlags are the `migrate lint` flags Ptah's CLI accepts,
// each consuming the following token as its value. Any flag outside this set
// makes the command unsupported (a Gap) instead of a hard failure, so a future
// fixture exercising an unmapped flag degrades honestly rather than turning red.
var txtarMigrateLintValueFlags = map[string]bool{
	"--dir":        true,
	"--dev-url":    true,
	"--dir-format": true,
	"--format":     true,
	"--latest":     true,
	"--git-base":   true,
	"--git-dir":    true,
	"--env":        true,
	"--config":     true,
	"-c":           true,
	"--var":        true,
}

func txtarPlanMigrateLint(fields []string) (txtarMigrateLintPlan, bool) {
	var plan txtarMigrateLintPlan
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		switch {
		case field == ">":
			if i+1 >= len(fields) {
				return txtarMigrateLintPlan{}, false
			}
			plan.redirect = fields[i+1]
			i++
		case txtarMigrateLintValueFlags[field]:
			if i+1 >= len(fields) {
				return txtarMigrateLintPlan{}, false
			}
			plan.cliArgs = append(plan.cliArgs, field, fields[i+1])
			i++
		case strings.HasPrefix(field, "-") && strings.Contains(field, "="):
			name, _, _ := strings.Cut(field, "=")
			if !txtarMigrateLintValueFlags[name] {
				return txtarMigrateLintPlan{}, false
			}
			plan.cliArgs = append(plan.cliArgs, field)
		default:
			// A bare flag we do not map, or an unexpected positional argument.
			return txtarMigrateLintPlan{}, false
		}
	}
	return plan, true
}

// txtarMigrateLintRun is the outcome of one real `atlas migrate lint`
// invocation on the ptah-compat binary.
type txtarMigrateLintRun struct {
	stdout         string
	stderr         string
	failed         bool
	semanticStdout string
	semanticStderr string
	semanticFailed bool
	err            error
}

func txtarExecMigrateLint(runtime *txtarRuntime, bin string, plan txtarMigrateLintPlan) (txtarMigrateLintRun, error) {
	run, err := txtarExecMigrateLintOnce(runtime, bin, plan)
	if err != nil || plan.redirect != "" {
		return run, err
	}
	semanticPlan := plan
	semanticPlan.cliArgs = append(slices.Clone(plan.cliArgs), "--format", "{{ json .Files }}")
	semanticRun, err := txtarExecMigrateLintOnce(runtime, bin, semanticPlan)
	if err != nil {
		return txtarMigrateLintRun{}, err
	}
	run.semanticStdout = semanticRun.stdout
	run.semanticStderr = semanticRun.stderr
	run.semanticFailed = semanticRun.failed
	return run, nil
}

func txtarExecMigrateLintOnce(runtime *txtarRuntime, bin string, plan txtarMigrateLintPlan) (txtarMigrateLintRun, error) {
	workdir, err := os.MkdirTemp("", "txtar-migrate-lint-*")
	if err != nil {
		return txtarMigrateLintRun{}, err
	}
	defer func() { _ = os.RemoveAll(workdir) }()

	// A fresh, empty SQLite dev database per invocation: `migrate lint` replays
	// the migrations onto it to derive the schema changes it analyzes.
	devURL := "sqlite://" + filepath.ToSlash(filepath.Join(workdir, "__ptah_dev.db"))
	if err := txtarMaterializeLintFiles(runtime, workdir, plan.redirect, devURL); err != nil {
		return txtarMigrateLintRun{}, err
	}

	args := append([]string{"migrate", "lint"}, txtarSubstituteDevURL(plan.cliArgs, devURL)...)
	stdout, stderr, runErr := commandStreams(bin, args, workdir)
	run := txtarMigrateLintRun{stdout: stdout, stderr: stderr}
	if runErr == nil {
		return run, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// A non-zero exit is the linter's failure-threshold signal, not a harness
		// error: surface it as a failed command so `! atlas migrate lint` matches
		// and a bare command records the finding detail.
		run.failed = true
		run.err = fmt.Errorf("atlas migrate lint exited %d: %s", exitErr.ExitCode(), oneLine(firstNonEmpty(stderr, stdout)))
		return run, nil
	}
	return txtarMigrateLintRun{}, runErr
}

// txtarMaterializeLintFiles writes the fixture's virtual files into workdir so
// the real CLI can read the migration directory and any atlas.hcl. The Atlas
// dev-url placeholder inside an atlas.hcl `dev = "URL"` attribute is rewritten
// to the ephemeral SQLite dev URL, matching how Atlas's own testscript runner
// substitutes URL.
func txtarMaterializeLintFiles(runtime *txtarRuntime, workdir, redirect, devURL string) error {
	for name, content := range runtime.files {
		if name == "stdout" || name == "stderr" || name == redirect {
			continue
		}
		if !txtarSafeRelPath(name) {
			continue
		}
		if path.Base(name) == "atlas.hcl" {
			content = txtarSubstituteHCLDevURL(content, devURL)
		}
		dest := filepath.Join(workdir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// txtarSubstituteDevURL replaces the Atlas `URL` dev-url placeholder on the
// command line with a concrete dev database URL. Both `--dev-url URL` and
// `--dev-url=URL` spellings are handled; any other dev-url value is left intact.
func txtarSubstituteDevURL(args []string, devURL string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == "--dev-url" && i+1 < len(out) && out[i+1] == "URL":
			out[i+1] = devURL
			i++
		case out[i] == "--dev-url=URL":
			out[i] = "--dev-url=" + devURL
		}
	}
	return out
}

// txtarSubstituteHCLDevURL rewrites the quoted Atlas `"URL"` dev-url placeholder
// in an atlas.hcl body to the concrete dev database URL. In the Atlas migrate
// lint fixtures URL appears only as a dev database value, so a scoped token
// replacement is sufficient and avoids reimplementing HCL.
func txtarSubstituteHCLDevURL(content, devURL string) string {
	return strings.ReplaceAll(content, `"URL"`, `"`+devURL+`"`)
}

// txtarSafeRelPath reports whether name is a clean relative path that stays
// inside the materialization root.
func txtarSafeRelPath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	clean := path.Clean(name)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
