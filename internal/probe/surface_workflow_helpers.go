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

type sqliteColumnFact struct {
	Name       string
	Type       string
	PrimaryKey int64
}

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

// expectSQLiteInt64ColumnAt runs a read-only query and compares its single
// integer column in row order. Workflow probes use it to prove that a command
// preserved brownfield data, not merely the containing table.
func (w *proWorkflowRuntime) expectSQLiteInt64ColumnAt(fixture, stage, dbPath, query string, want []int64) *Result {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("open %s: %w", dbPath, err))
		return &failure
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("query %s: %w", dbPath, err))
		return &failure
	}
	defer func() { _ = rows.Close() }()
	got := make([]int64, 0, len(want))
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			failure := w.harnessFailure(stage, fmt.Errorf("scan %s: %w", dbPath, err))
			return &failure
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("read %s: %w", dbPath, err))
		return &failure
	}
	if !slices.Equal(got, want) {
		gap := w.gap(fixture, stage, fmt.Sprintf("%s query values = %v, want %v", filepath.Base(dbPath), got, want))
		return &gap
	}
	return nil
}

// expectSQLiteColumnFactsAt compares the complete ordered column set for one
// table, including declared types and primary-key positions.
func (w *proWorkflowRuntime) expectSQLiteColumnFactsAt(
	fixture string,
	stage string,
	dbPath string,
	table string,
	want []sqliteColumnFact,
) *Result {
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("open %s: %w", dbPath, err))
		return &failure
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(context.Background(), `
SELECT name, lower(type), pk
FROM pragma_table_info(?)
ORDER BY cid`, table)
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("read %s.%s columns: %w", dbPath, table, err))
		return &failure
	}
	defer func() { _ = rows.Close() }()
	got := make([]sqliteColumnFact, 0, len(want))
	for rows.Next() {
		var fact sqliteColumnFact
		if err := rows.Scan(&fact.Name, &fact.Type, &fact.PrimaryKey); err != nil {
			failure := w.harnessFailure(stage, fmt.Errorf("scan %s.%s columns: %w", dbPath, table, err))
			return &failure
		}
		got = append(got, fact)
	}
	if err := rows.Err(); err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("iterate %s.%s columns: %w", dbPath, table, err))
		return &failure
	}
	if !slices.Equal(got, want) {
		gap := w.gap(fixture, stage, fmt.Sprintf("%s.%s columns = %v, want %v", filepath.Base(dbPath), table, got, want))
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
