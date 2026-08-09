package probe

// White-box testing required: these tests pin the internal live-harness
// validator for Ptah's intentional failed-down safety divergence.

import (
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
)

func TestValidateFailedDownRevisionTransition_HappyPath(t *testing.T) {
	c := qt.New(t)
	before, after, want := failedDownRevisionTestFixture()

	c.Check(validateFailedDownRevisionTransition(before, after, want), qt.IsNil)
}

func TestValidateFailedDownRevisionTransition_FailurePath(t *testing.T) {
	c := qt.New(t)
	before, after, want := failedDownRevisionTestFixture()
	after[0].Description = "changed"

	c.Check(
		validateFailedDownRevisionTransition(before, after, want),
		qt.ErrorMatches,
		`unrelated revision 1 changed during failed down`,
	)
}

func TestValidateFailedDownRevisionTransition_RejectsWrongDirection(t *testing.T) {
	c := qt.New(t)
	before, after, want := failedDownRevisionTestFixture()
	after[1].OperatorVersion = "Ptah"

	c.Check(
		validateFailedDownRevisionTransition(before, after, want),
		qt.ErrorMatches,
		`revision 2 operator = "Ptah", want Ptah/down`,
	)
}

func TestValidateFailedDownRevisionTransition_RejectsMalformedPartialHash(t *testing.T) {
	c := qt.New(t)
	before, after, want := failedDownRevisionTestFixture()
	after[1].PartialHashes = `["h1:test"]`

	c.Check(
		validateFailedDownRevisionTransition(before, after, want),
		qt.ErrorMatches,
		`revision 2 partial hashes = \[h1:test\], want \[h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\]`,
	)
}

func TestValidateFailedDownRevisionTransition_RejectsMalformedTimestamp(t *testing.T) {
	c := qt.New(t)
	before, after, want := failedDownRevisionTestFixture()
	after[1].ExecutedAt = "not-a-timestamp"

	c.Check(
		validateFailedDownRevisionTransition(before, after, want),
		qt.ErrorMatches,
		`revision 2 failed-down timestamp: not an Atlas-readable timestamp`,
	)
}

func TestValidateFailedDownRevisionTransition_RejectsWrongBaselineHash(t *testing.T) {
	c := qt.New(t)
	before, after, want := failedDownRevisionTestFixture()
	before[1].Hash = "wrong"

	c.Check(
		validateFailedDownRevisionTransition(before, after, want),
		qt.ErrorMatches,
		`revision 2 baseline hash = "wrong", want "hash"`,
	)
}

func failedDownRevisionTestFixture() (
	[]projectConfigRevisionMetadata,
	[]projectConfigRevisionMetadata,
	failedDownRevisionExpectation,
) {
	first := projectConfigRevisionMetadata{Version: "1", Description: "first", Applied: 1, Total: 1}
	startedAt := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	second := projectConfigRevisionMetadata{
		Version:                    "2",
		Description:                "second",
		Type:                       2,
		Applied:                    2,
		Total:                      2,
		ExecutedAt:                 startedAt.Add(-time.Minute).Format(time.RFC3339Nano),
		ExecutedAtStorageClass:     "text",
		ErrorStorageClass:          "text",
		ErrorStatementStorageClass: "text",
		Hash:                       "hash",
		PartialHashes:              "null",
		PartialHashesStorageClass:  "blob",
		OperatorVersion:            "Ptah",
	}
	failed := second
	failed.Applied = 1
	failed.ExecutedAt = startedAt.Add(time.Second).Format(time.RFC3339Nano)
	failed.ExecutionTime = 1
	failed.Error = "syntax error"
	failed.ErrorStatement = "BROKEN;"
	failed.PartialHashes = `["h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="]`
	failed.OperatorVersion = "Ptah/down"
	return []projectConfigRevisionMetadata{first, second},
		[]projectConfigRevisionMetadata{first, failed},
		failedDownRevisionExpectation{
			version:             "2",
			baselineDescription: "second",
			baselineTotal:       2,
			baselineHash:        "hash",
			applied:             1,
			total:               2,
			errorFragment:       "syntax error",
			errorStatement:      "BROKEN;",
			partialHashes:       []string{"h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
			window: projectConfigApplyWindow{
				startedAt:  startedAt,
				finishedAt: startedAt.Add(2 * time.Second),
			},
		}
}
