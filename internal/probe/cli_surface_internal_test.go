package probe

// White-box testing required: CLI surface discovery has private help parsing,
// command classification, and process-boundary comparison primitives whose
// edge cases cannot be isolated through the report-level public API.

import (
	"os"
	"strings"
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
		{name: "schema plan registry sub-verb", path: []string{"schema", "plan", "approve"}, want: CLISurfaceOutOfScope},
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
			usage:  "atlas migrate set [flags] [version]",
			prefix: "atlas",
			path:   []string{"migrate", "set"},
			want:   true,
		},
		{
			name:   "falls back to parent",
			usage:  "atlas migrate [command]",
			prefix: "atlas",
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
printf "'atlas migrate push' is not supported by the community version.\n"
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "push"}}

	got := compareOutOfScopeCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate push",
		bin,
		[]string{"migrate", "push"},
		cmd,
		"stokaro/ptah#514",
	)

	c.Assert(got.Outcome, qt.Equals, OK)
}

func TestCompareOutOfScopeCommand_ResolvingStubIsAGap(t *testing.T) {
	c := qt.New(t)

	// A still-stubbed Cloud/registry verb that silently starts resolving as an
	// open capability must be a gap: an implemented verb belongs in
	// implementedProVerbSurfaces, where its surface is measured instead of
	// merely observed.
	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  *--help*)
    printf "Usage:\n  atlas migrate push [flags]\n"
    exit 0
    ;;
  *)
    printf "error: a registry URL is required (--url)\n"
    exit 2
    ;;
esac
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "push"}}

	got := compareOutOfScopeCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate push",
		bin,
		[]string{"migrate", "push"},
		cmd,
		"stokaro/ptah#514",
	)

	c.Assert(got.Outcome, qt.Equals, Gap)
	c.Assert(got.Detail, qt.Contains, "must keep the CE abort")
	c.Assert(got.Detail, qt.Contains, "open-capability expectations")
}

func TestCompareOutOfScopeCommand_FailurePath(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Usage:\n  atlas migrate [flags]\n"
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "push"}}

	got := compareOutOfScopeCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate push",
		bin,
		[]string{"migrate", "push"},
		cmd,
		"stokaro/ptah#514",
	)

	c.Assert(got.Outcome, qt.Equals, Gap)
	c.Assert(got.Detail, qt.Contains, "community-version unsupported boundary")
}

func TestImplementedProVerbSurfaces_CoverEveryImplementedVerb(t *testing.T) {
	c := qt.New(t)

	// Every implemented verb must also be a known out-of-scope command, so the
	// inventory keeps one classification while the comparison tier applies the
	// tightened open-capability expectation.
	known := map[string]bool{}
	for _, cmd := range knownOutOfScopeAtlasCommands() {
		known[strings.Join(cmd.path, " ")] = true
	}
	for verb := range implementedProVerbSurfaces() {
		c.Check(known[verb], qt.IsTrue, qt.Commentf("implemented verb %q is not a known out-of-scope command", verb))
	}
}

func TestCompareImplementedProCommand_HappyPath(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  *--help*)
    printf "Usage:\n  atlas migrate edit [flags] {name | version}\n\nFlags:\n      --dir string          Migration directory\n      --dir-format string   Migration directory format\n  -h, --help                help for edit\n"
    exit 0
    ;;
  *)
    printf "error: atlas migrate edit requires version argument\n"
    exit 1
    ;;
esac
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "edit"}}

	got := compareImplementedProCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate edit",
		bin,
		[]string{"migrate", "edit"},
		cmd,
		implementedProVerbSurfaces()["migrate edit"],
		"stokaro/ptah#514",
	)

	c.Assert(got, qt.HasLen, 3)
	stages := []string{got[0].Stage, got[1].Stage, got[2].Stage}
	c.Check(stages, qt.DeepEquals, []string{"capability-runtime", "usage", "flags"})
	for _, result := range got {
		c.Check(result.Outcome, qt.Equals, OK, qt.Commentf("%s: %s", result.Stage, result.Detail))
	}
	c.Check(got[0].Detail, qt.Contains, "open Ptah capability")
	c.Check(got[1].Detail, qt.Contains, "atlas migrate edit [flags] {name | version}")
	c.Check(got[2].Detail, qt.Contains, "--dir --dir-format")
}

func TestCompareImplementedProCommand_StubRegressionShortCircuits(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Abort: 'atlas migrate test' is not supported by the community version.\n"
exit 1
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "test"}}

	got := compareImplementedProCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate test",
		bin,
		[]string{"migrate", "test"},
		cmd,
		implementedProVerbSurfaces()["migrate test"],
		"stokaro/ptah#514",
	)

	c.Assert(got, qt.HasLen, 1)
	c.Check(got[0].Stage, qt.Equals, "capability-runtime")
	c.Check(got[0].Outcome, qt.Equals, Gap)
	c.Check(got[0].Detail, qt.Contains, "regressed to Atlas CE's community-version abort stub")
}

func TestCompareImplementedProCommand_SurfaceDrift(t *testing.T) {
	c := qt.New(t)

	// Wrong usage line and a missing required flag must each produce a gap
	// even though the verb resolves as an open capability.
	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  *--help*)
    printf "Usage:\n  atlas migrate test [flags]\n\nFlags:\n      --dir string   Migration directory\n"
    exit 0
    ;;
  *)
    printf "error: failed to load test cases\n"
    exit 1
    ;;
esac
`)
	cmd := CLISurfaceCommand{Path: []string{"migrate", "test"}}

	got := compareImplementedProCommand(
		"atlas-cli-surface-ptah-compat",
		"atlas migrate test",
		bin,
		[]string{"migrate", "test"},
		cmd,
		implementedProVerbSurfaces()["migrate test"],
		"stokaro/ptah#514",
	)

	c.Assert(got, qt.HasLen, 3)
	c.Check(got[0].Outcome, qt.Equals, OK)
	c.Check(got[1].Outcome, qt.Equals, Gap)
	c.Check(got[1].Detail, qt.Contains, "usage mismatch")
	c.Check(got[2].Outcome, qt.Equals, Gap)
	c.Check(got[2].Detail, qt.Contains, "missing --dev-url, --dir-format, --run")
}

func writeExecutable(t *testing.T, content string) string {
	t.Helper()

	path := t.TempDir() + "/cmd"
	err := os.WriteFile(path, []byte(content), 0o700) //nolint:gosec // Test command must be executable.
	qt.New(t).Assert(err, qt.IsNil)
	return path
}
