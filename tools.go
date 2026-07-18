//go:build tools

// Package tools anchors the Ptah CLI's dependency closure in go.mod so that
// `go mod tidy` does not strip cobra/viper/cobraflags. The atlas-cli-surface
// probe builds `github.com/stokaro/ptah/cmd/ptah` by shelling out to `go build`, so
// nothing in the normal import graph references those modules; this blank import
// keeps them resolvable offline in CI. It is excluded from normal builds by the
// `tools` build tag.
package tools

import _ "github.com/stokaro/ptah/cmd/root"
