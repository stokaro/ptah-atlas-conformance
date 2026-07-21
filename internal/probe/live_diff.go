package probe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/stokaro/ptah/atlascompat"
	"github.com/stokaro/ptah/core/goschema"
	"github.com/stokaro/ptah/core/renderer"
	"github.com/stokaro/ptah/dbschema"
)

// RunSchemaDiff is the differential-vs-Atlas tier: it measures whether Ptah and
// a real Atlas CE binary *agree* about a live schema. It applies a first-party
// Ptah schema to a database, then reads what both tools understand about it as a
// typed schema and compares them by column facts (type, nullability, default,
// primary key, foreign key) rather than DDL text:
//
//   - Atlas's view comes from `atlas schema inspect` in its native HCL, parsed by
//     Ptah's own core/atlashcl into a goschema.Database. This exercises a real
//     drop-in path — can Ptah ingest Atlas's HCL — and, because both sides end up
//     as the same typed structure, the comparison needs no fragile SQL parsing.
//   - Ptah's view comes from its introspect -> convert chain (the read-db path).
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
	var renderErr error
	if panicked, pmsg := guard(func() { stmts, renderErr = renderer.GetOrderedCreateStatements(desired, dialect) }); panicked {
		return []Result{{"atlas-differential", name, "render", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}
	if renderErr != nil {
		return []Result{{"atlas-differential", name, "render", Gap, oneLine(renderErr.Error()), "stokaro/ptah#128"}}
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return []Result{{"atlas-differential", name, "apply", Gap,
				"Ptah-rendered DDL failed to apply: " + oneLine(err.Error()), ""}}
		}
	}

	// Atlas CE's view of the live schema, in its native HCL, parsed by Ptah's own
	// Atlas-HCL parser. A parse failure here is itself a drop-in gap (Ptah cannot
	// ingest Atlas's inspect output), distinct from a schema disagreement.
	atlasHCL, err := atlasInspectHCL(ctx, atlasBin, dbURL)
	if err != nil {
		return []Result{{"atlas-differential", name, "atlas-inspect", Fail, oneLine(err.Error()), ""}}
	}
	var atlasDB *goschema.Database
	if panicked, pmsg := guard(func() { atlasDB, err = atlascompat.ParseAtlasHCL(atlasHCL, "atlas.hcl") }); panicked {
		return []Result{{"atlas-differential", name, "atlas-hcl", Panic, oneLine(pmsg), "stokaro/ptah#276"}}
	}
	if err != nil {
		return []Result{{"atlas-differential", name, "atlas-hcl", Gap,
			"Ptah's core/atlashcl cannot ingest Atlas's inspect output: " + oneLine(err.Error()), "stokaro/ptah#276"}}
	}
	atlasFacts := factsFromDatabase(atlasDB)

	// Ptah's view of the same live schema (the introspect -> convert chain
	// `ptah read-db` uses).
	got, err := dbschema.ReadSchemaWithSchemas(conn, []string{schemaName})
	if err != nil {
		return []Result{{"atlas-differential", name, "introspect", Fail, oneLine(err.Error()), ""}}
	}
	var ptahFacts tableFacts
	if panicked, pmsg := guard(func() {
		ptahFacts = factsFromDatabase(atlascompat.DBSchemaToGoSchema(got))
	}); panicked {
		return []Result{{"atlas-differential", name, "ptah-convert", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}

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

// atlasInspectHCL runs `atlas schema inspect` and returns its native HCL. Only
// stdout is captured: a freshly built Atlas prints a "new version available"
// notice to stderr on its first run, and folding that into the HCL would make it
// unparseable. The update notifier is disabled as well, so a warmed or cold
// Atlas behaves identically. CE silently omits Pro objects
// (views/triggers/functions), which is exactly why the differential compares
// only CE-visible object kinds.
func atlasInspectHCL(ctx context.Context, atlasBin, dbURL string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, atlasBin, "schema", "inspect", "--url", dbURL)
	cmd.Env = append(os.Environ(), "ATLAS_NO_UPDATE_NOTIFIER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, oneLine(stderr.String()))
	}
	return out, nil
}

func countTables(f tableFacts) string {
	n := len(f)
	if n == 1 {
		return "1 table"
	}
	return strconv.Itoa(n) + " tables"
}
