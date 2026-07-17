package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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
! atlas migrate diff --to file://schema.hcl
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
		Name:  "sqlite/case.txtar",
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
	writeTestFile(t, path, `atlas migrate apply --url URL --log '{{ json . }}'
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
	writeTestFile(t, path, `apply 1.hcl
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
	if !strings.Contains(results[0].Detail, "unsupported: apply") {
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

func TestTxtarScriptProbeSkipsDBURLInspectAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
atlas schema inspect --url URL > inspected.hcl
cmp inspected.hcl expected.hcl

-- 1.hcl --
schema "main" {}
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
	assertResultDetailContains(t, results, "unsupported: apply")
	if strings.Contains(results[0].Detail, "atlas schema inspect") {
		t.Fatalf("dependent DB URL inspect should not be reported after unsupported DB mutation: %s", results[0].Detail)
	}
}

func TestTxtarScriptProbeKeepsFileURLInspectAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
atlas schema inspect --url file://schema.sql --dev-url URL --format '{{ sql . }}' > inspected.sql
cmp inspected.sql expected.sql

-- 1.hcl --
schema "main" {}
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
		t.Fatalf("expected original apply gap, got %#v", results[0])
	}
	if !strings.Contains(results[0].Detail, "unsupported: apply") {
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
	writeTestFile(t, path, `atlas migrate apply --url URL --dry-run
synced 1.hcl

-- 1.hcl --
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
	assertResultDetailContains(t, results, "unsupported: atlas migrate apply")
	assertResultDetailContains(t, results, "unsupported: synced")
}

func TestTxtarScriptProbeSkipsMigrationCommandsAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
atlas migrate status --url URL
atlas migrate apply --url URL
atlas migrate set 1 --url URL

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
	assertResultDetailContains(t, results, "unsupported: apply")
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
	writeTestFile(t, path, `apply 1.hcl
atlas schema apply --url URL --to file://2.hcl
atlas schema clean --url URL

-- 1.hcl --
schema "main" {}
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
	assertResultDetailContains(t, results, "unsupported: apply")
	for _, dependent := range []string{"atlas schema apply", "atlas schema clean"} {
		if strings.Contains(results[0].Detail, dependent) {
			t.Fatalf("dependent command %q should not be reported after unsupported DB mutation: %s",
				dependent, results[0].Detail)
		}
	}
}

func TestTxtarScriptProbeSkipsRawDBCommandsAfterUnsupportedDBMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.txtar")
	writeTestFile(t, path, `apply 1.hcl
apply 2.hcl
execsql 'INSERT INTO users (id) VALUES (1)'
! exist users

-- 1.hcl --
schema "main" {}
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
	assertResultDetailContains(t, results, "unsupported: apply")
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
