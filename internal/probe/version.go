package probe

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

const ptahModulePath = "ptah.run"

// PinnedPtah is what PtahVersion reports when the probe measures the ptah.run
// version go.mod requires: the module is linked without a replace directive
// and no external binary stands in for it.
const PinnedPtah = "go.mod"

// PtahVersion identifies the Ptah implementation the running probe measures.
// Reports stamp it so a generated artifact says what it exercised.
//
// The version go.mod requires is reported as PinnedPtah rather than spelled
// out. A report is committed beside go.mod, so the version adds nothing a
// reader cannot see there, and spelling it out would make every ptah.run bump
// rewrite every report even when no result moves. A departure from the pin is
// spelled out: a replace directive, or PTAH_BIN and PTAH_COMPAT_BIN with the
// digest of each binary. A report generated against one then differs from the
// pinned regeneration, which is what the staleness check reads.
func PtahVersion() string {
	linkedVersion, pinned := linkedPtahVersion()
	overrides := ptahBinaryOverrides()
	if len(overrides) == 0 {
		if pinned {
			return PinnedPtah
		}
		return linkedVersion
	}
	return linkedVersion + "; external binary overrides: " + strings.Join(overrides, ", ")
}

// linkedPtahVersion reports the linked ptah.run module and whether it is the
// version go.mod requires, with no replace directive.
func linkedPtahVersion() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ptahVersionUnknown(), false
	}
	for _, dep := range info.Deps {
		if dep.Path == ptahModulePath {
			return ptahModulePath + " " + moduleVersion(dep), dep.Replace == nil && dep.Version != ""
		}
	}
	return ptahVersionUnknown(), false
}

func ptahBinaryOverrides() []string {
	var overrides []string
	if value := strings.TrimSpace(os.Getenv("PTAH_BIN")); value != "" {
		overrides = append(overrides, externalBinaryIdentity("PTAH_BIN", value))
	}
	if value := strings.TrimSpace(os.Getenv("PTAH_COMPAT_BIN")); value != "" {
		overrides = append(overrides, externalBinaryIdentity("PTAH_COMPAT_BIN", value))
	}
	return overrides
}

func externalBinaryIdentity(name, path string) string {
	file, err := os.Open(path)
	if err != nil {
		return name + " sha256:(unavailable)"
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return name + " sha256:(unavailable)"
	}
	return name + " sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func moduleVersion(module *debug.Module) string {
	if module.Replace != nil {
		return moduleVersion(module.Replace)
	}
	if module.Version != "" {
		return module.Version
	}
	return "(version unknown)"
}

func ptahVersionUnknown() string {
	return ptahModulePath + " (version unknown)"
}
