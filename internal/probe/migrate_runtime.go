package probe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stokaro/ptah/dbschema"

	_ "modernc.org/sqlite"
)

const migrateRuntimeProbeName = "migrate-runtime"

type migrateRuntimeCheck func(string) Result

type sqliteRevisionFact struct {
	Version         string
	Description     string
	Applied         int
	Total           int
	OperatorVersion string
}

type migrateRuntimeTarget struct {
	Label string
	URL   string
}

// RunMigrateRuntime runs live migration-runtime conformance checks against
// deterministic local databases. These checks inspect database state directly,
// rather than treating successful CLI exit as sufficient evidence.
func RunMigrateRuntime() []Result {
	bin, err := ptahBinary()
	if err != nil {
		return []Result{{migrateRuntimeProbeName, "ptah atlas migrate", "build", Fail,
			"could not build the Ptah CLI to probe migrate runtime behavior: " + oneLine(err.Error()), ""}}
	}

	checks := []migrateRuntimeCheck{
		sqliteMigrateApplyRecordsState,
		sqliteMigrateSetRepairsRevisionState,
		sqliteMigrateApplyTxModeAllRollsBack,
		sqliteMigrateApplyTxModeFileKeepsPriorFiles,
		sqliteMigrateApplyTxModeNoneKeepsPartialStatement,
	}
	for _, target := range configuredMigrateRuntimeTargets(os.Getenv) {
		switch target.Label {
		case "postgres":
			checks = append(checks,
				func(bin string) Result { return postgresMigrateApplyCustomRevisionsSchema(bin, target.URL) },
				func(bin string) Result { return postgresMigrateNoTransactionConcurrentIndex(bin, target.URL) },
			)
		case "mysql":
			checks = append(checks, func(bin string) Result { return mysqlMigrateApplyRecordsState(bin, target.URL) })
		}
	}
	out := make([]Result, 0, len(checks))
	for _, check := range checks {
		out = append(out, check(bin))
	}
	return out
}

func configuredMigrateRuntimeTargets(getenv func(string) string) []migrateRuntimeTarget {
	targets := []struct{ label, env string }{
		{"postgres", "CONFORMANCE_POSTGRES_URL"},
		{"mysql", "CONFORMANCE_MYSQL_URL"},
	}
	configured := make([]migrateRuntimeTarget, 0, len(targets))
	for _, target := range targets {
		if url := getenv(target.env); url != "" {
			configured = append(configured, migrateRuntimeTarget{Label: target.label, URL: url})
		}
	}
	return configured
}

func sqliteMigrateApplyRecordsState(bin string) Result {
	const fixture = "sqlite/apply-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "apply.db")
	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "First", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "Second", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	status, err := commandOutput(bin, []string{
		"atlas", "migrate", "status",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "status", status, err)
	}
	if !strings.Contains(status, "Applied Migrations: 2") || !strings.Contains(status, "Pending Migrations: 0") {
		return migrateRuntimeGap(fixture, "status", "status did not report 2 applied and 0 pending migrations: "+oneLine(status))
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply created expected SQLite tables, Atlas revision rows, and applied status", ""}
}

func sqliteMigrateSetRepairsRevisionState(bin string) Result {
	const fixture = "sqlite/set-repair-state"
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, "set.db")
	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "set", "1",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "set", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "First", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}

	output, err = commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}
	if detail := compareSQLiteTables(db, []string{"atlas_schema_revisions", "pets"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareSQLiteRevisions(db, []sqliteRevisionFact{
		{Version: "1", Description: "First", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "Second", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"set recorded repair state and apply executed only the remaining migration", ""}
}

func sqliteMigrateApplyTxModeAllRollsBack(bin string) Result {
	return sqliteMigrateApplyTxMode(bin, "sqlite/tx-mode-all", "all", []string{"atlas_schema_revisions"})
}

func sqliteMigrateApplyTxModeFileKeepsPriorFiles(bin string) Result {
	return sqliteMigrateApplyTxMode(bin, "sqlite/tx-mode-file", "file", []string{"atlas_schema_revisions", "pets", "users"})
}

func sqliteMigrateApplyTxModeNoneKeepsPartialStatement(bin string) Result {
	return sqliteMigrateApplyTxMode(bin, "sqlite/tx-mode-none", "none", []string{"atlas_schema_revisions", "broken", "pets", "users"})
}

func sqliteMigrateApplyTxMode(bin, fixture, mode string, wantTables []string) Result {
	root, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE users (id INTEGER PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE pets (id INTEGER PRIMARY KEY);\n",
		"3_third.sql":  "CREATE TABLE broken (id INTEGER);\nTHIS IS A FAILING STATEMENT;\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}

	dbPath := filepath.Join(root, mode+".db")
	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", sqliteURL(dbPath),
		"--dir", fileURL(migrations),
		"--tx-mode", mode,
	})
	if err == nil {
		return migrateRuntimeGap(fixture, "apply", "broken migration unexpectedly succeeded")
	}
	if !strings.Contains(output, "THIS IS A FAILING STATEMENT") {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = db.Close() }()
	if detail := compareSQLiteTables(db, wantTables); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"`--tx-mode " + mode + "` leaves the expected SQLite state after a failed migration", ""}
}

func postgresMigrateApplyCustomRevisionsSchema(bin, dbURL string) Result {
	const fixture = "postgres/custom-revisions-schema"
	schema := migrateRuntimeIdentifier("ptah_rt_pg_schema")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE " + quotePostgresIdentifier(schema) + ".users (id integer PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	if result := cleanupPostgresRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupPostgresRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck

	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := comparePostgresRelations(conn, schema, []string{"atlas_schema_revisions", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := comparePostgresRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "First", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply created expected PostgreSQL schema objects and Atlas revision rows in a custom revisions schema", ""}
}

func postgresMigrateNoTransactionConcurrentIndex(bin, dbURL string) Result {
	const fixture = "postgres/no-transaction-concurrent-index"
	schema := migrateRuntimeIdentifier("ptah_rt_pg_notx")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql": "CREATE TABLE " + quotePostgresIdentifier(schema) + ".users (id integer PRIMARY KEY, email text);\n",
		"2_index.sql": "-- atlas:txmode none\n" +
			"CREATE INDEX CONCURRENTLY users_email_idx ON " + quotePostgresIdentifier(schema) + ".users (email);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	if result := cleanupPostgresRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupPostgresRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck

	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := comparePostgresIndexes(conn, schema, "users", []string{"users_email_idx", "users_pkey"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := comparePostgresRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "First", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "Index", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"`-- atlas:txmode none` applied PostgreSQL CREATE INDEX CONCURRENTLY outside the migration transaction", ""}
}

func mysqlMigrateApplyRecordsState(bin, dbURL string) Result {
	const fixture = "mysql/apply-state"
	schema := migrateRuntimeIdentifier("ptah_rt_mysql")
	_, migrations, cleanup, err := migrateRuntimeDir(map[string]string{
		"1_first.sql":  "CREATE TABLE " + quoteMySQLIdentifier(schema) + "." + quoteMySQLIdentifier("users") + " (id integer PRIMARY KEY);\n",
		"2_second.sql": "CREATE TABLE " + quoteMySQLIdentifier(schema) + "." + quoteMySQLIdentifier("pets") + " (id integer PRIMARY KEY);\n",
	})
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := migrateRuntimeHash(bin, migrations, fixture); result != nil {
		return *result
	}
	if result := cleanupMySQLRuntimeSchema(dbURL, schema, fixture); result != nil {
		return *result
	}
	defer cleanupMySQLRuntimeSchema(dbURL, schema, fixture) //nolint:errcheck

	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", schema,
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "apply", output, err)
	}

	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		return migrateRuntimeFail(fixture, "inspect", err)
	}
	defer func() { _ = conn.Close() }()
	if detail := compareMySQLTables(conn, schema, []string{"atlas_schema_revisions", "pets", "users"}); detail != "" {
		return migrateRuntimeGap(fixture, "inspect", detail)
	}
	if detail := compareMySQLRevisions(conn, schema, []sqliteRevisionFact{
		{Version: "1", Description: "First", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
		{Version: "2", Description: "Second", Applied: 1, Total: 1, OperatorVersion: "Ptah"},
	}); detail != "" {
		return migrateRuntimeGap(fixture, "revisions", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "inspect", OK,
		"apply created expected MySQL tables and Atlas revision rows", ""}
}

func migrateRuntimeDir(files map[string]string) (string, string, func(), error) {
	root, err := os.MkdirTemp("", "migrate-runtime-*")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	migrations := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		cleanup()
		return "", "", nil, err
	}
	for name, sql := range files {
		if err := os.WriteFile(filepath.Join(migrations, name), []byte(sql), 0o600); err != nil {
			cleanup()
			return "", "", nil, err
		}
	}
	return root, migrations, cleanup, nil
}

func migrateRuntimeHash(bin, migrations, fixture string) *Result {
	output, err := commandOutput(bin, []string{"atlas", "migrate", "hash", "--dir", fileURL(migrations)})
	if err != nil {
		result := migrateRuntimeExit(fixture, "hash", output, err)
		return &result
	}
	return nil
}

func openMigrateRuntimeConnection(dbURL string) (*dbschema.DatabaseConnection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return dbschema.ConnectToDatabase(ctx, dbURL)
}

func openSQLiteRuntimeDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func sqliteTableNames(db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT name
FROM sqlite_schema
WHERE type = 'table'
  AND name NOT LIKE 'sqlite_%'
ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func sqliteRevisionFacts(db *sql.DB) ([]sqliteRevisionFact, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT version, description, applied, total, operator_version
FROM atlas_schema_revisions
ORDER BY CAST(version AS INTEGER)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var revisions []sqliteRevisionFact
	for rows.Next() {
		var fact sqliteRevisionFact
		if err := rows.Scan(&fact.Version, &fact.Description, &fact.Applied, &fact.Total, &fact.OperatorVersion); err != nil {
			return nil, err
		}
		revisions = append(revisions, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func postgresRelationNames(conn *dbschema.DatabaseConnection, schema string) ([]string, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT tablename
FROM pg_tables
WHERE schemaname = $1
UNION
SELECT relname
FROM pg_class
JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
WHERE pg_namespace.nspname = $1
  AND pg_class.relkind = 'S'
ORDER BY 1`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func postgresRevisionFacts(conn *dbschema.DatabaseConnection, schema string) ([]sqliteRevisionFact, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT version, description, applied, total, operator_version
FROM `+quotePostgresIdentifier(schema)+`.atlas_schema_revisions
ORDER BY CAST(version AS integer)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRevisionFacts(rows)
}

func postgresIndexNames(conn *dbschema.DatabaseConnection, schema, table string) ([]string, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT indexname
FROM pg_indexes
WHERE schemaname = $1
  AND tablename = $2
ORDER BY indexname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func mysqlTableNames(conn *dbschema.DatabaseConnection, schema string) ([]string, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT table_name
FROM information_schema.tables
WHERE table_schema = ?
ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func mysqlRevisionFacts(conn *dbschema.DatabaseConnection, schema string) ([]sqliteRevisionFact, error) {
	rows, err := conn.QueryContext(context.Background(), `
SELECT version, description, applied, total, operator_version
FROM `+quoteMySQLIdentifier(schema)+`.atlas_schema_revisions
ORDER BY CAST(version AS SIGNED)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRevisionFacts(rows)
}

func scanRevisionFacts(rows *sql.Rows) ([]sqliteRevisionFact, error) {
	var revisions []sqliteRevisionFact
	for rows.Next() {
		var fact sqliteRevisionFact
		if err := rows.Scan(&fact.Version, &fact.Description, &fact.Applied, &fact.Total, &fact.OperatorVersion); err != nil {
			return nil, err
		}
		revisions = append(revisions, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revisions, nil
}

func compareSQLiteTables(db *sql.DB, want []string) string {
	got, err := sqliteTableNames(db)
	if err != nil {
		return "inspect SQLite tables failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("SQLite tables = %v, want %v", got, want)
}

func compareSQLiteRevisions(db *sql.DB, want []sqliteRevisionFact) string {
	got, err := sqliteRevisionFacts(db)
	if err != nil {
		return "inspect Atlas revision rows failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("Atlas revision rows = %v, want %v", got, want)
}

func comparePostgresRelations(conn *dbschema.DatabaseConnection, schema string, want []string) string {
	got, err := postgresRelationNames(conn, schema)
	if err != nil {
		return "inspect PostgreSQL relations failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("PostgreSQL relations in schema %s = %v, want %v", schema, got, want)
}

func comparePostgresRevisions(conn *dbschema.DatabaseConnection, schema string, want []sqliteRevisionFact) string {
	got, err := postgresRevisionFacts(conn, schema)
	if err != nil {
		return "inspect PostgreSQL Atlas revision rows failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("PostgreSQL Atlas revision rows = %v, want %v", got, want)
}

func comparePostgresIndexes(conn *dbschema.DatabaseConnection, schema, table string, want []string) string {
	got, err := postgresIndexNames(conn, schema, table)
	if err != nil {
		return "inspect PostgreSQL indexes failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("PostgreSQL indexes on %s.%s = %v, want %v", schema, table, got, want)
}

func compareMySQLTables(conn *dbschema.DatabaseConnection, schema string, want []string) string {
	got, err := mysqlTableNames(conn, schema)
	if err != nil {
		return "inspect MySQL tables failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("MySQL tables in schema %s = %v, want %v", schema, got, want)
}

func compareMySQLRevisions(conn *dbschema.DatabaseConnection, schema string, want []sqliteRevisionFact) string {
	got, err := mysqlRevisionFacts(conn, schema)
	if err != nil {
		return "inspect MySQL Atlas revision rows failed: " + oneLine(err.Error())
	}
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("MySQL Atlas revision rows = %v, want %v", got, want)
}

func sqliteURL(path string) string {
	return "sqlite://" + filepath.ToSlash(path)
}

func migrateRuntimeIdentifier(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteMySQLIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func cleanupPostgresRuntimeSchema(dbURL, schema, fixture string) *Result {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quotePostgresIdentifier(schema)+" CASCADE")
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	return nil
}

func cleanupMySQLRuntimeSchema(dbURL, schema, fixture string) *Result {
	conn, err := openMigrateRuntimeConnection(dbURL)
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quoteMySQLIdentifier(schema))
	if err != nil {
		result := migrateRuntimeFail(fixture, "cleanup", err)
		return &result
	}
	return nil
}

func migrateRuntimeFail(fixture, stage string, err error) Result {
	return Result{migrateRuntimeProbeName, fixture, stage, Fail, err.Error(), ""}
}

func migrateRuntimeGap(fixture, stage, detail string) Result {
	return Result{migrateRuntimeProbeName, fixture, stage, Gap, detail, "stokaro/ptah#648"}
}

func migrateRuntimeExit(fixture, stage, output string, err error) Result {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return migrateRuntimeGap(fixture, stage, oneLine(output))
	}
	return migrateRuntimeFail(fixture, stage, err)
}
