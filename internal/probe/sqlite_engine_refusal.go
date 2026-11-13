package probe

import (
	"context"
	"database/sql"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A SQLite statement can be refused by the engine ptah-compat links and
// accepted by the one Atlas CE links. SQLite 3.54.0 forbids adding, dropping
// or renaming a ROWID, _ROWID_ or OID column. ptah-compat lints on
// modernc.org/sqlite, which embeds SQLite 3.54.1; the pinned Atlas CE binary
// embeds 3.49.1. The imported cli-migrate-lint-destructive and
// cli-migrate-lint-project fixtures rename a column to oid in their first
// migration, so Ptah's dev replay stops there and Atlas CE's does not.
//
// The Ptah maintainers keep the refusal: Ptah lints against the engine it
// ships, and `migrate apply` would hit the same refusal on any SQLite 3.54 or
// later. The corpus stays unchanged, so the harness recognizes the difference
// by measuring it rather than by editing the fixture.

// sqliteRowidAliasIssue records that decision.
const sqliteRowidAliasIssue = "stokaro/ptah#5952"

// sqliteDriverModule is the pure-Go SQLite driver this harness and ptah-compat
// both link. It embeds its own SQLite release, so the module version fixes the
// engine version.
const sqliteDriverModule = "modernc.org/sqlite"

// sqliteRowidAliasRefusal is SQLite's own text for refusing to rename a column
// to a rowid alias.
const sqliteRowidAliasRefusal = "cannot rename to rowid alias"

// atlasCESQLite is the SQLite release the pinned Atlas CE v1.3.0 binary
// embeds. It was read through bin/atlas by applying a migration whose ATTACH
// path ends in sqlite_version(): the engine names the path in its error. That
// engine accepts the rename.
const atlasCESQLite = "3.49.1"

// sqliteEngineRefusal is the answer of the SQLite engine the harness links to
// a migration directory it refused with the rowid alias error.
type sqliteEngineRefusal struct {
	// engine is SELECT sqlite_version() on the engine that refused.
	engine string
	// module is the modernc.org/sqlite version the harness and the measured
	// ptah-compat binary both link.
	module string
	// file is the migration file the engine refused.
	file string
	// reason is the engine's error.
	reason string
}

// classify decides a migrate lint run over a directory the linked engine
// refuses; command is the run as the fixture spells it. Exit 1 is the
// recognized engine-version difference: the run is reported OK, and its streams
// and redirect target are not compared, because no report of that directory
// can reproduce Atlas CE's. The decision reads only the exit code, never Ptah's
// wording or output, so it holds whether Ptah reports the replay failure on
// stderr or inside the lint report.
//
// Any other exit stays red. Exit 0 means Ptah passed a migration its own engine
// refuses; another non-zero code is a crash or a timeout, not this difference.
func (r sqliteEngineRefusal) classify(command string, run txtarMigrateLintRun) txtarCommandResult {
	result := txtarCommandResult{stdout: run.stdout, stderr: run.stderr, failed: true, assertionsBlocked: true}
	switch {
	case !run.failed:
		result.err = fmt.Errorf("atlas migrate lint exited 0, but SQLite %s refuses %s: %s",
			r.engine, r.file, r.reason)
	case run.exitCode != 1:
		result.err = fmt.Errorf("%v (SQLite %s refuses %s, but that difference exits 1)", run.err, r.engine, r.file)
	default:
		result.engineRefusal = fmt.Sprintf(
			"engine-version difference: `%s` exited 1 because SQLite %s (%s %s, which ptah-compat links) "+
				"refuses %s: %s; Atlas CE v1.3.0 embeds SQLite %s, which accepts the rename",
			command, r.engine, sqliteDriverModule, r.module, r.file, r.reason, atlasCESQLite)
	}
	return result
}

// sqliteMigrationFile is one migration file of a directory, in replay order.
type sqliteMigrationFile struct {
	name string
	sql  string
}

// measureSQLiteEngineRefusal replays the migration directory a lint command
// reads on the SQLite engine the harness links. It reports a refusal only when
// the engine refuses a file with the rowid alias error and the binary under
// measurement links the same engine, since only then does the harness's
// measurement describe the binary's replay.
func measureSQLiteEngineRefusal(runtime *txtarRuntime, plan txtarMigrateLintPlan, bin string) (sqliteEngineRefusal, bool) {
	dir := txtarMigrateCommandRuntimeDir(runtime, plan.cliArgs)
	var files []sqliteMigrationFile
	for _, name := range txtarMigrationApplySQLFilesInDir(runtime, dir) {
		files = append(files, sqliteMigrationFile{name: name, sql: runtime.files[name]})
	}
	refusal, refused, err := replaySQLiteMigrations(files)
	if err != nil || !refused {
		return sqliteEngineRefusal{}, false
	}
	module, shared := sqliteEngineShared(bin)
	if !shared {
		return sqliteEngineRefusal{}, false
	}
	refusal.module = module
	return refusal, true
}

// replaySQLiteMigrations runs files in order on a fresh in-memory database of
// the SQLite engine this harness links, and stops at the first file the engine
// refuses, as a dev replay does. It reports a refusal only for the rowid alias
// error: success and any other error are not this difference.
func replaySQLiteMigrations(files []sqliteMigrationFile) (sqliteEngineRefusal, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return sqliteEngineRefusal{}, false, err
	}
	defer func() { _ = db.Close() }()
	// Every connection to :memory: opens a database of its own, so the replay
	// holds one connection for its whole length.
	conn, err := db.Conn(ctx)
	if err != nil {
		return sqliteEngineRefusal{}, false, err
	}
	defer func() { _ = conn.Close() }()
	var engine string
	if err := conn.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&engine); err != nil {
		return sqliteEngineRefusal{}, false, err
	}
	for _, file := range files {
		_, err := conn.ExecContext(ctx, file.sql)
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), sqliteRowidAliasRefusal) {
			return sqliteEngineRefusal{}, false, nil
		}
		return sqliteEngineRefusal{engine: engine, file: file.name, reason: oneLine(err.Error())}, true, nil
	}
	return sqliteEngineRefusal{}, false, nil
}

// sqliteEngineShared reports the modernc.org/sqlite version bin links, and
// whether it is the version this module's build list selects, which is the
// engine replaySQLiteMigrations runs on. A replaced module on either side, a
// binary without a build record, or one that links no such module is not
// shared.
func sqliteEngineShared(bin string) (string, bool) {
	harness, err := harnessSQLiteModule()
	if err != nil {
		return "", false
	}
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return "", false
	}
	for _, dep := range info.Deps {
		if dep.Path == sqliteDriverModule {
			return dep.Version, dep.Replace == nil && dep.Version != "" && dep.Version == harness
		}
	}
	return "", false
}

// harnessSQLiteModule asks the go command which modernc.org/sqlite version
// this module links. It does not read the running binary's build record: a
// test binary built by Go 1.26 records no dependencies at all.
var harnessSQLiteModule = sync.OnceValues(func() (string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", sqliteDriverModule)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading the linked %s module: %w", sqliteDriverModule, err)
	}
	var listed struct {
		Version string
		Replace *struct{}
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return "", fmt.Errorf("decoding the %s module list: %w", sqliteDriverModule, err)
	}
	if listed.Replace != nil || listed.Version == "" {
		return "", fmt.Errorf("%s is replaced or has no version in the build list", sqliteDriverModule)
	}
	return listed.Version, nil
})
