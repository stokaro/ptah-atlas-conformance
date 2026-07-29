package probe

import (
	"path/filepath"
	"slices"
	"strings"
)

const inspectSourceWorkflowSentinel = "_capability/inspect-source-workflow/SENTINEL"

// inspectSourceIssue tracks the `schema inspect` source and export batch
// (stokaro/ptah#814): dev-database inspection of non-database sources,
// split/write exports, and exclude field selectors.
const inspectSourceIssue = "stokaro/ptah#814"

// InspectSourceWorkflowProbe executes the `schema inspect` source model from
// stokaro/ptah#814 through the real `atlas ...` CLI on ephemeral SQLite:
// a local schema file is materialized on a dev database and introspected back
// (with a scheme-less path classified as the same local-file source), the
// split/write template exports a deterministic per-object tree whose files
// reload to the same schema, and `--exclude` supports resource selectors plus
// the documented extension field selector while refusing unsupported field
// selectors.
type InspectSourceWorkflowProbe struct {
	// FixtureRoot contains the committed desired-schema source file. Relative
	// paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and
	// local development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (InspectSourceWorkflowProbe) Name() string { return "inspect-source-workflow" }

func (p InspectSourceWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != inspectSourceWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("inspect-source-workflow", inspectSourceWorkflowSentinel, p.FixtureRoot, p.Binary, inspectSourceIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	i := &inspectSourceWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		i.localFileOverDevDatabase,
		i.splitWriteDeterministicTree,
		i.writtenTreeReloads,
		i.excludeSelectors,
	})
}

type inspectSourceWorkflow struct {
	*proWorkflowRuntime
}

func (i *inspectSourceWorkflow) localFileOverDevDatabase() Result {
	const (
		fixture = "atlas schema inspect"
		stage   = "local schema file over dev database"
	)
	result, failure := i.runCLI(stage,
		"schema", "inspect",
		"--url", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "insp-dev.db")),
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := i.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		`table "users"`,
		`table "posts"`,
		`column "email"`,
	}); gap != nil {
		return *gap
	}
	// A scheme-less token is classified as the same local-file source — the
	// #811/#814 source model that replaced the old missing-scheme rejection.
	schemeless, failure := i.runCLI(stage,
		"schema", "inspect",
		"--url", "schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "insp-dev-schemeless.db")),
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, schemeless, 0); gap != nil {
		return *gap
	}
	if schemeless.stdout != result.stdout {
		return i.gap(fixture, stage,
			"a scheme-less --url did not resolve to the same local-file inspection as file://: "+oneLine(schemeless.stdout))
	}
	return i.ok(fixture, stage,
		"`schema inspect --url file://schema.sql --dev-url ...` materialized the file on the dev database and rendered its HCL; a scheme-less path resolves to the identical local-file inspection")
}

func (i *inspectSourceWorkflow) splitWriteDeterministicTree() Result {
	const (
		fixture = "atlas schema inspect"
		stage   = "split export writes a deterministic tree"
	)
	result, failure := i.runCLI(stage,
		"schema", "inspect",
		"--url", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "split-dev.db")),
		"--format", `{{ hcl . | split | write "exported" }}`,
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if strings.TrimSpace(result.stdout) != "" {
		return i.gap(fixture, stage, "a write-mode export printed to stdout instead of only writing files: "+oneLine(result.stdout))
	}
	files, err := relativeFilesUnder(filepath.Join(i.runRoot, "exported"))
	if err != nil {
		return i.harnessFailure(stage, err)
	}
	want := []string{"tables/posts.hcl", "tables/users.hcl"}
	if !slices.Equal(files, want) {
		return i.gap(fixture, stage,
			"the split/write export tree is not the documented per-object shape: got "+strings.Join(files, ", ")+", want "+strings.Join(want, ", "))
	}
	for _, check := range []struct{ file, fragment string }{
		{"tables/users.hcl", `table "users"`},
		{"tables/posts.hcl", `table "posts"`},
	} {
		content, err := readRunFile(i.runRoot, filepath.Join("exported", check.file))
		if err != nil {
			return i.harnessFailure(stage, err)
		}
		if !strings.Contains(content, check.fragment) {
			return i.gap(fixture, stage, check.file+" does not contain "+check.fragment)
		}
	}
	return i.ok(fixture, stage,
		"`{{ hcl . | split | write \"exported\" }}` wrote the deterministic per-object tree tables/{posts,users}.hcl with one table block per file and nothing on stdout")
}

func (i *inspectSourceWorkflow) writtenTreeReloads() Result {
	const (
		fixture = "atlas schema inspect"
		stage   = "written tree reloads to the same schema"
	)
	result, failure := i.runCLI(stage,
		"schema", "diff",
		"--from", "file://exported/tables/users.hcl",
		"--from", "file://exported/tables/posts.hcl",
		"--to", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "reload-dev.db")),
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := i.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Schemas are synced, no changes to be made.",
	}); gap != nil {
		return *gap
	}
	return i.ok(fixture, stage,
		"the exported per-object files reload as a multi-file desired state that diffs as synced against the original schema")
}

func (i *inspectSourceWorkflow) excludeSelectors() Result {
	const (
		fixture = "atlas schema inspect --exclude"
		stage   = "resource and field selectors"
	)
	excluded, failure := i.runCLI(stage,
		"schema", "inspect",
		"--url", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "excl-dev.db")),
		"--exclude", "posts",
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, excluded, 0); gap != nil {
		return *gap
	}
	if !strings.Contains(excluded.stdout, `table "users"`) || strings.Contains(excluded.stdout, `table "posts"`) {
		return i.gap(fixture, stage, "--exclude posts did not filter exactly the excluded table: "+oneLine(excluded.stdout))
	}
	// The documented extension field selector parses and no-ops on SQLite.
	fieldSelector, failure := i.runCLI(stage,
		"schema", "inspect",
		"--url", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "excl-field-dev.db")),
		"--exclude", "[type=extension].version",
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, fieldSelector, 0); gap != nil {
		return *gap
	}
	// An unsupported field-selector form is refused deterministically.
	unsupported, failure := i.runCLI(stage,
		"schema", "inspect",
		"--url", "file://schema.sql",
		"--dev-url", sqliteURL(filepath.Join(i.runRoot, "excl-bad-dev.db")),
		"--exclude", "[type=table].version",
	)
	if failure != nil {
		return *failure
	}
	if gap := i.expectExit(fixture, stage, unsupported, 1); gap != nil {
		return *gap
	}
	if gap := i.expectFragments(fixture, stage, "stderr", unsupported.stderr, []string{
		"unsupported Atlas exclude field selector",
	}); gap != nil {
		return *gap
	}
	return i.ok(fixture, stage,
		"--exclude filters resource selectors, accepts the documented [type=extension].version field selector, and refuses unsupported field-selector forms with a deterministic diagnostic")
}
