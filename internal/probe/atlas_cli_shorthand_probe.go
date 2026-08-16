package probe

import (
	"os"
	"path/filepath"
	"strings"
)

// AtlasCLIShorthandProbe measures Atlas-compatible shorthand aliases that are
// observable CLI contracts but not fully covered by long-flag help probes.
type AtlasCLIShorthandProbe struct{}

func (AtlasCLIShorthandProbe) Name() string { return "atlas-cli-shorthands" }

func (AtlasCLIShorthandProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-cli-shorthands", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe Atlas shorthand aliases: " + oneLine(err.Error()), ""}}
	}
	return []Result{
		// The wording is the pinned community binary's own: `atlas schema inspect
		// -s public` with no --url answers `required flag(s) "url" not set`,
		// and Ptah answers the same. It used to answer `--url is required`,
		// which is what this row was pinned to.
		runAtlasVisibleShorthand(bin, "atlas schema inspect -s",
			[]string{"schema", "inspect", "-s", "public"}, `required flag(s) "url" not set`),
		runAtlasSchemaApplySchemaShorthand(bin),
		runAtlasSchemaApplyHiddenFileShorthand(bin),
		runAtlasSchemaDiffFromShorthand(bin),
		runAtlasSchemaDiffSchemaShorthand(bin),
		runAtlasShorthandAccepted(bin, "atlas migrate diff -s", []string{
			"migrate", "diff",
			"-s", "public",
			"--to", "file://schema.sql",
			"--dev-url", "docker://postgres/15/dev",
		}),
	}
}

// runAtlasShorthandAccepted proves a shorthand parses without pinning whatever
// the command fails on afterwards.
//
// It exists for `migrate diff -s`, which used to be measured by the refusal
// Ptah returned for a `docker://` dev URL. Ptah supports those now
// (stokaro/ptah#844), so the run travels further and stops on whatever the
// environment gives it next -- a missing `schema.sql` where the file is absent,
// a container runtime error where Docker is not reachable. Neither is a stable
// string to assert, and neither is what this row is about.
//
// What it is about is that `-s` is a working `--schema` alias, so that is what
// is asserted: the run either completes, or fails on something other than the
// flag itself. Cobra names an unusable shorthand explicitly, which is what
// makes the negative side of this reliable.
func runAtlasShorthandAccepted(bin, fixture string, args []string) Result {
	command := "`" + strings.Join(append([]string{"atlas"}, args...), " ") + "`"
	output, err := commandOutputDir(bin, args, "")
	if err == nil {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			command + " parsed successfully", ""}
	}
	for _, rejection := range []string{"unknown shorthand flag", "unknown flag"} {
		if strings.Contains(output, rejection) {
			return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
				command + " did not accept the shorthand: " + oneLine(output), "stokaro/ptah#621"}
		}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", OK,
		command + " accepted the shorthand and stopped later: " + oneLine(output), ""}
}

func runAtlasVisibleShorthand(bin, fixture string, args []string, want string) Result {
	output, err := commandOutputDir(bin, args, "")
	if err == nil {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			"`" + strings.Join(append([]string{"atlas"}, args...), " ") + "` parsed successfully", ""}
	}
	if strings.Contains(output, want) {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			"`" + strings.Join(append([]string{"atlas"}, args...), " ") + "` reached the expected command validation path", ""}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
		"`" + strings.Join(append([]string{"atlas"}, args...), " ") + "` did not reach the expected validation path: " + oneLine(output), "stokaro/ptah#621"}
}

// runAtlasSchemaApplySchemaShorthand proves `-s` is a working `--schema` alias
// on `schema apply` now that stokaro/ptah#813 implements schema scoping for
// local desired-state sources: an in-scope SQLite schema name plans the desired
// table, the shorthand output is byte-identical to the long flag's, and an
// out-of-scope schema name scopes the same desired state down to no changes.
func runAtlasSchemaApplySchemaShorthand(bin string) Result {
	const fixture = "atlas schema apply -s"

	dir, err := os.MkdirTemp("", "atlas-schema-apply-schema-shorthand-*")
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"creating temp schema-apply directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"writing desired schema failed: " + oneLine(err.Error()), ""}
	}
	targetURL := "sqlite://" + filepath.Join(dir, "apply.db")
	devURL := "sqlite://" + filepath.Join(dir, "dev.db")

	shortOut, err := commandOutputDir(bin, []string{
		"schema", "apply",
		"--url", targetURL,
		"--to", "file://" + schemaPath,
		"--dev-url", devURL,
		"-s", "main",
		"--dry-run",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema apply -s main` exited non-zero: " + oneLine(shortOut), "stokaro/ptah#813"}
	}
	if !strings.Contains(shortOut, "Planned schema changes:") || !strings.Contains(shortOut, "CREATE TABLE") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema apply -s main` did not plan the in-scope table: " + oneLine(shortOut), "stokaro/ptah#813"}
	}

	longOut, err := commandOutputDir(bin, []string{
		"schema", "apply",
		"--url", targetURL,
		"--to", "file://" + schemaPath,
		"--dev-url", devURL,
		"--schema", "main",
		"--dry-run",
	}, dir)
	if err != nil || longOut != shortOut {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema apply -s` output diverges from `--schema`: " + oneLine(longOut), "stokaro/ptah#813"}
	}

	scopedOut, err := commandOutputDir(bin, []string{
		"schema", "apply",
		"--url", targetURL,
		"--to", "file://" + schemaPath,
		"--dev-url", devURL,
		"-s", "out_of_scope",
		"--dry-run",
	}, dir)
	// No trailing period: the pinned community binary prints
	// `Schema is synced, no changes to be made` without one, and Ptah now
	// matches it. Asserting the period made this row red for punctuation while
	// reporting it as a scoping failure, which is the wrong thing to read.
	//
	// The plural `schema diff` message keeps its period in both, so the two are
	// not the same string and must not be normalized together.
	if err != nil || !strings.Contains(scopedOut, "Schema is synced, no changes to be made") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema apply -s` with an out-of-scope schema name did not scope the plan away: " + oneLine(scopedOut), "stokaro/ptah#813"}
	}

	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`atlas schema apply -s` scopes like --schema: in-scope main plans the table, output is identical to the long flag, and an out-of-scope schema name plans no changes", ""}
}

func runAtlasSchemaApplyHiddenFileShorthand(bin string) Result {
	const fixture = "atlas schema apply --file/-f"

	present, _, err := commandFlags(bin, []string{"schema", "apply"})
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "help", Fail,
			"reading `atlas schema apply --help` failed: " + oneLine(err.Error()), ""}
	}
	if present["--file"] {
		return Result{"atlas-cli-shorthands", fixture, "help", Gap,
			"`atlas schema apply --file` is visible in help, but Atlas OSS registers it as hidden", "stokaro/ptah#621"}
	}

	dir, err := os.MkdirTemp("", "atlas-schema-apply-file-shorthand-*")
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"creating temp schema-apply directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"writing desired schema failed: " + oneLine(err.Error()), ""}
	}

	output, err := commandOutputDir(bin, []string{
		"schema", "apply",
		"--url", "sqlite://" + filepath.Join(dir, "apply.db"),
		"-f", schemaPath,
		"--dev-url", "sqlite://" + filepath.Join(dir, "dev.db"),
		"--dry-run",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema apply -f` exited non-zero: " + oneLine(output), "stokaro/ptah#621"}
	}
	if !strings.Contains(output, "Planned schema changes:") || !strings.Contains(output, "CREATE TABLE") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema apply -f` did not print the expected dry-run plan: " + oneLine(output), "stokaro/ptah#621"}
	}
	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`atlas schema apply --file/-f` is hidden from help and maps to the local desired-schema input path", ""}
}

func runAtlasSchemaDiffFromShorthand(bin string) Result {
	const fixture = "atlas schema diff -f"

	dir, err := os.MkdirTemp("", "atlas-schema-diff-from-shorthand-*")
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"creating temp schema-diff directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	fromPath := filepath.Join(dir, "from.sql")
	toPath := filepath.Join(dir, "to.sql")
	if err := os.WriteFile(fromPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"writing current schema failed: " + oneLine(err.Error()), ""}
	}
	if err := os.WriteFile(toPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL DEFAULT '');\n"), 0o600); err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"writing desired schema failed: " + oneLine(err.Error()), ""}
	}

	output, err := commandOutputDir(bin, []string{
		"schema", "diff",
		"-f", "file://" + fromPath,
		"--to", "file://" + toPath,
		"--dev-url", "sqlite://" + filepath.Join(dir, "dev.db"),
	}, dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -f` exited non-zero: " + oneLine(output), "stokaro/ptah#621"}
	}
	if !strings.Contains(output, "ALTER TABLE") || !strings.Contains(output, "email") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -f` did not produce the expected migration SQL: " + oneLine(output), "stokaro/ptah#621"}
	}
	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`atlas schema diff -f` behaves like `--from` for local schema-file diffs", ""}
}

// runAtlasSchemaDiffSchemaShorthand proves `-s` is a working `--schema` alias
// on `schema diff` now that stokaro/ptah#813 implements schema scoping for
// local desired-state sources: the in-scope SQLite schema name diffs to the
// expected ALTER, the shorthand output is byte-identical to the long flag's,
// and an out-of-scope schema name reports the sources as synced.
func runAtlasSchemaDiffSchemaShorthand(bin string) Result {
	const fixture = "atlas schema diff -s"

	dir, err := os.MkdirTemp("", "atlas-schema-diff-schema-shorthand-*")
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"creating temp schema-diff directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	fromPath := filepath.Join(dir, "from.sql")
	toPath := filepath.Join(dir, "to.sql")
	if err := os.WriteFile(fromPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"writing current schema failed: " + oneLine(err.Error()), ""}
	}
	if err := os.WriteFile(toPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL DEFAULT '');\n"), 0o600); err != nil {
		return Result{"atlas-cli-shorthands", fixture, "setup", Fail,
			"writing desired schema failed: " + oneLine(err.Error()), ""}
	}
	diffArgs := func(schemaFlag, schemaName, devName string) []string {
		return []string{
			"schema", "diff",
			"-f", "file://" + fromPath,
			"--to", "file://" + toPath,
			"--dev-url", "sqlite://" + filepath.Join(dir, devName),
			schemaFlag, schemaName,
		}
	}

	shortOut, err := commandOutputDir(bin, diffArgs("-s", "main", "dev-short.db"), dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s main` exited non-zero: " + oneLine(shortOut), "stokaro/ptah#813"}
	}
	if !strings.Contains(shortOut, "ALTER TABLE") || !strings.Contains(shortOut, "email") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s main` did not produce the in-scope migration SQL: " + oneLine(shortOut), "stokaro/ptah#813"}
	}

	longOut, err := commandOutputDir(bin, diffArgs("--schema", "main", "dev-long.db"), dir)
	if err != nil || longOut != shortOut {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s` output diverges from `--schema`: " + oneLine(longOut), "stokaro/ptah#813"}
	}

	scopedOut, err := commandOutputDir(bin, diffArgs("-s", "out_of_scope", "dev-scoped.db"), dir)
	if err != nil || !strings.Contains(scopedOut, "Schemas are synced, no changes to be made.") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s` with an out-of-scope schema name did not scope the diff away: " + oneLine(scopedOut), "stokaro/ptah#813"}
	}

	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`atlas schema diff -s` scopes like --schema: in-scope main yields the ALTER, output is identical to the long flag, and an out-of-scope schema name reports synced", ""}
}
