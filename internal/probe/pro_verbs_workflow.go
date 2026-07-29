package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// proVerbsIssue tracks the Atlas Pro-verb conformance batch: the `migrate
// test` / `schema test` forwards, the `migrate edit`/`rebase`/`rm` forwards,
// the local `schema plan` / `schema apply --plan` workflow, and the bare
// `migrate down` Atlas revision-format default.
const proVerbsIssue = "stokaro/ptah#758"

// proWorkflowRuntime bundles what every CLI workflow stage needs: the built
// ptah-compat binary (named `atlas`, the only Atlas-shaped surface since
// stokaro/ptah#850), the committed fixture root, and a scratch run directory
// the fixture tree has been copied into. Stages run the real drop-in CLI from
// the run directory so committed fixtures stay pristine while the measured
// commands read and write relative paths exactly the way an Atlas caller
// would.
type proWorkflowRuntime struct {
	probe    string
	sentinel string
	bin      string
	root     string
	runRoot  string
	// issue owns every gap the workflow reports.
	issue string
}

// newProWorkflowRuntime resolves the fixture root, builds (or accepts) the
// pinned ptah-compat binary, creates the scratch run directory, and copies the
// committed fixture tree into it. Gaps reported through the runtime carry
// issue. On error it returns a harness Fail result and a nil runtime; the
// caller must invoke cleanup on a non-nil runtime.
func newProWorkflowRuntime(probeName, sentinel, fixtureRoot, binaryOverride, issue string) (*proWorkflowRuntime, *Result) {
	root := strings.TrimSpace(fixtureRoot)
	if root == "" {
		failure := proWorkflowHarnessFailure(probeName, sentinel, "fixture setup", fmt.Errorf("fixture root is empty"))
		return nil, &failure
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		failure := proWorkflowHarnessFailure(probeName, sentinel, "fixture setup", fmt.Errorf("resolve fixture root: %w", err))
		return nil, &failure
	}
	info, err := os.Stat(absolute)
	if err != nil {
		failure := proWorkflowHarnessFailure(probeName, sentinel, "fixture setup", fmt.Errorf("stat fixture root: %w", err))
		return nil, &failure
	}
	if !info.IsDir() {
		failure := proWorkflowHarnessFailure(probeName, sentinel, "fixture setup", fmt.Errorf("fixture root is not a directory: %s", absolute))
		return nil, &failure
	}

	bin := strings.TrimSpace(binaryOverride)
	if bin == "" {
		built, err := ptahCompatAtlasBinary()
		if err != nil {
			failure := proWorkflowHarnessFailure(probeName, sentinel, "binary build", err)
			return nil, &failure
		}
		bin = built
	}

	runRoot, err := os.MkdirTemp("", "ptah-pro-verbs-*")
	if err != nil {
		failure := proWorkflowHarnessFailure(probeName, sentinel, "runtime setup", err)
		return nil, &failure
	}
	w := &proWorkflowRuntime{probe: probeName, sentinel: sentinel, bin: bin, root: absolute, runRoot: runRoot, issue: issue}
	// The measured commands rewrite checksum files and migration directories in
	// place, so they always run against a scratch copy of the committed tree.
	if err := os.CopyFS(runRoot, os.DirFS(absolute)); err != nil {
		w.cleanup()
		failure := proWorkflowHarnessFailure(probeName, sentinel, "runtime setup", err)
		return nil, &failure
	}
	return w, nil
}

func (w *proWorkflowRuntime) cleanup() {
	_ = os.RemoveAll(w.runRoot)
}

// runSteps executes workflow steps in order. Each step depends on the state
// the previous step established, so a non-OK step short-circuits: the returned
// slice always ends with the first divergence, keeping the gate red on the
// real problem instead of a cascade of follow-on noise.
func (w *proWorkflowRuntime) runSteps(steps []func() Result) []Result {
	results := make([]Result, 0, len(steps))
	for _, step := range steps {
		result := step()
		results = append(results, result)
		if result.Outcome != OK {
			break
		}
	}
	return results
}

// runCLI runs a ptah-compat CLI command (Atlas argument form, no leading
// `atlas` token — the compat root IS atlas) in the run directory. It returns
// either a harness Fail (process could not run) via the pointer, or the
// completed command result for the caller to validate.
func (w *proWorkflowRuntime) runCLI(stage string, args ...string) (ptahCommandResult, *Result) {
	return w.runCLIWithEnv(stage, nil, args...)
}

func (w *proWorkflowRuntime) runCLIWithEnv(stage string, extraEnv []string, args ...string) (ptahCommandResult, *Result) {
	result, err := runPtahCommandInDirWithEnv(w.bin, args, w.runRoot, extraEnv)
	if err != nil {
		failure := proWorkflowHarnessFailure(w.probe, w.sentinel, stage, fmt.Errorf(
			"execute `atlas %s`: %w; %s", strings.Join(args, " "), err, result.diagnostic()))
		return result, &failure
	}
	return result, nil
}

// runNativeCLI runs a native `ptah` command in the run directory for harness
// verification steps that live outside the measured Atlas surface (for
// example `ptah migrations validate`). The native binary is resolved on
// demand; PTAH_BIN still overrides the build.
func (w *proWorkflowRuntime) runNativeCLI(stage string, args ...string) (ptahCommandResult, *Result) {
	bin, err := ptahBinary()
	if err != nil {
		failure := proWorkflowHarnessFailure(w.probe, w.sentinel, stage, fmt.Errorf("build native Ptah CLI: %w", err))
		return ptahCommandResult{}, &failure
	}
	result, err := runPtahCommandInDirWithEnv(bin, args, w.runRoot, nil)
	if err != nil {
		failure := proWorkflowHarnessFailure(w.probe, w.sentinel, stage, fmt.Errorf(
			"execute `ptah %s`: %w; %s", strings.Join(args, " "), err, result.diagnostic()))
		return result, &failure
	}
	return result, nil
}

// hashAtlasMigrations (re)writes atlas.sum through the real Atlas verb so the
// scratch migration directory is always a valid, integrity-covered
// Atlas-format directory before the measured stage runs.
func (w *proWorkflowRuntime) hashAtlasMigrations(stage string) *Result {
	result, harness := w.runCLI(stage, "migrate", "hash", "--dir", "file://migrations")
	if harness != nil {
		return harness
	}
	if result.exitCode != 0 {
		failure := proWorkflowHarnessFailure(w.probe, w.sentinel, stage, fmt.Errorf(
			"hash migration directory: exit code %d: %s", result.exitCode, result.diagnostic()))
		return &failure
	}
	return nil
}

func (w *proWorkflowRuntime) ok(fixture, stage, detail string) Result {
	return Result{Probe: w.probe, Fixture: fixture, Stage: stage, Outcome: OK, Detail: detail}
}

func (w *proWorkflowRuntime) gap(fixture, stage, detail string) Result {
	return Result{Probe: w.probe, Fixture: fixture, Stage: stage, Outcome: Gap, Detail: detail, Issue: w.issue}
}

func (w *proWorkflowRuntime) harnessFailure(stage string, err error) Result {
	return proWorkflowHarnessFailure(w.probe, w.sentinel, stage, err)
}

// expectExit converts a wrong exit code into a gap result; a nil return means
// the exit code matched.
func (w *proWorkflowRuntime) expectExit(fixture, stage string, result ptahCommandResult, want int) *Result {
	if result.exitCode == want {
		return nil
	}
	gap := w.gap(fixture, stage, fmt.Sprintf(
		"expected exit code %d, got %d: %s", want, result.exitCode, result.diagnostic()))
	return &gap
}

// expectFragments converts a missing output fragment into a gap result; a nil
// return means every fragment was present.
func (w *proWorkflowRuntime) expectFragments(fixture, stage, stream, output string, fragments []string) *Result {
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			continue
		}
		detail := fmt.Sprintf("%s does not contain %q", stream, fragment)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			detail += ": " + oneLine(trimmed)
		} else {
			detail += " (no output)"
		}
		gap := w.gap(fixture, stage, detail)
		return &gap
	}
	return nil
}

func proWorkflowHarnessFailure(probeName, sentinel, stage string, err error) Result {
	return Result{Probe: probeName, Fixture: sentinel, Stage: stage, Outcome: Fail, Detail: err.Error()}
}
