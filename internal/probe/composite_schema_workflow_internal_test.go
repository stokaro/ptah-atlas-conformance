package probe

// White-box testing required: these tests inject schema-comparison output to
// verify the exact clean sentinel and structured drift-output boundary.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestValidateCleanSchemaComparison_HappyPath(t *testing.T) {
	c := qt.New(t)
	result := compositeCommandResult{command: ptahCommandResult{
		stdout:   "Comparing schema\n=== SCHEMA COMPARISON ===\n\nNo schema differences detected.\n",
		exitCode: 0,
	}}

	c.Assert(validateCleanSchemaComparison(result), qt.IsNil)
}

func TestValidateCleanSchemaComparison_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name   string
		stdout string
	}{
		{name: "legacy empty array", stdout: "=== SCHEMA COMPARISON ===\n[]\n"},
		{name: "extra output", stdout: "=== SCHEMA COMPARISON ===\nNo schema differences detected.\nextra\n"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			result := compositeCommandResult{command: ptahCommandResult{stdout: test.stdout}}
			c.Assert(validateCleanSchemaComparison(result), qt.ErrorMatches, `expected exact clean sentinel .*`)
		})
	}
}

func TestValidateDriftSchemaComparison_HappyPath(t *testing.T) {
	c := qt.New(t)
	result := compositeCommandResult{command: ptahCommandResult{
		stdout: "=== SCHEMA COMPARISON ===\n\n" +
			"Differences detected (1 category):\n" +
			"  columns_added (1): users.status\n\n" +
			"Reconciling SQL:\nALTER TABLE users ADD COLUMN status TEXT;\n",
		exitCode: 1,
	}}

	c.Assert(validateDriftSchemaComparison(result), qt.IsNil)
}

func TestValidateDriftSchemaComparison_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name string
		diff string
	}{
		{name: "legacy empty array", diff: "[]"},
		{name: "uncategorized SQL", diff: "ALTER TABLE users ADD COLUMN status TEXT;"},
		{name: "header without category row", diff: "Differences detected (1 category):\n\nReconciling SQL: none."},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			result := compositeCommandResult{command: ptahCommandResult{
				stdout:   "=== SCHEMA COMPARISON ===\n" + test.diff,
				exitCode: 1,
			}}
			c.Assert(validateDriftSchemaComparison(result), qt.ErrorMatches, `expected category-bearing difference output.*`)
		})
	}
}
