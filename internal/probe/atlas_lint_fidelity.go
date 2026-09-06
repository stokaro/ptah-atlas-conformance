package probe

import (
	"fmt"
	"slices"
	"strings"
	"testing/fstest"

	"ptah.run/migration/lint"
)

// lintFidelityFile is the single change file used by the cross-cutting checks.
const lintFidelityFile = "0000000001_x.up.sql"

// lintFidelityBehaviorChecks exercises the analyzer behaviors CI consumers
// depend on beyond per-concern coverage: inline suppression, configuration-driven
// disable and severity override, and line attribution. Each is an enforced
// assertion — if Ptah's behavior regresses (a removed suppression path, an
// ignored disable, a lost line number), its row turns red, which is exactly the
// "deliberately removed behavior makes the gate red" guarantee this matrix owes
// automation consumers.
func lintFidelityBehaviorChecks() []Result {
	return []Result{
		lintSuppressionCheck(),
		lintDisableCheck(),
		lintSeverityOverrideCheck(),
		lintAttributionCheck(),
	}
}

// lintSingleChange lints a one-statement change file under opts and returns the
// substantive findings on it.
func lintSingleChange(sql string, opts lint.Options) ([]lint.Finding, error) {
	files := fstest.MapFS{
		lintFidelityFile:        {Data: []byte(sql)},
		"0000000001_x.down.sql": {Data: []byte("-- down\n")},
	}
	findings, err := lint.LintFS(files, opts)
	if err != nil {
		return nil, err
	}
	return substantiveChangeFindings(findings, lintFidelityFile), nil
}

func lintFidelityFail(fixture, detail string) Result {
	return Result{"lint-analyzer-catalog", fixture, "lint", Fail, detail, "stokaro/ptah#270"}
}

func lintFidelityOK(fixture, detail string) Result {
	return Result{"lint-analyzer-catalog", fixture, "lint", OK, detail, ""}
}

// lintSuppressionCheck asserts that an inline `-- ptah:nolint <code>` directive
// removes the finding it names, and that the same change without the directive
// still fires it (so the check cannot pass by accident).
func lintSuppressionCheck() Result {
	const fixture = "fidelity: inline suppression"
	base, err := lintSingleChange("DROP TABLE t;", lint.Options{})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error on the baseline: "+oneLine(err.Error()))
	}
	suppressed, err := lintSingleChange("-- ptah:nolint DS101\nDROP TABLE t;", lint.Options{})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error on the suppressed change: "+oneLine(err.Error()))
	}
	if !slices.Contains(findingCodes(base), "DS101") {
		return lintFidelityFail(fixture, "baseline broken: expected DS101 on DROP TABLE, got "+joinOrNone(findingCodes(base)))
	}
	if slices.Contains(findingCodes(suppressed), "DS101") {
		return lintFidelityFail(fixture, "`-- ptah:nolint DS101` did not suppress the DS101 finding")
	}
	return lintFidelityOK(fixture, "`-- ptah:nolint DS101` removes the DS101 finding that fires without it")
}

// lintDisableCheck asserts that Options.Disabled silences a rule by code.
func lintDisableCheck() Result {
	const fixture = "fidelity: config disable"
	on, err := lintSingleChange("DROP TABLE t;", lint.Options{})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error with the rule enabled: "+oneLine(err.Error()))
	}
	off, err := lintSingleChange("DROP TABLE t;", lint.Options{Disabled: []string{"DS101"}})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error with the rule disabled: "+oneLine(err.Error()))
	}
	if !slices.Contains(findingCodes(on), "DS101") {
		return lintFidelityFail(fixture, "baseline broken: expected DS101 on DROP TABLE, got "+joinOrNone(findingCodes(on)))
	}
	if slices.Contains(findingCodes(off), "DS101") {
		return lintFidelityFail(fixture, "Disabled:[DS101] did not silence the DS101 finding")
	}
	return lintFidelityOK(fixture, "Options.Disabled:[DS101] silences the DS101 finding that fires without it")
}

// lintSeverityOverrideCheck asserts that a per-code RuleConfig.Severity override
// changes a finding's severity. It pins the un-overridden default to error first,
// so the check cannot pass vacuously if DS101's default ever becomes warning (the
// override would then be a no-op and this must still go red).
func lintSeverityOverrideCheck() Result {
	const fixture = "fidelity: config severity override"
	base, err := lintSingleChange("DROP TABLE t;", lint.Options{})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error on the baseline: "+oneLine(err.Error()))
	}
	baseFinding := findByCode(base, "DS101")
	if baseFinding == nil {
		return lintFidelityFail(fixture, "baseline broken: expected DS101 on DROP TABLE, got "+joinOrNone(findingCodes(base)))
	}
	if baseFinding.Severity != lint.SeverityError {
		return lintFidelityFail(fixture, fmt.Sprintf("baseline broken: DS101 default severity is %q, want error (the override test needs a non-warning default)", baseFinding.Severity))
	}
	overridden, err := lintSingleChange("DROP TABLE t;", lint.Options{
		RuleConfigs: map[string]lint.RuleConfig{"DS101": {Severity: lint.SeverityWarning}},
	})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error with the override: "+oneLine(err.Error()))
	}
	f := findByCode(overridden, "DS101")
	if f == nil {
		return lintFidelityFail(fixture, "override silenced DS101 entirely; expected it at warning")
	}
	if f.Severity != lint.SeverityWarning {
		return lintFidelityFail(fixture, fmt.Sprintf("severity override ignored: DS101 reported %q, want warning", f.Severity))
	}
	return lintFidelityOK(fixture, "RuleConfig{DS101: warning} lowers DS101 from its default error severity to warning")
}

// lintAttributionCheck asserts that a finding is attributed to the line of the
// offending statement, not the file head (the statement here sits on line 3).
func lintAttributionCheck() Result {
	const fixture = "fidelity: line attribution"
	findings, err := lintSingleChange("-- a comment\n\nDROP TABLE t;\n", lint.Options{})
	if err != nil {
		return lintFidelityFail(fixture, "LintFS error: "+oneLine(err.Error()))
	}
	f := findByCode(findings, "DS101")
	if f == nil {
		return lintFidelityFail(fixture, "baseline broken: expected DS101 on DROP TABLE, got "+joinOrNone(findingCodes(findings)))
	}
	if f.Line != 3 {
		return lintFidelityFail(fixture, fmt.Sprintf("attribution wrong: DS101 reported line %d, want 3", f.Line))
	}
	return lintFidelityOK(fixture, "DS101 is attributed to line 3, the offending statement's line, not the file head")
}

func findByCode(findings []lint.Finding, code string) *lint.Finding {
	for i := range findings {
		if findings[i].Rule == code {
			return &findings[i]
		}
	}
	return nil
}

func joinOrNone(codes []string) string {
	if len(codes) == 0 {
		return "(none)"
	}
	return strings.Join(codes, ", ")
}
