package probe

import (
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// atlasVerbFlags is the set of essential Atlas flags each measured verb accepts
// — the flags a standard drop-in workflow relies on. OSS rows run through the
// strict CE surface. The one Pro-only command in this older catalog, migrate
// down, runs through the full surface so this probe cannot erase Ptah's open
// capability while measuring CE parity elsewhere. A resolving `atlas <verb>`
// is not a drop-in until it also accepts these; the compat surface probe only
// proves the verb exists, so this probe measures the depth behind it. Source:
// https://atlasgo.io/cli-reference. Cloud/enterprise-leaning flags (--web,
// --plan, --export, --edit) are deliberately omitted to keep this OSS-scoped;
// `schema fmt` (positional paths), `version` and `license` take no such flags.
type atlasVerbFlagSpec struct {
	AtlasCmd string
	Path     []string
	Flags    []string
	Defaults []atlasFlagDefault
	Surface  atlasVerbFlagSurface
}

type atlasVerbFlagSurface uint8

const (
	atlasVerbFlagStrictCE atlasVerbFlagSurface = iota
	atlasVerbFlagFullCompatibility
)

type atlasFlagDefault struct {
	Flag  string
	Value string
}

var atlasVerbFlags = []atlasVerbFlagSpec{
	{AtlasCmd: "atlas schema inspect", Path: []string{"schema", "inspect"}, Flags: []string{"--url", "--dev-url", "--schema", "--exclude", "--format"}},
	{AtlasCmd: "atlas schema apply", Path: []string{"schema", "apply"}, Flags: []string{"--url", "--to", "--dev-url", "--dry-run", "--auto-approve", "--schema"}},
	{AtlasCmd: "atlas schema diff", Path: []string{"schema", "diff"}, Flags: []string{"--from", "--to", "--dev-url", "--format", "--schema"}},
	{AtlasCmd: "atlas schema clean", Path: []string{"schema", "clean"}, Flags: []string{"--url", "--dry-run", "--auto-approve", "--format"}},
	{AtlasCmd: "atlas migrate diff", Path: []string{"migrate", "diff"}, Flags: []string{"--to", "--dev-url", "--dir", "--format", "--schema"}},
	{AtlasCmd: "atlas migrate apply", Path: []string{"migrate", "apply"}, Flags: []string{"--url", "--dir", "--dry-run", "--tx-mode", "--revisions-schema"}},
	{
		AtlasCmd: "atlas migrate down",
		Path:     []string{"migrate", "down"},
		Flags: []string{
			"--url", "--dir", "--dev-url", "--to-version", "--to-tag", "--dry-run",
			"--format", "--revisions-schema", "--lock-timeout", "--skip-checks", "--plan",
		},
		Surface: atlasVerbFlagFullCompatibility,
	},
	{
		AtlasCmd: "atlas migrate lint",
		Path:     []string{"migrate", "lint"},
		Flags:    []string{"--dev-url", "--dir", "--dir-format", "--latest"},
		Defaults: []atlasFlagDefault{{Flag: "--dir-format", Value: "atlas"}},
	},
	{
		AtlasCmd: "atlas migrate hash",
		Path:     []string{"migrate", "hash"},
		Flags:    []string{"--dir", "--dir-format"},
		Defaults: []atlasFlagDefault{{Flag: "--dir-format", Value: "atlas"}},
	},
	{
		AtlasCmd: "atlas migrate status",
		Path:     []string{"migrate", "status"},
		Flags:    []string{"--url", "--dir", "--dir-format", "--revisions-schema"},
		Defaults: []atlasFlagDefault{{Flag: "--dir-format", Value: "atlas"}},
	},
	{
		AtlasCmd: "atlas migrate validate",
		Path:     []string{"migrate", "validate"},
		Flags:    []string{"--dev-url", "--dir", "--dir-format"},
		Defaults: []atlasFlagDefault{{Flag: "--dir-format", Value: "atlas"}},
	},
	{
		AtlasCmd: "atlas migrate new",
		Path:     []string{"migrate", "new"},
		Flags:    []string{"--dir", "--dir-format"},
		Defaults: []atlasFlagDefault{{Flag: "--dir-format", Value: "atlas"}},
	},
	{
		AtlasCmd: "atlas migrate set",
		Path:     []string{"migrate", "set"},
		Flags:    []string{"--url", "--dir", "--dir-format", "--revisions-schema"},
		Defaults: []atlasFlagDefault{{Flag: "--dir-format", Value: "atlas"}},
	},
	{AtlasCmd: "atlas migrate import", Path: []string{"migrate", "import"}, Flags: []string{"--from", "--to"}},
}

// AtlasCLIFlagsProbe measures interface-parity depth: for each OSS verb that the
// surface probe already reports as resolving, does `atlas <verb>` on the
// ptah-compat binary accept the Atlas flags a drop-in caller would pass? It
// builds the real ptah-compat CLI and reads each command's `--help`, so it
// flips green on its own as Ptah wires the flags.
type AtlasCLIFlagsProbe struct{}

func (AtlasCLIFlagsProbe) Name() string { return "atlas-cli-flags" }

func (AtlasCLIFlagsProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-cli-flags", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe its flags: " + oneLine(err.Error()), ""}}
	}

	var out []Result
	for _, v := range atlasVerbFlags {
		present, help, cerr := commandFlags(bin, v.Path, v.Surface)
		if cerr != nil {
			out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", Fail,
				"reading `atlas " + strings.Join(v.Path, " ") + " --help` failed: " + oneLine(cerr.Error()), ""})
			continue
		}
		var missing []string
		for _, f := range v.Flags {
			if !present[f] {
				missing = append(missing, f)
			}
		}
		defaultMismatches := missingFlagDefaults(help, v.Defaults)
		switch len(missing) {
		case 0:
			if len(defaultMismatches) != 0 {
				out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", Gap,
					"`atlas " + strings.Join(v.Path, " ") + "` does not advertise Atlas default(s): " +
						strings.Join(defaultMismatches, ", "), "stokaro/ptah#622"})
				continue
			}
			detail := "accepts all essential Atlas flags: " + strings.Join(v.Flags, " ")
			if len(v.Defaults) != 0 {
				detail += "; advertises Atlas defaults: " + strings.Join(formatFlagDefaults(v.Defaults), " ")
			}
			out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", OK,
				detail, ""})
		default:
			sort.Strings(missing)
			out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", Gap,
				"`atlas " + strings.Join(v.Path, " ") + "` does not accept " + strings.Join(missing, ", ") +
					" — the command resolves but is not yet flag-compatible with Atlas", "stokaro/ptah#510"})
		}
	}
	return out
}

var flagPattern = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)

func missingFlagDefaults(help string, defaults []atlasFlagDefault) []string {
	var out []string
	for _, def := range defaults {
		if !helpLineHasFlagDefault(help, def.Flag, def.Value) {
			out = append(out, def.Flag+"="+def.Value)
		}
	}
	return out
}

func helpLineHasFlagDefault(help, flag, value string) bool {
	want := `(default "` + value + `")`
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, flag) && strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func formatFlagDefaults(defaults []atlasFlagDefault) []string {
	out := make([]string, 0, len(defaults))
	for _, def := range defaults {
		out = append(out, def.Flag+"="+def.Value)
	}
	return out
}

// commandFlags returns the set of long flags and raw help text that
// `<bin> <path> --help` advertises, across both the command's own Flags and any
// inherited/global flags. `--help` never executes the command, so this is
// side-effect free.
func commandFlags(
	bin string,
	path []string,
	surface atlasVerbFlagSurface,
) (map[string]bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(append([]string{}, path...), "--help")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = atlasVerbFlagEnvironment(surface)
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, "", err
		}
	}
	set := map[string]bool{}
	help := string(outBytes)
	for _, m := range flagPattern.FindAllString(help, -1) {
		set[m] = true
	}
	return set, help, nil
}

func atlasVerbFlagEnvironment(surface atlasVerbFlagSurface) []string {
	if surface == atlasVerbFlagFullCompatibility {
		return ptahCommandEnvironment()
	}
	return ptahStrictCECommandEnvironment()
}
