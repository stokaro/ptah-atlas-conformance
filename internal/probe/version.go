package probe

import (
	"runtime/debug"
)

const ptahModulePath = "github.com/stokaro/ptah"

// PtahVersion reports the Ptah module version linked into the running probe
// binary. Reports include this value so generated conformance artifacts identify
// the implementation they actually exercised.
func PtahVersion() string {
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
