package probe_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const ceAbortOutput = "Abort: 'atlas migrate down' is not supported by the community version.\n\n" +
	"To install the non-community version of Atlas, use the following command:\n"

func TestClassifyCEGating(t *testing.T) {
	c := qt.New(t)
	namedError := regexp.MustCompile(`missing data source handler for "composite_schema"`)

	tests := []struct {
		name        string
		rules       probe.CEGatingRules
		exitCode    int
		output      string
		wantClass   probe.CEGatingClass
		wantSummary string
	}{
		{
			name:        "community abort fires on the stub refusal",
			exitCode:    1,
			output:      ceAbortOutput,
			wantClass:   probe.CEGatingCommunityAbort,
			wantSummary: "Abort: 'atlas migrate down' is not supported by the community version.",
		},
		{
			name:        "community abort fires for gated flags too",
			exitCode:    1,
			output:      "Abort: 'atlas schema apply --include' is not supported by the community version.\n",
			wantClass:   probe.CEGatingCommunityAbort,
			wantSummary: "Abort: 'atlas schema apply --include' is not supported by the community version.",
		},
		{
			name:      "community abort wins over a works predicate",
			rules:     probe.CEGatingRules{SuccessExit: 1},
			exitCode:  1,
			output:    ceAbortOutput,
			wantClass: probe.CEGatingCommunityAbort,
		},
		{
			name:      "community abort stays silent on ordinary community-notice output",
			exitCode:  0,
			output:    "You're running the community build of Atlas, which differs from the official version.\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			// Near-miss negative: an Abort line without the community-version
			// sentence must not classify as a gating stub.
			name:        "community abort stays silent on an unrelated Abort line",
			exitCode:    1,
			output:      "Abort: could not connect to gateway\n",
			wantClass:   probe.CEGatingUnclassified,
			wantSummary: "exit 1: Abort: could not connect to gateway",
		},
		{
			// Specificity: the classifier must match the community sentence,
			// not merely the first line starting with "Abort:".
			name:        "community abort matches the community sentence, not just any Abort line",
			exitCode:    1,
			output:      "Abort: retrying connection\nAbort: 'atlas schema push' is not supported by the community version.\n",
			wantClass:   probe.CEGatingCommunityAbort,
			wantSummary: "Abort: 'atlas schema push' is not supported by the community version.",
		},
		{
			name:        "unknown flag fires",
			exitCode:    1,
			output:      "Error: unknown flag: --web\n",
			wantClass:   probe.CEGatingUnknownFlag,
			wantSummary: "Error: unknown flag: --web",
		},
		{
			name:      "unknown flag stays silent on clean output",
			exitCode:  0,
			output:    "Migration Status: OK\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			// Near-miss negative: the word "flag" inside ordinary working
			// output must not classify as unknown-flag.
			name:      "unknown flag stays silent on the word flag inside works output",
			rules:     probe.CEGatingRules{SuccessFragments: []string{"applied"}},
			exitCode:  0,
			output:    "applied migration 2 (a flag day migration)\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			// Specificity: the classifier must match cobra's "unknown flag: "
			// error, not merely a line containing "flag".
			name:        "unknown flag matches the cobra error line, not just any flag mention",
			exitCode:    1,
			output:      "flag parsing started\nError: unknown flag: --web\n",
			wantClass:   probe.CEGatingUnknownFlag,
			wantSummary: "Error: unknown flag: --web",
		},
		{
			name:        "named error fires on a non-zero exit with the pattern",
			rules:       probe.CEGatingRules{NamedErrorPattern: namedError},
			exitCode:    1,
			output:      "Error: missing data source handler for \"composite_schema\"\n",
			wantClass:   probe.CEGatingNamedError,
			wantSummary: "Error: missing data source handler for \"composite_schema\"",
		},
		{
			name:      "named error stays silent on exit 0",
			rules:     probe.CEGatingRules{NamedErrorPattern: namedError},
			exitCode:  0,
			output:    "Error: missing data source handler for \"composite_schema\"\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			name:      "named error stays silent without a configured pattern",
			exitCode:  1,
			output:    "Error: missing data source handler for \"composite_schema\"\n",
			wantClass: probe.CEGatingUnclassified,
		},
		{
			name:      "absent fires on the migrate parent help",
			rules:     probe.CEGatingRules{AbsentCommandPath: "atlas migrate ls"},
			exitCode:  0,
			output:    "'atlas migrate' wraps several sub-commands for migration management.\n\nUsage:\n  atlas migrate [command]\n",
			wantClass: probe.CEGatingAbsent,
		},
		{
			name:      "absent fires on the schema parent help",
			rules:     probe.CEGatingRules{AbsentCommandPath: "atlas schema validate"},
			exitCode:  0,
			output:    "The `atlas schema` command groups subcommands working with declarative Atlas schemas.\n",
			wantClass: probe.CEGatingAbsent,
		},
		{
			name:      "absent stays silent when no subcommand was named",
			exitCode:  0,
			output:    "'atlas migrate' wraps several sub-commands for migration management.\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			// Near-miss negative: a future Atlas registering the name as its
			// own command GROUP prints that group's help — same wrapping
			// sentence, but the first line names the attempted path. That is
			// no longer absent and must not stay green.
			name:      "absent stays silent when the help names the attempted path itself",
			rules:     probe.CEGatingRules{AbsentCommandPath: "atlas migrate ls"},
			exitCode:  0,
			output:    "'atlas migrate ls' wraps several sub-commands for listing revisions.\n\nUsage:\n  atlas migrate ls [command]\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			name:      "absent stays silent on a non-zero exit",
			rules:     probe.CEGatingRules{AbsentCommandPath: "atlas migrate ls"},
			exitCode:  1,
			output:    "'atlas migrate' wraps several sub-commands for migration management.\n",
			wantClass: probe.CEGatingUnclassified,
		},
		{
			name: "silent-unenforced fires on exit 0 with the fragments",
			rules: probe.CEGatingRules{
				SilentWhenExitZero: true,
				SilentFragments:    []string{"Schema is synced"},
			},
			exitCode:    0,
			output:      "Schema is synced, no changes to be made\n",
			wantClass:   probe.CEGatingSilentUnenforced,
			wantSummary: `exit 0; output contains "Schema is synced"`,
		},
		{
			name: "silent-unenforced stays silent on a non-zero exit",
			rules: probe.CEGatingRules{
				SilentWhenExitZero: true,
				SilentFragments:    []string{"Schema is synced"},
			},
			exitCode:  1,
			output:    "Schema is synced, no changes to be made\n",
			wantClass: probe.CEGatingUnclassified,
		},
		{
			name: "silent-unenforced stays silent when a fragment is missing",
			rules: probe.CEGatingRules{
				SilentWhenExitZero: true,
				SilentFragments:    []string{"Schema is synced"},
			},
			exitCode:  0,
			output:    "-- Planned Changes:\nCREATE TABLE `users` ...\n",
			wantClass: probe.CEGatingWorks,
		},
		{
			name:        "works accepts a plain exit 0",
			exitCode:    0,
			output:      "",
			wantClass:   probe.CEGatingWorks,
			wantSummary: "exit 0",
		},
		{
			name: "works accepts the lint exit-1-with-findings contract",
			rules: probe.CEGatingRules{
				SuccessExit:      1,
				SuccessFragments: []string{"destructive changes detected"},
			},
			exitCode:    1,
			output:      "  -- analyzing version 20260101000002\n    -- destructive changes detected:\n",
			wantClass:   probe.CEGatingWorks,
			wantSummary: `exit 1; output contains "destructive changes detected"`,
		},
		{
			name: "works stays silent when the exit code differs",
			rules: probe.CEGatingRules{
				SuccessExit:      1,
				SuccessFragments: []string{"destructive changes detected"},
			},
			exitCode:  0,
			output:    "destructive changes detected\n",
			wantClass: probe.CEGatingUnclassified,
		},
		{
			name:      "works stays silent when a fragment is missing",
			rules:     probe.CEGatingRules{SuccessFragments: []string{"Migration Status: OK"}},
			exitCode:  0,
			output:    "Migration Status: PENDING\n",
			wantClass: probe.CEGatingUnclassified,
		},
		{
			name:        "unclassified reports the first non-empty output line",
			exitCode:    3,
			output:      "\nsomething unexpected happened\n",
			wantClass:   probe.CEGatingUnclassified,
			wantSummary: "exit 3: something unexpected happened",
		},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			gotClass, gotSummary := probe.ClassifyCEGating(tt.rules, tt.exitCode, tt.output)
			c.Check(gotClass, qt.Equals, tt.wantClass)
			if tt.wantSummary != "" {
				c.Check(gotSummary, qt.Equals, tt.wantSummary)
			}
		})
	}
}

// TestCEGatingScenarioTable_MatchesMeasuredBaseline pins the scenario table to
// the hand-measured 2026-08-01 Atlas CE v1.2.0 gating baseline. Changing the
// table without re-measuring against the real binary must fail here.
func TestCEGatingScenarioTable_MatchesMeasuredBaseline(t *testing.T) {
	c := qt.New(t)
	table := probe.CEGatingScenarioTable()

	got := make(map[string]probe.CEGatingClass, len(table))
	for _, s := range table {
		c.Check(s.Argv, qt.Not(qt.HasLen), 0, qt.Commentf("%s has empty argv", s.Fixture))
		_, dup := got[s.Fixture]
		c.Check(dup, qt.IsFalse, qt.Commentf("duplicate fixture label %q", s.Fixture))
		got[s.Fixture] = s.Expected
	}

	c.Assert(got, qt.DeepEquals, map[string]probe.CEGatingClass{
		// Works logged out.
		"atlas migrate hash":                      probe.CEGatingWorks,
		"atlas migrate lint (destructive latest)": probe.CEGatingWorks,
		"atlas schema diff":                       probe.CEGatingWorks,
		"atlas migrate diff":                      probe.CEGatingWorks,
		"atlas migrate apply":                     probe.CEGatingWorks,
		"atlas migrate status":                    probe.CEGatingWorks,
		"atlas schema apply (declarative)":        probe.CEGatingWorks,
		// Registered community-abort stubs.
		"atlas schema push":            probe.CEGatingCommunityAbort,
		"atlas schema plan --env":      probe.CEGatingCommunityAbort,
		"atlas schema test":            probe.CEGatingCommunityAbort,
		"atlas migrate push":           probe.CEGatingCommunityAbort,
		"atlas migrate test":           probe.CEGatingCommunityAbort,
		"atlas migrate checkpoint":     probe.CEGatingCommunityAbort,
		"atlas migrate edit":           probe.CEGatingCommunityAbort,
		"atlas migrate rebase":         probe.CEGatingCommunityAbort,
		"atlas migrate rm":             probe.CEGatingCommunityAbort,
		"atlas migrate down (bare)":    probe.CEGatingCommunityAbort,
		"atlas schema apply --include": probe.CEGatingCommunityAbort,
		// Never-registered verbs.
		"atlas migrate ls":      probe.CEGatingAbsent,
		"atlas migrate show":    probe.CEGatingAbsent,
		"atlas schema validate": probe.CEGatingAbsent,
		"atlas schema stats":    probe.CEGatingAbsent,
		// Silent, unenforced behavior.
		"atlas schema apply (role block)":            probe.CEGatingSilentUnenforced,
		"atlas migrate apply (failing txtar checks)": probe.CEGatingSilentUnenforced,
		// Named errors.
		"atlas schema inspect --env (composite_schema)": probe.CEGatingNamedError,
		"atlas schema inspect --env (external_schema)":  probe.CEGatingNamedError,
		// Flags a licensed Atlas build registers and CE does not register at all.
		"atlas schema inspect --web":     probe.CEGatingUnknownFlag,
		"atlas schema inspect --include": probe.CEGatingUnknownFlag,
	})

	counts := map[probe.CEGatingClass]int{}
	for _, class := range got {
		counts[class]++
	}
	c.Check(counts, qt.DeepEquals, map[probe.CEGatingClass]int{
		probe.CEGatingWorks:            7,
		probe.CEGatingCommunityAbort:   11,
		probe.CEGatingAbsent:           4,
		probe.CEGatingSilentUnenforced: 2,
		probe.CEGatingNamedError:       2,
		probe.CEGatingUnknownFlag:      2,
	})
}

func TestRunCEGating_UnusableBinaryIsAHarnessFailurePerScenario(t *testing.T) {
	c := qt.New(t)
	missing := filepath.Join(t.TempDir(), "atlas-does-not-exist")

	run := probe.RunCEGating(missing)

	c.Assert(run.Results, qt.HasLen, len(probe.CEGatingScenarioTable()))
	for _, r := range run.Results {
		c.Check(r.Probe, qt.Equals, "ce-gating")
		c.Check(r.Outcome, qt.Equals, probe.Fail,
			qt.Commentf("%s/%s: %s", r.Fixture, r.Stage, r.Detail))
	}
	c.Check(run.Observed, qt.HasLen, 0)
}

// TestRunCEGating_BareBinaryNameResolvesViaPATH pins the documented
// `atlas`-on-PATH fallback: a bare command name must be resolved through PATH
// before any scenario changes the working directory — never made absolute
// against the probe's cwd, which would break every scenario with fork/exec
// failures and silently rewrite the committed reports.
func TestRunCEGating_BareBinaryNameResolvesViaPATH(t *testing.T) {
	c := qt.New(t)
	binDir := t.TempDir()
	// A stand-in atlas that exits 0 for every invocation: enough to prove the
	// name resolved and ran, without depending on a real Atlas build.
	fake := filepath.Join(binDir, "ce-gating-fake-atlas")
	c.Assert(os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755), qt.IsNil)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := probe.RunCEGating("ce-gating-fake-atlas")

	c.Assert(run.Results, qt.HasLen, len(probe.CEGatingScenarioTable()))
	for _, r := range run.Results {
		c.Check(r.Detail, qt.Not(qt.Contains), "fork/exec",
			qt.Commentf("%s/%s: %s", r.Fixture, r.Stage, r.Detail))
	}
	// A setup-free stub scenario must have produced a classified observation:
	// the silent fake exits 0, so the abort baseline reads as a works gap —
	// proof the bare name resolved via PATH and actually executed.
	var sawSchemaPush bool
	for _, r := range run.Results {
		if r.Fixture != "atlas schema push" {
			continue
		}
		sawSchemaPush = true
		c.Check(r.Outcome, qt.Equals, probe.Gap, qt.Commentf("%s", r.Detail))
		c.Check(r.Detail, qt.Contains, "observed works")
	}
	c.Check(sawSchemaPush, qt.IsTrue)
}

func TestCEGatingAtlasVersion_HermeticFirstLine(t *testing.T) {
	c := qt.New(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-atlas")
	// The stand-in reports $HOME so the test can prove the version probe runs
	// under the scratch logged-out environment, not the developer's.
	script := "#!/bin/sh\necho \"fake atlas v9.9 home=$HOME\"\necho \"second line\"\n"
	c.Assert(os.WriteFile(fake, []byte(script), 0o755), qt.IsNil)

	line, err := probe.CEGatingAtlasVersion(fake)

	c.Assert(err, qt.IsNil)
	c.Check(line, qt.Contains, "fake atlas v9.9")
	c.Check(line, qt.Not(qt.Contains), "second line")
	// The reported HOME is the scenario scratch home, never the real one.
	c.Check(line, qt.Contains, "ptah-ce-gating-")
	if home := os.Getenv("HOME"); home != "" {
		c.Check(line, qt.Not(qt.Contains), "home="+home)
	}
}

func TestCEGatingAtlasVersion_UnusableBinary(t *testing.T) {
	c := qt.New(t)

	_, err := probe.CEGatingAtlasVersion(filepath.Join(t.TempDir(), "atlas-does-not-exist"))

	c.Assert(err, qt.IsNotNil)
}

func TestRenderCEGatingMarkdown_Header(t *testing.T) {
	c := qt.New(t)
	results := []probe.Result{{
		Probe:   "ce-gating",
		Fixture: "atlas migrate hash",
		Stage:   "works",
		Outcome: probe.OK,
		Detail:  "class: works — exit 0",
	}}

	md := probe.RenderCEGatingMarkdownWithCommand(results, "atlas community version v1.2.0", "make probe-ce-gating")

	c.Check(md, qt.Contains, "# Atlas CE gating conformance report")
	c.Check(md, qt.Contains, "make probe-ce-gating")
	c.Check(md, qt.Contains, "`atlas community version v1.2.0`")
	c.Check(md, qt.Contains, "logged out")
	c.Check(md, qt.Contains, "measured 2026-08-01 baseline for Atlas CE v1.2.0")
	// This tier measures the Atlas binary only; Ptah must not be claimed.
	c.Check(md, qt.Not(qt.Contains), "Ptah at `")
}
