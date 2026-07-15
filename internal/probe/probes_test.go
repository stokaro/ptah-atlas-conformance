package probe

import (
	"os"
	"path/filepath"
	"testing"
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
	if result[0].Outcome != Gap {
		t.Fatalf("expected unmeasured txtar gap, got %#v", result[0])
	}
	if result[0].Stage != "unmeasured" {
		t.Fatalf("expected unmeasured stage, got %#v", result[0])
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

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
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
