package probe

import (
	"os"
	"path/filepath"
	"strings"
)

// AtlasCLISchemaCleanRuntimeProbe measures non-trivial `atlas schema clean`
// behavior that help-output flag checks cannot prove.
type AtlasCLISchemaCleanRuntimeProbe struct{}

func (AtlasCLISchemaCleanRuntimeProbe) Name() string { return "atlas-cli-schema-clean-runtime" }

func (AtlasCLISchemaCleanRuntimeProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahBinary()
	if err != nil {
		return []Result{{"atlas-cli-schema-clean-runtime", atlasCLISentinel, "build", Fail,
			"could not build the Ptah CLI to probe schema clean runtime: " + oneLine(err.Error()), ""}}
	}
	return []Result{
		runAtlasSchemaCleanFormatDryRun(bin),
		runAtlasSchemaCleanFormatApply(bin),
		runAtlasSchemaCleanInvalidFormatBeforeConnect(bin),
		runAtlasSchemaCleanActualInvalidFormatBeforeApply(bin),
	}
}

func runAtlasSchemaCleanFormatDryRun(bin string) Result {
	const fixture = "ptah atlas schema clean --dry-run --format"

	dir, dbPath, setupErr := createAtlasSchemaCleanSQLiteFixture(bin, "format")
	if setupErr != nil {
		return setupErr.result(fixture)
	}
	defer os.RemoveAll(dir)

	output, err := commandOutputDir(bin, []string{
		"atlas", "schema", "clean",
		"--url", "sqlite://" + dbPath + "?password=hidden",
		"--dry-run",
		"--format", "{{ json . }}",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", Gap,
			"`ptah atlas schema clean --dry-run --format` exited non-zero: " + oneLine(output), "stokaro/ptah#629"}
	}

	required := []string{
		`"Env"`,
		`"Driver":"sqlite"`,
		"password=xxxxx",
		`"DryRun":true`,
		`"Applied":false`,
		`"Objects"`,
		`"Changes"`,
		"users",
		`DROP TABLE IF EXISTS \"users\"`,
	}
	for _, want := range required {
		if !strings.Contains(output, want) {
			return Result{"atlas-cli-schema-clean-runtime", fixture, "format", Gap,
				"`ptah atlas schema clean --dry-run --format '{{ json . }}'` output is missing " + want + ": " + oneLine(output),
				"stokaro/ptah#629"}
		}
	}

	inspectOutput, err := commandOutputDir(bin, []string{
		"atlas", "schema", "inspect",
		"--url", "sqlite://" + dbPath,
		"--format", "{{ json . }}",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "inspect", Gap,
			"`ptah atlas schema inspect` failed after schema clean dry-run: " + oneLine(inspectOutput), "stokaro/ptah#629"}
	}
	if !strings.Contains(inspectOutput, "users") {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "dry-run", Gap,
			"`ptah atlas schema clean --dry-run --format` mutated the SQLite fixture; users table disappeared", "stokaro/ptah#629"}
	}

	return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", OK,
		"`ptah atlas schema clean --dry-run --format '{{ json . }}'` emits structured redacted JSON and does not mutate SQLite", ""}
}

func runAtlasSchemaCleanInvalidFormatBeforeConnect(bin string) Result {
	const fixture = "ptah atlas schema clean invalid --format"

	dir, err := os.MkdirTemp("", "atlas-schema-clean-invalid-format-*")
	if err != nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "setup", Fail,
			"creating temp schema clean directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "should-not-exist.db")
	output, err := commandOutputDir(bin, []string{
		"atlas", "schema", "clean",
		"--url", "sqlite://" + dbPath,
		"--format", "{{ .DoesNotExist }}",
	}, dir)
	if err == nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", Gap,
			"`ptah atlas schema clean --format '{{ .DoesNotExist }}'` unexpectedly succeeded", "stokaro/ptah#629"}
	}
	if !strings.Contains(output, "execute --format template") {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", Gap,
			"`ptah atlas schema clean --format '{{ .DoesNotExist }}'` failed for the wrong reason: " + oneLine(output),
			"stokaro/ptah#629"}
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "connect", Gap,
			"`ptah atlas schema clean` created or touched the SQLite database before rejecting the invalid format", "stokaro/ptah#629"}
	}

	return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", OK,
		"`ptah atlas schema clean` rejects invalid format templates before opening the database", ""}
}

func runAtlasSchemaCleanFormatApply(bin string) Result {
	const fixture = "ptah atlas schema clean --format --auto-approve"

	dir, dbPath, setupErr := createAtlasSchemaCleanSQLiteFixture(bin, "clean-apply")
	if setupErr != nil {
		return setupErr.result(fixture)
	}
	defer os.RemoveAll(dir)

	output, err := commandOutputDir(bin, []string{
		"atlas", "schema", "clean",
		"--url", "sqlite://" + dbPath + "?password=hidden",
		"--format", "{{ json . }}",
		"--auto-approve",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", Gap,
			"`ptah atlas schema clean --format --auto-approve` exited non-zero: " + oneLine(output), "stokaro/ptah#629"}
	}

	required := []string{
		`"Env"`,
		`"Driver":"sqlite"`,
		"password=xxxxx",
		`"DryRun":false`,
		`"Applied":true`,
		"users",
		`DROP TABLE IF EXISTS \"users\"`,
	}
	for _, want := range required {
		if !strings.Contains(output, want) {
			return Result{"atlas-cli-schema-clean-runtime", fixture, "format", Gap,
				"`ptah atlas schema clean --format --auto-approve` output is missing " + want + ": " + oneLine(output),
				"stokaro/ptah#629"}
		}
	}

	inspectOutput, err := commandOutputDir(bin, []string{
		"atlas", "schema", "inspect",
		"--url", "sqlite://" + dbPath,
		"--format", "{{ json . }}",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "inspect", Gap,
			"`ptah atlas schema inspect` failed after schema clean apply: " + oneLine(inspectOutput), "stokaro/ptah#629"}
	}
	if strings.Contains(inspectOutput, "users") {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "apply", Gap,
			"`ptah atlas schema clean --format --auto-approve` reported applied but left the users table behind", "stokaro/ptah#629"}
	}

	return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", OK,
		"`ptah atlas schema clean --format --auto-approve` emits applied JSON and removes the SQLite table", ""}
}

func runAtlasSchemaCleanActualInvalidFormatBeforeApply(bin string) Result {
	const fixture = "ptah atlas schema clean actual invalid --format"

	dir, dbPath, setupErr := createAtlasSchemaCleanSQLiteFixture(bin, "actual-invalid")
	if setupErr != nil {
		return setupErr.result(fixture)
	}
	defer os.RemoveAll(dir)

	output, err := commandOutputDir(bin, []string{
		"atlas", "schema", "clean",
		"--url", "sqlite://" + dbPath,
		"--format", "{{ if .Applied }}{{ .DoesNotExist }}{{ end }}",
		"--auto-approve",
	}, dir)
	if err == nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", Gap,
			"`ptah atlas schema clean` unexpectedly accepted an applied-state invalid format", "stokaro/ptah#629"}
	}
	if !strings.Contains(output, "execute --format template") {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", Gap,
			"`ptah atlas schema clean` rejected the applied-state invalid format for the wrong reason: " + oneLine(output),
			"stokaro/ptah#629"}
	}

	inspectOutput, err := commandOutputDir(bin, []string{
		"atlas", "schema", "inspect",
		"--url", "sqlite://" + dbPath,
		"--format", "{{ json . }}",
	}, dir)
	if err != nil {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "inspect", Gap,
			"`ptah atlas schema inspect` failed after rejected actual format: " + oneLine(inspectOutput), "stokaro/ptah#629"}
	}
	if !strings.Contains(inspectOutput, "users") {
		return Result{"atlas-cli-schema-clean-runtime", fixture, "apply", Gap,
			"`ptah atlas schema clean` mutated the SQLite fixture before rejecting the applied-state invalid format", "stokaro/ptah#629"}
	}

	return Result{"atlas-cli-schema-clean-runtime", fixture, "execute", OK,
		"`ptah atlas schema clean` rejects applied-state invalid format templates before mutating SQLite", ""}
}

type schemaCleanSetupError struct {
	stage  string
	detail string
}

func (e schemaCleanSetupError) result(fixture string) Result {
	return Result{"atlas-cli-schema-clean-runtime", fixture, e.stage, Gap, e.detail, "stokaro/ptah#629"}
}

func createAtlasSchemaCleanSQLiteFixture(bin, name string) (string, string, *schemaCleanSetupError) {
	dir, err := os.MkdirTemp("", "atlas-schema-clean-"+name+"-*")
	if err != nil {
		return "", "", &schemaCleanSetupError{stage: "setup", detail: "creating temp schema clean directory failed: " + oneLine(err.Error())}
	}

	dbPath := filepath.Join(dir, "clean.db")
	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", "", &schemaCleanSetupError{stage: "setup", detail: "writing schema file failed: " + oneLine(err.Error())}
	}

	applyOutput, err := commandOutputDir(bin, []string{
		"atlas", "schema", "apply",
		"--url", "sqlite://" + dbPath,
		"--to", "file://" + schemaPath,
		"--auto-approve",
	}, dir)
	if err != nil {
		os.RemoveAll(dir)
		return "", "", &schemaCleanSetupError{
			stage:  "setup",
			detail: "`ptah atlas schema apply` could not create the SQLite fixture: " + oneLine(applyOutput),
		}
	}
	return dir, dbPath, nil
}
