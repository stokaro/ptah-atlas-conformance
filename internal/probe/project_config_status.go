package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	projectConfigStatusFixture          = "sqlite/project-config-apply-oracle"
	projectConfigDatabaseEnv            = "PTAH_ATLAS_PROJECT_CONFIG_E2E_URL"
	projectConfigStatusFormat           = "{{ json . }}"
	projectConfigAtlasProducer          = "atlas"
	projectConfigPtahProducer           = "ptah"
	projectConfigTimestampLayout        = "2006-01-02 15:04:05.999999999Z07:00"
	projectConfigMinimumTimedExecution  = time.Millisecond
	projectConfigDynamicMetadataTimeLag = 100 * time.Millisecond
)

// atlasProjectConfigApplyOracle uses Atlas CE to create and finish a
// brownfield migration, then compares Ptah against an Atlas-controlled clone.
func atlasProjectConfigApplyOracle(ptahBin, atlasBin string) Result {
	atlasVersion, err := validatePinnedAtlasBinary(atlasBin)
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-version", err)
	}
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

	_, bootstrapWindow, err := projectConfigApply(atlasBin, root, startPath, "1")
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-bootstrap", err)
	}
	bootstrapRuntime := []projectConfigRevisionRuntimeExpectation{
		{
			producer:             projectConfigAtlasProducer,
			window:               bootstrapWindow,
			minimumExecutionTime: time.Nanosecond,
		},
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
		bootstrapRuntime,
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

	_, controlWindow, err := projectConfigApply(atlasBin, root, controlPath, "")
	if err != nil {
		return migrateRuntimeFail(projectConfigStatusFixture, "atlas-control-apply", err)
	}
	_, candidateWindow, err := projectConfigApply(ptahBin, root, candidatePath, "")
	if err != nil {
		return projectConfigStatusGap("ptah-apply", err.Error())
	}
	// Atlas CE v1.2.0 resets ExecutedAt on each revision write, so its final
	// duration can cover only write overhead. Ptah intentionally records the
	// migration start and full elapsed duration. Both producers must emit
	// timestamps and positive durations bounded by their command windows, but
	// only Ptah's fields describe one coherent interval. The timed fixture
	// additionally proves Ptah stores its full duration in nanoseconds rather
	// than copying Atlas's write-order artifact.
	controlRuntime := []projectConfigRevisionRuntimeExpectation{
		bootstrapRuntime[0],
		{
			producer:             projectConfigAtlasProducer,
			window:               controlWindow,
			minimumExecutionTime: time.Nanosecond,
		},
	}
	candidateRuntime := []projectConfigRevisionRuntimeExpectation{
		bootstrapRuntime[0],
		{
			producer:             projectConfigPtahProducer,
			window:               candidateWindow,
			minimumExecutionTime: projectConfigMinimumTimedExecution,
			validateTimeline:     true,
		},
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
		controlRuntime,
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
					candidateRuntime,
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
					candidateRuntime,
				)...,
			)
		}
		problems = append(
			problems,
			projectConfigRevisionMetadataProblems(
				"Ptah candidate",
				candidateDatabase.Revisions,
				candidateRuntime,
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
		Detail: atlasVersion + " created a one-migration brownfield database, Atlas CE and Ptah independently applied " +
			"the remainder from untouched atlas.hcl clones, and status facts, end schema, stable full revision " +
			"metadata, and storage classes matched the Atlas control; measured timing invariants, Ptah full-duration " +
			"nanosecond units, producer identity, and Atlas CE reading Ptah state all passed",
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
