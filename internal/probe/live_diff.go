package probe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"go.5x5.cz/ptah/atlascompat"
	"go.5x5.cz/ptah/core/goschema"
	"go.5x5.cz/ptah/core/renderer"
	"go.5x5.cz/ptah/dbschema"
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
// drift that would make ptah-compat's `atlas schema inspect` a non-faithful
// drop-in. It is
// scoped to CE-visible object kinds (tables/columns/constraints); Ptah objects
// Atlas CE omits (views, triggers, functions — Pro-gated) are not penalized here,
// they are covered by the Ptah-vs-Ptah round-trip tier instead.
//
// Unlike the round-trip tier this needs a real Atlas binary; the caller passes
// its path (built from the pinned atlas.version tag) and an Atlas-compatible URL
// for the same live database. When no binary is available the caller skips this
// tier entirely.
func RunSchemaDiff(ctx context.Context, conn *dbschema.DatabaseConnection, atlasBin, dbURL, name, dir string) []Result {
	dialect := conn.Info().Dialect

	desired, err := goschema.ParseDir(dir)
	if err != nil {
		return []Result{{"atlas-differential", name, "parse", Fail, oneLine(err.Error()), ""}}
	}
	schemas, err := resetDatabase(ctx, conn, dialect, desired)
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
	defaultAttrs := schemaDefaults(atlasDB, schemas[0])
	atlasFacts := factsFromDatabase(foldDefaultSchema(atlasDB, schemas[0], defaultAttrs))

	// Ptah's view of the same live schema (the introspect -> convert chain
	// `ptah read-db` uses).
	got, err := dbschema.ReadSchemaWithSchemas(conn, schemas)
	if err != nil {
		return []Result{{"atlas-differential", name, "introspect", Fail, oneLine(err.Error()), ""}}
	}
	var ptahFacts tableFacts
	if panicked, pmsg := guard(func() {
		ptahFacts = factsFromDatabase(foldDefaultSchema(atlascompat.DBSchemaToGoSchema(got), schemas[0], defaultAttrs))
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
	n := 0
	for key := range f {
		if key != globalFactsKey {
			n++
		}
	}
	if n == 1 {
		return "1 table"
	}
	return strconv.Itoa(n) + " tables"
}

type defaultSchemaAttrs struct {
	charset string
	collate string
}

func schemaDefaults(db *goschema.Database, defaultSchema string) defaultSchemaAttrs {
	defaultSchema = normSchema(defaultSchema)
	if db == nil || defaultSchema == "" {
		return defaultSchemaAttrs{}
	}
	for _, schema := range db.Schemas {
		if normSchema(schema.Name) == defaultSchema {
			return defaultSchemaAttrs{charset: normIdent(schema.Charset), collate: normIdent(schema.Collate)}
		}
	}
	return defaultSchemaAttrs{}
}

func foldDefaultSchema(db *goschema.Database, defaultSchema string, attrs defaultSchemaAttrs) *goschema.Database {
	defaultSchema = normSchema(defaultSchema)
	if db == nil || defaultSchema == "" {
		return db
	}
	filteredSchemas := db.Schemas[:0]
	for _, schema := range db.Schemas {
		if normSchema(schema.Name) != defaultSchema {
			filteredSchemas = append(filteredSchemas, schema)
		}
	}
	db.Schemas = filteredSchemas
	for i := range db.Tables {
		if normSchema(db.Tables[i].Schema) == defaultSchema {
			db.Tables[i].Schema = ""
		}
		if normIdent(db.Tables[i].Charset) == attrs.charset {
			db.Tables[i].Charset = ""
		}
		if normIdent(db.Tables[i].Collate) == attrs.collate {
			db.Tables[i].Collate = ""
		}
	}
	for i := range db.Fields {
		db.Fields[i].Foreign = foldDefaultSchemaRef(db.Fields[i].Foreign, defaultSchema)
		if normIdent(db.Fields[i].Charset) == attrs.charset {
			db.Fields[i].Charset = ""
		}
		if normIdent(db.Fields[i].Collate) == attrs.collate {
			db.Fields[i].Collate = ""
		}
	}
	for i := range db.Indexes {
		db.Indexes[i].TableName = foldDefaultSchemaRef(db.Indexes[i].TableName, defaultSchema)
	}
	for i := range db.Constraints {
		db.Constraints[i].Table = foldDefaultSchemaRef(db.Constraints[i].Table, defaultSchema)
		db.Constraints[i].ForeignTable = foldDefaultSchemaRef(db.Constraints[i].ForeignTable, defaultSchema)
	}
	return db
}

func foldDefaultSchemaRef(ref, defaultSchema string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	table, cols, hasCols := strings.Cut(ref, "(")
	table = strings.TrimSpace(table)
	parts := strings.Split(strings.ReplaceAll(strings.ReplaceAll(table, "`", ""), `"`, ""), ".")
	if len(parts) < 2 || normSchema(parts[len(parts)-2]) != defaultSchema {
		return ref
	}
	folded := parts[len(parts)-1]
	if hasCols {
		return folded + "(" + cols
	}
	return folded
}
