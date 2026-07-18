package probe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stokaro/ptah/core/ast"
	"github.com/stokaro/ptah/core/atlashcl"
	"github.com/stokaro/ptah/migration/migratesum"
	"github.com/stokaro/ptah/migration/migrator"
)

func TestLoadCorpusIncludesAllAtlasTestArtifactKinds(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqlcase", "1.sql"), "CREATE TABLE users (id int);\n")
	writeTestFile(t, filepath.Join(root, "sqlcase", "1.sql.golden"), "-- expected\n")
	writeTestFile(t, filepath.Join(root, "sqlcase", "atlas.sum"), "h1:fake\n")
	writeTestFile(t, filepath.Join(root, "integration", "case.txtar"), "-- input.sql --\nSELECT 1;\n")
	writeTestFile(t, filepath.Join(root, "schema", "desired.hcl"), "schema \"public\" {}\n")
	writeTestFile(t, filepath.Join(root, "templates", "app.tmpl"), "{{ .Name }}\n")

	fixtures, err := LoadCorpus(root)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Fixture{}
	for _, fx := range fixtures {
		byName[fx.Name] = fx
	}
	if len(byName) != 4 {
		t.Fatalf("expected 4 fixtures, got %d: %#v", len(byName), fixtures)
	}
	if got := byName["sqlcase"].Kind; got != FixtureKindSQLDir {
		t.Fatalf("sqlcase kind = %q", got)
	}
	if got := len(byName["sqlcase"].Files); got != 3 {
		t.Fatalf("sqlcase files = %d", got)
	}
	if got := len(byName["sqlcase"].SQLFiles); got != 1 {
		t.Fatalf("sqlcase sql files = %d", got)
	}
	if byName["sqlcase"].SumFile == "" {
		t.Fatal("sqlcase sum file was not captured")
	}
	if got := byName["integration/case.txtar"].Kind; got != FixtureKindTxtar {
		t.Fatalf("txtar kind = %q", got)
	}
	if got := byName["schema/desired.hcl"].Kind; got != FixtureKindHCL {
		t.Fatalf("hcl kind = %q", got)
	}
	if got := byName["templates/app.tmpl"].Kind; got != FixtureKindOther {
		t.Fatalf("other kind = %q", got)
	}
}

func TestCorpusProbeMarksImportedButUnmeasuredArtifacts(t *testing.T) {
	result := CorpusProbe{}.Run(Fixture{Name: "case.txtar", Kind: FixtureKindTxtar})
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(result), result)
	}
	if result[0].Outcome != OK {
		t.Fatalf("expected imported txtar OK, got %#v", result[0])
	}
	if result[0].Stage != "import" {
		t.Fatalf("expected import stage, got %#v", result[0])
	}

	result = CorpusProbe{}.Run(Fixture{Name: "schema.hcl", Kind: FixtureKindHCL})
	if len(result) != 1 {
		t.Fatalf("expected 1 HCL result, got %d: %#v", len(result), result)
	}
	if result[0].Outcome != OK {
		t.Fatalf("expected imported HCL OK, got %#v", result[0])
	}
	if result[0].Stage != "import" {
		t.Fatalf("expected HCL import stage, got %#v", result[0])
	}

	result = CorpusProbe{}.Run(Fixture{Name: "templates/app.tmpl", Kind: FixtureKindOther})
	if len(result) != 1 {
		t.Fatalf("expected 1 other result, got %d: %#v", len(result), result)
	}
	if result[0].Outcome != Gap {
		t.Fatalf("expected unknown artifact Gap, got %#v", result[0])
	}
	if result[0].Stage != "unmeasured" {
		t.Fatalf("expected unmeasured stage, got %#v", result[0])
	}
}

func TestCorpusProbeClassifiesAtlasSDKTemplateRunnerFixturesAsOutOfScope(t *testing.T) {
	for _, name := range []string{
		"sdk/tmplrun/testdata/app.tmpl",
		"sdk/tmplrun/testdata/foo.go",
	} {
		result := CorpusProbe{}.Run(Fixture{Name: name, Kind: FixtureKindOther})
		if len(result) != 1 {
			t.Fatalf("expected 1 result for %s, got %d: %#v", name, len(result), result)
		}
		if result[0].Outcome != OK {
			t.Fatalf("expected SDK template-runner fixture OK, got %#v", result[0])
		}
		if result[0].Stage != "out-of-scope" {
			t.Fatalf("expected out-of-scope stage, got %#v", result[0])
		}
		assertResultDetailContains(t, result, "no database schema or migration surface")
	}
}

func TestAtlasHCLProbeReportsSchemaParseSupport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.hcl")
	writeTestFile(t, path, `
schema "main" {}

table "users" {
  schema = schema.main
  column "id" {
    type = int
  }
  primary_key {
    columns = [column.id]
  }
}
`)

	results := AtlasHCLProbe{}.Run(Fixture{
		Name:  "schema.hcl",
		Kind:  FixtureKindHCL,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected HCL parse OK, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "parsed Atlas HCL schema file: 1 table(s), 1 field(s)")
}

func TestAtlasHCLProbeReportsUnsupportedSchemaGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "person.hcl")
	writeTestFile(t, path, `
person "rotemtam" {
  hobby = "ice-cream"
}
`)

	results := AtlasHCLProbe{}.Run(Fixture{
		Name:  "person.hcl",
		Kind:  FixtureKindHCL,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected HCL parse gap, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "Ptah cannot model this Atlas HCL schema file")
}

func TestAtlasHCLProbeClassifiesSchemaHCLGenericFixturesAsOutOfScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "person.hcl")
	writeTestFile(t, path, `
person "rotemtam" {
  hobby = "ice-cream"
}
`)

	results := AtlasHCLProbe{}.Run(Fixture{
		Name:  "schemahcl/testdata/person.hcl",
		Kind:  FixtureKindHCL,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected non-schema schemahcl fixture OK, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "only non-schema top-level blocks: person")
}

func TestAtlasHCLProbeDoesNotClassifySchemaHCLSchemaFixturesAsOutOfScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.hcl")
	writeTestFile(t, path, `
schema "main" {}

table "users" {
  schema = schema.main
  unsupported "thing" {}
}
`)

	results := AtlasHCLProbe{}.Run(Fixture{
		Name:  "schemahcl/testdata/schema.hcl",
		Kind:  FixtureKindHCL,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected schema-shaped HCL gap, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "Ptah cannot model this Atlas HCL schema file")
}

func TestParseProbeRendersAtlasSQLTemplates(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "1.sql")
	second := filepath.Join(dir, "2.sql")
	shared := filepath.Join(dir, "shared", "users.sql")
	writeTestFile(t, first, `{{- if eq .Env "dev" }}
CREATE TABLE dev1 (id INT);
{{- else }}
CREATE TABLE prod1 (id INT);
{{- end }}
`)
	writeTestFile(t, second, `{{ template "shared/users" "prod2" }}`)
	writeTestFile(t, shared, `{{- define "shared/users" }}
CREATE TABLE users_{{ $ }} (id INT);
{{- end }}
`)

	results := ParseProbe{}.Run(Fixture{
		Name:     "templatedir",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		SQLFiles: []string{first, second, shared},
	})

	if len(results) != 3 {
		t.Fatalf("expected 3 parse results, got %d: %#v", len(results), results)
	}
	for _, result := range results {
		if result.Outcome != OK {
			t.Fatalf("expected template SQL parse OK, got %#v", result)
		}
	}
	assertResultDetailContains(t, results, "rendered Atlas SQL template and parsed 1 statement(s)")
	assertResultDetailContains(t, results, "Atlas SQL template support file rendered no standalone statements")
}

func TestParseProbeClassifiesAtlasNegativeSQLFixtures(t *testing.T) {
	dir := t.TempDir()
	brokenKeyword := filepath.Join(dir, "20231029112426.sql")
	willFailComment := filepath.Join(dir, "20220318104615_second.sql")
	syntaxError := filepath.Join(dir, "3.sql")
	writeTestFile(t, brokenKeyword, `broken;`)
	writeTestFile(t, willFailComment, `ALTER TABLE tbl ADD col_2 bigint;
asdasd ALTER TABLE tbl ADD col_3 bigint; -- will fail
`)
	writeTestFile(t, syntaxError, `CREATE TABLE pets (id INT);
THIS LINE ADDS A SYNTAX ERROR;
`)

	results := ParseProbe{}.Run(Fixture{
		Name:     "cmd/atlas/internal/migrate/testdata/broken",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{brokenKeyword, willFailComment, syntaxError},
		SQLFiles: []string{brokenKeyword, willFailComment, syntaxError},
	})

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d: %#v", len(results), results)
	}
	for _, result := range results {
		if result.Outcome != OK {
			t.Fatalf("expected OK result for rejected negative fixture, got %#v", result)
		}
		if result.Stage != "expected-invalid" {
			t.Fatalf("expected expected-invalid stage, got %#v", result)
		}
	}
}

func TestParseProbeDoesNotClassifyAllBrokenDirFixturesAsNegative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1.sql")
	writeTestFile(t, path, `CREATE TABLE users (id INT);`)

	results := ParseProbe{}.Run(Fixture{
		Name:     "cmd/atlas/internal/migrate/testdata/broken",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{path},
		SQLFiles: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result for valid fixture in broken dir, got %#v", results[0])
	}
	if results[0].Stage != "round-trip" {
		t.Fatalf("expected round-trip stage, got %#v", results[0])
	}
}

func TestParseProbeClassifiesAtlasLexerFixtures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "19_ms_gocmd.sql")
	writeTestFile(t, path, `go
SELECT 1
GO
`)

	results := ParseProbe{}.Run(Fixture{
		Name:     "sql/migrate/testdata/lex",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{path},
		SQLFiles: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result for lexer fixture, got %#v", results[0])
	}
	if results[0].Stage != "lexer-only" {
		t.Fatalf("expected lexer-only stage, got %#v", results[0])
	}
}

func TestTxtarScriptProbeReportsCommandSurface(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `# comment
atlas migrate diff --to file://schema.hcl
stdout 'planned'
atlas migrate apply --url URL
cmpshow users expected.sql
only postgres

-- schema.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 unsupported command result, got %d: %#v", len(results), results)
	}
	for _, result := range results {
		if result.Outcome != Gap {
			t.Fatalf("expected measured txtar gap, got %#v", result)
		}
		if result.Stage != "script-runtime" {
			t.Fatalf("expected script-runtime stage, got %#v", result)
		}
	}
	assertResultDetailContains(t, results, "unsupported: atlas migrate diff")
	for _, result := range results {
		if strings.Contains(result.Detail, "stdout") ||
			strings.Contains(result.Detail, "only") ||
			strings.Contains(result.Detail, "atlas migrate apply") ||
			strings.Contains(result.Detail, "cmpshow") {
			t.Fatalf("detail should not count assertions/matching directives as commands: %s", result.Detail)
		}
	}
}

func TestTxtarScriptCommandsStopAtFirstFileMarker(t *testing.T) {
	commands := txtarScriptCommands(`atlas migrate hash
-- migrations/1.sql --
atlas migrate apply --url URL
`)

	if len(commands) != 1 || commands[0] != "atlas migrate hash" {
		t.Fatalf("commands = %#v, want only atlas migrate hash", commands)
	}
}

func TestTxtarScriptProbeExecutesSchemaInspectSQLAndCmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE users (
  id INT NOT NULL,
  PRIMARY KEY (id)
);

-- expected.sql --
-- Create "users" table
CREATE TABLE "users" ("id" integer NOT NULL, PRIMARY KEY ("id"));
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if results[0].Stage != "script-runtime" {
		t.Fatalf("expected script-runtime stage, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "executed 1 supported command") {
		t.Fatalf("detail missing supported command count: %s", results[0].Detail)
	}
	if !strings.Contains(results[0].Detail, "checked 1 assertion") {
		t.Fatalf("detail missing assertion count: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeExecutesPostgresSchemaInspectSQLWithCheckConstraints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . "  " }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE t1 (
  a int CONSTRAINT c1 CHECK (a > 0),
  b int CONSTRAINT c2 CHECK (b > 0),
  CONSTRAINT c3 CHECK (a < b)
);

-- expected.sql --
-- Create "t1" table
CREATE TABLE "t1" (
  "a" integer NULL,
  "b" integer NULL,
  CONSTRAINT "c1" CHECK (a > 0),
  CONSTRAINT "c2" CHECK (b > 0),
  CONSTRAINT "c3" CHECK (a < b)
);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaInspectSQLAndCmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE users (
  id INT NOT NULL,
  PRIMARY KEY (id)
);
CREATE SPATIAL INDEX idx_geom ON users (id);

-- expected.sql --
-- Create "users" table
CREATE TABLE `+"`users`"+` (`+"`id`"+` int NOT NULL, PRIMARY KEY (`+"`id`"+`), SPATIAL INDEX `+"`idx_geom`"+` (`+"`id`"+`)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaInspectSQLWithPrimaryKeyParts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE `+"`t1`"+` (`+"`id`"+` tinytext NOT NULL, PRIMARY KEY (`+"`id`"+` (7) DESC));

-- expected.sql --
-- Create "t1" table
CREATE TABLE `+"`t1`"+` (`+"`id`"+` tinytext NOT NULL, PRIMARY KEY (`+"`id`"+` (7) DESC)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaInspectChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . "  " }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE t1(
  name varchar(20) CHECK(name in ('a', 'b', 'c')),
  age int CHECK(age > 0),
  CONSTRAINT `+"`t1_check`"+` CHECK (name <> 'a' or age > 10)
);

-- expected.hcl --
table "t1" {
  schema = schema.script_check
  column "name" {
    null = true
    type = varchar(20)
  }
  column "age" {
    null = true
    type = int
  }
  check "t1_check" {
    expr = "((`+"`name`"+` <> _utf8mb4'a') or (`+"`age`"+` > 10))"
  }
  check "t1_chk_1" {
    expr = "(`+"`name`"+` in (_utf8mb4'a',_utf8mb4'b',_utf8mb4'c'))"
  }
  check "t1_chk_2" {
    expr = "(`+"`age`"+` > 0)"
  }
}
schema "script_check" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
-- expected.sql --
-- Create "t1" table
CREATE TABLE `+"`t1`"+` (
  `+"`name`"+` varchar(20) NULL,
  `+"`age`"+` int NULL,
  CONSTRAINT `+"`t1_check`"+` CHECK ((`+"`name`"+` <> _utf8mb4'a') or (`+"`age`"+` > 10)),
  CONSTRAINT `+"`t1_chk_1`"+` CHECK (`+"`name`"+` in (_utf8mb4'a',_utf8mb4'b',_utf8mb4'c')),
  CONSTRAINT `+"`t1_chk_2`"+` CHECK (`+"`age`"+` > 0)
) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/check.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLMigrateDiffChecks(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/check.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "check.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestAtlasNormalizeMySQLCheckExprIsIdempotent(t *testing.T) {
	cases := map[string]string{
		"`name` in (_utf8mb4'a',_utf8mb4'b')":   "`name` in (_utf8mb4'a',_utf8mb4'b')",
		"``name`` in (_utf8mb4'a',_utf8mb4'b')": "`name` in (_utf8mb4'a',_utf8mb4'b')",
		"`a``b` = _utf8mb4'x'":                  "`a``b` = _utf8mb4'x'",
	}
	for input, want := range cases {
		if got := atlasNormalizeCheckExpr("mysql", input); got != want {
			t.Fatalf("atlasNormalizeCheckExpr(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTxtarParseGeneratedCheckAlterStatement(t *testing.T) {
	node, ok := txtarParseGeneratedCheckAlterStatement(
		"ALTER TABLE `t1` DROP CHECK `t1_check`, ADD CONSTRAINT `t1_check` CHECK ((`name` <> _utf8mb4'b') or (`age` > 10))",
	)
	if !ok {
		t.Fatal("expected generated check ALTER statement to parse")
	}
	alter, ok := node.(*ast.AlterTableNode)
	if !ok {
		t.Fatalf("expected AlterTableNode, got %T", node)
	}
	if alter.Name != "t1" || len(alter.Operations) != 2 {
		t.Fatalf("unexpected alter node: %#v", alter)
	}
	drop, ok := alter.Operations[0].(*ast.DropConstraintOperation)
	if !ok || !drop.Check || drop.ConstraintName != "t1_check" {
		t.Fatalf("unexpected drop operation: %#v", alter.Operations[0])
	}
	add, ok := alter.Operations[1].(*ast.AddConstraintOperation)
	if !ok || add.Constraint == nil || add.Constraint.Type != ast.CheckConstraint ||
		add.Constraint.Name != "t1_check" ||
		add.Constraint.Expression != "(`name` <> _utf8mb4'b') or (`age` > 10)" {
		t.Fatalf("unexpected add operation: %#v", alter.Operations[1])
	}
}

func TestTxtarScriptProbeExecutesMariaDBSchemaInspectChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only maria107

atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . "  " }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE t1(
  buf json,
  name varchar(20) CHECK(name in ('a', 'b', 'c')),
  age int CHECK(age > 0),
  CONSTRAINT `+"`check1`"+` CHECK (name <> 'a' or age > 10)
);

-- expected.hcl --
table "t1" {
  schema = schema.script_check_maria
  column "buf" {
    null = true
    type = json
  }
  column "name" {
    null = true
    type = varchar(20)
  }
  column "age" {
    null = true
    type = int
  }
  check "age" {
    expr = "`+"`age`"+` > 0"
  }
  check "check1" {
    expr = "`+"`name`"+` <> 'a' or `+"`age`"+` > 10"
  }
  check "name" {
    expr = "`+"`name`"+` in ('a','b','c')"
  }
}
schema "script_check_maria" {
  charset = "utf8mb4"
  collate = "utf8mb4_general_ci"
}
-- expected.sql --
-- Create "t1" table
CREATE TABLE `+"`t1`"+` (
  `+"`buf`"+` json NULL,
  `+"`name`"+` varchar(20) NULL,
  `+"`age`"+` int NULL,
  CONSTRAINT `+"`age`"+` CHECK (`+"`age`"+` > 0),
  CONSTRAINT `+"`check1`"+` CHECK (`+"`name`"+` <> 'a' or `+"`age`"+` > 10),
  CONSTRAINT `+"`name`"+` CHECK (`+"`name`"+` in ('a','b','c'))
) CHARSET utf8mb4 COLLATE utf8mb4_general_ci;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/check-maria.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMariaDBColumnJSONFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "column-json.txtar")
	writeTestFile(t, path, `only maria*

apply 1.hcl
cmpshow users 1.sql

# The CHECK "json_valid(`+"`name`"+`)" should not be present in the HCL
# description because the "longtext" is converted to "json" type.
cmphcl 1.inspect.hcl

-- 1.hcl --
schema "script_column_json" {}

table "users" {
  schema = schema.script_column_json
  column "id" {
    null = false
    type = int
  }
  column "name" {
    null = false
    type = json
  }
  primary_key {
    columns = [column.id]
  }
}

-- 1.sql --
CREATE TABLE `+"`users`"+` (
  `+"`id`"+` int NOT NULL,
  `+"`name`"+` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL CHECK (json_valid(`+"`name`"+`)),
  PRIMARY KEY (`+"`id`"+`)
)

-- 1.inspect.hcl --
table "users" {
  schema = schema.script_column_json
  column "id" {
    null = false
    type = int
  }
  column "name" {
    null = false
    type = json
  }
  primary_key {
    columns = [column.id]
  }
}
schema "script_column_json" {
  charset = "utf8mb4"
  collate = "utf8mb4_general_ci"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-json.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMariaDBColumnTimePrecisionFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "column-time-precision-maria.txtar")
	writeTestFile(t, path, `only maria*

apply 1.hcl
cmpshow foo 1.sql
cmphcl 1.inspect.hcl

-- 1.hcl --
schema "script_column_time_precision_maria" {}

table "foo" {
  schema = schema.script_column_time_precision_maria
  column "id" {
    null = false
    type = char(36)
  }
  column "precision_default" {
    null = false
    type = timestamp
    default = sql("CURRENT_TIMESTAMP")
    on_update = sql("CURRENT_TIMESTAMP")
  }
  column "create_time" {
    null = false
    type = timestamp(6)
    default = sql("CURRENT_TIMESTAMP(6)")
  }
  column "update_time" {
    null = false
    type = datetime(6)
    default = sql("CURRENT_TIMESTAMP(6)")
    on_update = sql("CURRENT_TIMESTAMP(6)")
  }
  primary_key {
    columns = [column.id]
  }
}

-- 1.sql --
CREATE TABLE `+"`foo`"+` (
  `+"`id`"+` char(36) NOT NULL,
  `+"`precision_default`"+` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  `+"`create_time`"+` timestamp(6) NOT NULL DEFAULT current_timestamp(6),
  `+"`update_time`"+` datetime(6) NOT NULL DEFAULT current_timestamp(6) ON UPDATE current_timestamp(6),
  PRIMARY KEY (`+"`id`"+`)
)

-- 1.inspect.hcl --
table "foo" {
  schema = schema.script_column_time_precision_maria
  column "id" {
    null = false
    type = char(36)
  }
  column "precision_default" {
    null    = false
    type    = timestamp
    default = sql("CURRENT_TIMESTAMP")
    on_update = sql("CURRENT_TIMESTAMP")
  }
  column "create_time" {
    null    = false
    type    = timestamp(6)
    default = sql("CURRENT_TIMESTAMP(6)")
  }
  column "update_time" {
    null    = false
    type    = datetime(6)
    default = sql("CURRENT_TIMESTAMP(6)")
    on_update = sql("CURRENT_TIMESTAMP(6)")
  }
  primary_key {
    columns = [column.id]
  }
}
schema "script_column_time_precision_maria" {
  charset = "utf8mb4"
  collate = "utf8mb4_general_ci"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-time-precision-maria.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLColumnTimePrecisionFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "column-time-precision-mysql.txtar")
	writeTestFile(t, path, `only mysql56 mysql57 mysql8

apply 1.hcl
cmpshow foo 1.sql

-- 1.hcl --
schema "script_column_time_precision_mysql" {}

table "foo" {
  schema = schema.script_column_time_precision_mysql
  column "id" {
    null = false
    type = char(36)
  }
  column "precision_default" {
    null = false
    type = timestamp
    default = sql("CURRENT_TIMESTAMP")
    on_update = sql("CURRENT_TIMESTAMP")
  }
  column "create_time" {
    null = false
    type = timestamp(6)
    default = sql("CURRENT_TIMESTAMP(6)")
  }
  column "update_time" {
    null = false
    type = datetime(6)
    default = sql("CURRENT_TIMESTAMP(6)")
    on_update = sql("CURRENT_TIMESTAMP(6)")
  }
  primary_key {
    columns = [column.id]
  }
}

-- 1.sql --
CREATE TABLE `+"`foo`"+` (
  `+"`id`"+` char(36) NOT NULL,
  `+"`precision_default`"+` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `+"`create_time`"+` timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `+"`update_time`"+` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`+"`id`"+`)
)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-time-precision-mysql.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSchemaInspectHCLAndCmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE users (
  id INT NOT NULL,
  PRIMARY KEY (id)
);

-- expected.hcl --
table "users" {
  schema = schema.script_case
  column "id" {
    null = false
    type = integer
  }
  primary_key {
    columns = [column.id]
  }
}
schema "script_case" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresSchemaInspectHCLWithCheckConstraints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE t1 (
  a int CONSTRAINT c1 CHECK (a > 0),
  b int CONSTRAINT c2 CHECK (b > 0),
  CONSTRAINT c3 CHECK (a < b)
);

-- expected.hcl --
table "t1" {
  schema = schema.script_case
  column "a" {
    null = true
    type = integer
  }
  column "b" {
    null = true
    type = integer
  }
  check "c1" {
    expr = "(a > 0)"
  }
  check "c2" {
    expr = "(b > 0)"
  }
  check "c3" {
    expr = "(a < b)"
  }
}
schema "script_case" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSchemaInspectHCLWithInlinePrimaryKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE users (id INT PRIMARY KEY);

-- expected.hcl --
table "users" {
  schema = schema.script_case
  column "id" {
    null = false
    type = integer
  }
  primary_key {
    columns = [column.id]
  }
}
schema "script_case" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaInspectHCLAndCmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE users (
  id INT NOT NULL,
  PRIMARY KEY (id)
);

-- expected.hcl --
table "users" {
  schema = schema.script_case
  column "id" {
    null = false
    type = int
  }
  primary_key {
    columns = [column.id]
  }
}
schema "script_case" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaInspectHCLWithPrimaryKeyParts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE `+"`t1`"+` (`+"`id`"+` tinytext NOT NULL, PRIMARY KEY (`+"`id`"+` (7) DESC));

-- expected.hcl --
table "t1" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      desc   = true
      column = column.id
      prefix = 7
    }
  }
}
schema "script_primary_key_parts" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/primary-key-parts.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaInspectHCLSourceWithPrimaryKeyParts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://schema.hcl --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- schema.hcl --
table "t1" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      column = column.id
      prefix = 7
    }
  }
}
table "t2" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      desc   = true
      column = column.id
      prefix = 7
    }
  }
}
schema "script_primary_key_parts" {
  charset = "utf8mb4"
  collate = "utf8mb4_bin"
}

-- expected.hcl --
table "t1" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      column = column.id
      prefix = 7
    }
  }
}
table "t2" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      desc   = true
      column = column.id
      prefix = 7
    }
  }
}
schema "script_primary_key_parts" {
  charset = "utf8mb4"
  collate = "utf8mb4_bin"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/primary-key-parts.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresSchemaInspectHCLWithUniqueAndForeignKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://schema.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- schema.sql --
create table t1(a int primary key, b int unique);
create table t0(b int primary key references t1(b));

-- expected.hcl --
table "t0" {
  schema = schema.script_index_unique_constraint
  column "b" {
    null = false
    type = integer
  }
  primary_key {
    columns = [column.b]
  }
  foreign_key "t0_b_fkey" {
    columns     = [column.b]
    ref_columns = [table.t1.column.b]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}
table "t1" {
  schema = schema.script_index_unique_constraint
  column "a" {
    null = false
    type = integer
  }
  column "b" {
    null = true
    type = integer
  }
  primary_key {
    columns = [column.a]
  }
  unique "t1_b_key" {
    columns = [column.b]
  }
}
schema "script_index_unique_constraint" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/index-unique-constraint.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeCmpToleratesOnlyFinalNewline(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		equal bool
	}{
		{name: "exact", left: "same", right: "same", equal: true},
		{name: "final newline", left: "same\n", right: "same", equal: true},
		{name: "content mismatch", left: "same\n", right: "other", equal: false},
		{name: "internal newline mismatch", left: "a\nb\n", right: "ab", equal: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := txtarFilesEqual(tt.left, tt.right); got != tt.equal {
				t.Fatalf("txtarFilesEqual() = %t, want %t", got, tt.equal)
			}
		})
	}
}

func TestTxtarScriptProbeKeepsUnsupportedSchemaInspectHCLAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE users (
  id INT CHECK (id > 0)
);

-- expected.hcl --
ignored
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas schema inspect hcl") {
		t.Fatalf("detail missing unsupported HCL marker: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeReportsUnattachedMySQLInspectIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}'

-- a.sql --
CREATE SPATIAL INDEX idx_geom ON missing_table (id);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "without matching table") {
		t.Fatalf("detail missing unattached index error: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeExecutesVirtualFileCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `mkdir generated
cp source.sql generated/copied.sql
cmp generated/copied.sql expected.sql
mv generated/copied.sql generated/moved.sql
cmp generated/moved.sql expected.sql
rm generated/moved.sql
cp source.sql generated/moved.sql
exec rm -rf generated
mkdir generated
cp source.sql generated/after-rm.sql
cmp generated/after-rm.sql expected.sql
exec cat generated/after-rm.sql
stdout 'CREATE TABLE users'
exec touch generated/empty.sql
cmp generated/empty.sql empty.sql

-- source.sql --
CREATE TABLE users (id INT);
-- expected.sql --
CREATE TABLE users (id INT);
-- empty.sql --
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "executed 10 supported command") {
		t.Fatalf("detail missing virtual command count: %s", results[0].Detail)
	}
	if !strings.Contains(results[0].Detail, "checked 5 assertion") {
		t.Fatalf("detail missing cmp count: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeChecksCmpmig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `cmpmig 0 expected.sql
cmpmig 1 second.sql
cmpmig 2 up.sql
cmpmig 3 down.sql

-- migrations/20240101010101_first.sql --
CREATE TABLE users (id INT);
-- expected.sql --
CREATE TABLE users (id INT);
-- migrations/20240101010102_second.sql --
ALTER TABLE users ADD COLUMN email TEXT;
-- second.sql --
ALTER TABLE users ADD COLUMN email TEXT;
-- migrations/20240101010103_third.up.sql --
CREATE TABLE pets (id INT);
-- up.sql --
CREATE TABLE pets (id INT);
-- migrations/20240101010104_fourth.down.sql --
DROP TABLE pets;
-- down.sql --
DROP TABLE pets;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "checked 4 assertion") {
		t.Fatalf("detail missing cmpmig assertion count: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeSkipsCmpmigAfterUnsupportedMigrationProducer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff --dev-url URL --to file://schema.hcl first
cmpmig 0 expected.sql

-- schema.hcl --
schema "main" {}
-- expected.sql --
CREATE TABLE users (id INT);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: atlas migrate diff")
	if strings.Contains(results[0].Detail, "cmpmig") {
		t.Fatalf("dependent cmpmig should not be reported after unsupported migration producer: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeChecksExpectedMigrateDiffValidationFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `exec mkdir migrations

! atlas migrate diff --to file://1.hcl --dir file://migrations
stderr '"dev-url" not set'

! atlas migrate diff --dev-url URL --dir file://migrations
stderr '"to" not set'

-- 1.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteInitialMigrateDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `exec mkdir migrations
atlas migrate diff --dev-url sqlite://devdb --to file://1.hcl --dir file://migrations
cmpmig 0 diff.sql

-- 1.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
}
schema "main" {
}
-- diff.sql --
-- Create "users" table
CREATE TABLE `+"`users`"+` (`+"`id`"+` int NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-diff.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeReportsSyncedSQLiteInitialMigrateDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff --dev-url sqlite://devdb --to file://1.hcl --dir file://migrations
cmpmig 0 diff.sql
atlas migrate diff --dev-url sqlite://devdb --to file://1.hcl --dir file://migrations
stdout 'The migration directory is synced with the desired state, no changes to be made'

-- 1.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
}
schema "main" {
}
-- diff.sql --
-- Create "users" table
CREATE TABLE `+"`users`"+` (`+"`id`"+` int NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-diff.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLNamedInitialMigrateDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas migrate diff v1 --to file://schema.hcl --dev-url URL
cmpmig 0 migration.sql
atlas migrate diff v1-check --to file://schema.hcl --dev-url URL
stdout 'The migration directory is synced with the desired state, no changes to be made'

-- schema.hcl --
table "t1" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      desc   = true
      column = column.id
      prefix = 7
    }
  }
}
schema "script_primary_key_parts" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
-- migration.sql --
-- Create "t1" table
CREATE TABLE `+"`t1`"+` (`+"`id`"+` tinytext NOT NULL, PRIMARY KEY (`+"`id`"+` (7) DESC)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/primary-key-parts.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLMigrateDiffAddColumnAndQualifier(t *testing.T) {
	path := "../../third_party/atlas/upstream/internal/integration/testdata/mysql/cli-migrate-diff.txtar"

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/cli-migrate-diff.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   filepath.Dir(path),
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLMigrateDiffFormats(t *testing.T) {
	path := "../../third_party/atlas/upstream/internal/integration/testdata/mysql/cli-migrate-diff-format.txtar"

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/cli-migrate-diff-format.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   filepath.Dir(path),
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLFormattedInitialMigrateDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas migrate diff v1 --to file://schema.sql --dev-url URL --format '{{ sql . "  " }}'
cmpmig 0 migration.sql

-- schema.sql --
CREATE TABLE t1(
  name varchar(20),
  age int
);
-- migration.sql --
-- Create "t1" table
CREATE TABLE `+"`t1`"+` (
  `+"`name`"+` varchar(20) NULL,
  `+"`age`"+` int NULL
) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/check.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeRejectsUnsupportedMigrateDiffFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas migrate diff v1 --to file://schema.sql --dev-url URL --format '{{ json . }}'
cmpmig 0 migration.sql

-- schema.sql --
CREATE TABLE t1(id int);
-- migration.sql --
{}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/check.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: atlas migrate diff")
	if strings.Contains(results[0].Detail, "cmpmig") {
		t.Fatalf("dependent cmpmig should not be reported after unsupported format: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeExecutesMySQLIncrementalMigrateDiffPrimaryKeyParts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

atlas migrate diff v1 --to file://schema.v1.hcl --dev-url URL
cmpmig 0 migration.v1.sql
atlas migrate diff v2 --to file://schema.v2.hcl --dev-url URL
cmpmig 1 migration.v2.sql

-- schema.v1.hcl --
table "t1" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      column = column.id
      prefix = 7
    }
  }
}
schema "script_primary_key_parts" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
-- migration.v1.sql --
-- Create "t1" table
CREATE TABLE `+"`t1`"+` (`+"`id`"+` tinytext NOT NULL, PRIMARY KEY (`+"`id`"+` (7))) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
-- schema.v2.hcl --
table "t1" {
  schema = schema.script_primary_key_parts
  column "id" {
    null = false
    type = tinytext
  }
  column "id2" {
    null = false
    type = tinytext
  }
  primary_key {
    on {
      column = column.id
      prefix = 7
    }
    on {
      column = column.id2
      prefix = 1
    }
  }
}
schema "script_primary_key_parts" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
-- migration.v2.sql --
-- Modify "t1" table
ALTER TABLE `+"`t1`"+` ADD COLUMN `+"`id2`"+` tinytext NOT NULL, DROP PRIMARY KEY, ADD PRIMARY KEY (`+"`id`"+` (7), `+"`id2`"+` (1));
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/primary-key-parts.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLPrimaryKeyPartsFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/primary-key-parts.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "primary-key-parts.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLMigrateDiffModeNormalizedFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/cli-migrate-diff-mode-normalized.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "cli-migrate-diff-mode-normalized.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLIndexUniqueFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/index-unique.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "index-unique.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLProjectSchemasFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/cli-project-schemas.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "cli-project-schemas.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLMigrateApplyDatasourceFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/cli-migrate-apply-datasrc.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "cli-migrate-apply-datasrc.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLSchemaApplyDatasourceFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/cli-schema-apply-datasrc.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "cli-schema-apply-datasrc.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLProjectURLEscapeFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "mysql/cli-project-url-escape.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "mysql", "cli-project-url-escape.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresMigrateApplyDatasourceFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/cli-migrate-apply-datasrc.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", "cli-migrate-apply-datasrc.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresMigrateApplyFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/cli-migrate-apply.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", "cli-migrate-apply.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresMigrateStatusFixture(t *testing.T) {
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/cli-migrate-status.txtar",
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", "cli-migrate-status.txtar"),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresApplyFixtures(t *testing.T) {
	fixtures := []string{
		"column-bit.txtar",
		"column-comment.txtar",
		"column-float.txtar",
		"column-numeric.txtar",
		"column-range.txtar",
		"index-issue-557.txtar",
		"index-type.txtar",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			results := TxtarScriptProbe{}.Run(Fixture{
				Name: "postgres/" + fixture,
				Kind: FixtureKindTxtar,
				Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
				Files: []string{
					filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
				},
			})

			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
			}
			if results[0].Outcome != OK {
				t.Fatalf("expected OK result, got %#v", results[0])
			}
		})
	}
}

func TestTxtarScriptProbeExecutesPostgresPartialIndexFixture(t *testing.T) {
	fixture := "index-partial.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresColumnDefaultFixture(t *testing.T) {
	fixture := "column-default.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresColumnArrayFixture(t *testing.T) {
	fixture := "column-array.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresColumnIntervalFixture(t *testing.T) {
	fixture := "column-interval.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresColumnTimePrecisionFixture(t *testing.T) {
	fixture := "column-time-precision.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresColumnSerialFixture(t *testing.T) {
	fixture := "column-serial.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresPrimaryKeyFixture(t *testing.T) {
	fixture := "primary-key.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresForeignKeyFixtures(t *testing.T) {
	for _, fixture := range []string{"foreign-key.txtar", "foreign-key-action.txtar"} {
		t.Run(fixture, func(t *testing.T) {
			results := TxtarScriptProbe{}.Run(Fixture{
				Name: "postgres/" + fixture,
				Kind: FixtureKindTxtar,
				Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
				Files: []string{
					filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
				},
			})

			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
			}
			if results[0].Outcome != OK {
				t.Fatalf("expected OK result, got %#v", results[0])
			}
		})
	}
}

func TestTxtarScriptProbeExecutesPostgresIndexDescFixture(t *testing.T) {
	fixture := "index-desc.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresIndexIncludeFixture(t *testing.T) {
	fixture := "index-include.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresIndexTypeBRINFixture(t *testing.T) {
	fixture := "index-type-brin.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresIndexNullsDistinctFixture(t *testing.T) {
	fixture := "index-nulls-distinct.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresColumnTextSearchFixture(t *testing.T) {
	fixture := "column-textsearch.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarHCLStatementsPreservePostgresIndexIncludeAndWhere(t *testing.T) {
	fx := Fixture{Name: "postgres/index-include.txtar"}
	data := `
schema "$db" {}

table "users" {
  schema = schema.$db
  column "name" {
    null = false
    type = text
  }
  column "active" {
    null = true
    type = boolean
  }
  index "users_name" {
    columns = [column.name]
    where = "active"
    include = [column.active]
  }
}
`
	normalized := txtarNormalizeAtlasHCL(fx, data)
	db, err := atlashcl.Parse([]byte(normalized), "case.hcl")
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Indexes) != 1 {
		t.Fatalf("raw indexes = %d, want 1", len(db.Indexes))
	}
	if strings.Join(db.Indexes[0].IncludeColumns, ",") != "active" {
		t.Fatalf("raw include columns = %#v, want [active]", db.Indexes[0].IncludeColumns)
	}

	statements, err := txtarHCLStatements(fx, "case.hcl", data)
	if err != nil {
		t.Fatal(err)
	}

	var index *ast.IndexNode
	for _, stmt := range statements {
		if node, ok := stmt.(*ast.IndexNode); ok {
			index = node
			break
		}
	}
	if index == nil {
		t.Fatalf("index not found in %#v", statements)
	}
	if index.Condition != "active" {
		t.Fatalf("condition = %q, want active", index.Condition)
	}
	if strings.Join(index.IncludeColumns, ",") != "active" {
		t.Fatalf("include columns = %#v, want [active]", index.IncludeColumns)
	}

	actual, ok := txtarTableShowSQL(fx, statements, "users")
	if !ok {
		t.Fatal("expected virtual PostgreSQL table show SQL")
	}
	want := `"users_name" btree (name) INCLUDE (active) WHERE active`
	if !strings.Contains(actual, want) {
		t.Fatalf("show SQL missing %q:\n%s", want, actual)
	}
}

func TestTxtarHCLStatementsPreservePostgresIndexTypeAndStorageParams(t *testing.T) {
	fx := Fixture{Name: "postgres/index-type-brin.txtar"}
	data := `
schema "$db" {}

table "users" {
  schema = schema.$db
  column "c" {
    null = false
    type = int
  }
  index "users_c" {
    type = BRIN
    columns = [column.c]
    page_per_range = 2
  }
}
`
	statements, err := txtarHCLStatements(fx, "case.hcl", data)
	if err != nil {
		t.Fatal(err)
	}

	var index *ast.IndexNode
	for _, stmt := range statements {
		if node, ok := stmt.(*ast.IndexNode); ok {
			index = node
			break
		}
	}
	if index == nil {
		t.Fatalf("index not found in %#v", statements)
	}
	if index.Type != "BRIN" {
		t.Fatalf("type = %q, want BRIN", index.Type)
	}
	if got := index.StorageParams["pages_per_range"]; got != "2" {
		t.Fatalf("pages_per_range = %q, want 2", got)
	}

	actual, ok := txtarTableShowSQL(fx, statements, "users")
	if !ok {
		t.Fatal("expected virtual PostgreSQL table show SQL")
	}
	want := `"users_c" brin (c) WITH (pages_per_range='2')`
	if !strings.Contains(actual, want) {
		t.Fatalf("show SQL missing %q:\n%s", want, actual)
	}
}

func TestTxtarHCLStatementsPreservePostgresNullsDistinctIndexAndUnique(t *testing.T) {
	fx := Fixture{Name: "postgres/index-nulls-distinct.txtar"}
	data := `
schema "$db" {}

table "users" {
  schema = schema.$db
  column "c" {
    type = int
  }
  index "nulls_not_distinct" {
    unique = true
    columns = [column.c]
    nulls_distinct = false
  }
  unique "nulls_not_distinct2" {
    columns = [column.c]
    nulls_distinct = false
  }
}
`
	statements, err := txtarHCLStatements(fx, "case.hcl", data)
	if err != nil {
		t.Fatal(err)
	}
	actualHCL, err := renderAtlasInspectHCL("postgresql", txtarFixtureSchemaName(fx), statements)
	if err != nil {
		t.Fatal(err)
	}
	expectedHCL := `table "users" {
  schema = schema.script_index_nulls_distinct
  column "c" {
    null = false
    type = integer
  }
  index "nulls_not_distinct" {
    unique         = true
    columns        = [column.c]
    nulls_distinct = false
  }
  unique "nulls_not_distinct2" {
    columns        = [column.c]
    nulls_distinct = false
  }
}
schema "script_index_nulls_distinct" {
}
`
	if actualHCL != expectedHCL {
		t.Fatalf("inspect HCL mismatch:\nactual:\n%s\nexpected:\n%s", actualHCL, expectedHCL)
	}

	actual, ok := txtarTableShowSQL(fx, statements, "users")
	if !ok {
		t.Fatal("expected virtual PostgreSQL table show SQL")
	}
	for _, want := range []string{
		`"nulls_not_distinct" UNIQUE, btree (c) NULLS NOT DISTINCT`,
		`"nulls_not_distinct2" UNIQUE CONSTRAINT, btree (c) NULLS NOT DISTINCT`,
	} {
		if !strings.Contains(actual, want) {
			t.Fatalf("show SQL missing %q:\n%s", want, actual)
		}
	}
}

func TestTxtarScriptProbeKeepsPostgresComplexExpressionIndexAsGap(t *testing.T) {
	fixture := "index-expr.txtar"
	results := TxtarScriptProbe{}.Run(Fixture{
		Name: "postgres/" + fixture,
		Kind: FixtureKindTxtar,
		Dir:  filepath.Join("third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres"),
		Files: []string{
			filepath.Join("..", "..", "third_party", "atlas", "upstream", "internal", "integration", "testdata", "postgres", fixture),
		},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: apply") {
		t.Fatalf("detail missing expression-index apply gap: %s", results[0].Detail)
	}
}

func TestTxtarResolveAtlasSQLTenantsRequiresExactPattern(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/cli-schema-apply-datasrc.txtar")
	if err != nil {
		t.Fatal(err)
	}
	runtime := newTxtarRuntime(string(data))
	project := runtime.files["atlas.hcl"]
	env, ok := txtarAtlasNamedBlock(project, "env", "dev")
	if !ok {
		t.Fatal("expected dev env block")
	}
	tenants, ok := txtarResolveAtlasSQLTenants(project, env, map[string]string{
		"url":     "URL",
		"pattern": "script_cli_schema_apply_datasrc",
	})
	if !ok || len(tenants) != 1 || tenants[0] != "script_cli_schema_apply_datasrc" {
		t.Fatalf("unexpected tenants: ok=%v tenants=%#v", ok, tenants)
	}
	if tenants, ok := txtarResolveAtlasSQLTenants(project, env, map[string]string{
		"url":     "URL",
		"pattern": "script_%",
	}); ok {
		t.Fatalf("expected wildcard pattern to stay unsupported, got %#v", tenants)
	}
}

func TestTxtarResolveAtlasSQLTenantsSupportsPostgresSearchPathEnv(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/postgres/cli-migrate-apply-datasrc.txtar")
	if err != nil {
		t.Fatal(err)
	}
	runtime := newTxtarRuntime(string(data))
	project := runtime.files["atlas.hcl"]
	env, ok := txtarAtlasNamedBlock(project, "env", "dev")
	if !ok {
		t.Fatal("expected dev env block")
	}
	tenants, ok := txtarResolveAtlasSQLTenants(project, env, map[string]string{
		"url":     "URL",
		"pattern": "script_cli_migrate_apply_datasrc",
	})
	if !ok || len(tenants) != 1 || tenants[0] != "script_cli_migrate_apply_datasrc" {
		t.Fatalf("unexpected tenants: ok=%v tenants=%#v", ok, tenants)
	}
}

func TestTxtarParsePostgresMigrateApplyFixtureStatements(t *testing.T) {
	data := `CREATE TABLE "users" ("id" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY, "age" bigint NOT NULL, "name" character varying NOT NULL, PRIMARY KEY ("id"));
CREATE UNIQUE INDEX "users_age_key" ON "users" ("age");
CREATE TABLE "pets" ("id" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY, "name" character varying NOT NULL, PRIMARY KEY ("id"));`

	statements, failing, err := txtarParseMigrationStatements(data)
	if err != nil {
		t.Fatal(err)
	}
	if failing != "" {
		t.Fatalf("unexpected failing statement %q", failing)
	}
	if len(statements) != 3 {
		t.Fatalf("expected 3 statements, got %d: %#v", len(statements), statements)
	}
	if _, err := txtarApplyStatementsToVirtualState(nil, statements); err != nil {
		t.Fatal(err)
	}
}

func TestRenderTxtarDBStateInspectHCLDropsIndexesForExcludedTables(t *testing.T) {
	fx := Fixture{Name: "postgres/case.txtar"}
	statements := []ast.Node{
		ast.NewCreateTable("script_case.users").AddColumn(ast.NewColumn("id", "bigint").SetNotNull()),
		&ast.IndexNode{Name: "users_id_key", Table: "script_case.users", Columns: []string{"id"}, Unique: true},
	}
	out, err := renderTxtarDBStateInspectHCL(fx, statements, []string{"users"}, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `schema "script_case" {
}
`
	if out != expected {
		t.Fatalf("unexpected HCL:\ngot:\n%s\nwant:\n%s", out, expected)
	}
}

func TestTxtarResolveSchemaInspectEnvEscapesProjectURLPassword(t *testing.T) {
	project := `variable "pass" {
  default = "&pass?"
}

locals {
  escaped_pass = urlescape(var.pass)
}

env "local" {
  url = "mysql://a8m:${local.escaped_pass}@localhost:3308/script_case"
}

env "failed" {
  url = "mysql://a8m:${var.pass}@localhost:3308/script_case"
}
`
	runtime := &txtarRuntime{files: map[string]string{"atlas.hcl": project}}
	fx := Fixture{Name: "mysql/case.txtar"}
	sourceURL, result, ok := txtarResolveSchemaInspectEnv(fx, runtime, "local")
	if !ok || result != nil {
		t.Fatalf("expected resolved local URL, ok=%v result=%#v", ok, result)
	}
	if sourceURL != "mysql://a8m:%26pass%3F@localhost:3308/script_case" {
		t.Fatalf("source URL = %q", sourceURL)
	}
	_, result, ok = txtarResolveSchemaInspectEnv(fx, runtime, "failed")
	if !ok || result == nil || !result.failed {
		t.Fatalf("expected failed raw URL result, ok=%v result=%#v", ok, result)
	}
	if !strings.Contains(result.stderr, `invalid port ":&pass" after host`) {
		t.Fatalf("expected invalid port stderr, got %q", result.stderr)
	}
}

func TestTxtarExecSQLMySQLAuthNoopIsNarrow(t *testing.T) {
	supported := []string{
		`CREATE USER IF NOT EXISTS "a8m"@"%" IDENTIFIED BY "&pass?"`,
		`GRANT ALL PRIVILEGES ON *.* TO "a8m"@"%" WITH GRANT OPTION`,
		`DROP USER "a8m"@"%"`,
	}
	for _, stmt := range supported {
		if !txtarExecSQLMySQLAuthNoop(stmt) {
			t.Fatalf("expected auth no-op support for %q", stmt)
		}
	}

	unsupported := []string{
		`CREATE USER "a8m"@"%" IDENTIFIED BY "&pass?"`,
		`CREATE DATABASE script_case`,
		`CREATE TABLE users (id int)`,
		`GRANT SELECT ON *.* TO "a8m"@"%"`,
	}
	for _, stmt := range unsupported {
		if txtarExecSQLMySQLAuthNoop(stmt) {
			t.Fatalf("expected auth no-op rejection for %q", stmt)
		}
	}
}

func TestTxtarSchemaApplyTenantJSONLogIncludesMultipleAppliedStatements(t *testing.T) {
	statements := []ast.Node{
		ast.NewCreateTable("tenant.users").AddColumn(ast.NewColumn("id", "int").SetNotNull()),
		&ast.IndexNode{Name: "idx_users_id", Table: "tenant.users", Columns: []string{"id"}},
		ast.NewCreateTable("tenant.pets").AddColumn(ast.NewColumn("id", "int").SetNotNull()),
	}
	output := txtarSchemaApplyTenantJSONLog(
		Fixture{Name: "mysql/case.txtar"},
		txtarSchemaApplyArgs{tenant: "tenant"},
		statements,
	)
	if output == "" {
		t.Fatal("expected tenant JSON log output")
	}

	var payload struct {
		Applied []string
		Tenant  string
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("expected valid JSON output, got %q: %v", output, err)
	}
	if payload.Tenant != "tenant" {
		t.Fatalf("expected tenant, got %q", payload.Tenant)
	}
	if len(payload.Applied) != 2 {
		t.Fatalf("expected two applied statements, got %#v", payload.Applied)
	}
	if strings.Contains(strings.Join(payload.Applied, "\n"), "tenant.") {
		t.Fatalf("expected unqualified applied SQL, got %#v", payload.Applied)
	}
	if !strings.Contains(payload.Applied[0], "KEY `idx_users_id` (`id`)") {
		t.Fatalf("expected inline index in first statement, got %q", payload.Applied[0])
	}
}

func TestTxtarParseInsertRows(t *testing.T) {
	tableName, rows, ok := txtarParseInsertRows("INSERT INTO $db.t (c, d) VALUES (1, 1), (1, 2), (1, 3)")
	if !ok {
		t.Fatal("expected INSERT statement to parse")
	}
	if tableName != "t" {
		t.Fatalf("table name = %q, want %q", tableName, "t")
	}
	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3", len(rows))
	}
	if rows[0]["c"] != "1" || rows[1]["d"] != "2" || rows[2]["d"] != "3" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	if _, _, ok := txtarParseInsertRows(`CREATE USER IF NOT EXISTS "a8m"@"%" IDENTIFIED BY "&pass?"`); ok {
		t.Fatal("expected unsupported DDL to stay unsupported")
	}
}

func TestTxtarExpectedApplyFailureDetectsMySQLDuplicateUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/index-unique.txtar")
	if err != nil {
		t.Fatal(err)
	}

	fx := Fixture{Name: "mysql/index-unique.txtar"}
	runtime := newTxtarRuntime(string(data))
	current, err := txtarHCLStatements(fx, "1.hcl", runtime.files["1.hcl"])
	if err != nil {
		t.Fatal(err)
	}
	next, err := txtarHCLStatements(fx, "2.fail.hcl", runtime.files["2.fail.hcl"])
	if err != nil {
		t.Fatal(err)
	}
	tableName, rows, ok := txtarParseInsertRows("INSERT INTO $db.t (c, d) VALUES (1, 1), (1, 2), (1, 3)")
	if !ok {
		t.Fatal("expected INSERT statement to parse")
	}
	got := txtarExpectedApplyFailure(fx, current, next, map[string][]txtarVirtualRow{tableName: rows})
	const want = "Error 1062: Duplicate entry '1' for key 'c'"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestTxtarParseGeneratedDropIndexAlterStatement(t *testing.T) {
	node, ok := txtarParseGeneratedDropIndexAlterStatement("ALTER TABLE `tbl` DROP INDEX `u_ref_id`")
	if !ok {
		t.Fatal("expected generated drop-index ALTER statement to parse")
	}
	drop, ok := node.(*ast.DropIndexNode)
	if !ok {
		t.Fatalf("expected DropIndexNode, got %T", node)
	}
	if drop.Name != "u_ref_id" || drop.Table != "tbl" {
		t.Fatalf("unexpected drop index node: %#v", drop)
	}

	if _, ok := txtarParseGeneratedDropIndexAlterStatement("ALTER TABLE `tbl` DROP INDEX `u_ref_id`, ADD INDEX `i_ref_id` (`ref_id`)"); ok {
		t.Fatal("expected composite ALTER statement to stay on the general parser path")
	}
}

func TestTxtarParseGeneratedPrimaryKeyAlterStatement(t *testing.T) {
	node, ok := txtarParseGeneratedPrimaryKeyAlterStatement(
		"ALTER TABLE `t1` ADD COLUMN `id2` tinytext NOT NULL, DROP PRIMARY KEY, ADD PRIMARY KEY (`id` (7), `id2` (1))",
	)
	if !ok {
		t.Fatal("expected generated primary-key ALTER statement to parse")
	}
	alter, ok := node.(*ast.AlterTableNode)
	if !ok {
		t.Fatalf("expected AlterTableNode, got %T", node)
	}
	if alter.Name != "t1" || len(alter.Operations) != 3 {
		t.Fatalf("unexpected alter node: %#v", alter)
	}
	if _, ok := alter.Operations[0].(*ast.AddColumnOperation); !ok {
		t.Fatalf("unexpected add-column operation: %#v", alter.Operations[0])
	}
	drop, ok := alter.Operations[1].(*ast.DropConstraintOperation)
	if !ok || drop.ConstraintName != "PRIMARY" {
		t.Fatalf("unexpected drop operation: %#v", alter.Operations[1])
	}
	add, ok := alter.Operations[2].(*ast.AddConstraintOperation)
	if !ok || add.Constraint == nil || add.Constraint.Type != ast.PrimaryKeyConstraint ||
		!txtarPrimaryKeyColumnsEqual(
			txtarPrimaryKey{columns: add.Constraint.ColumnParts},
			txtarPrimaryKey{columns: []ast.ConstraintColumn{
				{Name: "id", Prefix: "7"},
				{Name: "id2", Prefix: "1"},
			}},
		) {
		t.Fatalf("unexpected add primary-key operation: %#v", alter.Operations[2])
	}
}

func TestTxtarScriptProbeExecutesSQLiteSQLMigrateDiffAndSchemaDiff(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/sqlite/cli-migrate-diff-sql.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-diff-sql.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteMultifileMigrateDiff(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/sqlite/cli-migrate-diff-multifile.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-diff-multifile.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteEnvMigrateDiffFixtures(t *testing.T) {
	for _, fixture := range []string{
		"cli-migrate-diff-minimal-env.txtar",
		"cli-migrate-diff-datasrc-hcl.txtar",
		"cli-migrate-diff-datasrc-hcl-paths.txtar",
		"cli-migrate-project-multifile.txtar",
		"cli-migrate-project.txtar",
	} {
		t.Run(fixture, func(t *testing.T) {
			data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/sqlite/" + fixture)
			if err != nil {
				t.Fatal(err)
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "case.txtar")
			writeTestFile(t, path, string(data))

			results := TxtarScriptProbe{}.Run(Fixture{
				Name:  "sqlite/" + fixture,
				Kind:  FixtureKindTxtar,
				Dir:   dir,
				Files: []string{path},
			})

			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
			}
			if results[0].Outcome != OK {
				t.Fatalf("expected OK result, got %#v", results[0])
			}
		})
	}
}

func TestTxtarScriptProbeResolvesSQLiteEnvMigrationDirForHashValidate(t *testing.T) {
	runtime := newTxtarRuntime(`-- atlas.hcl --
env "local" {
  dev = "sqlite://dev"
  src = "1.hcl"
  migration {
    dir = "file://custom"
    format = atlas
  }
}
-- 1.hcl --
schema "main" {}
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
}
`)
	fx := Fixture{Name: "sqlite/custom-dir.txtar", Kind: FixtureKindTxtar}

	result, ok := runTxtarMigrateDiff(fx, runtime, txtarCommandFields("atlas migrate diff --env local"), false)
	if !ok || result.err != nil || result.failed || result.unsupported != "" {
		t.Fatalf("migrate diff result = %#v, ok = %v", result, ok)
	}
	if _, ok := runtime.files["custom/1.sql"]; !ok {
		t.Fatalf("expected custom/1.sql to be generated, files: %#v", runtime.files)
	}
	if _, ok := runtime.files["custom/atlas.sum"]; !ok {
		t.Fatalf("expected custom/atlas.sum to be generated, files: %#v", runtime.files)
	}

	result, ok = runTxtarMigrateValidate(runtime, txtarCommandFields("atlas migrate validate --env local"))
	if !ok || result.err != nil || result.failed {
		t.Fatalf("validate result = %#v, ok = %v", result, ok)
	}

	runtime.files["custom/2.sql"] = ""
	runtime.addParentDirs("custom/2.sql")
	result, ok = runTxtarMigrateValidate(runtime, txtarCommandFields("atlas migrate validate --env local"))
	if !ok || !result.failed || !strings.Contains(result.stderr, "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got result = %#v, ok = %v", result, ok)
	}

	result, ok = runTxtarMigrateHash(runtime, txtarCommandFields("atlas migrate hash --env local"))
	if !ok || result.err != nil || result.failed {
		t.Fatalf("hash result = %#v, ok = %v", result, ok)
	}
	result, ok = runTxtarMigrateValidate(runtime, txtarCommandFields("atlas migrate validate --env local"))
	if !ok || result.err != nil || result.failed {
		t.Fatalf("validate after hash result = %#v, ok = %v", result, ok)
	}
}

func TestTxtarScriptProbeKeepsEnvDirDependentCommandsBlocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff --env local --qualifier main
atlas migrate validate --env local

-- atlas.hcl --
env "local" {
  dev = "sqlite://dev"
  src = "1.hcl"
  migration {
    dir = "file://custom"
    format = atlas
  }
}
-- 1.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/custom-dir-blocked.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: atlas migrate diff")
	for _, leaked := range []string{"atlas migrate validate", "checksum file not found", "checksum mismatch"} {
		if strings.Contains(results[0].Detail, leaked) {
			t.Fatalf("dependent command leaked into detail %q: %s", leaked, results[0].Detail)
		}
	}
}

func TestTxtarAtlasProjectBlockParsingIgnoresQuotedAndCommentedBraces(t *testing.T) {
	project := `env "other" {
  note = "quoted } brace should not end this block"
  dev = "sqlite://other"
  src = "missing.hcl"
}

env "local" {
  note = "literal { } and escaped \" quote"
  // comment { should not affect nesting }
  # another comment } should not affect nesting
  dev = "sqlite://dev"
  src = "1.hcl"
  migration {
    /* block comment { } should not affect nesting */
    dir = "file://custom"
  }
}
`

	env, ok := txtarAtlasNamedBlock(project, "env", "local")
	if !ok {
		t.Fatal("local env block was not found")
	}
	devURL, ok := txtarHCLStringAttr(env, "dev")
	if !ok || devURL != "sqlite://dev" {
		t.Fatalf("dev = %q, ok = %v", devURL, ok)
	}
	migration, ok := txtarAtlasAnonymousBlock(env, "migration")
	if !ok {
		t.Fatal("migration block was not found")
	}
	dir, ok := txtarHCLStringAttr(migration, "dir")
	if !ok || dir != "file://custom" {
		t.Fatalf("migration dir = %q, ok = %v", dir, ok)
	}
}

func TestTxtarScriptProbeKeepsInitialMigrateDiffAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `exec mkdir migrations
atlas migrate diff --dev-url URL --to file://./1.hcl first
cmpmig 0 1.sql

-- 1.hcl --
schema "script_cli_migrate_diff" {}

table "users" {
  schema = schema.script_cli_migrate_diff
  column "id" {
    null = false
    type = bigint
  }
  primary_key {
    columns = [column.id]
  }
}

-- 1.sql --
-- Create "users" table
CREATE TABLE "users" ("id" bigint NOT NULL, PRIMARY KEY ("id"));
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/cli-migrate-diff.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing migrate diff gap: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeKeepsIncrementalMigrateDiffAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
atlas migrate diff --dev-url URL --to file://schema.hcl second

-- migrations/1_first.sql --
CREATE TABLE users (id bigint NOT NULL);
-- schema.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing migrate diff gap: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeChecksValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `cat payload.json
validJSON stdout

-- payload.json --
{"ok": true}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "checked 1 assertion") {
		t.Fatalf("detail missing validJSON assertion count: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeReportsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `cat payload.json
validJSON stdout

-- payload.json --
{not-json}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "validJSON stdout: invalid JSON") {
		t.Fatalf("detail missing invalid JSON error: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeReportsVirtualFileCommandFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `cp missing.sql copied.sql

-- expected.sql --
CREATE TABLE users (id INT);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "missing.sql missing") {
		t.Fatalf("detail missing cp failure: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeSkipsValidJSONAfterUnsupportedProducer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate apply --url URL --unsupported --log '{{ json . }}'
validJSON stdout
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if strings.Contains(results[0].Detail, "validJSON") {
		t.Fatalf("unsupported producer should not report dependent validJSON: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeSkipsDBAssertionsAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
exist users
synced 2.hcl
cmpshow users expected.sql
cmphcl expected.hcl

-- 1.hcl --
schema "main" {}

-- 2.hcl --
schema "main" {}
-- expected.sql --
CREATE TABLE users (id int);
-- expected.hcl --
table "users" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas schema clean") {
		t.Fatalf("detail missing original unsupported DB mutation: %s", results[0].Detail)
	}
	for _, dependent := range []string{"exist", "synced", "cmpshow", "cmphcl"} {
		if strings.Contains(results[0].Detail, dependent) {
			t.Fatalf("dependent DB assertion %q should not be reported after unsupported DB mutation: %s",
				dependent, results[0].Detail)
		}
	}
}

func TestTxtarScriptProbeReportsDBAssertionsWithoutUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `exist users
synced 1.hcl
cmpshow users expected.sql
cmphcl expected.hcl

-- 1.hcl --
schema "main" {}
-- expected.sql --
CREATE TABLE users (id int);
-- expected.hcl --
table "users" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: exist")
	assertResultDetailContains(t, results, "unsupported: synced")
	assertResultDetailContains(t, results, "unsupported: cmpshow")
	assertResultDetailContains(t, results, "unsupported: cmphcl")
}

func TestTxtarScriptProbeExecutesClearSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `clearSchema
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "executed 1 supported command")
}

func TestTxtarScriptProbeClearSchemaResetsUnsupportedDBState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
clearSchema
cmphcl expected.hcl

-- expected.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: atlas schema clean")
	assertResultDetailContains(t, results, "unsupported: cmphcl")
}

func TestTxtarScriptProbeSkipsDBURLInspectAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
atlas schema inspect --url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- expected.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: atlas schema clean")
	if strings.Contains(results[0].Detail, "atlas schema inspect") {
		t.Fatalf("dependent DB URL inspect should not be reported after unsupported DB mutation: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeKeepsFileURLInspectAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
atlas schema inspect --url file://schema.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp inspected.sql expected.sql

-- schema.sql --
CREATE TABLE users (
  id INT NOT NULL,
  PRIMARY KEY (id)
);
-- expected.sql --
-- Create "users" table
CREATE TABLE "users" ("id" integer NOT NULL, PRIMARY KEY ("id"));
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected original schema clean gap, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas schema clean") {
		t.Fatalf("detail missing original unsupported DB mutation: %s", results[0].Detail)
	}
	if strings.Contains(results[0].Detail, "atlas schema inspect") {
		t.Fatalf("file URL inspect should still execute, not be reported unsupported: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeReportsExpectedDBURLInspectFailureAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `execsql 'CREATE TABLE users (id int)'
! atlas schema inspect --env failed
stderr 'invalid port'

-- atlas.hcl --
env "failed" {
  url = "mysql://user:bad@localhost:&pass/db"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: execsql")
	if strings.Contains(results[0].Detail, "atlas schema inspect db-url") {
		t.Fatalf("DB URL inspect should be dependent after unsupported execsql: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeExecutesSQLiteExecSQLAndCmpHCL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `execsql 'CREATE TABLE tbl (col)'
cmphcl expected.hcl

-- expected.hcl --
table "tbl" {
  schema = schema.main
  column "col" {
    null = true
    type = blob
  }
}
schema "main" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/column-default.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteMigrateApplyAndCmpShow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas migrate apply
stderr 'checksum file not found'
stdout 'atlas migrate hash'
atlas migrate hash
atlas migrate apply --url URL
stdout 'Migrating to version 2 \(2 migrations in total\):'
stdout '-- migrating version 1'
stdout '-- 2 sql statements'
cmpshow users users.sql
cmpshow pets pets.sql
atlas schema inspect --url URL --exclude atlas_schema_revisions --exclude users --exclude pets
cmp stdout empty.hcl
atlas migrate apply --url URL 1
stdout 'No migration files to execute'
clearSchema
atlas migrate apply --url URL 1
stdout 'Migrating to version 1 \(1 migrations in total\):'
cmpshow users users.sql
atlas migrate apply --url URL 1
stdout 'Migrating to version 2 from 1 \(1 migrations in total\):'
cmpshow pets pets.sql

-- migrations/1_first.sql --
CREATE TABLE `+"`"+`users`+"`"+` (
  `+"`"+`id`+"`"+` integer NOT NULL,
  `+"`"+`age`+"`"+` integer NOT NULL,
  `+"`"+`name`+"`"+` TEXT NOT NULL,
  PRIMARY KEY (`+"`"+`id`+"`"+`)
);

-- migrations/2_second.sql --
CREATE TABLE `+"`"+`pets`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, `+"`"+`name`+"`"+` TEXT NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`));

-- empty.hcl --
schema "main" {
}
-- users.sql --
CREATE TABLE `+"`"+`users`+"`"+` (
  `+"`"+`id`+"`"+` integer NOT NULL,
  `+"`"+`age`+"`"+` integer NOT NULL,
  `+"`"+`name`+"`"+` TEXT NOT NULL,
  PRIMARY KEY (`+"`"+`id`+"`"+`)
)

-- pets.sql --
CREATE TABLE `+"`"+`pets`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, `+"`"+`name`+"`"+` TEXT NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`))
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-apply.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLMigrateApplyAndAlterIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql

! atlas migrate apply
stderr 'checksum file not found'
stdout 'atlas migrate hash'
atlas migrate hash
atlas migrate apply --url URL --revisions-schema $db
stdout 'Migrating to version 3 \(3 migrations in total\):'
stdout '-- migrating version 2'
stdout '-> ALTER TABLE `+"`users`"+` ADD UNIQUE INDEX `+"`age`"+` \(`+"`age`"+`\);'
stdout '-- 3 migrations'
stdout '-- 3 sql statements'
cmpshow users users.sql
cmpshow pets pets.sql
atlas migrate apply --url URL --revisions-schema $db
stdout 'No migration files to execute'
clearSchema
atlas migrate apply --url URL --revisions-schema $db 1
stdout 'Migrating to version 1 \(1 migrations in total\):'
cmpshow users users_1.sql
atlas migrate apply --url URL --revisions-schema $db 1
stdout 'Migrating to version 2 from 1 \(1 migrations in total\):'
cmpshow users users.sql
atlas migrate apply --url URL --revisions-schema $db 1
stdout 'Migrating to version 3 from 2 \(1 migrations in total\):'
cmpshow users users.sql
cmpshow pets pets.sql
atlas migrate apply --url URL --revisions-schema $db 1
stdout 'No migration files to execute'
clearSchema
atlas migrate apply --url URL --revisions-schema $db --log '{{ json . }}'
validJSON stdout
stdout '"Driver":"mysql"'
stdout '"Scheme":"mysql"'
stdout '"Dir":"file://migrations"'
stdout '"Target":"3"'
stdout '"Pending":\[{"Name":"1_first.sql","Version":"1","Description":"first"},{"Name":"2_second.sql","Version":"2","Description":"second"},{"Name":"3_third.sql","Version":"3","Description":"third"}\]'
stdout '"Applied":\["CREATE TABLE `+"`users`"+` \(`+"`id`"+` bigint NOT NULL AUTO_INCREMENT, `+"`age`"+` bigint NOT NULL, `+"`name`"+` varchar\(255\) NOT NULL, PRIMARY KEY \(`+"`id`"+`\)\) CHARSET utf8mb4 COLLATE utf8mb4_bin;"\]'
stdout '"Start":"\d\d\d\d-\d\d-\d\dT\d\d:\d\d:\d\d.[0-9]'

-- migrations/1_first.sql --
CREATE TABLE `+"`users`"+` (`+"`id`"+` bigint NOT NULL AUTO_INCREMENT, `+"`age`"+` bigint NOT NULL, `+"`name`"+` varchar(255) NOT NULL, PRIMARY KEY (`+"`id`"+`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
-- migrations/2_second.sql --
ALTER TABLE `+"`users`"+` ADD UNIQUE INDEX `+"`age`"+` (`+"`age`"+`);
-- migrations/3_third.sql --
CREATE TABLE `+"`pets`"+` (`+"`id`"+` bigint NOT NULL AUTO_INCREMENT, `+"`name`"+` varchar(255) NOT NULL, PRIMARY KEY (`+"`id`"+`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
-- users.sql --
CREATE TABLE `+"`users`"+` (
  `+"`id`"+` bigint(20) NOT NULL AUTO_INCREMENT,
  `+"`age`"+` bigint(20) NOT NULL,
  `+"`name`"+` varchar(255) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`+"`id`"+`),
  UNIQUE KEY `+"`age`"+` (`+"`age`"+`)
)
-- mysql8/users.sql --
CREATE TABLE `+"`users`"+` (
  `+"`id`"+` bigint NOT NULL AUTO_INCREMENT,
  `+"`age`"+` bigint NOT NULL,
  `+"`name`"+` varchar(255) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`+"`id`"+`),
  UNIQUE KEY `+"`age`"+` (`+"`age`"+`)
)
-- users_1.sql --
CREATE TABLE `+"`users`"+` (
  `+"`id`"+` bigint(20) NOT NULL AUTO_INCREMENT,
  `+"`age`"+` bigint(20) NOT NULL,
  `+"`name`"+` varchar(255) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`+"`id`"+`)
)
-- mysql8/users_1.sql --
CREATE TABLE `+"`users`"+` (
  `+"`id`"+` bigint NOT NULL AUTO_INCREMENT,
  `+"`age`"+` bigint NOT NULL,
  `+"`name`"+` varchar(255) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`+"`id`"+`)
)
-- pets.sql --
CREATE TABLE `+"`pets`"+` (
  `+"`id`"+` bigint(20) NOT NULL AUTO_INCREMENT,
  `+"`name`"+` varchar(255) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`+"`id`"+`)
)
-- mysql8/pets.sql --
CREATE TABLE `+"`pets`"+` (
  `+"`id`"+` bigint NOT NULL AUTO_INCREMENT,
  `+"`name`"+` varchar(255) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`+"`id`"+`)
)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/cli-migrate-apply.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteMigrateApplyTxModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
cp broken.sql migrations/3_third.sql
! atlas migrate apply --url URL --tx-mode invalid
stderr 'unknown tx-mode "invalid"'
! atlas migrate apply --url URL --tx-mode all
stderr 'executing statement "THIS IS A FAILING STATEMENT;" from version "3"'
atlas schema inspect --url URL --exclude atlas_schema_revisions
cmp stdout empty.hcl
atlas migrate apply --url URL 1
cmpshow users users.sql
! atlas migrate apply --url URL --tx-mode all
stderr 'executing statement "THIS IS A FAILING STATEMENT;" from version "3"'
atlas schema inspect --url URL --exclude atlas_schema_revisions --exclude users
cmp stdout empty.hcl
clearSchema
cp broken.sql migrations/3_third.sql
atlas migrate hash
! atlas migrate apply --url URL --tx-mode file
stderr 'executing statement "THIS IS A FAILING STATEMENT;" from version "3"'
cmpshow users users.sql
cmpshow pets pets.sql
atlas schema inspect --url URL --exclude atlas_schema_revisions --exclude users --exclude pets
cmp stdout empty.hcl
clearSchema
cp broken.sql migrations/3_third.sql
atlas migrate hash
! atlas migrate apply --url URL --tx-mode none
stderr 'executing statement "THIS IS A FAILING STATEMENT;" from version "3"'
cmpshow users users.sql
cmpshow pets pets.sql
atlas schema inspect --url URL --exclude atlas_schema_revisions --exclude users --exclude pets
cmp stdout broken.hcl

-- migrations/1_first.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`));
-- migrations/2_second.sql --
CREATE TABLE `+"`"+`pets`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`));
-- broken.sql --
CREATE TABLE `+"`"+`broken`+"`"+` (`+"`"+`id`+"`"+` integer);
THIS IS A FAILING STATEMENT;

-- empty.hcl --
schema "main" {
}
-- broken.hcl --
table "broken" {
  schema = schema.main
  column "id" {
    null = true
    type = integer
  }
}
schema "main" {
}
-- users.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`))
-- pets.sql --
CREATE TABLE `+"`"+`pets`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`))
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-apply.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesGenericApplyOutsideCLIInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmphcl expected.hcl

-- 1.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = integer
  }
}
schema "main" {
}

-- expected.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = integer
  }
}
schema "main" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/autoincrement.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteMigrateSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas migrate set 0
stderr 'checksum file not found'
atlas migrate hash
! atlas migrate set --url URL
stderr 'accepts 1 arg\(s\), received 0'
! atlas migrate set 4 --url URL
stderr 'migration with version "4" not found'
atlas migrate set 1 --url URL
atlas migrate apply 1 --url URL --dry-run
stdout 'Migrating to version 2 from 1'
atlas migrate set 3 --url URL
atlas migrate apply --url URL
stdout 'No migration files to execute'
clearSchema
mv broken.sql migrations/4.sql
atlas migrate hash
! atlas migrate apply --url URL --tx-mode none
stdout 'Migrating to version 4'
atlas migrate set 4 --url URL
atlas migrate apply --url URL
stdout 'No migration files to execute'

-- migrations/1_first.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`));
-- migrations/2_second.sql --
CREATE TABLE `+"`"+`pets`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`));
-- migrations/3_third.sql --
CREATE TABLE `+"`"+`vets`+"`"+` (`+"`"+`id`+"`"+` integer NOT NULL, PRIMARY KEY (`+"`"+`id`+"`"+`));
-- broken.sql --
CREATE TABLE `+"`"+`broken`+"`"+` (`+"`"+`id`+"`"+` integer);
asdf ALTER TABLE `+"`"+`users`+"`"+` ADD UNIQUE INDEX `+"`"+`name`+"`"+` (`+"`"+`name`+"`"+`);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-set.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteApplyWithGeneratedColumnCmpShow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users expected.sql

-- 1.hcl --
schema "main" {}

table "users" {
  schema = schema.main
  column "a" {
    null = false
    type = int
  }
  column "b" {
    type = int
    as = "1"
  }
  column "c" {
    type = int
    as {
      expr = "a * 2"
      type = STORED
    }
  }
}

-- expected.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`a`+"`"+` int NOT NULL, `+"`"+`b`+"`"+` int NOT NULL AS (1) VIRTUAL, `+"`"+`c`+"`"+` int NOT NULL AS (a * 2) STORED)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/column-generated.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesPostgresIdentityColumnCmpShow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users expected.sql

-- 1.hcl --
schema "$db" {}

table "users" {
  schema = schema.$db
  column "name" {
    null = false
    type = int
    identity {
      generated = ALWAYS
      start = 10
      increment = 10
    }
  }
}

-- expected.sql --
                  Table "script_column_identity.users"
 Column |  Type   | Collation | Nullable |           Default
--------+---------+-----------+----------+------------------------------
 name   | integer |           | not null | generated always as identity
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/column-identity.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarNormalizeMySQLShowSQL(t *testing.T) {
	actual := "-- Create \"users\" table\n" +
		"CREATE TABLE `users` (`rank` bigint NOT NULL, UNIQUE KEY `rank_idx` (`rank`)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;\n"
	expected := "CREATE TABLE `users` (\n" +
		"  `rank` bigint(20) NOT NULL,\n" +
		"  UNIQUE KEY `rank_idx` (`rank`)\n" +
		")\n"

	if txtarNormalizeMySQLShowSQL(actual) != txtarNormalizeMySQLShowSQL(expected) {
		t.Fatalf("normalized MySQL show SQL mismatch:\ngot  %q\nwant %q",
			txtarNormalizeMySQLShowSQL(actual), txtarNormalizeMySQLShowSQL(expected))
	}
}

func TestTxtarNormalizeMySQLShowSQLPreservesTinyintOne(t *testing.T) {
	booleanAlias := "CREATE TABLE `users` (`a` tinyint(1) NOT NULL)"
	plainTinyint := "CREATE TABLE `users` (`a` tinyint(4) NOT NULL)"

	if txtarNormalizeMySQLShowSQL(booleanAlias) == txtarNormalizeMySQLShowSQL(plainTinyint) {
		t.Fatalf("tinyint(1) must remain distinct from plain tinyint:\ngot  %q\nwant not %q",
			txtarNormalizeMySQLShowSQL(booleanAlias), txtarNormalizeMySQLShowSQL(plainTinyint))
	}
}

func TestTxtarScriptProbeExecutesMySQLBoolColumnApplyCmpShowAndSynced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users 1.sql
synced 2.hcl
apply 3.hcl
cmpshow users 3.sql
synced 3.hcl

-- 1.hcl --
schema "$db" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  column "a" {
    type = bool
  }
  column "b" {
    type = boolean
  }
  column "c" {
    type = tinyint(1)
  }
}

-- 1.sql --
CREATE TABLE `+"`"+`users`+"`"+` (
  `+"`"+`a`+"`"+` tinyint(1) NOT NULL,
  `+"`"+`b`+"`"+` tinyint(1) NOT NULL,
  `+"`"+`c`+"`"+` tinyint(1) NOT NULL
)
-- 2.hcl --
schema "$db" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  column "a" {
    type = boolean
  }
  column "b" {
    type = tinyint(1)
  }
  column "c" {
    type = bool
  }
}

-- 3.hcl --
schema "$db" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  column "a" {
    type = boolean
  }
  column "b" {
    type = tinyint
  }
  column "c" {
    type = bool
  }
}

-- 3.sql --
CREATE TABLE `+"`"+`users`+"`"+` (
  `+"`"+`a`+"`"+` tinyint(1) NOT NULL,
  `+"`"+`b`+"`"+` tinyint(4) NOT NULL,
  `+"`"+`c`+"`"+` tinyint(1) NOT NULL
)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-bool.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLColumnDefaultExprFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/column-default-expr.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-default-expr.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLColumnCharsetFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/column-charset.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-charset.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLApplyCmpShowAndExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
exist users
cmpshow users 1.sql
apply 0.hcl
! exist users

-- 1.hcl --
schema "$db" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  column "rank" {
    type = bigint
  }
  index "rank_idx" {
    unique = true
    columns = [table.users.column.rank]
  }
}

-- 1.sql --
CREATE TABLE `+"`"+`users`+"`"+` (
  `+"`"+`rank`+"`"+` bigint(20) NOT NULL,
  UNIQUE KEY `+"`"+`rank_idx`+"`"+` (`+"`"+`rank`+"`"+`)
)

-- 0.hcl --
schema "$db" {
  charset = "$charset"
  collate = "$collate"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/index-add-drop.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLTableEngineApplyAndCmpHCL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8

apply 1.hcl
cmphcl 1.inspect.hcl
apply 2.hcl
cmphcl 2.inspect.hcl
apply 3.hcl
cmphcl 3.inspect.hcl

-- 1.hcl --
schema "script_table_engine" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  engine = InnoDB
  column "name" {
    null = false
    type = varchar(255)
  }
  charset = "$charset"
  collate = "$collate"
}

-- 1.inspect.hcl --
table "users" {
  schema = schema.script_table_engine
  column "name" {
    null = false
    type = varchar(255)
  }
}
schema "script_table_engine" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
-- 2.hcl --
schema "script_table_engine" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  engine = MyISAM
  column "name" {
    null = false
    type = varchar(255)
  }
  charset = "$charset"
  collate = "$collate"
}

-- 2.inspect.hcl --
table "users" {
  schema = schema.script_table_engine
  engine = MyISAM
  column "name" {
    null = false
    type = varchar(255)
  }
}
schema "script_table_engine" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
-- 3.hcl --
schema "script_table_engine" {
  charset = "$charset"
  collate = "$collate"
}

table "users" {
  schema = schema.$db
  column "name" {
    null = false
    type = varchar(255)
  }
  charset = "$charset"
  collate = "$collate"
}

-- 3.inspect.hcl --
table "users" {
  schema = schema.script_table_engine
  column "name" {
    null = false
    type = varchar(255)
  }
}
schema "script_table_engine" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/table-engine.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarMySQLApplyTableOptionsSupportedKeepsUnknownTableOptionsRed(t *testing.T) {
	if !txtarMySQLApplyTableOptionsSupported("mysql", map[string]string{
		"ENGINE":  "MyISAM",
		"CHARSET": "utf8mb4",
		"COLLATE": "utf8mb4_0900_ai_ci",
	}) {
		t.Fatal("expected MySQL default table charset/collate and MyISAM engine to be supported")
	}
	if txtarMySQLApplyTableOptionsSupported("mysql", map[string]string{"ENGINE": "MEMORY"}) {
		t.Fatal("unexpectedly accepted arbitrary MySQL table engine")
	}
	if txtarMySQLApplyTableOptionsSupported("mysql", map[string]string{"CHARSET": "latin1"}) {
		t.Fatal("unexpectedly accepted non-default MySQL table charset")
	}
	if txtarMySQLApplyTableOptionsSupported("mysql", map[string]string{"COLLATE": "latin1_bin"}) {
		t.Fatal("unexpectedly accepted non-default MySQL table collation")
	}
}

func TestTxtarScriptProbeKeepsUnsupportedGenericApplyBlockingDBAssertions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users expected.sql

-- 1.hcl --
schema "main" {}

table "users" {
  schema = schema.main
  column "a" {
    null = false
    type = int
    comment = "still unsupported by virtual apply"
  }
}

-- expected.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`a`+"`"+` int NOT NULL)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/comment.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: apply")
	if strings.Contains(results[0].Detail, "cmpshow") {
		t.Fatalf("dependent cmpshow should be blocked after unsupported apply: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeExecutesSQLiteApplyWithPartialIndexCmpShow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users 1.sql

-- 1.hcl --
schema "main" {}

table "users" {
  schema = schema.main
  column "name" {
    null = false
    type = text
  }
  column "active" {
    null = true
    type = boolean
  }
  index "users_name" {
    columns = [column.name]
    where = "active"
  }
}

-- 1.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`name`+"`"+` text NOT NULL, `+"`"+`active`+"`"+` boolean NULL)
CREATE INDEX `+"`"+`users_name`+"`"+` ON `+"`"+`users`+"`"+` (`+"`"+`name`+"`"+`) WHERE active
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/index-partial.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteTableOptionsFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/sqlite/table-options.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/table-options.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteIndexPartFixtures(t *testing.T) {
	for _, fixture := range []string{"index-desc.txtar", "index-expr.txtar"} {
		t.Run(fixture, func(t *testing.T) {
			data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/sqlite/" + fixture)
			if err != nil {
				t.Fatal(err)
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "case.txtar")
			writeTestFile(t, path, string(data))

			results := TxtarScriptProbe{}.Run(Fixture{
				Name:  "sqlite/" + fixture,
				Kind:  FixtureKindTxtar,
				Dir:   dir,
				Files: []string{path},
			})

			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
			}
			if results[0].Outcome != OK {
				t.Fatalf("expected OK result, got %#v", results[0])
			}
		})
	}
}

func TestTxtarScriptProbeExecutesMySQLIndexDescFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/index-desc.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/index-desc.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLIndexExprFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/index-expr.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/index-expr.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeKeepsMariaDBIndexExprUnsupported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only maria*

apply 1.hcl
cmpshow users 1.sql

-- 1.hcl --
schema "script_case" {}

table "users" {
  schema = schema.script_case
  column "name" {
    null = false
    type = varchar(128)
  }
  index "users_lower_name" {
    on {
      expr = "lower(`+"`name`"+`)"
    }
  }
}

-- 1.sql --
CREATE TABLE `+"`users`"+` (
  `+"`name`"+` varchar(128) NOT NULL,
  KEY `+"`users_lower_name`"+` ((lower(`+"`name`"+`)))
)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/mariadb-index-expr.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: apply")
}

func TestTxtarScriptProbeExecutesMySQLColumnGeneratedInspectFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/column-generated-inspect.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-generated-inspect.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLColumnGeneratedFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/column-generated.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-generated.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeChecksMySQLColumnGeneratedExpectedFailureMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql*

apply 1.hcl
! apply 2.hcl 'wrong generated column message'

-- 1.hcl --
schema "script_case" {}

table "users" {
  schema = schema.script_case
  column "a" {
    type = int
  }
  column "b" {
    type = int
    as = "a * 2"
  }
}

-- 2.hcl --
schema "script_case" {}

table "users" {
  schema = schema.script_case
  column "a" {
    type = int
  }
  column "b" {
    type = int
  }
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/column-generated-wrong-message.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: apply")
}

func TestTxtarScriptProbeKeepsMariaDBGeneratedColumnsUnsupported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only maria*

apply 1.hcl
cmphcl 1.inspect.hcl

-- 1.hcl --
schema "script_case" {}

table "users" {
  schema = schema.script_case
  column "a" {
    type = int
  }
  column "b" {
    type = int
    as = "a * 2"
  }
}

-- 1.inspect.hcl --
table "users" {
  schema = schema.script_case
}
schema "script_case" {
  charset = "utf8mb4"
  collate = "utf8mb4_general_ci"
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/mariadb-column-generated.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: apply")
}

func TestRenderAtlasInspectHCLRejectsIndexWithoutRenderedTable(t *testing.T) {
	_, err := renderAtlasInspectHCL("mysql", "script_case", []ast.Node{
		&ast.IndexNode{Name: "idx_missing", Table: "missing", Columns: []string{"id"}},
	})
	if !errors.Is(err, errUnsupportedInspectHCL) {
		t.Fatalf("expected unsupported inspect HCL error, got %v", err)
	}
}

func TestRenderAtlasInspectHCLRejectsEmptyIndexParts(t *testing.T) {
	_, err := renderAtlasInspectHCL("mysql", "script_case", []ast.Node{
		&ast.CreateTableNode{
			Name:    "users",
			Columns: []*ast.ColumnNode{{Name: "id", Type: "int", Nullable: true}},
		},
		&ast.IndexNode{Name: "idx_empty", Table: "users"},
	})
	if !errors.Is(err, errUnsupportedInspectHCL) {
		t.Fatalf("expected unsupported inspect HCL error, got %v", err)
	}
}

func TestTxtarScriptProbeExecutesMySQLForeignKeyAddFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/foreign-key-add.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/foreign-key-add.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarExpectedApplyFailureDetectsMySQLForeignKeySetNullOnNotNullColumn(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/foreign-key-add.txtar")
	if err != nil {
		t.Fatal(err)
	}

	fx := Fixture{Name: "mysql/foreign-key-add.txtar"}
	runtime := newTxtarRuntime(string(data))
	statements, err := txtarHCLStatements(fx, "invalid-on-delete-action.hcl", runtime.files["invalid-on-delete-action.hcl"])
	if err != nil {
		t.Fatal(err)
	}

	got := txtarExpectedApplyFailure(fx, nil, statements, nil)
	const want = `foreign key constraint was "author_id" SET NULL, but column "author_id" is NOT NULL`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestTxtarMySQLForeignKeyAddBaseStateIsSupported(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/foreign-key-add.txtar")
	if err != nil {
		t.Fatal(err)
	}

	fx := Fixture{Name: "mysql/foreign-key-add.txtar"}
	runtime := newTxtarRuntime(string(data))
	statements, err := txtarHCLStatements(fx, "1.hcl", runtime.files["1.hcl"])
	if err != nil {
		t.Fatal(err)
	}
	if !txtarFixtureSupportsVirtualApply(fx, statements) {
		t.Fatalf("expected 1.hcl to be supported")
	}
}

func TestTxtarScriptProbeExecutesMySQLForeignKeyModifyActionFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/foreign-key-modify-action.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/foreign-key-modify-action.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLCompositeForeignKeyFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/foreign-key.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/foreign-key.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLPrimaryKeyFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/primary-key.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/primary-key.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLIndexPrefixFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/index-prefix.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/index-prefix.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMySQLIndexTypeFixture(t *testing.T) {
	data, err := os.ReadFile("../../third_party/atlas/upstream/internal/integration/testdata/mysql/index-type.txtar")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, string(data))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/index-type.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeCmpShowDoesNotIgnoreIndexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users missing-index.sql

-- 1.hcl --
schema "main" {}

table "users" {
  schema = schema.main
  column "name" {
    null = false
    type = text
  }
  index "users_name" {
    columns = [column.name]
  }
}

-- missing-index.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`name`+"`"+` text NOT NULL)
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/index-partial.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "cmpshow users missing-index.sql did not match")
}

func TestTxtarScriptProbeCmpShowKeepsWhereExpressionQuotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
cmpshow users wrong-where.sql

-- 1.hcl --
schema "main" {}

table "users" {
  schema = schema.main
  column "name" {
    null = false
    type = text
  }
  column "active" {
    null = true
    type = boolean
  }
  index "users_name" {
    columns = [column.name]
    where = "active = \"yes\""
  }
}

-- wrong-where.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`name`+"`"+` text NOT NULL, `+"`"+`active`+"`"+` boolean NULL)
CREATE INDEX `+"`"+`users_name`+"`"+` ON `+"`"+`users`+"`"+` (`+"`"+`name`+"`"+`) WHERE active = `+"`"+`yes`+"`"+`
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/index-partial.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "cmpshow users wrong-where.sql did not match")
}

func TestTxtarScriptProbeExecutesApplyDBURLInspectWithExcludes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
atlas schema inspect -u URL > inspected.hcl
cmp inspected.hcl script_cli_inspect.hcl
atlas schema inspect -u URL --exclude "users" > inspected.hcl
cmp inspected.hcl notable.hcl
atlas schema inspect -u URL --exclude "*.[ab]*" > inspected.hcl
cmp inspected.hcl id.hcl
atlas schema inspect -u URL --exclude "*.*" > inspected.hcl
cmp inspected.hcl nocolumn.hcl

-- 1.hcl --
table "users" {
  schema = schema.$db
  column "id" {
    null = false
    type = int
  }
  column "a" {
    null = false
    type = int
  }
  column "b" {
    null = false
    type = int
  }
  column "ab" {
    null = false
    type = int
  }
  column "ac" {
    null = false
    type = int4
  }
}
schema "$db" {
}
-- script_cli_inspect.hcl --
table "users" {
  schema = schema.script_cli_inspect
  column "id" {
    null = false
    type = integer
  }
  column "a" {
    null = false
    type = integer
  }
  column "b" {
    null = false
    type = integer
  }
  column "ab" {
    null = false
    type = integer
  }
  column "ac" {
    null = false
    type = integer
  }
}
schema "script_cli_inspect" {
}
-- notable.hcl --
schema "script_cli_inspect" {
}
-- id.hcl --
table "users" {
  schema = schema.script_cli_inspect
  column "id" {
    null = false
    type = integer
  }
}
schema "script_cli_inspect" {
}
-- nocolumn.hcl --
table "users" {
  schema = schema.script_cli_inspect
}
schema "script_cli_inspect" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/cli-inspect.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteApplyDBURLInspectWithExcludes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
atlas schema inspect -u URL > inspected.hcl
cmp inspected.hcl 1.hcl
atlas schema inspect -u URL --exclude "users" > inspected.hcl
cmp inspected.hcl notable.hcl
atlas schema inspect -u URL --exclude "*.[ab]*" > inspected.hcl
cmp inspected.hcl id.hcl
atlas schema inspect -u URL --exclude "*.*" > inspected.hcl
cmp inspected.hcl nocolumn.hcl

-- 1.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
  column "a" {
    null = false
    type = int
  }
  column "b" {
    null = false
    type = int
  }
  column "ab" {
    null = false
    type = int
  }
  column "ac" {
    null = false
    type = uint64
  }
}
schema "main" {
}
-- notable.hcl --
schema "main" {
}
-- id.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
}
schema "main" {
}
-- nocolumn.hcl --
table "users" {
  schema = schema.main
}
schema "main" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-inspect.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeKeepsPostgresExecSQLUniqueConstraintAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `execsql 'CREATE TABLE script_index_unique_constraint.users (name text, last text, nickname text UNIQUE, UNIQUE(name, last))'
cmphcl expected.hcl

-- expected.hcl --
table "users" {
  schema = schema.script_index_unique_constraint
  column "name" {
    null = true
    type = text
  }
  column "last" {
    null = true
    type = text
  }
  column "nickname" {
    null = true
    type = text
  }
  unique "users_name_last_key" {
    columns = [column.name, column.last]
  }
  unique "users_nickname_key" {
    columns = [column.nickname]
  }
}
schema "script_index_unique_constraint" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/index-unique-constraint.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: execsql")
	if strings.Contains(results[0].Detail, "cmphcl") {
		t.Fatalf("dependent cmphcl should not be reported after unsupported execsql: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeKeepsCmpHCLWithoutVirtualDBStateAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `cmphcl expected.hcl

-- expected.hcl --
schema "main" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "unsupported: cmphcl")
}

func TestTxtarScriptProbeExecutesMigrateHash(t *testing.T) {
	expected := atlasSumBytes(t, map[string]string{
		"1_first.sql": "SELECT 1;\n",
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
cmp migrations/atlas.sum expected.sum

-- migrations/1_first.sql --
SELECT 1;
-- expected.sum --
`+string(expected))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "executed 1 supported command") {
		t.Fatalf("detail missing migrate hash execution: %s", results[0].Detail)
	}
	if !strings.Contains(results[0].Detail, "checked 1 assertion") {
		t.Fatalf("detail missing atlas.sum assertion: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeMigrateHashUpdatesAfterFileChange(t *testing.T) {
	expected := atlasSumBytes(t, map[string]string{
		"1_first.sql":  "SELECT 1;\n",
		"2_second.sql": "SELECT 2;\n",
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
cp two.sql migrations/2_second.sql
atlas migrate hash
cmp migrations/atlas.sum expected.sum

-- migrations/1_first.sql --
SELECT 1;
-- two.sql --
SELECT 2;
-- expected.sum --
`+string(expected))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "executed 3 supported command") {
		t.Fatalf("detail missing migrate hash and cp execution: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeExecutesCleanMigrateStatus(t *testing.T) {
	expected := atlasSumBytes(t, map[string]string{
		"1.sql": "CREATE TABLE users (id int);\n",
		"2.sql": "ALTER TABLE users ADD COLUMN name text;\n",
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
atlas migrate status --url URL --revisions-schema $db
cmp stdout status_clean.txt

-- migrations/1.sql --
CREATE TABLE users (id int);
-- migrations/2.sql --
ALTER TABLE users ADD COLUMN name text;
-- migrations/atlas.sum --
`+string(expected)+`-- status_clean.txt --
Migration Status: PENDING
  -- Current Version: No migration applied yet
  -- Next Version:    1
  -- Executed Files:  0
  -- Pending Files:   2
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "executed 2 supported command") {
		t.Fatalf("detail missing hash/status execution: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeKeepsMigrateStatusWithoutChecksumAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate status --url URL --revisions-schema $db

-- migrations/1.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate status") {
		t.Fatalf("detail missing migrate status gap: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeChecksMigrateSetValidationFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas migrate set 0
stderr 'checksum file not found'
stdout 'checksum error'

atlas migrate hash

! atlas migrate set --url URL
stderr 'accepts 1 arg\(s\), received 0'

! atlas migrate set --url URL foo bar
stderr 'accepts 1 arg\(s\), received 2'

-- migrations/1.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMigrateSetRevisionUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
atlas migrate set 1 --url URL
atlas migrate apply --url URL
stdout 'No migration files to execute'

-- migrations/1.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesMigrateValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
atlas migrate validate

-- migrations/1.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected migrate validate OK, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "executed 2 supported command(s)")
}

func TestTxtarScriptProbeReportsMigrateValidateChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
touch migrations/2.sql
! atlas migrate validate
stderr 'Error: checksum mismatch'

-- migrations/1.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected expected validate failure to satisfy stderr assertion, got %#v", results[0])
	}
	assertResultDetailContains(t, results, "checked 1 assertion(s)")
}

func TestTxtarScriptProbeSkipsMigrateHashAfterUnsupportedMigrationFileProducer(t *testing.T) {
	expected := atlasSumBytes(t, map[string]string{
		"1_first.sql": "SELECT 1;\n",
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff
atlas migrate hash
cmp migrations/atlas.sum expected.sum

-- migrations/1_first.sql --
SELECT 1;
-- expected.sum --
`+string(expected))

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing original unsupported migration producer: %s", results[0].Detail)
	}
	if strings.Contains(results[0].Detail, "atlas migrate hash") {
		t.Fatalf("hash blocked by unsupported migration file should not be reported as independently unsupported: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeSkipsMigrateValidateAndNewAfterUnsupportedMigrationFileProducer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff
atlas migrate validate
atlas migrate new 2
cmpmig 0 expected.sql

-- schema.hcl --
schema "main" {}
-- expected.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing original unsupported migration file producer: %s", results[0].Detail)
	}
	for _, dependent := range []string{"atlas migrate validate", "atlas migrate new", "cmpmig"} {
		if strings.Contains(results[0].Detail, dependent) {
			t.Fatalf("dependent command %q should not be reported after unsupported migration producer: %s",
				dependent, results[0].Detail)
		}
	}
}

func TestTxtarScriptProbeSkipsSchemaDiffAfterUnsupportedMigrationFileProducer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff --dir file://migrations
atlas schema diff --from file://migrations --to file://schema.sql

-- schema.sql --
CREATE TABLE users (id int);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing original unsupported migration file producer: %s", results[0].Detail)
	}
	if strings.Contains(results[0].Detail, "atlas schema diff") {
		t.Fatalf("dependent schema diff should not be reported after unsupported migration producer: %s",
			results[0].Detail)
	}
}

func TestTxtarScriptProbeKeepsIndependentSchemaDiffGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema diff --from file://from.sql --to file://to.sql

-- from.sql --
CREATE TABLE users (id int);

-- to.sql --
CREATE TABLE users (id int, name text);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas schema diff") {
		t.Fatalf("detail missing independent schema diff gap: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeDoesNotSkipDBAssertionsAfterExpectedFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! apply invalid.hcl 'expected failure'
exist users

-- invalid.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: apply")
	assertResultDetailContains(t, results, "unsupported: exist")
}

func TestTxtarScriptProbeDoesNotSkipDBAssertionsAfterDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate hash
atlas migrate apply --url URL --dry-run
synced 1.hcl

-- migrations/1_first.sql --
CREATE TABLE users (id integer);
-- 1.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: synced")
}

func TestTxtarScriptProbeSkipsMigrationCommandsAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
atlas migrate status --url URL
atlas migrate apply --url URL
atlas migrate set 1 --url URL
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: atlas schema clean")
	for _, dependent := range []string{"atlas migrate status", "atlas migrate apply", "atlas migrate set"} {
		if strings.Contains(results[0].Detail, dependent) {
			t.Fatalf("dependent command %q should not be reported after unsupported DB mutation: %s",
				dependent, results[0].Detail)
		}
	}
}

func TestTxtarScriptProbeSkipsSchemaMutationCommandsAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
atlas schema apply --url URL --to file://2.hcl
atlas schema clean --url URL

-- 2.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: atlas schema clean")
	for _, dependent := range []string{"atlas schema apply"} {
		if strings.Contains(results[0].Detail, dependent) {
			t.Fatalf("dependent command %q should not be reported after unsupported DB mutation: %s",
				dependent, results[0].Detail)
		}
	}
}

func TestTxtarScriptProbeSkipsRawDBCommandsAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema clean --url URL
apply 2.hcl
execsql 'INSERT INTO users (id) VALUES (1)'
! exist users

-- 2.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	assertResultDetailContains(t, results, "unsupported: atlas schema clean")
	for _, dependent := range []string{"execsql", "exist"} {
		if strings.Contains(results[0].Detail, dependent) {
			t.Fatalf("dependent command %q should not be reported after unsupported DB mutation: %s",
				dependent, results[0].Detail)
		}
	}
}

func TestTxtarScriptProbeSkipsVirtualFileCommandBlockedByUnsupportedRedirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff > out.txt
exec cat out.txt
stdout 'CREATE TABLE users'
exec touch out.txt
cmp out.txt empty.sql

-- empty.sql --
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing original unsupported command: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeClassifiesUnsupportedExecWrappedCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `exec atlas migrate diff
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas migrate diff") {
		t.Fatalf("detail missing wrapped command key: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeChecksExpectedSchemaInspectFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema inspect -u file://a.sql --format '{{ sql . }}'
stderr 'Error: --dev-url cannot be empty'

-- a.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeChecksExpectedSchemaInspectMissingURLFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema inspect
stderr '"url" not set'
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeChecksExpectedSchemaApplyValidationFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema apply -f 1.hcl
stderr '"url" not set'

! atlas schema apply --url URL
stderr 'one of flag\(s\) "file" or "to" is required'

! atlas schema apply -f atlas.hcl -u URL
stderr 'cannot parse project file'

-- atlas.hcl --
env "local" {
  url = "URL"
  src = "./1.hcl"
}
-- 1.hcl --
schema "main" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteSchemaApplyMultifileCmpShow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema apply -f users.hcl -f schema.hcl -u URL --auto-approve
cmpshow users expected.sql

-- users.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
  column "status" {
    null = true
    type = text
    default = "hello"
  }
}
-- schema.hcl --
schema "main" {
}
-- expected.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`id`+"`"+` int NOT NULL, `+"`"+`status`+"`"+` text NULL DEFAULT 'hello')
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-apply-multifile.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteSchemaApplyToFileAndDBInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema apply --url URL --dev-url DEV_URL --to file://schema.v1.hcl --auto-approve
atlas schema apply --url URL --dev-url DEV_URL --to file://schema.v1.hcl --auto-approve
stdout 'Schema is synced, no changes to be made'
atlas schema inspect --url URL > got
cmp schema.v1.hcl.inspected got
atlas schema apply --url URL --dev-url DEV_URL --to file://schema.v2.hcl --auto-approve
atlas schema apply --url URL --dev-url DEV_URL --to file://schema.v2.hcl --auto-approve
stdout 'Schema is synced, no changes to be made'
atlas schema inspect --url URL > got
cmp schema.v2.hcl.inspected got

-- schema.v1.hcl --
table "t" {
  schema = schema.main
  column "c" {
    null = true
    type = sql("USER_DEFINED")
  }
}
schema "main" {
}
-- schema.v1.hcl.inspected --
table "t" {
  schema = schema.main
  column "c" {
    null = true
    type = sql("USER_DEFINED")
  }
}
schema "main" {
}
-- schema.v2.hcl --
table "t" {
  schema = schema.main
  column "c" {
    null = true
    type = sql("USER_TYPE")
  }
}
schema "main" {
}
-- schema.v2.hcl.inspected --
table "t" {
  schema = schema.main
  column "c" {
    null = true
    type = sql("USER_TYPE")
  }
}
schema "main" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/column-user-defined.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteSchemaApplyEnvSourceAndInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema apply --env local --auto-approve
atlas schema inspect --env local > inspected.hcl
cmp 1.hcl inspected.hcl
atlas schema apply --env local --auto-approve -f 2.hcl
atlas schema inspect --env local > inspected.hcl
cmp 2.hcl inspected.hcl

-- atlas.hcl --
env "local" {
  url = "URL"
  src = "./1.hcl"
}
-- 1.hcl --
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
}
schema "main" {
}
-- 2.hcl --
table "other" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
}
schema "main" {
}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-schema-project-file.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteSchemaApplyEnvVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema apply --env local --auto-approve
stderr 'missing value for required variable "user_status_default"'

atlas schema apply --env local --auto-approve --var user_status_default=hello
cmpshow users expected.sql

-- atlas.hcl --
variable "user_status_default" {
  type = string
}
env "local" {
  url = "URL"
  src = "./1.hcl"
  def_val = var.user_status_default
}
-- 1.hcl --
variable "def_val" {
  type = string
}
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
  column "status" {
    null = true
    type = text
    default = var.def_val
  }
}
schema "main" {
}
-- expected.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`id`+"`"+` int NOT NULL, `+"`"+`status`+"`"+` text NULL DEFAULT 'hello')
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-project-vars.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeReportsSQLiteSchemaApplyMissingSourceVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema apply --env local --auto-approve
stderr 'missing value for required variable "def_val"'

atlas schema apply --env local_with_vals --auto-approve
cmpshow users expected.sql

-- atlas.hcl --
env "local" {
  url = "URL"
  src = "./1.hcl"
}
env "local_with_vals" {
  url = "URL"
  src = "./1.hcl"
  def_val = "hello"
}
-- 1.hcl --
variable "def_val" {
  type = string
}
table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = int
  }
  column "status" {
    null = true
    type = text
    default = var.def_val
  }
}
schema "main" {
}
-- expected.sql --
CREATE TABLE `+"`"+`users`+"`"+` (`+"`"+`id`+"`"+` int NOT NULL, `+"`"+`status`+"`"+` text NULL DEFAULT 'hello')
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-apply-vars.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteMigrateLintDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate lint --dir file://migrations --dev-url URL --latest=2
stdout 'Analyzing changes until version 2 \(2 migrations in total\):'
stdout ''
stdout '  -- analyzing version 1'
stdout '    -- no diagnostics found'
stdout '  -- analyzing version 2'
stdout '    -- data dependent changes detected:'
stdout '      -- L1: Adding a non-nullable "int" column "c2" will fail in case table "users" is not empty'
stdout '         https://atlasgo.io/lint/analyzers#MF103'
stdout '  -- 1 version ok, 1 with warnings'
stdout '  -- 4 schema changes'
stdout '  -- 1 diagnostic'

-- migrations/1.sql --
CREATE TABLE users (id int);

/* Same-file additions are not data dependent. */
ALTER TABLE users ADD COLUMN c1 int NOT NULL;

-- migrations/2.sql --
ALTER TABLE users ADD COLUMN c2 int NOT NULL;
ALTER TABLE users ADD COLUMN c3 int NOT NULL DEFAULT 1;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-lint-add-notnull.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeExecutesSQLiteMigrateLintNolintAndProjectLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate lint --dir file://migrations --dev-url URL --env=log_name > got.txt
cmp got.txt expected.txt

atlas migrate lint --dir file://migrations2 --dev-url URL --latest=1
stdout '  -- 1 version ok'

-- atlas.hcl --
lint {
  latest = 1
}

env "log_name" {
  lint {
    log = "{{ range .Files }}{{ println .Name }}{{ end }}"
  }
}
-- migrations/1.sql --
CREATE TABLE users (id int);
-- migrations/2.sql --
DROP TABLE users;
-- expected.txt --
2.sql
-- migrations2/1.sql --
CREATE TABLE users (id int);
CREATE TABLE pets (id int);
-- migrations2/2.sql --
-- atlas:nolint destructive data_depend

DROP TABLE pets;
ALTER TABLE users ADD COLUMN name text NOT NULL;
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "sqlite/cli-migrate-lint-project.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeReportsUnexpectedSchemaInspectFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --format '{{ sql . }}'

-- a.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "--dev-url cannot be empty") {
		t.Fatalf("detail missing command failure: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeReportsUnexpectedSuccessForExpectedFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql

-- a.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "expected command failure, but command succeeded") {
		t.Fatalf("detail missing unexpected success: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeAcceptsVersionedMysqlOnlyDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql8
atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql

-- a.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if strings.Contains(results[0].Detail, "only mysql8") {
		t.Fatalf("versioned mysql only directive should not be reported unsupported: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeAcceptsMatchingFamilyOnlyDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only mysql
atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql

-- a.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if strings.Contains(results[0].Detail, "only mysql") {
		t.Fatalf("matching only directive should not be reported unsupported: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeAcceptsMariaOnlyDirectiveForMariaNamedFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `only maria107 maria102
atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql

-- a.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "mysql/check.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
	if strings.Contains(results[0].Detail, "only maria107") {
		t.Fatalf("maria version only directive should not be reported unsupported: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeUsesRegexpAssertions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}'
stdout 'CREATE TABLE "users" \([\s\S]*PRIMARY KEY'
! stdout 'CREATE TABLE accounts'

-- a.sql --
CREATE TABLE users (
  id INT NOT NULL,
  PRIMARY KEY (id)
);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeOverwritesPseudoStreamsWithEmptyOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `! atlas schema inspect -u file://a.sql --format '{{ sql . }}'
atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp stderr empty.txt

-- a.sql --
CREATE TABLE users (id INT NOT NULL);

-- empty.txt --
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected OK result, got %#v", results[0])
	}
}

func TestTxtarScriptProbeKeepsUnsupportedHCLInspectAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- a.sql --
CREATE TABLE users (id INT CHECK (id > 0));

-- expected.hcl --
table "users" {}
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: atlas schema inspect hcl") {
		t.Fatalf("detail missing unsupported HCL inspect: %s", results[0].Detail)
	}
	if strings.Contains(results[0].Detail, "cmp inspected.hcl") {
		t.Fatalf("unsupported producer should not turn dependent cmp into mismatch: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeSkipsAssertionsAfterUnsupportedStdoutProducer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas migrate diff --dev-url URL --to file://schema.sql
! stdout 'no changes'
cmp stdout expected.sql

-- schema.sql --
CREATE TABLE users (id INT NOT NULL);

-- expected.sql --
-- Create "users" table
CREATE TABLE "users" ("id" integer NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Gap {
		t.Fatalf("expected Gap result, got %#v", results[0])
	}
	if strings.Contains(results[0].Detail, "stdout") {
		t.Fatalf("unsupported stdout producer should not be checked as mismatch: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeReportsSchemaInspectSQLMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `atlas schema inspect -u file://a.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp inspected.sql expected.sql

-- a.sql --
CREATE TABLE users (id INT NOT NULL);

-- expected.sql --
CREATE TABLE users (id INT NOT NULL);
`)

	results := TxtarScriptProbe{}.Run(Fixture{
		Name:  "postgres/case.txtar",
		Kind:  FixtureKindTxtar,
		Dir:   dir,
		Files: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != Fail {
		t.Fatalf("expected Fail result, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "cmp inspected.sql expected.sql did not match") {
		t.Fatalf("detail missing cmp mismatch: %s", results[0].Detail)
	}
}

func TestAtlasTxtarDownProbeCapturesSectionBoundaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20240305171147_section_boundary.sql")
	writeTestFile(t, path, `-- atlas:txtar

-- migration.sql --
-- keep this marker-like SQL comment --
CREATE TABLE txtar_boundary_widgets (id INT PRIMARY KEY, name TEXT NOT NULL);

-- schema.sql --
SELECT 'ptah_conformance_txtar_extra_section_sentinel';

-- down.sql --
SELECT 'ptah_conformance_txtar_down_sentinel';
DROP TABLE txtar_boundary_widgets;
`)

	results := AtlasTxtarDownProbe{}.Run(Fixture{
		Name:     "txtar-down-boundary",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{path},
		SQLFiles: []string{path},
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 txtar-down observations, got %d: %#v", len(results), results)
	}
	for _, result := range results {
		if result.Outcome != OK {
			t.Fatalf("expected OK result, got %#v", result)
		}
	}
	assertResult(t, results, "20240305171147/up", "migration.sql captured 1 statement(s)")
	assertResult(t, results, "20240305171147/down", "down.sql captured 2 statement(s)")
}

func TestLintProbeTreatsDownOnlyDropsAsNonDestructive(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "1_initial.up.sql"), "CREATE TABLE users (id INT);\n")
	writeTestFile(t, filepath.Join(dir, "1_initial.down.sql"), "DROP TABLE users;\n")

	results := LintProbe{}.Run(Fixture{
		Name:     "golang-migrate",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{filepath.Join(dir, "1_initial.up.sql"), filepath.Join(dir, "1_initial.down.sql")},
		SQLFiles: []string{filepath.Join(dir, "1_initial.up.sql"), filepath.Join(dir, "1_initial.down.sql")},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 lint observation, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected down-only DROP TABLE to be OK, got %#v", results[0])
	}
}

func TestLintProbeIgnoresRollbackSectionsWhenLookingForExpectedDrops(t *testing.T) {
	cases := map[string]string{
		"dbmate": `-- migrate:up
CREATE TABLE users (id INT);
-- migrate:down
DROP TABLE users;
`,
		"goose": `-- +goose Up
CREATE TABLE users (id INT);
-- +goose Down
DROP TABLE users;
`,
		"liquibase": `--liquibase formatted sql
--changeset atlas:1-1
CREATE TABLE users (id INT);
--rollback DROP TABLE users;
`,
	}

	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "1_initial.sql")
			writeTestFile(t, path, sql)

			results := LintProbe{}.Run(Fixture{
				Name:     name,
				Kind:     FixtureKindSQLDir,
				Dir:      dir,
				Files:    []string{path},
				SQLFiles: []string{path},
			})

			if len(results) != 1 {
				t.Fatalf("expected 1 lint observation, got %d: %#v", len(results), results)
			}
			if results[0].Outcome != OK {
				t.Fatalf("expected rollback-only DROP TABLE to be OK, got %#v", results[0])
			}
		})
	}
}

func TestLintProbeAcceptsFlywayRepeatableAtlasName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "3R_views.sql")
	writeTestFile(t, path, "CREATE VIEW my_view AS SELECT 1;\n")

	results := LintProbe{}.Run(Fixture{
		Name:     "cmd/atlas/internal/cmdapi/testdata/import/flyway_gold",
		Kind:     FixtureKindSQLDir,
		Dir:      dir,
		Files:    []string{path},
		SQLFiles: []string{path},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 lint observation, got %d: %#v", len(results), results)
	}
	if results[0].Outcome != OK {
		t.Fatalf("expected Flyway repeatable to lint cleanly, got %#v", results[0])
	}
	if results[0].Issue != "" {
		t.Fatalf("expected no tracking issue, got %#v", results[0])
	}
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func atlasSumBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()

	fsys := fstest.MapFS{}
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	sum, err := migratesum.ComputeWithFormat(fsys, migrator.MigrationDirFormatAtlas)
	if err != nil {
		t.Fatal(err)
	}
	return sum.Bytes()
}

func assertResult(t *testing.T, results []Result, stage, detail string) {
	t.Helper()

	for _, result := range results {
		if result.Stage == stage && result.Detail == detail {
			return
		}
	}
	t.Fatalf("missing result stage=%q detail=%q in %#v", stage, detail, results)
}

func assertResultDetailContains(t *testing.T, results []Result, detail string) {
	t.Helper()

	for _, result := range results {
		if strings.Contains(result.Detail, detail) {
			return
		}
	}
	t.Fatalf("missing result detail containing %q in %#v", detail, results)
}
