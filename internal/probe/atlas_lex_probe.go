package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stokaro/ptah/core/sqlutil"
)

// atlasGoldenSep is how Atlas's lexer tests serialize a statement list into a
// .golden file: strings.Join(stmts, "\n-- end --\n"). Source:
// ariga/atlas sql/migrate/lex_test.go at the pinned commit.
const atlasGoldenSep = "\n-- end --\n"

// LexSplitParityProbe is a differential check against Atlas's own recorded
// output: for every Atlas lexer fixture that ships a `.golden`, it asks whether
// Ptah's dialect-aware migration statement splitter breaks the same SQL into the
// same statements Atlas does. This matters for drop-in: if Ptah splits a
// multi-statement migration — a stored function body, a BEGIN ATOMIC block, a
// MySQL DELIMITER section — differently from Atlas, the migration
// executes differently.
//
// The comparison is normalized (comments, trailing semicolons and whitespace are
// stripped on both sides) so it measures statement BOUNDARIES and core content,
// not how each tool preserves comments — Atlas keeps them, Ptah strips them, and
// that difference does not change execution. It is behavioral: as Ptah's splitter
// learns delimiters and body-aware grouping, cases flip to green on their own.
type LexSplitParityProbe struct{}

func (LexSplitParityProbe) Name() string { return "lex-split-parity" }

func (LexSplitParityProbe) Run(fx Fixture) []Result {
	if fx.Kind != FixtureKindSQLDir {
		return nil
	}
	var out []Result
	for _, f := range fx.SQLFiles {
		golden := f + ".golden"
		if _, err := os.Stat(golden); err != nil {
			continue
		}
		rel := fx.Name + "/" + filepath.Base(f)

		if sqlServerLexFixture(rel) {
			out = append(out, Result{"lex-split-parity", rel, "out-of-scope", OK,
				"SQL Server statement delimiting (GO / BEGIN TRY); SQL Server is a Pro Atlas driver, not an OSS drop-in target", ""})
			continue
		}

		in, err := os.ReadFile(f)
		if err != nil {
			out = append(out, Result{"lex-split-parity", rel, "read", Fail, err.Error(), ""})
			continue
		}
		g, err := os.ReadFile(golden)
		if err != nil {
			out = append(out, Result{"lex-split-parity", rel, "read", Fail, err.Error(), ""})
			continue
		}

		var ptahStmts []string
		panicked, pmsg := guard(func() {
			ptahStmts = sqlutil.SplitSQLStatementsForDialect(string(in), atlasLexFixtureDialect(f))
		})
		if panicked {
			out = append(out, Result{"lex-split-parity", rel, "split", Panic,
				"Ptah statement splitter panicked: " + oneLine(pmsg), "stokaro/ptah#128"})
			continue
		}

		ptah := normalizeStmts(ptahStmts)
		atlas := normalizeStmts(strings.Split(string(g), atlasGoldenSep))
		if stmtListsEqual(ptah, atlas) {
			out = append(out, Result{"lex-split-parity", rel, "split", OK,
				fmt.Sprintf("Ptah splits into the same %d statement(s) as Atlas", len(atlas)), ""})
		} else {
			out = append(out, Result{"lex-split-parity", rel, "split", Gap,
				fmt.Sprintf("Ptah splits this into %d statement(s), Atlas into %d — statement boundaries differ "+
					"(delimiter directive, function/atomic body, or embedded-semicolon handling)", len(ptah), len(atlas)),
				"stokaro/ptah#273"})
		}
	}
	return out
}

// atlasLexFixtureDialect maps Atlas's explicit lexer-fixture suffixes to the
// dialect the upstream golden was produced for. The dialect is part of the
// fixture contract: MySQL processes backslash escapes while PostgreSQL standard
// strings do not. Files without an explicit suffix keep Ptah's compatibility
// default.
func atlasLexFixtureDialect(path string) string {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".my.sql"):
		return "mysql"
	case strings.HasSuffix(name, ".pg.sql"):
		return "postgres"
	default:
		return ""
	}
}

// sqlServerLexFixture reports whether a lexer fixture exercises SQL Server
// statement delimiting (the GO batch separator or T-SQL BEGIN TRY). SQL Server
// is a Pro Atlas driver, so it is out of OSS scope.
func sqlServerLexFixture(rel string) bool {
	l := strings.ToLower(rel)
	return strings.Contains(l, "/sqlserver/") ||
		strings.Contains(l, "/lexbegintry/") ||
		strings.Contains(l, "ms_gocmd") ||
		strings.Contains(l, "ms_go-delim")
}

var (
	lexBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lexLineComment  = regexp.MustCompile(`--[^\n]*`)
	lexHashComment  = regexp.MustCompile(`#[^\n]*`)
	lexWhitespace   = regexp.MustCompile(`\s+`)
)

// normalizeStmts strips comments (block, `--`, and MySQL `#`), trailing
// semicolons and collapses whitespace so two statement lists can be compared on
// boundaries and core SQL rather than on comment/semicolon preservation. Both
// sides are normalized identically, so the fact that Ptah drops comments during
// splitting while Atlas keeps them does not by itself count as a difference —
// only genuine boundary or content divergence does. Empty results (a comment-only
// fragment) are dropped.
func normalizeStmts(stmts []string) []string {
	var out []string
	for _, s := range stmts {
		s = lexBlockComment.ReplaceAllString(s, "")
		s = lexLineComment.ReplaceAllString(s, "")
		s = lexHashComment.ReplaceAllString(s, "")
		s = lexWhitespace.ReplaceAllString(s, " ")
		s = strings.TrimSpace(s)
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stmtListsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
