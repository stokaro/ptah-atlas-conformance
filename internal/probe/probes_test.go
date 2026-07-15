package probe

import (
	"os"
	"path/filepath"
	"testing"
)

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
		Dir:      dir,
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
