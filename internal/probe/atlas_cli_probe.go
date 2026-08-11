package probe

import (
	"bytes"
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

// CLIVerb is one Atlas CLI command and the command token path that must
// resolve on the ptah-compat binary for Ptah to be a drop-in replacement.
type CLIVerb struct {
	// AtlasCmd is the upstream command, e.g. "atlas migrate diff".
	AtlasCmd string
	// Path is the compat command token path that must resolve, e.g.
	// {"migrate","diff"} for `atlas migrate diff` on the ptah-compat binary.
	Path []string
	// OSS is true for Apache-2.0 Atlas commands (parity targets). Cloud/registry
	// and Pro-only commands are recorded but never gate.
	OSS bool
}

// atlasCLIVerbs is the full Atlas CLI surface. OSS verbs must resolve on the
// ptah-compat binary (built as `atlas`); cloud/registry/Pro verbs are out of
// OSS scope and only recorded. The split is grounded in Atlas's documented
// open CLI feature surface and current CLI reference. In particular, `atlas
// migrate down` is Pro-gated by Atlas CE v1.3.0; Ptah exposes it as an open
// extension and measures it in the dedicated non-OSS sentinel and workflow
// tiers. Since stokaro/ptah#850 the ptah-compat binary is the only Atlas-shaped
// surface:
// the main `ptah` binary rejects the `atlas` namespace outright (pinned by the
// cli-exit-behavior probe).
var atlasCLIVerbs = []CLIVerb{
	{"atlas version", []string{"version"}, true},
	{"atlas license", []string{"license"}, true},
	{"atlas schema inspect", []string{"schema", "inspect"}, true},
	{"atlas schema apply", []string{"schema", "apply"}, true},
	{"atlas schema diff", []string{"schema", "diff"}, true},
	{"atlas schema fmt", []string{"schema", "fmt"}, true},
	{"atlas schema clean", []string{"schema", "clean"}, true},
	{"atlas migrate apply", []string{"migrate", "apply"}, true},
	{"atlas migrate diff", []string{"migrate", "diff"}, true},
	{"atlas migrate down", []string{"migrate", "down"}, false},
	{"atlas migrate hash", []string{"migrate", "hash"}, true},
	{"atlas migrate import", []string{"migrate", "import"}, true},
	{"atlas migrate lint", []string{"migrate", "lint"}, true},
	{"atlas migrate new", []string{"migrate", "new"}, true},
	{"atlas migrate set", []string{"migrate", "set"}, true},
	{"atlas migrate status", []string{"migrate", "status"}, true},
	{"atlas migrate validate", []string{"migrate", "validate"}, true},
	// Cloud registry / Pro-only — recorded out of scope, never gated.
	{"atlas schema test", []string{"schema", "test"}, false},
	{"atlas schema plan", []string{"schema", "plan"}, false},
	{"atlas schema push", []string{"schema", "push"}, false},
	{"atlas migrate checkpoint", []string{"migrate", "checkpoint"}, false},
	{"atlas migrate rebase", []string{"migrate", "rebase"}, false},
	{"atlas migrate rm", []string{"migrate", "rm"}, false},
	{"atlas migrate edit", []string{"migrate", "edit"}, false},
	{"atlas migrate push", []string{"migrate", "push"}, false},
	{"atlas migrate test", []string{"migrate", "test"}, false},
}

// AtlasCompatBinarySurfaceProbe measures the binary-level drop-in surface. It
// builds Ptah's compatibility binary under the executable name `atlas`, then
// verifies the OSS command paths in the exact shape Atlas callers use. Since
// stokaro/ptah#850 removed the `ptah atlas ...` namespace, this is the only
// Atlas-shaped command surface Ptah ships.
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
		exists, cerr := commandResolves(bin, v.Path, v.AtlasCmd)
		switch {
		case cerr != nil:
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "resolve", Fail,
				"probing `" + v.AtlasCmd + "` via ptah-compat failed: " + oneLine(cerr.Error()), ""})
		case exists:
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "resolve", OK,
				"`" + v.AtlasCmd + "` resolves through a ptah-compat binary named `atlas`", ""})
		default:
			out = append(out, Result{"atlas-compat-binary-surface", v.AtlasCmd, "resolve", Gap,
				"Ptah compatibility binary named `atlas` has no `" + strings.Join(v.Path, " ") + "` command",
				"stokaro/ptah#514"})
		}
	}
	return out
}

// AtlasCLIUtilityRuntimeProbe proves Atlas utility commands execute, not merely
// resolve to help. These commands have no database side effects and are useful
// smoke tests for Atlas-compatible runtime wiring.
type AtlasCLIUtilityRuntimeProbe struct{}

func (AtlasCLIUtilityRuntimeProbe) Name() string { return "atlas-cli-utility-runtime" }

func (AtlasCLIUtilityRuntimeProbe) Run(fx Fixture) []Result {
	if fx.Name != atlasCLISentinel {
		return nil
	}

	compatBin, err := ptahCompatAtlasBinary()
	if err != nil {
		return []Result{{"atlas-cli-utility-runtime", atlasCLISentinel, "build", Fail,
			"could not build the Ptah compatibility CLI to probe Atlas utility runtime: " + oneLine(err.Error()), ""}}
	}

	checks := []atlasUtilityRuntimeCheck{
		{
			fixture:  "ptah-compat atlas version",
			bin:      compatBin,
			path:     []string{"version"},
			display:  "atlas version",
			contains: []string{"Version:", "Commit:"},
			compat:   true,
		},
		{
			fixture:  "ptah-compat atlas license",
			bin:      compatBin,
			path:     []string{"license"},
			display:  "atlas license",
			contains: []string{"License: MIT", "does not use Atlas source code"},
			compat:   true,
		},
	}

	schemaFmtChecks := []atlasSchemaFmtRuntimeCheck{
		{
			fixture: "ptah-compat atlas schema fmt",
			bin:     compatBin,
			path:    []string{"schema", "fmt"},
			display: "atlas schema fmt",
			compat:  true,
		},
	}

	out := make([]Result, 0, len(checks)+len(schemaFmtChecks))
	for _, check := range checks {
		out = append(out, check.run())
	}
	for _, check := range schemaFmtChecks {
		out = append(out, check.run())
	}
	return out
}

type atlasUtilityRuntimeCheck struct {
	fixture  string
	bin      string
	path     []string
	display  string
	contains []string
	compat   bool
}

func (c atlasUtilityRuntimeCheck) run() Result {
	output, err := commandOutputStrictCE(c.bin, c.path)
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Gap,
				"`" + c.display + "` exited non-zero: " + oneLine(output), "stokaro/ptah#510"}
		}
		return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Fail,
			"executing `" + c.display + "` failed: " + oneLine(err.Error()), ""}
	}

	lower := strings.ToLower(output)
	if strings.Contains(lower, "not implemented") || strings.Contains(lower, "unimplemented") {
		return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Gap,
			"`" + c.display + "` still reports an unimplemented placeholder", "stokaro/ptah#510"}
	}
	for _, want := range c.contains {
		if !strings.Contains(output, want) {
			return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Gap,
				"`" + c.display + "` output does not contain " + want + ": " + oneLine(output), "stokaro/ptah#510"}
		}
	}
	detail := "`" + c.display + "` executes and prints Ptah-owned utility output"
	if c.compat {
		detail = "`" + c.display + "` executes through a ptah-compat binary named `atlas` and prints Ptah-owned utility output"
	}
	return Result{"atlas-cli-utility-runtime", c.fixture, "execute", OK, detail, ""}
}

type atlasSchemaFmtRuntimeCheck struct {
	fixture string
	bin     string
	path    []string
	display string
	compat  bool
}

func (c atlasSchemaFmtRuntimeCheck) run() Result {
	dir, err := os.MkdirTemp("", "atlas-schema-fmt-*")
	if err != nil {
		return Result{"atlas-cli-utility-runtime", c.fixture, "setup", Fail,
			"creating temp schema fmt directory failed: " + oneLine(err.Error()), ""}
	}
	defer os.RemoveAll(dir)

	rootFile := filepath.Join(dir, "a_schema.hcl")
	nestedFile := filepath.Join(dir, "nested", "z_schema.hcl")
	ignoredFile := filepath.Join(dir, "notes.txt")
	if err := os.MkdirAll(filepath.Dir(nestedFile), 0o755); err != nil {
		return Result{"atlas-cli-utility-runtime", c.fixture, "setup", Fail,
			"creating nested schema fmt directory failed: " + oneLine(err.Error()), ""}
	}
	if err := os.WriteFile(rootFile, []byte(`schema "main"{}`+"\n"), 0o600); err != nil {
		return Result{"atlas-cli-utility-runtime", c.fixture, "setup", Fail,
			"writing root HCL fixture failed: " + oneLine(err.Error()), ""}
	}
	if err := os.WriteFile(nestedFile, []byte(`schema "nested"{}`+"\n"), 0o600); err != nil {
		return Result{"atlas-cli-utility-runtime", c.fixture, "setup", Fail,
			"writing nested HCL fixture failed: " + oneLine(err.Error()), ""}
	}
	if err := os.WriteFile(ignoredFile, []byte(`schema "ignored"{}`+"\n"), 0o600); err != nil {
		return Result{"atlas-cli-utility-runtime", c.fixture, "setup", Fail,
			"writing ignored non-HCL fixture failed: " + oneLine(err.Error()), ""}
	}

	output, err := commandOutputDirStrictCE(c.bin, c.path, dir)
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Gap,
				"`" + c.display + "` exited non-zero: " + oneLine(output), "stokaro/ptah#510"}
		}
		return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Fail,
			"executing `" + c.display + "` failed: " + oneLine(err.Error()), ""}
	}
	lower := strings.ToLower(output)
	if strings.Contains(lower, "not implemented") || strings.Contains(lower, "unimplemented") {
		return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Gap,
			"`" + c.display + "` still reports an unimplemented placeholder", "stokaro/ptah#510"}
	}
	if !sameOutputLines(output, []string{"a_schema.hcl", filepath.Join("nested", "z_schema.hcl")}) {
		return Result{"atlas-cli-utility-runtime", c.fixture, "execute", Gap,
			"`" + c.display + "` did not report exactly the formatted HCL files: " + oneLine(output), "stokaro/ptah#510"}
	}

	if ok, detail := schemaFmtFileContentOK(rootFile, `schema "main" {}
`); !ok {
		return Result{"atlas-cli-utility-runtime", c.fixture, "format", Gap, detail, "stokaro/ptah#510"}
	}
	if ok, detail := schemaFmtFileContentOK(nestedFile, `schema "nested" {}
`); !ok {
		return Result{"atlas-cli-utility-runtime", c.fixture, "format", Gap, detail, "stokaro/ptah#510"}
	}
	if ok, detail := schemaFmtFileContentOK(ignoredFile, `schema "ignored"{}`+"\n"); !ok {
		return Result{"atlas-cli-utility-runtime", c.fixture, "format", Gap, detail, "stokaro/ptah#510"}
	}

	detail := "`" + c.display + "` formats .hcl files recursively from the current directory and ignores non-HCL files"
	if c.compat {
		detail = "`" + c.display + "` formats .hcl files recursively through a ptah-compat binary named `atlas`"
	}
	return Result{"atlas-cli-utility-runtime", c.fixture, "execute", OK, detail, ""}
}

func sameOutputLines(output string, want []string) bool {
	got := strings.Fields(strings.TrimSpace(output))
	if len(got) != len(want) {
		return false
	}
	remaining := make(map[string]int, len(want))
	for _, line := range want {
		remaining[line]++
	}
	for _, line := range got {
		if remaining[line] == 0 {
			return false
		}
		remaining[line]--
	}
	return true
}

func schemaFmtFileContentOK(path, want string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "reading schema fmt fixture failed: " + oneLine(err.Error())
	}
	if string(data) != want {
		return false, filepath.Base(path) + " content mismatch after schema fmt"
	}
	return true, ""
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
	cmd.Env = ptahStrictCECommandEnvironment()
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

func commandOutput(bin string, path []string) (string, error) {
	return commandOutputDir(bin, path, "")
}

func commandOutputDir(bin string, path []string, dir string) (string, error) {
	return commandOutputDirWithExactEnv(bin, path, dir, ptahCommandEnvironment())
}

func commandOutputStrictCE(bin string, path []string) (string, error) {
	return commandOutputDirStrictCE(bin, path, "")
}

func commandOutputDirStrictCE(bin string, path []string, dir string) (string, error) {
	return commandOutputDirWithExactEnv(bin, path, dir, ptahStrictCECommandEnvironment())
}

func commandOutputDirWithExactEnv(bin string, path []string, dir string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, path...)
	cmd.Dir = dir
	cmd.Env = env
	outBytes, err := cmd.CombinedOutput()
	return string(outBytes), err
}

func commandOutputWithExactEnv(bin string, path, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, path...)
	cmd.Env = env
	outBytes, err := cmd.CombinedOutput()
	return string(outBytes), err
}

// commandStreams runs bin with args in dir and returns stdout and stderr
// captured separately, along with the process error. Unlike commandOutput it
// keeps the streams apart, which matters when a probe asserts on stdout alone
// (e.g. Atlas migrate-lint writes its analysis report to stdout while genuine
// errors go to stderr).
func commandStreams(bin string, args []string, dir string) (stdout, stderr string, err error) {
	return commandStreamsWithExactEnv(bin, args, dir, ptahCommandEnvironment())
}

func commandStreamsStrictCE(bin string, args []string, dir string) (stdout, stderr string, err error) {
	return commandStreamsWithExactEnv(bin, args, dir, ptahStrictCECommandEnvironment())
}

func commandStreamsWithEnv(
	bin string,
	args []string,
	dir string,
	env []string,
) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(ptahCommandEnvironment(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func commandStreamsWithExactEnv(
	bin string,
	args []string,
	dir string,
	env []string,
) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
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
		ptahBinPath, ptahBinErr = buildPtahCommand("ptah", "go.5x5.cz/ptah/cmd/ptah")
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
		ptahCompatBinPath, ptahCompatBinErr = buildPtahCommand("atlas", "go.5x5.cz/ptah/cmd/ptah-compat")
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
	cmd.Env = append(os.Environ(), "GOWORK=off")
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
