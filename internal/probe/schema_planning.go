package probe

import (
	"context"
	"os"
	"sort"
	"strings"
)

// planningCase is a paired desired-schema fixture. Applying the plan that takes a
// database from schema A to schema B must reach the same end state as building
// schema B directly — a "plan produces the intended end state" check that a
// correct introspection alone does not guarantee. Each case exercises a distinct
// planning operation (add/drop table, add/drop/modify column, and a mix).
type planningCase struct {
	// Name labels the fixture (the operation under test).
	Name string
	// A is the starting desired schema (applied to an empty database first).
	A string
	// B is the target desired schema (the plan must transform A into it).
	B string
}

// planningCatalog is the paired-schema planning matrix. Schemas are PostgreSQL
// DDL applied through `ptah atlas schema apply --to file://...`.
var planningCatalog = []planningCase{
	{
		Name: "add table",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);` + "\n" + `CREATE TABLE orders (id SERIAL PRIMARY KEY, total INTEGER);`,
	},
	{
		Name: "drop table",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);` + "\n" + `CREATE TABLE legacy (id SERIAL PRIMARY KEY);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);`,
	},
	{
		Name: "add column",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT, email TEXT);`,
	},
	{
		Name: "drop column",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT, email TEXT);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);`,
	},
	{
		Name: "modify column nullability",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);`,
	},
	{
		Name: "modify column type category",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code INTEGER);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code TEXT);`,
	},
	{
		Name: "modify column type width",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code INTEGER);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code BIGINT);`,
	},
	{
		Name: "modify column varchar length",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code VARCHAR(50));`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code VARCHAR(100));`,
	},
	{
		Name: "mixed add/drop/modify",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT);` + "\n" + `CREATE TABLE legacy (id SERIAL PRIMARY KEY);`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL, email TEXT);` + "\n" + `CREATE TABLE orders (id SERIAL PRIMARY KEY, total INTEGER);`,
	},
}

// Each planningCatalog case runs as its own migrate-runtime check (via
// runPostgres) so the paired-schema planning matrix emits one report row per
// operation: applying A then B to a reset schema (exercising the A->B plan) and B
// alone to a second reset schema must introspect to the same canonical schema,
// proving the generated plan reaches the intended end state.
const planningProbeName = "schema-planning"

func (c planningCase) runPostgres(bin, dbURL string) Result {
	fixture := "postgres/" + strings.ReplaceAll(c.Name, " ", "-")

	// Path 1: empty -> A -> B (exercise the plan).
	if detail := resetPostgresPublic(dbURL); detail != "" {
		return Result{planningProbeName, fixture, "reset", Fail, detail, ""}
	}
	if detail := applySchema(bin, dbURL, c.A); detail != "" {
		return Result{planningProbeName, fixture, "apply-a", Fail, "applying schema A failed: " + detail, "stokaro/ptah#652"}
	}
	if detail := applySchema(bin, dbURL, c.B); detail != "" {
		return Result{planningProbeName, fixture, "apply-b-onto-a", Gap, "applying the A->B plan failed: " + detail, "stokaro/ptah#652"}
	}
	planned, detail := inspectSchema(bin, dbURL)
	if detail != "" {
		return Result{planningProbeName, fixture, "inspect", Fail, detail, ""}
	}

	// Path 2: empty -> B (the intended end state).
	if detail := resetPostgresPublic(dbURL); detail != "" {
		return Result{planningProbeName, fixture, "reset", Fail, detail, ""}
	}
	if detail := applySchema(bin, dbURL, c.B); detail != "" {
		return Result{planningProbeName, fixture, "apply-b", Fail, "applying schema B failed: " + detail, "stokaro/ptah#652"}
	}
	intended, detail := inspectSchema(bin, dbURL)
	if detail != "" {
		return Result{planningProbeName, fixture, "inspect", Fail, detail, ""}
	}

	if planned != intended {
		return Result{planningProbeName, fixture, "end-state", Gap,
			"the A->B plan reached a different end state than building B directly: " + firstDiff(planned, intended), "stokaro/ptah#652"}
	}
	return Result{planningProbeName, fixture, "end-state", OK,
		"the A->B plan reaches the same canonical schema as building B directly", ""}
}

// applySchema applies a desired-state SQL schema to dbURL via
// `ptah atlas schema apply --to file://... --auto-approve`, returning "" on
// success or a one-line error detail.
func applySchema(bin, dbURL, schema string) string {
	path, cleanup, err := writeSchemaFile(schema)
	if err != nil {
		return oneLine(err.Error())
	}
	defer cleanup()
	output, err := commandOutput(bin, []string{
		"atlas", "schema", "apply", "--url", dbURL, "--to", "file://" + path, "--auto-approve",
	})
	if err != nil {
		return oneLine(output)
	}
	return ""
}

// writeSchemaFile writes a desired-state schema to a temporary .sql file.
func writeSchemaFile(schema string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "planning-*.sql")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.WriteString(schema); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

// inspectSchema returns the canonical schema of dbURL via
// `ptah atlas schema inspect --format sql`.
func inspectSchema(bin, dbURL string) (canonical, detail string) {
	output, err := commandOutput(bin, []string{"atlas", "schema", "inspect", "--url", dbURL, "--format", "sql"})
	if err != nil {
		return "", "inspect failed: " + oneLine(output)
	}
	return canonicalizeSchemaSQL(output), ""
}

// canonicalizeSchemaSQL normalizes introspected SQL to a comparable form. It
// splits into per-statement blocks (so a column stays bound to its table), drops
// comment and blank lines and trailing commas, sorts the lines within each block
// and then the blocks, so two databases with the same tables, columns, and
// constraints compare equal regardless of emission order, while any added,
// dropped, moved, or type-changed element differs.
func canonicalizeSchemaSQL(sql string) string {
	var blocks []string
	for _, statement := range strings.Split(sql, ";") {
		var lines []string
		for _, line := range strings.Split(statement, "\n") {
			trimmed := strings.TrimSuffix(strings.TrimSpace(line), ",")
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			lines = append(lines, trimmed)
		}
		if len(lines) == 0 {
			continue
		}
		sort.Strings(lines)
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	sort.Strings(blocks)
	return strings.Join(blocks, "\n;\n")
}

// firstDiff returns the first differing line between two canonical schemas, for a
// compact, actionable report detail.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		av, bv := "", ""
		if i < len(al) {
			av = al[i]
		}
		if i < len(bl) {
			bv = bl[i]
		}
		if av != bv {
			return "plan has " + quoteOrNone(av) + ", direct build has " + quoteOrNone(bv)
		}
	}
	return "(schemas differ)"
}

func quoteOrNone(s string) string {
	if s == "" {
		return "(nothing)"
	}
	return `"` + s + `"`
}

// resetPostgresPublic drops and recreates the public schema so each planning path
// starts from an empty database.
func resetPostgresPublic(dbURL string) string {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return oneLine(err.Error())
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public"); err != nil {
		return oneLine(err.Error())
	}
	return ""
}
