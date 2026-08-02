package probe

// White-box testing required: these tests verify the semantic diagnostic
// compiler used only by the internal txtar runner and not exposed as public API.

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp"
)

const migrateLintDS102Text = "  -- analyzing version 3\n" +
	"    -- destructive changes detected:\n" +
	"      -- L1 [DS102]: Ptah-owned non-empty diagnostic\n"

func TestParseMigrateLintDiagnostics_HappyPath(t *testing.T) {
	c := qt.New(t)
	stdout := "  -- analyzing version 2\n" +
		"    -- data dependent changes detected:\n" +
		"      -- L4 [MF103]: Ptah-owned non-empty diagnostic\n" +
		"         Ptah-owned continuation\n"

	got, err := parseMigrateLintDiagnostics(stdout)

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.CmpEquals(cmp.AllowUnexported(migrateLintDiagnostic{})), []migrateLintDiagnostic{
		{version: "2", group: "data dependent", line: 4, code: "MF103"},
	})
}

func TestParseMigrateLintDiagnostics_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{name: "missing context", stdout: "      -- L1 [DS102]: diagnostic\n", want: "has no version/group context"},
		{name: "legacy URL", stdout: "  -- analyzing version 2\n    -- destructive changes detected:\n      -- L1: text https://example.test/lint/analyzers#DS102\n", want: "contains legacy diagnostic prose"},
		{name: "legacy suggested fix", stdout: "  -- analyzing version 2\n    -- destructive changes detected:\n    -- suggested fix:\n", want: "contains legacy diagnostic prose"},
		{name: "malformed diagnostic", stdout: "  -- analyzing version 2\n    -- destructive changes detected:\n      -- L1: diagnostic without a code\n", want: "contains malformed diagnostic"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got, err := parseMigrateLintDiagnostics(test.stdout)
			c.Assert(err, qt.ErrorMatches, ".*"+test.want+".*")
			c.Assert(got, qt.IsNil)
		})
	}
}

func TestParseMigrateLintSemanticDiagnostics_HappyPath(t *testing.T) {
	c := qt.New(t)
	stdout := `[{"Name":"2.sql","Findings":[{"rule":"MF103","line":4,"message":"risk; remediate it","context":{"subjects":[{"kind":"column","name":"c2","parent":"users","data_type":"int"}]}}]}]`

	got, err := parseMigrateLintSemanticDiagnostics(stdout)

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.CmpEquals(cmp.AllowUnexported(
		migrateLintSemanticDiagnostic{}, migrateLintSubject{},
	)), []migrateLintSemanticDiagnostic{
		{
			version:     "2",
			line:        4,
			code:        "MF103",
			subjects:    []migrateLintSubject{{kind: "column", name: "c2", parent: "users", dataType: "int"}},
			remediation: true,
		},
	})
}

func TestParseMigrateLintSemanticDiagnostics_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{name: "malformed JSON", stdout: `{`, want: "parse migrate lint structured output"},
		{name: "non-versioned file", stdout: `[{"Name":"repeatable.sql"}]`, want: "parse migrate lint version"},
		{name: "missing context", stdout: `[{"Name":"2.sql","Findings":[{"rule":"MF103","line":1}]}]`, want: "has no context"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got, err := parseMigrateLintSemanticDiagnostics(test.stdout)
			c.Assert(err, qt.ErrorMatches, ".*"+test.want+".*")
			c.Assert(got, qt.IsNil)
		})
	}
}

func TestMigrateLintExpectedDiagnostics_ObserveHappyPath(t *testing.T) {
	c := qt.New(t)
	expected := migrateLintExpectedDiagnostics{}

	handled, err := expected.observe("stdout '  -- analyzing version 2'")
	c.Assert(err, qt.IsNil)
	c.Assert(handled, qt.IsFalse)
	handled, err = expected.observe("stdout '    -- destructive changes detected:'")
	c.Assert(err, qt.IsNil)
	c.Assert(handled, qt.IsFalse)
	handled, err = expected.observe(`stdout '      -- L1: Dropping table "users"'`)
	c.Assert(err, qt.IsNil)
	c.Assert(handled, qt.IsTrue)
	handled, err = expected.observe("stdout '         https://atlasgo.io/lint/analyzers#DS102'")
	c.Assert(err, qt.IsNil)
	c.Assert(handled, qt.IsTrue)
	handled, err = expected.observe("stdout '    -- suggested fix:'")
	c.Assert(err, qt.IsNil)
	c.Assert(handled, qt.IsTrue)
	handled, err = expected.observe(`stdout '      -> Verify table "users" before removal'`)
	c.Assert(err, qt.IsNil)
	c.Assert(handled, qt.IsTrue)
	c.Assert(expected.diagnostics, qt.CmpEquals(cmp.AllowUnexported(migrateLintDiagnostic{})), []migrateLintDiagnostic{
		{version: "2", group: "destructive", line: 1, code: "DS102"},
	})
	c.Assert(expected.semantic, qt.CmpEquals(cmp.AllowUnexported(
		migrateLintSemanticDiagnostic{}, migrateLintSubject{},
	)), []migrateLintSemanticDiagnostic{
		{
			version:     "2",
			line:        1,
			code:        "DS102",
			subjects:    []migrateLintSubject{{kind: "table", name: "users"}},
			remediation: true,
		},
	})
}

func TestMigrateLintExpectedDiagnostics_ObserveFailurePath(t *testing.T) {
	c := qt.New(t)

	c.Run("pending line before group", func(c *qt.C) {
		expected := migrateLintExpectedDiagnostics{version: "2", group: "destructive", pendingLine: 1}
		handled, err := expected.observe("stdout '    -- data dependent changes detected:'")
		c.Assert(err, qt.ErrorMatches, ".*omitted the diagnostic code for line 1.*")
		c.Assert(handled, qt.IsFalse)
	})

	c.Run("missing quoted diagnostic subject", func(c *qt.C) {
		expected := migrateLintExpectedDiagnostics{version: "2", group: "destructive"}
		handled, err := expected.observe("stdout '      -- L1: prose https://atlasgo.io/lint/analyzers#DS102'")
		c.Assert(err, qt.ErrorMatches, ".*quoted subjects, want 1.*")
		c.Assert(handled, qt.IsTrue)
	})

	c.Run("fix heading without diagnostic", func(c *qt.C) {
		expected := migrateLintExpectedDiagnostics{version: "2", group: "destructive"}
		handled, err := expected.observe("stdout '    -- suggested fix:'")
		c.Assert(err, qt.ErrorMatches, ".*has no preceding diagnostic.*")
		c.Assert(handled, qt.IsTrue)
	})

	c.Run("fix body without heading", func(c *qt.C) {
		expected := migrateLintExpectedDiagnostics{diagnosticSeen: true}
		handled, err := expected.observe(`stdout '      -> Verify table "users"'`)
		c.Assert(err, qt.ErrorMatches, ".*has no fix heading.*")
		c.Assert(handled, qt.IsTrue)
	})

	c.Run("fix subject differs", func(c *qt.C) {
		expected := migrateLintExpectedDiagnostics{
			diagnosticSeen:   true,
			suggestedFixSeen: true,
			semantic: []migrateLintSemanticDiagnostic{
				{subjects: []migrateLintSubject{{kind: "table", name: "users"}}},
			},
		}
		handled, err := expected.observe(`stdout '      -> Verify table "pets"'`)
		c.Assert(err, qt.ErrorMatches, ".*subject does not match.*")
		c.Assert(handled, qt.IsTrue)
	})
}

func TestMigrateLintExpectedDiagnostics_CompareHappyPath(t *testing.T) {
	c := qt.New(t)
	expected := migrateLintExpectedDiagnostics{
		diagnostics: []migrateLintDiagnostic{{version: "3", group: "destructive", line: 1, code: "DS102"}},
		semantic: []migrateLintSemanticDiagnostic{{
			version: "3", line: 1, code: "DS102",
			subjects: []migrateLintSubject{{kind: "table", name: "users"}}, remediation: true,
		}},
	}
	semantic := `[{"Name":"3.sql","Findings":[{"rule":"DS102","line":1,"message":"risk; Ptah remediation","context":{"subjects":[{"kind":"table","name":"users"}]}}]}]`

	err := expected.compare(txtarMigrateLintRun{
		stdout: migrateLintDS102Text, semanticStdout: semantic, failed: true, semanticFailed: true,
	})

	c.Assert(err, qt.IsNil)
}

func TestMigrateLintExpectedDiagnostics_CompareFailurePath(t *testing.T) {
	c := qt.New(t)
	expected := migrateLintExpectedDiagnostics{
		diagnostics: []migrateLintDiagnostic{{version: "3", group: "destructive", line: 1, code: "DS102"}},
		semantic: []migrateLintSemanticDiagnostic{{
			version: "3", line: 1, code: "DS102",
			subjects: []migrateLintSubject{{kind: "table", name: "users"}}, remediation: true,
		}},
	}
	tests := []struct {
		name           string
		stdout         string
		stderr         string
		semanticStdout string
		semanticStderr string
		failed         bool
		semanticFailed bool
		want           string
	}{
		{name: "wrong text code", stdout: "  -- analyzing version 3\n    -- destructive changes detected:\n      -- L1 [MF103]: diagnostic\n", semanticStdout: `[]`, want: "diagnostics differ"},
		{name: "wrong table", stdout: migrateLintDS102Text, semanticStdout: `[{"Name":"3.sql","Findings":[{"rule":"DS102","line":1,"message":"risk; fix","context":{"subjects":[{"kind":"table","name":"pets"}]}}]}]`, want: "semantic diagnostics differ"},
		{name: "missing remediation", stdout: migrateLintDS102Text, semanticStdout: `[{"Name":"3.sql","Findings":[{"rule":"DS102","line":1,"message":"risk only","context":{"subjects":[{"kind":"table","name":"users"}]}}]}]`, want: "semantic diagnostics differ"},
		{name: "unexpected default stderr", stderr: "warning\n", semanticStdout: `[]`, want: "wrote unexpected stderr"},
		{name: "unexpected structured stderr", stdout: migrateLintDS102Text, semanticStdout: `[]`, semanticStderr: "warning\n", want: "structured run wrote unexpected stderr"},
		{name: "failure state mismatch", stdout: migrateLintDS102Text, semanticStdout: `[]`, failed: true, want: "failure state differs"},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			err := expected.compare(txtarMigrateLintRun{
				stdout:         test.stdout,
				stderr:         test.stderr,
				semanticStdout: test.semanticStdout,
				semanticStderr: test.semanticStderr,
				failed:         test.failed,
				semanticFailed: test.semanticFailed,
			})
			c.Assert(err, qt.ErrorMatches, ".*"+test.want+".*")
		})
	}
}
