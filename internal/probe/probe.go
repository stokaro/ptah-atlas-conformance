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
	// OK — the observation met its declared contract: Atlas parity, a Ptah-only
	// capability, or an explicitly measured Ptah-better divergence.
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

// NonOK returns every observation that keeps the full conformance gate red.
func NonOK(results []Result) []Result {
	var out []Result
	for _, r := range results {
		if r.Outcome != OK {
			out = append(out, r)
		}
	}
	return out
}

// A Probe inspects one fixture and returns one or more Results.
type Probe interface {
	Name() string
	Run(fx Fixture) []Result
}

// FixtureKind identifies the shape of an Atlas test artifact.
type FixtureKind string

const (
	FixtureKindSQLDir FixtureKind = "sql-dir"
	FixtureKindTxtar  FixtureKind = "txtar"
	FixtureKindHCL    FixtureKind = "hcl"
	FixtureKindOther  FixtureKind = "other"
)

// Fixture is a vendored Atlas artifact under third_party/atlas/upstream or a
// first-party Atlas-compatible regression artifact under testdata/atlas.
type Fixture struct {
	// Name is the corpus-relative label, e.g. "atlasexec/testdata/migrations".
	Name string
	// Kind describes whether the fixture is a SQL directory, txtar file, HCL file,
	// or currently unsupported artifact.
	Kind FixtureKind
	// Dir is the absolute path to the fixture directory.
	Dir string
	// Files are all files that belong to this fixture, sorted.
	Files []string
	// SQLFiles are the .sql files in the fixture, sorted.
	SQLFiles []string
	// SumFile is the absolute path to atlas.sum, or "" if absent.
	SumFile string
}

// LoadCorpus discovers every Atlas test artifact under root. Directories that
// contain SQL migrations or atlas.sum become SQL directory fixtures. Standalone
// txtar, HCL, and other testdata files become single-file fixtures so the report
// can show them explicitly instead of silently ignoring them.
func LoadCorpus(root string) ([]Fixture, error) {
	root = filepath.Clean(root)
	allFiles := map[string]bool{}
	dirFiles := map[string][]string{}
	sqlByDir := map[string][]string{}
	sumByDir := map[string]string{}

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		allFiles[p] = true
		dir := filepath.Dir(p)
		dirFiles[dir] = append(dirFiles[dir], p)
		switch {
		case strings.HasSuffix(p, ".sql"):
			sqlByDir[dir] = append(sqlByDir[dir], p)
		case filepath.Base(p) == "atlas.sum":
			sumByDir[dir] = p
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var out []Fixture
	claimed := map[string]bool{}
	fixtureDirs := map[string]bool{}
	for dir := range sqlByDir {
		fixtureDirs[dir] = true
	}
	for dir := range sumByDir {
		fixtureDirs[dir] = true
	}
	for dir := range fixtureDirs {
		files := append([]string(nil), dirFiles[dir]...)
		sort.Strings(files)
		for _, f := range files {
			claimed[f] = true
		}
		sqls := append([]string(nil), sqlByDir[dir]...)
		sort.Strings(sqls)
		out = append(out, Fixture{
			Name:     relName(root, dir),
			Kind:     FixtureKindSQLDir,
			Dir:      dir,
			Files:    files,
			SQLFiles: sqls,
			SumFile:  sumByDir[dir],
		})
	}

	for file := range allFiles {
		if claimed[file] {
			continue
		}
		out = append(out, Fixture{
			Name:  relName(root, file),
			Kind:  classifyFileFixture(file),
			Dir:   filepath.Dir(file),
			Files: []string{file},
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func classifyFileFixture(file string) FixtureKind {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".txtar":
		return FixtureKindTxtar
	case ".hcl":
		return FixtureKindHCL
	default:
		return FixtureKindOther
	}
}

func relName(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
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
