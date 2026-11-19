package probe

// White-box testing required: these tests exercise the modified-CHECK
// comparator and plan splitter, which are private to the migrate-runtime
// probe, over plans no pinned binary can be made to print on demand.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

const (
	measuredAtlasMariaDBCheckPlan = "-- Modify \"t\" table\n" +
		"ALTER TABLE `t` DROP CONSTRAINT `qty_ck`, ADD CONSTRAINT `qty_ck` CHECK (`qty` > 0);\n"
	measuredPtahMariaDBCheckPlan = "-- Modify \"t\" table\n" +
		"ALTER TABLE `t` DROP CONSTRAINT `qty_ck`, ADD CONSTRAINT `qty_ck` CHECK (qty > 0);\n"
)

func measuredMariaDBCheck(plan string) mariadbModifiedCheckObservation {
	return mariadbModifiedCheckObservation{
		plan:        integrityProcessResult{stdout: plan},
		checkClause: mariadbModifiedCheckClause,
	}
}

func keepMariaDBCheckObservation(*mariadbModifiedCheckObservation) {}

func TestCompareMariaDBModifiedCheck_HappyPath(t *testing.T) {
	c := qt.New(t)
	atlas := measuredMariaDBCheck(measuredAtlasMariaDBCheckPlan)
	atlas.plan.stderr = "Notice: This Atlas edition lacks support for features such as checkpoints.\n"

	got := compareMariaDBModifiedCheck(atlas, measuredMariaDBCheck(measuredPtahMariaDBCheckPlan))

	c.Assert(got.Outcome, qt.Equals, OK)
	c.Assert(got.Stage, qt.Equals, "plan")
}

func TestCompareMariaDBModifiedCheck_FailurePath(t *testing.T) {
	tests := []struct {
		name       string
		atlas      func(*mariadbModifiedCheckObservation)
		ptah       func(*mariadbModifiedCheckObservation)
		wantStage  string
		wantDetail string
		wantIssue  string
	}{
		{
			name: "Atlas CE drops the constraint with DROP CHECK",
			atlas: func(o *mariadbModifiedCheckObservation) {
				o.plan.stdout = "ALTER TABLE `t` DROP CHECK `qty_ck`, ADD CONSTRAINT `qty_ck` CHECK (`qty` > 0);\n"
			},
			ptah:       keepMariaDBCheckObservation,
			wantStage:  "atlas-oracle",
			wantDetail: `Atlas CE no longer plans the modified CHECK as measured: plan drops the constraint with DROP CHECK, which MariaDB refuses: .*`,
		},
		{
			name: "Atlas CE fails to plan",
			atlas: func(o *mariadbModifiedCheckObservation) {
				o.plan = integrityProcessResult{stderr: "Error: x\n", exitCode: 1}
			},
			ptah:       keepMariaDBCheckObservation,
			wantStage:  "atlas-oracle",
			wantDetail: `Atlas CE no longer plans the modified CHECK as measured: schema diff \{exit=1 .*`,
		},
		{
			name:       "ptah-compat plans nothing",
			atlas:      keepMariaDBCheckObservation,
			ptah:       func(o *mariadbModifiedCheckObservation) { o.plan.stdout = "" },
			wantStage:  "ptah",
			wantDetail: `ptah-compat modified CHECK plan: plan does not drop and add the constraint: `,
			wantIssue:  "stokaro/ptah#648",
		},
		{
			name:       "MariaDB refuses the ptah-compat plan",
			atlas:      keepMariaDBCheckObservation,
			ptah:       func(o *mariadbModifiedCheckObservation) { o.applyError = "Error 1064 (42000)" },
			wantStage:  "ptah",
			wantDetail: `ptah-compat modified CHECK plan: MariaDB refused the plan: Error 1064 \(42000\)`,
			wantIssue:  "stokaro/ptah#648",
		},
		{
			name:       "the applied clause differs",
			atlas:      keepMariaDBCheckObservation,
			ptah:       func(o *mariadbModifiedCheckObservation) { o.checkClause = "`qty` >= 0" },
			wantStage:  "ptah",
			wantDetail: "ptah-compat modified CHECK plan: applied CHECK clause \"`qty` >= 0\", want \"`qty` > 0\"",
			wantIssue:  "stokaro/ptah#648",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			atlas := measuredMariaDBCheck(measuredAtlasMariaDBCheckPlan)
			ptah := measuredMariaDBCheck(measuredPtahMariaDBCheckPlan)
			test.atlas(&atlas)
			test.ptah(&ptah)

			got := compareMariaDBModifiedCheck(atlas, ptah)

			c.Assert(got.Outcome, qt.Equals, Gap)
			c.Assert(got.Stage, qt.Equals, test.wantStage)
			c.Assert(got.Detail, qt.Matches, test.wantDetail)
			c.Assert(got.Issue, qt.Equals, test.wantIssue)
		})
	}
}

func TestPlanStatements_HappyPath(t *testing.T) {
	c := qt.New(t)

	got := planStatements("-- Modify \"t\" table\nALTER TABLE `t` DROP CONSTRAINT `a`;\n" +
		"-- Add \"u\" table\nCREATE TABLE `u` (\n  `id` int\n);\n")

	c.Assert(got, qt.DeepEquals, []string{
		"ALTER TABLE `t` DROP CONSTRAINT `a`",
		"CREATE TABLE `u` (\n  `id` int\n)",
	})
}

func TestAtlasMariaDBURL_HappyPath(t *testing.T) {
	c := qt.New(t)

	c.Assert(atlasMariaDBURL("mariadb://root:pw@tcp(localhost:3307)/conf_1"), qt.Equals, "mariadb://root:pw@localhost:3307/conf_1")
	c.Assert(atlasMariaDBURL("mariadb://root:pw@localhost:3307/conf_1"), qt.Equals, "mariadb://root:pw@localhost:3307/conf_1")
}
