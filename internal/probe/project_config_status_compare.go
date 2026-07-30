package probe

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

func validateProjectConfigAtlasObservation(
	status projectConfigStatusFacts,
	database projectConfigDatabaseFacts,
	expectedStatus projectConfigStableStatusFacts,
	expectedDatabase projectConfigDatabaseFacts,
	expectedProducers []string,
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
		projectConfigStatusMetadataProblems("Atlas CE", status, database.Revisions, expectedProducers)...,
	)
	problems = append(
		problems,
		projectConfigRevisionMetadataProblems("Atlas CE", database.Revisions, expectedProducers)...,
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
	expectedProducers []string,
) []string {
	var problems []string
	if len(status.Applied) != len(revisions) {
		problems = append(
			problems,
			fmt.Sprintf("%s status applied revision count = %d, database count = %d", reader, len(status.Applied), len(revisions)),
		)
	}
	count := min(len(status.Applied), len(revisions), len(expectedProducers))
	for i := range count {
		statusRevision := status.Applied[i]
		databaseRevision := revisions[i]
		prefix := fmt.Sprintf("%s revision %s", reader, databaseRevision.Version)
		statusTime, statusTimeErr := parseProjectConfigRevisionTime(statusRevision.ExecutedAt)
		databaseTime, databaseTimeErr := parseProjectConfigRevisionTime(databaseRevision.ExecutedAt)
		if statusTimeErr != nil {
			problems = append(problems, prefix+" status timestamp: "+statusTimeErr.Error())
		} else if !projectConfigTimestampIsPlausible(statusTime) {
			problems = append(problems, prefix+" status timestamp is outside the plausible runtime range")
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
			if *statusRevision.ExecutionTime < 0 {
				problems = append(problems, prefix+" status execution time is negative")
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
		if got := projectConfigProducer(statusRevision.OperatorVersion); got != expectedProducers[i] {
			problems = append(
				problems,
				fmt.Sprintf("%s status producer class = %q, want %q", prefix, got, expectedProducers[i]),
			)
		}
	}
	return problems
}

func projectConfigRevisionMetadataProblems(
	reader string,
	revisions []projectConfigRevisionMetadata,
	expectedProducers []string,
) []string {
	var problems []string
	if len(revisions) != len(expectedProducers) {
		problems = append(
			problems,
			fmt.Sprintf("%s revision count = %d, producer expectation count = %d", reader, len(revisions), len(expectedProducers)),
		)
	}
	for i := range min(len(revisions), len(expectedProducers)) {
		revision := revisions[i]
		prefix := fmt.Sprintf("%s revision %s", reader, revision.Version)
		executedAt, err := parseProjectConfigRevisionTime(revision.ExecutedAt)
		if err != nil {
			problems = append(problems, prefix+" timestamp: "+err.Error())
		} else if !projectConfigTimestampIsPlausible(executedAt) {
			problems = append(problems, prefix+" timestamp is outside the plausible runtime range")
		}
		if revision.ExecutionTime < 0 {
			problems = append(problems, prefix+" execution time is negative")
		}
		if got := projectConfigProducer(revision.OperatorVersion); got != expectedProducers[i] {
			problems = append(
				problems,
				fmt.Sprintf("%s producer class = %q, want %q", prefix, got, expectedProducers[i]),
			)
		}
	}
	return problems
}

func projectConfigProducer(value string) string {
	switch {
	case strings.HasPrefix(value, "Atlas CLI "):
		return projectConfigAtlasProducer
	case strings.HasPrefix(value, "Ptah"):
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

func projectConfigTimestampIsPlausible(value time.Time) bool {
	return value.After(time.Unix(0, 0)) && value.Before(time.Now().Add(5*time.Minute))
}
