package probe_test

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestLexSplitParityProbe_UsesFixtureDialect(t *testing.T) {
	c := qt.New(t)
	dir := t.TempDir()
	sqlPath := filepath.Join(dir, "1.my.sql")
	c.Assert(os.WriteFile(sqlPath, []byte(`create table t (c text default '\\');
create table t (c text default "\\");
create table t (c text default "\"");
create table t (c text default "\"" + '\'');`), 0o600), qt.IsNil)
	c.Assert(os.WriteFile(sqlPath+".golden", []byte(`create table t (c text default '\\');
-- end --
create table t (c text default "\\");
-- end --
create table t (c text default "\"");
-- end --
create table t (c text default "\"" + '\'');`), 0o600), qt.IsNil)

	results := probe.LexSplitParityProbe{}.Run(probe.Fixture{
		Name:     "sql/migrate/testdata/lexescaped",
		Kind:     probe.FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{sqlPath, sqlPath + ".golden"},
		SQLFiles: []string{sqlPath},
	})

	c.Assert(results, qt.DeepEquals, []probe.Result{{
		Probe:   "lex-split-parity",
		Fixture: "sql/migrate/testdata/lexescaped/1.my.sql",
		Stage:   "split",
		Outcome: probe.OK,
		Detail:  "Ptah splits into the same 4 statement(s) as Atlas",
	}})
}
