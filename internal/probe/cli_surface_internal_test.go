package probe

// White-box testing required: CLI surface discovery has private help parsing,
// command classification, and process-boundary comparison primitives whose
// edge cases cannot be isolated through the report-level public API.

import (
	"os"
	"sort"
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

func TestCommandHelp_HappyPath(t *testing.T) {
	c := qt.New(t)
	bin := writeExecutable(t, `#!/bin/sh
printf "Usage:\n  atlas migrate status [flags]\n"
`)

	got, err := commandHelp(bin, []string{"migrate", "status"})

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.Equals, "Usage:\n  atlas migrate status [flags]\n")
}

func TestCommandHelp_FailurePath(t *testing.T) {
	c := qt.New(t)
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name: "non-zero exit",
			script: `#!/bin/sh
printf "Usage:\n  atlas migrate status [flags]\n"
exit 1
`,
			want: `help exited 1; stdout="Usage: atlas migrate status \[flags\]" stderr=""`,
		},
		{
			name: "help on stderr",
			script: `#!/bin/sh
printf "Usage:\n  atlas migrate status [flags]\n" >&2
`,
			want: `help wrote to stderr: Usage: atlas migrate status \[flags\]`,
		},
	}

	for _, test := range tests {
		c.Run(test.name, func(c *qt.C) {
			got, err := commandHelp(writeExecutable(t, test.script), []string{"migrate", "status"})
			c.Assert(err, qt.ErrorMatches, test.want)
			c.Assert(got, qt.Equals, "")
		})
	}
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
case "$*" in
  *--help*)
    printf "atlas migrate push is not implemented by Ptah.\n"
    ;;
  *)
    printf "Error: atlas migrate push is not implemented by Ptah\n" >&2
    exit 1
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

	c.Assert(got, qt.HasLen, 2)
	c.Assert(got[0].Outcome, qt.Equals, OK)
	c.Assert(got[0].Detail, qt.Contains, "exit 1, empty stdout, and byte-exact stderr")
	c.Assert(got[1].Outcome, qt.Equals, OK)
	c.Assert(got[1].Detail, qt.Contains, "byte-exact Ptah-owned unavailable-command help boundary")
}

func TestCompareOutOfScopeCommand_ResolvingStubIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "migration pushed\n"
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "exited successfully")
	c.Assert(got[0].Detail, qt.Contains, "open-capability expectations")
}

func TestCompareOutOfScopeCommand_WrongDiagnosticIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Error: cloud command unavailable\n" >&2
exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "byte-exact Ptah-owned unavailable-command diagnostic")
}

func TestCompareOutOfScopeCommand_WrongCommandDiagnosticIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Error: atlas schema push is not implemented by Ptah\n" >&2
exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "atlas migrate push is not implemented by Ptah")
}

func TestCompareOutOfScopeCommand_CopiedAtlasDiagnosticIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Abort: 'atlas migrate push' is not supported by the community version.\n" >&2
exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "byte-exact Ptah-owned unavailable-command diagnostic")
}

func TestCompareOutOfScopeCommand_WrongExitCodeIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Error: atlas migrate push is not implemented by Ptah\n" >&2
exit 2
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "requires exit code 1")
}

func TestCompareOutOfScopeCommand_RuntimeDiagnosticOnStdoutIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Error: atlas migrate push is not implemented by Ptah\n"
exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "unexpected stdout")
}

func TestCompareOutOfScopeCommand_RuntimeWhitespaceDriftIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Error: atlas migrate push is not implemented by Ptah \n" >&2
exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, Gap)
	c.Assert(got[0].Detail, qt.Contains, "byte-exact Ptah-owned unavailable-command diagnostic")
}

func TestCompareOutOfScopeCommand_HelpDiagnosticOnStderrIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  *--help*)
    printf "atlas migrate push is not implemented by Ptah.\n" >&2
    ;;
  *)
    printf "Error: atlas migrate push is not implemented by Ptah\n" >&2
    exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, OK)
	c.Assert(got[1].Outcome, qt.Equals, Gap)
	c.Assert(got[1].Detail, qt.Contains, "unexpected stderr")
}

func TestCompareOutOfScopeCommand_HelpExtraOutputIsAGap(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  *--help*)
    printf "atlas migrate push is not implemented by Ptah.\nextra\n"
    ;;
  *)
    printf "Error: atlas migrate push is not implemented by Ptah\n" >&2
    exit 1
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

	c.Assert(got[0].Outcome, qt.Equals, OK)
	c.Assert(got[1].Outcome, qt.Equals, Gap)
	c.Assert(got[1].Detail, qt.Contains, "byte-exact Ptah-owned unavailable-command help text")
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
	c.Check(stages, qt.DeepEquals, []string{"availability-boundary", "usage", "flags"})
	for _, result := range got {
		c.Check(result.Outcome, qt.Equals, OK, qt.Commentf("%s: %s", result.Stage, result.Detail))
	}
	c.Check(got[0].Detail, qt.Contains, "does not return either unavailable-command sentinel")
	c.Check(got[1].Detail, qt.Contains, "atlas migrate edit [flags] {name | version}")
	c.Check(got[2].Detail, qt.Contains, "--dir --dir-format")
}

func TestCompareImplementedProCommand_StubRegressionShortCircuits(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Error: atlas migrate test is not implemented by Ptah\n" >&2
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
	c.Check(got[0].Stage, qt.Equals, "availability-boundary")
	c.Check(got[0].Outcome, qt.Equals, Gap)
	c.Check(got[0].Detail, qt.Contains, "regressed to an unavailable-command stub")
}

func TestCompareImplementedProCommand_LegacyAtlasStubRegressionShortCircuits(t *testing.T) {
	c := qt.New(t)

	bin := writeExecutable(t, `#!/bin/sh
printf "Abort: 'atlas migrate test' is not supported by the community version.\n" >&2
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
	c.Check(got[0].Stage, qt.Equals, "availability-boundary")
	c.Check(got[0].Outcome, qt.Equals, Gap)
	c.Check(got[0].Detail, qt.Contains, "regressed to an unavailable-command stub")
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

// ceInspectFlags mirrors the long flags the pinned Atlas CE binary registers on
// `atlas schema inspect`. The Pro-flag allowance is measured as a delta from
// exactly this set, so the tests below state it explicitly instead of shelling
// out to the binary.
var ceInspectFlags = []string{"--config", "--dev-url", "--env", "--exclude", "--format", "--schema", "--url", "--var"}

func TestProSurfaceFlags_TableIsWellFormed(t *testing.T) {
	c := qt.New(t)

	// The allowance only ever applies to OSS commands: out-of-scope verbs are
	// compared by compareOutOfScopeCommand / compareImplementedProCommand and
	// never reach compareFlags, so an entry there would be silently dead.
	for command, flags := range proSurfaceFlags() {
		classification, _ := classifyAtlasCommand(strings.Fields(command))
		c.Check(classification, qt.Equals, CLISurfaceOSS,
			qt.Commentf("Pro-flag allowance %q is not an OSS command, so compareFlags never consults it", command))

		c.Check(flags, qt.Not(qt.HasLen), 0, qt.Commentf("empty allowance for %q", command))
		seen := map[string]bool{}
		for i, flag := range flags {
			c.Check(strings.HasPrefix(flag, "--"), qt.IsTrue,
				qt.Commentf("%q allowance entry %q is not a long flag", command, flag))
			c.Check(seen[flag], qt.IsFalse, qt.Commentf("%q allowance repeats %q", command, flag))
			seen[flag] = true
			if i > 0 {
				c.Check(flags[i-1] < flag, qt.IsTrue,
					qt.Commentf("%q allowance is not sorted at %q", command, flag))
			}
		}
	}
}

func TestCompareFlags_ProSurfaceFlagIsAllowedAndNamed(t *testing.T) {
	c := qt.New(t)

	// `atlas schema inspect --include` is registered by Atlas
	// but not by the pinned CE binary, and ptah-compat implements it
	// (stokaro/ptah#977). It must not read as a non-Atlas flag.
	atlasCmd := CLISurfaceCommand{
		Path:     []string{"schema", "inspect"},
		Flags:    ceInspectFlags,
		ProFlags: proSurfaceFlags()["schema inspect"],
	}
	target := helpDetails{Flags: append(append([]string(nil), ceInspectFlags...), "--include")}

	got := compareFlags("atlas-cli-surface-ptah-compat", "atlas schema inspect", atlasCmd, target, cliSurfaceCompatIssue)

	c.Assert(got.Outcome, qt.Equals, OK, qt.Commentf("detail: %s", got.Detail))
	// A bare OK would make a mistaken allow-list entry invisible forever, so
	// the adopted Pro surface has to be named in the committed report.
	c.Check(got.Detail, qt.Contains, "plus Pro-surface flags implemented openly: --include")
	c.Check(got.Detail, qt.Contains, "long flags match Atlas: "+strings.Join(ceInspectFlags, " "))
}

func TestCompareFlags_UnimplementedProSurfaceFlagIsNotAGap(t *testing.T) {
	c := qt.New(t)

	// --export is on the same Atlas-only delta but ptah-compat does not
	// implement it. Missing flags are measured against the CE set only, so an
	// unimplemented Pro flag must never turn the tier red — and must not be
	// announced as adopted either.
	atlasCmd := CLISurfaceCommand{
		Path:     []string{"schema", "inspect"},
		Flags:    ceInspectFlags,
		ProFlags: proSurfaceFlags()["schema inspect"],
	}
	target := helpDetails{Flags: ceInspectFlags}

	got := compareFlags("atlas-cli-surface-ptah-compat", "atlas schema inspect", atlasCmd, target, cliSurfaceCompatIssue)

	c.Assert(got.Outcome, qt.Equals, OK, qt.Commentf("detail: %s", got.Detail))
	c.Check(got.Detail, qt.Not(qt.Contains), "--export")
	c.Check(got.Detail, qt.Not(qt.Contains), "Pro-surface flags implemented openly")
}

func TestCompareFlags_ProFlagAdoptedByCEIsNoLongerAnnouncedAsProSurface(t *testing.T) {
	c := qt.New(t)

	// Simulates a future atlas.version bump in which CE itself starts
	// registering --include. The flag is then ordinary CE parity, so the detail
	// must stop advertising it as adopted Pro surface — otherwise the report
	// would keep crediting a dead allow-list entry.
	ceFlags := append(append([]string(nil), ceInspectFlags...), "--include")
	sort.Strings(ceFlags)
	atlasCmd := CLISurfaceCommand{
		Path:     []string{"schema", "inspect"},
		Flags:    ceFlags,
		ProFlags: proSurfaceFlags()["schema inspect"],
	}
	target := helpDetails{Flags: ceFlags}

	got := compareFlags("atlas-cli-surface-ptah-compat", "atlas schema inspect", atlasCmd, target, cliSurfaceCompatIssue)

	c.Assert(got.Outcome, qt.Equals, OK, qt.Commentf("detail: %s", got.Detail))
	c.Check(got.Detail, qt.Equals, "long flags match Atlas: "+strings.Join(ceFlags, " "))
	c.Check(got.Detail, qt.Not(qt.Contains), "Pro-surface flags implemented openly")
}

func TestCompareFlags_ArbitraryExtraFlagOnAnAllowedCommandIsStillAGap(t *testing.T) {
	c := qt.New(t)

	// The allowance is closed: `schema inspect` having an entry must not make
	// the command a free-for-all.
	atlasCmd := CLISurfaceCommand{
		Path:     []string{"schema", "inspect"},
		Flags:    ceInspectFlags,
		ProFlags: proSurfaceFlags()["schema inspect"],
	}
	target := helpDetails{Flags: append(append([]string(nil), ceInspectFlags...), "--include", "--ptah-anything")}

	got := compareFlags("atlas-cli-surface-ptah-compat", "atlas schema inspect", atlasCmd, target, cliSurfaceCompatIssue)

	c.Assert(got.Outcome, qt.Equals, Gap, qt.Commentf("detail: %s", got.Detail))
	c.Check(got.Detail, qt.Equals, "flag mismatch: extra --ptah-anything")
	c.Check(got.Issue, qt.Equals, cliSurfaceCompatIssue)
}

func TestCompareFlags_ProFlagAllowanceIsPerCommand(t *testing.T) {
	c := qt.New(t)

	// --include is allowed on `schema inspect` because Atlas
	// registers it there. Atlas does NOT register it on
	// `migrate diff` (verified against the same help surface),
	// so the same flag must still be a gap on that command.
	c.Assert(proSurfaceFlags()["migrate diff"], qt.HasLen, 0)

	ceDiffFlags := []string{"--dev-url", "--dir", "--to"}
	atlasCmd := CLISurfaceCommand{
		Path:     []string{"migrate", "diff"},
		Flags:    ceDiffFlags,
		ProFlags: proSurfaceFlags()["migrate diff"],
	}
	target := helpDetails{Flags: append(append([]string(nil), ceDiffFlags...), "--include")}

	got := compareFlags("atlas-cli-surface-ptah-compat", "atlas migrate diff", atlasCmd, target, cliSurfaceCompatIssue)

	c.Assert(got.Outcome, qt.Equals, Gap, qt.Commentf("detail: %s", got.Detail))
	c.Check(got.Detail, qt.Equals, "flag mismatch: extra --include")
}

func TestCompareFlags_MissingCEFlagIsStillAGapOnAnAllowedCommand(t *testing.T) {
	c := qt.New(t)

	// The allowance must not leak into the missing direction: every CE flag is
	// still mandatory on a command that carries a Pro-flag entry.
	atlasCmd := CLISurfaceCommand{
		Path:     []string{"schema", "inspect"},
		Flags:    ceInspectFlags,
		ProFlags: proSurfaceFlags()["schema inspect"],
	}
	target := helpDetails{Flags: append([]string(nil), ceInspectFlags[1:]...)}

	got := compareFlags("atlas-cli-surface-ptah-compat", "atlas schema inspect", atlasCmd, target, cliSurfaceCompatIssue)

	c.Assert(got.Outcome, qt.Equals, Gap, qt.Commentf("detail: %s", got.Detail))
	c.Check(got.Detail, qt.Equals, "flag mismatch: missing --config")
}

func TestDiscoverCLISurface_AttachesProSurfaceFlags(t *testing.T) {
	c := qt.New(t)

	// Pins the wiring, not just the rule: the allow-list is useless unless
	// discovery actually attaches it to the discovered command.
	bin := writeExecutable(t, `#!/bin/sh
case "$*" in
  "version")
    printf "atlas community version v1.2.0\n"
    ;;
  "--help")
    printf "Usage:\n  atlas\n\nAvailable Commands:\n  schema      Work with atlas schemas\n\nFlags:\n  -h, --help   help for atlas\n"
    ;;
  "schema --help")
    printf "Usage:\n  atlas schema\n\nAvailable Commands:\n  inspect     Inspect a database schema\n\nFlags:\n  -h, --help   help for schema\n"
    ;;
  "schema inspect --help")
    printf "Usage:\n  atlas schema inspect [flags]\n\nFlags:\n  -u, --url string   select a resource\n  -h, --help         help for inspect\n"
    ;;
  *)
    exit 1
    ;;
esac
`)

	inventory, err := DiscoverCLISurface(bin)
	c.Assert(err, qt.IsNil)

	byPath := map[string]CLISurfaceCommand{}
	for _, cmd := range inventory.Commands {
		byPath[strings.Join(cmd.Path, " ")] = cmd
	}

	inspect, ok := byPath["schema inspect"]
	c.Assert(ok, qt.IsTrue)
	c.Check(inspect.Flags, qt.DeepEquals, []string{"--url"})
	c.Check(inspect.ProFlags, qt.DeepEquals, proSurfaceFlags()["schema inspect"])

	// Commands with no allowance entry must carry no Pro flags at all.
	schema, ok := byPath["schema"]
	c.Assert(ok, qt.IsTrue)
	c.Check(schema.ProFlags, qt.HasLen, 0)
}

func writeExecutable(t *testing.T, content string) string {
	t.Helper()

	path := t.TempDir() + "/cmd"
	err := os.WriteFile(path, []byte(content), 0o700) //nolint:gosec // Test command must be executable.
	qt.New(t).Assert(err, qt.IsNil)
	return path
}
