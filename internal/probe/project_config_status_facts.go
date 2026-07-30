package probe

import (
	"encoding/json"
	"fmt"
	"slices"
)

type projectConfigStatusFile struct {
	Name        string
	Version     string
	Description string
}

type projectConfigStatusRevision struct {
	Version         string
	Description     string
	Type            string
	Applied         int64
	Total           int64
	ExecutedAt      string
	ExecutionTime   *int64
	OperatorVersion string
}

type projectConfigStatusFacts struct {
	Available []projectConfigStatusFile
	Applied   []projectConfigStatusRevision
	Pending   []projectConfigStatusFile
	Current   string
	Next      string
	Status    string
}

type projectConfigStableStatusRevision struct {
	Version     string
	Description string
	Type        string
	Applied     int64
	Total       int64
}

type projectConfigStableStatusFacts struct {
	Available []projectConfigStatusFile
	Applied   []projectConfigStableStatusRevision
	Pending   []projectConfigStatusFile
	Current   string
	Next      string
	Status    string
}

type projectConfigRevisionMetadata struct {
	Version                    string
	Description                string
	Type                       int64
	Applied                    int64
	Total                      int64
	ExecutedAt                 string
	ExecutedAtStorageClass     string
	ExecutionTime              int64
	ErrorIsNull                bool
	Error                      string
	ErrorStorageClass          string
	ErrorStatementIsNull       bool
	ErrorStatement             string
	ErrorStatementStorageClass string
	Hash                       string
	PartialHashesIsNull        bool
	PartialHashes              string
	PartialHashesStorageClass  string
	OperatorVersion            string
}

type projectConfigStableRevisionMetadata struct {
	Version                    string
	Description                string
	Type                       int64
	Applied                    int64
	Total                      int64
	ExecutedAtStorageClass     string
	ErrorIsNull                bool
	Error                      string
	ErrorStorageClass          string
	ErrorStatementIsNull       bool
	ErrorStatement             string
	ErrorStatementStorageClass string
	Hash                       string
	PartialHashesIsNull        bool
	PartialHashes              string
	PartialHashesStorageClass  string
}

type projectConfigSchemaObjectFact struct {
	Type  string
	Name  string
	Table string
}

type projectConfigSchemaColumnFact struct {
	Table         string
	Position      int64
	Name          string
	Type          string
	NotNull       bool
	DefaultIsNull bool
	Default       string
	PrimaryKey    int64
	Hidden        int64
}

type projectConfigDatabaseFacts struct {
	Objects   []projectConfigSchemaObjectFact
	Columns   []projectConfigSchemaColumnFact
	Revisions []projectConfigRevisionMetadata
}

func projectConfigExpectedBootstrapStatusFacts() projectConfigStableStatusFacts {
	return projectConfigStableStatusFacts{
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
		Applied: []projectConfigStableStatusRevision{
			{
				Version:     "20260719010000",
				Description: "create_users",
				Type:        "applied",
				Applied:     1,
				Total:       1,
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

func projectConfigExpectedFinalStatusFacts() projectConfigStableStatusFacts {
	return projectConfigStableStatusFacts{
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
		Applied: []projectConfigStableStatusRevision{
			{
				Version:     "20260719010000",
				Description: "create_users",
				Type:        "applied",
				Applied:     1,
				Total:       1,
			},
			{
				Version:     "20260719010101",
				Description: "add_email",
				Type:        "applied",
				Applied:     3,
				Total:       3,
			},
		},
		Pending: []projectConfigStatusFile{},
		Current: "20260719010101",
		Next:    "Already at latest version",
		Status:  "OK",
	}
}

func projectConfigExpectedBootstrapDatabaseFacts() projectConfigDatabaseFacts {
	return projectConfigDatabaseFacts{
		Objects: []projectConfigSchemaObjectFact{
			{Type: "table", Name: "users", Table: "users"},
		},
		Columns: []projectConfigSchemaColumnFact{
			{
				Table:         "users",
				Name:          "id",
				Type:          "INTEGER",
				DefaultIsNull: true,
				PrimaryKey:    1,
			},
		},
		Revisions: []projectConfigRevisionMetadata{
			projectConfigExpectedRevision(
				"20260719010000",
				"create_users",
				"xANbVwwQ0lhvq3faTPPDXbRZ+jffdTnZzv2IgEiH00Q=",
				1,
			),
		},
	}
}

func projectConfigExpectedFinalDatabaseFacts() projectConfigDatabaseFacts {
	return projectConfigDatabaseFacts{
		Objects: []projectConfigSchemaObjectFact{
			{Type: "table", Name: "users", Table: "users"},
		},
		Columns: []projectConfigSchemaColumnFact{
			{
				Table:         "users",
				Name:          "id",
				Type:          "INTEGER",
				DefaultIsNull: true,
				PrimaryKey:    1,
			},
			{
				Table:         "users",
				Position:      1,
				Name:          "email",
				Type:          "TEXT",
				DefaultIsNull: true,
			},
		},
		Revisions: []projectConfigRevisionMetadata{
			projectConfigExpectedRevision(
				"20260719010000",
				"create_users",
				"xANbVwwQ0lhvq3faTPPDXbRZ+jffdTnZzv2IgEiH00Q=",
				1,
			),
			projectConfigExpectedRevision(
				"20260719010101",
				"add_email",
				"u25T9Ckm3YWsejluAv488jadAP98IruCAi50hGWmuPo=",
				3,
			),
		},
	}
}

func projectConfigExpectedRevision(version, description, hash string, statements int64) projectConfigRevisionMetadata {
	return projectConfigRevisionMetadata{
		Version:                    version,
		Description:                description,
		Type:                       2,
		Applied:                    statements,
		Total:                      statements,
		ExecutedAtStorageClass:     "text",
		ErrorStorageClass:          "text",
		ErrorStatementStorageClass: "text",
		Hash:                       hash,
		PartialHashes:              "null",
		PartialHashesStorageClass:  "blob",
	}
}

func parseProjectConfigStatusFacts(output string) (projectConfigStatusFacts, error) {
	var document projectConfigStatusFacts
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		return projectConfigStatusFacts{}, fmt.Errorf("decode migrate status JSON: %w: %s", err, oneLine(output))
	}
	return document, nil
}

func stableProjectConfigStatusFacts(facts projectConfigStatusFacts) projectConfigStableStatusFacts {
	stableApplied := make([]projectConfigStableStatusRevision, len(facts.Applied))
	for i, revision := range facts.Applied {
		stableApplied[i] = projectConfigStableStatusRevision{
			Version:     revision.Version,
			Description: revision.Description,
			Type:        revision.Type,
			Applied:     revision.Applied,
			Total:       revision.Total,
		}
	}
	return projectConfigStableStatusFacts{
		Available: slices.Clone(facts.Available),
		Applied:   stableApplied,
		Pending:   slices.Clone(facts.Pending),
		Current:   facts.Current,
		Next:      facts.Next,
		Status:    facts.Status,
	}
}

func stableProjectConfigRevisions(
	revisions []projectConfigRevisionMetadata,
) []projectConfigStableRevisionMetadata {
	stable := make([]projectConfigStableRevisionMetadata, len(revisions))
	for i, revision := range revisions {
		stable[i] = projectConfigStableRevisionMetadata{
			Version:                    revision.Version,
			Description:                revision.Description,
			Type:                       revision.Type,
			Applied:                    revision.Applied,
			Total:                      revision.Total,
			ExecutedAtStorageClass:     revision.ExecutedAtStorageClass,
			ErrorIsNull:                revision.ErrorIsNull,
			Error:                      revision.Error,
			ErrorStorageClass:          revision.ErrorStorageClass,
			ErrorStatementIsNull:       revision.ErrorStatementIsNull,
			ErrorStatement:             revision.ErrorStatement,
			ErrorStatementStorageClass: revision.ErrorStatementStorageClass,
			Hash:                       revision.Hash,
			PartialHashesIsNull:        revision.PartialHashesIsNull,
			PartialHashes:              revision.PartialHashes,
			PartialHashesStorageClass:  revision.PartialHashesStorageClass,
		}
	}
	return stable
}
