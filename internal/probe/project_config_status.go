package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	projectConfigStatusFixture = "sqlite/project-config-status-oracle"
	projectConfigDatabaseEnv   = "PTAH_ATLAS_PROJECT_CONFIG_E2E_URL"
	projectConfigStatusFormat  = "{{ json . }}"
)

type projectConfigStatusFile struct {
	Name        string
	Version     string
	Description string
	Type        string
}

type projectConfigStatusFacts struct {
	Available []projectConfigStatusFile
	Applied   []projectConfigStatusFile
	Pending   []projectConfigStatusFile
	Current   string
	Next      string
	Status    string
}

type projectConfigCommandResult struct {
	stdout string
	stderr string
}

// atlasProjectConfigStatusOracle has Atlas CE create the revision state, then
// compares Atlas and Ptah status facts from the same untouched Atlas project.
func atlasProjectConfigStatusOracle(ptahBin, atlasBin string) Result {
	root, err := filepath.Abs(filepath.Join("testdata", "workflows", "project-config"))
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "setup", err)
	}
	workDir, err := os.MkdirTemp("", migrateRuntimeIdentifier("ptah_project_config")+"_")
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	dbPath := filepath.Join(workDir, "project-config.db")
	dbURL := sqliteURL(dbPath)
	configURL := fileURL(filepath.Join(root, "atlas.hcl"))
	env := []string{
		projectConfigDatabaseEnv + "=" + dbURL,
		"ATLAS_NO_UPDATE_NOTIFIER=1",
	}
	atlasSetOutput, err := projectConfigCommand(
		atlasBin,
		[]string{"migrate", "set", "20260719010000", "--env", "local", "--config", configURL},
		root,
		env,
	)
	if err != nil {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-set",
			fmt.Errorf(
				"Atlas CE could not create the project revision state: %w: stdout=%s stderr=%s",
				err,
				oneLine(atlasSetOutput.stdout),
				oneLine(atlasSetOutput.stderr),
			),
		)
	}
	if strings.TrimSpace(atlasSetOutput.stderr) != "" {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-set",
			fmt.Errorf("Atlas CE migrate set wrote unexpected stderr: %s", oneLine(atlasSetOutput.stderr)),
		)
	}

	statusArgs := []string{
		"migrate", "status",
		"--env", "local",
		"--config", configURL,
		"--format", projectConfigStatusFormat,
	}
	atlasOutput, err := projectConfigCommand(atlasBin, statusArgs, root, env)
	if err != nil {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-status",
			fmt.Errorf(
				"Atlas CE status oracle failed: %w: stdout=%s stderr=%s",
				err,
				oneLine(atlasOutput.stdout),
				oneLine(atlasOutput.stderr),
			),
		)
	}
	if strings.TrimSpace(atlasOutput.stderr) != "" {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-status",
			fmt.Errorf("Atlas CE migrate status wrote unexpected stderr: %s", oneLine(atlasOutput.stderr)),
		)
	}
	atlasFacts, err := parseProjectConfigStatusFacts(atlasOutput.stdout)
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-status", err)
	}
	expected := projectConfigExpectedStatusFacts()
	if !reflect.DeepEqual(atlasFacts, expected) {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-status",
			fmt.Errorf("Atlas CE fixture facts = %#v, want %#v", atlasFacts, expected),
		)
	}

	ptahOutput, err := projectConfigCommand(ptahBin, append([]string{"atlas"}, statusArgs...), root, env)
	if err != nil {
		return projectConfigStatusGap(
			"ptah-status",
			fmt.Sprintf(
				"Ptah status failed: %v: stdout=%s stderr=%s",
				err,
				oneLine(ptahOutput.stdout),
				oneLine(ptahOutput.stderr),
			),
		)
	}
	if strings.TrimSpace(ptahOutput.stderr) != "" {
		return projectConfigStatusGap(
			"ptah-status",
			"Ptah migrate status wrote unexpected stderr: "+oneLine(ptahOutput.stderr),
		)
	}
	ptahFacts, err := parseProjectConfigStatusFacts(ptahOutput.stdout)
	if err != nil {
		return projectConfigStatusGap("ptah-status", err.Error())
	}
	if !reflect.DeepEqual(ptahFacts, atlasFacts) {
		return projectConfigStatusGap(
			"compare",
			fmt.Sprintf("Ptah facts = %#v, Atlas CE facts = %#v", ptahFacts, atlasFacts),
		)
	}

	return Result{
		Probe:   migrateRuntimeProbeName,
		Fixture: projectConfigStatusFixture,
		Stage:   "compare",
		Outcome: OK,
		Detail: "Atlas CE created the brownfield revision state, and Atlas CE and Ptah reported identical " +
			"project-config available, applied, pending, current, next, and status facts",
	}
}

func projectConfigExpectedStatusFacts() projectConfigStatusFacts {
	return projectConfigStatusFacts{
		Available: []projectConfigStatusFile{
			{
				Name:        "20260719010000_create_users.sql",
				Version:     "20260719010000",
				Description: "create_users",
			},
			{
				Name:        "20260719010101_add_email.sql",
				Version:     "20260719010101",
				Description: "add_email",
			},
		},
		Applied: []projectConfigStatusFile{
			{
				Version:     "20260719010000",
				Description: "create_users",
				Type:        "manually set",
			},
		},
		Pending: []projectConfigStatusFile{
			{
				Name:        "20260719010101_add_email.sql",
				Version:     "20260719010101",
				Description: "add_email",
			},
		},
		Current: "20260719010000",
		Next:    "20260719010101",
		Status:  "PENDING",
	}
}

func parseProjectConfigStatusFacts(output string) (projectConfigStatusFacts, error) {
	var document struct {
		Available []projectConfigStatusFile
		Applied   []projectConfigStatusFile
		Pending   []projectConfigStatusFile
		Current   string
		Next      string
		Status    string
	}
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		return projectConfigStatusFacts{}, fmt.Errorf("decode migrate status JSON: %w: %s", err, oneLine(output))
	}
	return projectConfigStatusFacts(document), nil
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

func projectConfigStatusGap(stage, detail string) Result {
	return Result{
		Probe:   migrateRuntimeProbeName,
		Fixture: projectConfigStatusFixture,
		Stage:   stage,
		Outcome: Gap,
		Detail:  detail,
		Issue:   "stokaro/ptah#276",
	}
}
