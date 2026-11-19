package probe

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// The duplicate-version oracle measures a retained divergence. Two files
// spelling the same version make Atlas CE run the first file, record its
// revision and panic; the next apply finds nothing to execute, so the second
// file never runs. ptah-compat refuses the directory before any migration
// runs. Ptah documents the divergence as "Two files for one version" on its
// compatibility differences page,
// docs/site/src/content/docs/atlas/retained-divergences.md in stokaro/ptah.
const (
	duplicateVersionFixture = "sqlite/apply-duplicate-version"
	duplicateVersionIssue   = "stokaro/ptah#6324"
	duplicateVersion        = "20260101000000"
	duplicateVersionFirst   = duplicateVersion + "_a.sql"
	duplicateVersionSecond  = duplicateVersion + "_b.sql"
	duplicateVersionTableA  = "duplicate_a"
	duplicateVersionTableB  = "duplicate_b"

	// duplicateVersionAtlasPanic is the first line Atlas CE writes on stderr.
	duplicateVersionAtlasPanic = "panic: runtime error: index out of range [0] with length 0\n"
	// duplicateVersionAtlasRerun is Atlas CE's answer to the second apply.
	duplicateVersionAtlasRerun = "No migration files to execute\n"
	// duplicateVersionPtahRefusal is the start of ptah-compat's refusal. The
	// refusal may go on to name the files, so only the part that names the
	// version is pinned.
	duplicateVersionPtahRefusal = "Error: error registering migrations: duplicate Atlas up migration for version " +
		duplicateVersion
)

// duplicateVersionObservation is what one binary did to a fresh SQLite
// database: the first and second apply, and the tables and revision versions
// the database held afterwards.
type duplicateVersionObservation struct {
	first     integrityProcessResult
	second    integrityProcessResult
	tables    []string
	revisions []string
}

func sqliteMigrateApplyDuplicateVersion(ptahBin, atlasBin string) Result {
	root, err := os.MkdirTemp("", "ptah-duplicate-version-*")
	if err != nil {
		return migrateRuntimeFail(duplicateVersionFixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()

	dir := filepath.Join(root, "migrations")
	if err := writeDuplicateVersionFixture(dir); err != nil {
		return migrateRuntimeFail(duplicateVersionFixture, "setup", err)
	}
	atlasBin = resolveCEGatingBinary(atlasBin)
	atlasEnv := []string{"HOME=" + filepath.Join(root, "atlas-home")}
	if _, stderr, err := commandStreamsWithEnv(atlasBin, []string{"migrate", "hash", "--dir", fileURL(dir)}, "", atlasEnv); err != nil {
		return migrateRuntimeFail(duplicateVersionFixture, "atlas hash", fmt.Errorf("%w: %s", err, oneLine(stderr)))
	}

	atlas, err := observeDuplicateVersionApply(atlasBin, dir, filepath.Join(root, "atlas.db"), atlasEnv)
	if err != nil {
		return migrateRuntimeFail(duplicateVersionFixture, "atlas apply", err)
	}
	ptah, err := observeDuplicateVersionApply(ptahBin, dir, filepath.Join(root, "ptah.db"), nil)
	if err != nil {
		return migrateRuntimeFail(duplicateVersionFixture, "ptah apply", err)
	}
	return compareDuplicateVersionApply(atlas, ptah)
}

func writeDuplicateVersionFixture(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	files := map[string]string{
		duplicateVersionFirst:  "CREATE TABLE " + duplicateVersionTableA + " (id INTEGER PRIMARY KEY);\n",
		duplicateVersionSecond: "CREATE TABLE " + duplicateVersionTableB + " (id INTEGER PRIMARY KEY);\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func observeDuplicateVersionApply(bin, dir, dbPath string, env []string) (duplicateVersionObservation, error) {
	var observation duplicateVersionObservation
	var err error
	args := []string{"migrate", "apply", "--dir", fileURL(dir), "--url", sqliteURL(dbPath)}
	if observation.first, err = runDuplicateVersionCommand(bin, args, env); err != nil {
		return duplicateVersionObservation{}, err
	}
	if observation.second, err = runDuplicateVersionCommand(bin, args, env); err != nil {
		return duplicateVersionObservation{}, err
	}
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return duplicateVersionObservation{}, err
	}
	defer func() { _ = db.Close() }()
	if observation.tables, err = sqliteTableNames(db); err != nil {
		return duplicateVersionObservation{}, err
	}
	if !slices.Contains(observation.tables, "atlas_schema_revisions") {
		return observation, nil
	}
	revisions, err := sqliteRevisionFacts(db)
	if err != nil {
		return duplicateVersionObservation{}, err
	}
	for _, revision := range revisions {
		observation.revisions = append(observation.revisions, revision.Version)
	}
	return observation, nil
}

func runDuplicateVersionCommand(bin string, args, env []string) (integrityProcessResult, error) {
	stdout, stderr, err := commandStreamsWithEnv(bin, args, "", env)
	result := integrityProcessResult{stdout: stdout, stderr: stderr}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result, nil
	}
	return integrityProcessResult{}, err
}

// compareDuplicateVersionApply checks each binary against the behavior
// measured for it, and reports the divergence as kept only when both hold. A
// change on either side is a gap: an Atlas CE that stops crashing reopens the
// decision, and a ptah-compat that runs a file is the defect the refusal
// exists to prevent.
func compareDuplicateVersionApply(atlas, ptah duplicateVersionObservation) Result {
	if problem := duplicateVersionAtlasProblem(atlas); problem != "" {
		return Result{migrateRuntimeProbeName, duplicateVersionFixture, "atlas-oracle", Gap,
			"Atlas CE no longer matches the measured duplicate-version crash: " + problem, duplicateVersionIssue}
	}
	if problem := duplicateVersionPtahProblem(ptah); problem != "" {
		return Result{migrateRuntimeProbeName, duplicateVersionFixture, "ptah", Gap,
			"ptah-compat did not refuse the duplicate version before running a migration: " + problem, duplicateVersionIssue}
	}
	return Result{migrateRuntimeProbeName, duplicateVersionFixture, fileTxModePtahBetterStage, OK,
		"retained divergence: Atlas CE ran " + duplicateVersionFirst + ", recorded revision " + duplicateVersion +
			" and panicked with exit 2, and its next apply exited 0 with nothing to execute, so " +
			duplicateVersionSecond + " never ran; ptah-compat refused the directory with exit 1 on both applies " +
			"and left the database empty",
		duplicateVersionIssue}
}

func duplicateVersionAtlasProblem(atlas duplicateVersionObservation) string {
	switch {
	case atlas.first.exitCode != 2 || atlas.first.stdout != "" ||
		!strings.HasPrefix(atlas.first.stderr, duplicateVersionAtlasPanic):
		return "first apply " + integrityProcessDetail(atlas.first)
	case atlas.second != integrityProcessResult{stdout: duplicateVersionAtlasRerun}:
		return "second apply " + integrityProcessDetail(atlas.second)
	case !slices.Equal(atlas.tables, []string{"atlas_schema_revisions", duplicateVersionTableA}):
		return fmt.Sprintf("tables %q", atlas.tables)
	case !slices.Equal(atlas.revisions, []string{duplicateVersion}):
		return fmt.Sprintf("revisions %q", atlas.revisions)
	}
	return ""
}

func duplicateVersionPtahProblem(ptah duplicateVersionObservation) string {
	for i, apply := range []integrityProcessResult{ptah.first, ptah.second} {
		if apply.exitCode != 1 || apply.stdout != "" || !strings.HasPrefix(apply.stderr, duplicateVersionPtahRefusal) {
			return fmt.Sprintf("apply %d %s", i+1, integrityProcessDetail(apply))
		}
	}
	if len(ptah.tables) != 0 {
		return fmt.Sprintf("tables %q", ptah.tables)
	}
	return ""
}
