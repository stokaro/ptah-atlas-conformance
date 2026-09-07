//go:build darwin || linux

// White-box testing required: the command runners are unexported, and the
// property under test is what they hand the child process, which no exported
// result reports.
//
// The build tag replaces a `runtime.GOOS == "windows"` skip that stood in every
// test here. The tests need a POSIX shell script to report the environment it
// was given, so on Windows there is nothing to run -- and a conditional inside a
// test function is what the declarative-test standard refuses. The tag says the
// same thing to the compiler instead, and it says it once rather than five
// times.
package probe

import (
	"os"
	"path/filepath"
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
	c := qt.New(t)
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "1")

	stdout, _, err := commandStreamsWithEnv(writeEnvPrinter(c), nil, "", []string{"CONFORMANCE_OVERRIDE=set"})

	c.Assert(err, qt.IsNil)
	c.Assert(stdout, qt.Contains, "CONFORMANCE_OVERRIDE=set")
	c.Assert(stdout, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT=1")
}

// TestStrictCERunners_SelectTheCEOnlySurface is the other half of the scrub.
//
// Removing every inherited PTAH_* makes the surface a decision this repository
// takes rather than one the operator's shell takes, and the CE-oracle probes
// then have to take it: they compare ptah-compat against a CE contract, so the
// variable has to arrive from the runner. Asserting the constant, or asserting
// that strictCEEnvironment returns it, would prove nothing about what any child
// process receives -- which is the exact gap the scrub test above was written
// for.
//
// The negative row beside each is what makes the pair mean something. A runner
// that set the variable unconditionally would satisfy every positive assertion
// and silently put the whole run on the CE-only surface, which is the failure
// this design exists to avoid: on a row that measures a capability Atlas CE
// lacks, a refusal is indistinguishable from the capability being gone.
func TestStrictCERunners_SelectTheCEOnlySurface(t *testing.T) {
	c := qt.New(t)
	// Not set in the parent: the variable has to come from the runner, and an
	// inherited one would be scrubbed anyway.
	t.Setenv("CONFORMANCE_KEEP", "present")
	bin := writeEnvPrinter(c)

	strictOut, err := commandOutputDirStrictCE(bin, nil, "")
	c.Assert(err, qt.IsNil)
	plainOut, err := commandOutputDir(bin, nil, "")
	c.Assert(err, qt.IsNil)
	// commandOutput is asserted beside commandOutputDir rather than assumed to
	// follow it. It is the runner most of the repository calls, and it is a
	// one-line delegation -- which is exactly how it got rewritten to the CE-only
	// surface while every test here still passed: the whole migrate-runtime and
	// txtar corpus moved to a policy that refuses Ptah's own directives, and the
	// only thing that noticed was a stale committed report in CI.
	plainNoDir, err := commandOutput(bin, nil)
	c.Assert(err, qt.IsNil)
	strictStdout, _, err := commandStreamsStrictCE(bin, nil, "")
	c.Assert(err, qt.IsNil)
	plainStdout, _, err := commandStreams(bin, nil, "")
	c.Assert(err, qt.IsNil)

	c.Assert(strictOut, qt.Contains, "PTAH_ATLAS_STRICT_COMPAT=1")
	c.Assert(plainOut, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT")
	c.Assert(strictStdout, qt.Contains, "PTAH_ATLAS_STRICT_COMPAT=1")
	c.Assert(plainStdout, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT")
	c.Assert(plainNoDir, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT")
	// The strict runners still hand the child a real environment rather than
	// only their own variable; a runner that replaced it would break every
	// probe that needs PATH and satisfy the assertions above.
	c.Assert(strictOut, qt.Contains, "CONFORMANCE_KEEP=present")
	c.Assert(strictStdout, qt.Contains, "CONFORMANCE_KEEP=present")
}

// TestStrictCERunners_ScrubAnInheritedSelection pins that the strict runners
// keep the scrub rather than passing the parent's value through.
//
// PTAH_ATLAS_STRICT_COMPAT=0 in the parent must not reach the child and must not
// win: the runner's own value is the decision, and an environment that could
// override it would let a developer's shell turn a CE-oracle row into a
// full-surface one without changing a line of this repository.
func TestStrictCERunners_ScrubAnInheritedSelection(t *testing.T) {
	c := qt.New(t)
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "0")
	bin := writeEnvPrinter(c)

	out, err := commandOutputDirStrictCE(bin, nil, "")

	c.Assert(err, qt.IsNil)
	c.Assert(out, qt.Contains, "PTAH_ATLAS_STRICT_COMPAT=1")
	c.Assert(out, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT=0")
}
