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
	schemas, err := resetDatabase(ctx, conn, dialect, desired)
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

	got, err := dbschema.ReadSchemaWithSchemas(conn, schemas)
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
		"clean round-trip: " + roundTripObjectSummary(desired), ""}}
}

func roundTripObjectSummary(db *goschema.Database) string {
	// Keep this list aligned with the object families compared by
	// schemadiff.CompareWithDialect. A count in a successful report row is
	// evidence only when the clean diff proves that object family survived.
	objects := []struct {
		name  string
		count int
	}{
		{"tables", len(db.Tables)},
		{"fields", len(db.Fields)},
		{"indexes", len(db.Indexes)},
		{"constraints", len(db.Constraints)},
		{"enums", len(db.Enums)},
		{"extensions", len(db.Extensions)},
		{"functions", len(db.Functions)},
		{"sequences", len(db.Sequences)},
		{"domains", len(db.Domains)},
		{"composite_types", len(db.CompositeTypes)},
		{"ranges", len(db.Ranges)},
		{"views", len(db.Views)},
		{"materialized_views", len(db.MaterializedViews)},
		{"triggers", len(db.Triggers)},
		{"rls_policies", len(db.RLSPolicies)},
		{"rls_enabled_tables", len(db.RLSEnabledTables)},
		{"roles", len(db.Roles)},
		{"grants", len(db.Grants)},
	}
	parts := make([]string, 0, len(objects))
	for _, object := range objects {
		if object.count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", object.name, object.count))
		}
	}
	if len(parts) == 0 {
		return "no objects"
	}
	return strings.Join(parts, ", ")
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
// MariaDB drop every table and view in the current database; SQLite drops user
// tables and views from the main database.
func resetDatabase(ctx context.Context, conn *dbschema.DatabaseConnection, dialect string, desired *goschema.Database) ([]string, error) {
	switch dialect {
	case "postgres":
		existingSchemas, err := postgresNonSystemSchemas(ctx, conn)
		if err != nil {
			return nil, err
		}
		for _, schema := range existingSchemas {
			if _, err := conn.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+quotePostgresIdent(schema)+" CASCADE"); err != nil {
				return nil, err
			}
		}
		for _, s := range []string{"DROP SCHEMA IF EXISTS public CASCADE", "CREATE SCHEMA public"} {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				return nil, err
			}
		}
		return postgresSchemasToRead(desired), nil
	case "mysql", "mariadb":
		var db string
		if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&db); err != nil {
			return nil, err
		}
		rows, err := conn.Query("SELECT table_name, table_type FROM information_schema.tables WHERE table_schema = DATABASE()")
		if err != nil {
			return nil, err
		}
		type obj struct{ name, typ string }
		var objs []obj
		for rows.Next() {
			var o obj
			if err := rows.Scan(&o.name, &o.typ); err != nil {
				rows.Close()
				return nil, err
			}
			objs = append(objs, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
			return nil, err
		}
		defer conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1") //nolint:errcheck
		for _, o := range objs {
			drop := "DROP TABLE IF EXISTS `" + o.name + "`"
			if strings.Contains(strings.ToUpper(o.typ), "VIEW") {
				drop = "DROP VIEW IF EXISTS `" + o.name + "`"
			}
			if _, err := conn.ExecContext(ctx, drop); err != nil {
				return nil, err
			}
		}
		return []string{db}, nil
	case "sqlite":
		rows, err := conn.QueryContext(ctx, "SELECT name, type FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'")
		if err != nil {
			return nil, err
		}
		type obj struct{ name, typ string }
		var objs []obj
		for rows.Next() {
			var o obj
			if err := rows.Scan(&o.name, &o.typ); err != nil {
				rows.Close()
				return nil, err
			}
			objs = append(objs, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return nil, err
		}
		defer conn.ExecContext(ctx, "PRAGMA foreign_keys = ON") //nolint:errcheck
		for _, typ := range []string{"view", "table"} {
			for _, o := range objs {
				if o.typ != typ {
					continue
				}
				if _, err := conn.ExecContext(ctx, "DROP "+strings.ToUpper(o.typ)+" IF EXISTS "+quoteSQLiteIdent(o.name)); err != nil {
					return nil, err
				}
			}
		}
		return []string{"main"}, nil
	default:
		return nil, fmt.Errorf("round-trip reset does not support dialect %q", dialect)
	}
}

func postgresNonSystemSchemas(ctx context.Context, conn *dbschema.DatabaseConnection) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
SELECT schema_name
FROM information_schema.schemata
WHERE schema_name <> 'public'
  AND schema_name <> 'information_schema'
  AND schema_name NOT LIKE 'pg_%'
ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schemas []string
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			return nil, err
		}
		schemas = append(schemas, schema)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schemas, nil
}

func postgresSchemasToRead(desired *goschema.Database) []string {
	schemas := []string{"public"}
	schemas = append(schemas, nonDefaultPostgresSchemas(desired)...)
	return schemas
}

func nonDefaultPostgresSchemas(desired *goschema.Database) []string {
	set := map[string]struct{}{}
	if desired != nil {
		for _, schema := range desired.Schemas {
			addPostgresSchema(set, schema.Name)
		}
		for _, table := range desired.Tables {
			addPostgresSchema(set, table.Schema)
		}
	}
	out := make([]string, 0, len(set))
	for schema := range set {
		out = append(out, schema)
	}
	sort.Strings(out)
	return out
}

func addPostgresSchema(set map[string]struct{}, name string) {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "", "public":
		return
	default:
		set[name] = struct{}{}
	}
}

func quotePostgresIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
