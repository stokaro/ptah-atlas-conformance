package probe

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
// DDL applied through `atlas schema apply --to file://...` (ptah-compat).
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
		Name: "modify column decimal scale",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, price NUMERIC(10,2));`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, price NUMERIC(10));`,
	},
	{
		Name: "modify column varchar unbounded",
		A:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code VARCHAR(50));`,
		B:    `CREATE TABLE users (id SERIAL PRIMARY KEY, code VARCHAR);`,
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

const planningPostgresDevDatabasePrefix = "ptah_conformance_dev"

func (c planningCase) runPostgres(bin, dbURL string) (result Result) {
	fixture := "postgres/" + strings.ReplaceAll(c.Name, " ", "-")
	devDatabase := migrateRuntimeIdentifier(planningPostgresDevDatabasePrefix)
	targetURL, devURL, err := planningPostgresURLs(dbURL, devDatabase)
	if err != nil {
		return Result{planningProbeName, fixture, "setup", Fail, err.Error(), ""}
	}

	// Path 1: empty -> A -> B (exercise the plan).
	if detail := resetPostgresPublic(dbURL); detail != "" {
		return Result{planningProbeName, fixture, "reset", Fail, detail, ""}
	}
	if detail := createPostgresDatabase(dbURL, devDatabase); detail != "" {
		return Result{planningProbeName, fixture, "reset-dev", Fail, detail, ""}
	}
	defer func() {
		detail := dropPostgresDatabase(dbURL, devDatabase)
		if detail == "" {
			return
		}
		if result.Outcome == OK {
			result = Result{planningProbeName, fixture, "cleanup-dev", Fail, detail, ""}
			return
		}
		result.Detail += "; cleanup-dev failed: " + detail
	}()
	if detail := applySchema(bin, targetURL, devURL, c.A); detail != "" {
		return Result{planningProbeName, fixture, "apply-a", Fail, "applying schema A failed: " + detail, "stokaro/ptah#652"}
	}
	if detail := applySchema(bin, targetURL, devURL, c.B); detail != "" {
		return Result{planningProbeName, fixture, "apply-b-onto-a", Gap, "applying the A->B plan failed: " + detail, "stokaro/ptah#652"}
	}
	planned, detail := inspectSchema(bin, targetURL)
	if detail != "" {
		return Result{planningProbeName, fixture, "inspect", Fail, detail, ""}
	}

	// Path 2: empty -> B (the intended end state).
	if detail := resetPostgresPublic(dbURL); detail != "" {
		return Result{planningProbeName, fixture, "reset", Fail, detail, ""}
	}
	if detail := applySchema(bin, targetURL, devURL, c.B); detail != "" {
		return Result{planningProbeName, fixture, "apply-b", Fail, "applying schema B failed: " + detail, "stokaro/ptah#652"}
	}
	intended, detail := inspectSchema(bin, targetURL)
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
// `atlas schema apply --to file://... --auto-approve` (ptah-compat), returning "" on
// success or a one-line error detail.
func applySchema(bin, dbURL, devURL, schema string) string {
	path, cleanup, err := writeSchemaFile(schema)
	if err != nil {
		return oneLine(err.Error())
	}
	defer cleanup()
	output, err := commandOutput(bin, []string{
		"schema", "apply",
		"--url", dbURL,
		"--dev-url", devURL,
		"--to", "file://" + path,
		"--auto-approve",
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
// `atlas schema inspect --format sql` (ptah-compat).
func inspectSchema(bin, dbURL string) (canonical, detail string) {
	output, err := commandOutput(bin, []string{"schema", "inspect", "--url", dbURL, "--format", "sql"})
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
	return resetPostgresSchema(dbURL, "public")
}

func resetPostgresSchema(dbURL, schema string) string {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return oneLine(err.Error())
	}
	defer func() { _ = conn.Close() }()
	quoted := quotePostgresIdentifier(schema)
	statement := "DROP SCHEMA IF EXISTS " + quoted + " CASCADE; CREATE SCHEMA " + quoted
	if _, err := conn.ExecContext(context.Background(), statement); err != nil {
		return oneLine(err.Error())
	}
	return ""
}

func planningPostgresURLs(raw, devDatabase string) (target, dev string, err error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return "", "", fmt.Errorf("parse PostgreSQL URL: %w", err)
	}
	target = postgresURLWithSearchPath(*parsed, "public")
	parsed.Path = "/" + devDatabase
	parsed.RawPath = ""
	dev = postgresURLWithSearchPath(*parsed, "public")
	return target, dev, nil
}

func postgresURLWithSearchPath(parsed url.URL, schema string) string {
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func createPostgresDatabase(dbURL, database string) string {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return oneLine(err.Error())
	}
	defer func() { _ = conn.Close() }()

	statement := "CREATE DATABASE " + quotePostgresIdentifier(database)
	if _, err := conn.ExecContext(context.Background(), statement); err != nil {
		return oneLine(err.Error())
	}
	return ""
}

func dropPostgresDatabase(dbURL, database string) string {
	if !strings.HasPrefix(database, planningPostgresDevDatabasePrefix+"_") {
		return fmt.Sprintf("refusing to drop non-conformance database %q", database)
	}
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return oneLine(err.Error())
	}
	defer func() { _ = conn.Close() }()

	statement := "DROP DATABASE " + quotePostgresIdentifier(database)
	if _, err := conn.ExecContext(context.Background(), statement); err != nil {
		return oneLine(err.Error())
	}
	return ""
}
