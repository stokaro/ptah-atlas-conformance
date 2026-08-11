package probe

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// runTxtarMigrateLint drives the real `atlas migrate lint` CLI (ptah-compat) over a
// txtar fixture's materialized migration directory. It proves Ptah's own Atlas
// migrate-lint behavior end to end — the default analysis text report, the
// destructive/data-dependent diagnostics, `-- atlas:nolint` suppression, the
// exit-1 failure threshold, and atlas.hcl `--env`/`lint.log` project-config
// resolution — rather than a harness-local reimplementation of Atlas's linter.
// This is the point of stokaro/ptah#651: the fixtures stay green only if Ptah's
// real report renderer (ptah#747) reproduces Atlas's output.
//
// The dev-url replay needs a directly-connectable dev database, so each command
// materializes an ephemeral pure-Go SQLite database (modernc.org/sqlite) into a
// throwaway working directory. That keeps this tier Docker-free and
// Atlas-binary-free. Non-SQLite families need an Atlas HCL-inspect-backed dev
// URL that this offline tier does not provide, and remain explicitly
// unsupported.
func runTxtarMigrateLint(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "lint" {
		return txtarCommandResult{}, false
	}
	if txtarFixtureFamily(fx) != "sqlite" {
		return txtarCommandResult{unsupported: "atlas migrate lint (non-sqlite dev-url replay)"}, true
	}

	plan, ok := txtarPlanMigrateLint(fields[3:])
	if !ok {
		return txtarCommandResult{unsupported: "atlas migrate lint"}, true
	}

	bin, err := ptahCompatAtlasBinary()
	if err != nil {
		// A build failure here is environmental (the go build ./... gate catches
		// genuine breakage); degrade to unsupported rather than a false red.
		return txtarCommandResult{unsupported: "atlas migrate lint (ptah-compat CLI unavailable: " + oneLine(err.Error()) + ")"}, true
	}

	run, err := txtarExecMigrateLint(runtime, bin, plan)
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	if plan.redirect != "" {
		runtime.files[plan.redirect] = run.stdout
		runtime.addParentDirs(plan.redirect)
		return txtarCommandResult{stderr: run.stderr, failed: run.failed, err: run.err}, true
	}
	return txtarCommandResult{stdout: run.stdout, stderr: run.stderr, failed: run.failed, err: run.err}, true
}

// txtarMigrateLintPlan is the parsed shell form of a `migrate lint` command:
// the flags to forward to the Ptah CLI (with the dev-url still holding the Atlas
// `URL` placeholder) and any `> file` stdout redirect.
type txtarMigrateLintPlan struct {
	cliArgs  []string
	redirect string
}

// txtarMigrateLintValueFlags are the `migrate lint` flags Ptah's CLI accepts,
// each consuming the following token as its value. Any flag outside this set
// makes the command unsupported (a Gap) instead of a hard failure, so a future
// fixture exercising an unmapped flag degrades honestly rather than turning red.
var txtarMigrateLintValueFlags = map[string]bool{
	"--dir":        true,
	"--dev-url":    true,
	"--dir-format": true,
	"--format":     true,
	"--latest":     true,
	"--git-base":   true,
	"--git-dir":    true,
	"--env":        true,
	"--config":     true,
	"-c":           true,
	"--var":        true,
}

func txtarPlanMigrateLint(fields []string) (txtarMigrateLintPlan, bool) {
	var plan txtarMigrateLintPlan
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		switch {
		case field == ">":
			if i+1 >= len(fields) {
				return txtarMigrateLintPlan{}, false
			}
			plan.redirect = fields[i+1]
			i++
		case txtarMigrateLintValueFlags[field]:
			if i+1 >= len(fields) {
				return txtarMigrateLintPlan{}, false
			}
			plan.cliArgs = append(plan.cliArgs, field, fields[i+1])
			i++
		case strings.HasPrefix(field, "-") && strings.Contains(field, "="):
			name, _, _ := strings.Cut(field, "=")
			if !txtarMigrateLintValueFlags[name] {
				return txtarMigrateLintPlan{}, false
			}
			plan.cliArgs = append(plan.cliArgs, field)
		default:
			// A bare flag we do not map, or an unexpected positional argument.
			return txtarMigrateLintPlan{}, false
		}
	}
	return plan, true
}

// txtarMigrateLintRun is the outcome of one real `atlas migrate lint`
// invocation on the ptah-compat binary.
type txtarMigrateLintRun struct {
	stdout string
	stderr string
	failed bool
	err    error
}

func txtarExecMigrateLint(runtime *txtarRuntime, bin string, plan txtarMigrateLintPlan) (txtarMigrateLintRun, error) {
	workdir, err := os.MkdirTemp("", "txtar-migrate-lint-*")
	if err != nil {
		return txtarMigrateLintRun{}, err
	}
	defer func() { _ = os.RemoveAll(workdir) }()

	// A fresh, empty SQLite dev database per invocation: `migrate lint` replays
	// the migrations onto it to derive the schema changes it analyzes.
	devURL := "sqlite://" + filepath.ToSlash(filepath.Join(workdir, "__ptah_dev.db"))
	if err := txtarMaterializeLintFiles(runtime, workdir, plan.redirect, devURL); err != nil {
		return txtarMigrateLintRun{}, err
	}

	args := append([]string{"migrate", "lint"}, txtarSubstituteDevURL(plan.cliArgs, devURL)...)
	stdout, stderr, runErr := commandStreamsStrictCE(bin, args, workdir)
	run := txtarMigrateLintRun{stdout: stdout, stderr: stderr}
	if runErr == nil {
		return run, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// A non-zero exit is the linter's failure-threshold signal, not a harness
		// error: surface it as a failed command so `! atlas migrate lint` matches
		// and a bare command records the finding detail.
		run.failed = true
		run.err = fmt.Errorf("atlas migrate lint exited %d: %s", exitErr.ExitCode(), oneLine(firstNonEmpty(stderr, stdout)))
		return run, nil
	}
	return txtarMigrateLintRun{}, runErr
}

// txtarMaterializeLintFiles writes the fixture's virtual files into workdir so
// the real CLI can read the migration directory and any atlas.hcl. The Atlas
// dev-url placeholder inside an atlas.hcl `dev = "URL"` attribute is rewritten
// to the ephemeral SQLite dev URL, matching how Atlas's own testscript runner
// substitutes URL.
func txtarMaterializeLintFiles(runtime *txtarRuntime, workdir, redirect, devURL string) error {
	for name, content := range runtime.files {
		if name == "stdout" || name == "stderr" || name == redirect {
			continue
		}
		if !txtarSafeRelPath(name) {
			continue
		}
		if path.Base(name) == "atlas.hcl" {
			content = txtarSubstituteHCLDevURL(content, devURL)
		}
		dest := filepath.Join(workdir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// txtarSubstituteDevURL replaces the Atlas `URL` dev-url placeholder on the
// command line with a concrete dev database URL. Both `--dev-url URL` and
// `--dev-url=URL` spellings are handled; any other dev-url value is left intact.
func txtarSubstituteDevURL(args []string, devURL string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == "--dev-url" && i+1 < len(out) && out[i+1] == "URL":
			out[i+1] = devURL
			i++
		case out[i] == "--dev-url=URL":
			out[i] = "--dev-url=" + devURL
		}
	}
	return out
}

// txtarSubstituteHCLDevURL rewrites the quoted Atlas `"URL"` dev-url placeholder
// in an atlas.hcl body to the concrete dev database URL. In the Atlas migrate
// lint fixtures URL appears only as a dev database value, so a scoped token
// replacement is sufficient and avoids reimplementing HCL.
func txtarSubstituteHCLDevURL(content, devURL string) string {
	return strings.ReplaceAll(content, `"URL"`, `"`+devURL+`"`)
}

// txtarSafeRelPath reports whether name is a clean relative path that stays
// inside the materialization root.
func txtarSafeRelPath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	clean := path.Clean(name)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
