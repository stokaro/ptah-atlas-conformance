package probe

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

const ptahModulePath = "go.5x5.cz/ptah"

// PtahVersion reports the Ptah module version linked into the running probe
// binary. Reports include this value so generated conformance artifacts identify
// the implementation they actually exercised.
func PtahVersion() string {
	linkedVersion := linkedPtahVersion()
	overrides := ptahBinaryOverrides()
	if len(overrides) == 0 {
		return linkedVersion
	}
	return linkedVersion + "; external binary overrides: " + strings.Join(overrides, ", ")
}

func linkedPtahVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ptahVersionUnknown()
	}
	for _, dep := range info.Deps {
		if dep.Path == ptahModulePath {
			return ptahModulePath + " " + moduleVersion(dep)
		}
	}
	return ptahVersionUnknown()
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
