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

// atlasAnalyzerCatalog is the full set of Atlas analyzer concerns that fire by
// default in an OSS build (https://atlasgo.io/lint/analyzers), one synthetic
// migration per concern. The catalog is exhaustive over the default-firing
// families: destructive (DS), data-dependent (MF), backward-incompatible (BC),
// constraint-deletion (CD), PostgreSQL concurrency (PG1) and blocking rewrites
// (PG3), PostgreSQL alignment (PG110), MySQL/MariaDB (MY), SQLite (LT) and
// transaction safety (TX). Deliberately excluded, with reason:
//   - NM (naming conventions): off unless a naming policy is configured, so it
//     does not fire in a default OSS run and is not a default drop-in gap.
//   - SA (SQL injection) / OW (ownership): policy/enterprise analyzers, not part
//     of the default OSS lint pass.
// The probe is behavioral: LintFS decides OK vs gap, so cases Ptah already covers
// (drops, rename, non-concurrent index) read green and prove the probe works.
var atlasAnalyzerCatalog = []analyzerCase{
	// Destructive changes (DS) — Ptah's DS family covers these.
	{"DS101", "drop schema", "postgres", `CREATE SCHEMA s;`, `DROP SCHEMA s;`},
	{"DS102", "drop table", "postgres", `CREATE TABLE t (id INT);`, `DROP TABLE t;`},
	{"DS103", "drop column", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t DROP COLUMN c;`},

	// Data-dependent changes (MF) — need a dev DB to know if the table has data;
	// Ptah's text linter has no data-dependent family.
	{"MF101", "add unique constraint on existing column", "postgres", `CREATE TABLE t (id INT, email TEXT);`, `ALTER TABLE t ADD CONSTRAINT u UNIQUE (email);`},
	// MF102 (change a non-unique index to unique) is a diff-semantics concern:
	// it can only be expressed by dropping and recreating the index, and any index
	// recreation independently trips a PG concurrency finding, which would mask the
	// real data-dependent gap. It is folded into MF101 rather than probed falsely.
	{"MF103", "add non-nullable column without default", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD COLUMN c INT NOT NULL;`},
	{"MF104", "modify nullable column to non-nullable", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`},

	// Backward-incompatible changes (BC) — Ptah's BC family covers renames.
	{"BC101", "rename table", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t RENAME TO t2;`},
	{"BC102", "rename column", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t RENAME COLUMN c TO d;`},

	// Constraint deletion (CD).
	{"CD101", "drop foreign key", "postgres", `CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE t (id INT, p_id INT, CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id));`, `ALTER TABLE t DROP CONSTRAINT fk;`},
	{"CD102", "drop check constraint", "postgres", `CREATE TABLE t (id INT, CONSTRAINT ck CHECK (id > 0));`, `ALTER TABLE t DROP CONSTRAINT ck;`},
	{"CD103", "drop primary key", "postgres", `CREATE TABLE t (id INT, CONSTRAINT pk PRIMARY KEY (id));`, `ALTER TABLE t DROP CONSTRAINT pk;`},

	// PostgreSQL concurrency (PG1) — Ptah has PG101/PG102.
	{"PG101", "create index without CONCURRENTLY", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `CREATE INDEX idx ON t (c);`},
	{"PG102", "drop index without CONCURRENTLY", "postgres", `CREATE TABLE t (id INT, c TEXT);
CREATE INDEX idx ON t (c);`, `DROP INDEX idx;`},
	{"PG103", "missing atlas:txmode none for CONCURRENTLY", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `CREATE INDEX CONCURRENTLY idx ON t (c);`},
	{"PG104", "add primary key takes ACCESS EXCLUSIVE lock", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD PRIMARY KEY (id);`},
	{"PG105", "add unique constraint takes ACCESS EXCLUSIVE lock", "postgres", `CREATE TABLE t (id INT, email TEXT);`, `ALTER TABLE t ADD CONSTRAINT u UNIQUE (email);`},

	// PostgreSQL blocking rewrites / scans (PG3).
	{"PG301", "column type change rewrites the table", "postgres", `CREATE TABLE t (id INT, c INT);`, `ALTER TABLE t ALTER COLUMN c TYPE BIGINT;`},
	{"PG302", "add column with volatile default rewrites the table", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD COLUMN c UUID DEFAULT gen_random_uuid();`},
	{"PG303", "modify nullable to non-nullable requires full scan", "postgres", `CREATE TABLE t (id INT, c TEXT);`, `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`},
	{"PG304", "add primary key on nullable columns requires full scan", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD PRIMARY KEY (id);`},
	{"PG305", "add check constraint requires full scan", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD CONSTRAINT ck CHECK (id > 0);`},
	{"PG306", "add foreign key validates existing rows and blocks writes", "postgres", `CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE t (id INT, p_id INT);`, `ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id);`},
	{"PG307", "change table logging mode rewrites the table", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t SET UNLOGGED;`},
	{"PG308", "add trigger takes SHARE ROW EXCLUSIVE lock", "postgres", `CREATE TABLE t (id INT);
CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql;`, `CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION f();`},
	{"PG309", "add stored generated column rewrites the table", "postgres", `CREATE TABLE t (id INT, c INT);`, `ALTER TABLE t ADD COLUMN g INT GENERATED ALWAYS AS (c * 2) STORED;`},
	{"PG310", "add identity column rewrites the table", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t ADD COLUMN n INT GENERATED ALWAYS AS IDENTITY;`},
	{"PG311", "change table access method rewrites the table", "postgres", `CREATE TABLE t (id INT);`, `ALTER TABLE t SET ACCESS METHOD heap2;`},
	{"PG110", "create table with non-optimal column alignment", "postgres", `SELECT 1;`, `CREATE TABLE t (a BOOLEAN, b BIGINT, c BOOLEAN, d BIGINT);`},

	// MySQL / MariaDB (MY).
	{"MY101", "add non-nullable column without default", "mysql", "CREATE TABLE t (id INT);", "ALTER TABLE t ADD COLUMN c INT NOT NULL;"},
	{"MY102", "inline REFERENCES on added column has no effect", "mysql", "CREATE TABLE p (id INT PRIMARY KEY);\nCREATE TABLE t (id INT);", "ALTER TABLE t ADD COLUMN p_id INT REFERENCES p (id);"},
	{"MY110", "remove enum value requires table copy", "mysql", "CREATE TABLE t (id INT, c ENUM('a','b'));", "ALTER TABLE t MODIFY COLUMN c ENUM('a');"},
	{"MY112", "insert enum value not at the end requires table copy", "mysql", "CREATE TABLE t (id INT, c ENUM('a','b'));", "ALTER TABLE t MODIFY COLUMN c ENUM('a','x','b');"},
	{"MY120", "remove set value requires table copy", "mysql", "CREATE TABLE t (id INT, c SET('a','b'));", "ALTER TABLE t MODIFY COLUMN c SET('a');"},
	{"MY130", "change column type requires table copy", "mysql", "CREATE TABLE t (id INT, c INT);", "ALTER TABLE t MODIFY COLUMN c BIGINT;"},
	{"MY131", "add foreign key blocks DML", "mysql", "CREATE TABLE p (id INT PRIMARY KEY);\nCREATE TABLE t (id INT, p_id INT);", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id);"},
	{"MY132", "add primary key rebuilds the table", "mysql", "CREATE TABLE t (id INT);", "ALTER TABLE t ADD PRIMARY KEY (id);"},
	{"MY133", "drop primary key copies the table and blocks DML", "mysql", "CREATE TABLE t (id INT PRIMARY KEY);", "ALTER TABLE t DROP PRIMARY KEY;"},
	{"MY134", "add fulltext index blocks DML", "mysql", "CREATE TABLE t (id INT, c TEXT);", "ALTER TABLE t ADD FULLTEXT INDEX ft (c);"},
	{"MY135", "add spatial index blocks DML", "mysql", "CREATE TABLE t (id INT, g GEOMETRY NOT NULL);", "ALTER TABLE t ADD SPATIAL INDEX sp (g);"},
	{"MY136", "change table character set rebuilds the table", "mysql", "CREATE TABLE t (id INT, c VARCHAR(10));", "ALTER TABLE t CONVERT TO CHARACTER SET utf8mb4;"},

	// SQLite (LT).
	{"LT101", "modify nullable to non-nullable without default", "sqlite", `CREATE TABLE t (id INTEGER, c TEXT);`, `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`},

	// Transaction safety (TX).
	{"TX101", "mixing transactional and non-transactional statements", "postgres", `CREATE TABLE t (id INT);`, `CREATE INDEX CONCURRENTLY idx ON t (id);
ALTER TABLE t ADD COLUMN c INT;`},
	{"TX201", "nested transaction block", "postgres", `CREATE TABLE t (id INT);`, `BEGIN;
ALTER TABLE t ADD COLUMN c INT;
COMMIT;`},
}

// AtlasLintAnalyzerProbe measures Ptah's lint catalog against Atlas's, one
// synthetic migration per analyzer concern, through Ptah's real LintFS.
//
// Coverage criterion: a case is OK when Ptah's linter emits at least one
// substantive (non file-convention) finding on the change file — i.e. Ptah warns
// the user about this dangerous change at all. The actual Ptah rule code is
// recorded in every OK detail so a reviewer can see whether it is the same
// concern (e.g. DS102 for a table drop) or a coarser destructive warning (e.g.
// DS103 firing on a MySQL enum modification). This deliberately measures "does
// Ptah warn" rather than "does Ptah have byte-for-byte the same analyzer": the
// stricter reading would couple the probe to Ptah's future rule codes and could
// leave a case red forever even after Ptah adds an equivalent rule, defeating
// the auto-flip contract.
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
