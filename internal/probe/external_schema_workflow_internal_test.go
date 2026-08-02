package probe

// White-box testing required: these tests inject command output directly into
// the internal external-schema workflow to verify its exact stdout boundary.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestExternalSchemaWorkflowRenderResult_ContaminatedStdoutIsGap(t *testing.T) {
	c := qt.New(t)
	const expected = "CREATE TABLE users (id INTEGER PRIMARY KEY);"
	workflow := externalSchemaWorkflow{}

	tests := []struct {
		name   string
		stdout string
	}{
		{
			name:   "legacy display header",
			stdout: "=== SQLITE SCHEMA ===\n\n" + expected,
		},
		{
			name:   "progress prefix",
			stdout: "Found 1 table\n" + expected,
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			result := workflow.renderResult(
				compositeCommandResult{command: ptahCommandResult{stdout: test.stdout}},
				"external hcl schema",
				"explicit command render",
				expected,
				"",
			)
			c.Assert(result.Outcome, qt.Equals, Gap)
			c.Assert(result.Detail, qt.Equals, "rendered SQL differs from the expected SQLite snapshot")
		})
	}
}
