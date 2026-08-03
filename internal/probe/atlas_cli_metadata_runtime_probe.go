package probe

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	out := make([]Result, 0, len(checks)+8)
	for _, check := range checks {
		out = append(out, check(bin))
	}
	out = append(out, atlasMigrateRejectsUnsupportedMetadataDirFormats(bin)...)
	out = append(out, atlasMigrateReadsConvertedMetadataDirs(bin)...)
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
	if !strings.Contains(output, "Total Migrations: 1") || !strings.Contains(output, "Pending Migrations: 1") {
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

// atlasMigrateRejectsUnsupportedMetadataDirFormats covers the verbs that still
// have no foreign-format implementation. Refusing explicitly is the acceptable
// shape here; silently reading foreign-format files as Atlas files is not.
// `migrate status` and `migrate set` used to be on this list and no longer are:
// stokaro/ptah#1002 gave them a real converted-directory reader, which
// atlasMigrateReadsConvertedMetadataDirs pins instead.
func atlasMigrateRejectsUnsupportedMetadataDirFormats(bin string) []Result {
	checks := []struct {
		fixture string
		args    []string
	}{
		{
			fixture: "atlas migrate lint --dir-format goose",
			args:    []string{"migrate", "lint", "--latest", "1"},
		},
		{
			fixture: "atlas migrate new --dir-format goose",
			args:    []string{"migrate", "new", "init"},
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

// metadataDirSpelling is one of the two ways an Atlas caller names the
// directory convention: the `--dir-format` flag, or a `format` query parameter
// on the `--dir` URL. The pinned Atlas CE binary honors both, so a verb that
// only understands one of them is not a drop-in.
type metadataDirSpelling struct {
	// label names the spelling the way it reads in a fixture title, e.g.
	// "--dir-format goose".
	label string
	args  func(migrations, format string) []string
}

func metadataDirSpellings() []metadataDirSpelling {
	return []metadataDirSpelling{
		{
			label: "--dir-format goose",
			args: func(migrations, format string) []string {
				return []string{"--dir", fileURL(migrations), "--dir-format", format}
			},
		},
		{
			label: "--dir ?format=goose",
			args: func(migrations, format string) []string {
				return []string{"--dir", fileURL(migrations) + "?format=" + url.QueryEscape(format)}
			},
		},
	}
}

// atlasMigrateReadsConvertedMetadataDirs pins stokaro/ptah#1002: `migrate
// status` and `migrate set` read a migration directory laid out in a foreign
// tool's convention instead of refusing the layout outright, through both
// spellings above. It also pins that the format value is taken verbatim —
// Atlas CE matches the format name case-sensitively and does not trim it, so
// accepting `Goose` or ` goose` would make Ptah quietly more permissive than
// the tool it stands in for.
//
// This tier has no Atlas binary, so it pins the capability and the value
// contract; the process-for-process comparison against the pinned community
// binary lives in the migrate-runtime tier, which does have one.
func atlasMigrateReadsConvertedMetadataDirs(bin string) []Result {
	out := make([]Result, 0, 6)
	for _, spelling := range metadataDirSpellings() {
		out = append(out,
			atlasMigrateStatusReadsConvertedDir(bin, spelling),
			atlasMigrateSetReadsConvertedDir(bin, spelling),
		)
	}
	return append(out, atlasMigrateTakesDirFormatVerbatim(bin)...)
}

func atlasMigrateStatusReadsConvertedDir(bin string, spelling metadataDirSpelling) Result {
	fixture := "atlas migrate status " + spelling.label
	root, migrations, cleanup, result := prepareGooseMetadataDir(bin, fixture, spelling)
	if result != nil {
		return *result
	}
	defer cleanup()

	args := append([]string{"migrate", "status", "--url", "sqlite://" + filepath.Join(root, "status.db")},
		spelling.args(migrations, "goose")...)
	output, err := commandOutput(bin, args)
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}
	if !strings.Contains(output, "Total Migrations: 2") || !strings.Contains(output, "Pending Migrations: 2") {
		return Result{"atlas-cli-metadata-runtime", fixture, "execute", Gap,
			"`" + fixture + "` did not read the Goose-format directory as two pending migrations: " + oneLine(output),
			"stokaro/ptah#1002"}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`" + fixture + "` reads a directory laid out in Goose's convention and reports its two migrations as pending", ""}
}

func atlasMigrateSetReadsConvertedDir(bin string, spelling metadataDirSpelling) Result {
	fixture := "atlas migrate set " + spelling.label
	root, migrations, cleanup, result := prepareGooseMetadataDir(bin, fixture, spelling)
	if result != nil {
		return *result
	}
	defer cleanup()

	dbURL := "sqlite://" + filepath.Join(root, "set.db")
	args := append([]string{"migrate", "set", "--url", dbURL}, spelling.args(migrations, "goose")...)
	output, err := commandOutput(bin, append(args, "2"))
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "execute", output, err)
	}

	// Reading the state back proves the revisions were written for the Goose
	// versions, not merely that the command exited 0.
	statusArgs := append([]string{"migrate", "status", "--url", dbURL}, spelling.args(migrations, "goose")...)
	status, err := commandOutput(bin, statusArgs)
	if err != nil {
		return atlasMetadataRuntimeExit(fixture, "readback", status, err)
	}
	if !strings.Contains(status, "Applied Migrations: 2") || !strings.Contains(status, "Pending Migrations: 0") {
		return Result{"atlas-cli-metadata-runtime", fixture, "readback", Gap,
			"`" + fixture + "` did not leave the Goose-format directory fully applied: " + oneLine(status),
			"stokaro/ptah#1002"}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "execute", OK,
		"`" + fixture + "` sets revisions from a directory laid out in Goose's convention; the read-back reports both migrations applied", ""}
}

// atlasMigrateTakesDirFormatVerbatim pins that neither verb widens the set of
// accepted format names. Atlas CE compares the value verbatim, so every
// near-miss spelling below is refused; a Ptah that folded case or trimmed
// whitespace would accept directories Atlas CE rejects.
func atlasMigrateTakesDirFormatVerbatim(bin string) []Result {
	nearMisses := []string{"Goose", "GOOSE", " goose", "goose "}

	verbs := []struct {
		fixture string
		prefix  func(dbURL string) []string
		suffix  []string
	}{
		{
			fixture: "atlas migrate status --dir-format verbatim",
			prefix:  func(dbURL string) []string { return []string{"migrate", "status", "--url", dbURL} },
		},
		{
			fixture: "atlas migrate set --dir-format verbatim",
			prefix:  func(dbURL string) []string { return []string{"migrate", "set", "--url", dbURL} },
			suffix:  []string{"2"},
		},
	}

	out := make([]Result, 0, len(verbs))
	for _, verb := range verbs {
		out = append(out, atlasMigrateRefusesNearMissDirFormats(bin, verb.fixture, verb.prefix, verb.suffix, nearMisses))
	}
	return out
}

func atlasMigrateRefusesNearMissDirFormats(bin, fixture string, prefix func(string) []string, suffix, nearMisses []string) Result {
	spelling := metadataDirSpellings()[0]
	root, migrations, cleanup, result := prepareGooseMetadataDir(bin, fixture, spelling)
	if result != nil {
		return *result
	}
	defer cleanup()

	dbURL := "sqlite://" + filepath.Join(root, "verbatim.db")
	for _, value := range nearMisses {
		full := append(prefix(dbURL), spelling.args(migrations, value)...)
		output, err := commandOutput(bin, append(full, suffix...))
		if err == nil {
			return Result{"atlas-cli-metadata-runtime", fixture, "flags", Gap,
				"`" + fixture + "` accepted --dir-format=" + strconv.Quote(value) +
					", but Atlas CE matches the format name verbatim and refuses it",
				"stokaro/ptah#1002"}
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return atlasMetadataRuntimeFail(fixture, "flags", err)
		}
		if !strings.Contains(output, strconv.Quote(value)) {
			return Result{"atlas-cli-metadata-runtime", fixture, "flags", Gap,
				"`" + fixture + "` refused --dir-format=" + strconv.Quote(value) +
					" without echoing the rejected value back: " + oneLine(output),
				"stokaro/ptah#1002"}
		}
	}
	return Result{"atlas-cli-metadata-runtime", fixture, "flags", OK,
		"`" + fixture + "` refuses every near-miss format spelling (" + strings.Join(quoteAll(nearMisses), ", ") +
			"), echoing each rejected value verbatim", ""}
}

func quoteAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strconv.Quote(value))
	}
	return out
}

// prepareGooseMetadataDir writes a Goose-convention directory and hashes it
// through the spelling under test, so the hash step is measured by the same
// route as the verb it feeds.
func prepareGooseMetadataDir(bin, fixture string, spelling metadataDirSpelling) (string, string, func(), *Result) {
	root, migrations, cleanup, err := atlasMetadataRuntimeDir()
	if err != nil {
		result := atlasMetadataRuntimeFail(fixture, "setup", err)
		return "", "", nil, &result
	}
	if err := writeGooseMetadataDir(migrations); err != nil {
		cleanup()
		result := atlasMetadataRuntimeFail(fixture, "setup", err)
		return "", "", nil, &result
	}
	output, err := commandOutput(bin, append([]string{"migrate", "hash"}, spelling.args(migrations, "goose")...))
	if err != nil {
		cleanup()
		result := atlasMetadataRuntimeExit(fixture, "setup", output, err)
		return "", "", nil, &result
	}
	return root, migrations, cleanup, nil
}

const (
	gooseMetadataFirstSQL = "-- +goose Up\nCREATE TABLE users (id INTEGER PRIMARY KEY);\n" +
		"-- +goose Down\nDROP TABLE users;\n"
	gooseMetadataSecondSQL = "-- +goose Up\nALTER TABLE users ADD COLUMN email TEXT;\n" +
		"-- +goose Down\nALTER TABLE users DROP COLUMN email;\n"
)

// writeGooseMetadataDir lays the directory out the way Goose does: numeric
// version prefixes and `+goose Up`/`+goose Down` section directives, neither of
// which the Atlas convention has.
func writeGooseMetadataDir(migrations string) error {
	if err := os.WriteFile(filepath.Join(migrations, "1_initial.sql"), []byte(gooseMetadataFirstSQL), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(migrations, "2_second_migration.sql"), []byte(gooseMetadataSecondSQL), 0o600)
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
