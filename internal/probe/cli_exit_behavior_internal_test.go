package probe

// White-box testing required: the exit-behavior harness has internal result
// classification and process-boundary logic that is not exposed by its public
// probe API, but must be tested independently from generated report gates.

import (
	"errors"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

const testCLIExitIssue = "stokaro/ptah#688"

// TestCLIExitBehaviorMatrixShape verifies that both compatibility surfaces emit
// one well-formed observation per catalog case. Parity outcomes belong to the
// regression-budget and full-conformance gates, not the harness unit-test tier.
func TestCLIExitBehaviorMatrixShape(t *testing.T) {
	c := qt.New(t)

	results := AtlasCLIExitBehaviorProbe{}.Run(Fixture{Name: cliExitSentinel})
	want := len(cliExitSurfaces()) * len(cliExitCatalog)
	c.Assert(results, qt.HasLen, want)
	for _, result := range results {
		c.Check(result.Probe, qt.Equals, cliExitProbeName)
		c.Check(result.Fixture, qt.Not(qt.Equals), "")
		c.Check(result.Stage, qt.Not(qt.Equals), "")
		c.Check(result.Detail, qt.Not(qt.Equals), "")
	}
}

func TestCLIExitBehaviorIgnoresNonSentinel(t *testing.T) {
	c := qt.New(t)

	got := (AtlasCLIExitBehaviorProbe{}).Run(Fixture{Name: "some/fixture.sql"})
	c.Assert(got, qt.IsNil)
}

func TestClassifyCLIExitResult_HappyPath(t *testing.T) {
	c := qt.New(t)

	tests := []struct {
		name string
		test cliExitCase
		res  cliExitResult
		want Result
	}{
		{
			name: "successful help",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamStdout},
			res:  cliExitResult{exit: 0, stdout: 42},
			want: Result{cliExitProbeName, "ptah-atlas/help", "exit", OK, "exit 0, output → stdout", ""},
		},
		{
			name: "successful validation is silent",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamSilent},
			res:  cliExitResult{exit: 0},
			want: Result{cliExitProbeName, "ptah-atlas/help", "exit", OK, "exit 0, output → silent", ""},
		},
		{
			name: "command failure",
			test: cliExitCase{Want: exitFail, WantStream: exitStreamStderr, StderrClass: "unknown flag"},
			res:  cliExitResult{exit: 1, stderr: "error: unknown flag: --bad\n"},
			want: Result{cliExitProbeName, "ptah-atlas/help", "exit", OK, "exit 1, error → stderr", ""},
		},
		{
			name: "checksum failure uses both streams",
			test: cliExitCase{
				Want:        exitFail,
				WantStream:  exitStreamBoth,
				ExactStdout: atlasChecksumGuidance,
				StderrClass: "checksum mismatch",
			},
			res: cliExitResult{
				exit:       1,
				stdout:     len(atlasChecksumGuidance),
				stdoutText: atlasChecksumGuidance,
				stderr:     "Error: checksum mismatch\n",
			},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/help",
				"exit",
				OK,
				"exit 1, error → stdout and stderr",
				"",
			},
		},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			got := classifyCLIExitResult("ptah-atlas/help", tt.test, tt.res)
			c.Assert(got, qt.DeepEquals, tt.want)
		})
	}
}

func TestClassifyCLIExitResult_FailurePath(t *testing.T) {
	c := qt.New(t)

	tests := []struct {
		name string
		test cliExitCase
		res  cliExitResult
		want Result
	}{
		{
			name: "success exits one",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamStdout, Issue: testCLIExitIssue},
			res:  cliExitResult{exit: 1, stderr: "error: unexpected failure\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"exit",
				Gap,
				"expected exit 0, got 1 (stderr)",
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure exits zero",
			test: cliExitCase{
				Want: exitFail, WantStream: exitStreamStderr,
				StderrClass: "unknown flag", Issue: testCLIExitIssue,
			},
			res: cliExitResult{exit: 0, stderr: "error: unknown flag\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"exit",
				Gap,
				"expected exit 1, got 0 (stderr)",
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure exits two",
			test: cliExitCase{
				Want: exitFail, WantStream: exitStreamStderr,
				StderrClass: "unknown flag", Issue: testCLIExitIssue,
			},
			res: cliExitResult{exit: 2, stderr: "error: unknown flag\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"exit",
				Gap,
				"expected exit 1, got 2 (stderr)",
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure is silent",
			test: cliExitCase{
				Want: exitFail, WantStream: exitStreamStderr,
				StderrClass: "unknown flag", Issue: testCLIExitIssue,
			},
			res: cliExitResult{exit: 1},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected error only on stderr, got silent",
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure writes stdout",
			test: cliExitCase{
				Want: exitFail, WantStream: exitStreamStderr,
				StderrClass: "unknown flag", Issue: testCLIExitIssue,
			},
			res: cliExitResult{exit: 1, stdout: 8, stderr: "error: unknown flag\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected error only on stderr, got stdout and stderr",
				"stokaro/ptah#688",
			},
		},
		{
			name: "success is silent",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamStdout, Issue: testCLIExitIssue},
			res:  cliExitResult{exit: 0},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected output only on stdout, got silent",
				"stokaro/ptah#688",
			},
		},
		{
			name: "silent success writes stdout",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamSilent, Issue: testCLIExitIssue},
			res:  cliExitResult{exit: 0, stdout: 8},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected silent command, got stdout",
				"stokaro/ptah#688",
			},
		},
		{
			name: "success writes stderr",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamStdout, Issue: testCLIExitIssue},
			res:  cliExitResult{exit: 0, stderr: "help\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected output only on stdout, got stderr",
				"stokaro/ptah#688",
			},
		},
		{
			name: "success writes both streams",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamStdout, Issue: testCLIExitIssue},
			res:  cliExitResult{exit: 0, stdout: 8, stderr: "help\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected output only on stdout, got stdout and stderr",
				"stokaro/ptah#688",
			},
		},
		{
			name: "success writes whitespace to stderr",
			test: cliExitCase{Want: exitOK, WantStream: exitStreamStdout, Issue: testCLIExitIssue},
			res:  cliExitResult{exit: 0, stdout: 8, stderr: "\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected output only on stdout, got stdout and stderr",
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure has wrong error class",
			test: cliExitCase{
				Want: exitFail, WantStream: exitStreamStderr,
				StderrClass: "unknown flag", Issue: testCLIExitIssue,
			},
			res: cliExitResult{exit: 1, stderr: "error: connection refused\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"class",
				Gap,
				`expected error-class substring "unknown flag" on stderr`,
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure has wrong stdout class",
			test: cliExitCase{
				Want:        exitFail,
				WantStream:  exitStreamBoth,
				StdoutClass: "checksum error",
				StderrClass: "checksum mismatch",
				Issue:       testCLIExitIssue,
			},
			res: cliExitResult{
				exit:       1,
				stdout:     20,
				stdoutText: "unrelated guidance\n",
				stderr:     "Error: checksum mismatch\n",
			},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"class",
				Gap,
				`expected output-class substring "checksum error" on stdout`,
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure has wrong exact stdout",
			test: cliExitCase{
				Want:        exitFail,
				WantStream:  exitStreamBoth,
				ExactStdout: atlasChecksumGuidance,
				StderrClass: "checksum mismatch",
				Issue:       testCLIExitIssue,
			},
			res: cliExitResult{
				exit:       1,
				stdout:     24,
				stdoutText: "You have a checksum error\n",
				stderr:     "Error: checksum mismatch\n",
			},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"content",
				Gap,
				"stdout does not match Atlas CE output",
				"stokaro/ptah#688",
			},
		},
		{
			name: "failure has wrong exact stderr",
			test: cliExitCase{
				Want:        exitFail,
				WantStream:  exitStreamStderr,
				ExactStderr: "Error: unknown command \"bad\" for \"atlas\"\n",
				Issue:       testCLIExitIssue,
			},
			res: cliExitResult{
				exit:   1,
				stderr: "error: unexpected positional arguments [\"bad\"]\n",
			},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"content",
				Gap,
				"stderr does not match Atlas CE output",
				"stokaro/ptah#688",
			},
		},
		{
			name: "both-stream failure misses stdout",
			test: cliExitCase{
				Want:        exitFail,
				WantStream:  exitStreamBoth,
				StdoutClass: "checksum error",
				StderrClass: "checksum mismatch",
				Issue:       testCLIExitIssue,
			},
			res: cliExitResult{exit: 1, stderr: "Error: checksum mismatch\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"stream",
				Gap,
				"expected output on stdout and stderr, got stderr",
				"stokaro/ptah#688",
			},
		},
		{
			name: "stream expectation is missing",
			test: cliExitCase{Want: exitFail, StderrClass: "unknown flag"},
			res:  cliExitResult{exit: 1, stderr: "error: unknown flag\n"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"setup",
				Fail,
				"missing output-stream expectation",
				"",
			},
		},
		{
			name: "command times out",
			test: cliExitCase{
				Want: exitFail, WantStream: exitStreamStderr,
				StderrClass: "unknown flag", Issue: testCLIExitIssue,
			},
			res: cliExitResult{timedOut: true},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"run",
				Fail,
				"the command timed out",
				"stokaro/ptah#270",
			},
		},
		{
			name: "command cannot start",
			test: cliExitCase{Want: exitFail, WantStream: exitStreamStderr},
			res:  cliExitResult{runErr: "fork/exec /missing: no such file or directory"},
			want: Result{
				cliExitProbeName,
				"ptah-atlas/unknown-flag",
				"run",
				Fail,
				"could not start command: fork/exec /missing: no such file or directory",
				"",
			},
		},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			got := classifyCLIExitResult("ptah-atlas/unknown-flag", tt.test, tt.res)
			c.Assert(got, qt.DeepEquals, tt.want)
		})
	}
}

func TestRunCLIExitCase_FailurePath(t *testing.T) {
	c := qt.New(t)

	surface := cliExitSurface{label: "ptah-atlas"}

	c.Run("fixture setup fails", func(c *qt.C) {
		test := cliExitCase{
			Name: "fixture setup fails",
			Build: func(string) ([]string, error) {
				return nil, errors.New("fixture setup failed")
			},
			Want:       exitFail,
			WantStream: exitStreamStderr,
		}

		got := runCLIExitCase("", surface, test)
		want := Result{
			cliExitProbeName,
			"ptah-atlas/fixture-setup-fails",
			"setup",
			Fail,
			"fixture setup failed",
			"",
		}
		c.Assert(got, qt.DeepEquals, want)
	})

	c.Run("binary is missing", func(c *qt.C) {
		test := cliExitCase{
			Name: "binary is missing",
			Build: func(string) ([]string, error) {
				return []string{"--help"}, nil
			},
			Want:       exitOK,
			WantStream: exitStreamStdout,
		}
		missingBinary := filepath.Join(t.TempDir(), "missing")

		got := runCLIExitCase(missingBinary, surface, test)
		c.Assert(got.Probe, qt.Equals, cliExitProbeName)
		c.Assert(got.Fixture, qt.Equals, "ptah-atlas/binary-is-missing")
		c.Assert(got.Stage, qt.Equals, "run")
		c.Assert(got.Outcome, qt.Equals, Fail)
		c.Assert(got.Detail, qt.Contains, "could not start command:")
		c.Assert(got.Issue, qt.Equals, "")
	})
}
