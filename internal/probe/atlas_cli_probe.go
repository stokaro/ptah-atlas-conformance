package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// atlasCLISentinel is the first-party marker fixture that owns the CLI-surface
// probe's single emission. See testdata/atlas/_capability/atlas-cli/SENTINEL.
const atlasCLISentinel = "_capability/atlas-cli/SENTINEL"

// CLIVerb is one Atlas CLI command and the `ptah atlas ...` command that must
// exist for Ptah to be a drop-in replacement for it.
type CLIVerb struct {
	// AtlasCmd is the upstream command, e.g. "atlas migrate diff".
	AtlasCmd string
	// Path is the ptah command token path that must resolve, e.g.
	// {"atlas","migrate","diff"} for `ptah atlas migrate diff`.
	Path []string
	// OSS is true for Apache-2.0 Atlas commands (parity targets). Cloud/registry
	// and Pro-only commands are recorded but never gate.
	OSS bool
}

// atlasCLIVerbs is the full Atlas CLI surface. OSS verbs must eventually resolve
// under `ptah atlas ...`; cloud/registry/Pro verbs are out of OSS scope and only
// recorded. The OSS split is grounded in the Apache-2.0 source at the pinned
// commit (ariga/atlas@a5e0aecc): cmd/atlas/internal/cmdapi/migrate.go defines
// exactly apply/diff/hash/import/lint/new/set/status/validate, and schema.go
// defines inspect/apply/diff/fmt/clean. Commands absent from that source at the
// pin (schema test/plan/push, migrate down/rebase/rm/edit/checkpoint/push/test)
// are cloud/registry/Pro and are not OSS drop-in targets.
var atlasCLIVerbs = []CLIVerb{
	{"atlas version", []string{"atlas", "version"}, true},
	{"atlas license", []string{"atlas", "license"}, true},
	{"atlas schema inspect", []string{"atlas", "schema", "inspect"}, true},
	{"atlas schema apply", []string{"atlas", "schema", "apply"}, true},
	{"atlas schema diff", []string{"atlas", "schema", "diff"}, true},
	{"atlas schema fmt", []string{"atlas", "schema", "fmt"}, true},
	{"atlas schema clean", []string{"atlas", "schema", "clean"}, true},
	{"atlas migrate apply", []string{"atlas", "migrate", "apply"}, true},
	{"atlas migrate diff", []string{"atlas", "migrate", "diff"}, true},
	{"atlas migrate hash", []string{"atlas", "migrate", "hash"}, true},
	{"atlas migrate import", []string{"atlas", "migrate", "import"}, true},
	{"atlas migrate lint", []string{"atlas", "migrate", "lint"}, true},
	{"atlas migrate new", []string{"atlas", "migrate", "new"}, true},
	{"atlas migrate set", []string{"atlas", "migrate", "set"}, true},
	{"atlas migrate status", []string{"atlas", "migrate", "status"}, true},
	{"atlas migrate validate", []string{"atlas", "migrate", "validate"}, true},
	// Cloud registry / Pro-only — recorded out of scope, never gated.
	{"atlas schema test", []string{"atlas", "schema", "test"}, false},
	{"atlas schema plan", []string{"atlas", "schema", "plan"}, false},
	{"atlas schema push", []string{"atlas", "schema", "push"}, false},
	{"atlas migrate checkpoint", []string{"atlas", "migrate", "checkpoint"}, false},
	{"atlas migrate down", []string{"atlas", "migrate", "down"}, false},
	{"atlas migrate rebase", []string{"atlas", "migrate", "rebase"}, false},
	{"atlas migrate rm", []string{"atlas", "migrate", "rm"}, false},
	{"atlas migrate edit", []string{"atlas", "migrate", "edit"}, false},
	{"atlas migrate push", []string{"atlas", "migrate", "push"}, false},
	{"atlas migrate test", []string{"atlas", "migrate", "test"}, false},
}

// AtlasCLISurfaceProbe measures the `ptah atlas ...` drop-in CLI surface. For
// each OSS Atlas verb it builds the real Ptah binary and asks whether the
// matching `ptah atlas <verb>` command resolves. It is behavioral, not a static
// assertion: the day Ptah registers an `atlas` namespace, the matching verbs
// flip from gap to ok on their own.
type AtlasCLISurfaceProbe struct{}

func (AtlasCLISurfaceProbe) Name() string { return "atlas-cli-surface" }

func (AtlasCLISurfaceProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahBinary()
	if err != nil {
		return []Result{{"atlas-cli-surface", atlasCLISentinel, "build", Fail,
			"could not build the Ptah CLI to probe its command surface: " + oneLine(err.Error()), ""}}
	}

	var out []Result
	for _, v := range atlasCLIVerbs {
		if !v.OSS {
			out = append(out, Result{"atlas-cli-surface", v.AtlasCmd, "out-of-scope", OK,
				"cloud/registry or Pro-only Atlas command; not an OSS drop-in target", ""})
			continue
		}
		exists, cerr := commandResolves(bin, v.Path)
		switch {
		case cerr != nil:
			out = append(out, Result{"atlas-cli-surface", v.AtlasCmd, "resolve", Fail,
				"probing `ptah " + strings.Join(v.Path, " ") + "` failed: " + oneLine(cerr.Error()), ""})
		case exists:
			out = append(out, Result{"atlas-cli-surface", v.AtlasCmd, "resolve", OK,
				"`ptah " + strings.Join(v.Path, " ") + "` resolves", ""})
		default:
			out = append(out, Result{"atlas-cli-surface", v.AtlasCmd, "resolve", Gap,
				"Ptah has no `" + strings.Join(v.Path, " ") + "` command; the `ptah atlas ...` " +
					"drop-in namespace is unimplemented", "stokaro/ptah#268"})
		}
	}
	return out
}

// commandResolves reports whether `<bin> <path...> --help` resolves to the
// requested command rather than falling back to the root help. cobra prints the
// command's own "Usage:" line (containing the full token path) when the command
// exists, and the root usage otherwise. `--help` never executes the command, so
// this is side-effect free even for verbs like `schema apply`.
func commandResolves(bin string, path []string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(append([]string{}, path...), "--help")
	cmd := exec.CommandContext(ctx, bin, args...)
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		// cobra prints help and exits 0 for `--help`, even for an unknown command
		// (it falls back to the root help), so a non-zero exit is not the "missing"
		// signal — the usage-line scan below is. A *exec.ExitError still carries
		// usable output; a start failure (*exec.Error) or timeout does not, so
		// surface those as an error.
		if _, ok := err.(*exec.ExitError); !ok {
			return false, err
		}
	}
	joined := strings.Join(path, " ")
	for _, line := range strings.Split(string(outBytes), "\n") {
		line = strings.TrimSpace(line)
		// A resolved command's usage line ends with "<binary> <path> [flags]" or
		// "<binary> <path> [command]"; the root fallback never contains the path.
		if strings.Contains(line, " "+joined+" ") || strings.HasSuffix(line, " "+joined) {
			return true, nil
		}
	}
	return false, nil
}

var (
	ptahBinOnce sync.Once
	ptahBinPath string
	ptahBinErr  error
)

// ptahBinary builds the pinned Ptah CLI once per process and returns its path.
// The version is whatever go.mod pins, so the probe always measures the same
// Ptah the library probes measure. An explicit PTAH_BIN overrides the build,
// which keeps CI fast and lets a contributor point at a local build.
func ptahBinary() (string, error) {
	ptahBinOnce.Do(func() {
		if env := strings.TrimSpace(os.Getenv("PTAH_BIN")); env != "" {
			ptahBinPath = env
			return
		}
		dir, err := os.MkdirTemp("", "ptah-cli-*")
		if err != nil {
			ptahBinErr = err
			return
		}
		bin := filepath.Join(dir, "ptah")
		cmd := exec.Command("go", "build", "-o", bin, "github.com/stokaro/ptah/cmd")
		if out, err := cmd.CombinedOutput(); err != nil {
			ptahBinErr = wrapBuildErr(err, out)
			return
		}
		ptahBinPath = bin
	})
	return ptahBinPath, ptahBinErr
}

func wrapBuildErr(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return &buildError{err: err, out: oneLine(string(out))}
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }
