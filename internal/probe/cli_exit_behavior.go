package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cliExitSentinel owns the CLI exit/error-behavior probe's emission.
const cliExitSentinel = "_capability/cli-exit-behavior/SENTINEL"

// cliExitProbeName is the probe/report name.
const cliExitProbeName = "cli-exit-behavior"

// cliExitClass is the expected exit-code class of a CLI invocation. Atlas — and
// therefore a drop-in — exits zero on success and non-zero on any error; scripts
// branch on exactly that boundary, so it is the contract this matrix enforces
// (the exact non-zero value is recorded but treated as a documented divergence,
// since Atlas does not publish specific codes as a contract).
type cliExitClass int

const (
	exitOK   cliExitClass = iota // must exit 0
	exitFail                     // must exit non-zero
)

// cliExitCase is one CLI invocation exercised for exit and stream behavior,
// expressed in Atlas argument form. ptah-compat (the drop-in `atlas` binary) runs
// the args verbatim; `ptah atlas ...` runs them under the native namespace.
type cliExitCase struct {
	// Name is the fixture label.
	Name string
	// Build returns the Atlas-form args for the invocation. It may create files
	// under tmp (a fresh per-case directory the command also runs in), so setup
	// like a tampered atlas.sum stays isolated.
	Build func(tmp string) []string
	// Want is the required exit-code class.
	Want cliExitClass
	// Class is a stable stderr substring documenting the error class (empty for a
	// success case). It is recorded, not brittle-matched — a change is visible in
	// the report but does not by itself fail the class contract.
	Class string
}

// cliExitCatalog covers representative Atlas OSS success and failure paths that
// need no database, so the matrix runs in the offline tier. Each exercises a
// distinct failure mode scripts must observe compatibly.
var cliExitCatalog = []cliExitCase{
	{
		Name:  "help succeeds",
		Build: func(string) []string { return []string{"--help"} },
		Want:  exitOK,
	},
	{
		Name:  "invalid database URL",
		Build: func(string) []string { return []string{"schema", "inspect", "--url", "not-a-valid-url"} },
		Want:  exitFail, Class: "URL",
	},
	{
		Name: "missing migration directory",
		Build: func(string) []string {
			return []string{"migrate", "validate", "--dir", "file:///nonexistent-ptah-conformance-dir"}
		},
		Want: exitFail, Class: "no such file or directory",
	},
	{
		Name: "broken atlas.sum",
		Build: func(tmp string) []string {
			_ = os.WriteFile(filepath.Join(tmp, "20230101000000_init.sql"), []byte("CREATE TABLE t (id int);\n"), 0o600)
			_ = os.WriteFile(filepath.Join(tmp, "atlas.sum"), []byte("h1:tampered\n20230101000000_init.sql h1:bogus\n"), 0o600)
			return []string{"migrate", "validate", "--dir", "file://" + tmp}
		},
		Want: exitFail, Class: "atlas.sum",
	},
	{
		Name:  "unknown flag",
		Build: func(string) []string { return []string{"migrate", "validate", "--totally-unknown-flag"} },
		Want:  exitFail, Class: "unknown flag",
	},
	{
		Name:  "unknown subcommand",
		Build: func(string) []string { return []string{"definitely-not-a-command"} },
		Want:  exitFail, Class: "positional",
	},
	{
		Name: "accepted but unimplemented flag",
		Build: func(string) []string {
			return []string{"schema", "apply", "--url", "sqlite://f", "--to", "file://x", "--plan", "p"}
		},
		Want: exitFail, Class: "--plan",
	},
	{
		Name:  "missing project config",
		Build: func(string) []string { return []string{"schema", "inspect", "--env", "nonexistent"} },
		Want:  exitFail, Class: "atlas.hcl",
	},
}

// cliExitSurface is one Ptah CLI presenting the Atlas surface.
type cliExitSurface struct {
	label  string
	binary func() (string, error)
	prefix []string // prepended to the Atlas-form args
}

func cliExitSurfaces() []cliExitSurface {
	return []cliExitSurface{
		{label: "ptah-compat", binary: ptahCompatAtlasBinary, prefix: nil},
		{label: "ptah-atlas", binary: ptahBinary, prefix: []string{"atlas"}},
	}
}

// AtlasCLIExitBehaviorProbe exercises Atlas OSS success and failure paths across
// the ptah-compat drop-in and the `ptah atlas` namespace, asserting the drop-in
// exit contract (success → 0, failure → non-zero on stderr) and recording the
// exact exit code, the stream the output went to, and the error class — so a
// deliberately inverted exit code, a moved stream, or a changed error class turns
// the committed-report gate red.
type AtlasCLIExitBehaviorProbe struct{}

func (AtlasCLIExitBehaviorProbe) Name() string { return cliExitProbeName }

func (AtlasCLIExitBehaviorProbe) Run(fx Fixture) []Result {
	if fx.Name != cliExitSentinel {
		return nil
	}
	var out []Result
	for _, surface := range cliExitSurfaces() {
		bin, err := surface.binary()
		if err != nil {
			out = append(out, Result{cliExitProbeName, surface.label, "build", Fail,
				"could not build the Ptah CLI: " + oneLine(err.Error()), "stokaro/ptah#270"})
			continue
		}
		for _, c := range cliExitCatalog {
			out = append(out, runCLIExitCase(bin, surface, c))
		}
	}
	return out
}

func runCLIExitCase(bin string, surface cliExitSurface, c cliExitCase) Result {
	// Space-free label so the (probe, fixture, stage) waiver key is one token.
	label := surface.label + "/" + strings.ReplaceAll(c.Name, " ", "-")
	tmp, err := os.MkdirTemp("", "cli-exit-*")
	if err != nil {
		return Result{cliExitProbeName, label, "setup", Fail, err.Error(), ""}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	args := append(append([]string{}, surface.prefix...), c.Build(tmp)...)
	res := runCLIExit(bin, args, tmp)

	if res.timedOut {
		return Result{cliExitProbeName, label, "run", Fail, "the command timed out", "stokaro/ptah#270"}
	}

	stream := streamChoice(res.stdout, res.stderr)
	classNote := ""
	if c.Class != "" && !strings.Contains(res.stderr, c.Class) {
		classNote = fmt.Sprintf("; error-class substring %q not on stderr", c.Class)
	}

	switch c.Want {
	case exitOK:
		if res.exit != 0 {
			return Result{cliExitProbeName, label, "exit", Gap,
				fmt.Sprintf("expected success but exited %d (%s)%s", res.exit, stream, classNote), "stokaro/ptah#270"}
		}
		return Result{cliExitProbeName, label, "exit", OK,
			fmt.Sprintf("exit 0, output → %s", stream), ""}
	default: // exitFail
		if res.exit == 0 {
			// A failure path that exits 0 means scripts cannot observe the error.
			return Result{cliExitProbeName, label, "exit", Gap,
				fmt.Sprintf("expected non-zero exit, got 0 — the failure is not observable via exit code (%s)%s", stream, classNote), "stokaro/ptah#270"}
		}
		return Result{cliExitProbeName, label, "exit", OK,
			fmt.Sprintf("exit %d, error → %s%s", res.exit, stream, classNote), ""}
	}
}

// streamChoice describes which stream carried output, so the report gates the
// stdout/stderr choice: an error must reach stderr, and (like Atlas) ideally
// leave stdout clean.
func streamChoice(stdoutLen int, stderr string) string {
	hasErr := strings.TrimSpace(stderr) != ""
	switch {
	case stdoutLen > 0 && hasErr:
		return "stderr (stdout not clean)"
	case hasErr:
		return "stderr"
	case stdoutLen > 0:
		return "stdout"
	default:
		return "silent"
	}
}

type cliExitResult struct {
	exit     int
	stdout   int
	stderr   string
	timedOut bool
}

func runCLIExit(bin string, args []string, dir string) cliExitResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := cliExitResult{exit: 0, stdout: stdout.Len(), stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		res.timedOut = true
		return res
	}
	if err != nil {
		res.exit = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exit = ee.ExitCode()
		}
	}
	return res
}
