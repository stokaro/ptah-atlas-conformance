package probe

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

type failedDownRevisionExpectation struct {
	version             string
	baselineDescription string
	baselineTotal       int64
	baselineHash        string
	applied             int64
	total               int64
	errorFragment       string
	errorStatement      string
	partialHashes       []string
	window              projectConfigApplyWindow
}

func validateFailedDownRevisionTransition(
	before,
	after []projectConfigRevisionMetadata,
	want failedDownRevisionExpectation,
) error {
	if len(after) != len(before) {
		return fmt.Errorf("revision row count changed from %d to %d", len(before), len(after))
	}
	beforeIndex := slices.IndexFunc(before, func(revision projectConfigRevisionMetadata) bool {
		return revision.Version == want.version
	})
	afterIndex := slices.IndexFunc(after, func(revision projectConfigRevisionMetadata) bool {
		return revision.Version == want.version
	})
	if beforeIndex < 0 || afterIndex < 0 {
		return fmt.Errorf("revision %s is missing before or after failed down", want.version)
	}
	for _, revision := range before {
		if revision.Version == want.version {
			continue
		}
		i := slices.IndexFunc(after, func(candidate projectConfigRevisionMetadata) bool {
			return candidate.Version == revision.Version
		})
		if i < 0 {
			return fmt.Errorf("unrelated revision %s disappeared during failed down", revision.Version)
		}
		if revision != after[i] {
			return fmt.Errorf("unrelated revision %s changed during failed down", revision.Version)
		}
	}

	previous := before[beforeIndex]
	failed := after[afterIndex]
	if err := validateFailedDownBaseline(previous, want); err != nil {
		return err
	}
	if failed.Version != previous.Version ||
		failed.Description != previous.Description ||
		failed.Type != previous.Type ||
		failed.Hash != previous.Hash {
		return fmt.Errorf("revision %s identity changed during failed down", want.version)
	}
	if failed.Applied != want.applied || failed.Total != want.total {
		return fmt.Errorf(
			"revision %s progress = %d/%d, want %d/%d",
			want.version,
			failed.Applied,
			failed.Total,
			want.applied,
			want.total,
		)
	}
	if failed.ErrorIsNull || !strings.Contains(failed.Error, want.errorFragment) || failed.ErrorStorageClass != "text" {
		return fmt.Errorf("revision %s error metadata does not describe the failed down", want.version)
	}
	if failed.ErrorStatementIsNull ||
		failed.ErrorStatement != want.errorStatement ||
		failed.ErrorStatementStorageClass != "text" {
		return fmt.Errorf("revision %s error statement = %q, want %q", want.version, failed.ErrorStatement, want.errorStatement)
	}
	if failed.OperatorVersion != "Ptah/down" {
		return fmt.Errorf("revision %s operator = %q, want Ptah/down", want.version, failed.OperatorVersion)
	}
	previousTime, err := parseProjectConfigRevisionTime(previous.ExecutedAt)
	if err != nil {
		return fmt.Errorf("revision %s previous timestamp: %w", want.version, err)
	}
	failedTime, err := parseProjectConfigRevisionTime(failed.ExecutedAt)
	if err != nil {
		return fmt.Errorf("revision %s failed-down timestamp: %w", want.version, err)
	}
	maximumExecutionTime := want.window.finishedAt.Sub(want.window.startedAt) +
		2*projectConfigDynamicMetadataTimeLag
	if !failedTime.After(previousTime) ||
		!projectConfigTimestampIsInApplyWindow(failedTime, want.window) ||
		failed.ExecutedAtStorageClass != "text" ||
		failed.ExecutionTime <= 0 ||
		time.Duration(failed.ExecutionTime) > maximumExecutionTime {
		return fmt.Errorf("revision %s did not record the failed-down timing interval", want.version)
	}
	return validateFailedDownPartialHashes(failed, want.applied, want.partialHashes)
}

func validateFailedDownBaseline(
	revision projectConfigRevisionMetadata,
	want failedDownRevisionExpectation,
) error {
	if revision.Description != want.baselineDescription || revision.Type != 2 {
		return fmt.Errorf(
			"revision %s baseline identity = description %q, type %d; want %q, type 2",
			want.version,
			revision.Description,
			revision.Type,
			want.baselineDescription,
		)
	}
	if revision.Applied != want.baselineTotal || revision.Total != want.baselineTotal {
		return fmt.Errorf(
			"revision %s baseline progress = %d/%d, want %d/%d",
			want.version,
			revision.Applied,
			revision.Total,
			want.baselineTotal,
			want.baselineTotal,
		)
	}
	if _, err := parseProjectConfigRevisionTime(revision.ExecutedAt); err != nil ||
		revision.ExecutedAtStorageClass != "text" || revision.ExecutionTime < 0 {
		return fmt.Errorf("revision %s baseline timing metadata is invalid", want.version)
	}
	if revision.ErrorIsNull || revision.Error != "" || revision.ErrorStorageClass != "text" ||
		revision.ErrorStatementIsNull || revision.ErrorStatement != "" ||
		revision.ErrorStatementStorageClass != "text" {
		return fmt.Errorf("revision %s baseline error metadata is not clean non-null text", want.version)
	}
	if revision.Hash != want.baselineHash {
		return fmt.Errorf("revision %s baseline hash = %q, want %q", want.version, revision.Hash, want.baselineHash)
	}
	if revision.PartialHashesIsNull || revision.PartialHashes != "null" ||
		revision.PartialHashesStorageClass != "blob" {
		return fmt.Errorf("revision %s baseline partial_hashes is not non-null JSON null", want.version)
	}
	if revision.OperatorVersion != "Ptah" {
		return fmt.Errorf("revision %s baseline operator = %q, want Ptah", want.version, revision.OperatorVersion)
	}
	return nil
}

func validateFailedDownPartialHashes(
	revision projectConfigRevisionMetadata,
	applied int64,
	expected []string,
) error {
	if revision.PartialHashesIsNull || revision.PartialHashesStorageClass != "blob" {
		return fmt.Errorf("revision %s partial_hashes is not stored as non-null JSON", revision.Version)
	}
	if applied == 0 {
		if revision.PartialHashes != "null" || len(expected) != 0 {
			return fmt.Errorf("revision %s partial_hashes = %q, want null", revision.Version, revision.PartialHashes)
		}
		return nil
	}
	var hashes []string
	if err := json.Unmarshal([]byte(revision.PartialHashes), &hashes); err != nil {
		return fmt.Errorf("decode revision %s partial_hashes: %w", revision.Version, err)
	}
	if len(hashes) != int(applied) || !slices.Equal(hashes, expected) {
		return fmt.Errorf("revision %s partial hashes = %v, want %v", revision.Version, hashes, expected)
	}
	for _, hash := range hashes {
		encoded, ok := strings.CutPrefix(hash, "h1:")
		if !ok {
			return fmt.Errorf("revision %s partial hash %q is not an h1 digest", revision.Version, hash)
		}
		digest, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("revision %s partial hash %q is not a valid SHA-256 h1 digest", revision.Version, hash)
		}
	}
	return nil
}

func failedDownStatementHash(statement string) string {
	digest := sha256.Sum256([]byte(statement))
	return "h1:" + base64.StdEncoding.EncodeToString(digest[:])
}
