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
	bin, err := ptahBinary()
	if err != nil {
		return []Result{{"atlas-cli-shorthands", atlasCLISentinel, "build", Fail,
			"could not build the Ptah CLI to probe Atlas shorthand aliases: " + oneLine(err.Error()), ""}}
	}
	return []Result{
		runAtlasVisibleShorthand(bin, "ptah atlas schema inspect -s", []string{"atlas", "schema", "inspect", "-s", "public"}, "--url is required"),
		runAtlasSchemaApplySchemaShorthand(bin),
		runAtlasSchemaApplyHiddenFileShorthand(bin),
		runAtlasSchemaDiffFromShorthand(bin),
		runAtlasSchemaDiffSchemaShorthand(bin),
		runAtlasVisibleShorthand(bin, "ptah atlas migrate diff -s", []string{
			"atlas", "migrate", "diff",
			"-s", "public",
			"--to", "file://schema.sql",
			"--dev-url", "docker://postgres/15/dev",
		}, "accepts docker --dev-url values"),
	}
}

func runAtlasVisibleShorthand(bin, fixture string, args []string, want string) Result {
	output, err := commandOutputDir(bin, args, "")
	if err == nil {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			"`" + strings.Join(append([]string{"ptah"}, args...), " ") + "` parsed successfully", ""}
	}
	if strings.Contains(output, want) {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			"`" + strings.Join(append([]string{"ptah"}, args...), " ") + "` reached the expected command validation path", ""}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
		"`" + strings.Join(append([]string{"ptah"}, args...), " ") + "` did not reach the expected validation path: " + oneLine(output), "stokaro/ptah#621"}
}

func runAtlasSchemaApplySchemaShorthand(bin string) Result {
	const fixture = "ptah atlas schema apply -s"
	output, err := commandOutputDir(bin, []string{
		"atlas", "schema", "apply",
		"--url", "sqlite://schema.db",
		"--to", "file://schema.sql",
		"-s", "public",
		"--dry-run",
	}, "")
	if err == nil || strings.Contains(output, "accepts --schema") {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			"`ptah atlas schema apply -s` reaches the same --schema validation path as the long flag", ""}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
		"`ptah atlas schema apply -s` did not reach the expected --schema validation path: " + oneLine(output), "stokaro/ptah#621"}
}

func runAtlasSchemaApplyHiddenFileShorthand(bin string) Result {
	const fixture = "ptah atlas schema apply --file/-f"

	present, err := commandFlags(bin, []string{"atlas", "schema", "apply"})
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "help", Fail,
			"reading `ptah atlas schema apply --help` failed: " + oneLine(err.Error()), ""}
	}
	if present["--file"] {
		return Result{"atlas-cli-shorthands", fixture, "help", Gap,
			"`ptah atlas schema apply --file` is visible in help, but Atlas OSS registers it as hidden", "stokaro/ptah#621"}
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
		"atlas", "schema", "apply",
		"--url", "sqlite://" + filepath.Join(dir, "apply.db"),
		"-f", schemaPath,
		"--dry-run",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`ptah atlas schema apply -f` exited non-zero: " + oneLine(output), "stokaro/ptah#621"}
	}
	if !strings.Contains(output, "Planned schema changes:") || !strings.Contains(output, "CREATE TABLE") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`ptah atlas schema apply -f` did not print the expected dry-run plan: " + oneLine(output), "stokaro/ptah#621"}
	}
	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`ptah atlas schema apply --file/-f` is hidden from help and maps to the local desired-schema input path", ""}
}

func runAtlasSchemaDiffFromShorthand(bin string) Result {
	const fixture = "ptah atlas schema diff -f"

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
		"atlas", "schema", "diff",
		"-f", "file://" + fromPath,
		"--to", "file://" + toPath,
		"--dev-url", "sqlite://" + filepath.Join(dir, "dev.db"),
	}, dir)
	if err != nil {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`ptah atlas schema diff -f` exited non-zero: " + oneLine(output), "stokaro/ptah#621"}
	}
	if !strings.Contains(output, "ALTER TABLE") || !strings.Contains(output, "email") {
		return Result{"atlas-cli-shorthands", fixture, "execute", Gap,
			"`ptah atlas schema diff -f` did not produce the expected migration SQL: " + oneLine(output), "stokaro/ptah#621"}
	}
	return Result{"atlas-cli-shorthands", fixture, "execute", OK,
		"`ptah atlas schema diff -f` behaves like `--from` for local schema-file diffs", ""}
}

func runAtlasSchemaDiffSchemaShorthand(bin string) Result {
	const fixture = "ptah atlas schema diff -s"
	output, err := commandOutputDir(bin, []string{
		"atlas", "schema", "diff",
		"-f", "file://from.sql",
		"--to", "file://schema.sql",
		"--dev-url", "sqlite://dev.db",
		"-s", "public",
	}, "")
	if err == nil || strings.Contains(output, "accepts --schema") {
		return Result{"atlas-cli-shorthands", fixture, "parse", OK,
			"`ptah atlas schema diff -s` reaches the same --schema validation path as the long flag", ""}
	}
	return Result{"atlas-cli-shorthands", fixture, "parse", Gap,
		"`ptah atlas schema diff -s` did not reach the expected --schema validation path: " + oneLine(output), "stokaro/ptah#621"}
}
