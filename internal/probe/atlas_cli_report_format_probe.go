package probe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AtlasCLIReportFormatProbe measures Atlas-compatible Go-template/JSON report
// data shapes. Help and flag probes prove that --format exists; this runtime
// probe proves that commonly scripted report objects expose Atlas-shaped fields.
type AtlasCLIReportFormatProbe struct{}

func (AtlasCLIReportFormatProbe) Name() string { return "atlas-cli-report-format" }

func (AtlasCLIReportFormatProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-cli-report-format", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe Atlas report formats: " + oneLine(err.Error()), ""}}
	}

	fixture, cleanup, setupErr := createAtlasReportFormatFixture(bin)
	if setupErr != nil {
		return []Result{*setupErr}
	}
	defer cleanup()

	dryRun := runAtlasMigrateApplyDryRunReportShape(bin, fixture)
	applied := runAtlasMigrateApplyAppliedReportShape(bin, fixture)
	status := runAtlasMigrateStatusAppliedReportShape(bin, fixture)
	schemaClean := runAtlasSchemaCleanDryRunReportShape(bin)
	return []Result{dryRun, applied, status, schemaClean}
}

type atlasReportFormatFixture struct {
	dir    string
	dbURL  string
	dirURL string
}

func createAtlasReportFormatFixture(bin string) (atlasReportFormatFixture, func(), *Result) {
	dir, err := os.MkdirTemp("", "atlas-report-format-*")
	if err != nil {
		return atlasReportFormatFixture{}, func() {}, &Result{"atlas-cli-report-format", "atlas --format", "setup", Fail,
			"creating temp report-format directory failed: " + oneLine(err.Error()), ""}
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	migrations := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		cleanup()
		return atlasReportFormatFixture{}, func() {}, &Result{"atlas-cli-report-format", "atlas --format", "setup", Fail,
			"creating migration directory failed: " + oneLine(err.Error()), ""}
	}
	migrationPath := filepath.Join(migrations, "20240101000000_create_users.sql")
	if err := os.WriteFile(migrationPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		cleanup()
		return atlasReportFormatFixture{}, func() {}, &Result{"atlas-cli-report-format", "atlas --format", "setup", Fail,
			"writing Atlas migration fixture failed: " + oneLine(err.Error()), ""}
	}
	dirURL := fileURL(migrations)
	output, err := commandOutputDirStrictCE(bin, []string{"migrate", "hash", "--dir", dirURL}, dir)
	if err != nil {
		cleanup()
		return atlasReportFormatFixture{}, func() {}, &Result{"atlas-cli-report-format", "atlas migrate hash", "setup", Gap,
			"`atlas migrate hash` could not prepare atlas.sum for report-format probe: " + oneLine(output), "stokaro/ptah#631"}
	}
	return atlasReportFormatFixture{
		dir:    dir,
		dbURL:  "sqlite://" + filepath.Join(dir, "report.db") + "?password=hidden",
		dirURL: dirURL,
	}, cleanup, nil
}

func runAtlasMigrateApplyDryRunReportShape(bin string, fixture atlasReportFormatFixture) Result {
	const name = "atlas migrate apply --dry-run --format json"

	stdout, stderr, err := commandStreamsStrictCE(bin, []string{
		"migrate", "apply",
		"--url", fixture.dbURL,
		"--dir", fixture.dirURL,
		"--dry-run",
		"--format", "{{ json . }}",
	}, fixture.dir)
	if err != nil {
		return atlasReportFormatExit(name, stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog, "password=hidden"); detail != "" {
		return atlasReportFormatGap(name, "stderr", detail)
	}

	var report atlasMigrateApplyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return atlasReportFormatGap(name, "format", "dry-run apply did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := requireAtlasReportURL(report.Driver, report.URL); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	if len(report.Pending) != 1 {
		return atlasReportFormatGap(name, "format", fmt.Sprintf("dry-run apply Pending length = %d, want 1", len(report.Pending)))
	}
	if detail := requireAtlasMigrationFile(report.Pending[0]); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	if len(report.Applied) != 0 {
		return atlasReportFormatGap(name, "dry-run", "dry-run apply unexpectedly reported applied files")
	}
	if report.Message != "" {
		return atlasReportFormatGap(name, "format", "dry-run apply emitted a non-empty Message for pending migrations")
	}
	return Result{"atlas-cli-report-format", name, "format", OK,
		"`atlas migrate apply --dry-run --format '{{ json . }}'` exposes Atlas-shaped URL and pending migration fields", ""}
}

func runAtlasMigrateApplyAppliedReportShape(bin string, fixture atlasReportFormatFixture) Result {
	const name = "atlas migrate apply --format json"

	stdout, stderr, err := commandStreamsStrictCE(bin, []string{
		"migrate", "apply",
		"--url", fixture.dbURL,
		"--dir", fixture.dirURL,
		"--format", "{{ json . }}",
	}, fixture.dir)
	if err != nil {
		return atlasReportFormatExit(name, stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog, "password=hidden"); detail != "" {
		return atlasReportFormatGap(name, "stderr", detail)
	}

	var report atlasMigrateApplyReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return atlasReportFormatGap(name, "format", "apply did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := requireAtlasReportURL(report.Driver, report.URL); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	if len(report.Pending) != 1 {
		return atlasReportFormatGap(name, "format", fmt.Sprintf("apply Pending length = %d, want 1", len(report.Pending)))
	}
	if len(report.Applied) != 1 {
		return atlasReportFormatGap(name, "format", fmt.Sprintf("apply Applied length = %d, want 1", len(report.Applied)))
	}
	if detail := requireAtlasMigrationFile(report.Applied[0].atlasMigrationFileReport); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	applied := report.Applied[0]
	switch {
	case applied.Start == "":
		return atlasReportFormatGap(name, "format", "applied migration Start is empty")
	case applied.End == "":
		return atlasReportFormatGap(name, "format", "applied migration End is empty")
	case applied.Skipped == nil:
		return atlasReportFormatGap(name, "format", "applied migration Skipped field is missing")
	case *applied.Skipped != 0:
		return atlasReportFormatGap(name, "format", fmt.Sprintf("applied migration Skipped = %d, want 0", *applied.Skipped))
	case len(applied.Applied) != 1:
		return atlasReportFormatGap(name, "format", fmt.Sprintf("applied migration Applied statement count = %d, want 1", len(applied.Applied)))
	case string(applied.Checks) != "null":
		return atlasReportFormatGap(name, "format", "applied migration Checks is not present as JSON null")
	case string(applied.Error) != "null":
		return atlasReportFormatGap(name, "format", "applied migration Error is not present as JSON null")
	}
	if !strings.Contains(report.Message, "Migrated to version 20240101000000") {
		return atlasReportFormatGap(name, "format", "apply Message does not describe the applied target: "+oneLine(report.Message))
	}
	return Result{"atlas-cli-report-format", name, "format", OK,
		"`atlas migrate apply --format '{{ json . }}'` exposes Atlas-shaped applied migration fields, nulls, and zero values", ""}
}

func runAtlasMigrateStatusAppliedReportShape(bin string, fixture atlasReportFormatFixture) Result {
	const name = "atlas migrate status --format json"

	stdout, stderr, err := commandStreamsStrictCE(bin, []string{
		"migrate", "status",
		"--url", fixture.dbURL,
		"--dir", fixture.dirURL,
		"--format", "{{ json . }}",
	}, fixture.dir)
	if err != nil {
		return atlasReportFormatExit(name, stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog, "password=hidden"); detail != "" {
		return atlasReportFormatGap(name, "stderr", detail)
	}

	var report atlasMigrateStatusReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return atlasReportFormatGap(name, "format", "status did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := requireAtlasReportURL(report.Env.Driver, report.Env.URL); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	if len(report.Available) != 1 {
		return atlasReportFormatGap(name, "format", fmt.Sprintf("status Available length = %d, want 1", len(report.Available)))
	}
	if detail := requireAtlasMigrationFile(report.Available[0]); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	if len(report.Applied) != 1 {
		return atlasReportFormatGap(name, "format", fmt.Sprintf("status Applied length = %d, want 1", len(report.Applied)))
	}
	applied := report.Applied[0]
	switch {
	case applied.Version != "20240101000000":
		return atlasReportFormatGap(name, "format", "status applied revision Version mismatch: "+applied.Version)
	case applied.Description != "create_users":
		return atlasReportFormatGap(name, "format", "status applied revision Description mismatch: "+applied.Description)
	case applied.Type != "applied":
		return atlasReportFormatGap(name, "format", "status applied revision Type mismatch: "+applied.Type)
	case applied.Applied != 1:
		return atlasReportFormatGap(name, "format", fmt.Sprintf("status applied revision Applied = %d, want 1", applied.Applied))
	case applied.Total != 1:
		return atlasReportFormatGap(name, "format", fmt.Sprintf("status applied revision Total = %d, want 1", applied.Total))
	case applied.ExecutedAt == "":
		return atlasReportFormatGap(name, "format", "status applied revision ExecutedAt is empty")
	case applied.ExecutionTime == nil:
		return atlasReportFormatGap(name, "format", "status applied revision ExecutionTime field is missing")
	case applied.OperatorVersion == "":
		return atlasReportFormatGap(name, "format", "status applied revision OperatorVersion is empty")
	case report.Status != "OK":
		return atlasReportFormatGap(name, "format", "status report Status mismatch: "+report.Status)
	case report.Current != "20240101000000":
		return atlasReportFormatGap(name, "format", "status report Current mismatch: "+report.Current)
	}
	return Result{"atlas-cli-report-format", name, "format", OK,
		"`atlas migrate status --format '{{ json . }}'` exposes Atlas-shaped applied revision entries", ""}
}

func runAtlasSchemaCleanDryRunReportShape(bin string) Result {
	const name = "atlas schema clean --dry-run --format json"

	dir, dbPath, setupErr := createAtlasReportFormatSchemaCleanFixture(bin)
	if setupErr != nil {
		return *setupErr
	}
	defer func() { _ = os.RemoveAll(dir) }()

	stdout, stderr, err := commandStreams(bin, []string{
		"schema", "clean",
		"--url", "sqlite://" + dbPath + "?password=hidden",
		"--dry-run",
		"--format", "{{ json . }}",
	}, dir)
	if err != nil {
		return atlasReportFormatExit(name, stdout+stderr, err)
	}
	if detail := inspectInfoOnlyStderr(stderr, compatInfoLog, "password=hidden"); detail != "" {
		return atlasReportFormatGap(name, "stderr", detail)
	}

	var report atlasSchemaCleanReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return atlasReportFormatGap(name, "format", "schema clean did not emit valid JSON: "+oneLine(err.Error()))
	}
	if detail := requireAtlasReportURL(report.Env.Driver, report.Env.URL); detail != "" {
		return atlasReportFormatGap(name, "format", detail)
	}
	switch {
	case report.DryRun == nil:
		return atlasReportFormatGap(name, "format", "schema clean DryRun field is missing")
	case !*report.DryRun:
		return atlasReportFormatGap(name, "format", "schema clean DryRun = false, want true")
	case report.Applied == nil:
		return atlasReportFormatGap(name, "format", "schema clean Applied field is missing")
	case *report.Applied:
		return atlasReportFormatGap(name, "format", "schema clean Applied = true, want false for dry-run")
	case len(report.Objects) == 0:
		return atlasReportFormatGap(name, "format", "schema clean Objects is empty")
	case len(report.Changes) == 0:
		return atlasReportFormatGap(name, "format", "schema clean Changes is empty")
	}
	return Result{"atlas-cli-report-format", name, "format", OK,
		"`atlas schema clean --dry-run --format '{{ json . }}'` exposes Atlas-shaped URL and dry-run report fields", ""}
}

func createAtlasReportFormatSchemaCleanFixture(bin string) (string, string, *Result) {
	dir, err := os.MkdirTemp("", "atlas-report-format-clean-*")
	if err != nil {
		return "", "", &Result{"atlas-cli-report-format", "atlas schema clean --format", "setup", Fail,
			"creating temp schema clean directory failed: " + oneLine(err.Error()), ""}
	}

	dbPath := filepath.Join(dir, "clean.db")
	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", &Result{"atlas-cli-report-format", "atlas schema clean --format", "setup", Fail,
			"writing schema clean fixture failed: " + oneLine(err.Error()), ""}
	}

	output, err := commandOutputDirStrictCE(bin, []string{
		"schema", "apply",
		"--url", "sqlite://" + dbPath,
		"--to", "file://" + schemaPath,
		"--dev-url", "sqlite://" + filepath.Join(dir, "dev.db"),
		"--auto-approve",
	}, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", &Result{"atlas-cli-report-format", "atlas schema clean --format", "setup", Gap,
			"`atlas schema apply` could not create the schema clean fixture: " + oneLine(output), "stokaro/ptah#631"}
	}
	return dir, dbPath, nil
}

type atlasReportURL struct {
	Scheme   string
	RawQuery string
	Schema   string
}

type atlasMigrationFileReport struct {
	Name        string
	Version     string
	Description string
}

type atlasMigrateApplyReport struct {
	Driver  string
	URL     atlasReportURL
	Pending []atlasMigrationFileReport
	Applied []atlasMigrateApplyAppliedFile
	Message string
}

type atlasMigrateApplyAppliedFile struct {
	atlasMigrationFileReport
	Start   string
	End     string
	Skipped *int
	Applied []string
	Checks  json.RawMessage
	Error   json.RawMessage
}

type atlasMigrateStatusReport struct {
	Env       atlasReportEnv
	Available []atlasMigrationFileReport
	Applied   []atlasMigrateStatusAppliedRevision
	Current   string
	Status    string
}

type atlasReportEnv struct {
	Driver string
	URL    atlasReportURL
}

type atlasMigrateStatusAppliedRevision struct {
	Version         string
	Description     string
	Type            string
	Applied         int
	Total           int
	ExecutedAt      string
	ExecutionTime   *int64
	OperatorVersion string
}

type atlasSchemaCleanReport struct {
	Env     atlasReportEnv
	DryRun  *bool
	Applied *bool
	Objects []any
	Changes []any
}

func requireAtlasReportURL(driver string, reportURL atlasReportURL) string {
	switch {
	case driver != "sqlite":
		return "report Driver = " + driver + ", want sqlite"
	case reportURL.Scheme != "sqlite":
		return "report URL Scheme = " + reportURL.Scheme + ", want sqlite"
	case reportURL.Schema != "main":
		return "report URL Schema = " + reportURL.Schema + ", want main"
	case !strings.Contains(reportURL.RawQuery, "password=xxxxx"):
		return "report URL RawQuery is not redacted: " + reportURL.RawQuery
	}
	return ""
}

func requireAtlasMigrationFile(file atlasMigrationFileReport) string {
	switch {
	case file.Name != "20240101000000_create_users.sql":
		return "migration file Name mismatch: " + file.Name
	case file.Version != "20240101000000":
		return "migration file Version mismatch: " + file.Version
	case file.Description != "create_users":
		return "migration file Description mismatch: " + file.Description
	}
	return ""
}

func atlasReportFormatExit(fixture, output string, err error) Result {
	return Result{"atlas-cli-report-format", fixture, "execute", Gap,
		"`" + fixture + "` exited non-zero: " + oneLine(output) + " (" + oneLine(err.Error()) + ")", "stokaro/ptah#631"}
}

func atlasReportFormatGap(fixture, stage, detail string) Result {
	return Result{"atlas-cli-report-format", fixture, stage, Gap, detail, "stokaro/ptah#631"}
}
