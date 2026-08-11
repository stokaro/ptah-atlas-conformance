package probe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

type projectConfigCommandResult struct {
	stdout string
	stderr string
}

type projectConfigApplyWindow struct {
	startedAt  time.Time
	finishedAt time.Time
}

func projectConfigApply(
	bin, root, dbPath, amount string,
	surface ptahCommandSurface,
) (projectConfigCommandResult, projectConfigApplyWindow, error) {
	args := []string{"migrate", "apply"}
	if amount != "" {
		args = append(args, amount)
	}
	args = append(args, "--env", "local")
	window := projectConfigApplyWindow{startedAt: time.Now()}
	output, err := projectConfigSuccessfulCommand(bin, args, root, projectConfigEnvironment(dbPath), surface)
	window.finishedAt = time.Now()
	return output, window, err
}

func projectConfigStatus(bin, root, dbPath string, surface ptahCommandSurface) (projectConfigStatusFacts, error) {
	output, err := projectConfigSuccessfulCommand(
		bin,
		[]string{"migrate", "status", "--env", "local", "--format", projectConfigStatusFormat},
		root,
		projectConfigEnvironment(dbPath),
		surface,
	)
	if err != nil {
		return projectConfigStatusFacts{}, err
	}
	return parseProjectConfigStatusFacts(output.stdout)
}

func projectConfigEnvironment(dbPath string) []string {
	return []string{
		projectConfigDatabaseEnv + "=" + sqliteURL(dbPath),
		"ATLAS_NO_UPDATE_NOTIFIER=1",
	}
}

func projectConfigSuccessfulCommand(
	bin string,
	args []string,
	dir string,
	extraEnv []string,
	surface ptahCommandSurface,
) (projectConfigCommandResult, error) {
	output, err := projectConfigCommand(bin, args, dir, extraEnv, surface)
	if err != nil {
		return output, fmt.Errorf(
			"%s failed: %w: stdout=%s stderr=%s",
			strings.Join(args, " "),
			err,
			oneLine(output.stdout),
			oneLine(output.stderr),
		)
	}
	if strings.TrimSpace(output.stderr) != "" {
		return output, fmt.Errorf("%s wrote unexpected stderr: %s", strings.Join(args, " "), oneLine(output.stderr))
	}
	return output, nil
}

func projectConfigCommand(
	bin string,
	args []string,
	dir string,
	extraEnv []string,
	surface ptahCommandSurface,
) (projectConfigCommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = slices.Concat(surface.environment(), extraEnv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return projectConfigCommandResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}, err
}

func validatePinnedAtlasBinary(atlasBin string) (string, error) {
	pinnedData, err := os.ReadFile("atlas.version")
	if err != nil {
		return "", fmt.Errorf("read atlas.version: %w", err)
	}
	pinned := strings.TrimSpace(string(pinnedData))
	observed, err := atlasVersionLine(atlasBin)
	if err != nil {
		return "", err
	}
	if !AtlasVersionMatchesPin(observed, pinned) {
		return "", fmt.Errorf("Atlas binary reports %q, want atlas.version %q", observed, pinned)
	}
	return observed, nil
}

// AtlasVersionMatchesPin reports whether an Atlas version line identifies the
// Community release selected by atlas.version.
func AtlasVersionMatchesPin(observed, pinned string) bool {
	return pinned != "" && strings.TrimSpace(observed) == "atlas community version "+pinned
}
