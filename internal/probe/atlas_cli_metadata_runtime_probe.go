package probe

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AtlasCLIMetadataRuntimeProbe checks the behavior behind Atlas migration
// metadata flags and defaults. The flag-surface probe proves the flags are
// advertised; this probe proves Ptah's Atlas adapter uses Atlas migration
// directory semantics by default instead of silently falling back to Ptah's
// native paired up/down layout.
type AtlasCLIMetadataRuntimeProbe struct{}

func (AtlasCLIMetadataRuntimeProbe) Name() string { return "atlas-cli-metadata-runtime" }

func (AtlasCLIMetadataRuntimeProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahBinary()
	if err != nil {
		return []Result{{"atlas-cli-metadata-runtime", atlasCLISentinel, "build", Fail,
			"could not build the Ptah CLI to probe Atlas metadata runtime: " + oneLine(err.Error()), ""}}
	}

	checks := []func(string) Result{
		atlasMigrateNewDefaultsToAtlasDir,
		atlasMigrateHashDefaultsToAtlasSum,
		atlasMigrateStatusDefaultsToAtlasDir,
		atlasMigrateApplyAcceptsRevisionsSchema,
		atlasMigrateStatusAcceptsRevisionsSchema,
		atlasMigrateSetAcceptsRevisionsSchema,
		atlasMigrateApplyRejectsDirFormat,
	}

	out := make([]Result, 0, len(checks)+6)
	for _, check := range checks {
		out = append(out, check(bin))
	}
	out = append(out, atlasMigrateRejectsUnsupportedMetadataDirFormats(bin)...)
	return out
}

func atlasMigrateNewDefaultsToAtlasDir(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate new", "setup", err)
	}
	defer cleanup()

	output, err := commandOutput(bin, []string{"atlas", "migrate", "new", "init", "--dir", fileURL(migrations)})
	if err != nil {
		return atlasMetadataRuntimeExit("ptah atlas migrate new", "execute", output, err)
	}
	if ok, detail := atlasMigrationDirLooksNativeAtlas(root, migrations); !ok {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate new", "files", Gap, detail, "stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate new", "execute", OK,
		"`ptah atlas migrate new` defaults to Atlas single-file migrations and writes atlas.sum", ""}
}

func atlasMigrateHashDefaultsToAtlasSum(bin string) Result {
	_, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate hash", "setup", err)
	}
	defer cleanup()
	if err := writeAtlasMigration(migrations); err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate hash", "setup", err)
	}

	output, err := commandOutput(bin, []string{"atlas", "migrate", "hash", "--dir", fileURL(migrations)})
	if err != nil {
		return atlasMetadataRuntimeExit("ptah atlas migrate hash", "execute", output, err)
	}
	sum, err := os.ReadFile(filepath.Join(migrations, "atlas.sum"))
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate hash", "files", err)
	}
	if !strings.Contains(string(sum), "20240101000000_init.sql") {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate hash", "files", Gap,
			"`ptah atlas migrate hash` did not write an atlas.sum entry for the Atlas migration file", "stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate hash", "execute", OK,
		"`ptah atlas migrate hash` defaults to Atlas directory format and writes atlas.sum", ""}
}

func atlasMigrateStatusDefaultsToAtlasDir(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate status", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "ptah atlas migrate status"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "status.db")
	output, err := commandOutput(bin, []string{"atlas", "migrate", "status", "--url", dbURL, "--dir", fileURL(migrations)})
	if err != nil {
		return atlasMetadataRuntimeExit("ptah atlas migrate status", "execute", output, err)
	}
	if !strings.Contains(output, "Total Migrations: 1") || !strings.Contains(output, "Pending Migrations: 1") {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate status", "execute", Gap,
			"`ptah atlas migrate status` did not report the Atlas migration directory as one pending migration: " + oneLine(output),
			"stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate status", "execute", OK,
		"`ptah atlas migrate status` defaults to Atlas directory format and reads atlas.sum-backed migrations", ""}
}

func atlasMigrateApplyAcceptsRevisionsSchema(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate apply --revisions-schema", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "ptah atlas migrate apply --revisions-schema"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "apply-schema.db")
	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", "custom_meta",
	})
	if err == nil {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate apply --revisions-schema", "execute", OK,
			"`ptah atlas migrate apply --revisions-schema` executed successfully", ""}
	}
	if outputReachedRevisionSchemaPath(output) {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate apply --revisions-schema", "execute", OK,
			"`ptah atlas migrate apply --revisions-schema` is accepted and reaches the revision-schema execution path", ""}
	}
	return atlasMetadataRuntimeExit("ptah atlas migrate apply --revisions-schema", "execute", output, err)
}

func atlasMigrateStatusAcceptsRevisionsSchema(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate status --revisions-schema", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "ptah atlas migrate status --revisions-schema"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "status-schema.db")
	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "status",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", "custom_meta",
	})
	if err == nil {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate status --revisions-schema", "execute", OK,
			"`ptah atlas migrate status --revisions-schema` executed successfully", ""}
	}
	if outputReachedRevisionSchemaPath(output) {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate status --revisions-schema", "execute", OK,
			"`ptah atlas migrate status --revisions-schema` is accepted and reaches the revision-schema execution path", ""}
	}
	return atlasMetadataRuntimeExit("ptah atlas migrate status --revisions-schema", "execute", output, err)
}

func atlasMigrateSetAcceptsRevisionsSchema(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("ptah atlas migrate set --revisions-schema", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "ptah atlas migrate set --revisions-schema"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "set-schema.db")
	output, err := commandOutput(bin, []string{
		"atlas", "migrate", "set",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", "custom_meta",
		"--version", "20240101000000",
	})
	if err == nil {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate set --revisions-schema", "execute", OK,
			"`ptah atlas migrate set --revisions-schema` executed successfully", ""}
	}
	if outputReachedRevisionSchemaPath(output) {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate set --revisions-schema", "execute", OK,
			"`ptah atlas migrate set --revisions-schema` is accepted and reaches the revision-schema execution path", ""}
	}
	return atlasMetadataRuntimeExit("ptah atlas migrate set --revisions-schema", "execute", output, err)
}

func atlasMigrateRejectsUnsupportedMetadataDirFormats(bin string) []Result {
	checks := []struct {
		fixture string
		args    []string
	}{
		{
			fixture: "ptah atlas migrate hash --dir-format goose",
			args:    []string{"atlas", "migrate", "hash"},
		},
		{
			fixture: "ptah atlas migrate lint --dir-format goose",
			args:    []string{"atlas", "migrate", "lint", "--latest", "1"},
		},
		{
			fixture: "ptah atlas migrate new --dir-format goose",
			args:    []string{"atlas", "migrate", "new", "init"},
		},
		{
			fixture: "ptah atlas migrate set --dir-format goose",
			args:    []string{"atlas", "migrate", "set", "--url", "sqlite://ignored.db"},
		},
		{
			fixture: "ptah atlas migrate status --dir-format goose",
			args:    []string{"atlas", "migrate", "status", "--url", "sqlite://ignored.db"},
		},
		{
			fixture: "ptah atlas migrate validate --dir-format goose",
			args:    []string{"atlas", "migrate", "validate"},
		},
	}

	out := make([]Result, 0, len(checks))
	for _, check := range checks {
		out = append(out, atlasMigrateRejectsUnsupportedMetadataDirFormat(bin, check.fixture, check.args))
	}
	return out
}

func atlasMigrateRejectsUnsupportedMetadataDirFormat(bin, fixture string, prefix []string) Result {
	_, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if err := writeAtlasMigration(migrations); err != nil {
		return atlasMetadataRuntimeFail(fixture, "setup", err)
	}

	args := append([]string{}, prefix...)
	args = append(args, "--dir", fileURL(migrations), "--dir-format", "goose")
	output, err := commandOutput(bin, args)
	if err == nil {
		return Result{"atlas-cli-metadata-runtime", fixture, "execute", Gap,
			"`" + fixture + "` succeeded, but Ptah does not implement external Atlas migration formats yet",
			"stokaro/ptah#622"}
	}
	if !strings.Contains(output, "does not implement that directory format yet") {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`" + fixture + "` fails explicitly instead of treating external-format files as Atlas files", ""}
}

func atlasMigrateApplyRejectsDirFormat(bin string) Result {
	output, err := commandOutput(bin, []string{"atlas", "migrate", "apply", "--dir-format", "atlas", "--help"})
	if err == nil {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate apply --dir-format", "flags", Gap,
			"`ptah atlas migrate apply` accepts --dir-format, but Atlas OSS does not register that flag on apply",
			"stokaro/ptah#622"}
	}
	if strings.Contains(output, "unknown flag") {
		return Result{"atlas-cli-metadata-runtime", "ptah atlas migrate apply --dir-format", "flags", OK,
			"`ptah atlas migrate apply` rejects --dir-format, matching Atlas OSS flag surface", ""}
	}
	return atlasMetadataRuntimeExit("ptah atlas migrate apply --dir-format", "flags", output, err)
}

func atlasMetadataRuntimeDir() (string, string, func(), error) {
	root, err := os.MkdirTemp("", "atlas-metadata-runtime-*")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	migrations := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		cleanup()
		return "", "", nil, err
	}
	return root, migrations, cleanup, nil
}

func prepareAtlasMetadataMigration(bin, migrations, fixture string) *Result {
	if err := writeAtlasMigration(migrations); err != nil {
		result := atlasMetadataRuntimeFail(fixture, "setup", err)
		return &result
	}
	output, err := commandOutput(bin, []string{"atlas", "migrate", "hash", "--dir", fileURL(migrations)})
	if err != nil {
		result := atlasMetadataRuntimeExit(fixture, "setup", output, err)
		return &result
	}
	return nil
}

func outputReachedRevisionSchemaPath(output string) bool {
	if strings.Contains(output, "unknown flag") || strings.Contains(output, "unknown shorthand") {
		return false
	}
	return strings.Contains(output, "failed to create migrations schema") || strings.Contains(output, `near "SCHEMA"`)
}

func writeAtlasMigration(migrations string) error {
	return os.WriteFile(
		filepath.Join(migrations, "20240101000000_init.sql"),
		[]byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"),
		0o600,
	)
}

func atlasMigrationDirLooksNativeAtlas(root, migrations string) (bool, string) {
	files, err := os.ReadDir(migrations)
	if err != nil {
		return false, "reading generated migration directory failed: " + oneLine(err.Error())
	}

	var sqlFiles int
	var sumFiles int
	for _, file := range files {
		name := file.Name()
		switch {
		case strings.HasSuffix(name, ".up.sql") || strings.HasSuffix(name, ".down.sql"):
			return false, "`ptah atlas migrate new` generated Ptah paired migration file " + name
		case strings.HasSuffix(name, "_init.sql"):
			sqlFiles++
		case name == "atlas.sum":
			sumFiles++
		}
	}
	if sqlFiles != 1 {
		return false, "`ptah atlas migrate new` generated unexpected SQL files under " + root
	}
	if sumFiles != 1 {
		return false, "`ptah atlas migrate new` did not generate atlas.sum under " + root
	}
	return true, ""
}

func fileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func atlasMetadataRuntimeFail(fixture, stage string, err error) Result {
	return Result{"atlas-cli-metadata-runtime", fixture, stage, Fail, err.Error(), ""}
}

func atlasMetadataRuntimeExit(fixture, stage, output string, err error) Result {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return Result{"atlas-cli-metadata-runtime", fixture, stage, Gap, oneLine(output), "stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, stage, Fail, err.Error(), ""}
}
