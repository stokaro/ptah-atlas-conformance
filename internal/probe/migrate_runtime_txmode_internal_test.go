package probe

// White-box testing required: these tests pin the normalization and revision
// projection used by the private Atlas/Ptah process comparator. The behavior is
// otherwise observable only through the expensive live runtime report.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestFileTxModeStableError(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name:   "plain error",
			stderr: "Error: unknown txmode\n",
			want:   "unknown txmode",
		},
		{
			name:   "Atlas advisory after error",
			stderr: "Error: unknown txmode\nYou're running the community build of Atlas.\n",
			want:   "unknown txmode",
		},
		{
			name:   "no stable error line",
			stderr: "migration failed\n",
			want:   "",
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Assert(fileTxModeStableError(test.stderr), qt.Equals, test.want)
		})
	}
}

func TestFileTxModeRevisionVersions(t *testing.T) {
	c := qt.New(t)
	revisions := []projectConfigRevisionMetadata{
		{Version: "2", Applied: 0, Total: 0},
		{Version: "3", Applied: 1, Total: 1},
	}

	c.Assert(fileTxModeRevisionVersions(revisions), qt.DeepEquals, []string{"2", "3"})
}

func TestFileTxModeMatrixIssue(t *testing.T) {
	c := qt.New(t)

	c.Assert(fileTxModeMatrixIssue(fileTxModeMatrixCase{}), qt.Equals, fileTxModeIssue)
	c.Assert(fileTxModeMatrixIssue(fileTxModeMatrixCase{Issue: fileTxModeWhitespaceIssue}), qt.Equals,
		fileTxModeWhitespaceIssue)
}

func TestCompareRejectedFileTxModeSelection_AttributesCompatWrapper(t *testing.T) {
	c := qt.New(t)
	const want = `unknown txmode "bogus" found in file directive "2_invalid.sql"`
	observation := fileTxModeObservation{
		Process: integrityProcessResult{
			exitCode: 1,
			stderr:   "Error: error applying migrations: " + want + "\n",
		},
		Tables:    []string{"atlas_schema_revisions", "first_valid"},
		Revisions: []projectConfigRevisionMetadata{{Version: "1"}},
	}

	detail, issue := compareRejectedFileTxModeSelection("Ptah", observation, observation, want)

	c.Assert(detail, qt.Contains, "Ptah diagnostic")
	c.Assert(issue, qt.Equals, fileTxModeDiagnosticIssue)
}

func TestCompareFileTxModeBookkeeping_AggregatesEveryBodyExecutionCell(t *testing.T) {
	c := qt.New(t)
	ptahRevision := projectConfigRevisionMetadata{
		Version:                   "1",
		Description:               "case",
		Applied:                   1,
		Total:                     2,
		PartialHashesIsNull:       false,
		PartialHashes:             "null",
		PartialHashesStorageClass: "blob",
	}
	pair := fileTxModePair{
		Ptah: fileTxModeObservation{Revisions: []projectConfigRevisionMetadata{ptahRevision}},
	}
	observations := make(map[string]fileTxModePair, 7)
	for _, name := range fileTxModeBookkeepingCaseNames() {
		observations[name] = pair
	}

	result := compareFileTxModeBookkeeping(observations)

	c.Assert(result.Outcome, qt.Equals, Gap)
	c.Assert(result.Issue, qt.Equals, fileTxModeBookkeepingIssue)
	c.Assert(result.Detail, qt.Contains, "differs in 7/7 body-execution cells")
	c.Assert(result.Detail, qt.Contains, `partial_hashes="null"`)
}

func TestCompareFileTxModeMatrixCase_RecordsIntentionalDivergence(t *testing.T) {
	c := qt.New(t)
	atlasExpected := fileTxModeExpectedState{
		Tables:    []string{"atlas_schema_revisions"},
		Revisions: []fileTxModeRevisionFact{},
	}
	ptahRevision := fileTxModeRevisionFact{
		Version:         "1",
		Description:     "case",
		Type:            2,
		Applied:         1,
		Total:           2,
		ErrorStatement:  "INSERT INTO txmode_missing (id) VALUES (1);",
		OperatorVersion: "Ptah",
	}
	testCase := fileTxModeMatrixCase{
		GlobalMode:    "file",
		AtlasExpected: atlasExpected,
		PtahExpected: fileTxModeExpectedState{
			BodyTable: true,
			Tables:    []string{"atlas_schema_revisions", fileTxModeBodyTable},
			Revisions: []fileTxModeRevisionFact{ptahRevision},
		},
		IntentionalDivergence: "Ptah preserves the explicit directive",
		Issue:                 fileTxModeWhitespaceIssue,
	}
	pair := fileTxModePair{
		Atlas: fileTxModeObservation{
			Process: integrityProcessResult{exitCode: 1, stderr: fileTxModeMissingTable},
			Tables:  []string{"atlas_schema_revisions"},
		},
		Ptah: fileTxModeObservation{
			Process: integrityProcessResult{exitCode: 1, stderr: fileTxModeMissingTable},
			Tables:  []string{"atlas_schema_revisions", fileTxModeBodyTable},
			Revisions: []projectConfigRevisionMetadata{
				{
					Version:         "1",
					Description:     "case",
					Type:            2,
					Applied:         1,
					Total:           2,
					ErrorStatement:  "INSERT INTO txmode_missing (id) VALUES (1);",
					OperatorVersion: "Ptah",
				},
			},
		},
	}

	result := compareFileTxModeMatrixCase("fixture", testCase, pair)

	c.Assert(result, qt.DeepEquals, Result{
		Probe:   migrateRuntimeProbeName,
		Fixture: "fixture",
		Stage:   fileTxModePtahBetterStage,
		Outcome: OK,
		Detail:  "Ptah preserves the explicit directive; measured state: Atlas body=false/revisions=0, Ptah body=true/revisions=1",
		Issue:   fileTxModeWhitespaceIssue,
	})
}

func TestCompareFileTxModeMatrixCase_RejectsWrongRevisionState(t *testing.T) {
	c := qt.New(t)
	testCase := fileTxModeMatrixCase{
		GlobalMode: "file",
		AtlasExpected: fileTxModeExpectedState{
			Tables:    []string{"atlas_schema_revisions"},
			Revisions: []fileTxModeRevisionFact{},
		},
		PtahExpected: fileTxModeExpectedState{
			BodyTable: true,
			Tables:    []string{"atlas_schema_revisions", fileTxModeBodyTable},
			Revisions: []fileTxModeRevisionFact{
				{
					Version:         "1",
					Description:     "case",
					Type:            2,
					Applied:         1,
					Total:           2,
					ErrorStatement:  "INSERT INTO txmode_missing (id) VALUES (1);",
					OperatorVersion: "Ptah",
				},
			},
		},
		IntentionalDivergence: "Ptah preserves the explicit directive",
		Issue:                 fileTxModeWhitespaceIssue,
	}
	pair := fileTxModePair{
		Atlas: fileTxModeObservation{
			Process: integrityProcessResult{exitCode: 1, stderr: fileTxModeMissingTable},
			Tables:  []string{"atlas_schema_revisions"},
		},
		Ptah: fileTxModeObservation{
			Process:   integrityProcessResult{exitCode: 1, stderr: fileTxModeMissingTable},
			Tables:    []string{"atlas_schema_revisions", fileTxModeBodyTable},
			Revisions: []projectConfigRevisionMetadata{{Version: "1"}},
		},
	}

	result := compareFileTxModeMatrixCase("fixture", testCase, pair)

	c.Assert(result.Outcome, qt.Equals, Gap)
	c.Assert(result.Stage, qt.Equals, "ptah")
	c.Assert(result.Detail, qt.Contains, "revision facts")
}
