package probe

// White-box testing required: the subprocess environment scrub is a safety
// boundary whose case-insensitive behavior cannot be observed portably through
// the public probe, and direct setup-check factory access proves the panic
// regression test exercises the exact production check definitions.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestDBTestCommandEnvironment_StripsPtahVariables(t *testing.T) {
	c := qt.New(t)
	t.Setenv("PTAH_DB_URL", "sqlite://must-not-escape")
	t.Setenv("ptah_token", "must-not-escape")
	t.Setenv("DBTEST_KEEP", "present")

	environment := dbTestCommandEnvironment()

	c.Assert(environment, qt.Not(qt.Contains), "PTAH_DB_URL=sqlite://must-not-escape")
	c.Assert(environment, qt.Not(qt.Contains), "ptah_token=must-not-escape")
	c.Assert(environment, qt.Contains, "DBTEST_KEEP=present")
}

func TestDBTestWorkflowSetupChecks_RejectPanicWithExpectedExitAndFragments(t *testing.T) {
	c := qt.New(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "panic-command")
	c.Assert(os.WriteFile(source, []byte(`package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "failed to load test cases: field unexpected not found")
	panic("unexpected decoder failure")
}
`), 0o600), qt.IsNil)
	build := exec.Command("go", "build", "-o", binary, source)
	c.Assert(build.Run(), qt.IsNil)

	migrationResult := dbTestMigrationSetupFailureCheck("unused-root").run(binary)
	schemaResult := dbTestSchemaSetupFailureCheck("unused-root", "unused-models").run(binary)

	c.Assert(migrationResult.Outcome, qt.Equals, Gap)
	c.Assert(migrationResult.Detail, qt.Contains, `stderr output unexpectedly contains "panic:"`)
	c.Assert(schemaResult.Outcome, qt.Equals, Gap)
	c.Assert(schemaResult.Detail, qt.Contains, `stderr output unexpectedly contains "panic:"`)
}
