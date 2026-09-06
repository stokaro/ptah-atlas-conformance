//go:build tools

// Package tools anchors the Ptah CLI's dependency closure in go.mod. The
// atlas-cli-surface probe builds `ptah.run/cmd/ptah` by shelling
// out to `go build`, so nothing in the normal import graph references its
// command-only dependencies. This blank import keeps them resolvable offline in
// CI. It is excluded from normal builds by the `tools` build tag.
package tools

import _ "ptah.run/cmd/root"
