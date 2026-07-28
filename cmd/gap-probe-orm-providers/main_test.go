package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestCommand_HarnessFailureMakesFullGateRed(t *testing.T) {
	c := qt.New(t)
	dir := t.TempDir()
	markdownReport := filepath.Join(dir, "gaps.md")
	jsonReport := filepath.Join(dir, "gaps.json")
	cmd := exec.Command(
		"go", "run", ".",
		"--fixtures", filepath.Join(dir, "missing"),
		"--md", markdownReport,
		"--json", jsonReport,
		"--gate",
	)
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(dir, "gocache"))

	output, err := cmd.CombinedOutput()

	c.Assert(err, qt.ErrorMatches, "exit status 1")
	c.Check(string(output), qt.Contains, "ORM PROVIDER CONFORMANCE GATE: RED")
	markdown, err := os.ReadFile(markdownReport)
	c.Assert(err, qt.IsNil)
	c.Check(string(markdown), qt.Contains, "## Status: NOT DONE - 1 non-OK observation")
	data, err := os.ReadFile(jsonReport)
	c.Assert(err, qt.IsNil)
	var results []probe.Result
	c.Assert(json.Unmarshal(data, &results), qt.IsNil)
	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "orm-provider-smoke")
	c.Check(results[0].Fixture, qt.Equals, "orm providers")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Detail, qt.Contains, filepath.Join(dir, "missing"))
	c.Check(results[0].Issue, qt.Equals, "")
}
