package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stokaro/ptah/core/goschema"
	"github.com/stokaro/ptah/core/renderer"
	"github.com/stokaro/ptah/dbschema"
	"github.com/stokaro/ptah/migration/schemadiff"
	difftypes "github.com/stokaro/ptah/migration/schemadiff/types"
)

// RunRoundTrip is the behavioral self-consistency check that a drop-in needs: it
// applies a first-party Ptah schema to a live database, introspects it back, and
// diffs the introspected schema against the desired one. A clean diff means
// Ptah's generate -> apply -> introspect loop is lossless for that schema; a
// non-empty diff is a gap — Ptah loses or mis-round-trips a construct, so a
// migration authored against that schema would drift. It is behavioral and
// auto-flips as Ptah's renderer/reader improve. Unlike an introspection-vs-Atlas
// probe, this is Ptah-vs-Ptah and so carries no Pro/OSS ambiguity about which
// objects Atlas itself chooses to inspect.
//
// The fixture is isolated by resetting the public schema first, so fixtures do
// not contaminate each other.
func RunRoundTrip(ctx context.Context, conn *dbschema.DatabaseConnection, name, dir, dialect string) []Result {
	desired, err := goschema.ParseDir(dir)
	if err != nil {
		return []Result{{"roundtrip-consistency", name, "parse", Fail, oneLine(err.Error()), ""}}
	}
	for _, reset := range []string{"DROP SCHEMA IF EXISTS public CASCADE", "CREATE SCHEMA public"} {
		if _, err := conn.ExecContext(ctx, reset); err != nil {
			return []Result{{"roundtrip-consistency", name, "reset", Fail, oneLine(err.Error()), ""}}
		}
	}

	var stmts []string
	panicked, pmsg := guard(func() { stmts = renderer.GetOrderedCreateStatements(desired, dialect) })
	if panicked {
		return []Result{{"roundtrip-consistency", name, "render", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return []Result{{"roundtrip-consistency", name, "apply", Gap,
				"Ptah-rendered DDL failed to apply to a real database: " + oneLine(err.Error()), ""}}
		}
	}

	got, err := dbschema.ReadSchemaWithSchemas(conn, []string{"public"})
	if err != nil {
		return []Result{{"roundtrip-consistency", name, "introspect", Fail, oneLine(err.Error()), ""}}
	}

	var diff *difftypes.SchemaDiff
	panicked, pmsg = guard(func() { diff = schemadiff.CompareWithDialect(desired, got, dialect) })
	if panicked {
		return []Result{{"roundtrip-consistency", name, "diff", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}
	if diff != nil && diff.HasChanges() {
		return []Result{{"roundtrip-consistency", name, "roundtrip", Gap,
			"desired schema does not survive apply -> introspect: " + describeDiff(diff), "stokaro/ptah#285"}}
	}
	return []Result{{"roundtrip-consistency", name, "roundtrip", OK,
		fmt.Sprintf("clean round-trip: %d table(s), %d view(s), %d enum(s)",
			len(desired.Tables), len(desired.Views), len(desired.Enums)), ""}}
}

// describeDiff summarizes which categories of the diff are non-empty, so a gap
// says what Ptah lost (e.g. "views_added, tables_modified") without dumping the
// whole structure.
func describeDiff(diff *difftypes.SchemaDiff) string {
	raw, err := json.Marshal(diff)
	if err != nil {
		return "diff present"
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "diff present"
	}
	var keys []string
	for k, v := range m {
		s := strings.TrimSpace(string(v))
		if s == "null" || s == "[]" || s == "{}" || s == "false" || s == "0" || s == `""` {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "diff present"
	}
	return "differs in " + strings.Join(keys, ", ")
}
