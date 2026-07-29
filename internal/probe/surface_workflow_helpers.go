package probe

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// This file holds the SQLite- and filesystem-level assertions shared by the
// Atlas surface-batch workflow probes (desired-state, apply-simulation,
// schema-scope, inspect-source, and qualifier-txmode). They read databases and
// directories directly, independently of the measured CLI, so a probe's
// verdict never depends on the command output alone.

// execSQLiteStatement runs one statement against a SQLite database file,
// creating the file when it does not exist. Probes use it to seed targets and
// pre-litter dev databases outside the measured CLI.
func execSQLiteStatement(dbPath, statement string) error {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), statement); err != nil {
		return fmt.Errorf("exec on %s: %w", dbPath, err)
	}
	return nil
}

// expectSQLiteTablesAt reads the SQLite database at the absolute dbPath and
// returns a gap when its user tables differ from want (sorted). A nil return
// means the tables match exactly.
func (w *proWorkflowRuntime) expectSQLiteTablesAt(fixture, stage, dbPath string, want []string) *Result {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("open %s: %w", dbPath, err))
		return &failure
	}
	defer func() { _ = db.Close() }()
	got, err := sqliteTableNames(db)
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("list tables of %s: %w", dbPath, err))
		return &failure
	}
	if !slices.Equal(got, want) {
		gap := w.gap(fixture, stage, fmt.Sprintf("%s tables = %v, want %v", filepath.Base(dbPath), got, want))
		return &gap
	}
	return nil
}

// expectFileNeverCreated returns a gap when path exists: the measured command
// was required to fail before the file (a target or dev database) came into
// being.
func (w *proWorkflowRuntime) expectFileNeverCreated(fixture, stage, path, role string) *Result {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	gap := w.gap(fixture, stage, fmt.Sprintf("the %s %s exists although the command had to fail before creating it", role, filepath.Base(path)))
	return &gap
}

// errFromCommand wraps a completed-but-failed harness command (not a measured
// expectation) into an error for a Fail result.
func errFromCommand(action string, result ptahCommandResult) error {
	return fmt.Errorf("%s: exit code %d: %s", action, result.exitCode, result.diagnostic())
}

// readRunFile reads a file below the scratch run root and returns its content
// as a string.
func readRunFile(runRoot, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runRoot, name))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// relativeFilesUnder lists every regular file under root as a slash-separated
// path relative to root, sorted, so probes can assert an exported tree is
// exactly the deterministic shape the CLI documents.
func relativeFilesUnder(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}
