package probe

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const gooseIntegrityFixture = "goose/hash-validate-differential"

type integrityProcessResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func gooseMigrateIntegrityOracle(ptahBin, atlasBin string) []Result {
	root, err := os.MkdirTemp("", "ptah-goose-integrity-*")
	if err != nil {
		return []Result{migrateRuntimeFail(gooseIntegrityFixture, "setup", err)}
	}
	defer func() { _ = os.RemoveAll(root) }()

	atlasDir := filepath.Join(root, "atlas")
	ptahDir := filepath.Join(root, "ptah")
	atlasEnv := []string{"HOME=" + filepath.Join(root, "atlas-home")}
	for _, dir := range []string{atlasDir, ptahDir} {
		if err := writeGooseIntegrityFixture(dir); err != nil {
			return []Result{migrateRuntimeFail(gooseIntegrityFixture, "setup", err)}
		}
	}

	atlasBin = resolveCEGatingBinary(atlasBin)
	atlasHash, err := runIntegrityCommand(atlasBin, "hash", atlasDir, ptahFullSurface, atlasEnv...)
	if err != nil {
		return []Result{migrateRuntimeFail(gooseIntegrityFixture, "atlas hash", err)}
	}
	ptahHash, err := runIntegrityCommand(ptahBin, "hash", ptahDir, ptahStrictCESurface)
	if err != nil {
		return []Result{migrateRuntimeFail(gooseIntegrityFixture, "ptah hash", err)}
	}

	results := []Result{compareGooseHashProcesses(atlasHash, ptahHash)}
	atlasSum, err := os.ReadFile(filepath.Join(atlasDir, "atlas.sum"))
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "read Atlas checksum", err))
	}
	ptahSum, err := os.ReadFile(filepath.Join(ptahDir, "atlas.sum"))
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "read Ptah checksum", err))
	}
	results = append(results, compareGooseChecksumBytes(atlasSum, ptahSum))

	if err := os.WriteFile(filepath.Join(ptahDir, "atlas.sum"), atlasSum, 0o600); err != nil { //nolint:gosec // Fixed child of the private MkdirTemp root.
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "install Atlas checksum", err))
	}
	if err := os.WriteFile(filepath.Join(atlasDir, "atlas.sum"), ptahSum, 0o600); err != nil { //nolint:gosec // Fixed child of the private MkdirTemp root.
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "install Ptah checksum", err))
	}
	atlasValidate, err := runIntegrityCommand(atlasBin, "validate", atlasDir, ptahFullSurface, atlasEnv...)
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "Atlas cross-validation", err))
	}
	ptahValidate, err := runIntegrityCommand(ptahBin, "validate", ptahDir, ptahStrictCESurface)
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "Ptah cross-validation", err))
	}
	results = append(results, compareGooseCleanValidation(atlasValidate, ptahValidate))

	for _, dir := range []string{atlasDir, ptahDir} {
		if err := os.WriteFile(filepath.Join(dir, "1_initial.sql"), []byte(gooseIntegrityInitialSQL+"-- tampered\n"), 0o600); err != nil {
			return append(results, migrateRuntimeFail(gooseIntegrityFixture, "tamper fixture", err))
		}
	}
	atlasFirstTampered, err := runIntegrityCommand(atlasBin, "validate", atlasDir, ptahFullSurface, atlasEnv...)
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "Atlas first tamper validation", err))
	}
	results = append(results, compareAtlasFirstErrorAdvisory(atlasFirstTampered))
	atlasTampered, err := runIntegrityCommand(atlasBin, "validate", atlasDir, ptahFullSurface, atlasEnv...)
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "Atlas stable tamper validation", err))
	}
	ptahTampered, err := runIntegrityCommand(ptahBin, "validate", ptahDir, ptahStrictCESurface)
	if err != nil {
		return append(results, migrateRuntimeFail(gooseIntegrityFixture, "Ptah tamper validation", err))
	}
	return append(results, compareGooseTamperedValidation(atlasTampered, ptahTampered))
}

const (
	gooseIntegrityInitialSQL = "-- +goose Up\nCREATE TABLE users (id INTEGER PRIMARY KEY);\n" +
		"-- +goose Down\nDROP TABLE users;\n"
	gooseIntegritySecondSQL = "-- +goose Up\nALTER TABLE users ADD COLUMN email TEXT;\n" +
		"-- +goose Down\nALTER TABLE users DROP COLUMN email;\n"
	gooseIntegrityTamperStdout = "You have a checksum error in your migration directory.\n\n" +
		"\tL2: 1_initial.sql was edited\n\n" +
		"Please check your migration files and run 'atlas migrate hash' to re-hash the contents\n\n"
	gooseIntegrityTamperStderr      = "Error: checksum mismatch\n"
	gooseIntegrityCommunityAdvisory = "You're running the community build of Atlas, which differs from the official version.\n" +
		"If this error persists, try installing the official version as a troubleshooting step:\n\n" +
		"  curl -sSf https://atlasgo.sh | sh\n\n" +
		"More installation options: https://atlasgo.io/docs#installation\n"
)

func writeGooseIntegrityFixture(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "1_initial.sql"), []byte(gooseIntegrityInitialSQL), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "2_second_migration.sql"), []byte(gooseIntegritySecondSQL), 0o600)
}

func runIntegrityCommand(
	bin, verb, dir string,
	surface ptahCommandSurface,
	env ...string,
) (integrityProcessResult, error) {
	commandEnv := slices.Concat(surface.environment(), env)
	stdout, stderr, err := commandStreamsWithExactEnv(bin, []string{
		"migrate", verb, "--dir", fileURL(dir), "--dir-format", "goose",
	}, "", commandEnv)
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

func compareAtlasFirstErrorAdvisory(atlas integrityProcessResult) Result {
	want := integrityProcessResult{
		stdout:   gooseIntegrityTamperStdout,
		stderr:   gooseIntegrityTamperStderr + gooseIntegrityCommunityAdvisory,
		exitCode: 1,
	}
	if atlas != want {
		return migrateRuntimeGap(gooseIntegrityFixture, "Atlas first-error advisory",
			"Atlas CE first tamper response differs from the pinned v1.3.0 stateful process contract: "+
				integrityProcessDetail(atlas))
	}
	return Result{migrateRuntimeProbeName, gooseIntegrityFixture, "Atlas first-error advisory", OK,
		"Atlas CE emitted the pinned v1.3.0 community-build advisory on the first error in an isolated HOME; the stable checksum contract is compared on the repeated error", ""}
}

func compareGooseHashProcesses(atlas, ptah integrityProcessResult) Result {
	if atlas != ptah {
		return migrateRuntimeGap(gooseIntegrityFixture, "hash process",
			"hash process results differ: Atlas="+integrityProcessDetail(atlas)+
				" Ptah="+integrityProcessDetail(ptah))
	}
	if atlas.exitCode != 0 || atlas.stdout != "" || atlas.stderr != "" {
		return migrateRuntimeGap(gooseIntegrityFixture, "hash process",
			"successful Atlas Goose hash was not silent: "+integrityProcessDetail(atlas))
	}
	return Result{migrateRuntimeProbeName, gooseIntegrityFixture, "hash process", OK,
		"Atlas CE and ptah-compat both generated the Goose checksum with exit 0 and empty stdout/stderr", ""}
}

func compareGooseChecksumBytes(atlas, ptah []byte) Result {
	if !bytes.Equal(atlas, ptah) {
		return migrateRuntimeGap(gooseIntegrityFixture, "checksum bytes",
			"Atlas CE and ptah-compat generated different atlas.sum byte streams")
	}
	return Result{migrateRuntimeProbeName, gooseIntegrityFixture, "checksum bytes", OK,
		"Atlas CE and ptah-compat generated byte-identical atlas.sum files from the same Goose directory", ""}
}

func compareGooseCleanValidation(atlas, ptah integrityProcessResult) Result {
	if atlas != ptah {
		return migrateRuntimeGap(gooseIntegrityFixture, "cross-validation",
			"clean cross-validation results differ: Atlas="+integrityProcessDetail(atlas)+
				" Ptah="+integrityProcessDetail(ptah))
	}
	if atlas.exitCode != 0 || atlas.stdout != "" || atlas.stderr != "" {
		return migrateRuntimeGap(gooseIntegrityFixture, "cross-validation",
			"clean cross-validation was not silent: "+integrityProcessDetail(atlas))
	}
	return Result{migrateRuntimeProbeName, gooseIntegrityFixture, "cross-validation", OK,
		"each binary silently accepted the other binary's Goose atlas.sum", ""}
}

func compareGooseTamperedValidation(atlas, ptah integrityProcessResult) Result {
	if atlas.exitCode != 1 || atlas.stdout != gooseIntegrityTamperStdout || atlas.stderr != gooseIntegrityTamperStderr {
		return migrateRuntimeGap(gooseIntegrityFixture, "tamper detection",
			"Atlas CE tamper response differs from the pinned v1.3.0 process contract: "+
				integrityProcessDetail(atlas))
	}
	if atlas != ptah {
		return migrateRuntimeGap(gooseIntegrityFixture, "tamper detection",
			"tampered validation results differ: Atlas="+integrityProcessDetail(atlas)+
				" Ptah="+integrityProcessDetail(ptah))
	}
	return Result{migrateRuntimeProbeName, gooseIntegrityFixture, "tamper detection", OK,
		"Atlas CE and ptah-compat rejected the same tampered Goose directory with the pinned v1.3.0 stable exit/stdout/stderr contract after the Atlas-only first-error advisory was measured separately", ""}
}

func integrityProcessDetail(result integrityProcessResult) string {
	return fmt.Sprintf("{exit=%d stdout=%q stderr=%q}", result.exitCode,
		oneLine(strings.ReplaceAll(result.stdout, os.TempDir(), "$TMPDIR")),
		oneLine(strings.ReplaceAll(result.stderr, os.TempDir(), "$TMPDIR")))
}
