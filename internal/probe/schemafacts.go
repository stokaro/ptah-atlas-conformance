package probe

import (
	"sort"
	"strings"

	"github.com/stokaro/ptah/core/goschema"
)

// This file reduces a schema to a canonical, comparable set of column facts so
// the differential probe compares what two tools *understand* about a schema
// rather than how they spell DDL. Both sides arrive as a typed
// goschema.Database — Ptah's from live introspection, Atlas's from parsing its
// native HCL `schema inspect` output through Ptah's own core/atlashcl parser —
// so there is no SQL text parsing here; the facts are read off typed fields. It
// folds the systematic, semantically-equivalent representation differences
// (serial vs integer+nextval, `character varying`/`character_varying` vs
// `varchar`, `timestamp without time zone` vs `timestamp`, field-level vs
// table-level primary key, foreign-key action ordering and defaults) and leaves
// genuine differences (a dropped length, a missing column, a different
// nullability, a lost primary-key membership) visible.

// tableFacts maps table name -> sorted list of "object: signature" strings.
type tableFacts map[string][]string

// factsFromDatabase reads canonical schema facts off a typed schema.
func factsFromDatabase(db *goschema.Database) tableFacts {
	if db == nil {
		return tableFacts{}
	}
	tableByStruct := map[string]goschema.Table{}
	for _, t := range db.Tables {
		tableByStruct[t.StructName] = t
	}
	fieldsByStruct := map[string][]goschema.Field{}
	for _, f := range db.Fields {
		fieldsByStruct[f.StructName] = append(fieldsByStruct[f.StructName], f)
	}

	out := tableFacts{}
	for structName, t := range tableByStruct {
		fields := fieldsByStruct[structName]

		// Effective primary key = table-level composite key plus any field
		// marked primary, so inline and table-level PKs compare equal.
		pk := map[string]bool{}
		for _, c := range t.PrimaryKey {
			pk[normIdent(c)] = true
		}
		for _, f := range fields {
			if f.Primary {
				pk[normIdent(f.Name)] = true
			}
		}

		var list []string
		for _, f := range fields {
			list = append(list, normIdent(f.Name)+": "+fieldSignature(f, pk[normIdent(f.Name)]))
			if f.Unique {
				list = append(list, "~unique("+normColumns([]string{f.Name})+"): columns="+normColumns([]string{f.Name}))
			}
			if strings.TrimSpace(f.Check) != "" {
				list = append(list, checkFact(f.Check))
			}
			if strings.TrimSpace(f.Foreign) != "" {
				list = append(list, "~fk("+normIdent(f.Name)+"): "+foreignSignature(f))
			}
		}
		list = append(list, constraintFacts(db.Constraints, tableByStruct, t.Name)...)
		list = append(list, indexFacts(db.Indexes, tableByStruct, t.Name)...)
		sort.Strings(list)
		out[t.Name] = list
	}
	return out
}

// fieldSignature canonicalizes one column: type, nullability, default presence,
// primary-key membership, generated-column expression, and generated-column
// kind. Default constants are compared by normalized value rather than mere
// presence, while non-constant expressions remain visible as expressions.
func fieldSignature(f goschema.Field, isPK bool) string {
	typ := canonType(f.Type)
	serial := isSerial(f)
	if serial {
		typ = "serial"
	}
	sig := typ
	if f.Nullable {
		sig += " null"
	} else {
		sig += " notnull"
	}
	// A serial's sequence default is intrinsic to the type, not a distinct default.
	if def := defaultSignature(f); def != "" && !serial {
		sig += " default=" + def
	}
	if isPK {
		sig += " pk"
	}
	if strings.TrimSpace(f.GeneratedExpression) != "" {
		sig += " generated=" + normExpr(f.GeneratedExpression)
		if kind := normGeneratedKind(f.GeneratedKind); kind != "" {
			sig += " kind=" + kind
		}
	}
	return sig
}

// foreignSignature canonicalizes a field's foreign key: referenced target plus
// referential actions, so action ordering and an omitted default do not read as
// a difference.
func foreignSignature(f goschema.Field) string {
	return "-> " + normRef(f.Foreign) + " del=" + normAction(f.OnDelete) + " upd=" + normAction(f.OnUpdate)
}

func checkFact(expr string) string {
	normalized := normExpr(expr)
	// SQLite does not preserve stable CHECK names through Atlas/Ptah inspect, so
	// the expression is the semantic identity for this differential tier.
	return "~check(" + normalized + "): expr=" + normalized
}

func constraintFacts(constraints []goschema.Constraint, tableByStruct map[string]goschema.Table, tableName string) []string {
	var facts []string
	for _, c := range constraints {
		if normIdent(constraintTable(c, tableByStruct)) != normIdent(tableName) {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(c.Type)) {
		case "UNIQUE":
			cols := normColumns(c.Columns)
			facts = append(facts, "~unique("+cols+"): columns="+cols)
		case "CHECK":
			facts = append(facts, checkFact(c.CheckExpression))
		case "FOREIGN KEY":
			facts = append(facts, "~fk("+normColumns(c.Columns)+"): -> "+normRef(c.ForeignTable+"("+strings.Join(c.ForeignColumnsOrDefault(), ",")+")")+
				" del="+normAction(c.OnDelete)+" upd="+normAction(c.OnUpdate))
		}
	}
	return facts
}

func indexFacts(indexes []goschema.Index, tableByStruct map[string]goschema.Table, tableName string) []string {
	var facts []string
	for _, idx := range indexes {
		if normIdent(indexTable(idx, tableByStruct)) != normIdent(tableName) {
			continue
		}
		cols := normColumns(indexColumns(idx))
		if idx.Unique {
			sig := "columns=" + cols
			if condition := normExpr(idx.Condition); condition != "" {
				sig += " where=" + condition
			}
			facts = append(facts, "~unique("+cols+"): "+sig)
			continue
		}
		sig := "columns=" + cols
		if typ := strings.ToLower(strings.TrimSpace(idx.Type)); typ != "" {
			sig += " type=" + typ
		}
		if condition := normExpr(idx.Condition); condition != "" {
			sig += " where=" + condition
		}
		if include := normColumns(idx.IncludeColumns); include != "" {
			sig += " include=" + include
		}
		facts = append(facts, "~index("+normIdent(idx.Name)+"): "+sig)
	}
	return facts
}

// isSerial folds the two spellings of an auto-incrementing integer: Atlas's
// `serial` type and Ptah's introspected `integer` + `nextval(...)` default.
func isSerial(f goschema.Field) bool {
	return f.AutoInc ||
		strings.Contains(strings.ToLower(f.Type), "serial") ||
		strings.Contains(strings.ToLower(f.DefaultExpr), "nextval")
}

func defaultSignature(f goschema.Field) string {
	if expr := strings.TrimSpace(f.DefaultExpr); expr != "" {
		if value, ok := normDefaultConstant(expr, false); ok {
			return "value(" + normDefaultValueForType(value, f.Type) + ")"
		}
		return "expr(" + normExpr(expr) + ")"
	}
	if f.DefaultSet || strings.TrimSpace(f.Default) != "" {
		if value, ok := normDefaultConstant(f.Default, true); ok {
			return "value(" + normDefaultValueForType(value, f.Type) + ")"
		}
		return "value(" + normLiteral(f.Default) + ")"
	}
	return ""
}

func normDefaultValueForType(value, typ string) string {
	if !isNumericType(typ) {
		return value
	}
	if normalized, ok := normNumber(value); ok {
		return normalized
	}
	return value
}

func isNumericType(typ string) bool {
	typ = canonType(typ)
	return strings.HasPrefix(typ, "int") ||
		strings.HasPrefix(typ, "bigint") ||
		strings.HasPrefix(typ, "smallint") ||
		strings.HasPrefix(typ, "decimal") ||
		strings.HasPrefix(typ, "numeric") ||
		strings.HasPrefix(typ, "float") ||
		strings.HasPrefix(typ, "double")
}

// canonType folds equivalent type spellings, including the underscore forms Atlas
// HCL uses (`character_varying`) and the spaced forms Ptah introspects
// (`character varying`), while preserving a length/precision suffix so a dropped
// length stays visible.
func canonType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.ReplaceAll(t, `"`, "")
	if inner, ok := functionArg(t, "sql"); ok {
		t = inner
	}

	base, suffix := t, ""
	if i := strings.IndexRune(t, '('); i >= 0 {
		base, suffix = t[:i], t[i:]
	}
	base = strings.TrimSpace(base)
	if i := strings.LastIndex(base, "."); i >= 0 { // strip schema qualifier on user types
		base = base[i+1:]
	}
	if base == "tinyint" && suffix == "(1)" {
		base, suffix = "bool", ""
	}
	// Longest-first so "double precision" wins before "double". Both spaced and
	// underscored spellings map to the same canonical name.
	aliases := []struct{ from, to string }{
		{"timestamp without time zone", "timestamp"},
		{"timestamp_without_time_zone", "timestamp"},
		{"time without time zone", "time"},
		{"time_without_time_zone", "time"},
		{"character varying", "varchar"},
		{"character_varying", "varchar"},
		{"double precision", "float8"},
		{"double_precision", "float8"},
		{"character", "char"},
		{"integer", "int"},
		{"int4", "int"},
		{"int8", "bigint"},
		{"tinyint", "bool"},
		{"boolean", "bool"},
		{"numeric", "decimal"},
	}
	for _, a := range aliases {
		if base == a.from {
			base = a.to
			break
		}
	}
	if base == "enum" && suffix != "" {
		suffix = "(" + normEnumValues(strings.Trim(suffix, "()")) + ")"
	}
	return strings.TrimSpace(base + suffix)
}

func normEnumValues(raw string) string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		values = append(values, normLiteral(part))
	}
	return strings.Join(values, ",")
}

// normRef canonicalizes a "table(col)" reference: strip a schema qualifier and
// quotes, lower-case.
func normRef(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	ref = strings.ReplaceAll(ref, `"`, "")
	table, cols := ref, ""
	if i := strings.IndexRune(ref, '('); i >= 0 {
		table, cols = ref[:i], ref[i:]
	}
	if i := strings.LastIndex(table, "."); i >= 0 {
		table = table[i+1:]
	}
	return strings.TrimSpace(table) + cols
}

// normAction folds a referential action, unifying Atlas HCL's underscore
// spelling (`NO_ACTION`, `SET_NULL`) with Ptah's spaced spelling (`NO ACTION`,
// `SET NULL`) and defaulting an empty clause to the SQL default (NO ACTION).
func normAction(a string) string {
	a = strings.ToLower(strings.TrimSpace(a))
	a = strings.ReplaceAll(a, "_", " ")
	if a == "" {
		return "no action"
	}
	return a
}

func constraintTable(c goschema.Constraint, tableByStruct map[string]goschema.Table) string {
	if strings.TrimSpace(c.Table) != "" {
		return c.Table
	}
	if t, ok := tableByStruct[c.StructName]; ok {
		return t.Name
	}
	return c.StructName
}

func indexTable(idx goschema.Index, tableByStruct map[string]goschema.Table) string {
	if strings.TrimSpace(idx.TableName) != "" {
		return idx.TableName
	}
	if t, ok := tableByStruct[idx.StructName]; ok {
		return t.Name
	}
	return idx.StructName
}

func indexColumns(idx goschema.Index) []string {
	if len(idx.Fields) > 0 {
		return idx.Fields
	}
	cols := make([]string, 0, len(idx.Parts))
	for _, part := range idx.Parts {
		switch {
		case strings.TrimSpace(part.Name) != "":
			cols = append(cols, part.Name)
		case strings.TrimSpace(part.Expr) != "":
			cols = append(cols, "expr("+part.Expr+")")
		}
	}
	return cols
}

func normColumns(cols []string) string {
	normalized := make([]string, 0, len(cols))
	for _, c := range cols {
		normalized = append(normalized, normColumnOrExpr(c))
	}
	return strings.Join(normalized, ",")
}

func normColumnOrExpr(s string) string {
	s = strings.TrimSpace(s)
	inner, ok := strings.CutPrefix(s, "expr(")
	if ok && strings.HasSuffix(inner, ")") {
		return "expr(" + normExpr(inner[:len(inner)-1]) + ")"
	}
	return normIdent(s)
}

func normIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if i := strings.LastIndex(s, "."); i >= 0 { // strip schema qualifier
		s = s[i+1:]
	}
	return strings.ToLower(strings.Trim(s, `"`))
}

func normGeneratedKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return ""
	}
	return strings.ReplaceAll(kind, "_", " ")
}

func normLiteral(lit string) string {
	lit = strings.TrimSpace(lit)
	lit = strings.Trim(lit, `"`)
	lit = strings.Trim(lit, `'`)
	return strings.TrimSpace(lit)
}

func normDefaultConstant(value string, allowBareLiteral bool) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	value = stripOuterParens(value)
	if !allowBareLiteral {
		before, ok := cutCastOutsideQuotes(value)
		if ok {
			value = strings.TrimSpace(before)
		}
	}
	if literal, ok := singleQuotedLiteral(value); ok {
		return literal, true
	}
	if doubleQuoted, ok := doubleQuotedLiteral(value); ok && allowBareLiteral {
		return doubleQuoted, true
	}
	if before, ok := cutCastOutsideQuotes(value); ok && !allowBareLiteral {
		value = strings.TrimSpace(before)
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "true", "false", "null":
		return lower, true
	}
	if normalized, ok := normNumber(lower); ok {
		return normalized, true
	}
	if allowBareLiteral && !strings.ContainsAny(value, "() ") {
		return strings.Trim(value, `"`), true
	}
	return "", false
}

func normNumber(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	digits := strings.TrimPrefix(value, "-")
	if digits == "" {
		return "", false
	}
	for _, r := range digits {
		if (r < '0' || r > '9') && r != '.' {
			return "", false
		}
	}
	if strings.Count(digits, ".") > 1 {
		return "", false
	}
	if strings.Contains(digits, ".") {
		value = strings.TrimRight(value, "0")
		value = strings.TrimRight(value, ".")
	}
	if value == "-0" || value == "" {
		return "0", true
	}
	return value, true
}

func normExpr(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	expr = strings.ReplaceAll(expr, `\'`, `'`)
	expr = stripOuterParens(expr)
	expr = removeIdentifierQuotesAndCharsetIntroducers(expr)
	return strings.Join(strings.Fields(lowerOutsideSingleQuotes(expr)), " ")
}

func removeIdentifierQuotesAndCharsetIntroducers(s string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !inQuote {
			for _, charset := range []string{"_utf8mb4", "_utf8"} {
				if strings.HasPrefix(strings.ToLower(s[i:]), charset+"'") {
					i += len(charset)
					ch = s[i]
					break
				}
			}
		}
		if ch == '\'' {
			b.WriteByte(ch)
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				b.WriteByte(s[i])
				continue
			}
			inQuote = !inQuote
			continue
		}
		if (ch != '"' && ch != '`') || inQuote {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func functionArg(s, name string) (string, bool) {
	prefix := name + "("
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, ")") {
		return "", false
	}
	return strings.TrimSpace(s[len(prefix) : len(s)-1]), true
}

func lowerOutsideSingleQuotes(s string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' {
			b.WriteByte(ch)
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				b.WriteByte(s[i])
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte(byte(strings.ToLower(string(ch))[0]))
	}
	return b.String()
}

func cutCastOutsideQuotes(s string) (string, bool) {
	inQuote := false
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\'' {
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if !inQuote && s[i] == ':' && s[i+1] == ':' {
			return s[:i], true
		}
	}
	return s, false
}

func singleQuotedLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), true
}

func doubleQuotedLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	return strings.ReplaceAll(s[1:len(s)-1], `\"`, `"`), true
}

func stripOuterParens(expr string) string {
	for {
		expr = strings.TrimSpace(expr)
		if len(expr) < 2 || expr[0] != '(' || expr[len(expr)-1] != ')' || !outerParensWrap(expr) {
			return expr
		}
		expr = expr[1 : len(expr)-1]
	}
}

func outerParensWrap(expr string) bool {
	depth := 0
	inQuote := false
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if ch == '\'' {
			if inQuote && i+1 < len(expr) && expr[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(expr)-1 {
				return false
			}
		}
		if depth < 0 {
			return false
		}
	}
	return depth == 0
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
			diffs = append(diffs, diffLists(t, a, p)...)
		}
	}
	return diffs
}

func diffLists(table string, atlas, ptah []string) []string {
	am := factMap(atlas)
	pm := factMap(ptah)
	var diffs []string
	seen := map[string]bool{}
	for c := range am {
		seen[c] = true
	}
	for c := range pm {
		seen[c] = true
	}
	var cols []string
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
