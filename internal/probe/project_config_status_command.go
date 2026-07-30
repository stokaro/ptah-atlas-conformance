package probe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type projectConfigCommandResult struct {
	stdout string
	stderr string
}

func projectConfigApply(bin, root, dbPath, amount string) (projectConfigCommandResult, error) {
	args := []string{"migrate", "apply"}
	if amount != "" {
		args = append(args, amount)
	}
	args = append(args, "--env", "local")
	return projectConfigSuccessfulCommand(bin, args, root, projectConfigEnvironment(dbPath))
}

func projectConfigStatus(bin, root, dbPath string) (projectConfigStatusFacts, error) {
	output, err := projectConfigSuccessfulCommand(
		bin,
		[]string{"migrate", "status", "--env", "local", "--format", projectConfigStatusFormat},
		root,
		projectConfigEnvironment(dbPath),
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
) (projectConfigCommandResult, error) {
	output, err := projectConfigCommand(bin, args, dir, extraEnv)
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
) (projectConfigCommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(ptahCommandEnvironment(), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return projectConfigCommandResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}, err
}
