package probe

import (
	"path/filepath"
)

const schemaScopeWorkflowSentinel = "_capability/schema-scope-workflow/SENTINEL"

// schemaScopeIssue tracks the `--schema`/`--include` desired-state scoping
// batch (stokaro/ptah#813).
const schemaScopeIssue = "stokaro/ptah#813"

// SchemaScopeWorkflowProbe executes the `--schema`/`--include` scoping model
// from stokaro/ptah#813 through the real `ptah atlas ...` CLI on ephemeral
// SQLite: a scoped `schema apply` creates only the selected objects and
// leaves out-of-scope objects (desired and pre-existing) untouched, repeated
// `--include` values union, a selection whose objects depend on unselected
// objects is refused with the cross-scope foreign-key diagnostic, and a
// malformed selector fails before the dev database is contacted.
type SchemaScopeWorkflowProbe struct {
	// FixtureRoot contains the committed desired-schema source files. Relative
	// paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and
	// local development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (SchemaScopeWorkflowProbe) Name() string { return "schema-scope-workflow" }

func (p SchemaScopeWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != schemaScopeWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("schema-scope-workflow", schemaScopeWorkflowSentinel, p.FixtureRoot, p.Binary, schemaScopeIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	s := &schemaScopeWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		s.scopedApplyLeavesOutOfScopeUntouched,
		s.includeValuesUnion,
		s.crossScopeForeignKeyRefusal,
		s.malformedSelectorFailsBeforeDevDatabase,
	})
}

type schemaScopeWorkflow struct {
	*proWorkflowRuntime
}

func (s *schemaScopeWorkflow) scopedApplyLeavesOutOfScopeUntouched() Result {
	const (
		fixture = "ptah atlas schema apply --include"
		stage   = "scoped apply leaves out-of-scope objects untouched"
	)
	targetDB := filepath.Join(s.runRoot, "scope-target.db")
	// The target already contains an out-of-scope table the scoped apply must
	// leave alone.
	if err := execSQLiteStatement(targetDB, "CREATE TABLE scope_keepme (id INTEGER PRIMARY KEY)"); err != nil {
		return s.harnessFailure(stage, err)
	}
	result, failure := s.runCLI(stage,
		"atlas", "schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--include", "scope_users",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schema apply completed successfully.",
	}); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"scope_keepme", "scope_users"}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"`schema apply --include scope_users` created only the selected table: the desired-but-unselected tables were not created and the pre-existing out-of-scope table survived")
}

func (s *schemaScopeWorkflow) includeValuesUnion() Result {
	const (
		fixture = "ptah atlas schema apply --include"
		stage   = "repeated include values union"
	)
	targetDB := filepath.Join(s.runRoot, "union-target.db")
	result, failure := s.runCLI(stage,
		"atlas", "schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--include", "scope_users",
		"--include", "scope_archive",
		"--auto-approve",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := s.expectSQLiteTablesAt(fixture, stage, targetDB, []string{"scope_archive", "scope_users"}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"repeated --include values union: both selected tables were created while the unselected dependent table stayed out of the plan")
}

func (s *schemaScopeWorkflow) crossScopeForeignKeyRefusal() Result {
	const (
		fixture = "ptah atlas schema apply --include"
		stage   = "cross-scope foreign key refusal"
	)
	targetDB := filepath.Join(s.runRoot, "fk-target.db")
	result, failure := s.runCLI(stage,
		"atlas", "schema", "apply",
		"--url", sqliteURL(targetDB),
		"--to", "file://schema.sql",
		"--include", "scope_groups",
		"--dry-run",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		`table "scope_groups" depends on table "scope_users" via a foreign key, but "scope_users" is not selected`,
		"add the missing objects to the selection or exclude the dependent objects",
	}); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"selecting a table whose foreign key points at an unselected table is refused with the cross-scope dependency diagnostic and its remediation guidance")
}

func (s *schemaScopeWorkflow) malformedSelectorFailsBeforeDevDatabase() Result {
	const (
		fixture = "ptah atlas schema diff --include"
		stage   = "malformed selector fails before the dev database"
	)
	devDB := filepath.Join(s.runRoot, "never-dev.db")
	result, failure := s.runCLI(stage,
		"atlas", "schema", "diff",
		"--from", "file://empty.sql",
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(devDB),
		"--include", "*[type=column]",
	)
	if failure != nil {
		return *failure
	}
	if gap := s.expectExit(fixture, stage, result, 1); gap != nil {
		return *gap
	}
	if gap := s.expectFragments(fixture, stage, "stderr", result.stderr, []string{
		`unsupported Atlas include selector "*[type=column]": column resources ride along with their parent and cannot be included on their own`,
	}); gap != nil {
		return *gap
	}
	if gap := s.expectFileNeverCreated(fixture, stage, devDB, "dev database"); gap != nil {
		return *gap
	}
	return s.ok(fixture, stage,
		"a malformed include selector fails with the deterministic diagnostic before any database work: the dev database file was never created")
}
