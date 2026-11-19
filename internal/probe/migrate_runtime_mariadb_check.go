package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The modified-CHECK oracle plans a changed CHECK constraint on MariaDB with
// Atlas CE and with ptah-compat, and applies each plan to its own copy of the
// starting table. MariaDB refuses `DROP CHECK` with error 1064, so a plan that
// replaces the constraint has to drop it with `DROP CONSTRAINT`. Measured on
// 2026-11-19: Atlas CE v1.4.0 writes `DROP CONSTRAINT`, v1.3.0 wrote
// `DROP CHECK`, and ptah-compat writes `DROP CONSTRAINT`.
//
// The statement kind and the applied result are compared, not the plan's bytes:
// the two binaries spell the CHECK expression differently. Stderr is not
// compared either, because Atlas CE writes an edition notice there on its
// first run in a fresh HOME.
const (
	mariadbModifiedCheckFixture = "mariadb/modified-check-plan"
	mariadbModifiedCheckName    = "qty_ck"
	mariadbModifiedCheckDesired = "CREATE TABLE t (id int PRIMARY KEY, qty int, " +
		"CONSTRAINT qty_ck CHECK (qty > 0));\n"
	// mariadbModifiedCheckClause is the clause MariaDB stores for the desired
	// constraint, read back from information_schema.CHECK_CONSTRAINTS.
	mariadbModifiedCheckClause = "`qty` > 0"
)

// mariadbModifiedCheckObservation is what one binary planned, and what applying
// that plan left in the database.
type mariadbModifiedCheckObservation struct {
	plan        integrityProcessResult
	applyError  string
	checkClause string
}

func mariadbModifiedCheckPlan(ptahBin, atlasBin, dbURL string) Result {
	root, err := os.MkdirTemp("", "ptah-mariadb-check-*")
	if err != nil {
		return migrateRuntimeFail(mariadbModifiedCheckFixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	desired := filepath.Join(root, "desired.sql")
	if err := os.WriteFile(desired, []byte(mariadbModifiedCheckDesired), 0o600); err != nil {
		return migrateRuntimeFail(mariadbModifiedCheckFixture, "setup", err)
	}

	atlasEnv := []string{"HOME=" + filepath.Join(root, "atlas-home")}
	atlas, failure := observeMariaDBModifiedCheck(resolveCEGatingBinary(atlasBin), dbURL, desired, "atlas", atlasMariaDBURL, atlasEnv)
	if failure != nil {
		return *failure
	}
	ptah, failure := observeMariaDBModifiedCheck(ptahBin, dbURL, desired, "ptah", func(raw string) string { return raw }, nil)
	if failure != nil {
		return *failure
	}
	return compareMariaDBModifiedCheck(atlas, ptah)
}

// observeMariaDBModifiedCheck creates the starting table in a schema of its
// own, plans the desired schema with bin against a dev schema of its own, and
// applies the plan's statements to the starting table.
func observeMariaDBModifiedCheck(
	bin, dbURL, desired, label string,
	binaryURL func(string) string,
	env []string,
) (mariadbModifiedCheckObservation, *Result) {
	target := migrateRuntimeIdentifier("ptah_rt_mariadb_check_" + label)
	dev := target + "_dev"
	for _, schema := range []string{target, dev} {
		if result := resetMySQLRuntimeSchema(dbURL, schema, mariadbModifiedCheckFixture); result != nil {
			return mariadbModifiedCheckObservation{}, result
		}
		defer cleanupMySQLRuntimeSchema(dbURL, schema, mariadbModifiedCheckFixture) //nolint:errcheck
	}

	conn, err := openMigrateRuntimeConnection(mysqlRuntimeURLForSchema(dbURL, target))
	if err != nil {
		result := migrateRuntimeFail(mariadbModifiedCheckFixture, "setup", err)
		return mariadbModifiedCheckObservation{}, &result
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "CREATE TABLE t (id int PRIMARY KEY, qty int, "+
		"CONSTRAINT qty_ck CHECK (qty >= 0))"); err != nil {
		result := migrateRuntimeFail(mariadbModifiedCheckFixture, "setup", err)
		return mariadbModifiedCheckObservation{}, &result
	}

	var observation mariadbModifiedCheckObservation
	stdout, stderr, err := commandStreamsWithEnv(bin, []string{
		"schema", "diff",
		"--from", binaryURL(mysqlRuntimeURLForSchema(dbURL, target)),
		"--to", fileURL(desired),
		"--dev-url", binaryURL(mysqlRuntimeURLForSchema(dbURL, dev)),
	}, "", env)
	observation.plan = integrityProcessResult{stdout: stdout, stderr: stderr}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		observation.plan.exitCode = exitErr.ExitCode()
		return observation, nil
	case err != nil:
		result := migrateRuntimeFail(mariadbModifiedCheckFixture, label+" plan", err)
		return mariadbModifiedCheckObservation{}, &result
	}

	for _, statement := range planStatements(stdout) {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			observation.applyError = oneLine(err.Error())
			return observation, nil
		}
	}
	if err := conn.QueryRowContext(ctx, `
SELECT check_clause
FROM information_schema.check_constraints
WHERE constraint_schema = ? AND constraint_name = ?`, target, mariadbModifiedCheckName).Scan(&observation.checkClause); err != nil {
		result := migrateRuntimeFail(mariadbModifiedCheckFixture, label+" inspect", err)
		return mariadbModifiedCheckObservation{}, &result
	}
	return observation, nil
}

// planStatements returns the statements of a plan printed by `schema diff`:
// its `--` comment lines dropped and the rest split at each statement's
// closing semicolon.
func planStatements(plan string) []string {
	var body strings.Builder
	for line := range strings.Lines(plan) {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		body.WriteString(line)
	}
	var statements []string
	for statement := range strings.SplitSeq(body.String(), ";\n") {
		if statement = strings.TrimSuffix(strings.TrimSpace(statement), ";"); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

// atlasMariaDBURL gives Atlas CE the host form of a MariaDB URL, which it
// accepts, for the tcp(host:port) form the runtime tier configures.
func atlasMariaDBURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}
	userInfo, afterTCP, ok := strings.Cut(rest, "@tcp(")
	if !ok {
		return raw
	}
	host, suffix, ok := strings.Cut(afterTCP, ")")
	if !ok {
		return raw
	}
	return scheme + "://" + userInfo + "@" + host + suffix
}

// compareMariaDBModifiedCheck checks that each binary planned the change as a
// DROP CONSTRAINT and an ADD CONSTRAINT, and that MariaDB accepted the plan
// and stored the desired clause.
func compareMariaDBModifiedCheck(atlas, ptah mariadbModifiedCheckObservation) Result {
	if problem := mariadbModifiedCheckProblem(atlas); problem != "" {
		return Result{migrateRuntimeProbeName, mariadbModifiedCheckFixture, "atlas-oracle", Gap,
			"Atlas CE no longer plans the modified CHECK as measured: " + problem, ""}
	}
	if problem := mariadbModifiedCheckProblem(ptah); problem != "" {
		return migrateRuntimeGap(mariadbModifiedCheckFixture, "ptah", "ptah-compat modified CHECK plan: "+problem)
	}
	return Result{migrateRuntimeProbeName, mariadbModifiedCheckFixture, "plan", OK,
		"Atlas CE and ptah-compat both replaced the modified MariaDB CHECK with DROP CONSTRAINT and " +
			"ADD CONSTRAINT, and MariaDB applied each plan and stored " + mariadbModifiedCheckClause, ""}
}

func mariadbModifiedCheckProblem(observation mariadbModifiedCheckObservation) string {
	plan := observation.plan
	switch {
	case plan.exitCode != 0:
		return "schema diff " + integrityProcessDetail(plan)
	case strings.Contains(plan.stdout, "DROP CHECK"):
		return "plan drops the constraint with DROP CHECK, which MariaDB refuses: " + oneLine(plan.stdout)
	case !strings.Contains(plan.stdout, "DROP CONSTRAINT `"+mariadbModifiedCheckName+"`") ||
		!strings.Contains(plan.stdout, "ADD CONSTRAINT `"+mariadbModifiedCheckName+"` CHECK"):
		return "plan does not drop and add the constraint: " + oneLine(plan.stdout)
	case observation.applyError != "":
		return "MariaDB refused the plan: " + observation.applyError
	case observation.checkClause != mariadbModifiedCheckClause:
		return fmt.Sprintf("applied CHECK clause %q, want %q", observation.checkClause, mariadbModifiedCheckClause)
	}
	return ""
}
