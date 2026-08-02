package probe

import (
	"os"
	"path/filepath"
	"strings"

	"go.5x5.cz/ptah/atlascompat"
	"go.5x5.cz/ptah/migration/migrator"
)

// AtlasCLIHiddenRuntimeProbe measures Atlas-compatible hidden CLI behavior that
// cannot be covered by help-output flag probes.
type AtlasCLIHiddenRuntimeProbe struct{}

func (AtlasCLIHiddenRuntimeProbe) Name() string { return "atlas-cli-hidden-runtime" }

func (AtlasCLIHiddenRuntimeProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-cli-hidden-runtime", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe hidden Atlas runtime flags: " + oneLine(err.Error()), ""}}
	}
	return []Result{runAtlasMigrateDiffHiddenDryRun(bin)}
}

func runAtlasMigrateDiffHiddenDryRun(bin string) Result {
	const fixture = "atlas migrate diff --dry-run"

	present, _, err := commandFlags(bin, []string{"migrate", "diff"})
	if err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "help", Fail,
			"reading `atlas migrate diff --help` failed: " + oneLine(err.Error()), ""}
	}
	if present["--dry-run"] {
		return Result{"atlas-cli-hidden-runtime", fixture, "help", Gap,
			"`atlas migrate diff --dry-run` is visible in help, but Atlas OSS registers it as hidden", "stokaro/ptah#618"}
	}

	dir, err := os.MkdirTemp("", "atlas-migrate-diff-dry-run-*")
	if err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "setup", Fail,
			"creating temp dry-run directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	migrationsDir := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "setup", Fail,
			"creating migration directory failed: " + oneLine(err.Error()), ""}
	}
	if err := os.WriteFile(filepath.Join(migrationsDir, "1_init.sql"), []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "setup", Fail,
			"writing initial migration failed: " + oneLine(err.Error()), ""}
	}
	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL DEFAULT '');\n"), 0o600); err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "setup", Fail,
			"writing desired schema failed: " + oneLine(err.Error()), ""}
	}

	sum, err := atlascompat.ComputeSum(os.DirFS(migrationsDir), migrator.MigrationDirFormatAtlas)
	if err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "setup", Fail,
			"computing baseline atlas.sum failed: " + oneLine(err.Error()), ""}
	}
	sumPath := filepath.Join(migrationsDir, "atlas.sum")
	beforeSum := sum.Bytes()
	if err := os.WriteFile(sumPath, beforeSum, 0o600); err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "setup", Fail,
			"writing baseline atlas.sum failed: " + oneLine(err.Error()), ""}
	}

	output, err := commandOutputDir(bin, []string{
		"migrate", "diff",
		"--dev-url", "sqlite://" + filepath.Join(dir, "dev.db"),
		"--dir", "file://" + migrationsDir,
		"--to", "file://" + schemaPath,
		"--dry-run",
		"add_email",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "execute", Gap,
			"`atlas migrate diff --dry-run` exited non-zero: " + oneLine(output), "stokaro/ptah#618"}
	}

	if !strings.Contains(output, "ALTER TABLE") || !strings.Contains(output, "email") {
		return Result{"atlas-cli-hidden-runtime", fixture, "execute", Gap,
			"`atlas migrate diff --dry-run` did not print generated SQL: " + oneLine(output), "stokaro/ptah#618"}
	}
	if strings.Contains(output, "Created migration file:") {
		return Result{"atlas-cli-hidden-runtime", fixture, "execute", Gap,
			"`atlas migrate diff --dry-run` printed file-write status: " + oneLine(output), "stokaro/ptah#618"}
	}
	if migrationCount, err := atlasDryRunMigrationFileCount(migrationsDir); err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "files", Fail,
			"reading migration directory after dry-run failed: " + oneLine(err.Error()), ""}
	} else if migrationCount != 1 {
		return Result{"atlas-cli-hidden-runtime", fixture, "files", Gap,
			"`atlas migrate diff --dry-run` wrote a migration file", "stokaro/ptah#618"}
	}
	afterSum, err := os.ReadFile(sumPath)
	if err != nil {
		return Result{"atlas-cli-hidden-runtime", fixture, "files", Fail,
			"reading atlas.sum after dry-run failed: " + oneLine(err.Error()), ""}
	}
	if string(afterSum) != string(beforeSum) {
		return Result{"atlas-cli-hidden-runtime", fixture, "files", Gap,
			"`atlas migrate diff --dry-run` rewrote atlas.sum", "stokaro/ptah#618"}
	}
	if _, err := os.Stat(filepath.Join(migrationsDir, ".ptah-migrate-diff.lock")); !os.IsNotExist(err) {
		return Result{"atlas-cli-hidden-runtime", fixture, "files", Gap,
			"`atlas migrate diff --dry-run` left the migration directory lock behind", "stokaro/ptah#618"}
	}
	return Result{"atlas-cli-hidden-runtime", fixture, "execute", OK,
		"`atlas migrate diff --dry-run` is hidden from help, prints SQL, and does not write a migration file or rewrite atlas.sum", ""}
}

func atlasDryRunMigrationFileCount(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			count++
		}
	}
	return count, nil
}
