package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	proMaintWorkflowSentinel = "_capability/pro-maint-workflow/SENTINEL"

	proMaintEditTarget   = "20260101000001"
	proMaintEditFile     = "20260101000001_create_users.sql"
	proMaintRemoveTarget = "20260101000002"
	proMaintRemoveFile   = "20260101000002_create_posts.sql"
	proMaintEditMarker   = "-- edited by the conformance probe"
)

// ProMaintWorkflowProbe executes the Atlas Pro directory-maintenance verbs
// Ptah implements as open capabilities — `atlas migrate edit`, `atlas migrate
// rebase`, and `atlas migrate rm` (stokaro/ptah#807) — end to end through the
// real `atlas ...` CLI. The workflow is fully offline: it mutates a
// scratch copy of a committed Atlas-format directory with a hermetic scripted
// $EDITOR and proves each verb leaves the directory in a state that still
// passes `ptah migrations validate` against the rewritten atlas.sum.
type ProMaintWorkflowProbe struct {
	// FixtureRoot contains the committed Atlas-format migration directory.
	// Relative paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ProMaintWorkflowProbe) Name() string { return "pro-maint-workflow" }

func (p ProMaintWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != proMaintWorkflowSentinel {
		return nil
	}
	w, failure := newProWorkflowRuntime("pro-maint-workflow", proMaintWorkflowSentinel, p.FixtureRoot, p.Binary, proVerbsIssue)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	m := &proMaintWorkflow{proWorkflowRuntime: w}
	return w.runSteps([]func() Result{
		m.editorRoundTrip,
		m.rebaseToEndOfHistory,
		m.removeMigration,
	})
}

type proMaintWorkflow struct {
	*proWorkflowRuntime
}

func (m *proMaintWorkflow) editorRoundTrip() Result {
	const (
		fixture = "atlas migrate edit"
		stage   = "editor round-trip"
	)
	if harness := m.hashAtlasMigrations(stage); harness != nil {
		return *harness
	}
	// The editor is a hermetic script appending a marker line, so the "edit"
	// is deterministic and never opens an interactive program. Both $VISUAL
	// and $EDITOR are pinned because $VISUAL wins when set.
	editor := filepath.Join(m.runRoot, "append-editor.sh")
	script := "#!/bin/sh\nprintf -- '" + proMaintEditMarker + "\\n' >> \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil { //nolint:gosec // The scripted editor must be executable.
		return m.harnessFailure(stage, err)
	}
	result, harness := m.runCLIWithEnv(stage,
		[]string{"EDITOR=" + editor, "VISUAL=" + editor},
		"migrate", "edit", "--dir", "file://migrations", proMaintEditTarget,
	)
	if harness != nil {
		return *harness
	}
	if gap := m.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := m.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Edited migration " + proMaintEditTarget,
		"Wrote migrations/atlas.sum",
	}); gap != nil {
		return *gap
	}
	edited, err := os.ReadFile(filepath.Join(m.runRoot, "migrations", proMaintEditFile))
	if err != nil {
		return m.harnessFailure(stage, err)
	}
	if !strings.Contains(string(edited), proMaintEditMarker) {
		return m.gap(fixture, stage, "the scripted $EDITOR change is missing from the migration file after edit")
	}
	if gap := m.expectValidate(fixture, stage); gap != nil {
		return *gap
	}
	return m.ok(fixture, stage,
		"the hermetic scripted $EDITOR change landed in the migration file, atlas.sum was rewritten, and the directory still passes `ptah migrations validate`")
}

func (m *proMaintWorkflow) rebaseToEndOfHistory() Result {
	const (
		fixture = "atlas migrate rebase"
		stage   = "rebase to end of history"
	)
	startedAt := time.Now().UTC().Truncate(time.Second)
	result, harness := m.runCLI(stage,
		"migrate", "rebase", "--dir", "file://migrations", proMaintEditTarget,
	)
	finishedAt := time.Now().UTC().Truncate(time.Second)
	if harness != nil {
		return *harness
	}
	if gap := m.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := m.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Rebased migration " + proMaintEditTarget + " to ",
		"Wrote migrations/atlas.sum",
	}); gap != nil {
		return *gap
	}
	rebasedVersion, err := proMaintRebasedVersion(result.stdout)
	if err != nil {
		return m.gap(fixture, stage, err.Error())
	}
	rebasedAt, err := time.Parse("20060102150405", strconv.FormatInt(rebasedVersion, 10))
	if err != nil {
		return m.gap(fixture, stage,
			fmt.Sprintf("rebased version %d is not a readable UTC calendar second: %s", rebasedVersion, oneLine(err.Error())))
	}
	if rebasedAt.Before(startedAt) || rebasedAt.After(finishedAt) {
		return m.gap(fixture, stage, fmt.Sprintf(
			"rebased version %d is outside the command window %s..%s",
			rebasedVersion, startedAt.Format(time.RFC3339), finishedAt.Format(time.RFC3339)))
	}
	if _, err := os.Stat(filepath.Join(m.runRoot, "migrations", proMaintEditFile)); !os.IsNotExist(err) {
		return m.gap(fixture, stage, "the rebased migration's old file still exists: "+proMaintEditFile)
	}
	rebasedFile := strconv.FormatInt(rebasedVersion, 10) + "_create_users.sql"
	rebased, err := os.ReadFile(filepath.Join(m.runRoot, "migrations", rebasedFile))
	if err != nil {
		return m.gap(fixture, stage, "the rebased migration file is missing: "+oneLine(err.Error()))
	}
	if !strings.Contains(string(rebased), proMaintEditMarker) {
		return m.gap(fixture, stage, "the rebased migration file lost the edited content")
	}
	if gap := m.expectValidate(fixture, stage); gap != nil {
		return *gap
	}
	return m.ok(fixture, stage,
		"the migration moved to the end of history under a readable UTC calendar-second version, kept its edited content, and the directory still passes `ptah migrations validate`")
}

func proMaintRebasedVersion(stdout string) (int64, error) {
	prefix := "Rebased migration " + proMaintEditTarget + " to "
	for line := range strings.SplitSeq(stdout, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), prefix)
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse rebased version %q: %w", value, err)
		}
		return version, nil
	}
	return 0, fmt.Errorf("stdout does not contain %q", prefix+"<version>")
}

func (m *proMaintWorkflow) removeMigration() Result {
	const (
		fixture = "atlas migrate rm"
		stage   = "remove migration"
	)
	result, harness := m.runCLI(stage,
		"migrate", "rm", "--dir", "file://migrations", proMaintRemoveTarget,
	)
	if harness != nil {
		return *harness
	}
	if gap := m.expectExit(fixture, stage, result, 0); gap != nil {
		return *gap
	}
	if gap := m.expectFragments(fixture, stage, "stdout", result.stdout, []string{
		"Removed migrations/" + proMaintRemoveFile,
		"Wrote migrations/atlas.sum",
	}); gap != nil {
		return *gap
	}
	if _, err := os.Stat(filepath.Join(m.runRoot, "migrations", proMaintRemoveFile)); !os.IsNotExist(err) {
		return m.gap(fixture, stage, "the removed migration file still exists: "+proMaintRemoveFile)
	}
	sum, err := os.ReadFile(filepath.Join(m.runRoot, "migrations", "atlas.sum"))
	if err != nil {
		return m.harnessFailure(stage, err)
	}
	if strings.Contains(string(sum), proMaintRemoveFile) {
		return m.gap(fixture, stage, "atlas.sum still covers the removed migration file")
	}
	if gap := m.expectValidate(fixture, stage); gap != nil {
		return *gap
	}
	return m.ok(fixture, stage,
		"the migration file was removed, atlas.sum no longer covers it, and the remaining directory still passes `ptah migrations validate`")
}

// expectValidate asserts the scratch directory passes the native integrity
// check against the rewritten atlas.sum after each maintenance verb. It runs
// the native `ptah` binary: `migrations validate` is a Ptah-owned harness
// verification, not part of the measured Atlas drop-in surface.
func (m *proMaintWorkflow) expectValidate(fixture, stage string) *Result {
	result, harness := m.runNativeCLI(stage,
		"migrations", "validate", "--dir", "migrations", "--dir-format", "atlas",
	)
	if harness != nil {
		return harness
	}
	if result.exitCode != 0 {
		gap := m.gap(fixture, stage, fmt.Sprintf(
			"`ptah migrations validate` failed after the maintenance verb with exit code %d: %s",
			result.exitCode, result.diagnostic()))
		return &gap
	}
	if !strings.Contains(result.stdout, "matches atlas.sum") {
		gap := m.gap(fixture, stage,
			"`ptah migrations validate` did not confirm the directory matches atlas.sum: "+result.diagnostic())
		return &gap
	}
	return nil
}
