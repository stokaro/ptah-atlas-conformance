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

func TestProjectConfigProducer_HappyPath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "Atlas CE", value: "Atlas CLI v1.2.0", want: projectConfigAtlasProducer},
		{name: "Atlas CE prerelease", value: "Atlas CLI v1.2.0-rc.1", want: projectConfigAtlasProducer},
		{name: "Ptah", value: "Ptah", want: projectConfigPtahProducer},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(projectConfigProducer(test.value), qt.Equals, test.want)
		})
	}
}

func TestProjectConfigProducer_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name  string
		value string
	}{
		{name: "unknown", value: "other"},
		{name: "Atlas prefix", value: "other Atlas CLI v1.2.0"},
		{name: "Atlas suffix", value: "Atlas CLI v1.2.0 other"},
		{name: "Ptah suffix", value: "Ptah development"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(projectConfigProducer(test.value), qt.Equals, "")
		})
	}
}

func TestAtlasVersionMatchesPin_HappyPath(t *testing.T) {
	c := qt.New(t)

	c.Assert(atlasVersionMatchesPin("atlas community version v1.2.0", "v1.2.0"), qt.IsTrue)
}

func TestAtlasVersionMatchesPin_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name     string
		observed string
		pinned   string
	}{
		{name: "empty pin", observed: "atlas community version v1.2.0"},
		{name: "wrong version", observed: "atlas community version v1.1.0", pinned: "v1.2.0"},
		{name: "untrusted prefix", observed: "wrapper atlas community version v1.2.0", pinned: "v1.2.0"},
		{name: "trailing text", observed: "atlas community version v1.2.0 modified", pinned: "v1.2.0"},
		{name: "official binary spelling", observed: "atlas version v1.2.0", pinned: "v1.2.0"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(atlasVersionMatchesPin(test.observed, test.pinned), qt.IsFalse)
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
		[]projectConfigRevisionRuntimeExpectation{
			{
				producer: projectConfigPtahProducer,
				window: projectConfigApplyWindow{
					startedAt:  time.Date(2026, time.July, 30, 0, 19, 41, 0, time.UTC),
					finishedAt: time.Date(2026, time.July, 30, 0, 19, 43, 0, time.UTC),
				},
			},
		},
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
		[]projectConfigRevisionRuntimeExpectation{
			{
				producer: projectConfigPtahProducer,
				window: projectConfigApplyWindow{
					startedAt:  time.Date(2026, time.July, 30, 0, 19, 41, 0, time.UTC),
					finishedAt: time.Date(2026, time.July, 30, 0, 19, 43, 0, time.UTC),
				},
			},
		},
	)

	c.Assert(problems, qt.HasLen, 1)
	c.Assert(problems[0], qt.Equals, "Atlas CE reading Ptah revision 2 status timestamp is outside its measured apply window")
}

func TestProjectConfigTimestampIsInApplyWindow(t *testing.T) {
	c := qt.New(t)
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Second)
	window := projectConfigApplyWindow{startedAt: startedAt, finishedAt: finishedAt}
	tests := []struct {
		name  string
		value time.Time
		want  bool
	}{
		{
			name:  "lower tolerance boundary",
			value: startedAt.Add(-projectConfigDynamicMetadataTimeLag),
			want:  true,
		},
		{name: "inside", value: startedAt.Add(500 * time.Millisecond), want: true},
		{
			name:  "upper tolerance boundary",
			value: finishedAt.Add(projectConfigDynamicMetadataTimeLag),
			want:  true,
		},
		{
			name:  "stale",
			value: startedAt.Add(-projectConfigDynamicMetadataTimeLag - time.Nanosecond),
			want:  false,
		},
		{
			name:  "future",
			value: finishedAt.Add(projectConfigDynamicMetadataTimeLag + time.Nanosecond),
			want:  false,
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(projectConfigTimestampIsInApplyWindow(test.value, window), qt.Equals, test.want)
		})
	}
}

func TestProjectConfigExecutionTimeProblems_HappyPath(t *testing.T) {
	c := qt.New(t)
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expectation := projectConfigRevisionRuntimeExpectation{
		window: projectConfigApplyWindow{
			startedAt:  startedAt,
			finishedAt: startedAt.Add(100 * time.Millisecond),
		},
		minimumExecutionTime: time.Millisecond,
	}

	problems := projectConfigExecutionTimeProblems("revision", int64(50*time.Millisecond), expectation)

	c.Assert(problems, qt.HasLen, 0)
}

func TestProjectConfigExecutionTimeProblems_FailurePath(t *testing.T) {
	c := qt.New(t)
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expectation := projectConfigRevisionRuntimeExpectation{
		window: projectConfigApplyWindow{
			startedAt:  startedAt,
			finishedAt: startedAt.Add(100 * time.Millisecond),
		},
		minimumExecutionTime: time.Millisecond,
	}

	tooShort := projectConfigExecutionTimeProblems("revision", int64(time.Millisecond-time.Nanosecond), expectation)
	tooLong := projectConfigExecutionTimeProblems("revision", int64(201*time.Millisecond), expectation)

	c.Assert(tooShort, qt.DeepEquals, []string{
		"revision execution time = 999.999µs, want at least 1ms",
	})
	c.Assert(tooLong, qt.DeepEquals, []string{
		"revision execution time = 201ms, exceeds measured apply window 200ms",
	})
}

func TestProjectConfigExecutionTimelineProblems_HappyPath(t *testing.T) {
	c := qt.New(t)
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expectation := projectConfigRevisionRuntimeExpectation{
		window: projectConfigApplyWindow{
			startedAt:  startedAt,
			finishedAt: startedAt.Add(100 * time.Millisecond),
		},
	}

	problems := projectConfigExecutionTimelineProblems(
		"revision",
		startedAt,
		50*time.Millisecond,
		expectation,
	)

	c.Assert(problems, qt.HasLen, 0)
}

func TestProjectConfigExecutionTimelineProblems_FailurePath(t *testing.T) {
	c := qt.New(t)
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expectation := projectConfigRevisionRuntimeExpectation{
		window: projectConfigApplyWindow{
			startedAt:  startedAt,
			finishedAt: startedAt.Add(100 * time.Millisecond),
		},
	}

	problems := projectConfigExecutionTimelineProblems(
		"revision",
		startedAt.Add(100*time.Millisecond),
		101*time.Millisecond,
		expectation,
	)

	c.Assert(problems, qt.DeepEquals, []string{
		"revision implied finish 2026-07-30T12:00:00.201Z exceeds measured apply finish 2026-07-30T12:00:00.2Z",
	})
}

func TestProjectConfigRevisionMetadataProblems_AllowsAtlasWriteOrderTiming(t *testing.T) {
	c := qt.New(t)
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(100 * time.Millisecond)
	revisions := []projectConfigRevisionMetadata{
		{
			Version:         "2",
			ExecutedAt:      finishedAt.Format(time.RFC3339Nano),
			ExecutionTime:   int64(150 * time.Millisecond),
			OperatorVersion: "Atlas CLI v1.2.0",
		},
	}
	expectations := []projectConfigRevisionRuntimeExpectation{
		{
			producer:             projectConfigAtlasProducer,
			window:               projectConfigApplyWindow{startedAt: startedAt, finishedAt: finishedAt},
			minimumExecutionTime: time.Nanosecond,
		},
	}

	problems := projectConfigRevisionMetadataProblems("Atlas CE", revisions, expectations)

	c.Assert(problems, qt.HasLen, 0)
}
