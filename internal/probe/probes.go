package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stokaro/ptah/core/parser"
	"github.com/stokaro/ptah/dbschema"
	"github.com/stokaro/ptah/migration/lint"
	"github.com/stokaro/ptah/migration/migratesum"
	"github.com/stokaro/ptah/migration/migrator"
)

// AllProbes is the ordered set the CLI runs.
func AllProbes() []Probe {
	return []Probe{
		CorpusProbe{},
		ParseProbe{},
		MigDirProbe{},
		AtlasTxtarDownProbe{},
		SumProbe{},
		LintProbe{},
	}
}

// CorpusProbe proves every imported Atlas test artifact is visible in the
// generated report. SQL directory fixtures are measured by the concrete probes
// below. Non-SQL Atlas fixtures stay red until the harness grows probes for
// their semantics instead of silently ignoring them.
type CorpusProbe struct{}

func (CorpusProbe) Name() string { return "corpus-inventory" }

func (CorpusProbe) Run(fx Fixture) []Result {
	switch fx.Kind {
	case FixtureKindSQLDir:
		support := len(fx.Files) - len(fx.SQLFiles)
		if fx.SumFile != "" {
			support--
		}
		return []Result{{
			Probe:   "corpus-inventory",
			Fixture: fx.Name,
			Stage:   "import",
			Outcome: OK,
			Detail: fmt.Sprintf("imported SQL directory: %d sql file(s), atlas.sum=%t, %d support file(s)",
				len(fx.SQLFiles), fx.SumFile != "", support),
		}}
	case FixtureKindTxtar:
		return []Result{{
			Probe:   "corpus-inventory",
			Fixture: fx.Name,
			Stage:   "unmeasured",
			Outcome: Gap,
			Detail:  "Atlas txtar integration fixture is vendored but no txtar command/runtime probe consumes it yet",
			Issue:   "stokaro/ptah#285",
		}}
	case FixtureKindHCL:
		return []Result{{
			Probe:   "corpus-inventory",
			Fixture: fx.Name,
			Stage:   "unmeasured",
			Outcome: Gap,
			Detail:  "Atlas HCL fixture is vendored but Ptah has no HCL conformance probe for it yet",
			Issue:   "stokaro/ptah#276",
		}}
	default:
		return []Result{{
			Probe:   "corpus-inventory",
			Fixture: fx.Name,
			Stage:   "unmeasured",
			Outcome: Gap,
			Detail:  "Atlas test artifact is vendored but no conformance probe consumes this fixture kind yet",
			Issue:   "stokaro/ptah#289",
		}}
	}
}

// ParseProbe feeds each Atlas .sql file into Ptah's DDL parser and records
// whether Ptah can turn Atlas-authored SQL into its own AST.
type ParseProbe struct{}

func (ParseProbe) Name() string { return "sql-parse" }

func (ParseProbe) Run(fx Fixture) []Result {
	if fx.Kind != FixtureKindSQLDir {
		return nil
	}
	var out []Result
	for _, f := range fx.SQLFiles {
		rel := fx.Name + "/" + filepath.Base(f)
		data, err := os.ReadFile(f)
		if err != nil {
			out = append(out, Result{"sql-parse", rel, "read", Fail, err.Error(), ""})
			continue
		}
		if strings.Contains(string(data), "-- atlas:txtar") {
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
	if fx.Kind != FixtureKindSQLDir {
		return nil
	}
	if fx.SumFile == "" && !looksVersioned(fx) {
		return nil // not a migration directory
	}
	files, err := migrator.DiscoverMigrationFiles(os.DirFS(fx.Dir), migrator.MigrationDirFormatAuto)
	if err != nil {
		return []Result{{"migdir-ingest", fx.Name, "recognize", Gap,
			"Ptah cannot discover this Atlas migration directory: " + oneLine(err.Error()), "stokaro/ptah#273"}}
	}
	matched := len(files)
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

// AtlasTxtarDownProbe verifies that Atlas txtar files with a down.sql section
// are loaded as migrations with executable, directionally separated SQL. The
// probe stays offline by installing an interceptor that records and handles
// every statement before any database connection is touched.
type AtlasTxtarDownProbe struct{}

func (AtlasTxtarDownProbe) Name() string { return "txtar-down" }

func (AtlasTxtarDownProbe) Run(fx Fixture) []Result {
	if fx.Kind != FixtureKindSQLDir {
		return nil
	}
	if !fixtureContains(fx, "-- atlas:txtar") {
		return nil
	}

	var provider *migrator.FSMigrationProvider
	recorder := &txtarRecorder{}
	var err error
	panicked, pmsg := guard(func() {
		provider, err = migrator.NewFSMigrationProvider(
			os.DirFS(fx.Dir),
			migrator.WithMigrationDirFormat(migrator.MigrationDirFormatAtlas),
			migrator.WithStatementInterceptor(recorder),
		)
	})
	switch {
	case panicked:
		return []Result{{"txtar-down", fx.Name, "load", Panic, oneLine(pmsg), "stokaro/ptah#128"}}
	case err != nil:
		return []Result{{"txtar-down", fx.Name, "load", Gap, "Ptah cannot load Atlas txtar migration: " + oneLine(err.Error()), "stokaro/ptah#290"}}
	}

	var out []Result
	for _, migration := range provider.Migrations() {
		version := fmt.Sprintf("%d", migration.Version)
		recorder.Reset()
		panicked, pmsg = guard(func() {
			err = migration.Up(context.Background(), nil)
		})
		switch {
		case panicked:
			out = append(out, Result{"txtar-down", fx.Name, version + "/up", Panic, oneLine(pmsg), "stokaro/ptah#128"})
			continue
		case err != nil:
			out = append(out, Result{"txtar-down", fx.Name, version + "/up", Fail, "migration.sql failed before statement capture: " + oneLine(err.Error()), "stokaro/ptah#290"})
			continue
		case len(recorder.Statements) == 0:
			out = append(out, Result{"txtar-down", fx.Name, version + "/up", Gap, "migration.sql loaded without executable statements", "stokaro/ptah#290"})
			continue
		case containsStatement(recorder.Statements, "ptah_conformance_txtar_extra_section_sentinel"):
			out = append(out, Result{"txtar-down", fx.Name, version + "/up", Gap, "unknown txtar file section leaked into migration.sql execution", "stokaro/ptah#290"})
			continue
		case containsStatement(recorder.Statements, "ptah_conformance_txtar_down_sentinel"):
			out = append(out, Result{"txtar-down", fx.Name, version + "/up", Gap, "down.sql statements leaked into migration.sql execution", "stokaro/ptah#290"})
			continue
		default:
			out = append(out, Result{"txtar-down", fx.Name, version + "/up", OK, fmt.Sprintf("migration.sql captured %d statement(s)", len(recorder.Statements)), ""})
		}

		recorder.Reset()
		panicked, pmsg = guard(func() {
			err = migration.Down(context.Background(), nil)
		})
		var noDown *migrator.AtlasDownNotImplementedError
		switch {
		case panicked:
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", Panic, oneLine(pmsg), "stokaro/ptah#128"})
		case errors.As(err, &noDown):
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", Gap, "Atlas txtar migration loaded without down.sql execution support", "stokaro/ptah#290"})
		case err != nil:
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", Fail, "down.sql failed before statement capture: " + oneLine(err.Error()), "stokaro/ptah#290"})
		case len(recorder.Statements) == 0:
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", Gap, "down.sql loaded without executable statements", "stokaro/ptah#290"})
		case containsStatement(recorder.Statements, "ptah_conformance_txtar_extra_section_sentinel"):
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", Gap, "unknown txtar file section leaked into down.sql execution", "stokaro/ptah#290"})
		case fixtureContains(fx, "ptah_conformance_txtar_down_sentinel") &&
			!containsStatement(recorder.Statements, "ptah_conformance_txtar_down_sentinel"):
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", Gap, "down.sql sentinel was not captured in down execution", "stokaro/ptah#290"})
		default:
			out = append(out, Result{"txtar-down", fx.Name, version + "/down", OK, fmt.Sprintf("down.sql captured %d statement(s)", len(recorder.Statements)), ""})
		}
	}
	return out
}

type txtarRecorder struct {
	Statements []string
}

func (r *txtarRecorder) ValidateDirectives(map[string]string) error {
	return nil
}

func (r *txtarRecorder) ExecuteStatement(_ context.Context, _ *dbschema.DatabaseConnection, stmt string, _ map[string]string) (bool, error) {
	r.Statements = append(r.Statements, stmt)
	return true, nil
}

func (r *txtarRecorder) Reset() {
	r.Statements = nil
}

func containsStatement(statements []string, needle string) bool {
	for _, statement := range statements {
		if strings.Contains(statement, needle) {
			return true
		}
	}
	return false
}

// SumProbe measures distance to atlas.sum: can Ptah parse the file, and does
// Ptah's own hash of the directory match Atlas's? (#274)
type SumProbe struct{}

func (SumProbe) Name() string { return "sum-compat" }

func (SumProbe) Run(fx Fixture) []Result {
	if fx.Kind != FixtureKindSQLDir {
		return nil
	}
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
				"the remaining gap is hash compatibility, not Atlas file discovery", got),
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
	if fx.Kind != FixtureKindSQLDir {
		return nil
	}
	if len(fx.SQLFiles) == 0 {
		return nil
	}
	if fixtureContains(fx, "-- atlas:txtar") {
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
