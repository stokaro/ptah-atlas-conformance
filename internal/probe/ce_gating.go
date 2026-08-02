package probe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The CE gating tier executes the pinned Atlas CE binary, logged out, through
// a fixed scenario table covering the capabilities Ptah's feature matrix
// asserts about the CE column, and classifies each observed outcome. The
// expected classes encode the hand-measured 2026-08-01 baseline for Atlas CE
// v1.2.0, re-confirmed unchanged against Atlas CE v1.3.0 on 2026-08-02; a
// renovate bump of atlas.version that changes gating behavior turns the gate
// red instead of silently invalidating the matrix.
//
// The v1.3.0 re-measurement found ZERO class changes in either direction: the
// generated report was byte-identical apart from its header version line. That
// is recorded here because "the pin moved and nothing moved with it" is only
// trustworthy if someone wrote down that it was actually re-measured rather
// than assumed.

// CEGatingClass partitions the observed behavior of one Atlas CE invocation.
type CEGatingClass string

const (
	// CEGatingWorks — the command ran and met its scenario-specific success
	// predicate (exit 0, or for lint an exit-1 run that reported findings).
	CEGatingWorks CEGatingClass = "works"
	// CEGatingCommunityAbort — the verb or flag is registered but refuses with
	// "Abort: '...' is not supported by the community version".
	CEGatingCommunityAbort CEGatingClass = "community-abort"
	// CEGatingAbsent — the named subcommand does not exist: the parent group
	// help is printed and the process exits 0.
	CEGatingAbsent CEGatingClass = "absent"
	// CEGatingUnregisteredCommand — the command name is not registered at all
	// under its parent: cobra reports `unknown command "x" for "atlas"` and the
	// process exits 1. Distinct from absent (a name under a registered group,
	// which yields that group's help at exit 0) and from community-abort (a
	// name that IS registered and refuses as a Pro stub). Keeping the three
	// apart is what makes "this verb is missing" a measurement rather than a
	// reading of the help listing.
	CEGatingUnregisteredCommand CEGatingClass = "unregistered-command"
	// CEGatingUnknownFlag — the flag is not registered at all ("unknown flag:").
	CEGatingUnknownFlag CEGatingClass = "unknown-flag"
	// CEGatingNamedError — the command failed with the scenario's specific,
	// named error (for example a missing data source handler).
	CEGatingNamedError CEGatingClass = "named-error"
	// CEGatingSilentUnenforced — the dangerous class: the Pro-gated construct
	// was accepted without diagnostics and had no enforced effect.
	CEGatingSilentUnenforced CEGatingClass = "silent-unenforced"
	// CEGatingUnclassified — the observation matched no known class; always a
	// gap against the measured baseline.
	CEGatingUnclassified CEGatingClass = "unclassified"
)

// ceGatingClassOrder fixes the reporting order of classes in summaries.
var ceGatingClassOrder = []CEGatingClass{
	CEGatingWorks,
	CEGatingCommunityAbort,
	CEGatingAbsent,
	CEGatingUnregisteredCommand,
	CEGatingUnknownFlag,
	CEGatingNamedError,
	CEGatingSilentUnenforced,
	CEGatingUnclassified,
}

// CEGatingClassOrder returns the fixed class reporting order used by run
// summaries, most-capable class first.
func CEGatingClassOrder() []CEGatingClass {
	return append([]CEGatingClass(nil), ceGatingClassOrder...)
}

var (
	// ceCommunityAbortPattern matches the registered community-version stub
	// refusal, for both gated verbs and gated flags.
	ceCommunityAbortPattern = regexp.MustCompile(`Abort: '.*' is not supported by the community version`)
	// ceUnknownFlagPattern matches cobra's unregistered-flag error.
	ceUnknownFlagPattern = regexp.MustCompile(`unknown flag: `)
	// ceUnknownCommandPattern matches cobra's unregistered-command error, which
	// the binary emits (exit 1) for a name it never registered under its
	// parent. No registered Atlas verb can produce this line, so the pattern is
	// global like the two above rather than scenario-gated.
	ceUnknownCommandPattern = regexp.MustCompile(`unknown command "[^"]*" for `)
)

// ceParentHelpFragments identify the `atlas migrate` / `atlas schema` parent
// group help, which the pinned binary prints (exit 0) for never-registered
// subcommand names.
var ceParentHelpFragments = []string{
	"wraps several sub-commands",
	"groups subcommands",
}

// CEGatingRules are the scenario-specific knobs ClassifyCEGating consults on
// top of the fixed global community-abort and unknown-flag patterns.
type CEGatingRules struct {
	// AbsentCommandPath, when non-empty, is the full command path the argv
	// named under a parent group (e.g. "atlas migrate ls"), enabling the
	// absent (parent-help, exit 0) classification. The first help line must
	// not name the attempted path itself: if a future Atlas registers the
	// name as its own command group, that group's help has the same
	// "wraps several sub-commands" shape but leads with the attempted path,
	// and must not pass as absent.
	AbsentCommandPath string
	// SuccessExit is the exit code that counts as works. The zero value keeps
	// the common exit-0 contract; migrate lint sets 1 because reporting
	// findings is the working state.
	SuccessExit int
	// SuccessFragments must all appear in the combined output for works.
	SuccessFragments []string
	// SuccessAbsentFragments must NOT appear in the combined output for works.
	//
	// This exists because CE's indifference and CE's support are otherwise
	// indistinguishable. CE silently drops HCL constructs it does not know, so
	// a Pro-only construct still yields exit 0 and a plausible-looking result;
	// only the *absence* of its effect in the emitted DDL proves it was
	// ignored rather than honored. A scenario asserting exit 0 alone would be
	// read backwards by the next person as "CE supports this". Pair every such
	// row with a nonsense-construct control asserting the identical shape.
	SuccessAbsentFragments []string
	// NamedErrorPattern classifies matching non-zero-exit output as
	// named-error.
	NamedErrorPattern *regexp.Regexp
	// SilentWhenExitZero enables the silent-unenforced classification: the
	// scenario is constructed so that a clean exit 0 (with SilentFragments
	// present) proves the Pro-gated construct was accepted but not enforced.
	SilentWhenExitZero bool
	// SilentFragments must all appear in the combined output for
	// silent-unenforced.
	SilentFragments []string
	// SilentAbsentFragments must NOT appear in the combined output for
	// silent-unenforced. Same rationale as SuccessAbsentFragments: the class
	// asserts that a construct had NO enforced effect, and only the absence of
	// the enforcement's own diagnostics can carry that claim.
	SilentAbsentFragments []string
}

// ClassifyCEGating is a pure function classifying one observed Atlas CE
// invocation from its exit code and combined stdout+stderr output. It returns
// the class and a deterministic one-line observed summary suitable for a
// committed report (matched output lines for error classes, derived exit and
// fragment facts otherwise, never timings or temp paths).
func ClassifyCEGating(rules CEGatingRules, exitCode int, output string) (CEGatingClass, string) {
	if line, ok := firstMatchingLine(output, ceCommunityAbortPattern); ok {
		return CEGatingCommunityAbort, line
	}
	if line, ok := firstMatchingLine(output, ceUnknownFlagPattern); ok {
		return CEGatingUnknownFlag, line
	}
	if line, ok := firstMatchingLine(output, ceUnknownCommandPattern); ok {
		return CEGatingUnregisteredCommand, line
	}
	if rules.NamedErrorPattern != nil && exitCode != 0 {
		if line, ok := firstMatchingLine(output, rules.NamedErrorPattern); ok {
			return CEGatingNamedError, line
		}
	}
	if rules.AbsentCommandPath != "" && exitCode == 0 &&
		containsAnyFragment(output, ceParentHelpFragments) &&
		!strings.Contains(firstNonEmptyLine(output), rules.AbsentCommandPath) {
		return CEGatingAbsent, "exit 0; the parent group help was printed instead of running the named subcommand"
	}
	if rules.SilentWhenExitZero && exitCode == 0 && containsAllFragments(output, rules.SilentFragments) &&
		!containsAnyFragment(output, rules.SilentAbsentFragments) {
		return CEGatingSilentUnenforced,
			observedWorksSummary(exitCode, rules.SilentFragments, rules.SilentAbsentFragments)
	}
	if exitCode == rules.SuccessExit && containsAllFragments(output, rules.SuccessFragments) &&
		!containsAnyFragment(output, rules.SuccessAbsentFragments) {
		return CEGatingWorks, observedWorksSummary(exitCode, rules.SuccessFragments, rules.SuccessAbsentFragments)
	}
	return CEGatingUnclassified, fmt.Sprintf("exit %d: %s", exitCode, oneLine(firstNonEmptyLine(output)))
}

func firstMatchingLine(output string, pattern *regexp.Regexp) (string, bool) {
	for line := range strings.Lines(output) {
		if pattern.MatchString(line) {
			return oneLine(strings.TrimSpace(line)), true
		}
	}
	return "", false
}

func firstNonEmptyLine(output string) string {
	for line := range strings.Lines(output) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return "(no output)"
}

func containsAnyFragment(output string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			return true
		}
	}
	return false
}

func containsAllFragments(output string, fragments []string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			return false
		}
	}
	return true
}

// observedExitSummary derives a deterministic summary from the verified exit
// code and output fragments, so OK report rows never embed timings or paths.
func observedExitSummary(exitCode int, fragments []string) string {
	if len(fragments) == 0 {
		return fmt.Sprintf("exit %d", exitCode)
	}
	quoted := make([]string, len(fragments))
	for i, fragment := range fragments {
		quoted[i] = fmt.Sprintf("%q", fragment)
	}
	return fmt.Sprintf("exit %d; output contains %s", exitCode, strings.Join(quoted, ", "))
}

// observedWorksSummary extends observedExitSummary with the verified-absent
// fragments, so a committed row that depends on an absence says so out loud.
// Without this the report would show a bare "exit 0" for a scenario whose
// whole point is what the output does NOT contain.
func observedWorksSummary(exitCode int, present, absent []string) string {
	summary := observedExitSummary(exitCode, present)
	if len(absent) == 0 {
		return summary
	}
	quoted := make([]string, len(absent))
	for i, fragment := range absent {
		quoted[i] = fmt.Sprintf("%q", fragment)
	}
	return fmt.Sprintf("%s; output does not contain %s", summary, strings.Join(quoted, ", "))
}

// CEGatingScenario is the read-only public view of one fixed scenario: its
// report fixture label, the measured argv, and the expected baseline class.
type CEGatingScenario struct {
	Fixture  string
	Argv     []string
	Expected CEGatingClass
}

// CEGatingScenarioTable returns the fixed scenario table encoding the measured
// 2026-08-01 Atlas CE v1.2.0 gating baseline, re-confirmed against v1.3.0 on
// 2026-08-02.
func CEGatingScenarioTable() []CEGatingScenario {
	scenarios := ceGatingScenarios()
	table := make([]CEGatingScenario, len(scenarios))
	for i, s := range scenarios {
		table[i] = CEGatingScenario{
			Fixture:  s.fixture,
			Argv:     append([]string(nil), s.argv...),
			Expected: s.expected,
		}
	}
	return table
}

// CEGatingRun is the outcome of executing the fixed scenario table against
// one Atlas CE binary.
type CEGatingRun struct {
	// Results holds one report row per scenario.
	Results []Result
	// Observed counts the observed class per scenario; harness failures
	// (scenario could not run) are not counted.
	Observed map[CEGatingClass]int
}

// RunCEGating executes every fixed CE gating scenario against atlasBin. Each
// scenario runs in its own scratch directory with a scratch HOME,
// XDG_CONFIG_HOME, and XDG_DATA_HOME plus ATLAS_NO_UPDATE_NOTIFIER=1 and
// ATLAS_NO_ANON_TELEMETRY=1, so the measurement is always logged out — a
// developer's real Atlas login cannot leak in.
func RunCEGating(atlasBin string) CEGatingRun {
	run := CEGatingRun{Observed: map[CEGatingClass]int{}}
	bin := resolveCEGatingBinary(atlasBin)
	for _, s := range ceGatingScenarios() {
		result, class := s.execute(bin)
		run.Results = append(run.Results, result)
		if class != "" {
			run.Observed[class]++
		}
	}
	return run
}

// resolveCEGatingBinary resolves atlasBin before any scenario changes the
// working directory. Bare command names go through exec.LookPath so the
// documented `atlas`-on-PATH fallback keeps working; path-shaped values are
// made absolute so per-scenario work dirs cannot re-resolve them. An
// unresolvable name is returned unchanged, letting exec report the honest
// not-found error per scenario.
func resolveCEGatingBinary(atlasBin string) string {
	if !strings.ContainsRune(atlasBin, os.PathSeparator) {
		if found, err := exec.LookPath(atlasBin); err == nil {
			return found
		}
		return atlasBin
	}
	if abs, err := filepath.Abs(atlasBin); err == nil {
		return abs
	}
	return atlasBin
}

// CEGatingAtlasVersion reports the first line of `atlas version` for the
// binary under test, executed under the same scrubbed logged-out environment
// as every scenario so a developer's ambient Atlas state cannot color the
// committed report header.
func CEGatingAtlasVersion(atlasBin string) (string, error) {
	rt, err := newCEGatingRuntime(resolveCEGatingBinary(atlasBin))
	if err != nil {
		return "", err
	}
	defer rt.cleanup()
	result, err := rt.runAtlas("version")
	if err != nil {
		return "", fmt.Errorf("execute `atlas version`: %w", err)
	}
	if result.exitCode != 0 {
		return "", fmt.Errorf("`atlas version` exit code %d: %s", result.exitCode, result.diagnostic())
	}
	line := strings.TrimSpace(strings.SplitN(result.stdout, "\n", 2)[0])
	if line == "" {
		return "", fmt.Errorf("`atlas version` produced no output")
	}
	return line, nil
}

// ceGatingScenario is the internal scenario shape: the public view plus the
// fixture setup and the classifier knobs.
type ceGatingScenario struct {
	fixture string
	// setup prepares fixtures inside the scenario scratch directory and may
	// run unmeasured harness commands (hashing, base applies, row inserts).
	setup func(rt *ceGatingRuntime) error
	// argv is the measured Atlas invocation, without the leading binary token.
	argv     []string
	expected CEGatingClass
	rules    CEGatingRules
}

func (s ceGatingScenario) execute(atlasBin string) (Result, CEGatingClass) {
	rt, err := newCEGatingRuntime(atlasBin)
	if err != nil {
		return ceGatingHarnessFailure(s.fixture, "scenario setup", err), ""
	}
	defer rt.cleanup()
	if s.setup != nil {
		if err := s.setup(rt); err != nil {
			return ceGatingHarnessFailure(s.fixture, "scenario setup", err), ""
		}
	}
	command, err := rt.runAtlas(s.argv...)
	if err != nil {
		return ceGatingHarnessFailure(s.fixture, "measured command", fmt.Errorf(
			"execute `atlas %s`: %w; %s", strings.Join(s.argv, " "), err, command.diagnostic())), ""
	}
	observed, summary := ClassifyCEGating(s.rules, command.exitCode, command.stdout+"\n"+command.stderr)
	if observed == s.expected {
		return Result{
			Probe:   "ce-gating",
			Fixture: s.fixture,
			Stage:   string(s.expected),
			Outcome: OK,
			Detail:  fmt.Sprintf("class: %s — %s", observed, summary),
		}, observed
	}
	return Result{
		Probe:   "ce-gating",
		Fixture: s.fixture,
		Stage:   string(s.expected),
		Outcome: Gap,
		Detail: fmt.Sprintf("expected class %s, observed %s — %s: %s",
			s.expected, observed, summary, command.diagnostic()),
	}, observed
}

func ceGatingHarnessFailure(fixture, stage string, err error) Result {
	return Result{
		Probe:   "ce-gating",
		Fixture: fixture,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}

// ceGatingRuntime is one scenario's isolated execution environment.
type ceGatingRuntime struct {
	atlasBin string
	scratch  string
	workDir  string
	env      []string
}

func newCEGatingRuntime(atlasBin string) (*ceGatingRuntime, error) {
	scratch, err := os.MkdirTemp("", "ptah-ce-gating-*")
	if err != nil {
		return nil, fmt.Errorf("create scenario scratch dir: %w", err)
	}
	rt := &ceGatingRuntime{atlasBin: atlasBin, scratch: scratch}
	subdirs := map[string]string{
		"HOME":            "home",
		"XDG_CONFIG_HOME": "xdg-config",
		"XDG_DATA_HOME":   "xdg-data",
		"TMPDIR":          "tmp",
	}
	// Deterministic env order keeps runs reproducible.
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "TMPDIR"} {
		dir := filepath.Join(scratch, subdirs[name])
		if err := os.MkdirAll(dir, 0o700); err != nil {
			rt.cleanup()
			return nil, fmt.Errorf("create scenario %s dir: %w", name, err)
		}
		rt.env = append(rt.env, name+"="+dir)
	}
	rt.workDir = filepath.Join(scratch, "work")
	if err := os.MkdirAll(rt.workDir, 0o700); err != nil {
		rt.cleanup()
		return nil, fmt.Errorf("create scenario work dir: %w", err)
	}
	rt.env = append(rt.env,
		"ATLAS_NO_UPDATE_NOTIFIER=1",
		"ATLAS_NO_ANON_TELEMETRY=1",
	)
	if path := os.Getenv("PATH"); path != "" {
		rt.env = append(rt.env, "PATH="+path)
	}
	return rt, nil
}

func (rt *ceGatingRuntime) cleanup() {
	_ = os.RemoveAll(rt.scratch)
}

// runAtlas runs the Atlas binary in the scenario work directory under the
// scrubbed logged-out environment. A non-zero exit code is a completed
// observation, not an error; the error return is reserved for harness-level
// failures (binary missing, timeout).
func (rt *ceGatingRuntime) runAtlas(args ...string) (ptahCommandResult, error) {
	return runCommandHermetic(rt.atlasBin, args, rt.workDir, rt.env)
}

// mustRunAtlas runs an unmeasured harness step that has to succeed before the
// measured command is meaningful.
func (rt *ceGatingRuntime) mustRunAtlas(args ...string) error {
	result, err := rt.runAtlas(args...)
	if err != nil {
		return fmt.Errorf("harness `atlas %s`: %w; %s", strings.Join(args, " "), err, result.diagnostic())
	}
	if result.exitCode != 0 {
		return fmt.Errorf("harness `atlas %s`: exit code %d: %s",
			strings.Join(args, " "), result.exitCode, result.diagnostic())
	}
	return nil
}

// writeFile writes one fixture file below the scenario work directory.
func (rt *ceGatingRuntime) writeFile(name, content string) error {
	path := filepath.Join(rt.workDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create fixture dir for %s: %w", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write fixture %s: %w", name, err)
	}
	return nil
}

// mkdir creates one directory below the scenario work directory.
func (rt *ceGatingRuntime) mkdir(name string) error {
	if err := os.MkdirAll(filepath.Join(rt.workDir, name), 0o700); err != nil {
		return fmt.Errorf("create fixture dir %s: %w", name, err)
	}
	return nil
}

// execSQLite executes one statement against a SQLite database file in the
// scenario work directory, using the same driver the rest of the probe
// package uses. Scenarios use it to establish data states (for example a row
// that violates an upcoming pre-migration check) without involving Atlas.
func (rt *ceGatingRuntime) execSQLite(dbFile, statement string) error {
	db, err := openSQLiteRuntimeDB(filepath.Join(rt.workDir, dbFile))
	if err != nil {
		return fmt.Errorf("open scenario SQLite database %s: %w", dbFile, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(statement); err != nil {
		return fmt.Errorf("execute scenario SQL on %s: %w", dbFile, err)
	}
	return nil
}

// Shared fixture content. The migration corpus is deliberately tiny: two
// versions where the second is destructive, so one directory serves the hash,
// lint, apply, and status scenarios.
const (
	ceGatingSQLiteDevURL = "sqlite://file?mode=memory"

	ceGatingInitMigration = "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL);\n" +
		"CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n"
	ceGatingDropMigration = "DROP TABLE widgets;\n"
	// ceGatingPendingMigration is applied AFTER drift is injected, so the
	// drift-check scenarios have real work for `migrate apply` to do. Without
	// a pending version, apply would exit 0 for the uninteresting reason that
	// there was nothing to run.
	ceGatingPendingMigration = "CREATE TABLE orders (id INTEGER PRIMARY KEY);\n"
	// ceGatingDriftInjectionSQL is out-of-band schema change no migration in
	// the corpus produces: a table the directory never creates plus a column
	// the directory never adds. This is the drift a pre-apply drift check is
	// supposed to catch.
	ceGatingDriftInjectionSQL = "CREATE TABLE rogue_drift (x INTEGER);\n" +
		"ALTER TABLE users ADD COLUMN injected TEXT;\n"

	ceGatingFromHCL = `schema "main" {}
table "users" {
  schema = schema.main
  column "id" {
    type = int
  }
  primary_key {
    columns = [column.id]
  }
}
`
	ceGatingDesiredHCL = `schema "main" {}
table "users" {
  schema = schema.main
  column "id" {
    type = int
  }
  column "email" {
    type = text
  }
  primary_key {
    columns = [column.id]
  }
}
`
	// ceGatingEmptySchemaHCL is the empty baseline the unknown-construct
	// scenarios diff FROM, so the emitted DDL is a single CREATE TABLE and the
	// absent-fragment assertions have an unambiguous target.
	ceGatingEmptySchemaHCL = `schema "main" {}
`
	// ceGatingInvisibleColumnHCL carries the v1.3.0-announced MySQL invisible
	// column attribute. CE emits the column with no INVISIBLE keyword.
	ceGatingInvisibleColumnHCL = `schema "main" {}
table "t" {
  schema = schema.main
  column "id" {
    type = int
  }
  column "secret" {
    type = int
    invisible = true
  }
}
`
	// ceGatingNonsenseColumnAttrHCL is the control for the row above: a column
	// attribute that cannot possibly be supported. CE treats it identically,
	// which is what proves `invisible` is ignored rather than honored.
	ceGatingNonsenseColumnAttrHCL = `schema "main" {}
table "t" {
  schema = schema.main
  column "id" {
    type = int
  }
  column "secret" {
    type = int
    zzz_nonsense_attr = true
  }
}
`
	// ceGatingAnnotationHCL carries the v1.3.0-announced Schema Annotations
	// blocks. CE drops both the declaration and the usage.
	ceGatingAnnotationHCL = `schema "main" {}
annotation "gql" {
  attr "name" {
    type = string
  }
}
table "t" {
  schema = schema.main
  column "id" {
    type = int
  }
  annotation {
    gql = "Thing"
  }
}
`
	// ceGatingDriftCheckConfig carries the v1.3.0-announced pre-apply drift
	// check. CE parses the file, ignores the block, and proceeds.
	ceGatingDriftCheckConfig = `check "migrate_apply" {
  drift {
    on_error = "FAIL"
  }
}
env "local" {
  url = "sqlite://app.db"
}
`
	// ceGatingNonsenseBlockConfig is the control for the row above: an
	// atlas.hcl top-level block that cannot possibly be supported. CE ignores
	// it exactly as it ignores `check`, proving the indifference is a general
	// policy rather than a check-shaped allowance.
	ceGatingNonsenseBlockConfig = `frobnicate_nonsense "zzz" {
  totally_made_up = "yes"
}
env "local" {
  url = "sqlite://app.db"
}
`
	// ceGatingRoledHCL is ceGatingDesiredHCL plus a Pro-gated role block. On a
	// database already at the desired state, applying it must expose whether
	// CE enforces, rejects, or silently drops the role.
	ceGatingRoledHCL = `schema "main" {}
role "app" {
}
table "users" {
  schema = schema.main
  column "id" {
    type = int
  }
  column "email" {
    type = text
  }
  primary_key {
    columns = [column.id]
  }
}
`
	// ceGatingCheckedMigration is an Atlas txtar-format migration whose
	// checks.sql assertion fails once users contains a row. A checks-enforcing
	// binary must refuse to apply it; the pinned CE binary runs the check as
	// an ordinary statement and applies anyway.
	ceGatingCheckedMigration = `-- atlas:txtar

-- checks.sql --
SELECT NOT EXISTS (SELECT 1 FROM users);

-- migration.sql --
CREATE TABLE audit (id INTEGER PRIMARY KEY, note TEXT NOT NULL);
`
	ceGatingCompositeConfig = `data "composite_schema" "app" {
  schema "main" {
    url = "file://part.hcl"
  }
}

env "dev" {
  src = data.composite_schema.app.url
  url = data.composite_schema.app.url
  dev = "` + ceGatingSQLiteDevURL + `"
}
`
	// The program is never executed: CE rejects the data source before any
	// external process could run, so the referenced script does not need to
	// exist or be executable.
	ceGatingExternalSchemaConfig = `data "external_schema" "app" {
  program = ["./gen.sh"]
}

env "dev" {
  src = data.external_schema.app.url
  url = "sqlite://ext.db"
  dev = "` + ceGatingSQLiteDevURL + `"
}
`
)

var ceCompositeSchemaErrorPattern = regexp.MustCompile(`missing data source handler for "composite_schema"`)

// Unlike the verb stubs' "Abort: '...' is not supported by the community
// version." sentence, the external_schema rejection is an Error line with its
// own phrasing, so it is pinned as a named error rather than community-abort.
var ceExternalSchemaErrorPattern = regexp.MustCompile(`data\.external_schema is not supported by the community version`)

// setupCEGatingMigrations writes the two-version migration directory and, when
// hash is set, integrity-hashes it through the real `atlas migrate hash` verb.
func setupCEGatingMigrations(rt *ceGatingRuntime, hash bool) error {
	if err := rt.writeFile("migrations/20260101000001_init.sql", ceGatingInitMigration); err != nil {
		return err
	}
	if err := rt.writeFile("migrations/20260101000002_drop_widgets.sql", ceGatingDropMigration); err != nil {
		return err
	}
	if !hash {
		return nil
	}
	return rt.mustRunAtlas("migrate", "hash", "--dir", "file://migrations")
}

// setupCEGatingCheckedState prepares the pre-migration-check scenario: an
// Atlas-format directory whose second migration is check-guarded, the first
// migration applied, and a users row inserted so the pending checks.sql
// assertion is false.
func setupCEGatingCheckedState(rt *ceGatingRuntime) error {
	if err := rt.writeFile("checked/20260101000001_create_users.sql",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL);\n"); err != nil {
		return err
	}
	if err := rt.writeFile("checked/20260101000002_guarded_audit.sql", ceGatingCheckedMigration); err != nil {
		return err
	}
	if err := rt.mustRunAtlas("migrate", "hash", "--dir", "file://checked"); err != nil {
		return err
	}
	if err := rt.mustRunAtlas("migrate", "apply", "1", "--dir", "file://checked", "--url", "sqlite://checked.db"); err != nil {
		return err
	}
	return rt.execSQLite("checked.db", "INSERT INTO users (id, email) VALUES (1, 'a@b.c')")
}

// setupCEGatingDeclarativeState applies the desired schema so the database is
// already synced before the measured declarative scenario runs.
func setupCEGatingDeclarativeState(rt *ceGatingRuntime, extraFiles map[string]string) error {
	if err := rt.writeFile("desired.hcl", ceGatingDesiredHCL); err != nil {
		return err
	}
	for name, content := range extraFiles {
		if err := rt.writeFile(name, content); err != nil {
			return err
		}
	}
	return rt.mustRunAtlas("schema", "apply",
		"--url", "sqlite://declarative.db",
		"--to", "file://desired.hcl",
		"--dev-url", ceGatingSQLiteDevURL,
		"--auto-approve")
}

// ceGatingScenarios is the fixed scenario table. Expected classes encode the
// hand-measured 2026-08-01 baseline for the pinned Atlas CE v1.2.0 binary,
// re-confirmed unchanged against v1.3.0 on 2026-08-02, with the v1.3.0 rows
// and the control set added at that point; do not edit an expectation without
// re-measuring against the real binary.
//
// Rows whose fixture name starts with "control:" are not capability
// assertions — they are the reference shapes that make the capability rows
// legible. Do not delete one because it looks redundant: each exists because
// its subject row is otherwise ambiguous between "CE supports this" and "CE
// does not know what this is".
func ceGatingScenarios() []ceGatingScenario {
	return []ceGatingScenario{
		// Working, logged out.
		{
			// Exit-0-only works predicate: `migrate hash` prints nothing on
			// success, so (with migrate diff) this is the weakest predicate in
			// the table. The verified effect — atlas.sum written — lives on
			// disk, outside what the exit-code+output classifier can see.
			fixture: "atlas migrate hash",
			setup: func(rt *ceGatingRuntime) error {
				return setupCEGatingMigrations(rt, false)
			},
			argv:     []string{"migrate", "hash", "--dir", "file://migrations"},
			expected: CEGatingWorks,
		},
		{
			fixture: "atlas migrate lint (destructive latest)",
			setup: func(rt *ceGatingRuntime) error {
				return setupCEGatingMigrations(rt, true)
			},
			argv: []string{"migrate", "lint",
				"--dir", "file://migrations", "--dev-url", ceGatingSQLiteDevURL, "--latest", "1"},
			expected: CEGatingWorks,
			rules: CEGatingRules{
				// Lint reporting findings is the working state: exit 1 with
				// diagnostics, not exit 0.
				SuccessExit:      1,
				SuccessFragments: []string{"destructive changes detected"},
			},
		},
		{
			fixture: "atlas schema diff",
			setup: func(rt *ceGatingRuntime) error {
				if err := rt.writeFile("from.hcl", ceGatingFromHCL); err != nil {
					return err
				}
				return rt.writeFile("to.hcl", ceGatingDesiredHCL)
			},
			argv: []string{"schema", "diff",
				"--from", "file://from.hcl", "--to", "file://to.hcl", "--dev-url", ceGatingSQLiteDevURL},
			expected: CEGatingWorks,
			rules:    CEGatingRules{SuccessFragments: []string{"ALTER TABLE"}},
		},
		{
			// Exit-0-only works predicate: `migrate diff` prints nothing when
			// it plans a migration, so (with migrate hash) this is the weakest
			// predicate in the table — the planned file appears on disk only.
			fixture: "atlas migrate diff",
			setup: func(rt *ceGatingRuntime) error {
				if err := rt.mkdir("planned"); err != nil {
					return err
				}
				return rt.writeFile("to.hcl", ceGatingDesiredHCL)
			},
			argv: []string{"migrate", "diff", "add_users",
				"--dir", "file://planned", "--to", "file://to.hcl", "--dev-url", ceGatingSQLiteDevURL},
			expected: CEGatingWorks,
		},
		{
			fixture: "atlas migrate apply",
			setup: func(rt *ceGatingRuntime) error {
				return setupCEGatingMigrations(rt, true)
			},
			argv:     []string{"migrate", "apply", "--dir", "file://migrations", "--url", "sqlite://app.db"},
			expected: CEGatingWorks,
			rules:    CEGatingRules{SuccessFragments: []string{"2 migrations in total"}},
		},
		{
			fixture: "atlas migrate status",
			setup: func(rt *ceGatingRuntime) error {
				if err := setupCEGatingMigrations(rt, true); err != nil {
					return err
				}
				return rt.mustRunAtlas("migrate", "apply", "--dir", "file://migrations", "--url", "sqlite://app.db")
			},
			argv:     []string{"migrate", "status", "--dir", "file://migrations", "--url", "sqlite://app.db"},
			expected: CEGatingWorks,
			rules:    CEGatingRules{SuccessFragments: []string{"Migration Status: OK"}},
		},
		{
			fixture: "atlas schema apply (declarative)",
			setup: func(rt *ceGatingRuntime) error {
				return rt.writeFile("desired.hcl", ceGatingDesiredHCL)
			},
			argv: []string{"schema", "apply",
				"--url", "sqlite://declarative.db",
				"--to", "file://desired.hcl",
				"--dev-url", ceGatingSQLiteDevURL,
				"--auto-approve"},
			expected: CEGatingWorks,
			rules:    CEGatingRules{SuccessFragments: []string{"Planned Changes"}},
		},

		// Registered community-abort stubs.
		{
			fixture:  "atlas schema push",
			argv:     []string{"schema", "push"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas schema plan --env",
			argv:     []string{"schema", "plan", "--env", "x"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas schema test",
			argv:     []string{"schema", "test"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas migrate push",
			argv:     []string{"migrate", "push", "demo"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas migrate test",
			argv:     []string{"migrate", "test"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas migrate checkpoint",
			argv:     []string{"migrate", "checkpoint"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas migrate edit",
			argv:     []string{"migrate", "edit"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas migrate rebase",
			argv:     []string{"migrate", "rebase", "1"},
			expected: CEGatingCommunityAbort,
		},
		{
			fixture:  "atlas migrate rm",
			argv:     []string{"migrate", "rm"},
			expected: CEGatingCommunityAbort,
		},
		{
			// Bare invocation only: the stub registers no flags, so with
			// --url/--dir the process dies earlier on "unknown flag" instead
			// of reaching the community gate.
			fixture:  "atlas migrate down (bare)",
			argv:     []string{"migrate", "down"},
			expected: CEGatingCommunityAbort,
		},
		{
			// The abort names the Pro-gated flag, not the verb.
			fixture: "atlas schema apply --include",
			setup: func(rt *ceGatingRuntime) error {
				return setupCEGatingDeclarativeState(rt, nil)
			},
			argv: []string{"schema", "apply",
				"--url", "sqlite://declarative.db",
				"--to", "file://desired.hcl",
				"--dev-url", ceGatingSQLiteDevURL,
				"--auto-approve",
				"--include", "users"},
			expected: CEGatingCommunityAbort,
		},

		// Never-registered verbs: parent group help, exit 0.
		{
			fixture:  "atlas migrate ls",
			argv:     []string{"migrate", "ls"},
			expected: CEGatingAbsent,
			rules:    CEGatingRules{AbsentCommandPath: "atlas migrate ls"},
		},
		{
			fixture:  "atlas migrate show",
			argv:     []string{"migrate", "show"},
			expected: CEGatingAbsent,
			rules:    CEGatingRules{AbsentCommandPath: "atlas migrate show"},
		},
		{
			fixture:  "atlas schema validate",
			argv:     []string{"schema", "validate"},
			expected: CEGatingAbsent,
			rules:    CEGatingRules{AbsentCommandPath: "atlas schema validate"},
		},
		{
			fixture:  "atlas schema stats",
			argv:     []string{"schema", "stats"},
			expected: CEGatingAbsent,
			rules:    CEGatingRules{AbsentCommandPath: "atlas schema stats"},
		},

		// Silent, unenforced behavior — the dangerous class.
		{
			fixture: "atlas schema apply (role block)",
			setup: func(rt *ceGatingRuntime) error {
				return setupCEGatingDeclarativeState(rt, map[string]string{"roled.hcl": ceGatingRoledHCL})
			},
			argv: []string{"schema", "apply",
				"--url", "sqlite://declarative.db",
				"--to", "file://roled.hcl",
				"--dev-url", ceGatingSQLiteDevURL,
				"--auto-approve"},
			expected: CEGatingSilentUnenforced,
			rules: CEGatingRules{
				// The role block produces neither an abort nor a diagnostic:
				// the apply reports a fully synced schema.
				SilentWhenExitZero: true,
				SilentFragments:    []string{"Schema is synced"},
			},
		},
		{
			fixture:  "atlas migrate apply (failing txtar checks)",
			setup:    setupCEGatingCheckedState,
			argv:     []string{"migrate", "apply", "--dir", "file://checked", "--url", "sqlite://checked.db"},
			expected: CEGatingSilentUnenforced,
			rules: CEGatingRules{
				// The failing checks.sql assertion is executed as an ordinary
				// statement and the guarded migration applies anyway.
				SilentWhenExitZero: true,
				SilentFragments:    []string{"Migrating to version 20260101000002", "SELECT NOT EXISTS"},
			},
		},

		// Named errors.
		{
			fixture: "atlas schema inspect --env (composite_schema)",
			setup: func(rt *ceGatingRuntime) error {
				if err := rt.writeFile("atlas.hcl", ceGatingCompositeConfig); err != nil {
					return err
				}
				return rt.writeFile("part.hcl", ceGatingDesiredHCL)
			},
			argv:     []string{"schema", "inspect", "--env", "dev"},
			expected: CEGatingNamedError,
			rules:    CEGatingRules{NamedErrorPattern: ceCompositeSchemaErrorPattern},
		},
		{
			fixture: "atlas schema inspect --env (external_schema)",
			setup: func(rt *ceGatingRuntime) error {
				return rt.writeFile("atlas.hcl", ceGatingExternalSchemaConfig)
			},
			argv:     []string{"schema", "inspect", "--env", "dev"},
			expected: CEGatingNamedError,
			rules:    CEGatingRules{NamedErrorPattern: ceExternalSchemaErrorPattern},
		},
		{
			fixture:  "atlas schema inspect --web",
			argv:     []string{"schema", "inspect", "--web"},
			expected: CEGatingUnknownFlag,
		},
		{
			// Atlas registers --include on this command; CE
			// does not register it at all, so it dies on the flag rather than
			// reaching a community-version abort. This is the measured
			// justification for the `schema inspect` entry in proSurfaceFlags.
			fixture:  "atlas schema inspect --include",
			argv:     []string{"schema", "inspect", "--include", "users"},
			expected: CEGatingUnknownFlag,
		},

		// --- The three-way verb control -------------------------------------
		//
		// These three rows exist as a set and must be read as a set. Together
		// they separate the only three ways a verb can be missing from CE, and
		// they are what makes every "verb X is not in CE" claim on this chain a
		// measurement rather than a reading of the help listing:
		//
		//   1. unregistered-command — the name does not exist (exit 1)
		//   2. absent               — the name is unknown under a REGISTERED
		//                             group, which prints that group's help
		//                             (exit 0)
		//   3. unknown-flag         — flag parsing precedes the community gate,
		//                             so a registered-but-gated verb answers
		//                             `Abort: ... community version` while an
		//                             unregistered one never gets that far
		//
		// Without row 1's nonsense control, `atlas cloud` exiting 1 could be
		// misread as a gated Pro stub; without row 3, a Pro verb's flag surface
		// could be misread as unparseable.
		{
			fixture:  "control: nonsense root verb",
			argv:     []string{"frobnicate-nonsense"},
			expected: CEGatingUnregisteredCommand,
		},
		{
			fixture:  "control: nonsense verb under a registered group",
			argv:     []string{"migrate", "frobnicate-nonsense"},
			expected: CEGatingAbsent,
			rules:    CEGatingRules{AbsentCommandPath: "atlas migrate frobnicate-nonsense"},
		},
		{
			// schema plan IS registered in CE (it aborts as a Pro stub), yet a
			// nonsense flag on it still dies at flag parsing. That ordering is
			// what lets the ce-gating table distinguish "flag not registered"
			// from "verb gated".
			fixture:  "control: nonsense flag on a gated verb",
			argv:     []string{"schema", "plan", "--frobnicate-nonsense"},
			expected: CEGatingUnknownFlag,
		},

		// --- v1.3.0 announced command groups --------------------------------
		//
		// Atlas v1.3.0 announced both of these. Measured against the community
		// build they are unregistered root verbs — not Pro stubs. If a future
		// Atlas moves either into CE, these rows go red and the capability
		// becomes a mandatory drop-in obligation rather than a best-effort one.
		{
			fixture:  "atlas script (v1.3.0)",
			argv:     []string{"script"},
			expected: CEGatingUnregisteredCommand,
		},
		{
			fixture:  "atlas cloud (v1.3.0)",
			argv:     []string{"cloud"},
			expected: CEGatingUnregisteredCommand,
		},

		// --- CE's indifference to unknown HCL constructs ---------------------
		//
		// Each subject row is paired with a nonsense control asserting the
		// IDENTICAL shape. The pairing is the point: CE exits 0 on a Pro-only
		// construct exactly as it exits 0 on gibberish, so a row asserting
		// exit 0 alone reads as "CE supports this". The negative fragment is
		// what pins the difference between indifference and support.
		{
			// v1.3.0 announced MySQL invisible columns. CE emits DDL with no
			// INVISIBLE keyword: the attribute was dropped, not honored.
			fixture: "atlas schema diff (column attr: invisible, v1.3.0)",
			setup: func(rt *ceGatingRuntime) error {
				if err := rt.writeFile("from.hcl", ceGatingEmptySchemaHCL); err != nil {
					return err
				}
				return rt.writeFile("to.hcl", ceGatingInvisibleColumnHCL)
			},
			argv: []string{"schema", "diff",
				"--from", "file://from.hcl", "--to", "file://to.hcl", "--dev-url", ceGatingSQLiteDevURL},
			expected: CEGatingWorks,
			rules: CEGatingRules{
				SuccessFragments:       []string{"CREATE TABLE"},
				SuccessAbsentFragments: []string{"INVISIBLE", "invisible"},
			},
		},
		{
			fixture: "control: nonsense column attribute",
			setup: func(rt *ceGatingRuntime) error {
				if err := rt.writeFile("from.hcl", ceGatingEmptySchemaHCL); err != nil {
					return err
				}
				return rt.writeFile("to.hcl", ceGatingNonsenseColumnAttrHCL)
			},
			argv: []string{"schema", "diff",
				"--from", "file://from.hcl", "--to", "file://to.hcl", "--dev-url", ceGatingSQLiteDevURL},
			expected: CEGatingWorks,
			rules: CEGatingRules{
				SuccessFragments:       []string{"CREATE TABLE"},
				SuccessAbsentFragments: []string{"zzz_nonsense_attr"},
			},
		},
		{
			// v1.3.0 announced Schema Annotations. CE drops the block.
			fixture: "atlas schema diff (annotation block, v1.3.0)",
			setup: func(rt *ceGatingRuntime) error {
				if err := rt.writeFile("from.hcl", ceGatingEmptySchemaHCL); err != nil {
					return err
				}
				return rt.writeFile("to.hcl", ceGatingAnnotationHCL)
			},
			argv: []string{"schema", "diff",
				"--from", "file://from.hcl", "--to", "file://to.hcl", "--dev-url", ceGatingSQLiteDevURL},
			expected: CEGatingWorks,
			rules: CEGatingRules{
				SuccessFragments:       []string{"CREATE TABLE"},
				SuccessAbsentFragments: []string{"annotation", "gql"},
			},
		},

		// --- v1.3.0 pre-apply drift detection --------------------------------
		//
		// The dangerous class. CE accepts `check "migrate_apply" { drift }` in
		// atlas.hcl and then applies a migration with real drift present, at
		// exit 0, printing no check output. The config is inert, and silently
		// so. Paired with a nonsense-block control proving atlas.hcl ignores
		// unknown top-level blocks generally.
		{
			// The measured claim is strong and the setup must earn it: apply
			// the corpus, inject REAL drift the migrations never produced (a
			// rogue table and an extra column), add a pending migration, then
			// measure `migrate apply` with the drift check configured. Exit 0
			// with the pending version applied proves the check neither ran
			// nor blocked. A `migrate status` here would prove only that the
			// config parses, which is a much weaker statement.
			fixture: "atlas migrate apply (check drift configured, drifted db, v1.3.0)",
			setup: func(rt *ceGatingRuntime) error {
				if err := setupCEGatingMigrations(rt, true); err != nil {
					return err
				}
				if err := rt.writeFile("atlas.hcl", ceGatingDriftCheckConfig); err != nil {
					return err
				}
				if err := rt.mustRunAtlas("migrate", "apply",
					"--dir", "file://migrations", "--url", "sqlite://app.db"); err != nil {
					return err
				}
				if err := rt.execSQLite("app.db", ceGatingDriftInjectionSQL); err != nil {
					return err
				}
				if err := rt.writeFile(
					"migrations/20260101000003_pending.sql", ceGatingPendingMigration); err != nil {
					return err
				}
				return rt.mustRunAtlas("migrate", "hash", "--dir", "file://migrations")
			},
			argv: []string{"migrate", "apply", "-c", "file://atlas.hcl", "--env", "local",
				"--dir", "file://migrations"},
			expected: CEGatingSilentUnenforced,
			rules: CEGatingRules{
				SilentWhenExitZero: true,
				SilentFragments:    []string{"20260101000003"},
				// If CE ever starts honoring the check, it prints a drift
				// diagnostic before any statement runs. Pinning the absence of
				// that wording means the row cannot stay green by accident.
				SilentAbsentFragments: []string{"drift", "does not match expected state"},
			},
		},
		{
			// Identical setup and identical measured argv as the row above,
			// differing only in the atlas.hcl block name. Same outcome proves
			// `check` is ignored because it is UNKNOWN, not because drift
			// detection ran and found nothing.
			fixture: "control: nonsense atlas.hcl top-level block",
			setup: func(rt *ceGatingRuntime) error {
				if err := setupCEGatingMigrations(rt, true); err != nil {
					return err
				}
				if err := rt.writeFile("atlas.hcl", ceGatingNonsenseBlockConfig); err != nil {
					return err
				}
				if err := rt.mustRunAtlas("migrate", "apply",
					"--dir", "file://migrations", "--url", "sqlite://app.db"); err != nil {
					return err
				}
				if err := rt.execSQLite("app.db", ceGatingDriftInjectionSQL); err != nil {
					return err
				}
				if err := rt.writeFile(
					"migrations/20260101000003_pending.sql", ceGatingPendingMigration); err != nil {
					return err
				}
				return rt.mustRunAtlas("migrate", "hash", "--dir", "file://migrations")
			},
			argv: []string{"migrate", "apply", "-c", "file://atlas.hcl", "--env", "local",
				"--dir", "file://migrations"},
			expected: CEGatingSilentUnenforced,
			rules: CEGatingRules{
				SilentWhenExitZero:    true,
				SilentFragments:       []string{"20260101000003"},
				SilentAbsentFragments: []string{"drift", "does not match expected state"},
			},
		},
	}
}
