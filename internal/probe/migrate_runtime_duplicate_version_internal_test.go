package probe

// White-box testing required: these tests exercise the duplicate-version
// comparator, which is private to the migrate-runtime probe, over observations
// no real binary can be made to produce on demand.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func measuredDuplicateVersionAtlas() duplicateVersionObservation {
	return duplicateVersionObservation{
		first: integrityProcessResult{
			stderr:   duplicateVersionAtlasPanic + "\ngoroutine 1 [running]:\n",
			exitCode: 2,
		},
		second:    integrityProcessResult{stdout: duplicateVersionAtlasRerun},
		tables:    []string{"atlas_schema_revisions", duplicateVersionTableA},
		revisions: []string{duplicateVersion},
	}
}

func measuredDuplicateVersionPtah() duplicateVersionObservation {
	refusal := integrityProcessResult{
		stderr:   duplicateVersionPtahRefusal + ": " + duplicateVersionFirst + " and " + duplicateVersionSecond + "\n",
		exitCode: 1,
	}
	return duplicateVersionObservation{first: refusal, second: refusal}
}

// keepDuplicateVersionObservation leaves a binary's measured observation as
// it is, for the rows that change only the other binary's.
func keepDuplicateVersionObservation(*duplicateVersionObservation) {}

func TestCompareDuplicateVersionApply_HappyPath(t *testing.T) {
	c := qt.New(t)

	got := compareDuplicateVersionApply(measuredDuplicateVersionAtlas(), measuredDuplicateVersionPtah())

	c.Assert(got.Outcome, qt.Equals, OK)
	c.Assert(got.Stage, qt.Equals, fileTxModePtahBetterStage)
	c.Assert(got.Issue, qt.Equals, duplicateVersionIssue)
}

// TestCompareDuplicateVersionApply_RefusalWithoutFileNames keeps the row
// green on a ptah-compat whose refusal names the version alone.
func TestCompareDuplicateVersionApply_RefusalWithoutFileNames(t *testing.T) {
	c := qt.New(t)
	ptah := measuredDuplicateVersionPtah()
	ptah.first.stderr = duplicateVersionPtahRefusal + "\n"
	ptah.second.stderr = duplicateVersionPtahRefusal + "\n"

	got := compareDuplicateVersionApply(measuredDuplicateVersionAtlas(), ptah)

	c.Assert(got.Outcome, qt.Equals, OK)
}

func TestCompareDuplicateVersionApply_FailurePath(t *testing.T) {
	tests := []struct {
		name       string
		atlas      func(*duplicateVersionObservation)
		ptah       func(*duplicateVersionObservation)
		wantStage  string
		wantDetail string
	}{
		{
			name:       "Atlas CE applies both files",
			atlas:      func(o *duplicateVersionObservation) { o.first = integrityProcessResult{stdout: "Migrating\n"} },
			ptah:       keepDuplicateVersionObservation,
			wantStage:  "atlas-oracle",
			wantDetail: `Atlas CE no longer matches the measured duplicate-version crash: first apply \{exit=0 .*`,
		},
		{
			name:       "Atlas CE runs the second file on a rerun",
			atlas:      func(o *duplicateVersionObservation) { o.second = integrityProcessResult{stdout: "Migrating\n"} },
			ptah:       keepDuplicateVersionObservation,
			wantStage:  "atlas-oracle",
			wantDetail: `Atlas CE no longer matches the measured duplicate-version crash: second apply .*`,
		},
		{
			name: "Atlas CE leaves the second table",
			atlas: func(o *duplicateVersionObservation) {
				o.tables = []string{"atlas_schema_revisions", duplicateVersionTableA, duplicateVersionTableB}
			},
			ptah:       keepDuplicateVersionObservation,
			wantStage:  "atlas-oracle",
			wantDetail: `Atlas CE no longer matches the measured duplicate-version crash: tables .*duplicate_b.*`,
		},
		{
			name:       "Atlas CE records no revision",
			atlas:      func(o *duplicateVersionObservation) { o.revisions = nil },
			ptah:       keepDuplicateVersionObservation,
			wantStage:  "atlas-oracle",
			wantDetail: `Atlas CE no longer matches the measured duplicate-version crash: revisions \[\]`,
		},
		{
			name:       "ptah-compat applies the first file",
			ptah:       func(o *duplicateVersionObservation) { o.first = integrityProcessResult{stdout: "Migrating\n"} },
			atlas:      keepDuplicateVersionObservation,
			wantStage:  "ptah",
			wantDetail: `ptah-compat did not refuse the duplicate version before running a migration: apply 1 \{exit=0 .*`,
		},
		{
			name:       "ptah-compat accepts the rerun",
			ptah:       func(o *duplicateVersionObservation) { o.second = integrityProcessResult{} },
			atlas:      keepDuplicateVersionObservation,
			wantStage:  "ptah",
			wantDetail: `ptah-compat did not refuse the duplicate version before running a migration: apply 2 .*`,
		},
		{
			name:       "ptah-compat refuses after creating a table",
			ptah:       func(o *duplicateVersionObservation) { o.tables = []string{duplicateVersionTableA} },
			atlas:      keepDuplicateVersionObservation,
			wantStage:  "ptah",
			wantDetail: `ptah-compat did not refuse the duplicate version before running a migration: tables \["duplicate_a"\]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			atlas := measuredDuplicateVersionAtlas()
			ptah := measuredDuplicateVersionPtah()
			test.atlas(&atlas)
			test.ptah(&ptah)

			got := compareDuplicateVersionApply(atlas, ptah)

			c.Assert(got.Outcome, qt.Equals, Gap)
			c.Assert(got.Stage, qt.Equals, test.wantStage)
			c.Assert(got.Detail, qt.Matches, test.wantDetail)
			c.Assert(got.Issue, qt.Equals, duplicateVersionIssue)
		})
	}
}
