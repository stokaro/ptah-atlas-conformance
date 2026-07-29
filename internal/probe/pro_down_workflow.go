package probe

import (
	"fmt"
	"path/filepath"
	"slices"
)

const proDownWorkflowSentinel = "_capability/pro-down-workflow/SENTINEL"

// ProDownWorkflowProbe proves the bare `atlas migrate down` revision-format
// default decided in stokaro/ptah#810: `atlas migrate apply` records its
// history in Atlas-format `atlas_schema_revisions` rows, and a bare `atlas
// migrate down` — no `--revision-format` flag — must read exactly those rows
// and actually revert. Before #810 the bare verb defaulted to Ptah's native
// revision table and silently rolled back nothing. Atlas keeps `migrate down`
// in its Pro build, so this is a first-party capability probe on a real
// SQLite database through the real `atlas ...` CLI. The only extra flag
// passed is `--confirm`, the non-interactive stand-in for the interactive
// rollback confirmation; it does not touch revision-format resolution.
type ProDownWorkflowProbe struct {
	// FixtureRoot contains the committed Atlas-format txtar migrations with
	// embedded down.sql sections. Relative paths are resolved from the probe
	// process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ProDownWorkflowProbe) Name() string { return "pro-down-workflow" }

func (p ProDownWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != proDownWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("pro-down-workflow", proDownWorkflowSentinel, p.FixtureRoot, p.Binary, proVerbsIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	d := &proDownWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		d.atlasFormatApplication,
		d.bareRollback,
	})
}

type proDownWorkflow struct {
	*proWorkflowRuntime
}

func (d *proDownWorkflow) atlasFormatApplication() Result {
	const (
		fixture = "atlas migrate apply"
		stage   = "atlas-format application"
	)
	if harness := d.hashAtlasMigrations(stage); harness != nil {
		return *harness
	}
	result, harness := d.runCLI(stage,
		"migrate", "apply",
		"--url", d.appURL(),
		"--dir", "file://migrations",
	)
	if harness != nil {
		return *harness
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Migration complete. Current version: 20260101000002",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectRevisionVersions(fixture, stage, []string{"20260101000001", "20260101000002"}); gap != nil {
		return *gap
	}
	if gap := d.expectTables(fixture, stage, []string{"atlas_schema_revisions", "posts", "users"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"`atlas migrate apply` executed both txtar migrations and recorded Atlas-format revision rows in atlas_schema_revisions")
}

func (d *proDownWorkflow) bareRollback() Result {
	const (
		fixture = "atlas migrate down"
		stage   = "bare rollback"
	)
	result, harness := d.runCLI(stage,
		"migrate", "down",
		"--url", d.appURL(),
		"--dir", "file://migrations",
		"--confirm",
	)
	if harness != nil {
		return *harness
	}
	if gap := d.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := d.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Migration rollback completed successfully",
	}); gap != nil {
		return *gap
	}
	if gap := d.expectRevisionVersions(fixture, stage, nil); gap != nil {
		return *gap
	}
	if gap := d.expectTables(fixture, stage, []string{"atlas_schema_revisions"}); gap != nil {
		return *gap
	}
	return d.ok(fixture, stage,
		"bare `atlas migrate down` — no --revision-format flag — defaulted to the Atlas revision format, read the rows `atlas migrate apply` wrote, executed both embedded down bodies, and cleared the revision history (before stokaro/ptah#810 this was a silent no-op)")
}

func (d *proDownWorkflow) appURL() string {
	return sqliteURL(filepath.Join(d.runRoot, "app.db"))
}

// expectRevisionVersions reads atlas_schema_revisions directly, independently
// of the CLI, and returns a gap when the recorded fully-applied versions
// differ from want.
func (d *proDownWorkflow) expectRevisionVersions(fixture, stage string, want []string) *Result {
	db, err := openSQLiteRuntimeDB(filepath.Join(d.runRoot, "app.db"))
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return &failure
	}
	defer func() { _ = db.Close() }()
	facts, err := sqliteRevisionFacts(db)
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return &failure
	}
	var got []string
	for _, fact := range facts {
		if fact.Applied != fact.Total {
			gap := d.gap(fixture, stage, fmt.Sprintf(
				"revision %s is partially applied (%d/%d)", fact.Version, fact.Applied, fact.Total))
			return &gap
		}
		got = append(got, fact.Version)
	}
	if !slices.Equal(got, want) {
		gap := d.gap(fixture, stage, fmt.Sprintf(
			"Atlas revision rows = %v, want %v", got, want))
		return &gap
	}
	return nil
}

func (d *proDownWorkflow) expectTables(fixture, stage string, want []string) *Result {
	db, err := openSQLiteRuntimeDB(filepath.Join(d.runRoot, "app.db"))
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return &failure
	}
	defer func() { _ = db.Close() }()
	got, err := sqliteTableNames(db)
	if err != nil {
		failure := d.harnessFailure(stage, err)
		return &failure
	}
	if !slices.Equal(got, want) {
		gap := d.gap(fixture, stage, fmt.Sprintf("tables = %v, want %v", got, want))
		return &gap
	}
	return nil
}
