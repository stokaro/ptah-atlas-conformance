package probe

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var projectConfigAtlasProducerPattern = regexp.MustCompile(
	`^Atlas CLI v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`,
)

type projectConfigRevisionRuntimeExpectation struct {
	producer             string
	window               projectConfigApplyWindow
	minimumExecutionTime time.Duration
	validateTimeline     bool
}

func validateProjectConfigAtlasObservation(
	status projectConfigStatusFacts,
	database projectConfigDatabaseFacts,
	expectedStatus projectConfigStableStatusFacts,
	expectedDatabase projectConfigDatabaseFacts,
	expectedRuntime []projectConfigRevisionRuntimeExpectation,
) []string {
	var problems []string
	if got := stableProjectConfigStatusFacts(status); !equalProjectConfigStableStatus(got, expectedStatus) {
		problems = append(problems, projectConfigStatusDifferences("status", expectedStatus, got)...)
	}
	if !slices.Equal(database.Objects, expectedDatabase.Objects) {
		problems = append(problems, fmt.Sprintf("schema objects = %v, want %v", database.Objects, expectedDatabase.Objects))
	}
	if !slices.Equal(database.Columns, expectedDatabase.Columns) {
		problems = append(problems, fmt.Sprintf("schema columns = %v, want %v", database.Columns, expectedDatabase.Columns))
	}
	gotRevisions := stableProjectConfigRevisions(database.Revisions)
	wantRevisions := stableProjectConfigRevisions(expectedDatabase.Revisions)
	problems = append(problems, projectConfigRevisionDifferences("stable revision metadata", wantRevisions, gotRevisions)...)
	problems = append(
		problems,
		projectConfigStatusMetadataProblems("Atlas CE", status, database.Revisions, expectedRuntime)...,
	)
	problems = append(
		problems,
		projectConfigRevisionMetadataProblems("Atlas CE", database.Revisions, expectedRuntime)...,
	)
	return problems
}

func equalProjectConfigStableStatus(left, right projectConfigStableStatusFacts) bool {
	return slices.Equal(left.Available, right.Available) &&
		slices.Equal(left.Applied, right.Applied) &&
		slices.Equal(left.Pending, right.Pending) &&
		left.Current == right.Current &&
		left.Next == right.Next &&
		left.Status == right.Status
}

func compareProjectConfigStatusFacts(
	reader string,
	atlasFacts, candidateFacts projectConfigStatusFacts,
) []string {
	atlasStable := stableProjectConfigStatusFacts(atlasFacts)
	candidateStable := stableProjectConfigStatusFacts(candidateFacts)
	if equalProjectConfigStableStatus(candidateStable, atlasStable) {
		return nil
	}
	return projectConfigStatusDifferences(reader+" stable status", atlasStable, candidateStable)
}

func compareProjectConfigDatabaseFacts(
	atlasFacts, candidateFacts projectConfigDatabaseFacts,
) []string {
	var problems []string
	if !slices.Equal(candidateFacts.Objects, atlasFacts.Objects) {
		problems = append(
			problems,
			fmt.Sprintf("Ptah schema objects = %v, Atlas CE control objects = %v", candidateFacts.Objects, atlasFacts.Objects),
		)
	}
	if !slices.Equal(candidateFacts.Columns, atlasFacts.Columns) {
		problems = append(
			problems,
			fmt.Sprintf("Ptah schema columns = %v, Atlas CE control columns = %v", candidateFacts.Columns, atlasFacts.Columns),
		)
	}
	atlasRevisions := stableProjectConfigRevisions(atlasFacts.Revisions)
	candidateRevisions := stableProjectConfigRevisions(candidateFacts.Revisions)
	problems = append(
		problems,
		projectConfigRevisionDifferences("Ptah stable full revision metadata", atlasRevisions, candidateRevisions)...,
	)
	return problems
}

func projectConfigStatusDifferences(
	label string,
	want, got projectConfigStableStatusFacts,
) []string {
	var differences []string
	if !slices.Equal(got.Available, want.Available) {
		differences = append(differences, fmt.Sprintf("available=%v, Atlas=%v", got.Available, want.Available))
	}
	if !slices.Equal(got.Applied, want.Applied) {
		differences = append(differences, fmt.Sprintf("applied=%v, Atlas=%v", got.Applied, want.Applied))
	}
	if !slices.Equal(got.Pending, want.Pending) {
		differences = append(differences, fmt.Sprintf("pending=%v, Atlas=%v", got.Pending, want.Pending))
	}
	if got.Current != want.Current {
		differences = append(differences, fmt.Sprintf("current=%q, Atlas=%q", got.Current, want.Current))
	}
	if got.Next != want.Next {
		differences = append(differences, fmt.Sprintf("next=%q, Atlas=%q", got.Next, want.Next))
	}
	if got.Status != want.Status {
		differences = append(differences, fmt.Sprintf("status=%q, Atlas=%q", got.Status, want.Status))
	}
	if len(differences) == 0 {
		return nil
	}
	return []string{label + " differs: " + strings.Join(differences, ", ")}
}

func projectConfigRevisionDifferences(
	label string,
	want, got []projectConfigStableRevisionMetadata,
) []string {
	var differences []string
	if len(got) != len(want) {
		differences = append(differences, fmt.Sprintf("%s count=%d, Atlas=%d", label, len(got), len(want)))
	}
	for i := range min(len(got), len(want)) {
		fields := projectConfigRevisionFieldDifferences(want[i], got[i])
		if len(fields) != 0 {
			differences = append(
				differences,
				fmt.Sprintf("%s revision %s differs: %s", label, got[i].Version, strings.Join(fields, ", ")),
			)
		}
	}
	return differences
}

func projectConfigRevisionFieldDifferences(
	want, got projectConfigStableRevisionMetadata,
) []string {
	var differences []string
	if got.Version != want.Version {
		differences = append(differences, fmt.Sprintf("version=%q, Atlas=%q", got.Version, want.Version))
	}
	if got.Description != want.Description {
		differences = append(differences, fmt.Sprintf("description=%q, Atlas=%q", got.Description, want.Description))
	}
	if got.Type != want.Type {
		differences = append(differences, fmt.Sprintf("type=%d, Atlas=%d", got.Type, want.Type))
	}
	if got.Applied != want.Applied {
		differences = append(differences, fmt.Sprintf("applied=%d, Atlas=%d", got.Applied, want.Applied))
	}
	if got.Total != want.Total {
		differences = append(differences, fmt.Sprintf("total=%d, Atlas=%d", got.Total, want.Total))
	}
	if got.ExecutedAtStorageClass != want.ExecutedAtStorageClass {
		differences = append(
			differences,
			fmt.Sprintf(
				"executed_at storage class=%q, Atlas=%q",
				got.ExecutedAtStorageClass,
				want.ExecutedAtStorageClass,
			),
		)
	}
	if got.ErrorIsNull != want.ErrorIsNull {
		differences = append(differences, fmt.Sprintf("error SQL-null=%t, Atlas=%t", got.ErrorIsNull, want.ErrorIsNull))
	}
	if got.Error != want.Error {
		differences = append(differences, fmt.Sprintf("error=%q, Atlas=%q", got.Error, want.Error))
	}
	if got.ErrorStorageClass != want.ErrorStorageClass {
		differences = append(
			differences,
			fmt.Sprintf("error storage class=%q, Atlas=%q", got.ErrorStorageClass, want.ErrorStorageClass),
		)
	}
	if got.ErrorStatementIsNull != want.ErrorStatementIsNull {
		differences = append(
			differences,
			fmt.Sprintf("error_stmt SQL-null=%t, Atlas=%t", got.ErrorStatementIsNull, want.ErrorStatementIsNull),
		)
	}
	if got.ErrorStatement != want.ErrorStatement {
		differences = append(
			differences,
			fmt.Sprintf("error_stmt=%q, Atlas=%q", got.ErrorStatement, want.ErrorStatement),
		)
	}
	if got.ErrorStatementStorageClass != want.ErrorStatementStorageClass {
		differences = append(
			differences,
			fmt.Sprintf(
				"error_stmt storage class=%q, Atlas=%q",
				got.ErrorStatementStorageClass,
				want.ErrorStatementStorageClass,
			),
		)
	}
	if got.Hash != want.Hash {
		differences = append(differences, fmt.Sprintf("hash=%q, Atlas=%q", got.Hash, want.Hash))
	}
	if got.PartialHashesIsNull != want.PartialHashesIsNull {
		differences = append(
			differences,
			fmt.Sprintf("partial_hashes SQL-null=%t, Atlas=%t", got.PartialHashesIsNull, want.PartialHashesIsNull),
		)
	}
	if got.PartialHashes != want.PartialHashes {
		differences = append(
			differences,
			fmt.Sprintf("partial_hashes=%q, Atlas=%q", got.PartialHashes, want.PartialHashes),
		)
	}
	if got.PartialHashesStorageClass != want.PartialHashesStorageClass {
		differences = append(
			differences,
			fmt.Sprintf(
				"partial_hashes storage class=%q, Atlas=%q",
				got.PartialHashesStorageClass,
				want.PartialHashesStorageClass,
			),
		)
	}
	return differences
}

func projectConfigStatusMetadataProblems(
	reader string,
	status projectConfigStatusFacts,
	revisions []projectConfigRevisionMetadata,
	expectedRuntime []projectConfigRevisionRuntimeExpectation,
) []string {
	var problems []string
	if len(status.Applied) != len(revisions) {
		problems = append(
			problems,
			fmt.Sprintf("%s status applied revision count = %d, database count = %d", reader, len(status.Applied), len(revisions)),
		)
	}
	count := min(len(status.Applied), len(revisions), len(expectedRuntime))
	for i := range count {
		statusRevision := status.Applied[i]
		databaseRevision := revisions[i]
		expectation := expectedRuntime[i]
		prefix := fmt.Sprintf("%s revision %s", reader, databaseRevision.Version)
		statusTime, statusTimeErr := parseProjectConfigRevisionTime(statusRevision.ExecutedAt)
		databaseTime, databaseTimeErr := parseProjectConfigRevisionTime(databaseRevision.ExecutedAt)
		if statusTimeErr != nil {
			problems = append(problems, prefix+" status timestamp: "+statusTimeErr.Error())
		} else if !projectConfigTimestampIsInApplyWindow(statusTime, expectation.window) {
			problems = append(problems, prefix+" status timestamp is outside its measured apply window")
		}
		if statusTimeErr == nil && databaseTimeErr == nil && !statusTime.Equal(databaseTime) {
			problems = append(
				problems,
				fmt.Sprintf("%s status timestamp %s does not represent database timestamp %s", prefix, statusTime, databaseTime),
			)
		}
		if statusRevision.ExecutionTime == nil {
			problems = append(problems, prefix+" status execution time is missing")
		} else {
			executionTime := time.Duration(*statusRevision.ExecutionTime)
			problems = append(
				problems,
				projectConfigExecutionTimeProblems(prefix+" status", *statusRevision.ExecutionTime, expectation)...,
			)
			if statusTimeErr == nil && expectation.validateTimeline {
				problems = append(
					problems,
					projectConfigExecutionTimelineProblems(
						prefix+" status",
						statusTime,
						executionTime,
						expectation,
					)...,
				)
			}
			if *statusRevision.ExecutionTime != databaseRevision.ExecutionTime {
				problems = append(
					problems,
					fmt.Sprintf(
						"%s status execution time = %d, database execution time = %d",
						prefix,
						*statusRevision.ExecutionTime,
						databaseRevision.ExecutionTime,
					),
				)
			}
		}
		if statusRevision.OperatorVersion != databaseRevision.OperatorVersion {
			problems = append(
				problems,
				fmt.Sprintf(
					"%s status producer = %q, database producer = %q",
					prefix,
					statusRevision.OperatorVersion,
					databaseRevision.OperatorVersion,
				),
			)
		}
		if got := projectConfigProducer(statusRevision.OperatorVersion); got != expectation.producer {
			problems = append(
				problems,
				fmt.Sprintf("%s status producer class = %q, want %q", prefix, got, expectation.producer),
			)
		}
	}
	return problems
}

func projectConfigRevisionMetadataProblems(
	reader string,
	revisions []projectConfigRevisionMetadata,
	expectedRuntime []projectConfigRevisionRuntimeExpectation,
) []string {
	var problems []string
	if len(revisions) != len(expectedRuntime) {
		problems = append(
			problems,
			fmt.Sprintf("%s revision count = %d, runtime expectation count = %d", reader, len(revisions), len(expectedRuntime)),
		)
	}
	for i := range min(len(revisions), len(expectedRuntime)) {
		revision := revisions[i]
		expectation := expectedRuntime[i]
		prefix := fmt.Sprintf("%s revision %s", reader, revision.Version)
		executedAt, err := parseProjectConfigRevisionTime(revision.ExecutedAt)
		if err != nil {
			problems = append(problems, prefix+" timestamp: "+err.Error())
		} else if !projectConfigTimestampIsInApplyWindow(executedAt, expectation.window) {
			problems = append(problems, prefix+" timestamp is outside its measured apply window")
		} else if expectation.validateTimeline {
			problems = append(
				problems,
				projectConfigExecutionTimelineProblems(
					prefix,
					executedAt,
					time.Duration(revision.ExecutionTime),
					expectation,
				)...,
			)
		}
		problems = append(problems, projectConfigExecutionTimeProblems(prefix, revision.ExecutionTime, expectation)...)
		if got := projectConfigProducer(revision.OperatorVersion); got != expectation.producer {
			problems = append(
				problems,
				fmt.Sprintf("%s producer class = %q, want %q", prefix, got, expectation.producer),
			)
		}
	}
	return problems
}

func projectConfigProducer(value string) string {
	switch {
	case projectConfigAtlasProducerPattern.MatchString(value):
		return projectConfigAtlasProducer
	case value == "Ptah":
		return projectConfigPtahProducer
	default:
		return ""
	}
}

func parseProjectConfigRevisionTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, projectConfigTimestampLayout}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, strings.TrimSpace(value))
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("not an Atlas-readable timestamp")
}

func projectConfigTimestampIsInApplyWindow(value time.Time, window projectConfigApplyWindow) bool {
	return !value.Before(window.startedAt.Add(-projectConfigDynamicMetadataTimeLag)) &&
		!value.After(window.finishedAt.Add(projectConfigDynamicMetadataTimeLag))
}

func projectConfigExecutionTimeProblems(
	prefix string,
	value int64,
	expectation projectConfigRevisionRuntimeExpectation,
) []string {
	var problems []string
	executionTime := time.Duration(value)
	if executionTime < expectation.minimumExecutionTime {
		problems = append(
			problems,
			fmt.Sprintf(
				"%s execution time = %s, want at least %s",
				prefix,
				executionTime,
				expectation.minimumExecutionTime,
			),
		)
	}
	maximum := expectation.window.finishedAt.Sub(expectation.window.startedAt) +
		projectConfigDynamicMetadataTimeLag
	if executionTime > maximum {
		problems = append(
			problems,
			fmt.Sprintf("%s execution time = %s, exceeds measured apply window %s", prefix, executionTime, maximum),
		)
	}
	return problems
}

func projectConfigExecutionTimelineProblems(
	prefix string,
	executedAt time.Time,
	executionTime time.Duration,
	expectation projectConfigRevisionRuntimeExpectation,
) []string {
	latestFinish := expectation.window.finishedAt.Add(projectConfigDynamicMetadataTimeLag)
	impliedFinish := executedAt.Add(executionTime)
	if !impliedFinish.After(latestFinish) {
		return nil
	}
	return []string{
		fmt.Sprintf(
			"%s implied finish %s exceeds measured apply finish %s",
			prefix,
			impliedFinish.Format(time.RFC3339Nano),
			latestFinish.Format(time.RFC3339Nano),
		),
	}
}
