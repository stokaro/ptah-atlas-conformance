package probe

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Atlas CE reports a lint run whose dev replay stopped at a statement the dev
// database refused, and the report has one of two shapes. They were measured
// on the pinned v1.3.0 binary over the rowid alias fixtures with the rename
// replaced by one no SQLite release accepts, a rename of a column the table
// lacks, in the same base file:
//
//   - without a log template, stdout holds the selection header,
//     `Error: executing statement: <answer>` and `-- 1 version with errors`;
//   - with a log template, stdout holds the template rendered over a .Files
//     that holds only the refused file: `1.sql` for a template ranging over the
//     names and `1` for one counting the files.
//
// stderr is empty and the exit code is 1. The binary adds an installation
// advisory on stderr to the first error a HOME sees in a day, and to no other.
// A statement in an analyzed file is quoted between the prefix and the answer;
// a base file's line carries the answer alone.
//
// ptah-compat prints the same shape (stokaro/ptah#5992), so a run over a
// directory the linked engine refuses is judged by its output. The expected
// text comes from the harness's own replay of that directory, on the driver
// ptah-compat links, and never from ptah-compat's output.

// sqliteReplayErrorPrefix opens the line that names the refused statement.
const sqliteReplayErrorPrefix = "Error: executing statement: "

// sqliteRefusalReport is the report Atlas CE prints for one lint run over a
// directory whose replay stopped at a refused statement.
type sqliteRefusalReport struct {
	// answer is the engine's error, as the harness's replay received it.
	answer string
	// templated is set when the run selects a lint log template, and log is
	// that template rendered over the refused file alone.
	templated bool
	log       string
}

// matches reports whether stdout is that report.
func (r sqliteRefusalReport) matches(stdout string) bool {
	if r.templated {
		return stdout == r.log
	}
	for line := range strings.Lines(stdout) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), sqliteReplayErrorPrefix)
		if ok && (rest == r.answer || strings.HasSuffix(rest, ": "+r.answer)) {
			return true
		}
	}
	return false
}

// want says what stdout must hold, for a run that does not hold it.
func (r sqliteRefusalReport) want() string {
	if r.templated {
		return fmt.Sprintf("%q from the lint log template", r.log)
	}
	return fmt.Sprintf("an %q line ending in the replay's answer", strings.TrimSpace(sqliteReplayErrorPrefix))
}

// found says what stdout held, for a run that matched.
func (r sqliteRefusalReport) found() string {
	if r.templated {
		return fmt.Sprintf("the lint log template printed %q for the refused file alone", r.log)
	}
	return fmt.Sprintf("stdout reports %q with that answer", strings.TrimSpace(sqliteReplayErrorPrefix))
}

// sqliteRefusalReportFor works out the report Atlas CE prints when the lint
// run plan describes stops at refusal. An error means the harness cannot work
// it out, and the run is not judged a match.
func sqliteRefusalReportFor(
	runtime *txtarRuntime,
	plan txtarMigrateLintPlan,
	refusal sqliteEngineRefusal,
) (sqliteRefusalReport, error) {
	report := sqliteRefusalReport{answer: refusal.reason}
	text, ok, err := txtarMigrateLintTemplate(runtime, plan.cliArgs)
	if err != nil || !ok {
		return report, err
	}
	log, err := renderLintLog(text, path.Base(refusal.file))
	if err != nil {
		return sqliteRefusalReport{}, err
	}
	report.templated = true
	report.log = log
	return report, nil
}

// txtarMigrateLintTemplate returns the lint log template a `migrate lint` run
// selects, resolved as the pinned Atlas CE resolves it: --format on the command
// line, else, when --env names an env, that env's lint log and then the log of
// the project's global lint block. The project file is read only for --env.
// ok is false when no template applies and the default text report is
// printed. A project file the harness cannot read that far is an error, never
// the default report.
func txtarMigrateLintTemplate(runtime *txtarRuntime, args []string) (text string, ok bool, err error) {
	if format, set := txtarFlagValue(args, "--format"); set {
		return format, true, nil
	}
	env := txtarMigrateCommandEnv(args)
	if env == "" {
		return "", false, nil
	}
	name := "atlas.hcl"
	if config, set := txtarFlagValue(args, "--config", "-c"); set {
		name = txtarFileURLPath(config)
	}
	data, present := runtime.files[name]
	if !present {
		return "", false, fmt.Errorf("--env %s selects an env, but the fixture has no %s", env, name)
	}
	file, diags := hclsyntax.ParseConfig([]byte(data), name, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return "", false, fmt.Errorf("parse %s: %s", name, oneLine(diags.Error()))
	}
	project, isBody := file.Body.(*hclsyntax.Body)
	if !isBody {
		return "", false, fmt.Errorf("%s has no HCL body", name)
	}
	envBody, found := hclBlockBody(project, "env", env)
	if !found {
		return "", false, fmt.Errorf("%s defines no env %q", name, env)
	}
	// An env format block can set the lint template too. The harness does
	// not resolve it, so it says so rather than reporting the default.
	if _, set := hclBlockBody(envBody, "format"); set {
		return "", false, fmt.Errorf("env %q has a format block, which this harness does not read", env)
	}
	for _, scope := range []*hclsyntax.Body{envBody, project} {
		lint, set := hclBlockBody(scope, "lint")
		if !set {
			continue
		}
		attr, set := lint.Attributes["log"]
		if !set {
			continue
		}
		value, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || value.Type() != cty.String || value.IsNull() {
			return "", false, fmt.Errorf("the lint log of %s is not a literal string", name)
		}
		return value.AsString(), true, nil
	}
	return "", false, nil
}

// hclBlockBody returns the body of the one block of type kind in body whose
// labels are labels.
func hclBlockBody(body *hclsyntax.Body, kind string, labels ...string) (*hclsyntax.Body, bool) {
	for _, block := range body.Blocks {
		if block.Type == kind && slices.Equal(block.Labels, labels) {
			return block.Body, true
		}
	}
	return nil, false
}

// txtarFlagValue returns the value of the first of names in args, spelled as
// two fields or as name=value.
func txtarFlagValue(args []string, names ...string) (string, bool) {
	for i, arg := range args {
		for _, name := range names {
			switch {
			case arg == name && i+1 < len(args):
				return args[i+1], true
			case strings.HasPrefix(arg, name+"="):
				return strings.TrimPrefix(arg, name+"="), true
			}
		}
	}
	return "", false
}

// renderLintLog renders text over a .Files holding only the file named name,
// which is what Atlas CE hands a lint log template when its replay stops in
// that file. The data carries the file's name and nothing else, and a key it
// lacks is an error, so a template that reads more than the name is refused
// rather than guessed.
func renderLintLog(text, name string) (string, error) {
	tmpl, err := template.New("log").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse the lint log template: %w", err)
	}
	data := map[string]any{"Files": []map[string]any{{"Name": name}}}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
