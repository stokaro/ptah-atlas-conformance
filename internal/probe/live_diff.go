package probe

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/stokaro/ptah/core/convert/dbschematogo"
	"github.com/stokaro/ptah/core/goschema"
	"github.com/stokaro/ptah/core/renderer"
	"github.com/stokaro/ptah/dbschema"
)

// RunSchemaDiff is the differential-vs-Atlas tier: it measures whether Ptah and
// a real Atlas CE binary *agree* about a live schema. It applies a first-party
// Ptah schema to a database, then asks both tools to describe what they see —
// Atlas via `schema inspect --format '{{ sql . }}'`, Ptah via its
// introspect -> render chain — and compares the two at the level of column
// facts (name, canonical type, nullability, default presence, primary key)
// rather than DDL text. Folding the systematic, semantically-equivalent spelling
// differences (serial vs integer+nextval, `character varying` vs `varchar`,
// inline vs table-level PRIMARY KEY) leaves only genuine disagreements visible.
//
// A row is OK when Ptah reports the same column facts Atlas does. A Gap means the
// two tools disagree about a construct Atlas CE can see — exactly the kind of
// drift that would make `ptah atlas schema inspect` a non-faithful drop-in. It is
// scoped to CE-visible object kinds (tables/columns/constraints); Ptah objects
// Atlas CE omits (views, triggers, functions — Pro-gated) are not penalized here,
// they are covered by the Ptah-vs-Ptah round-trip tier instead.
//
// Unlike the round-trip tier this needs a real Atlas binary; the caller passes
// its path (built from the pinned atlas.version tag). When no binary is
// available the caller skips this tier entirely.
func RunSchemaDiff(ctx context.Context, conn *dbschema.DatabaseConnection, atlasBin, dbURL, name, dir string) []Result {
	dialect := conn.Info().Dialect
	if dialect != "postgres" {
		return []Result{{"atlas-differential", name, "scope", OK,
			"differential is scoped to postgres (Atlas CE inspect parity); skipped for " + dialect, ""}}
	}

	desired, err := goschema.ParseDir(dir)
	if err != nil {
		return []Result{{"atlas-differential", name, "parse", Fail, oneLine(err.Error()), ""}}
	}
	schemaName, err := resetDatabase(ctx, conn, dialect)
	if err != nil {
		return []Result{{"atlas-differential", name, "reset", Fail, oneLine(err.Error()), ""}}
	}

	var stmts []string
	if panicked, pmsg := guard(func() { stmts = renderer.GetOrderedCreateStatements(desired, dialect) }); panicked {
		return []Result{{"atlas-differential", name, "render", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return []Result{{"atlas-differential", name, "apply", Gap,
				"Ptah-rendered DDL failed to apply: " + oneLine(err.Error()), ""}}
		}
	}

	// Atlas CE's view of the live schema.
	atlasSQL, err := atlasInspect(ctx, atlasBin, dbURL)
	if err != nil {
		return []Result{{"atlas-differential", name, "atlas-inspect", Fail, oneLine(err.Error()), ""}}
	}
	atlasFacts := extractTableFacts(atlasSQL)

	// Ptah's view of the same live schema (the introspect -> render chain that
	// `ptah read-db` uses).
	got, err := dbschema.ReadSchemaWithSchemas(conn, []string{schemaName})
	if err != nil {
		return []Result{{"atlas-differential", name, "introspect", Fail, oneLine(err.Error()), ""}}
	}
	var ptahSQL string
	if panicked, pmsg := guard(func() {
		gs := dbschematogo.ConvertDBSchemaToGoSchema(got)
		ptahSQL = strings.Join(
			renderer.GetOrderedCreateStatementsWithCapabilities(gs, dialect, conn.Info().Capabilities), "\n")
	}); panicked {
		return []Result{{"atlas-differential", name, "ptah-render", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}
	ptahFacts := extractTableFacts(ptahSQL)

	if len(atlasFacts) == 0 {
		return []Result{{"atlas-differential", name, "compare", Fail,
			"Atlas CE reported no comparable tables for this fixture", ""}}
	}

	diffs := diffTableFacts(atlasFacts, ptahFacts)
	if len(diffs) > 0 {
		return []Result{{"atlas-differential", name, "compare", Gap,
			"Ptah disagrees with Atlas CE on: " + oneLine(strings.Join(diffs, "; ")), "stokaro/ptah#285"}}
	}
	return []Result{{"atlas-differential", name, "compare", OK,
		countTables(atlasFacts) + " matches Atlas CE", ""}}
}

// atlasInspect runs `atlas schema inspect` and returns its canonical SQL. CE
// silently omits Pro objects (views/triggers/functions), which is exactly why
// the differential compares only CE-visible object kinds.
func atlasInspect(ctx context.Context, atlasBin, dbURL string) (string, error) {
	cmd := exec.CommandContext(ctx, atlasBin, "schema", "inspect", "--url", dbURL, "--format", "{{ sql . }}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, oneLine(string(out)))
	}
	return string(out), nil
}

func countTables(f tableFacts) string {
	n := len(f)
	if n == 1 {
		return "1 table"
	}
	return strconv.Itoa(n) + " tables"
}
