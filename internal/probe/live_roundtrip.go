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
func RunRoundTrip(ctx context.Context, conn *dbschema.DatabaseConnection, name, dir string) []Result {
	dialect := conn.Info().Dialect
	desired, err := goschema.ParseDir(dir)
	if err != nil {
		return []Result{{"roundtrip-consistency", name, "parse", Fail, oneLine(err.Error()), ""}}
	}
	schemaName, err := resetDatabase(ctx, conn, dialect)
	if err != nil {
		return []Result{{"roundtrip-consistency", name, "reset", Fail, oneLine(err.Error()), ""}}
	}

	var stmts []string
	var renderErr error
	panicked, pmsg := guard(func() { stmts, renderErr = renderer.GetOrderedCreateStatements(desired, dialect) })
	if panicked {
		return []Result{{"roundtrip-consistency", name, "render", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	}
	if renderErr != nil {
		return []Result{{"roundtrip-consistency", name, "render", Gap, oneLine(renderErr.Error()), "stokaro/ptah#128"}}
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return []Result{{"roundtrip-consistency", name, "apply", Gap,
				"Ptah-rendered DDL failed to apply to a real database: " + oneLine(err.Error()), ""}}
		}
	}

	got, err := dbschema.ReadSchemaWithSchemas(conn, []string{schemaName})
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
			"desired schema does not survive apply -> introspect: " + describeDiff(diff), liveRoundTripIssue(name)}}
	}
	return []Result{{"roundtrip-consistency", name, "roundtrip", OK,
		fmt.Sprintf("clean round-trip: %d table(s), %d view(s), %d enum(s)",
			len(desired.Tables), len(desired.Views), len(desired.Enums)), ""}}
}

func liveRoundTripIssue(name string) string {
	switch {
	case strings.Contains(name, "07-generated-column"):
		return "stokaro/ptah#610"
	case strings.Contains(name, "06-constraints-actions"):
		return "stokaro/ptah#611"
	case strings.Contains(name, "09-defaults-types"):
		return "stokaro/ptah#612"
	default:
		return "stokaro/ptah#285"
	}
}

// resetDatabase drops every object from the target so fixtures do not
// contaminate each other, and returns the schema/database name to introspect.
// The reset is dialect-aware: PostgreSQL recreates the public schema; MySQL and
// MariaDB drop every table and view in the current database.
func resetDatabase(ctx context.Context, conn *dbschema.DatabaseConnection, dialect string) (string, error) {
	switch dialect {
	case "postgres":
		for _, s := range []string{"DROP SCHEMA IF EXISTS public CASCADE", "CREATE SCHEMA public"} {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				return "", err
			}
		}
		return "public", nil
	case "mysql", "mariadb":
		var db string
		if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&db); err != nil {
			return "", err
		}
		rows, err := conn.Query("SELECT table_name, table_type FROM information_schema.tables WHERE table_schema = DATABASE()")
		if err != nil {
			return "", err
		}
		type obj struct{ name, typ string }
		var objs []obj
		for rows.Next() {
			var o obj
			if err := rows.Scan(&o.name, &o.typ); err != nil {
				rows.Close()
				return "", err
			}
			objs = append(objs, o)
		}
		rows.Close()
		if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
			return "", err
		}
		defer conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1") //nolint:errcheck
		for _, o := range objs {
			drop := "DROP TABLE IF EXISTS `" + o.name + "`"
			if strings.Contains(strings.ToUpper(o.typ), "VIEW") {
				drop = "DROP VIEW IF EXISTS `" + o.name + "`"
			}
			if _, err := conn.ExecContext(ctx, drop); err != nil {
				return "", err
			}
		}
		return db, nil
	default:
		return "", fmt.Errorf("round-trip reset does not support dialect %q", dialect)
	}
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
