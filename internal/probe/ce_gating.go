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
// v1.2.0; a renovate bump of atlas.version that changes gating behavior turns
// the gate red instead of silently invalidating the matrix.

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
	if rules.SilentWhenExitZero && exitCode == 0 && containsAllFragments(output, rules.SilentFragments) {
		return CEGatingSilentUnenforced, observedExitSummary(exitCode, rules.SilentFragments)
	}
	if exitCode == rules.SuccessExit && containsAllFragments(output, rules.SuccessFragments) {
		return CEGatingWorks, observedExitSummary(exitCode, rules.SuccessFragments)
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

// CEGatingScenario is the read-only public view of one fixed scenario: its
// report fixture label, the measured argv, and the expected baseline class.
type CEGatingScenario struct {
	Fixture  string
	Argv     []string
	Expected CEGatingClass
}

// CEGatingScenarioTable returns the fixed scenario table encoding the measured
// 2026-08-01 Atlas CE v1.2.0 gating baseline.
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
)

var ceCompositeSchemaErrorPattern = regexp.MustCompile(`missing data source handler for "composite_schema"`)

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
// hand-measured 2026-08-01 baseline for the pinned Atlas CE v1.2.0 binary; do
// not edit an expectation without re-measuring against the real binary.
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
			fixture:  "atlas schema inspect --web",
			argv:     []string{"schema", "inspect", "--web"},
			expected: CEGatingUnknownFlag,
		},
	}
}
