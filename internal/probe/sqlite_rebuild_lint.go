package probe

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const sqliteRebuildLintIssue = "stokaro/ptah#5953"

// sqliteRebuildLint measures the final migration against the schema produced
// by its predecessors. A text-only lint pass cannot tell which columns the
// replacement table omitted, so it cannot establish Atlas's DS103 finding.
func (p LintProbe) sqliteRebuildLint(fx Fixture) []Result {
	result := func(outcome Outcome, detail string) []Result {
		issue := ""
		if outcome != OK {
			issue = sqliteRebuildLintIssue
		}
		return []Result{{"lint-parity", fx.Name, "lint", outcome, detail, issue}}
	}
	bin := p.Binary
	if bin == "" {
		var err error
		bin, err = ptahCompatAtlasBinary()
		if err != nil {
			return result(Fail, "build ptah-compat for SQLite lint: "+oneLine(err.Error()))
		}
	}
	root, err := os.MkdirTemp("", "sqlite-rebuild-lint-*")
	if err != nil {
		return result(Fail, "create SQLite lint scratch directory: "+oneLine(err.Error()))
	}
	defer func() { _ = os.RemoveAll(root) }()
	migrations := filepath.Join(root, "migrations")
	if err := os.Mkdir(migrations, 0o700); err != nil {
		return result(Fail, "create SQLite lint migration directory: "+oneLine(err.Error()))
	}
	if err := os.CopyFS(migrations, os.DirFS(fx.Dir)); err != nil {
		return result(Fail, "copy SQLite lint fixture: "+oneLine(err.Error()))
	}
	stdout, stderr, runErr := commandStreamsStrictCE(bin, []string{
		"migrate", "lint", "--dir", fileURL(migrations),
		"--dev-url", sqliteURL(filepath.Join(root, "dev.db")), "--latest", "1",
	}, root)
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return result(Fail, "run SQLite lint: "+oneLine(runErr.Error()))
	}
	exitCode := 0
	if exitErr != nil {
		exitCode = exitErr.ExitCode()
	}
	// Atlas CE accepts the orphaned friendship row in sqlitetx2. Ptah
	// deliberately checks foreign keys before commit and rolls it back, and
	// reports the refusal as Atlas CE reports a replay it cannot finish: an
	// Error line inside the lint report on stdout, nothing on stderr, exit 1.
	if fx.Name == "cmd/atlas/internal/cmdapi/testdata/sqlitetx2" &&
		exitCode == 1 && stderr == "" && reportsOrphanedFriendshipRefusal(stdout) {
		return result(OK, "Ptah refused the orphaned friendship row before commit; Atlas CE accepts this invalid rebuild")
	}
	if stderr != "" {
		return result(Fail, "SQLite lint replay failed: "+oneLine(strings.ReplaceAll(stderr, root, "<scratch>")))
	}
	columnDrop := strings.Contains(stdout, `L4: Dropping non-virtual column "name"`) &&
		strings.Contains(stdout, "lint/analyzers#DS103")
	oneDiagnostic := strings.Contains(stdout, "-- 1 diagnostic\n")
	oneSchemaChange := strings.Contains(stdout, "-- 1 schema change\n")
	if exitCode == 1 && columnDrop && oneDiagnostic && oneSchemaChange {
		return result(OK, "SQLite dev replay reported one DS103 for the name column lost by the final table rebuild")
	}
	if exitCode == 1 && columnDrop && oneDiagnostic {
		return []Result{{"lint-parity", fx.Name, "lint", Gap,
			"SQLite dev replay reported DS103 but counted more than one schema change", "stokaro/ptah#5984"}}
	}
	return result(Gap, fmt.Sprintf(
		"SQLite dev replay did not match DS103 and the schema summary (exit %d, DS103=%t, one diagnostic=%t, one schema change=%t)",
		exitCode, columnDrop, oneDiagnostic, oneSchemaChange))
}

// sqliteOrphanRefusal is Ptah's own text for the foreign-key check that
// refuses sqlitetx2's rebuild. Atlas CE accepts the rebuild, so there is no
// Atlas wording to compare it with.
const sqliteOrphanRefusal = "rebuild left 1 unresolved foreign-key reference(s): friendships references missing users"

// reportsOrphanedFriendshipRefusal reports whether a lint report on stdout
// names the refusal on one of its Error lines.
func reportsOrphanedFriendshipRefusal(stdout string) bool {
	for line := range strings.Lines(stdout) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error: ") && strings.Contains(line, sqliteOrphanRefusal) {
			return true
		}
	}
	return false
}
