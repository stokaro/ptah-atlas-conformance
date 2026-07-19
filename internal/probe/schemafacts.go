package probe

import (
	"regexp"
	"sort"
	"strings"
)

// This file reduces a tool's `CREATE TABLE ...` SQL to a canonical, comparable
// set of column facts, so the differential probe compares what two tools
// *understand* about a schema rather than how they format DDL. It folds the
// systematic, semantically-equivalent spelling differences between Atlas and
// Ptah (serial vs integer+nextval, `character varying` vs `varchar`, `timestamp
// without time zone` vs `timestamp`, inline vs table-level PRIMARY KEY, schema
// qualification and quoting) and leaves genuine differences (a dropped length, a
// missing column, a different nullability) visible.

// tableFacts maps table name -> sorted list of "column: signature" strings.
type tableFacts map[string][]string

var (
	createTableRe = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s(]+)\s*\(`)
	nextvalRe     = regexp.MustCompile(`(?i)\bdefault\s+nextval\([^)]*\)(::regclass)?`)
	wsRe          = regexp.MustCompile(`\s+`)
)

// extractTableFacts parses every CREATE TABLE in sql into canonical column facts.
// It splits the SQL into statements first (both tools emit one statement per
// object) so a greedy match cannot mash two tables' columns together.
func extractTableFacts(sql string) tableFacts {
	out := tableFacts{}
	for _, stmt := range splitStatements(stripComments(sql)) {
		m := createTableRe.FindStringSubmatch(stmt)
		if m == nil {
			continue
		}
		table := normIdent(m[1])
		body := balancedBody(stmt[len(m[0])-1:]) // from the opening paren
		if body == "" {
			continue
		}
		var pkCols []string
		facts := map[string]string{}
		for _, item := range splitTopLevel(body) {
			item = strings.TrimSpace(item)
			low := strings.ToLower(item)
			switch {
			case strings.HasPrefix(low, "primary key"):
				pkCols = append(pkCols, parseColumnList(item)...)
				continue
			case strings.HasPrefix(low, "constraint"), strings.HasPrefix(low, "unique"),
				strings.HasPrefix(low, "foreign key"), strings.HasPrefix(low, "check"):
				// Table-level constraints are compared elsewhere; skip for the
				// column-fact set to keep this focused and low-noise.
				continue
			}
			col, sig, inlinePK := parseColumnDef(item)
			if col == "" {
				continue
			}
			if inlinePK {
				pkCols = append(pkCols, col)
			}
			facts[col] = sig
		}
		var list []string
		for c, s := range facts {
			pk := ""
			if contains(pkCols, c) {
				pk = " pk"
			}
			list = append(list, c+": "+s+pk)
		}
		sort.Strings(list)
		out[table] = list
	}
	return out
}

// parseColumnDef canonicalizes one `"name" type [NOT NULL] [DEFAULT ...] [PRIMARY KEY]`.
func parseColumnDef(def string) (col, sig string, inlinePK bool) {
	def = wsRe.ReplaceAllString(def, " ")
	fields := strings.SplitN(def, " ", 2)
	if len(fields) < 2 {
		return "", "", false
	}
	col = normIdent(fields[0])
	rest := fields[1]
	low := strings.ToLower(rest)

	nullable := !strings.Contains(low, "not null")
	inlinePK = strings.Contains(low, "primary key")
	// A serial/identity column is a synthetic integer with a sequence default;
	// fold both spellings to "serial" before stripping the default.
	isSerial := strings.Contains(low, "serial") || nextvalRe.MatchString(rest)

	// Type = everything up to the first of NOT NULL / DEFAULT / PRIMARY KEY / UNIQUE.
	typ := rest
	for _, kw := range []string{" not null", " default ", " primary key", " unique", " references ", " check"} {
		if i := strings.Index(strings.ToLower(typ), kw); i >= 0 {
			typ = typ[:i]
		}
	}
	typ = canonType(strings.TrimSpace(typ))
	if isSerial {
		typ = "serial"
		nullable = false
	}
	hasDefault := strings.Contains(low, "default ") && !isSerial
	sig = typ
	if nullable {
		sig += " null"
	} else {
		sig += " notnull"
	}
	if hasDefault {
		sig += " hasdefault"
	}
	return col, sig, inlinePK
}

// canonType folds equivalent type spellings.
func canonType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.ReplaceAll(t, `"`, "") // Atlas quotes user types: "public"."enum_x"
	// Strip a schema qualifier on the base type name (user-defined types such as
	// enums): "public.enum_x" -> "enum_x", without touching a "(len)" suffix.
	base, suffix := t, ""
	if i := strings.IndexRune(t, '('); i >= 0 {
		base, suffix = t[:i], t[i:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[i+1:]
	}
	t = strings.TrimSpace(base) + suffix
	t = strings.TrimSuffix(t, " without time zone") // timestamp/time
	repl := map[string]string{
		"character varying": "varchar",
		"character":         "char",
		"integer":           "int",
		"int4":              "int",
		"int8":              "bigint",
		"boolean":           "bool",
		"double precision":  "float8",
		"numeric":           "decimal",
	}
	// longest-key-first replacement of the leading type name
	for _, k := range []string{"character varying", "double precision", "character", "integer", "boolean", "numeric", "int4", "int8"} {
		if strings.HasPrefix(t, k) {
			t = repl[k] + t[len(k):]
			break
		}
	}
	return strings.TrimSpace(t)
}

func normIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if i := strings.LastIndex(s, "."); i >= 0 { // strip schema qualifier
		s = s[i+1:]
	}
	return strings.Trim(s, `"`)
}

func parseColumnList(s string) []string {
	i := strings.Index(s, "(")
	j := strings.LastIndex(s, ")")
	if i < 0 || j < 0 || j < i {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s[i+1:j], ",") {
		out = append(out, normIdent(p))
	}
	return out
}

var (
	lineCommentRe  = regexp.MustCompile(`--[^\n]*`)
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// stripComments removes SQL line and block comments so a per-statement match is
// not blocked by a leading `-- Create "x" table` annotation that both tools emit.
func stripComments(sql string) string {
	sql = blockCommentRe.ReplaceAllString(sql, "")
	sql = lineCommentRe.ReplaceAllString(sql, "")
	return sql
}

// splitStatements splits SQL into statements on semicolons at paren depth 0,
// ignoring semicolons inside string literals.
func splitStatements(sql string) []string {
	var out []string
	depth, start := 0, 0
	inStr := false
	rs := []rune(sql)
	for i, r := range rs {
		switch r {
		case '\'':
			inStr = !inStr
		case '(':
			if !inStr {
				depth++
			}
		case ')':
			if !inStr {
				depth--
			}
		case ';':
			if !inStr && depth == 0 {
				out = append(out, string(rs[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// balancedBody returns the content between the first '(' of s and its matching
// ')', so the column list is isolated from any trailing table options.
func balancedBody(s string) string {
	i := strings.IndexRune(s, '(')
	if i < 0 {
		return ""
	}
	depth := 0
	inStr := false
	rs := []rune(s[i:])
	for j, r := range rs {
		switch r {
		case '\'':
			inStr = !inStr
		case '(':
			if !inStr {
				depth++
			}
		case ')':
			if !inStr {
				depth--
				if depth == 0 {
					return string(rs[1:j])
				}
			}
		}
	}
	return ""
}

// splitTopLevel splits a CREATE TABLE body on commas that are not inside parens.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// diffTableFacts returns human-readable differences between two fact sets, or nil
// if they are equivalent.
func diffTableFacts(atlas, ptah tableFacts) []string {
	var diffs []string
	seen := map[string]bool{}
	for t := range atlas {
		seen[t] = true
	}
	for t := range ptah {
		seen[t] = true
	}
	var tables []string
	for t := range seen {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		a, aok := atlas[t]
		p, pok := ptah[t]
		switch {
		case aok && !pok:
			diffs = append(diffs, "Ptah is missing table "+t)
		case !aok && pok:
			// Ptah has a table Atlas did not report (e.g. a CE-omitted object);
			// not a gap in the Atlas-can-see sense.
			continue
		default:
			for _, d := range diffLists(t, a, p) {
				diffs = append(diffs, d)
			}
		}
	}
	return diffs
}

func diffLists(table string, atlas, ptah []string) []string {
	am := factMap(atlas)
	pm := factMap(ptah)
	var diffs []string
	var cols []string
	seen := map[string]bool{}
	for c := range am {
		seen[c] = true
	}
	for c := range pm {
		seen[c] = true
	}
	for c := range seen {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, c := range cols {
		a, aok := am[c]
		p, pok := pm[c]
		switch {
		case aok && !pok:
			diffs = append(diffs, table+"."+c+": Atlas has ["+a+"], Ptah missing")
		case !aok && pok:
			diffs = append(diffs, table+"."+c+": Ptah has ["+p+"], Atlas did not report it")
		case a != p:
			diffs = append(diffs, table+"."+c+": Atlas ["+a+"] vs Ptah ["+p+"]")
		}
	}
	return diffs
}

func factMap(facts []string) map[string]string {
	m := map[string]string{}
	for _, f := range facts {
		if i := strings.Index(f, ": "); i >= 0 {
			m[f[:i]] = f[i+2:]
		}
	}
	return m
}
