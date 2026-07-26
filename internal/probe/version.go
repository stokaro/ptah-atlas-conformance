package probe

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

const ptahModulePath = "github.com/stokaro/ptah"

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
		overrides = append(overrides, "PTAH_BIN="+strconv.Quote(value))
	}
	if value := strings.TrimSpace(os.Getenv("PTAH_COMPAT_BIN")); value != "" {
		overrides = append(overrides, "PTAH_COMPAT_BIN="+strconv.Quote(value))
	}
	return overrides
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
