package probe

// White-box testing required: CLI surface discovery has private help parsing,
// command classification, and process-boundary comparison primitives whose
// edge cases cannot be isolated through the report-level public API.

import (
	"os"
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestParseHelpDetails(t *testing.T) {
	c := qt.New(t)

	help := `The migrate command.

Usage:
  atlas migrate set [flags] [version]

Available Commands:
  apply       Applies pending migration files.
  set         Set the current version.

Flags:
  -u, --url string                select a resource
      --dir string                select migration directory (default "file://migrations")
  -h, --help                      help for set

Global Flags:
  -c, --config string        select config file
      --env string           set env
      --var <name>=<value>   input variables (default [])
`

	got := parseHelpDetails(help)

	c.Assert(got.Usage, qt.Equals, "atlas migrate set [flags] [version]")
	c.Assert(got.Flags, qt.DeepEquals, []string{"--config", "--dir", "--env", "--url", "--var"})
	c.Assert(got.Subcommands, qt.DeepEquals, []atlasHelpCommand{
		{Name: "apply", Summary: "Applies pending migration files."},
		{Name: "set", Summary: "Set the current version."},
	})
}

func TestParseHelpDetails_IgnoresExampleFlags(t *testing.T) {
	c := qt.New(t)

	help := `Usage:
  atlas migrate set [flags] [version]

Examples:
  atlas migrate set 1 --revision-schema my_revisions

Flags:
      --revisions-schema string   name of the schema
`

	got := parseHelpDetails(help)

	c.Assert(got.Flags, qt.DeepEquals, []string{"--revisions-schema"})
}

func TestClassifyAtlasCommand_HappyPath(t *testing.T) {
	c := qt.New(t)

	tests := []struct {
		name string
		path []string
		want CLISurfaceClassification
	}{
		{name: "root", path: nil, want: CLISurfaceOSS},
		{name: "version", path: []string{"version"}, want: CLISurfaceOSS},
		{name: "schema inspect", path: []string{"schema", "inspect"}, want: CLISurfaceOSS},
		{name: "migrate set", path: []string{"migrate", "set"}, want: CLISurfaceOSS},
		{name: "schema push", path: []string{"schema", "push"}, want: CLISurfaceOutOfScope},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			got, _ := classifyAtlasCommand(tt.path)
			c.Assert(got, qt.Equals, tt.want)
		})
	}
}

func TestClassifyAtlasCommand_FailurePath(t *testing.T) {
	c := qt.New(t)

	got, reason := classifyAtlasCommand([]string{"migrate", "future"})

	c.Assert(got, qt.Equals, CLISurfaceUnclassified)
	c.Assert(reason, qt.Contains, "new command")
}

func TestUsageMatchesPrefix(t *testing.T) {
	c := qt.New(t)

	tests := []struct {
		name   string
		usage  string
		prefix string
		path   []string
		want   bool
	}{
		{
			name:   "exact command with args",
			usage:  "ptah atlas migrate set [flags] [version]",
			prefix: "ptah atlas",
			path:   []string{"migrate", "set"},
			want:   true,
		},
		{
			name:   "falls back to parent",
			usage:  "ptah atlas migrate [command]",
			prefix: "ptah atlas",
			path:   []string{"migrate", "set"},
			want:   false,
		},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			got := usageMatchesPrefix(tt.usage, tt.prefix, tt.path)
			c.Assert(got, qt.Equals, tt.want)
		})
	}
}

func TestCompareOutOfScopeCommand_HappyPath(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "'atlas migrate checkpoint' is not supported by the community version.\n"
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "checkpoint"}}

	got := compareOutOfScopeCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate checkpoint",
		bin,
		[]string{"migrate", "checkpoint"},
		cmd,
		"stokaro/ptah#514",
	)

	c.Assert(got.Outcome, qt.Equals, OK)
}

func TestCompareOutOfScopeCommand_PtahCapability(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  *--help*)
    printf "Usage:\n  atlas migrate checkpoint [flags]\n"
    exit 0
    ;;
  *)
    printf "error: a shadow database URL is required (--shadow-db)\n"
    exit 2
    ;;
esac
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "checkpoint"}}

	got := compareOutOfScopeCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate checkpoint",
		bin,
		[]string{"migrate", "checkpoint"},
		cmd,
		"stokaro/ptah#514",
	)

	c.Assert(got.Outcome, qt.Equals, OK)
	c.Assert(got.Detail, qt.Contains, "open Ptah capability beyond Atlas CE")
	c.Assert(got.Detail, qt.Contains, "does not claim behavioral coverage")
}

func TestCompareOutOfScopeCommand_FailurePath(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Usage:\n  atlas migrate [flags]\n"
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "checkpoint"}}

	got := compareOutOfScopeCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate checkpoint",
		bin,
		[]string{"migrate", "checkpoint"},
		cmd,
		"stokaro/ptah#514",
	)

	c.Assert(got.Outcome, qt.Equals, Gap)
	c.Assert(got.Detail, qt.Contains, "community-version unsupported boundary")
}

func writeExecutable(t *testing.T, content string) string {
	t.Helper()

	path := t.TempDir() + "/cmd"
	err := os.WriteFile(path, []byte(content), 0o700) //nolint:gosec // Test command must be executable.
	qt.New(t).Assert(err, qt.IsNil)
	return path
}
