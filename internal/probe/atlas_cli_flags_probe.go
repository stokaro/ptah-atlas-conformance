package probe

import (
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// atlasVerbFlags is the set of essential Atlas flags each OSS verb accepts — the
// flags a standard drop-in workflow relies on. A resolving `ptah atlas <verb>`
// is not a drop-in until it also accepts these; the atlas-cli-surface probe only
// proves the verb exists, so this probe measures the depth behind it. Source:
// https://atlasgo.io/cli-reference. Cloud/enterprise-leaning flags (--web,
// --plan, --export, --edit) are deliberately omitted to keep this OSS-scoped;
// `schema fmt` (positional paths), `version` and `license` take no such flags.
var atlasVerbFlags = []struct {
	AtlasCmd string
	Path     []string
	Flags    []string
}{
	{"atlas schema inspect", []string{"atlas", "schema", "inspect"}, []string{"--url", "--dev-url", "--schema", "--exclude", "--format"}},
	{"atlas schema apply", []string{"atlas", "schema", "apply"}, []string{"--url", "--to", "--dev-url", "--dry-run", "--auto-approve"}},
	{"atlas schema diff", []string{"atlas", "schema", "diff"}, []string{"--from", "--to", "--dev-url", "--format"}},
	{"atlas schema clean", []string{"atlas", "schema", "clean"}, []string{"--url", "--dry-run", "--auto-approve"}},
	{"atlas migrate diff", []string{"atlas", "migrate", "diff"}, []string{"--to", "--dev-url", "--dir", "--format"}},
	{"atlas migrate apply", []string{"atlas", "migrate", "apply"}, []string{"--url", "--dir", "--dry-run", "--tx-mode"}},
	{"atlas migrate down", []string{"atlas", "migrate", "down"}, []string{"--url", "--dir", "--dev-url", "--to-version", "--to-tag", "--dry-run", "--format", "--revisions-schema", "--lock-timeout", "--skip-checks", "--plan"}},
	{"atlas migrate lint", []string{"atlas", "migrate", "lint"}, []string{"--dev-url", "--dir", "--latest"}},
	{"atlas migrate hash", []string{"atlas", "migrate", "hash"}, []string{"--dir"}},
	{"atlas migrate status", []string{"atlas", "migrate", "status"}, []string{"--url", "--dir"}},
	{"atlas migrate validate", []string{"atlas", "migrate", "validate"}, []string{"--dev-url", "--dir"}},
	{"atlas migrate new", []string{"atlas", "migrate", "new"}, []string{"--dir"}},
	{"atlas migrate set", []string{"atlas", "migrate", "set"}, []string{"--url", "--dir"}},
	{"atlas migrate import", []string{"atlas", "migrate", "import"}, []string{"--from", "--to"}},
}

// AtlasCLIFlagsProbe measures interface-parity depth: for each OSS verb that the
// surface probe already reports as resolving, does `ptah atlas <verb>` accept the
// Atlas flags a drop-in caller would pass? It builds the real Ptah CLI and reads
// each command's `--help`, so it flips green on its own as Ptah wires the flags.
type AtlasCLIFlagsProbe struct{}

func (AtlasCLIFlagsProbe) Name() string { return "atlas-cli-flags" }

func (AtlasCLIFlagsProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahBinary()
	if err != nil {
		return []Result{{"atlas-cli-flags", atlasCLISentinel, "build", Fail,
			"could not build the Ptah CLI to probe its flags: " + oneLine(err.Error()), ""}}
	}

	var out []Result
	for _, v := range atlasVerbFlags {
		present, cerr := commandFlags(bin, v.Path)
		if cerr != nil {
			out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", Fail,
				"reading `ptah " + strings.Join(v.Path, " ") + " --help` failed: " + oneLine(cerr.Error()), ""})
			continue
		}
		var missing []string
		for _, f := range v.Flags {
			if !present[f] {
				missing = append(missing, f)
			}
		}
		switch len(missing) {
		case 0:
			out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", OK,
				"accepts all essential Atlas flags: " + strings.Join(v.Flags, " "), ""})
		default:
			sort.Strings(missing)
			out = append(out, Result{"atlas-cli-flags", v.AtlasCmd, "flags", Gap,
				"`ptah " + strings.Join(v.Path, " ") + "` does not accept " + strings.Join(missing, ", ") +
					" — the command resolves but is not yet flag-compatible with Atlas", "stokaro/ptah#510"})
		}
	}
	return out
}

var flagPattern = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)

// commandFlags returns the set of long flags `<bin> <path> --help` advertises,
// across both the command's own Flags and any inherited/global flags. `--help`
// never executes the command, so this is side-effect free.
func commandFlags(bin string, path []string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(append([]string{}, path...), "--help")
	cmd := exec.CommandContext(ctx, bin, args...)
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, err
		}
	}
	set := map[string]bool{}
	for _, m := range flagPattern.FindAllString(string(outBytes), -1) {
		set[m] = true
	}
	return set, nil
}
