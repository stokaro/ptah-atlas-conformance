package probe

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const convertedDirFixture = "goose/status-set-differential"

// gooseMigrateConvertedDirOracle pins stokaro/ptah#1002 against the binary the
// harness stands in for. `migrate status` and `migrate set` now read a
// migration directory laid out in a foreign tool's convention; this measures
// what the pinned community Atlas binary does with the same directory and the
// same arguments, so the new behavior is an observation rather than an
// assumption.
//
// Three contracts are measured:
//
//   - `migrate set` on a converted directory: the community binary reports the
//     versions it set, and ptah-compat must produce the same process result.
//   - `migrate status` before and after that set: the two binaries print
//     different status layouts (a long-standing, format-wide difference that is
//     not specific to converted directories), so the comparison is on the
//     migration facts each layout carries plus the exit code.
//   - the format name is taken verbatim by both binaries: near-miss spellings
//     are refused with exit 1 by the community binary, and ptah-compat must
//     refuse them too rather than being quietly more permissive.
func gooseMigrateConvertedDirOracle(ptahBin, atlasBin string) []Result {
	root, err := os.MkdirTemp("", "ptah-goose-converted-*")
	if err != nil {
		return []Result{migrateRuntimeFail(convertedDirFixture, "setup", err)}
	}
	defer func() { _ = os.RemoveAll(root) }()

	atlasBin = resolveCEGatingBinary(atlasBin)
	atlasEnv := []string{"HOME=" + filepath.Join(root, "atlas-home")}

	sides := map[string]*convertedDirSide{
		"atlas": {bin: atlasBin, env: atlasEnv},
		"ptah":  {bin: ptahBin},
	}
	for name, side := range sides {
		side.dir = filepath.Join(root, name, "migrations")
		side.db = "sqlite://" + filepath.Join(root, name, "state.db")
		if err := writeGooseIntegrityFixture(side.dir); err != nil {
			return []Result{migrateRuntimeFail(convertedDirFixture, "setup", err)}
		}
		hash, err := runIntegrityCommand(side.bin, "hash", side.dir, side.env...)
		if err != nil {
			return []Result{migrateRuntimeFail(convertedDirFixture, name+" hash", err)}
		}
		if hash.exitCode != 0 {
			return []Result{migrateRuntimeGap(convertedDirFixture, name+" hash",
				"hashing the Goose directory failed: "+integrityProcessDetail(hash))}
		}
	}
	atlas, ptah := sides["atlas"], sides["ptah"]

	pendingAtlas, pendingPtah, result := convertedDirStatus(atlas, ptah, "pending status")
	if result != nil {
		return []Result{*result}
	}
	results := []Result{compareConvertedDirStatus("pending status", pendingAtlas, pendingPtah, 2, 0, 2)}

	setAtlas, err := runConvertedDirCommand(atlas, "set", "2")
	if err != nil {
		return append(results, migrateRuntimeFail(convertedDirFixture, "atlas set", err))
	}
	setPtah, err := runConvertedDirCommand(ptah, "set", "2")
	if err != nil {
		return append(results, migrateRuntimeFail(convertedDirFixture, "ptah set", err))
	}
	results = append(results, compareConvertedDirSet(setAtlas, setPtah))

	appliedAtlas, appliedPtah, result := convertedDirStatus(atlas, ptah, "applied status")
	if result != nil {
		return append(results, *result)
	}
	results = append(results, compareConvertedDirStatus("applied status", appliedAtlas, appliedPtah, 2, 2, 0))

	return append(results, compareConvertedDirVerbatimFormats(atlas, ptah))
}

type convertedDirSide struct {
	bin string
	env []string
	dir string
	db  string
}

// runConvertedDirCommand runs one Atlas-form verb against this side's own copy
// of the converted directory, naming the format the way a caller migrating off
// the other tool would.
func runConvertedDirCommand(side *convertedDirSide, verb string, extra ...string) (integrityProcessResult, error) {
	args := []string{
		"migrate", verb,
		"--url", side.db,
		"--dir", fileURL(side.dir),
		"--dir-format", "goose",
	}
	return runIntegrityCommandArgs(side.bin, append(args, extra...), side.env...)
}

func convertedDirStatus(atlas, ptah *convertedDirSide, stage string) (integrityProcessResult, integrityProcessResult, *Result) {
	atlasStatus, err := runConvertedDirCommand(atlas, "status")
	if err != nil {
		result := migrateRuntimeFail(convertedDirFixture, "atlas "+stage, err)
		return integrityProcessResult{}, integrityProcessResult{}, &result
	}
	ptahStatus, err := runConvertedDirCommand(ptah, "status")
	if err != nil {
		result := migrateRuntimeFail(convertedDirFixture, "ptah "+stage, err)
		return integrityProcessResult{}, integrityProcessResult{}, &result
	}
	return atlasStatus, ptahStatus, nil
}

// compareConvertedDirSet compares the two `migrate set` processes byte for
// byte. Unlike status, the pinned community binary and ptah-compat print the
// same text here, so anything less than equality is a regression.
func compareConvertedDirSet(atlas, ptah integrityProcessResult) Result {
	if atlas.exitCode != 0 {
		return migrateRuntimeGap(convertedDirFixture, "set process",
			"the community Atlas binary did not set revisions from the converted directory: "+integrityProcessDetail(atlas))
	}
	if atlas != ptah {
		return migrateRuntimeGap(convertedDirFixture, "set process",
			"set process results differ: Atlas="+integrityProcessDetail(atlas)+" Ptah="+integrityProcessDetail(ptah))
	}
	if !strings.Contains(atlas.stdout, "Current version is 2") {
		return migrateRuntimeGap(convertedDirFixture, "set process",
			"both binaries agreed but neither reported setting the Goose version 2: "+integrityProcessDetail(atlas))
	}
	return Result{migrateRuntimeProbeName, convertedDirFixture, "set process", OK,
		"`migrate set --dir-format goose` produced byte-identical exit/stdout/stderr on the community Atlas binary and ptah-compat, both reporting the Goose versions they set", ""}
}

// compareConvertedDirStatus compares the migration facts rather than the
// layout: the two binaries render status differently for every directory
// format, so requiring identical bytes here would measure that unrelated
// difference instead of the converted-directory reader.
func compareConvertedDirStatus(stage string, atlas, ptah integrityProcessResult, total, applied, pending int) Result {
	if atlas.exitCode != 0 || ptah.exitCode != 0 {
		return migrateRuntimeGap(convertedDirFixture, stage,
			"reading the converted directory did not exit 0: Atlas="+integrityProcessDetail(atlas)+
				" Ptah="+integrityProcessDetail(ptah))
	}
	atlasFacts := parseAtlasStatusFacts(atlas.stdout)
	ptahFacts := parsePtahStatusFacts(ptah.stdout)
	want := convertedDirStatusFacts{total: total, applied: applied, pending: pending}
	if atlasFacts != want {
		return migrateRuntimeGap(convertedDirFixture, stage,
			"the community Atlas binary read the converted directory as "+atlasFacts.String()+
				", not "+want.String()+": "+integrityProcessDetail(atlas))
	}
	if ptahFacts != atlasFacts {
		return migrateRuntimeGap(convertedDirFixture, stage,
			"converted-directory status facts differ: Atlas="+atlasFacts.String()+
				" Ptah="+ptahFacts.String()+" ("+integrityProcessDetail(ptah)+")")
	}
	return Result{migrateRuntimeProbeName, convertedDirFixture, stage, OK,
		"the community Atlas binary and ptah-compat both read the converted directory as " + want.String() +
			" (the status layouts differ for every directory format, so the migration facts are compared)", ""}
}

// compareConvertedDirVerbatimFormats pins that neither binary widens the set of
// accepted format names once the directory is readable: the format is matched
// case-sensitively and untrimmed on both verbs.
func compareConvertedDirVerbatimFormats(atlas, ptah *convertedDirSide) Result {
	nearMisses := []string{"Goose", "GOOSE", " goose", "goose "}
	verbs := []struct {
		name  string
		extra []string
	}{
		{name: "status"},
		{name: "set", extra: []string{"2"}},
	}

	for _, verb := range verbs {
		for _, value := range nearMisses {
			atlasResult, err := runConvertedDirFormatValue(atlas, verb.name, value, verb.extra...)
			if err != nil {
				return migrateRuntimeFail(convertedDirFixture, "verbatim format", err)
			}
			ptahResult, err := runConvertedDirFormatValue(ptah, verb.name, value, verb.extra...)
			if err != nil {
				return migrateRuntimeFail(convertedDirFixture, "verbatim format", err)
			}
			if atlasResult.exitCode != 1 {
				return migrateRuntimeGap(convertedDirFixture, "verbatim format",
					"the community Atlas binary did not refuse `migrate "+verb.name+" --dir-format "+value+
						"` with exit 1: "+integrityProcessDetail(atlasResult))
			}
			if ptahResult.exitCode != atlasResult.exitCode {
				return migrateRuntimeGap(convertedDirFixture, "verbatim format",
					"`migrate "+verb.name+" --dir-format "+value+"` exit codes differ: Atlas="+
						integrityProcessDetail(atlasResult)+" Ptah="+integrityProcessDetail(ptahResult))
			}
			if !strings.Contains(atlasResult.stderr, `"`+value+`"`) || !strings.Contains(ptahResult.stderr, `"`+value+`"`) {
				return migrateRuntimeGap(convertedDirFixture, "verbatim format",
					"`migrate "+verb.name+" --dir-format "+value+"` was refused without echoing the value verbatim: Atlas="+
						integrityProcessDetail(atlasResult)+" Ptah="+integrityProcessDetail(ptahResult))
			}
		}
	}
	return Result{migrateRuntimeProbeName, convertedDirFixture, "verbatim format", OK,
		"the community Atlas binary and ptah-compat both refuse the near-miss format spellings \"Goose\", \"GOOSE\", \" goose\" and \"goose \" on `migrate status` and `migrate set` with exit 1, each echoing the rejected value verbatim", ""}
}

func runConvertedDirFormatValue(side *convertedDirSide, verb, format string, extra ...string) (integrityProcessResult, error) {
	args := []string{
		"migrate", verb,
		"--url", side.db,
		"--dir", fileURL(side.dir),
		"--dir-format", format,
	}
	return runIntegrityCommandArgs(side.bin, append(args, extra...), side.env...)
}

type convertedDirStatusFacts struct {
	total   int
	applied int
	pending int
}

func (f convertedDirStatusFacts) String() string {
	return "{total=" + statusCount(f.total) + " applied=" + statusCount(f.applied) +
		" pending=" + statusCount(f.pending) + "}"
}

// statusCount renders a parsed count, keeping "unreadable" visibly distinct
// from zero in the reported detail.
func statusCount(value int) string {
	if value < 0 {
		return "unreadable"
	}
	return strconv.Itoa(value)
}

// parseAtlasStatusFacts reads the community binary's status block, which
// reports executed and pending file counts and no total.
func parseAtlasStatusFacts(stdout string) convertedDirStatusFacts {
	executed := statusFieldValue(stdout, "-- Executed Files:")
	pending := statusFieldValue(stdout, "-- Pending Files:")
	return convertedDirStatusFacts{total: executed + pending, applied: executed, pending: pending}
}

// parsePtahStatusFacts reads ptah-compat's status block, which reports the
// same three quantities under its own labels.
func parsePtahStatusFacts(stdout string) convertedDirStatusFacts {
	return convertedDirStatusFacts{
		total:   statusFieldValue(stdout, "Total Migrations:"),
		applied: statusFieldValue(stdout, "Applied Migrations:"),
		pending: statusFieldValue(stdout, "Pending Migrations:"),
	}
}

// statusFieldValue returns the integer following label, or -1 when the label is
// absent or carries something that is not a count, so a missing field can never
// be mistaken for a zero count.
func statusFieldValue(stdout, label string) int {
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, label) {
			continue
		}
		field := strings.TrimSpace(strings.TrimPrefix(trimmed, label))
		value, err := strconv.Atoi(field)
		if err != nil {
			return -1
		}
		return value
	}
	return -1
}
