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

const (
	atlasChecksumGuidance = "You have a checksum error in your migration directory.\n" +
		"Please check your migration files and run 'atlas migrate hash' to re-hash the contents\n\n"
	atlasSingleMigrationSum = "h1:RwlBQllTgRiQ5aUj9/rR1G0CguPI2caCrrSf7y+LbzA=\n" +
		"1_initial.sql h1:7PYyx3jJKyr9v/6Ta0xuXTz4HqAKDLjXxLWjRtnDhWA=\n"
	atlasTwoMigrationSum = "h1:+34914C9ncHjtpQsy5S8WcVKs661cvqzMlPhZ7LbT0E=\n" +
		"1_initial.sql h1:7PYyx3jJKyr9v/6Ta0xuXTz4HqAKDLjXxLWjRtnDhWA=\n" +
		"2_second.sql h1:ZwOkl2cGwIRo6FFePelI6LT91EmqcXgP9/b9YOhfahg=\n"
)

// cliExitCode is the exact process exit code required by the Atlas-compatible
// surfaces. Atlas CE exits zero on success and one on command failures.
type cliExitCode int

const (
	exitOK   cliExitCode = 0
	exitFail cliExitCode = 1
)

type cliExitStream int

const (
	exitStreamSilent cliExitStream = iota + 1
	exitStreamStdout
	exitStreamStderr
	exitStreamBoth
)

// cliExitCase is one CLI invocation exercised for exit and stream behavior,
// expressed in Atlas argument form. ptah-compat (the drop-in `atlas` binary)
// runs the args verbatim.
type cliExitCase struct {
	// Name is the fixture label.
	Name string
	// Build returns the Atlas-form args for the invocation. It may create files
	// under tmp (a fresh per-case directory the command also runs in), so setup
	// like a tampered atlas.sum stays isolated.
	Build func(tmp string) ([]string, error)
	// Want is the required process exit code.
	Want cliExitCode
	// WantStream is the required stdout/stderr output pattern.
	WantStream cliExitStream
	// ExactStdout requires byte-for-byte stdout when non-empty.
	ExactStdout string
	// ExactStderr requires byte-for-byte stderr when non-empty.
	ExactStderr string
	// StdoutClass and StderrClass are stable substrings documenting the expected
	// output class. Empty values skip content matching on that stream.
	StdoutClass string
	StderrClass string
	// Issue owns any compatibility gap reported for this case.
	Issue string
}

// cliExitCatalog covers representative Atlas OSS success and failure paths that
// need no database, so the matrix runs in the offline tier. Each exercises a
// distinct failure mode scripts must observe compatibly.
var cliExitCatalog = []cliExitCase{
	{
		Name:  "help succeeds",
		Build: func(string) ([]string, error) { return []string{"--help"}, nil },
		Want:  exitOK, WantStream: exitStreamStdout, Issue: "stokaro/ptah#688",
	},
	{
		Name: "clean atlas.sum succeeds silently",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id INT);\n",
				"atlas.sum":     atlasSingleMigrationSum,
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:       exitOK,
		WantStream: exitStreamSilent,
		Issue:      "stokaro/ptah#727",
	},
	{
		// Since stokaro/ptah#811/#814 a scheme-less token is a valid Ptah
		// desired-state source (a local schema file), so the invalid-URL
		// case pins an unsupported scheme instead. Atlas CE reports
		// `unknown driver "bogus"` and Ptah `unsupported desired-state URL
		// scheme "bogus"`: both name the quoted offending scheme on stderr.
		Name: "invalid database URL",
		Build: func(string) ([]string, error) {
			return []string{"schema", "inspect", "--url", "bogus://foo"}, nil
		},
		Want: exitFail, WantStream: exitStreamStderr, StderrClass: `"bogus"`,
		Issue: "stokaro/ptah#688",
	},
	{
		Name:  "missing required flag",
		Build: func(string) ([]string, error) { return []string{"schema", "inspect"}, nil },
		Want:  exitFail, WantStream: exitStreamStderr, StderrClass: "required", Issue: "stokaro/ptah#688",
	},
	{
		Name: "missing migration directory",
		Build: func(tmp string) ([]string, error) {
			missingDir := filepath.Join(tmp, "missing")
			return []string{"migrate", "validate", "--dir", "file://" + missingDir}, nil
		},
		Want: exitFail, WantStream: exitStreamStderr, StderrClass: "no such file or directory",
		Issue: "stokaro/ptah#688",
	},
	{
		Name: "malformed atlas.sum",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id INT);\n",
				"atlas.sum":     "h1:tampered\n1_initial.sql h1:bogus\n",
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:        exitFail,
		WantStream:  exitStreamBoth,
		ExactStdout: atlasChecksumGuidance,
		StdoutClass: "checksum error",
		StderrClass: "checksum mismatch",
		Issue:       "stokaro/ptah#714",
	},
	{
		Name: "missing atlas.sum",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id INT);\n",
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:        exitFail,
		WantStream:  exitStreamBoth,
		ExactStdout: atlasChecksumGuidance,
		ExactStderr: "Error: checksum file not found\n",
		StdoutClass: "checksum error",
		StderrClass: "checksum file not found",
		Issue:       "stokaro/ptah#723",
	},
	{
		Name: "edited migration",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id BIGINT);\n",
				"atlas.sum":     atlasSingleMigrationSum,
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:       exitFail,
		WantStream: exitStreamBoth,
		ExactStdout: "You have a checksum error in your migration directory.\n\n" +
			"\tL2: 1_initial.sql was edited\n\n" +
			"Please check your migration files and run 'atlas migrate hash' to re-hash the contents\n\n",
		StderrClass: "checksum mismatch",
		Issue:       "stokaro/ptah#714",
	},
	{
		Name: "added migration",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id INT);\n",
				"2_second.sql":  "CREATE TABLE u (id INT);\n",
				"atlas.sum":     atlasSingleMigrationSum,
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:       exitFail,
		WantStream: exitStreamBoth,
		ExactStdout: "You have a checksum error in your migration directory.\n\n" +
			"\tL3: 2_second.sql was added\n\n" +
			"Please check your migration files and run 'atlas migrate hash' to re-hash the contents\n\n",
		StderrClass: "checksum mismatch",
		Issue:       "stokaro/ptah#714",
	},
	{
		Name: "removed migration",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"2_second.sql": "CREATE TABLE u (id INT);\n",
				"atlas.sum":    atlasTwoMigrationSum,
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:       exitFail,
		WantStream: exitStreamBoth,
		ExactStdout: "You have a checksum error in your migration directory.\n\n" +
			"\tL2: 1_initial.sql was removed\n\n" +
			"Please check your migration files and run 'atlas migrate hash' to re-hash the contents\n\n",
		StderrClass: "checksum mismatch",
		Issue:       "stokaro/ptah#714",
	},
	{
		Name: "duplicate atlas.sum entry",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id INT);\n",
				"atlas.sum": atlasSingleMigrationSum +
					"1_initial.sql h1:7PYyx3jJKyr9v/6Ta0xuXTz4HqAKDLjXxLWjRtnDhWA=\n",
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:        exitFail,
		WantStream:  exitStreamBoth,
		ExactStdout: atlasChecksumGuidance,
		StderrClass: "checksum mismatch",
		Issue:       "stokaro/ptah#714",
	},
	{
		Name: "directory hash mismatch",
		Build: func(tmp string) ([]string, error) {
			err := writeCLIExitFixture(tmp, map[string]string{
				"1_initial.sql": "CREATE TABLE t (id INT);\n",
				"atlas.sum": "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
					"1_initial.sql h1:7PYyx3jJKyr9v/6Ta0xuXTz4HqAKDLjXxLWjRtnDhWA=\n",
			})
			if err != nil {
				return nil, err
			}
			return []string{"migrate", "validate", "--dir", "file://" + tmp}, nil
		},
		Want:        exitFail,
		WantStream:  exitStreamBoth,
		ExactStdout: atlasChecksumGuidance,
		StderrClass: "checksum mismatch",
		Issue:       "stokaro/ptah#714",
	},
	{
		Name: "unknown flag",
		Build: func(string) ([]string, error) {
			return []string{"migrate", "validate", "--totally-unknown-flag"}, nil
		},
		Want: exitFail, WantStream: exitStreamStderr, StderrClass: "unknown flag",
		Issue: "stokaro/ptah#688",
	},
	{
		Name:       "unknown subcommand",
		Build:      func(string) ([]string, error) { return []string{"definitely-not-a-command"}, nil },
		Want:       exitFail,
		WantStream: exitStreamStderr,
		ExactStderr: "Error: unknown command \"definitely-not-a-command\" for \"atlas\"\n" +
			"Run 'atlas --help' for usage.\n",
		StderrClass: "unknown command",
		Issue:       "stokaro/ptah#725",
	},
	{
		Name:       "unknown subcommand suggests close verb",
		Build:      func(string) ([]string, error) { return []string{"migrat"}, nil },
		Want:       exitFail,
		WantStream: exitStreamStderr,
		ExactStderr: "Error: unknown command \"migrat\" for \"atlas\"\n\n" +
			"Did you mean this?\n" +
			"\tmigrate\n\n" +
			"Run 'atlas --help' for usage.\n",
		StderrClass: "Did you mean this?",
		Issue:       "stokaro/ptah#725",
	},
	{
		Name: "migrate group extra token shows help",
		Build: func(string) ([]string, error) {
			return []string{"migrate", "aplly"}, nil
		},
		Want:        exitOK,
		WantStream:  exitStreamStdout,
		StdoutClass: "migrate [command]",
		Issue:       "stokaro/ptah#725",
	},
	{
		Name: "completion group extra token shows help",
		Build: func(string) ([]string, error) {
			return []string{"completion", "sh"}, nil
		},
		Want:        exitOK,
		WantStream:  exitStreamStdout,
		StdoutClass: "completion [command]",
		Issue:       "stokaro/ptah#725",
	},
	{
		Name: "completion bash generates script",
		Build: func(string) ([]string, error) {
			return []string{"completion", "bash"}, nil
		},
		Want:        exitOK,
		WantStream:  exitStreamStdout,
		StdoutClass: "# bash completion V2 for ",
		Issue:       "stokaro/ptah#725",
	},
	{
		Name: "completion shell extra token fails",
		Build: func(string) ([]string, error) {
			return []string{"completion", "bash", "extra"}, nil
		},
		Want:        exitFail,
		WantStream:  exitStreamStderr,
		ExactStderr: "Error: unknown command \"extra\" for \"atlas completion bash\"\n",
		StderrClass: "unknown command",
		Issue:       "stokaro/ptah#725",
	},
	{
		Name: "accepted but unimplemented flag",
		Build: func(string) ([]string, error) {
			return []string{"schema", "apply", "--url", "sqlite://f", "--to", "file://x", "--plan", "p"}, nil
		},
		Want: exitFail, WantStream: exitStreamStderr, StderrClass: "--plan",
		Issue: "stokaro/ptah#688",
	},
	{
		Name: "missing project config",
		Build: func(string) ([]string, error) {
			return []string{"schema", "inspect", "--env", "nonexistent"}, nil
		},
		Want: exitFail, WantStream: exitStreamStderr, StderrClass: "atlas.hcl",
		Issue: "stokaro/ptah#688",
	},
}

func writeCLIExitFixture(root string, files map[string]string) error {
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write %s fixture: %w", name, err)
		}
	}
	return nil
}

// cliExitSurface is one Ptah CLI presenting the Atlas surface.
type cliExitSurface struct {
	label  string
	binary func() (string, error)
	prefix []string // prepended to the Atlas-form args
}

func cliExitSurfaces() []cliExitSurface {
	// Since stokaro/ptah#850 the ptah-compat binary is the only Atlas-shaped
	// surface; the main `ptah` binary rejects the `atlas` namespace, which the
	// namespace-removal check below pins.
	return []cliExitSurface{
		{label: "ptah-compat", binary: ptahCompatAtlasBinary, prefix: nil},
	}
}

// nativeAtlasNamespaceRejection is the exact contract the main `ptah` binary
// must keep after stokaro/ptah#850 removed the `ptah atlas ...` command tree:
// the `atlas` token is an unknown command that exits 2 with a one-line stderr
// diagnostic and nothing on stdout. A binary that resolves the namespace again
// (or reports it differently) regresses the single-surface design.
const (
	nativeAtlasNamespaceFixture = "ptah-native/atlas-namespace-rejected"
	nativeAtlasNamespaceStderr  = "error: unknown command \"atlas\" for \"ptah\"\n"
	nativeAtlasNamespaceExit    = 2
)

// AtlasCLIExitBehaviorProbe exercises Atlas OSS success and failure paths on
// the ptah-compat drop-in binary, asserting the drop-in exit contract
// (success → 0, failure → 1 on stderr) and recording the stream the output
// went to and the error class. It also pins the stokaro/ptah#850 removal: the
// main `ptah` binary must keep rejecting the `atlas` namespace with exit 2. A
// wrong exit code, output stream, or error class turns the probe red.
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
		out = append(out, runCLIExitCatalog(bin, surface)...)
	}
	out = append(out, runNativeAtlasNamespaceRemovedCheck())
	return out
}

// runNativeAtlasNamespaceRemovedCheck asserts the main `ptah` binary rejects
// the `atlas` namespace exactly as stokaro/ptah#850 left it. This is not a
// cliExitCatalog case on purpose: the catalog is oracle-checked against Atlas
// CE, while this contract exists only on the native Ptah surface.
func runNativeAtlasNamespaceRemovedCheck() Result {
	bin, err := ptahBinary()
	if err != nil {
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "build", Fail,
			"could not build the Ptah CLI: " + oneLine(err.Error()), "stokaro/ptah#270"}
	}
	tmp, err := os.MkdirTemp("", "cli-exit-native-*")
	if err != nil {
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "setup", Fail, err.Error(), ""}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	res := runCLIExit(bin, []string{"atlas"}, tmp)
	switch {
	case res.timedOut:
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "run", Fail,
			"the command timed out", "stokaro/ptah#270"}
	case res.runErr != "":
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "run", Fail,
			"could not start command: " + oneLine(res.runErr), ""}
	case res.exit != nativeAtlasNamespaceExit:
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "exit", Gap,
			fmt.Sprintf("`ptah atlas` must stay removed and exit %d as an unknown command, got exit %d (%s); "+
				"re-adding the namespace regresses the ptah-compat single-surface design",
				nativeAtlasNamespaceExit, res.exit, streamChoice(res.stdout, res.stderr)), "stokaro/ptah#850"}
	case res.stdout > 0:
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "stream", Gap,
			"`ptah atlas` must print its unknown-command diagnostic on stderr only, but wrote to stdout",
			"stokaro/ptah#850"}
	case res.stderr != nativeAtlasNamespaceStderr:
		return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "content", Gap,
			"`ptah atlas` stderr diverged from the pinned unknown-command diagnostic: " + oneLine(res.stderr),
			"stokaro/ptah#850"}
	}
	return Result{cliExitProbeName, nativeAtlasNamespaceFixture, "exit", OK,
		"`ptah atlas` exits 2 with the unknown-command diagnostic on stderr; " +
			"the Atlas surface lives exclusively in the ptah-compat binary", ""}
}

func runCLIExitCatalog(bin string, surface cliExitSurface) []Result {
	out := make([]Result, 0, len(cliExitCatalog))
	for _, c := range cliExitCatalog {
		out = append(out, runCLIExitCase(bin, surface, c))
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

	caseArgs, err := c.Build(tmp)
	if err != nil {
		return Result{cliExitProbeName, label, "setup", Fail, err.Error(), ""}
	}
	args := append(append([]string{}, surface.prefix...), caseArgs...)
	res := runCLIExit(bin, args, tmp)
	return classifyCLIExitResult(label, c, res)
}

func classifyCLIExitResult(label string, c cliExitCase, res cliExitResult) Result {
	if res.timedOut {
		return Result{cliExitProbeName, label, "run", Fail, "the command timed out", "stokaro/ptah#270"}
	}
	if res.runErr != "" {
		return Result{cliExitProbeName, label, "run", Fail,
			"could not start command: " + oneLine(res.runErr), ""}
	}

	stream := streamChoice(res.stdout, res.stderr)
	if res.exit != int(c.Want) {
		return Result{cliExitProbeName, label, "exit", Gap,
			fmt.Sprintf("expected exit %d, got %d (%s)", c.Want, res.exit, stream), c.Issue}
	}

	switch c.WantStream {
	case exitStreamSilent:
		if res.stdout > 0 || res.stderr != "" {
			return Result{cliExitProbeName, label, "stream", Gap,
				fmt.Sprintf("expected silent command, got %s", stream), c.Issue}
		}
	case exitStreamStdout:
		if res.stdout == 0 || res.stderr != "" {
			return Result{cliExitProbeName, label, "stream", Gap,
				fmt.Sprintf("expected output only on stdout, got %s", stream), c.Issue}
		}
	case exitStreamStderr:
		if res.stderr == "" || res.stdout > 0 {
			return Result{cliExitProbeName, label, "stream", Gap,
				fmt.Sprintf("expected error only on stderr, got %s", stream), c.Issue}
		}
	case exitStreamBoth:
		if res.stdout == 0 || res.stderr == "" {
			return Result{cliExitProbeName, label, "stream", Gap,
				fmt.Sprintf("expected output on stdout and stderr, got %s", stream), c.Issue}
		}
	default:
		return Result{cliExitProbeName, label, "setup", Fail, "missing output-stream expectation", ""}
	}

	if c.ExactStdout != "" && res.stdoutText != c.ExactStdout {
		return Result{cliExitProbeName, label, "content", Gap,
			"stdout does not match Atlas CE output", c.Issue}
	}
	if c.ExactStderr != "" && res.stderr != c.ExactStderr {
		return Result{cliExitProbeName, label, "content", Gap,
			"stderr does not match Atlas CE output", c.Issue}
	}
	if c.StdoutClass != "" && !strings.Contains(res.stdoutText, c.StdoutClass) {
		return Result{cliExitProbeName, label, "class", Gap,
			fmt.Sprintf("expected output-class substring %q on stdout", c.StdoutClass), c.Issue}
	}
	if c.StderrClass != "" && !strings.Contains(res.stderr, c.StderrClass) {
		return Result{cliExitProbeName, label, "class", Gap,
			fmt.Sprintf("expected error-class substring %q on stderr", c.StderrClass), c.Issue}
	}
	if c.Want == exitOK {
		return Result{cliExitProbeName, label, "exit", OK,
			fmt.Sprintf("exit 0, output → %s", stream), ""}
	}
	return Result{cliExitProbeName, label, "exit", OK,
		fmt.Sprintf("exit 1, error → %s", stream), ""}
}

// streamChoice describes which streams carried output.
func streamChoice(stdoutLen int, stderr string) string {
	hasErr := stderr != ""
	switch {
	case stdoutLen > 0 && hasErr:
		return "stdout and stderr"
	case hasErr:
		return "stderr"
	case stdoutLen > 0:
		return "stdout"
	default:
		return "silent"
	}
}

type cliExitResult struct {
	exit       int
	stdout     int
	stdoutText string
	stderr     string
	runErr     string
	timedOut   bool
}

func runCLIExit(bin string, args []string, dir string) cliExitResult {
	return runCLIExitWithLimits(bin, args, dir, 30*time.Second, 2*time.Second)
}

func runCLIExitWithLimits(
	bin string,
	args []string,
	dir string,
	timeout time.Duration,
	waitDelay time.Duration,
) cliExitResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := cliExitResult{
		exit:       0,
		stdout:     stdout.Len(),
		stdoutText: stdout.String(),
		stderr:     stderr.String(),
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.timedOut = true
		return res
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exit = ee.ExitCode()
		} else {
			res.runErr = err.Error()
		}
	}
	return res
}
