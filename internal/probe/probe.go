// Package probe drives Atlas's own migration fixtures through Ptah's public API
// and records, per fixture, the stage at which Ptah diverges from or fails to
// ingest what Atlas produced. It answers "what does Atlas express that Ptah
// cannot yet", which the project's own fixtures cannot answer by construction.
//
// Every probe runs Ptah code that may panic on adversarial input (a known class,
// tracked upstream as stokaro/ptah#128). A panic is caught and reported as its
// own outcome — it is the strongest kind of gap, not a reason to abort the run.
package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Outcome is the verdict for one probe against one fixture.
type Outcome string

const (
	// OK — Ptah handled the fixture the way Atlas would expect.
	OK Outcome = "ok"
	// Gap — Ptah ran without error but does not cover what Atlas expresses.
	Gap Outcome = "gap"
	// Fail — Ptah returned an error on input Atlas accepts.
	Fail Outcome = "fail"
	// Panic — Ptah panicked on input Atlas accepts (stokaro/ptah#128).
	Panic Outcome = "panic"
)

// Result is one (probe, fixture) observation.
type Result struct {
	Probe   string  `json:"probe"`
	Fixture string  `json:"fixture"`
	Stage   string  `json:"stage"`
	Outcome Outcome `json:"outcome"`
	Detail  string  `json:"detail"`
	Issue   string  `json:"issue,omitempty"`
}

// A Probe inspects one fixture and returns one or more Results.
type Probe interface {
	Name() string
	Run(fx Fixture) []Result
}

// Fixture is a vendored Atlas artifact under third_party/atlas.
type Fixture struct {
	// Name is the corpus-relative label, e.g. "migrations/atlasexec-basic".
	Name string
	// Dir is the absolute path to the fixture directory.
	Dir string
	// SQLFiles are the .sql files in the fixture, sorted.
	SQLFiles []string
	// SumFile is the absolute path to atlas.sum, or "" if absent.
	SumFile string
}

// LoadCorpus discovers fixtures under root (the third_party/atlas tree).
// A directory that directly contains .sql files is one fixture.
func LoadCorpus(root string) ([]Fixture, error) {
	byDir := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return nil
		}
		byDir[filepath.Dir(p)] = append(byDir[filepath.Dir(p)], p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Fixture
	for dir, sqls := range byDir {
		sort.Strings(sqls)
		rel, _ := filepath.Rel(root, dir)
		fx := Fixture{Name: filepath.ToSlash(rel), Dir: dir, SQLFiles: sqls}
		if sum := filepath.Join(dir, "atlas.sum"); fileExists(sum) {
			fx.SumFile = sum
		}
		out = append(out, fx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// guard runs fn, converting a panic into a (panicked=true, msg) pair so a Ptah
// panic on one fixture never aborts the whole probe run.
func guard(fn func()) (panicked bool, msg string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			msg = fmt.Sprintf("%v", r)
		}
	}()
	fn()
	return false, ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
