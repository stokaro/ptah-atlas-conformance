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
// recorded. The split is grounded in Atlas's documented open CLI feature
// surface and current CLI reference. In particular, `atlas migrate down` is an
// OSS versioned-migration command even though Ptah still has behavior and flag
// gaps behind its resolving command path.
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
	{"atlas migrate down", []string{"atlas", "migrate", "down"}, true},
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
		wantUsage := "ptah " + strings.Join(v.Path, " ")
		exists, cerr := commandResolves(bin, v.Path, wantUsage)
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
					"drop-in namespace is unimplemented", "stokaro/ptah#510"})
		}
	}
	return out
}

// AtlasCompatBinarySurfaceProbe measures the binary-level drop-in surface. It
// builds Ptah's compatibility binary under the executable name `atlas`, then
// verifies the same OSS command paths without the native `ptah atlas` prefix.
type AtlasCompatBinarySurfaceProbe struct{}

func (AtlasCompatBinarySurfaceProbe) Name() string { return "atlas-compat-binary-surface" }

func (AtlasCompatBinarySurfaceProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}
	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-compat-binary-surface", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe its command surface: " + oneLine(err.Error()), ""}}
	}

	var out []Result
	for _, v := range atlasCLIVerbs {
		if !v.OSS {
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "out-of-scope", OK,
				"cloud/registry or Pro-only Atlas command; not an OSS drop-in target", ""})
			continue
		}
		path := v.Path[1:]
		exists, cerr := commandResolves(bin, path, v.AtlasCmd)
		switch {
		case cerr != nil:
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "resolve", Fail,
				"probing `" + v.AtlasCmd + "` via ptah-compat failed: " + oneLine(cerr.Error()), ""})
		case exists:
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "resolve", OK,
				"`" + v.AtlasCmd + "` resolves through a ptah-compat binary named `atlas`", ""})
		default:
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "resolve", Gap,
				"Ptah compatibility binary named `atlas` has no `" + strings.Join(path, " ") + "` command",
				"stokaro/ptah#514"})
		}
	}
	return out
}

// commandResolves reports whether `<bin> <path...> --help` resolves to the
// requested command rather than falling back to the root help. cobra prints the
// command's own "Usage:" line when the command exists, and the root usage
// otherwise. `wantUsage` must include the intended binary name, so the
// compatibility probe proves `atlas migrate apply` instead of only proving a
// suffix such as `migrate apply`.
func commandResolves(bin string, path []string, wantUsage string) (bool, error) {
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
	for _, line := range strings.Split(string(outBytes), "\n") {
		line = usageCommand(line)
		if line == wantUsage || strings.HasPrefix(line, wantUsage+" ") {
			return true, nil
		}
	}
	return false, nil
}

func usageCommand(line string) string {
	line = strings.TrimSpace(line)
	if rest, ok := strings.CutPrefix(line, "Usage:"); ok {
		return strings.TrimSpace(rest)
	}
	return line
}

var (
	ptahBinOnce sync.Once
	ptahBinPath string
	ptahBinErr  error

	ptahCompatBinOnce sync.Once
	ptahCompatBinPath string
	ptahCompatBinErr  error
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
		ptahBinPath, ptahBinErr = buildPtahCommand("ptah", "github.com/stokaro/ptah/cmd/ptah")
	})
	return ptahBinPath, ptahBinErr
}

// ptahCompatAtlasBinary builds the pinned Ptah compatibility CLI under the
// executable name `atlas`, so help and usage strings are measured in the exact
// drop-in shape existing Atlas scripts use.
func ptahCompatAtlasBinary() (string, error) {
	ptahCompatBinOnce.Do(func() {
		if env := strings.TrimSpace(os.Getenv("PTAH_COMPAT_BIN")); env != "" {
			ptahCompatBinPath = env
			return
		}
		ptahCompatBinPath, ptahCompatBinErr = buildPtahCommand("atlas", "github.com/stokaro/ptah/cmd/ptah-compat")
	})
	return ptahCompatBinPath, ptahCompatBinErr
}

func buildPtahCommand(binaryName, packagePath string) (string, error) {
	dir, err := os.MkdirTemp("", "ptah-cli-*")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, binaryName)
	cmd := exec.Command("go", "build", "-o", bin, packagePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", wrapBuildErr(err, out)
	}
	return bin, nil
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
