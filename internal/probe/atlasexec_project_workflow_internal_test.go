package probe

// White-box testing required: report-stream and live-state validator failures
// are internal conformance-harness invariants that are not observable through
// the exported Probe API without coupling tests to scratch runtime failures.

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestDecodeAtlasExecJSONStream_HappyPath(t *testing.T) {
	c := qt.New(t)
	output := `{"URL":{"Host":"bar.db"},"Applied":[{"Version":"20240112070806"}]}
{"URL":{"Host":"foo.db"},"Applied":[{"Version":"20240112070806"}]}`

	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](output)

	c.Assert(err, qt.IsNil)
	c.Assert(reports, qt.HasLen, 2)
	c.Check(reports[0].URL.Host, qt.Equals, "bar.db")
	c.Check(reports[1].URL.Host, qt.Equals, "foo.db")
	c.Check(validateAtlasExecTenantReports(reports, []atlasExecTenantReportExpectation{
		{host: "bar.db", appliedVersions: []string{atlasExecFirstVersion}},
		{host: "foo.db", appliedVersions: []string{atlasExecFirstVersion}},
	}), qt.IsNil)
}

func TestDecodeAtlasExecJSONStream_FailurePath(t *testing.T) {
	c := qt.New(t)

	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](`{"URL":`)

	c.Check(reports, qt.IsNil)
	c.Check(err, qt.ErrorMatches, `decode report 1:.*`)
}

func TestValidateAtlasExecStreams_HappyPath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name        string
		result      ptahCommandResult
		reportCount int
	}{
		{
			name:        "one report has no newline",
			result:      ptahCommandResult{stdout: `{"Applied":[]}`},
			reportCount: 1,
		},
		{
			name:        "two reports have one newline",
			result:      ptahCommandResult{stdout: "{\"Applied\":[]}\n{\"Applied\":[]}"},
			reportCount: 2,
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Check(validateAtlasExecStreams(test.result, test.reportCount), qt.IsNil)
		})
	}
}

func TestValidateAtlasExecStreams_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name        string
		result      ptahCommandResult
		reportCount int
		wantErr     string
	}{
		{
			name:        "stderr is not empty",
			result:      ptahCommandResult{stdout: `{"Applied":[]}`, stderr: "unexpected diagnostic\n"},
			reportCount: 1,
			wantErr:     `stderr is not empty: unexpected diagnostic`,
		},
		{
			name:        "separator is missing",
			result:      ptahCommandResult{stdout: `{"Applied":[]}{"Applied":[]}`},
			reportCount: 2,
			wantErr:     `stdout newline count = 0, want 1 between 2 JSON reports`,
		},
		{
			name:        "separator has an extra newline",
			result:      ptahCommandResult{stdout: "{\"Applied\":[]}\n\n{\"Applied\":[]}"},
			reportCount: 2,
			wantErr:     `stdout newline count = 2, want 1 between 2 JSON reports`,
		},
		{
			name:        "report has trailing whitespace",
			result:      ptahCommandResult{stdout: `{"Applied":[]} `},
			reportCount: 1,
			wantErr:     `stdout has leading or trailing whitespace`,
		},
		{
			name:        "separator has adjacent whitespace",
			result:      ptahCommandResult{stdout: "{\"Applied\":[]} \n{\"Applied\":[]}"},
			reportCount: 2,
			wantErr:     `stdout JSON report 1 has separator-adjacent whitespace`,
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Check(validateAtlasExecStreams(test.result, test.reportCount), qt.ErrorMatches, test.wantErr)
		})
	}
}

func TestValidateAtlasExecTenantReports_HappyPath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name    string
		reports []atlasExecApplyReport
		want    []atlasExecTenantReportExpectation
	}{
		{
			name: "one completion and one failure",
			reports: []atlasExecApplyReport{
				{URL: atlasExecURL{Host: "bar.db"}, Applied: []atlasExecAppliedFile{{Version: atlasExecSecondVersion}}},
				{URL: atlasExecURL{Host: "foo.db"}, Applied: []atlasExecAppliedFile{{Version: atlasExecSecondVersion}}, Error: "UNIQUE constraint failed: t1.c1"},
			},
			want: []atlasExecTenantReportExpectation{
				{host: "bar.db", appliedVersions: []string{atlasExecSecondVersion}},
				{host: "foo.db", appliedVersions: []string{atlasExecSecondVersion}, errorFragment: "UNIQUE constraint failed"},
			},
		},
		{
			name: "retry leaves successful tenant no-op",
			reports: []atlasExecApplyReport{
				{URL: atlasExecURL{Host: "bar.db"}},
				{URL: atlasExecURL{Host: "foo.db"}, Applied: []atlasExecAppliedFile{{Version: atlasExecSecondVersion}}, Error: "UNIQUE constraint failed: t1.c1"},
			},
			want: []atlasExecTenantReportExpectation{
				{host: "bar.db"},
				{host: "foo.db", appliedVersions: []string{atlasExecSecondVersion}, errorFragment: "UNIQUE constraint failed"},
			},
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			c.Check(validateAtlasExecTenantReports(test.reports, test.want), qt.IsNil)
		})
	}
}

func TestValidateAtlasExecTenantReports_FailurePath(t *testing.T) {
	c := qt.New(t)
	reports := []atlasExecApplyReport{{URL: atlasExecURL{Host: "bar.db"}}}
	want := []atlasExecTenantReportExpectation{{host: "bar.db"}, {host: "foo.db"}}

	err := validateAtlasExecTenantReports(reports, want)

	c.Check(err, qt.ErrorMatches, `tenant report count = 1, want 2`)
}

func TestValidateAtlasExecTenantState_HappyPath(t *testing.T) {
	c := qt.New(t)
	root := t.TempDir()
	barPath := filepath.Join(root, "bar.db")
	fooPath := filepath.Join(root, "foo.db")
	createAtlasExecStateDatabase(c, barPath, []string{
		`CREATE TABLE atlas_schema_revisions(version TEXT, description TEXT, applied INTEGER, total INTEGER, operator_version TEXT)`,
		`INSERT INTO atlas_schema_revisions VALUES ('20240112070806', '', 1, 1, ''), ('20240116003831', '', 1, 1, '')`,
		`CREATE TABLE t1(c1 int)`,
		`CREATE UNIQUE INDEX c1_unique ON t1(c1)`,
	})
	createAtlasExecStateDatabase(c, fooPath, []string{
		`CREATE TABLE atlas_schema_revisions(version TEXT, description TEXT, applied INTEGER, total INTEGER, operator_version TEXT)`,
		`INSERT INTO atlas_schema_revisions VALUES ('20240112070806', '', 1, 1, '')`,
		`CREATE TABLE t1(c1 int)`,
		`INSERT INTO t1(c1) VALUES (1), (1), (1)`,
	})
	want := atlasExecFinalTenantStates()

	c.Check(validateAtlasExecTenantState(barPath, want["bar.db"]), qt.IsNil)
	c.Check(validateAtlasExecTenantState(fooPath, want["foo.db"]), qt.IsNil)
}

func TestValidateAtlasExecTenantState_FailurePath(t *testing.T) {
	c := qt.New(t)
	path := filepath.Join(t.TempDir(), "foo.db")
	createAtlasExecStateDatabase(c, path, []string{
		`CREATE TABLE atlas_schema_revisions(version TEXT, description TEXT, applied INTEGER, total INTEGER, operator_version TEXT)`,
		`INSERT INTO atlas_schema_revisions VALUES ('20240112070806', '', 1, 1, '')`,
		`CREATE TABLE t1(c1 int)`,
	})

	err := validateAtlasExecTenantState(path, atlasExecTenantState{
		tables:      []string{"atlas_schema_revisions", "t1"},
		rows:        3,
		uniqueIndex: false,
		revisions: []atlasExecRevisionProgress{
			{version: atlasExecFirstVersion, applied: 1, total: 1},
		},
	})

	c.Check(err, qt.ErrorMatches, `t1 row count = 0, want 3`)
}

func TestValidateAtlasExecTenantState_RejectsUnexpectedDirtyRevision(t *testing.T) {
	c := qt.New(t)
	path := filepath.Join(t.TempDir(), "foo.db")
	createAtlasExecStateDatabase(c, path, []string{
		`CREATE TABLE atlas_schema_revisions(version TEXT, description TEXT, applied INTEGER, total INTEGER, operator_version TEXT)`,
		`INSERT INTO atlas_schema_revisions VALUES ('20240112070806', '', 1, 1, ''), ('20240116003831', '', 0, 1, '')`,
		`CREATE TABLE t1(c1 int)`,
		`INSERT INTO t1(c1) VALUES (1), (1), (1)`,
	})
	want := atlasExecFinalTenantStates()["foo.db"]

	err := validateAtlasExecTenantState(path, want)

	c.Check(err, qt.ErrorMatches, `revision progress = .*20240116003831 0 1.*, want .*20240112070806 1 1.*`)
}

func createAtlasExecStateDatabase(c *qt.C, path string, statements []string) {
	db, err := openSQLiteRuntimeDB(path)
	c.Assert(err, qt.IsNil)
	for _, statement := range statements {
		_, err := db.Exec(statement)
		c.Assert(err, qt.IsNil, qt.Commentf("%s", statement))
	}
	c.Assert(db.Close(), qt.IsNil)
}
