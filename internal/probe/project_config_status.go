package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	projectConfigStatusFixture   = "sqlite/project-config-apply-oracle"
	projectConfigDatabaseEnv     = "PTAH_ATLAS_PROJECT_CONFIG_E2E_URL"
	projectConfigStatusFormat    = "{{ json . }}"
	projectConfigAtlasProducer   = "atlas"
	projectConfigPtahProducer    = "ptah"
	projectConfigTimestampLayout = "2006-01-02 15:04:05.999999999Z07:00"
)

// atlasProjectConfigApplyOracle uses Atlas CE to create and finish a
// brownfield migration, then compares Ptah against an Atlas-controlled clone.
func atlasProjectConfigApplyOracle(ptahBin, atlasBin string) Result {
	root, err := filepath.Abs(filepath.Join("testdata", "workflows", "project-config"))
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "setup", err)
	}
	workDir, err := os.MkdirTemp("", migrateRuntimeIdentifier("ptah_project_config")+"_")
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	startPath := filepath.Join(workDir, "brownfield-start.db")
	controlPath := filepath.Join(workDir, "atlas-control.db")
	candidatePath := filepath.Join(workDir, "ptah-candidate.db")

	if _, err := projectConfigApply(atlasBin, root, startPath, "1"); err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-bootstrap", err)
	}
	bootstrapStatus, err := projectConfigStatus(atlasBin, root, startPath)
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-bootstrap-status", err)
	}
	bootstrapDatabase, err := inspectProjectConfigDatabase(startPath)
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-bootstrap-inspect", err)
	}
	if problems := validateProjectConfigAtlasObservation(
		bootstrapStatus,
		bootstrapDatabase,
		projectConfigExpectedBootstrapStatusFacts(),
		projectConfigExpectedBootstrapDatabaseFacts(),
		[]string{projectConfigAtlasProducer},
	); len(problems) != 0 {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-bootstrap-guard",
			fmt.Errorf("atlas CE did not create the expected brownfield start: %s", strings.Join(problems, "; ")),
		)
	}

	if err := cloneProjectConfigDatabase(startPath, controlPath); err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "clone-control", err)
	}
	if err := cloneProjectConfigDatabase(startPath, candidatePath); err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "clone-candidate", err)
	}

	if _, err := projectConfigApply(atlasBin, root, controlPath, ""); err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-control-apply", err)
	}
	if _, err := projectConfigApply(ptahBin, root, candidatePath, ""); err != nil {
		return projectConfigStatusGap("ptah-apply", err.Error())
	}

	controlStatus, err := projectConfigStatus(atlasBin, root, controlPath)
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-control-status", err)
	}
	controlDatabase, err := inspectProjectConfigDatabase(controlPath)
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-control-inspect", err)
	}
	if problems := validateProjectConfigAtlasObservation(
		controlStatus,
		controlDatabase,
		projectConfigExpectedFinalStatusFacts(),
		projectConfigExpectedFinalDatabaseFacts(),
		[]string{projectConfigAtlasProducer, projectConfigAtlasProducer},
	); len(problems) != 0 {
		return migrateRuntimeFail(
			projectConfigStatusFixture,
			"atlas-control-guard",
			fmt.Errorf("atlas CE control did not produce the expected final state: %s", strings.Join(problems, "; ")),
		)
	}

	problems := make([]string, 0, 8)
	ptahStatus, ptahStatusErr := projectConfigStatus(ptahBin, root, candidatePath)
	if ptahStatusErr != nil {
		problems = append(problems, "Ptah candidate status: "+ptahStatusErr.Error())
	}
	atlasCandidateStatus, atlasCandidateStatusErr := projectConfigStatus(atlasBin, root, candidatePath)
	if atlasCandidateStatusErr != nil {
		problems = append(problems, "Atlas CE reading Ptah candidate status: "+atlasCandidateStatusErr.Error())
	}
	candidateDatabase, candidateDatabaseErr := inspectProjectConfigDatabase(candidatePath)
	if candidateDatabaseErr != nil {
		problems = append(problems, "inspect Ptah candidate database: "+candidateDatabaseErr.Error())
	}
	if candidateDatabaseErr == nil {
		problems = append(problems, compareProjectConfigDatabaseFacts(controlDatabase, candidateDatabase)...)
		if ptahStatusErr == nil {
			problems = append(
				problems,
				compareProjectConfigStatusFacts("Ptah", controlStatus, ptahStatus)...,
			)
			problems = append(
				problems,
				projectConfigStatusMetadataProblems(
					"Ptah",
					ptahStatus,
					candidateDatabase.Revisions,
					[]string{projectConfigAtlasProducer, projectConfigPtahProducer},
				)...,
			)
		}
		if atlasCandidateStatusErr == nil {
			problems = append(
				problems,
				compareProjectConfigStatusFacts("Atlas CE reading Ptah", controlStatus, atlasCandidateStatus)...,
			)
			problems = append(
				problems,
				projectConfigStatusMetadataProblems(
					"Atlas CE reading Ptah",
					atlasCandidateStatus,
					candidateDatabase.Revisions,
					[]string{projectConfigAtlasProducer, projectConfigPtahProducer},
				)...,
			)
		}
		problems = append(
			problems,
			projectConfigRevisionMetadataProblems(
				"Ptah candidate",
				candidateDatabase.Revisions,
				[]string{projectConfigAtlasProducer, projectConfigPtahProducer},
			)...,
		)
	}
	if len(problems) != 0 {
		return projectConfigStatusGap("compare", strings.Join(problems, "; "))
	}

	return Result{
		Probe:   migrateRuntimeProbeName,
		Fixture: projectConfigStatusFixture,
		Stage:   "compare",
		Outcome: OK,
		Detail: "Atlas CE created a one-migration brownfield database, Atlas CE and Ptah independently applied " +
			"the remainder from untouched atlas.hcl clones, and status facts, end schema, stable full revision " +
			"metadata, semantic dynamic metadata, and Atlas CE reading Ptah timestamps all matched the Atlas control",
	}
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
