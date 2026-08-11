//go:build darwin || linux

package probe

// White-box testing required: runCLIExitWithLimits owns internal process and
// pipe-lifetime safeguards that cannot be observed through the public probe API.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
)

func TestRunCLIExit_BoundsRetainedOutputPipe(t *testing.T) {
	c := qt.New(t)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "child.pid")
	result := runCLIExitWithLimits(
		"sh",
		[]string{"-c", `sleep 30 & echo $! > "$1"`, "sh", pidFile},
		tmp,
		time.Second,
		50*time.Millisecond,
	)

	pidBytes, err := os.ReadFile(pidFile)
	c.Assert(err, qt.IsNil)
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	c.Assert(err, qt.IsNil)
	process, err := os.FindProcess(pid)
	c.Assert(err, qt.IsNil)
	c.Cleanup(func() {
		_ = process.Kill()
	})

	c.Assert(result.timedOut, qt.IsFalse)
	c.Assert(result.runErr, qt.Equals, exec.ErrWaitDelay.Error())
	c.Assert(process.Kill(), qt.IsNil)
}

func TestRunCLIExit_StripsAmbientPtahVariables(t *testing.T) {
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "1")
	c := qt.New(t)

	result := runCLIExit("sh", []string{"-c", `printf %s "${PTAH_ATLAS_STRICT_COMPAT-unset}"`}, t.TempDir())

	c.Assert(result.exit, qt.Equals, 0)
	c.Assert(result.stdoutText, qt.Equals, "unset")
}
