package probe

// White-box testing required: these tests exercise the differential result
// comparators, which are intentionally private to the migrate-runtime probe.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestCompareGooseIntegrity_HappyPath(t *testing.T) {
	c := qt.New(t)

	hash := compareGooseHashProcesses(integrityProcessResult{}, integrityProcessResult{})
	checksum := compareGooseChecksumBytes([]byte("same\n"), []byte("same\n"))
	clean := compareGooseCleanValidation(integrityProcessResult{}, integrityProcessResult{})
	tampered := integrityProcessResult{
		stdout: gooseIntegrityTamperStdout, stderr: gooseIntegrityTamperStderr, exitCode: 1,
	}
	advisory := compareAtlasFirstErrorAdvisory(integrityProcessResult{
		stdout:   gooseIntegrityTamperStdout,
		stderr:   gooseIntegrityTamperStderr + gooseIntegrityCommunityAdvisory,
		exitCode: 1,
	})
	tamper := compareGooseTamperedValidation(tampered, tampered)

	c.Assert(hash.Outcome, qt.Equals, OK)
	c.Assert(checksum.Outcome, qt.Equals, OK)
	c.Assert(clean.Outcome, qt.Equals, OK)
	c.Assert(advisory.Outcome, qt.Equals, OK)
	c.Assert(tamper.Outcome, qt.Equals, OK)
}

func TestCompareGooseIntegrity_FailurePath(t *testing.T) {
	c := qt.New(t)

	hash := compareGooseHashProcesses(
		integrityProcessResult{},
		integrityProcessResult{stdout: "progress\n"},
	)
	checksum := compareGooseChecksumBytes([]byte("atlas\n"), []byte("ptah\n"))
	clean := compareGooseCleanValidation(
		integrityProcessResult{},
		integrityProcessResult{stderr: "unexpected\n"},
	)
	tamperStreams := compareGooseTamperedValidation(
		integrityProcessResult{stderr: "Atlas\n", exitCode: 1},
		integrityProcessResult{stderr: "Ptah\n", exitCode: 1},
	)
	tamperExit := compareGooseTamperedValidation(
		integrityProcessResult{},
		integrityProcessResult{},
	)
	tamperSameWrongContract := compareGooseTamperedValidation(
		integrityProcessResult{stderr: "same but wrong\n", exitCode: 1},
		integrityProcessResult{stderr: "same but wrong\n", exitCode: 1},
	)
	advisoryMissing := compareAtlasFirstErrorAdvisory(integrityProcessResult{
		stdout: gooseIntegrityTamperStdout, stderr: gooseIntegrityTamperStderr, exitCode: 1,
	})

	c.Assert(hash.Outcome, qt.Equals, Gap)
	c.Assert(checksum.Outcome, qt.Equals, Gap)
	c.Assert(clean.Outcome, qt.Equals, Gap)
	c.Assert(tamperStreams.Outcome, qt.Equals, Gap)
	c.Assert(tamperExit.Outcome, qt.Equals, Gap)
	c.Assert(tamperSameWrongContract.Outcome, qt.Equals, Gap)
	c.Assert(advisoryMissing.Outcome, qt.Equals, Gap)
}
