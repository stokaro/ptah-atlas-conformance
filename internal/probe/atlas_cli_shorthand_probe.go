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
		runAtlasVisibleShorthand(bin, "atlas schema inspect -s", []string{"schema", "inspect", "-s", "public"}),
		runAtlasSchemaApplySchemaShorthand(bin),
		runAtlasSchemaApplyHiddenFileShorthand(bin),
		runAtlasSchemaDiffFromShorthand(bin),
		runAtlasSchemaDiffSchemaShorthand(bin),
		runAtlasVisibleShorthand(bin, "atlas migrate diff -s", []string{
			"migrate", "diff",
			"-s", "public",
			"--to", "file://schema.sql",
			"--dev-url", "docker://postgres/15/dev",
		}),
		runAtlasUnregisteredShorthandControl(bin),
	}
}

// refusesFlag reports cobra's rejection of a flag the command does not declare.
// It is emitted before the command runs anything, so its absence is what proves
// a flag exists. Both spellings are listed because the shorthand and long forms
// are worded differently and this probe drives argv, not a flag kind: measured
// byte-identical on the pinned Atlas CE binary and on ptah-compat for `-Z`.
func refusesFlag(output string) bool {
	for _, rejection := range []string{"unknown shorthand flag", "unknown flag"} {
		if strings.Contains(output, rejection) {
			return true
		}
	}
	return false
}

// syncedNoChanges is what a no-op plan prints. Measured on the pinned Atlas CE
// binary, which ends the sentence without a period; the transcription here
// carried one and reported a gap against output that already matched.
const syncedNoChanges = "Schema is synced, no changes to be made"

// runAtlasVisibleShorthand proves the command registers the shorthand under
// test. Cobra refuses an unregistered shorthand during flag parsing, so any
// invocation that gets past parsing -- succeeding, reporting a missing required
// flag, or failing to reach a dev database -- establishes that the shorthand is
// declared.
//
// The assertion is deliberately about parsing rather than about the wording of
// whatever fails next. An earlier version compared the output against a
// transcribed diagnostic, which measured the sentence Ptah happened to print at
// the time: `schema inspect -s` was recorded as wanting "--url is required"
// while both binaries now print cobra's own `required flag(s) "url" not set`,
// and `migrate diff -s` was recorded as wanting a phrase neither binary has
// ever printed. Both reported a gap for a shorthand that works.
func runAtlasVisibleShorthand(bin, fixture string, args []string) Result {
	spelling := "`" + strings.Join(append([]string{"atlas"}, args...), " ") + "`"
	output, err := commandOutputDirStrictCE(bin, args, "")
	if err == nil {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			spelling + " parsed successfully", ""}
	}
	if refusesFlag(output) {
		return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
			spelling + " rejected the shorthand during flag parsing: " + oneLine(output), "stokaro/ptah#621"}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", OK,
		spelling + " got past flag parsing, so the shorthand is registered", ""}
}

// runAtlasUnregisteredShorthandControl is the control for the assertion above.
// runAtlasVisibleShorthand passes on the ABSENCE of a rejection, which a
// command that stopped parsing flags altogether would also satisfy. This drives
// a shorthand no Atlas command declares and requires the rejection, so the
// detector is measured rather than assumed.
func runAtlasUnregisteredShorthandControl(bin string) Result {
	const fixture = "atlas schema inspect -Z (control)"
	args := []string{"schema", "inspect", "-Z", "public"}
	output, err := commandOutputDirStrictCE(bin, args, "")
	if err == nil {
		return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
			"`atlas schema inspect -Z` was accepted, so an unregistered shorthand is not refused: " + oneLine(output), "stokaro/ptah#621"}
	}
	if !refusesFlag(output) {
		return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
			"`atlas schema inspect -Z` failed without cobra's unregistered-shorthand rejection, so the control cannot police the probes above: " + oneLine(output), "stokaro/ptah#621"}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", OK,
		"`atlas schema inspect -Z` is refused as an unregistered shorthand, so the absence of that refusal is evidence", ""}
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

	shortOut, err := commandOutputDirStrictCE(bin, []string{
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

	longOut, err := commandOutputDirStrictCE(bin, []string{
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

	scopedOut, err := commandOutputDirStrictCE(bin, []string{
		"schema", "apply",
		"--url", targetURL,
		"--to", "file://" + schemaPath,
		"--dev-url", devURL,
		"-s", "out_of_scope",
		"--dry-run",
	}, dir)
	if err != nil || !strings.Contains(scopedOut, syncedNoChanges) {
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

	output, err := commandOutputDirStrictCE(bin, []string{
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

	output, err := commandOutputDirStrictCE(bin, []string{
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

	shortOut, err := commandOutputDirStrictCE(bin, diffArgs("-s", "main", "dev-short.db"), dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s main` exited non-zero: " + oneLine(shortOut), "stokaro/ptah#813"}
	}
	if !strings.Contains(shortOut, "ALTER TABLE") || !strings.Contains(shortOut, "email") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s main` did not produce the in-scope migration SQL: " + oneLine(shortOut), "stokaro/ptah#813"}
	}

	longOut, err := commandOutputDirStrictCE(bin, diffArgs("--schema", "main", "dev-long.db"), dir)
	if err != nil || longOut != shortOut {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s` output diverges from `--schema`: " + oneLine(longOut), "stokaro/ptah#813"}
	}

	scopedOut, err := commandOutputDirStrictCE(bin, diffArgs("-s", "out_of_scope", "dev-scoped.db"), dir)
	if err != nil || !strings.Contains(scopedOut, "Schemas are synced, no changes to be made.") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`atlas schema diff -s` with an out-of-scope schema name did not scope the diff away: " + oneLine(scopedOut), "stokaro/ptah#813"}
	}

	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`atlas schema diff -s` scopes like --schema: in-scope main yields the ALTER, output is identical to the long flag, and an out-of-scope schema name reports synced", ""}
}
