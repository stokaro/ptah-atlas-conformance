package probe

import (
	"strings"
	"testing/fstest"

	"github.com/stokaro/ptah/migration/lint"
)

// lintAnalyzerSentinel owns the analyzer-catalog probe's emission.
const lintAnalyzerSentinel = "_capability/lint-analyzers/SENTINEL"

// analyzerCase is one Atlas sqlcheck analyzer concern, expressed as a synthetic
// Ptah-format migration that Atlas would flag. The probe feeds it to Ptah's real
// linter and records whether Ptah emits any substantive (non file-convention)
// finding. It is behavioral: when Ptah gains the rule, the case flips to ok.
type analyzerCase struct {
	// AtlasCode is the upstream analyzer code this case represents.
	AtlasCode string
	// Concern is a short human description.
	Concern string
	// Dialect selects Ptah's dialect-specific rules.
	Dialect string
	// SetupSQL is a prior migration establishing the object (kept separate so the
	// offending change is an ALTER on an existing object, as Atlas sees it).
	SetupSQL string
	// ChangeSQL is the offending up migration.
	ChangeSQL string
}

// atlasAnalyzerCatalog is a representative slice of the Atlas analyzer catalog
// (https://atlasgo.io/lint/analyzers), one synthetic case per concern. Cases
// Ptah already covers (drops, non-concurrent index) verify the probe reads
// findings correctly; the rest map the parity gap.
var atlasAnalyzerCatalog = []analyzerCase{
	// Destructive — Ptah has DS101-103, so these should read as covered.
	{"DS102", "drop table", "postgres", `CREATE TABLE t (id INT);`, `DROP TABLE t;`},
	{"DS103", "drop column", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t DROP COLUMN c;`},
	// Data-dependent — Atlas MF101/MF103/MF104; Ptah has no dev-DB data-dependent family.
	{"MF103", "add non-nullable column without default", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD COLUMN c INT NOT NULL;`},
	{"MF101", "add unique index/constraint on existing column", "postgres", `CREATE TABLE t (id INT, email TEXT);`, `ALTER TABLE t ADD CONSTRAINT u UNIQUE (email);`},
	{"MF104", "modify nullable column to non-nullable", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`},
	// Backward-incompatible — Atlas BC101/BC102.
	{"BC101", "rename table", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t RENAME TO t2;`},
	{"BC102", "rename column", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t RENAME COLUMN c TO d;`},
	// Constraint deletion — Atlas CD101-103.
	{"CD101", "drop foreign key", "postgres", `CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE t (id INT, p_id INT, CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id));`, `ALTER TABLE t DROP CONSTRAINT fk;`},
	{"CD103", "drop primary key", "postgres", `CREATE TABLE t (id INT, CONSTRAINT pk PRIMARY KEY (id));`, `ALTER TABLE t DROP CONSTRAINT pk;`},
	// PostgreSQL concurrency — Ptah has PG101/PG102; PG103 (txmode) it does not.
	{"PG101", "create index without CONCURRENTLY", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `CREATE INDEX idx ON t (c);`},
	{"PG103", "missing atlas:txmode none for CONCURRENTLY", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `CREATE INDEX CONCURRENTLY idx ON t (c);`},
	// PostgreSQL blocking rewrites — Atlas PG301/PG306.
	{"PG306", "add foreign key validates existing rows and blocks writes", "postgres", `CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE t (id INT, p_id INT);`, `ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id);`},
	{"PG301", "column type change rewrites the table", "postgres", `CREATE TABLE t (id INT, c INT);`, `ALTER TABLE t ALTER COLUMN c TYPE BIGINT;`},
	// MySQL — Ptah has MY101; others it does not.
	{"MY102", "inline REFERENCES on added column has no effect", "mysql", "CREATE TABLE p (id INT PRIMARY KEY);\nCREATE TABLE t (id INT);", "ALTER TABLE t ADD COLUMN p_id INT REFERENCES p (id);"},
	// Transaction safety — Atlas TX101.
	{"TX101", "mixing transactional and non-transactional statements", "postgres", `CREATE TABLE t (id INT);`, `CREATE INDEX CONCURRENTLY idx ON t (id);
ALTER TABLE t ADD COLUMN c INT;`},
}

// AtlasLintAnalyzerProbe measures Ptah's lint catalog against Atlas's, one
// synthetic migration per analyzer concern, through Ptah's real LintFS.
type AtlasLintAnalyzerProbe struct{}

func (AtlasLintAnalyzerProbe) Name() string { return "lint-analyzer-catalog" }

func (AtlasLintAnalyzerProbe) Run(fx Fixture) []Result {
	if fx.Name != lintAnalyzerSentinel {
		return nil
	}
	// changeFile is the migration carrying the offending change. Findings are
	// attributed to it alone so a future Ptah rule that fires on the setup DDL
	// (e.g. "table created without a primary key") cannot flip a genuine gap to a
	// false OK — the probe must measure the change, not its scaffolding.
	const changeFile = "0000000002_change.up.sql"
	var out []Result
	for _, c := range atlasAnalyzerCatalog {
		files := fstest.MapFS{
			"0000000001_setup.up.sql":    {Data: []byte(c.SetupSQL)},
			"0000000001_setup.down.sql":  {Data: []byte("-- irreversible for the probe\n")},
			changeFile:                   {Data: []byte(c.ChangeSQL)},
			"0000000002_change.down.sql": {Data: []byte("-- irreversible for the probe\n")},
		}
		var findings []lint.Finding
		var err error
		panicked, pmsg := guard(func() {
			findings, err = lint.LintFS(files, lint.Options{Dialect: c.Dialect})
		})
		content := changeContentCodes(findings, changeFile)
		label := "Atlas " + c.AtlasCode + " (" + c.Concern + ")"
		switch {
		case panicked:
			out = append(out, Result{"lint-analyzer-catalog", label, c.Dialect, Panic,
				"Ptah linter panicked on the synthetic migration: " + oneLine(pmsg), "stokaro/ptah#128"})
		case err != nil:
			out = append(out, Result{"lint-analyzer-catalog", label, c.Dialect, Fail,
				"LintFS returned an error: " + oneLine(err.Error()), "stokaro/ptah#270"})
		case len(content) > 0:
			out = append(out, Result{"lint-analyzer-catalog", label, c.Dialect, OK,
				"Ptah flags this change: " + joinCodes(content), ""})
		default:
			out = append(out, Result{"lint-analyzer-catalog", label, c.Dialect, Gap,
				"Atlas " + c.AtlasCode + " flags this; Ptah emits no substantive finding on the change", "stokaro/ptah#270"})
		}
	}
	return out
}

// changeContentCodes returns the substantive (non file-convention) rule codes
// that fired on the change file only. MF rules are Ptah's migration-file
// conventions (naming/pairing) and say nothing about the SQL's safety, so they
// are excluded; findings on any other file (the setup) are excluded too.
func changeContentCodes(findings []lint.Finding, changeFile string) []string {
	var codes []string
	for _, f := range findings {
		if isStructuralRule(f.Rule) {
			continue
		}
		if !strings.Contains(f.File, changeFile) {
			continue
		}
		codes = append(codes, f.Rule)
	}
	return dedup(codes)
}

func isStructuralRule(rule string) bool { return len(rule) >= 2 && rule[:2] == "MF" }

func joinCodes(codes []string) string {
	if len(codes) == 0 {
		return "(none)"
	}
	out := codes[0]
	for _, c := range codes[1:] {
		out += ", " + c
	}
	return out
}
