package probe

import (
	"fmt"
	"slices"
	"strings"
	"testing/fstest"

	"ptah.run/migration/lint"
)

// lintAnalyzerSentinel owns the analyzer-catalog probe's emission.
const lintAnalyzerSentinel = "_capability/lint-analyzers/SENTINEL"

// analyzerCase is one Atlas sqlcheck analyzer concern, expressed as a synthetic
// Ptah-format migration that Atlas would flag. The probe feeds it to Ptah's real
// linter and records the fidelity of Ptah's response — which rule covers the
// concern, at what severity, and where — not merely that something fired.
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
	// Note documents why Ptah's covering rule differs from the Atlas code (a
	// "mapped" match) or, for an Unsupported concern, why Ptah's SQL-only linter
	// cannot reach it. Empty for a plain exact-code match.
	Note string
	// Unsupported marks a concern Ptah's SQL-only linter fundamentally cannot
	// analyze without a dev database or data statistics, distinguishing a
	// documented limitation from a fixable miss.
	Unsupported bool
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
//
// Notes on the "mapped" cases record where Ptah covers the concern under a
// different rule code than Atlas uses, so the matrix is explicit about the
// mapping rather than pretending code-for-code identity.
var atlasAnalyzerCatalog = []analyzerCase{
	// Destructive changes (DS).
	{AtlasCode: "DS101", Concern: "drop schema", Dialect: "postgres", SetupSQL: `CREATE SCHEMA s;`, ChangeSQL: `DROP SCHEMA s;`,
		Note: "Ptah groups schema and other database-object drops under DS107."},
	{AtlasCode: "DS102", Concern: "drop table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `DROP TABLE t;`,
		Note: "Ptah's table-drop rule is DS101 (code numbering differs from Atlas)."},
	{AtlasCode: "DS103", Concern: "drop column", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);`, ChangeSQL: `ALTER TABLE t DROP COLUMN c;`,
		Note: "Ptah's column-drop rule is DS102 (code numbering differs from Atlas)."},

	// Data-dependent changes (MF) — Atlas needs a dev DB to know if the table has
	// data; Ptah covers most via its data-dependent (DD) or lock/scan rules.
	{AtlasCode: "MF101", Concern: "add unique constraint on existing column", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, email TEXT);`, ChangeSQL: `ALTER TABLE t ADD CONSTRAINT u UNIQUE (email);`,
		Note: "Ptah has no data-dependent uniqueness analyzer; the PG105 access-exclusive-lock rule covers the same statement."},
	// MF102 (change a non-unique index to unique) is a diff-semantics concern:
	// it can only be expressed by dropping and recreating the index, and any index
	// recreation independently trips a PG concurrency finding, which would mask the
	// real data-dependent gap. It is folded into MF101 rather than probed falsely.
	{AtlasCode: "MF103", Concern: "add non-nullable column without default", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t ADD COLUMN c INT NOT NULL;`,
		Note: "Ptah's data-dependent DD101 rule covers this."},
	{AtlasCode: "MF104", Concern: "modify nullable column to non-nullable", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);`, ChangeSQL: `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`,
		Note: "Ptah's PG303 full-scan rule covers this."},

	// Backward-incompatible changes (BC).
	{AtlasCode: "BC101", Concern: "rename table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t RENAME TO t2;`},
	{AtlasCode: "BC102", Concern: "rename column", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);`, ChangeSQL: `ALTER TABLE t RENAME COLUMN c TO d;`,
		Note: "Ptah reports table and column renames under a single BC101 rename rule."},

	// Constraint deletion (CD). PostgreSQL expresses these as the ANSI
	// DROP CONSTRAINT form, whose type is not recoverable from the SQL, so Ptah
	// reports the untyped DS105 fallback here; the typed CD1xx codes fire on the
	// MySQL-family DROP FOREIGN KEY / CHECK / PRIMARY KEY forms (see MY133).
	{AtlasCode: "CD101", Concern: "drop foreign key", Dialect: "postgres", SetupSQL: `CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE t (id INT, p_id INT, CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id));`, ChangeSQL: `ALTER TABLE t DROP CONSTRAINT fk;`,
		Note: "PostgreSQL uses ANSI DROP CONSTRAINT, whose type is not in the SQL; Ptah's typed CD101 fires on the MySQL DROP FOREIGN KEY form."},
	{AtlasCode: "CD102", Concern: "drop check constraint", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, CONSTRAINT ck CHECK (id > 0));`, ChangeSQL: `ALTER TABLE t DROP CONSTRAINT ck;`,
		Note: "PostgreSQL uses ANSI DROP CONSTRAINT, whose type is not in the SQL; Ptah's typed CD102 fires on the MySQL DROP CHECK form."},
	{AtlasCode: "CD103", Concern: "drop primary key", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, CONSTRAINT pk PRIMARY KEY (id));`, ChangeSQL: `ALTER TABLE t DROP CONSTRAINT pk;`,
		Note: "PostgreSQL uses ANSI DROP CONSTRAINT, whose type is not in the SQL; Ptah's typed CD103 fires on the MySQL DROP PRIMARY KEY form."},

	// PostgreSQL concurrency (PG1).
	{AtlasCode: "PG101", Concern: "create index without CONCURRENTLY", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);`, ChangeSQL: `CREATE INDEX idx ON t (c);`},
	{AtlasCode: "PG102", Concern: "drop index without CONCURRENTLY", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);
CREATE INDEX idx ON t (c);`, ChangeSQL: `DROP INDEX idx;`,
		Note: "Ptah's drop-index rule is PG106; Ptah's own PG102 is an unrelated enum-in-transaction rule."},
	{AtlasCode: "PG103", Concern: "missing atlas:txmode none for CONCURRENTLY", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);`, ChangeSQL: `CREATE INDEX CONCURRENTLY idx ON t (c);`},
	{AtlasCode: "PG104", Concern: "add primary key takes ACCESS EXCLUSIVE lock", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t ADD PRIMARY KEY (id);`},
	{AtlasCode: "PG105", Concern: "add unique constraint takes ACCESS EXCLUSIVE lock", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, email TEXT);`, ChangeSQL: `ALTER TABLE t ADD CONSTRAINT u UNIQUE (email);`},

	// PostgreSQL blocking rewrites / scans (PG3).
	{AtlasCode: "PG301", Concern: "column type change rewrites the table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c INT);`, ChangeSQL: `ALTER TABLE t ALTER COLUMN c TYPE BIGINT;`,
		Note: "Ptah's DS103 column-type-change rule covers this."},
	{AtlasCode: "PG302", Concern: "add column with volatile default rewrites the table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t ADD COLUMN c UUID DEFAULT gen_random_uuid();`},
	{AtlasCode: "PG303", Concern: "modify nullable to non-nullable requires full scan", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c TEXT);`, ChangeSQL: `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`},
	{AtlasCode: "PG304", Concern: "add primary key on nullable columns requires full scan", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t ADD PRIMARY KEY (id);`,
		Note: "Ptah folds add-primary-key hazards into PG104."},
	{AtlasCode: "PG305", Concern: "add check constraint requires full scan", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t ADD CONSTRAINT ck CHECK (id > 0);`},
	{AtlasCode: "PG306", Concern: "add foreign key validates existing rows and blocks writes", Dialect: "postgres", SetupSQL: `CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE t (id INT, p_id INT);`, ChangeSQL: `ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id);`},
	{AtlasCode: "PG307", Concern: "change table logging mode rewrites the table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t SET UNLOGGED;`},
	{AtlasCode: "PG308", Concern: "add trigger takes SHARE ROW EXCLUSIVE lock", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);
CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql;`, ChangeSQL: `CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION f();`},
	{AtlasCode: "PG309", Concern: "add stored generated column rewrites the table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT, c INT);`, ChangeSQL: `ALTER TABLE t ADD COLUMN g INT GENERATED ALWAYS AS (c * 2) STORED;`},
	{AtlasCode: "PG310", Concern: "add identity column rewrites the table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t ADD COLUMN n INT GENERATED ALWAYS AS IDENTITY;`},
	{AtlasCode: "PG311", Concern: "change table access method rewrites the table", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `ALTER TABLE t SET ACCESS METHOD heap2;`},
	{AtlasCode: "PG110", Concern: "create table with non-optimal column alignment", Dialect: "postgres", SetupSQL: `SELECT 1;`, ChangeSQL: `CREATE TABLE t (a BOOLEAN, b BIGINT, c BOOLEAN, d BIGINT);`},

	// MySQL / MariaDB (MY).
	{AtlasCode: "MY101", Concern: "add non-nullable column without default", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT);", ChangeSQL: "ALTER TABLE t ADD COLUMN c INT NOT NULL;"},
	{AtlasCode: "MY102", Concern: "inline REFERENCES on added column has no effect", Dialect: "mysql", SetupSQL: "CREATE TABLE p (id INT PRIMARY KEY);\nCREATE TABLE t (id INT);", ChangeSQL: "ALTER TABLE t ADD COLUMN p_id INT REFERENCES p (id);"},
	{AtlasCode: "MY110", Concern: "remove enum value requires table copy", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, c ENUM('a','b'));", ChangeSQL: "ALTER TABLE t MODIFY COLUMN c ENUM('a');",
		Note: "Ptah flags the underlying column rewrite (DS103 / MY101), not the specific enum-copy concern."},
	{AtlasCode: "MY112", Concern: "insert enum value not at the end requires table copy", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, c ENUM('a','b'));", ChangeSQL: "ALTER TABLE t MODIFY COLUMN c ENUM('a','x','b');",
		Note: "Ptah flags the underlying column rewrite (DS103 / MY101), not the specific enum-copy concern."},
	{AtlasCode: "MY120", Concern: "remove set value requires table copy", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, c SET('a','b'));", ChangeSQL: "ALTER TABLE t MODIFY COLUMN c SET('a');",
		Note: "Ptah flags the underlying column rewrite (DS103 / MY101), not the specific set-copy concern."},
	{AtlasCode: "MY130", Concern: "change column type requires table copy", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, c INT);", ChangeSQL: "ALTER TABLE t MODIFY COLUMN c BIGINT;",
		Note: "Ptah flags the underlying column rewrite (DS103 / MY101), not the MySQL copy-algorithm concern specifically."},
	{AtlasCode: "MY131", Concern: "add foreign key blocks DML", Dialect: "mysql", SetupSQL: "CREATE TABLE p (id INT PRIMARY KEY);\nCREATE TABLE t (id INT, p_id INT);", ChangeSQL: "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (p_id) REFERENCES p (id);"},
	{AtlasCode: "MY132", Concern: "add primary key rebuilds the table", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT);", ChangeSQL: "ALTER TABLE t ADD PRIMARY KEY (id);"},
	{AtlasCode: "MY133", Concern: "drop primary key copies the table and blocks DML", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT PRIMARY KEY);", ChangeSQL: "ALTER TABLE t DROP PRIMARY KEY;",
		Note: "Ptah's typed CD103 primary-key-drop rule covers the MySQL DROP PRIMARY KEY form."},
	{AtlasCode: "MY134", Concern: "add fulltext index blocks DML", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, c TEXT);", ChangeSQL: "ALTER TABLE t ADD FULLTEXT INDEX ft (c);"},
	{AtlasCode: "MY135", Concern: "add spatial index blocks DML", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, g GEOMETRY NOT NULL);", ChangeSQL: "ALTER TABLE t ADD SPATIAL INDEX sp (g);"},
	{AtlasCode: "MY136", Concern: "change table character set rebuilds the table", Dialect: "mysql", SetupSQL: "CREATE TABLE t (id INT, c VARCHAR(10));", ChangeSQL: "ALTER TABLE t CONVERT TO CHARACTER SET utf8mb4;",
		Note: "Ptah's MY101 table-rewrite warning covers the character-set conversion."},

	// SQLite (LT).
	{AtlasCode: "LT101", Concern: "modify nullable to non-nullable without default", Dialect: "sqlite", SetupSQL: `CREATE TABLE t (id INTEGER, c TEXT);`, ChangeSQL: `ALTER TABLE t ALTER COLUMN c SET NOT NULL;`},

	// Transaction safety (TX).
	{AtlasCode: "TX101", Concern: "mixing transactional and non-transactional statements", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `CREATE INDEX CONCURRENTLY idx ON t (id);
ALTER TABLE t ADD COLUMN c INT;`},
	{AtlasCode: "TX201", Concern: "nested transaction block", Dialect: "postgres", SetupSQL: `CREATE TABLE t (id INT);`, ChangeSQL: `BEGIN;
ALTER TABLE t ADD COLUMN c INT;
COMMIT;`},
}

// AtlasLintAnalyzerProbe measures Ptah's analyzer fidelity against Atlas's, one
// synthetic migration per analyzer concern, through Ptah's real LintFS, plus a
// set of cross-cutting fidelity checks (suppression, configuration, attribution).
//
// Per-concern rows form a fidelity matrix: each row records which Ptah rule
// covers the concern, at what severity, and on which line, classifying the match
// as an exact code match, a documented "mapped" code, an intentionally
// unsupported concern (Ptah's SQL-only linter cannot reach it), or a missing gap
// linked to a Ptah issue. Because the recorded code, severity, and line are all
// committed to the report, any drift (a rule renumbered, a severity lowered, a
// covered concern regressing to silence) turns the gate red — this is the
// fidelity guarantee CI consumers need, beyond "some warning fired".
type AtlasLintAnalyzerProbe struct{}

func (AtlasLintAnalyzerProbe) Name() string { return "lint-analyzer-catalog" }

func (AtlasLintAnalyzerProbe) Run(fx Fixture) []Result {
	if fx.Name != lintAnalyzerSentinel {
		return nil
	}
	out := make([]Result, 0, len(atlasAnalyzerCatalog)+8)
	for _, c := range atlasAnalyzerCatalog {
		out = append(out, c.run())
	}
	out = append(out, lintFidelityBehaviorChecks()...)
	return out
}

// changeFile is the migration carrying the offending change. Findings are
// attributed to it alone so a future Ptah rule that fires on the setup DDL (e.g.
// "table created without a primary key") cannot flip a genuine gap to a false OK
// — the probe must measure the change, not its scaffolding.
const lintChangeFile = "0000000002_change.up.sql"

func (c analyzerCase) run() Result {
	files := fstest.MapFS{
		"0000000001_setup.up.sql":    {Data: []byte(c.SetupSQL)},
		"0000000001_setup.down.sql":  {Data: []byte("-- irreversible for the probe\n")},
		lintChangeFile:               {Data: []byte(c.ChangeSQL)},
		"0000000002_change.down.sql": {Data: []byte("-- irreversible for the probe\n")},
	}
	var findings []lint.Finding
	var err error
	panicked, pmsg := guard(func() {
		findings, err = lint.LintFS(files, lint.Options{Dialect: c.Dialect})
	})
	label := "Atlas " + c.AtlasCode + " (" + c.Concern + ")"
	matched := substantiveChangeFindings(findings, lintChangeFile)
	switch {
	case panicked:
		return Result{"lint-analyzer-catalog", label, c.Dialect, Panic,
			"Ptah linter panicked on the synthetic migration: " + oneLine(pmsg), "stokaro/ptah#128"}
	case err != nil:
		return Result{"lint-analyzer-catalog", label, c.Dialect, Fail,
			"LintFS returned an error: " + oneLine(err.Error()), "stokaro/ptah#270"}
	case len(matched) == 0 && c.Unsupported:
		return Result{"lint-analyzer-catalog", label, c.Dialect, OK,
			"unsupported by a SQL-only linter: " + c.Note, ""}
	case len(matched) == 0:
		return Result{"lint-analyzer-catalog", label, c.Dialect, Gap,
			"missing: Atlas flags this; Ptah emits no substantive finding on the change", "stokaro/ptah#270"}
	default:
		kind := "covered (mapped)"
		if slices.Contains(findingCodes(matched), c.AtlasCode) {
			kind = "covered (exact)"
		}
		detail := kind + ": Ptah " + describeFindings(matched)
		if c.Note != "" {
			detail += " — " + c.Note
		}
		return Result{"lint-analyzer-catalog", label, c.Dialect, OK, detail, ""}
	}
}

// substantiveChangeFindings returns the substantive (non file-convention)
// findings that fired on the change file only. MF rules are Ptah's migration-file
// conventions (naming/pairing) and say nothing about the SQL's safety, so they
// are excluded; findings on any other file (the setup) are excluded too.
func substantiveChangeFindings(findings []lint.Finding, changeFile string) []lint.Finding {
	var out []lint.Finding
	for _, f := range findings {
		if isStructuralRule(f.Rule) {
			continue
		}
		if !strings.Contains(f.File, changeFile) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func findingCodes(findings []lint.Finding) []string {
	codes := make([]string, 0, len(findings))
	for _, f := range findings {
		codes = append(codes, f.Rule)
	}
	return dedup(codes)
}

// describeFindings renders the matched findings as "CODE (severity) at Ln",
// deduplicated and stable, so the report captures the code, severity, and line
// attribution dimensions of fidelity in one gated string.
func describeFindings(findings []lint.Finding) string {
	seen := make(map[string]bool, len(findings))
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		key := fmt.Sprintf("%s (%s) at L%d", f.Rule, f.Severity, f.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, key)
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

func isStructuralRule(rule string) bool { return len(rule) >= 2 && rule[:2] == "MF" }
