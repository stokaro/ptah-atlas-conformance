package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

// TestMigrateRuntimeProbe_RefusesToReportOnAnUnpinnedOracle drives the built
// command, because the refusal it measures lives in main and nothing below it
// can observe whether a report file was written. A unit test on
// RunMigrateRuntime sees the empty AtlasVersion but not the decision taken from
// it, and that decision -- write nothing rather than write a header that cannot
// say what produced it -- is the whole of this change.
//
// The fake oracle reports a version no pin will ever carry, so the run stops at
// the version stage and never builds a Ptah binary or touches a database.
func TestMigrateRuntimeProbe_RefusesToReportOnAnUnpinnedOracle(t *testing.T) {
	c := qt.New(t)

	dir := t.TempDir()
	probeBin := filepath.Join(dir, "gap-probe-migrate-runtime")
	build := exec.Command("go", "build", "-o", probeBin, ".")
	buildOut, err := build.CombinedOutput()
	c.Assert(err, qt.IsNil, qt.Commentf("build: %s", buildOut))

	atlasBin := filepath.Join(dir, "atlas")
	c.Assert(os.WriteFile(atlasBin,
		[]byte("#!/bin/sh\nprintf 'atlas community version v0.0.0\\n'\n"), 0o600), qt.IsNil)
	c.Assert(os.Chmod(atlasBin, 0o700), qt.IsNil)

	mdOut := filepath.Join(dir, "gaps-migrate-runtime.md")
	jsonOut := filepath.Join(dir, "gaps-migrate-runtime.json")
	run := exec.Command(probeBin, "-md", mdOut, "-json", jsonOut)
	// The pin is read from the working directory, so the probe runs where the
	// tier runs: the repository root.
	run.Dir = filepath.Join("..", "..")
	run.Env = append(os.Environ(), "ATLAS_BIN="+atlasBin)
	output, err := run.CombinedOutput()

	var exitErr *exec.ExitError
	c.Assert(errors.As(err, &exitErr), qt.IsTrue, qt.Commentf("output: %s", output))
	c.Assert(exitErr.ExitCode(), qt.Equals, 2)
	c.Assert(string(output), qt.Contains, `Atlas binary reports "atlas community version v0.0.0"`)

	// The refusal is only worth anything if it also refuses to leave an
	// artifact behind: a one-row report still overwrites the committed file.
	_, mdErr := os.Stat(mdOut)
	c.Assert(errors.Is(mdErr, os.ErrNotExist), qt.IsTrue)
	_, jsonErr := os.Stat(jsonOut)
	c.Assert(errors.Is(jsonErr, os.ErrNotExist), qt.IsTrue)
}
