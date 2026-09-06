// White-box testing required: the command runners are unexported, and the
// property under test is what they hand the child process, which no exported
// result reports.
package probe

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	qt "github.com/frankban/quicktest"
)

// envPrinter is a program that reports the environment it was given. The
// runners are measured through it because asserting on ptahCommandEnvironment
// proves only that the helper filters, not that any runner calls it -- which
// is the half that was missing: commandOutputDir set no environment at all, so
// every atlas-cli and cli-surface probe inherited the operator's PTAH_*
// variables and measured whichever surface those selected.
const envPrinter = `#!/bin/sh
env
`

func writeEnvPrinter(c *qt.C) string {
	dir := c.TempDir()
	bin := filepath.Join(dir, "print-env")
	c.Assert(os.WriteFile(bin, []byte(envPrinter), 0o600), qt.IsNil)
	c.Assert(os.Chmod(bin, 0o755), qt.IsNil)
	return bin
}

func TestCommandOutputDir_DoesNotForwardPtahVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the environment reporter is a POSIX shell script")
	}
	c := qt.New(t)
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "1")
	t.Setenv("ptah_lowercase_leak", "1")
	t.Setenv("CONFORMANCE_KEEP", "present")

	out, err := commandOutputDir(writeEnvPrinter(c), nil, "")

	c.Assert(err, qt.IsNil)
	c.Assert(out, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT=1")
	c.Assert(out, qt.Not(qt.Contains), "ptah_lowercase_leak=1")
	// The paired assertion: the scrub has to be a filter, not an empty
	// environment. A runner handing the child nothing would satisfy every
	// assertion above and break every probe that needs PATH.
	c.Assert(out, qt.Contains, "CONFORMANCE_KEEP=present")
}

func TestCommandStreams_DoesNotForwardPtahVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the environment reporter is a POSIX shell script")
	}
	c := qt.New(t)
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "1")
	t.Setenv("CONFORMANCE_KEEP", "present")

	stdout, stderr, err := commandStreams(writeEnvPrinter(c), nil, "")

	c.Assert(err, qt.IsNil)
	c.Assert(stderr, qt.Equals, "")
	c.Assert(stdout, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT=1")
	c.Assert(stdout, qt.Contains, "CONFORMANCE_KEEP=present")
}

// commandStreamsWithEnv took its additions on top of the unfiltered
// environment, so a caller passing an override still leaked. The override must
// still arrive.
func TestCommandStreamsWithEnv_KeepsTheOverrideAndStillScrubs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the environment reporter is a POSIX shell script")
	}
	c := qt.New(t)
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "1")

	stdout, _, err := commandStreamsWithEnv(writeEnvPrinter(c), nil, "", []string{"CONFORMANCE_OVERRIDE=set"})

	c.Assert(err, qt.IsNil)
	c.Assert(stdout, qt.Contains, "CONFORMANCE_OVERRIDE=set")
	c.Assert(stdout, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT=1")
}
