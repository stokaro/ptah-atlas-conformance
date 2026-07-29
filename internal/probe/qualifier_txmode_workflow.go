package probe

import (
	"os"
	"path/filepath"
	"strings"
)

const qualifierTxModeWorkflowSentinel = "_capability/qualifier-txmode-workflow/SENTINEL"

// qualifierTxModeIssue tracks the `migrate diff --qualifier` and
// concurrent-index txmode-metadata batch (stokaro/ptah#815).
const qualifierTxModeIssue = "stokaro/ptah#815"

// QualifierTxModeWorkflowProbe executes the `migrate diff` qualifier and
// txmode-metadata contracts from stokaro/ptah#815 through the real
// `ptah atlas ...` CLI on ephemeral SQLite: an invalid `--qualifier` fails
// before the dev database exists, a valid qualifier is scoped to dialects
// with schema-qualified DDL (refused pre-artifact on SQLite — qualified
// artifact content itself needs a live PostgreSQL/MySQL dev database and is
// covered by the database-backed tiers), the atlas.hcl concurrent-index diff
// policy plans the documented single plain transactional file on SQLite,
// that artifact replays through `migrate apply`, and a generated-style
// `-- atlas:txmode none` migration executes outside a transaction (its
// statements before a failure persist) while a transactional control rolls
// back.
type QualifierTxModeWorkflowProbe struct {
	// FixtureRoot contains the committed schema, project config, and
	// migration fixtures. Relative paths are resolved from the probe process
	// directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and
	// local development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (QualifierTxModeWorkflowProbe) Name() string { return "qualifier-txmode-workflow" }

func (p QualifierTxModeWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != qualifierTxModeWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("qualifier-txmode-workflow", qualifierTxModeWorkflowSentinel, p.FixtureRoot, p.Binary, qualifierTxModeIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	q := &qualifierTxModeWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		q.invalidQualifierFailsBeforeDevDatabase,
		q.qualifierScopedToQualifiedDialects,
		q.concurrentIndexPolicyPlansSingleTransactionalFile,
		q.concurrentIndexArtifactReplays,
		q.txModeNoneDirectiveSemantics,
	})
}

type qualifierTxModeWorkflow struct {
	*proWorkflowRuntime
}

// emptyMigrationsDir creates (once) the scratch migrations directory the
// qualifier steps target and returns its absolute path.
func (q *qualifierTxModeWorkflow) emptyMigrationsDir(stage string) (string, *Result) {
	dir := filepath.Join(q.runRoot, "qualifier-migrations")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		failure := q.harnessFailure(stage, err)
		return "", &failure
	}
	return dir, nil
}

// expectNoArtifacts returns a gap when the qualifier migrations directory
// gained any file: the refused diff must not write artifacts.
func (q *qualifierTxModeWorkflow) expectNoArtifacts(fixture, stage, dir string) *Result {
	files, err := relativeFilesUnder(dir)
	if err != nil {
		failure := q.harnessFailure(stage, err)
		return &failure
	}
	if len(files) != 0 {
		gap := q.gap(fixture, stage, "the refused migrate diff wrote artifacts: "+strings.Join(files, ", "))
		return &gap
	}
	return nil
}

func (q *qualifierTxModeWorkflow) invalidQualifierFailsBeforeDevDatabase() Result {
	const (
		fixture = "ptah atlas migrate diff --qualifier"
		stage   = "invalid qualifier fails before the dev database"
	)
	dir, harness := q.emptyMigrationsDir(stage)
	if harness != nil {
		return *harness
	}
	devDB := filepath.Join(q.runRoot, "qualifier-never-dev.db")
	result, failure := q.runCLI(stage,
		"atlas", "migrate", "diff", "add_users",
		"--dir", "file://qualifier-migrations",
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(devDB),
		"--qualifier", "bad.name",
	)
	if failure != nil {
		return *failure
	}
	if gap := q.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := q.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		`invalid --qualifier "bad.name": character '.' is not allowed in a schema qualifier`,
	}); gap != nil {
		return *gap
	}
	if gap := q.expectFileNeverCreated(fixture, stage, devDB, "dev database"); gap != nil {
		return *gap
	}
	if gap := q.expectNoArtifacts(fixture, stage, dir); gap != nil {
		return *gap
	}
	return q.ok(fixture, stage,
		"an invalid --qualifier value is refused with the deterministic diagnostic before the dev database file exists and before any artifact is written")
}

func (q *qualifierTxModeWorkflow) qualifierScopedToQualifiedDialects() Result {
	const (
		fixture = "ptah atlas migrate diff --qualifier"
		stage   = "qualified artifacts are scoped to qualified dialects"
	)
	dir, harness := q.emptyMigrationsDir(stage)
	if harness != nil {
		return *harness
	}
	result, failure := q.runCLI(stage,
		"atlas", "migrate", "diff", "add_users",
		"--dir", "file://qualifier-migrations",
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(q.runRoot, "qualifier-dev.db")),
		"--qualifier", "tenant",
	)
	if failure != nil {
		return *failure
	}
	if gap := q.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := q.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		`atlas migrate diff --qualifier is not supported for dialect "sqlite"`,
	}); gap != nil {
		return *gap
	}
	if gap := q.expectNoArtifacts(fixture, stage, dir); gap != nil {
		return *gap
	}
	return q.ok(fixture, stage,
		"a valid --qualifier on a dialect without schema-qualified DDL is refused pre-artifact with the documented scope diagnostic; qualified artifact content is measured on the database-backed tiers")
}

func (q *qualifierTxModeWorkflow) concurrentIndexPolicyPlansSingleTransactionalFile() Result {
	const (
		fixture = "ptah atlas migrate diff"
		stage   = "concurrent-index policy on sqlite plans one transactional file"
	)
	if err := os.MkdirAll(filepath.Join(q.runRoot, "generated"), 0o750); err != nil {
		return q.harnessFailure(stage, err)
	}
	result, failure := q.runCLI(stage,
		"atlas", "migrate", "diff", "add_users",
		"--env", "local",
	)
	if failure != nil {
		return *failure
	}
	if gap := q.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := q.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Created migration file:",
	}); gap != nil {
		return *gap
	}
	files, err := relativeFilesUnder(filepath.Join(q.runRoot, "generated"))
	if err != nil {
		return q.harnessFailure(stage, err)
	}
	var sqlFiles []string
	for _, file := range files {
		if strings.HasSuffix(file, ".sql") {
			sqlFiles = append(sqlFiles, file)
		}
	}
	if len(sqlFiles) != 1 {
		return q.gap(fixture, stage, "the SQLite concurrent-index plan is not a single migration file: "+strings.Join(files, ", "))
	}
	content, err := readRunFile(q.runRoot, filepath.Join("generated", sqlFiles[0]))
	if err != nil {
		return q.harnessFailure(stage, err)
	}
	switch {
	case !strings.Contains(content, "CREATE TABLE") || !strings.Contains(content, "CREATE INDEX"):
		return q.gap(fixture, stage, "the planned migration does not create the desired table and index: "+oneLine(content))
	case strings.Contains(content, "atlas:txmode"):
		return q.gap(fixture, stage, "the SQLite plan carries txmode metadata although the dialect has no concurrent index builds")
	case strings.Contains(content, "CONCURRENTLY"):
		return q.gap(fixture, stage, "the SQLite plan uses CONCURRENTLY although the dialect does not support it")
	}
	return q.ok(fixture, stage,
		"with diff.concurrent_index.create enabled in atlas.hcl, the SQLite plan stays one plain transactional file — no txmode directive, no CONCURRENTLY — exactly the documented dialect scope")
}

func (q *qualifierTxModeWorkflow) concurrentIndexArtifactReplays() Result {
	const (
		fixture = "ptah atlas migrate apply"
		stage   = "concurrent-index artifact replays"
	)
	targetDB := filepath.Join(q.runRoot, "ci-target.db")
	result, failure := q.runCLI(stage,
		"atlas", "migrate", "apply",
		"--url", sqliteURL(targetDB),
		"--dir", "file://generated",
	)
	if failure != nil {
		return *failure
	}
	if gap := q.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := q.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Migration complete. Current version:",
	}); gap != nil {
		return *gap
	}
	if gap := q.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"atlas_schema_revisions", "users"}); gap != nil {
		return *gap
	}
	return q.ok(fixture, stage,
		"the concurrent-index-policy artifact replays through `migrate apply`: the table exists and the Atlas revision row records the applied version")
}

func (q *qualifierTxModeWorkflow) txModeNoneDirectiveSemantics() Result {
	const (
		fixture = "ptah atlas migrate apply"
		stage   = "txmode-none directive executes outside a transaction"
	)
	// Both fixture directories hold a failing second migration whose first
	// statement succeeds; only the txmode-none variant may keep that
	// statement's table.
	for _, dir := range []string{"txmode", "txmode-control"} {
		hash, failure := q.runCLI(stage, "atlas", "migrate", "hash", "--dir", "file://"+dir)
		if failure != nil {
			return *failure
		}
		if hash.exitCode != 0 {
			return q.harnessFailure(stage, errFromCommand("hash "+dir, hash))
		}
	}

	noneDB := filepath.Join(q.runRoot, "txmode-none.db")
	noneResult, failure := q.runCLI(stage,
		"atlas", "migrate", "apply",
		"--url", sqliteURL(noneDB),
		"--dir", "file://txmode",
	)
	if failure != nil {
		return *failure
	}
	if gap := q.expectExit(fixture, stage, noneResult, 1); gap != nil {
		return *gap
	}
	// The txmode-none file ran statement-by-statement outside a transaction:
	// the statement before the failure persisted.
	if gap := q.expectSQLiteTablesAt(fixture, stage, noneDB, []string{"atlas_schema_revisions", "base", "nontx_first"}); gap != nil {
		return *gap
	}

	controlDB := filepath.Join(q.runRoot, "txmode-control.db")
	controlResult, failure := q.runCLI(stage,
		"atlas", "migrate", "apply",
		"--url", sqliteURL(controlDB),
		"--dir", "file://txmode-control",
	)
	if failure != nil {
		return *failure
	}
	if gap := q.expectExit(fixture, stage, controlResult, 1); gap != nil {
		return *gap
	}
	// The identical failure inside a transactional file rolled back.
	if gap := q.expectSQLiteTablesAt(fixture, stage, controlDB, []string{"atlas_schema_revisions", "base"}); gap != nil {
		return *gap
	}
	return q.ok(fixture, stage,
		"a `-- atlas:txmode none` migration executes outside a transaction (the statement before the failure persisted) while the identical transactional control rolled back cleanly")
}
