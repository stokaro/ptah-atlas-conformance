//go:build atlasoracle

package probe_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

type atlasCommandResult struct {
	stdout string
	stderr string
}

func TestProjectConfigStatusFixture_AtlasCEReportsBrownfieldPendingSet(t *testing.T) {
	c := qt.New(t)
	atlasBin := os.Getenv("ATLAS_BIN")
	c.Assert(atlasBin, qt.Not(qt.Equals), "")
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "workflows", "project-config"))
	c.Assert(err, qt.IsNil)
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "project-config.db")
	configURL := "file://" + filepath.ToSlash(filepath.Join(root, "atlas.hcl"))
	env := append(os.Environ(),
		"PTAH_ATLAS_PROJECT_CONFIG_E2E_URL=sqlite://"+filepath.ToSlash(dbPath),
		"ATLAS_NO_UPDATE_NOTIFIER=1",
	)

	setOutput, err := runAtlasCommand(
		atlasBin,
		root,
		env,
		"migrate", "set", "20260719010000",
		"--env", "local",
		"--config", configURL,
	)
	c.Assert(err, qt.IsNil, qt.Commentf("stdout:\n%s\nstderr:\n%s", setOutput.stdout, setOutput.stderr))
	c.Assert(setOutput.stderr, qt.Equals, "")

	statusOutput, err := runAtlasCommand(
		atlasBin,
		root,
		env,
		"migrate", "status",
		"--env", "local",
		"--config", configURL,
		"--format", "{{ json . }}",
	)
	c.Assert(err, qt.IsNil, qt.Commentf("stdout:\n%s\nstderr:\n%s", statusOutput.stdout, statusOutput.stderr))
	c.Assert(statusOutput.stderr, qt.Equals, "")
	c.Assert(statusOutput.stdout, qt.Contains, `"Current":"20260719010000"`)
	c.Assert(statusOutput.stdout, qt.Contains, `"Next":"20260719010101"`)
	c.Assert(statusOutput.stdout, qt.Contains, `"Status":"PENDING"`)
	c.Assert(statusOutput.stdout, qt.Contains, `"Type":"manually set"`)
	c.Assert(statusOutput.stdout, qt.Contains, `"Pending":[{"Name":"20260719010101_add_email.sql"`)
}

func runAtlasCommand(bin, dir string, env []string, args ...string) (atlasCommandResult, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return atlasCommandResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}, err
}
