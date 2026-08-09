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
	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-cli-metadata-runtime", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe Atlas metadata runtime: " + oneLine(err.Error()), ""}}
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

	out := make([]Result, 0, len(checks)+4)
	for _, check := range checks {
		out = append(out, check(bin))
	}
	out = append(out, atlasMigrateSupportsGooseDirFormats(bin)...)
	return out
}

func atlasMigrateNewDefaultsToAtlasDir(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate new", "setup", err)
	}
	defer cleanup()

	output, err := commandOutput(bin, []string{"migrate", "new", "init", "--dir", fileURL(migrations)})
	if err != nil {
		return atlasMetadataRuntimeExit("atlas migrate new", "execute", output, err)
	}
	if ok, detail := atlasMigrationDirLooksNativeAtlas(root, migrations); !ok {
		return Result{"atlas-cli-metadata-runtime", "atlas migrate new", "files", Gap, detail, "stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", "atlas migrate new", "execute", OK,
		"`atlas migrate new` defaults to Atlas single-file migrations and writes atlas.sum", ""}
}

func atlasMigrateHashDefaultsToAtlasSum(bin string) Result {
	_, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate hash", "setup", err)
	}
	defer cleanup()
	if err := writeAtlasMigration(migrations); err != nil {
		return atlasMetadataRuntimeFail("atlas migrate hash", "setup", err)
	}

	output, err := commandOutput(bin, []string{"migrate", "hash", "--dir", fileURL(migrations)})
	if err != nil {
		return atlasMetadataRuntimeExit("atlas migrate hash", "execute", output, err)
	}
	sum, err := os.ReadFile(filepath.Join(migrations, "atlas.sum"))
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate hash", "files", err)
	}
	if !strings.Contains(string(sum), "20240101000000_init.sql") {
		return Result{"atlas-cli-metadata-runtime", "atlas migrate hash", "files", Gap,
			"`atlas migrate hash` did not write an atlas.sum entry for the Atlas migration file", "stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", "atlas migrate hash", "execute", OK,
		"`atlas migrate hash` defaults to Atlas directory format and writes atlas.sum", ""}
}

func atlasMigrateStatusDefaultsToAtlasDir(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate status", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "atlas migrate status"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "status.db")
	output, err := commandOutput(bin, []string{"migrate", "status", "--url", dbURL, "--dir", fileURL(migrations)})
	if err != nil {
		return atlasMetadataRuntimeExit("atlas migrate status", "execute", output, err)
	}
	if !strings.Contains(output, "-- Executed Files:  0") || !strings.Contains(output, "-- Pending Files:   1") {
		return Result{"atlas-cli-metadata-runtime", "atlas migrate status", "execute", Gap,
			"`atlas migrate status` did not report the Atlas migration directory as one pending migration: " + oneLine(output),
			"stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", "atlas migrate status", "execute", OK,
		"`atlas migrate status` defaults to Atlas directory format and reads atlas.sum-backed migrations", ""}
}

func atlasMigrateApplyAcceptsRevisionsSchema(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate apply --revisions-schema", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "atlas migrate apply --revisions-schema"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "apply-schema.db")
	output, err := commandOutput(bin, []string{
		"migrate", "apply",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return atlasMetadataRuntimeExit("atlas migrate apply --revisions-schema", "execute", output, err)
	}
	return Result{"atlas-cli-metadata-runtime", "atlas migrate apply --revisions-schema", "execute", OK,
		"`atlas migrate apply --revisions-schema main` executed successfully", ""}
}

func atlasMigrateStatusAcceptsRevisionsSchema(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate status --revisions-schema", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "atlas migrate status --revisions-schema"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "status-schema.db")
	output, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
	})
	if err != nil {
		return atlasMetadataRuntimeExit("atlas migrate status --revisions-schema", "execute", output, err)
	}
	return Result{"atlas-cli-metadata-runtime", "atlas migrate status --revisions-schema", "execute", OK,
		"`atlas migrate status --revisions-schema main` executed successfully", ""}
}

func atlasMigrateSetAcceptsRevisionsSchema(bin string) Result {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail("atlas migrate set --revisions-schema", "setup", err)
	}
	defer cleanup()
	if result := prepareAtlasMetadataMigration(bin, migrations, "atlas migrate set --revisions-schema"); result != nil {
		return *result
	}

	dbURL := "sqlite://" + filepath.Join(root, "set-schema.db")
	output, err := commandOutput(bin, []string{
		"migrate", "set",
		"--url", dbURL,
		"--dir", fileURL(migrations),
		"--revisions-schema", "main",
		"20240101000000",
	})
	if err != nil {
		return atlasMetadataRuntimeExit("atlas migrate set --revisions-schema", "execute", output, err)
	}
	return Result{"atlas-cli-metadata-runtime", "atlas migrate set --revisions-schema", "execute", OK,
		"`atlas migrate set --revisions-schema main` executed successfully", ""}
}

func atlasMigrateSupportsGooseDirFormats(bin string) []Result {
	return []Result{
		atlasMigrateLintSupportsGooseDirFormat(bin),
		atlasMigrateNewSupportsGooseDirFormat(bin),
		atlasMigrateSetSupportsGooseDirFormat(bin),
		atlasMigrateStatusSupportsGooseDirFormat(bin),
	}
}

func atlasMigrateLintSupportsGooseDirFormat(bin string) Result {
	const fixture = "atlas migrate lint --dir-format goose"

	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := prepareGooseMetadataMigration(bin, migrations, fixture); result != nil {
		return *result
	}

	output, err := commandOutput(bin, []string{
		"migrate", "lint",
		"--latest", "1",
		"--dir", fileURL(migrations),
		"--dir-format", "goose",
		"--dev-url", "sqlite://" + filepath.Join(root, "lint-dev.db"),
	})
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}
	if !strings.Contains(output, "no diagnostics found") || !strings.Contains(output, "1 version ok") {
		return Result{"atlas-cli-metadata-runtime", fixture, "execute", Gap,
			"`atlas migrate lint --dir-format goose` did not analyze the Goose migration: " + oneLine(output),
			"stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`atlas migrate lint --dir-format goose` hashed, loaded, replayed, and analyzed the Goose migration", ""}
}

func atlasMigrateNewSupportsGooseDirFormat(bin string) Result {
	const fixture = "atlas migrate new --dir-format goose"

	_, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()

	output, err := commandOutput(bin, []string{
		"migrate", "new", "init",
		"--dir", fileURL(migrations),
		"--dir-format", "goose",
	})
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}
	entries, err := os.ReadDir(migrations)
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "files", err)
	}
	var migrationPath string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_init.sql") {
			migrationPath = filepath.Join(migrations, entry.Name())
		}
	}
	if migrationPath == "" {
		return Result{"atlas-cli-metadata-runtime", fixture, "files", Gap,
			"`atlas migrate new --dir-format goose` did not write the Goose migration file", "stokaro/ptah#622"}
	}
	data, err := os.ReadFile(migrationPath)
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "files", err)
	}
	if string(data) != "-- +goose Up\n\n-- +goose Down\n" {
		return Result{"atlas-cli-metadata-runtime", fixture, "files", Gap,
			"`atlas migrate new --dir-format goose` wrote the wrong skeleton: " + oneLine(string(data)),
			"stokaro/ptah#622"}
	}
	if _, err := os.Stat(filepath.Join(migrations, "atlas.sum")); err != nil {
		return atlasMetadataRuntimeFail(fixture, "files", err)
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`atlas migrate new --dir-format goose` writes Atlas's Goose skeleton and refreshes atlas.sum", ""}
}

func atlasMigrateSetSupportsGooseDirFormat(bin string) Result {
	const fixture = "atlas migrate set --dir-format goose"

	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := prepareGooseMetadataMigration(bin, migrations, fixture); result != nil {
		return *result
	}

	output, err := commandOutput(bin, []string{
		"migrate", "set",
		"--url", "sqlite://" + filepath.Join(root, "set-goose.db"),
		atlasMetadataMigrationVersion,
		"--dir", fileURL(migrations),
		"--dir-format", "goose",
	})
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}
	if !strings.Contains(output, "Current version is "+atlasMetadataMigrationVersion) || !strings.Contains(output, "(init)") {
		return Result{"atlas-cli-metadata-runtime", fixture, "execute", Gap,
			"`atlas migrate set --dir-format goose` did not record the Goose revision: " + oneLine(output),
			"stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`atlas migrate set --dir-format goose` records the selected Goose revision with Atlas metadata", ""}
}

func atlasMigrateStatusSupportsGooseDirFormat(bin string) Result {
	const fixture = "atlas migrate status --dir-format goose"

	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		return atlasMetadataRuntimeFail(fixture, "setup", err)
	}
	defer cleanup()
	if result := prepareGooseMetadataMigration(bin, migrations, fixture); result != nil {
		return *result
	}

	output, err := commandOutput(bin, []string{
		"migrate", "status",
		"--url", "sqlite://" + filepath.Join(root, "status-goose.db"),
		"--dir", fileURL(migrations),
		"--dir-format", "goose",
	})
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}
	if !strings.Contains(output, "-- Next Version:    "+atlasMetadataMigrationVersion) ||
		!strings.Contains(output, "-- Pending Files:   1") {
		return Result{"atlas-cli-metadata-runtime", fixture, "execute", Gap,
			"`atlas migrate status --dir-format goose` did not report the Goose migration as pending: " + oneLine(output),
			"stokaro/ptah#622"}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`atlas migrate status --dir-format goose` reads the hashed Goose directory and reports one pending file", ""}
}

func atlasMigrateApplyRejectsDirFormat(bin string) Result {
	output, err := commandOutput(bin, []string{"migrate", "apply", "--dir-format", "atlas", "--help"})
	if err == nil {
		return Result{"atlas-cli-metadata-runtime", "atlas migrate apply --dir-format", "flags", Gap,
			"`atlas migrate apply` accepts --dir-format, but Atlas OSS does not register that flag on apply",
			"stokaro/ptah#622"}
	}
	if strings.Contains(output, "unknown flag") {
		return Result{"atlas-cli-metadata-runtime", "atlas migrate apply --dir-format", "flags", OK,
			"`atlas migrate apply` rejects --dir-format, matching Atlas OSS flag surface", ""}
	}
	return atlasMetadataRuntimeExit("atlas migrate apply --dir-format", "flags", output, err)
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
	output, err := commandOutput(bin, []string{"migrate", "hash", "--dir", fileURL(migrations)})
	if err != nil {
		result := atlasMetadataRuntimeExit(fixture, "setup", output, err)
		return &result
	}
	return nil
}

func prepareGooseMetadataMigration(bin, migrations, fixture string) *Result {
	if err := writeGooseMetadataMigration(migrations); err != nil {
		result := atlasMetadataRuntimeFail(fixture, "setup", err)
		return &result
	}
	output, err := commandOutput(bin, []string{
		"migrate", "hash",
		"--dir", fileURL(migrations),
		"--dir-format", "goose",
	})
	if err != nil {
		result := atlasMetadataRuntimeExit(fixture, "setup", output, err)
		return &result
	}
	return nil
}

const atlasMetadataMigrationVersion = "20240101000000"

func writeAtlasMigration(migrations string) error {
	return os.WriteFile(
		filepath.Join(migrations, atlasMetadataMigrationVersion+"_init.sql"),
		[]byte("CREATE TABLE users (id INTEGER PRIMARY KEY);\n"),
		0o600,
	)
}

func writeGooseMetadataMigration(migrations string) error {
	return os.WriteFile(
		filepath.Join(migrations, atlasMetadataMigrationVersion+"_init.sql"),
		[]byte("-- +goose Up\nCREATE TABLE users (id INTEGER PRIMARY KEY);\n\n-- +goose Down\nDROP TABLE users;\n"),
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
			return false, "`atlas migrate new` generated Ptah paired migration file " + name
		case strings.HasSuffix(name, "_init.sql"):
			sqlFiles++
		case name == "atlas.sum":
			sumFiles++
		}
	}
	if sqlFiles != 1 {
		return false, "`atlas migrate new` generated unexpected SQL files under " + root
	}
	if sumFiles != 1 {
		return false, "`atlas migrate new` did not generate atlas.sum under " + root
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
