package probe

// White-box testing required: project-config status parsing, stable metadata
// comparison, and database fact extraction are unexported probe boundaries
// that cannot be observed through the public harness without launching both
// Atlas CE and Ptah.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
)

func TestParseProjectConfigStatusFacts_HappyPath(t *testing.T) {
	c := qt.New(t)
	input := `{
		"Available": [
			{
				"Name": "20260719010000_create_users.sql",
				"Version": "20260719010000",
				"Description": "create_users",
				"Type": ""
			}
		],
		"Applied": [
			{
				"Name": "",
				"Version": "20260719010000",
				"Description": "create_users",
				"Type": "manually set"
				,"Applied": 1
				,"Total": 1
				,"ExecutedAt": "2026-07-30T02:19:42.142241+02:00"
				,"ExecutionTime": 47875
				,"OperatorVersion": "Atlas CLI v1.2.0"
			}
		],
		"Pending": [],
		"Current": "20260719010000",
		"Next": "",
		"Status": "OK"
	}`

	got, err := parseProjectConfigStatusFacts(input)

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.DeepEquals, projectConfigStatusFacts{
		Available: []projectConfigStatusFile{
			{
				Name:        "20260719010000_create_users.sql",
				Version:     "20260719010000",
				Description: "create_users",
			},
		},
		Applied: []projectConfigStatusRevision{
			{
				Version:         "20260719010000",
				Description:     "create_users",
				Type:            "manually set",
				Applied:         1,
				Total:           1,
				ExecutedAt:      "2026-07-30T02:19:42.142241+02:00",
				ExecutionTime:   new(int64(47875)),
				OperatorVersion: "Atlas CLI v1.2.0",
			},
		},
		Pending: []projectConfigStatusFile{},
		Current: "20260719010000",
		Status:  "OK",
	})
}

func TestParseProjectConfigStatusFacts_FailurePath(t *testing.T) {
	c := qt.New(t)

	got, err := parseProjectConfigStatusFacts(`{"Available":`)

	c.Assert(err, qt.ErrorMatches, `decode migrate status JSON: unexpected end of JSON input: .*`)
	c.Assert(got, qt.DeepEquals, projectConfigStatusFacts{})
}

func TestStableProjectConfigStatusFacts(t *testing.T) {
	c := qt.New(t)

	got := stableProjectConfigStatusFacts(projectConfigStatusFacts{
		Available: []projectConfigStatusFile{
			{Name: "1_first.sql", Version: "1", Description: "first"},
		},
		Applied: []projectConfigStatusRevision{
			{
				Version:         "1",
				Description:     "first",
				Type:            "applied",
				Applied:         1,
				Total:           1,
				ExecutedAt:      "2026-07-30T00:00:00Z",
				ExecutionTime:   new(int64(42)),
				OperatorVersion: "Atlas CLI v1.2.0",
			},
		},
		Pending: []projectConfigStatusFile{},
		Current: "1",
		Next:    "Already at latest version",
		Status:  "OK",
	})

	c.Assert(got, qt.DeepEquals, projectConfigStableStatusFacts{
		Available: []projectConfigStatusFile{
			{Name: "1_first.sql", Version: "1", Description: "first"},
		},
		Applied: []projectConfigStableStatusRevision{
			{Version: "1", Description: "first", Type: "applied", Applied: 1, Total: 1},
		},
		Pending: []projectConfigStatusFile{},
		Current: "1",
		Next:    "Already at latest version",
		Status:  "OK",
	})
}

func TestParseProjectConfigRevisionTime_HappyPath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name  string
		value string
		want  time.Time
	}{
		{
			name:  "status RFC3339 timestamp",
			value: "2026-07-30T02:19:42.142241+02:00",
			want:  time.Date(2026, time.July, 30, 2, 19, 42, 142241000, time.FixedZone("", 2*60*60)),
		},
		{
			name:  "SQLite Atlas timestamp",
			value: "2026-07-30 02:19:42.142241+02:00",
			want:  time.Date(2026, time.July, 30, 2, 19, 42, 142241000, time.FixedZone("", 2*60*60)),
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got, err := parseProjectConfigRevisionTime(test.value)
			c.Assert(err, qt.IsNil)
			c.Assert(got.Equal(test.want), qt.IsTrue)
		})
	}
}

func TestParseProjectConfigRevisionTime_FailurePath(t *testing.T) {
	c := qt.New(t)

	got, err := parseProjectConfigRevisionTime("2026-07-30 02:24:00 +0200 CEST m=+0.01")

	c.Assert(err, qt.ErrorMatches, `not an Atlas-readable timestamp`)
	c.Assert(got.IsZero(), qt.IsTrue)
}

func TestProjectConfigProducer(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "Atlas CE", value: "Atlas CLI v1.2.0", want: projectConfigAtlasProducer},
		{name: "Ptah", value: "Ptah", want: projectConfigPtahProducer},
		{name: "unknown", value: "other", want: ""},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(projectConfigProducer(test.value), qt.Equals, test.want)
		})
	}
}

func TestCanonicalProjectConfigJSON_HappyPath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name    string
		value   string
		sqlNull bool
		want    string
	}{
		{name: "JSON null", value: " null\n", want: "null"},
		{name: "JSON array", value: `[ "one", "two" ]`, want: `["one","two"]`},
		{name: "SQL null", value: "", sqlNull: true, want: ""},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got, err := canonicalProjectConfigJSON(test.value, test.sqlNull)
			c.Assert(err, qt.IsNil)
			c.Assert(got, qt.Equals, test.want)
		})
	}
}

func TestCanonicalProjectConfigJSON_FailurePath(t *testing.T) {
	c := qt.New(t)

	got, err := canonicalProjectConfigJSON("{", false)

	c.Assert(err, qt.ErrorMatches, "unexpected end of JSON input")
	c.Assert(got, qt.Equals, "")
}

func TestProjectConfigRevisionDifferences(t *testing.T) {
	c := qt.New(t)
	want := []projectConfigStableRevisionMetadata{
		{
			Version:                    "2",
			Description:                "add_email",
			Type:                       2,
			Applied:                    1,
			Total:                      1,
			ExecutedAtStorageClass:     "text",
			ErrorStorageClass:          "text",
			ErrorStatementStorageClass: "text",
			Hash:                       "hash",
			PartialHashes:              "null",
			PartialHashesStorageClass:  "blob",
		},
	}
	got := []projectConfigStableRevisionMetadata{
		{
			Version:                    "2",
			Description:                "Add Email",
			Type:                       2,
			Applied:                    1,
			Total:                      1,
			ExecutedAtStorageClass:     "blob",
			ErrorIsNull:                true,
			ErrorStorageClass:          "blob",
			ErrorStatementIsNull:       true,
			ErrorStatementStorageClass: "blob",
			Hash:                       "hash",
			PartialHashesIsNull:        true,
			PartialHashesStorageClass:  "text",
		},
	}

	differences := projectConfigRevisionDifferences("Ptah metadata", want, got)

	c.Assert(differences, qt.DeepEquals, []string{
		`Ptah metadata revision 2 differs: description="Add Email", Atlas="add_email", ` +
			`executed_at storage class="blob", Atlas="text", error SQL-null=true, Atlas=false, ` +
			`error storage class="blob", Atlas="text", error_stmt SQL-null=true, Atlas=false, ` +
			`error_stmt storage class="blob", Atlas="text", partial_hashes SQL-null=true, Atlas=false, ` +
			`partial_hashes="", Atlas="null", partial_hashes storage class="text", Atlas="blob"`,
	})
}

func TestProjectConfigRevisionMetadataFactsPreservesSQLiteStorageClasses(t *testing.T) {
	c := qt.New(t)
	db, err := openSQLiteRuntimeDB(filepath.Join(t.TempDir(), "revision-metadata.db"))
	c.Assert(err, qt.IsNil)
	t.Cleanup(func() {
		c.Check(db.Close(), qt.IsNil)
	})
	_, err = db.ExecContext(context.Background(), `
CREATE TABLE atlas_schema_revisions (
    version TEXT,
    description TEXT,
    type INTEGER,
    applied INTEGER,
    total INTEGER,
    executed_at,
    execution_time INTEGER,
    error,
    error_stmt,
    hash TEXT,
    partial_hashes,
    operator_version TEXT
)`)
	c.Assert(err, qt.IsNil)
	_, err = db.ExecContext(context.Background(), `
INSERT INTO atlas_schema_revisions (
    version,
    description,
    type,
    applied,
    total,
    executed_at,
    execution_time,
    error,
    error_stmt,
    hash,
    partial_hashes,
    operator_version
) VALUES (
    '2',
    'add_email',
    2,
    1,
    1,
    CAST('2026-07-30 02:19:42.142241+02:00' AS TEXT),
    42,
    CAST('' AS TEXT),
    CAST('' AS TEXT),
    'hash',
    CAST('null' AS BLOB),
    'Atlas CLI v1.2.0'
)`)
	c.Assert(err, qt.IsNil)

	got, err := projectConfigRevisionMetadataFacts(db)

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.DeepEquals, []projectConfigRevisionMetadata{
		{
			Version:                    "2",
			Description:                "add_email",
			Type:                       2,
			Applied:                    1,
			Total:                      1,
			ExecutedAt:                 "2026-07-30 02:19:42.142241+02:00",
			ExecutedAtStorageClass:     "text",
			ExecutionTime:              42,
			Error:                      "",
			ErrorStorageClass:          "text",
			ErrorStatement:             "",
			ErrorStatementStorageClass: "text",
			Hash:                       "hash",
			PartialHashes:              "null",
			PartialHashesStorageClass:  "blob",
			OperatorVersion:            "Atlas CLI v1.2.0",
		},
	})
}

func TestProjectConfigStatusMetadataProblems_HappyPath(t *testing.T) {
	c := qt.New(t)
	executionTime := int64(42)
	status := projectConfigStatusFacts{
		Applied: []projectConfigStatusRevision{
			{
				Version:         "2",
				ExecutedAt:      "2026-07-30T02:19:42.142241+02:00",
				ExecutionTime:   &executionTime,
				OperatorVersion: "Ptah",
			},
		},
	}
	revisions := []projectConfigRevisionMetadata{
		{
			Version:         "2",
			ExecutedAt:      "2026-07-30 02:19:42.142241+02:00",
			ExecutionTime:   42,
			OperatorVersion: "Ptah",
		},
	}

	problems := projectConfigStatusMetadataProblems(
		"Atlas CE reading Ptah",
		status,
		revisions,
		[]string{projectConfigPtahProducer},
	)

	c.Assert(problems, qt.HasLen, 0)
}

func TestProjectConfigStatusMetadataProblems_FailurePath(t *testing.T) {
	c := qt.New(t)
	executionTime := int64(42)
	status := projectConfigStatusFacts{
		Applied: []projectConfigStatusRevision{
			{
				Version:         "2",
				ExecutedAt:      "0001-01-01T00:00:00Z",
				ExecutionTime:   &executionTime,
				OperatorVersion: "Ptah",
			},
		},
	}
	revisions := []projectConfigRevisionMetadata{
		{
			Version:         "2",
			ExecutedAt:      "2026-07-30 02:24:00 +0200 CEST m=+0.01",
			ExecutionTime:   42,
			OperatorVersion: "Ptah",
		},
	}

	problems := projectConfigStatusMetadataProblems(
		"Atlas CE reading Ptah",
		status,
		revisions,
		[]string{projectConfigPtahProducer},
	)

	c.Assert(problems, qt.HasLen, 1)
	c.Assert(problems[0], qt.Equals, "Atlas CE reading Ptah revision 2 status timestamp is outside the plausible runtime range")
}
