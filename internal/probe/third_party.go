package probe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const thirdPartyProbeName = "third-party"

// ThirdPartyRepo is one pinned upstream repository the tier measures against.
// Every field is part of the pin: a run that resolved a different commit, or
// looked at a different directory, is measuring something else.
type ThirdPartyRepo struct {
	// Name is the owner/repository slug, used as the fixture name.
	Name string `json:"name"`
	// CloneURL is fetched with git rather than downloaded as an archive, so
	// git verifies the object hashes against Commit for us.
	CloneURL string `json:"clone_url"`
	// Commit is the full 40-character SHA this tier measures. Only a scheduled
	// job moves it; a pull request that changed it would make a red report say
	// "either Ptah regressed or upstream moved", which is the one ambiguity
	// every tier in this repository is built to avoid.
	Commit string `json:"commit"`
	// License is recorded because nothing from these trees is vendored here;
	// the tier fetches at run time and keeps no copy.
	License string `json:"license"`
	// Config is the repository-relative path to its atlas.hcl.
	Config string `json:"config"`
	// Migrations is the repository-relative migration directory.
	Migrations string `json:"migrations"`
	// Note explains what this repository can and cannot exercise.
	Note string `json:"note"`
}

// ThirdPartyRun carries one tier run: the observations, the Atlas oracle they
// were measured against, and whether the run could be measured at all.
type ThirdPartyRun struct {
	// Results are the tier's observations, in repository then command order.
	Results []Result
	// AtlasVersion is the oracle binary's own version line, stamped into the
	// report so the committed artifact records which Atlas produced it.
	AtlasVersion string
	// Infrastructure is set when the tier could not measure Ptah at all --
	// no oracle, no compatibility binary, or an upstream tree that would not
	// fetch. A caller reports it and writes no report: a red row must mean
	// Ptah, and a row that means "GitHub was down" would spend the meaning.
	Infrastructure error
}

// ParseThirdPartyRepos parses and validates the pinned repository ledger. Every
// field is required, and Commit must be a full 40-character SHA: an abbreviated
// or symbolic revision would let the tree the tier measures change without the
// ledger changing, which is the whole point of pinning.
func ParseThirdPartyRepos(data []byte) ([]ThirdPartyRepo, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var repos []ThirdPartyRepo
	if err := dec.Decode(&repos); err != nil {
		return nil, fmt.Errorf("parse third-party repository ledger: %w", err)
	}
	if len(repos) == 0 {
		return nil, errors.New("parse third-party repository ledger: no repositories listed")
	}
	seen := map[string]bool{}
	for i, r := range repos {
		for _, field := range []struct{ name, value string }{
			{"name", r.Name}, {"clone_url", r.CloneURL}, {"commit", r.Commit},
			{"license", r.License}, {"config", r.Config}, {"migrations", r.Migrations},
			{"note", r.Note},
		} {
			if field.value == "" {
				return nil, fmt.Errorf("parse third-party repository ledger: entry %d: %s is empty", i, field.name)
			}
		}
		if len(r.Commit) != 40 || strings.TrimLeft(r.Commit, "0123456789abcdef") != "" {
			return nil, fmt.Errorf(
				"parse third-party repository ledger: %s: commit %q is not a full lowercase SHA",
				r.Name, r.Commit)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("parse third-party repository ledger: %s listed twice", r.Name)
		}
		seen[r.Name] = true
	}
	return repos, nil
}

// thirdPartyCommand is one offline Atlas invocation measured on both binaries.
// Every command here must need neither a database nor the upstream project's
// own toolchain, because the tier's promise is that it measures Ptah rather
// than the reachability of somebody else's development environment.
type thirdPartyCommand struct {
	// stage names the observation in the report.
	stage string
	// argv is built from the repository entry, without the leading binary.
	argv func(r ThirdPartyRepo) []string
	// witness is the repository-relative file that must survive the command
	// byte for byte. Each of these commands reads or rewrites in place and
	// must produce no change on a tree its own upstream already committed, so
	// the bytes are a stronger statement than the exit code alone.
	witness func(r ThirdPartyRepo) string
}

func thirdPartyCommands() []thirdPartyCommand {
	return []thirdPartyCommand{
		{
			// Re-hashing a directory its author already hashed must be a
			// no-op. A divergence here is a checksum-format difference, which
			// is the single most load-bearing piece of Atlas compatibility:
			// atlas.sum parity is what lets the two binaries share a
			// migration directory at all.
			stage: "migrate hash",
			argv: func(r ThirdPartyRepo) []string {
				return []string{"migrate", "hash", "--dir", "file://" + r.Migrations}
			},
			witness: func(r ThirdPartyRepo) string { return filepath.Join(r.Migrations, "atlas.sum") },
		},
		{
			// Without --dev-url this is a checksum-only read: it replays no
			// migration and needs no database, so it belongs in the offline
			// set while `migrate lint` does not.
			stage: "migrate validate",
			argv: func(r ThirdPartyRepo) []string {
				return []string{"migrate", "validate", "--dir", "file://" + r.Migrations}
			},
			witness: func(r ThirdPartyRepo) string { return filepath.Join(r.Migrations, "atlas.sum") },
		},
		{
			// Formatting a file its author committed must not rewrite it.
			// This is the only command here that reads the project's own
			// atlas.hcl, so it is what measures the config surface.
			stage:   "schema fmt",
			argv:    func(r ThirdPartyRepo) []string { return []string{"schema", "fmt", r.Config} },
			witness: func(r ThirdPartyRepo) string { return r.Config },
		},
	}
}

// RunThirdParty measures ptah-compat against pinned upstream repositories that
// use Atlas, with the pinned Atlas CE binary as the oracle for every command.
// Only commands that need no database and none of the upstream project's own
// toolchain are run; see thirdPartyCommands for why that boundary is where it
// is.
func RunThirdParty() ThirdPartyRun {
	data, err := os.ReadFile("third-party-repos.json")
	if err != nil {
		return ThirdPartyRun{Infrastructure: fmt.Errorf("read third-party repository ledger: %w", err)}
	}
	repos, err := ParseThirdPartyRepos(data)
	if err != nil {
		return ThirdPartyRun{Infrastructure: err}
	}
	atlasBin := resolveCEGatingBinary(DefaultAtlasBinary())
	atlasVersion, err := validatePinnedAtlasBinary(atlasBin)
	if err != nil {
		return ThirdPartyRun{Infrastructure: err}
	}
	compatBin, err := ptahCompatAtlasBinary()
	if err != nil {
		return ThirdPartyRun{
			AtlasVersion:   atlasVersion,
			Infrastructure: fmt.Errorf("build the Ptah compatibility CLI: %w", err),
		}
	}

	var out []Result
	for _, repo := range repos {
		tree, err := fetchThirdPartyRepo(repo)
		if err != nil {
			return ThirdPartyRun{
				AtlasVersion:   atlasVersion,
				Infrastructure: fmt.Errorf("%s: %w", repo.Name, err),
			}
		}
		for _, cmd := range thirdPartyCommands() {
			result, infra := measureThirdPartyCommand(tree, repo, cmd, compatBin, atlasBin)
			if infra != nil {
				return ThirdPartyRun{
					AtlasVersion:   atlasVersion,
					Infrastructure: fmt.Errorf("%s / %s: %w", repo.Name, cmd.stage, infra),
				}
			}
			out = append(out, result)
		}
	}
	return ThirdPartyRun{Results: out, AtlasVersion: atlasVersion}
}

// fetchThirdPartyRepo materializes exactly the pinned commit into a temporary
// tree. It fetches with git rather than downloading an archive so that git
// verifies the object hashes: an archive service returning the wrong tree
// would be indistinguishable from the right one.
func fetchThirdPartyRepo(repo ThirdPartyRepo) (string, error) {
	dir, err := os.MkdirTemp("", "third-party-")
	if err != nil {
		return "", fmt.Errorf("scratch directory: %w", err)
	}
	steps := [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", repo.CloneURL},
		{"fetch", "--quiet", "--depth", "1", "origin", repo.Commit},
		{"checkout", "--quiet", "FETCH_HEAD"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// A fetch must never stop for credentials on a runner: an interactive
		// prompt reads as a hang rather than as the failure it is.
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, oneLine(string(output)))
		}
	}
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolve fetched HEAD: %w", err)
	}
	// The fetch asked for the pinned commit by name, so this can only differ
	// if git resolved something else. Asserting it costs one process and turns
	// a silent mismeasurement into a refusal.
	if got := strings.TrimSpace(string(head)); got != repo.Commit {
		return "", fmt.Errorf("fetched %s, want the pinned %s", got, repo.Commit)
	}
	return dir, nil
}

// measureThirdPartyCommand runs one command on both binaries in independent
// copies of the tree and compares what each left behind. The error return is
// for a harness failure -- a copy that would not be made -- which is not an
// observation about Ptah.
func measureThirdPartyCommand(
	tree string, repo ThirdPartyRepo, cmd thirdPartyCommand, compatBin, atlasBin string,
) (Result, error) {
	compatTree, err := copyTree(tree)
	if err != nil {
		return Result{}, fmt.Errorf("copy tree for ptah-compat: %w", err)
	}
	atlasTree, err := copyTree(tree)
	if err != nil {
		return Result{}, fmt.Errorf("copy tree for the Atlas oracle: %w", err)
	}
	argv := cmd.argv(repo)
	witness := cmd.witness(repo)

	compatExit, compatOut := runThirdPartyBinary(compatBin, compatTree, argv)
	atlasExit, atlasOut := runThirdPartyBinary(atlasBin, atlasTree, argv)

	compatWitness, compatErr := os.ReadFile(filepath.Join(compatTree, witness))
	atlasWitness, atlasErr := os.ReadFile(filepath.Join(atlasTree, witness))
	original, originalErr := os.ReadFile(filepath.Join(tree, witness))
	if originalErr != nil {
		return Result{}, fmt.Errorf("read the upstream %s: %w", witness, originalErr)
	}

	result := Result{Probe: thirdPartyProbeName, Fixture: repo.Name, Stage: cmd.stage}
	switch {
	case atlasErr != nil:
		// The oracle could not leave the witness behind, so there is nothing
		// to measure Ptah against. Reported rather than raised: with a pinned
		// commit this means the pin is wrong, which is this tier's problem to
		// fix and not a transient one.
		result.Outcome = Fail
		result.Detail = fmt.Sprintf(
			"the Atlas oracle left no %s (exit %d): %s", witness, atlasExit, oneLine(atlasOut))
	case atlasExit != 0:
		result.Outcome = Fail
		result.Detail = fmt.Sprintf(
			"the Atlas oracle refused the upstream tree (exit %d): %s", atlasExit, oneLine(atlasOut))
	case !bytes.Equal(atlasWitness, original):
		result.Outcome = Fail
		result.Detail = fmt.Sprintf(
			"the Atlas oracle rewrote %s on a tree its own author committed, so the file cannot"+
				" anchor a comparison", witness)
	case compatErr != nil:
		result.Outcome = Gap
		result.Detail = fmt.Sprintf(
			"ptah-compat left no %s where Atlas left one (exit %d): %s",
			witness, compatExit, oneLine(compatOut))
	case compatExit != atlasExit:
		result.Outcome = Gap
		result.Detail = fmt.Sprintf(
			"ptah-compat exited %d where Atlas exited %d: %s",
			compatExit, atlasExit, oneLine(compatOut))
	case !bytes.Equal(compatWitness, original):
		result.Outcome = Gap
		result.Detail = fmt.Sprintf(
			"ptah-compat rewrote %s, which Atlas left byte-identical", witness)
	default:
		result.Outcome = OK
		result.Detail = fmt.Sprintf(
			"exit %d on both binaries; %s byte-identical to the upstream commit",
			atlasExit, witness)
	}
	return result, nil
}

// runThirdPartyBinary runs one measured invocation and returns its exit code
// and combined output. A binary that could not start is reported as a non-zero
// exit with the reason in the output, because the comparison above only needs
// to know that the two sides disagreed and why.
func runThirdPartyBinary(bin, dir string, argv []string) (int, string) {
	cmd := exec.Command(bin, argv...)
	cmd.Dir = dir
	// The measured commands read no network and no login state; a developer's
	// real Atlas session must not be able to change what this observes.
	cmd.Env = append(os.Environ(),
		"ATLAS_NO_UPDATE_NOTIFIER=1", "ATLAS_NO_ANON_TELEMETRY=1", "HOME="+dir)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(output)
	}
	if err != nil {
		return -1, err.Error()
	}
	return 0, string(output)
}

// copyTree makes an independent copy so the two binaries cannot observe each
// other's writes. The measured commands rewrite in place, so sharing one tree
// would make whichever ran second measure the first one's output.
func copyTree(src string) (string, error) {
	dst, err := os.MkdirTemp("", "third-party-run-")
	if err != nil {
		return "", err
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		return "", err
	}
	return dst, nil
}
