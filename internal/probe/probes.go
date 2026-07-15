package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stokaro/ptah/core/parser"
	"github.com/stokaro/ptah/migration/lint"
	"github.com/stokaro/ptah/migration/migratesum"
	"github.com/stokaro/ptah/migration/migrator"
)

// AllProbes is the ordered set the CLI runs.
func AllProbes() []Probe {
	return []Probe{
		ParseProbe{},
		MigDirProbe{},
		SumProbe{},
		LintProbe{},
	}
}

// ParseProbe feeds each Atlas .sql file into Ptah's DDL parser and records
// whether Ptah can turn Atlas-authored SQL into its own AST.
type ParseProbe struct{}

func (ParseProbe) Name() string { return "sql-parse" }

func (ParseProbe) Run(fx Fixture) []Result {
	var out []Result
	for _, f := range fx.SQLFiles {
		rel := fx.Name + "/" + filepath.Base(f)
		data, err := os.ReadFile(f)
		if err != nil {
			out = append(out, Result{"sql-parse", rel, "read", Fail, err.Error(), ""})
			continue
		}
		var stmts int
		var perr error
		panicked, pmsg := guard(func() {
			list, e := parser.NewParser(string(data)).Parse()
			perr = e
			if list != nil {
				stmts = len(list.Statements)
			}
		})
		// This probe measures Ptah's DDL parser (core/parser), which backs
		// read-db/compare round-trip — NOT migration apply, which execs raw SQL.
		// A gap here means Ptah cannot represent the construct in its AST.
		switch {
		case panicked:
			out = append(out, Result{"sql-parse", rel, "round-trip", Panic,
				"parser panicked on Atlas DDL: " + oneLine(pmsg), "stokaro/ptah#128"})
		case perr != nil && strings.Contains(perr.Error(), "unsupported"):
			out = append(out, Result{"sql-parse", rel, "round-trip", Gap,
				"parser does not model this construct: " + oneLine(perr.Error()), ""})
		case perr != nil:
			out = append(out, Result{"sql-parse", rel, "round-trip", Fail,
				"parse error on Atlas DDL: " + oneLine(perr.Error()), "stokaro/ptah#133"})
		case stmts == 0:
			out = append(out, Result{"sql-parse", rel, "round-trip", Gap,
				"parser returned zero statements for non-empty Atlas DDL", "stokaro/ptah#133"})
		default:
			out = append(out, Result{"sql-parse", rel, "round-trip", OK,
				fmt.Sprintf("parsed %d statement(s)", stmts), ""})
		}
	}
	return out
}

// MigDirProbe checks whether Ptah's migrator even recognizes the files in an
// Atlas migration directory. Atlas names files NNNNNNNNNNNNNN_desc.sql (14-digit
// timestamp, single file); Ptah requires NNNNNNNNNN_desc.(up|down).sql. This is
// the concrete form of "Ptah silently loads zero migrations" (#273).
type MigDirProbe struct{}

func (MigDirProbe) Name() string { return "migdir-ingest" }

func (MigDirProbe) Run(fx Fixture) []Result {
	if fx.SumFile == "" && !looksVersioned(fx) {
		return nil // not a migration directory
	}
	var matched int
	for _, f := range fx.SQLFiles {
		if migrator.ValidateMigrationFileName(filepath.Base(f)) {
			matched++
		}
	}
	total := len(fx.SQLFiles)
	switch {
	case total == 0:
		return nil
	case matched == 0:
		return []Result{{"migdir-ingest", fx.Name, "recognize", Gap,
			fmt.Sprintf("Ptah recognizes 0/%d files; Atlas uses a 14-digit single-file name, "+
				"Ptah requires NNNNNNNNNN_desc.(up|down).sql", total), "stokaro/ptah#273"}}
	case matched < total:
		return []Result{{"migdir-ingest", fx.Name, "recognize", Gap,
			fmt.Sprintf("Ptah recognizes only %d/%d files", matched, total), "stokaro/ptah#273"}}
	default:
		return []Result{{"migdir-ingest", fx.Name, "recognize", OK,
			fmt.Sprintf("all %d files recognized", total), ""}}
	}
}

// SumProbe measures distance to atlas.sum: can Ptah parse the file, and does
// Ptah's own hash of the directory match Atlas's? (#274)
type SumProbe struct{}

func (SumProbe) Name() string { return "sum-compat" }

func (SumProbe) Run(fx Fixture) []Result {
	if fx.SumFile == "" {
		return nil
	}
	data, err := os.ReadFile(fx.SumFile)
	if err != nil {
		return []Result{{"sum-compat", fx.Name, "read", Fail, err.Error(), ""}}
	}
	var out []Result

	// (a) Can Ptah's parser read Atlas's atlas.sum byte stream?
	var atlasSum *migratesum.SumFile
	panicked, pmsg := guard(func() {
		atlasSum, err = migratesum.Parse(data)
	})
	switch {
	case panicked:
		out = append(out, Result{"sum-compat", fx.Name, "parse-sum", Panic, oneLine(pmsg), "stokaro/ptah#128"})
		return out
	case err != nil:
		out = append(out, Result{"sum-compat", fx.Name, "parse-sum", Gap,
			"Ptah cannot parse atlas.sum: " + oneLine(err.Error()), "stokaro/ptah#274"})
	default:
		out = append(out, Result{"sum-compat", fx.Name, "parse-sum", OK,
			fmt.Sprintf("parsed atlas.sum: dir hash + %d entries", len(atlasSum.Entries)), "stokaro/ptah#274"})
	}

	// (b) Does Ptah's own hash of the directory reproduce Atlas's hashes?
	var ptahSum *migratesum.SumFile
	panicked, pmsg = guard(func() {
		ptahSum, err = migratesum.Compute(os.DirFS(fx.Dir))
	})
	switch {
	case panicked:
		out = append(out, Result{"sum-compat", fx.Name, "recompute", Panic, oneLine(pmsg), "stokaro/ptah#128"})
	case err != nil:
		out = append(out, Result{"sum-compat", fx.Name, "recompute", Fail, oneLine(err.Error()), "stokaro/ptah#274"})
	case atlasSum != nil && ptahSum.DirHash == atlasSum.DirHash:
		out = append(out, Result{"sum-compat", fx.Name, "recompute", OK,
			"Ptah dir hash is byte-identical to atlas.sum", ""})
	default:
		got := "0"
		if ptahSum != nil {
			got = fmt.Sprintf("%d", len(ptahSum.Entries))
		}
		out = append(out, Result{"sum-compat", fx.Name, "recompute", Gap,
			fmt.Sprintf("Ptah hashes %s entries here and its dir hash differs from atlas.sum — "+
				"Ptah only hashes NNNNNNNNNN_desc.(up|down).sql files, so it skips Atlas's", got),
			"stokaro/ptah#274"})
	}
	return out
}

// LintProbe runs Ptah's linter over an Atlas migration directory and reports
// what it flags. The destructive-change fixture is the interesting case: Atlas
// classifies DROP TABLE as a destructive change (its DS family); Ptah should
// too. It also exposes whether Ptah can lint a directory whose files it does
// not recognize at all.
type LintProbe struct{}

func (LintProbe) Name() string { return "lint-parity" }

func (LintProbe) Run(fx Fixture) []Result {
	if len(fx.SQLFiles) == 0 {
		return nil
	}
	// Skip pure directive fixtures with no migration semantics.
	if fx.SumFile == "" && !looksVersioned(fx) {
		return nil
	}
	var findings []lint.Finding
	var err error
	panicked, pmsg := guard(func() {
		findings, err = lint.LintFS(os.DirFS(fx.Dir), lint.Options{Dialect: "postgres"})
	})
	switch {
	case panicked:
		return []Result{{"lint-parity", fx.Name, "lint", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	case err != nil:
		return []Result{{"lint-parity", fx.Name, "lint", Fail, oneLine(err.Error()), "stokaro/ptah#270"}}
	}
	// Separate substantive content findings (DS destructive, PG/MY/BC dialect)
	// from structural noise (MF = file naming/pairing). Emitting only structural
	// findings means Ptah flagged the file names, not what the migration does.
	var content, structural []string
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "MF") {
			structural = append(structural, f.Rule)
		} else {
			content = append(content, f.Rule)
		}
	}
	hasDrop := fixtureContains(fx, "DROP TABLE")
	switch {
	case len(content) > 0:
		return []Result{{"lint-parity", fx.Name, "lint", OK,
			"content findings: " + strings.Join(dedup(content), ", "), ""}}
	case hasDrop:
		return []Result{{"lint-parity", fx.Name, "lint", Gap,
			"fixture contains DROP TABLE (Atlas → destructive/DS101) but Ptah emitted only " +
				"file-convention findings (" + strings.Join(dedup(structural), ", ") + "); it flags Atlas's " +
				"file names rather than analyzing their content", "stokaro/ptah#273"}}
	case len(structural) > 0:
		return []Result{{"lint-parity", fx.Name, "lint", Gap,
			"only file-convention findings (" + strings.Join(dedup(structural), ", ") + "); Ptah does not " +
				"analyze the content of Atlas-named files", "stokaro/ptah#273"}}
	default:
		return []Result{{"lint-parity", fx.Name, "lint", Gap, "linter produced no findings", "stokaro/ptah#273"}}
	}
}

func looksVersioned(fx Fixture) bool {
	for _, f := range fx.SQLFiles {
		base := filepath.Base(f)
		if len(base) > 0 && base[0] >= '0' && base[0] <= '9' {
			return true
		}
	}
	return false
}

func fixtureContains(fx Fixture, needle string) bool {
	for _, f := range fx.SQLFiles {
		data, err := os.ReadFile(f)
		if err == nil && strings.Contains(strings.ToUpper(string(data)), strings.ToUpper(needle)) {
			return true
		}
	}
	return false
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
