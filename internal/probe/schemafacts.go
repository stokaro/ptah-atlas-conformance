package probe

import (
	"sort"
	"strconv"
	"strings"

	"ptah.run/core/schemamodel"
)

// This file reduces a schema to a canonical, comparable set of column facts so
// the differential probe compares what two tools *understand* about a schema
// rather than how they spell DDL. Both sides arrive as a typed
// schemamodel.Database — Ptah's from live introspection, Atlas's from parsing its
// native HCL `schema inspect` output through Ptah's own core/atlashcl parser —
// so there is no SQL text parsing here; the facts are read off typed fields. It
// folds the systematic, semantically-equivalent representation differences
// (serial vs integer+nextval, `character varying`/`character_varying` vs
// `varchar`, `timestamp without time zone` vs `timestamp`, field-level vs
// table-level primary key, foreign-key action ordering and defaults) and leaves
// genuine differences (a dropped length, a missing column, a different
// nullability, a lost primary-key membership) visible.

const globalFactsKey = "\x00global-schema-facts"

// tableFacts maps a table/global object key -> sorted list of "object:
// signature" strings.
type tableFacts map[string][]string

// factsFromDatabase reads canonical schema facts off a typed schema.
func factsFromDatabase(db *schemamodel.Database) tableFacts {
	if db == nil {
		return tableFacts{}
	}
	out := tableFacts{}
	if global := globalFacts(db); len(global) > 0 {
		sort.Strings(global)
		out[globalFactsKey] = global
	}

	tableByStruct := map[string]schemamodel.Table{}
	for _, t := range db.Tables {
		tableByStruct[t.StructName] = t
	}
	fieldsByStruct := map[string][]schemamodel.Field{}
	for _, f := range db.Fields {
		fieldsByStruct[f.StructName] = append(fieldsByStruct[f.StructName], f)
	}

	for structName, t := range tableByStruct {
		fields := fieldsByStruct[structName]
		tableKey := tableFactKey(t)

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
		if tableMeta := tableMetaFact(t); tableMeta != "" {
			list = append(list, tableMeta)
		}
		if primaryKey := primaryKeyFact(t, fields); primaryKey != "" {
			list = append(list, primaryKey)
		}
		for _, f := range fields {
			list = append(list, normIdent(f.Name)+": "+fieldSignature(f, pk[normIdent(f.Name)]))
			if f.Unique {
				cols := normColumns([]string{f.Name})
				list = append(list, uniqueFact(cols, "columns="+cols, nil, nil, ""))
			}
			if strings.TrimSpace(f.Check) != "" {
				list = append(list, checkFact(f.Check))
			}
			if strings.TrimSpace(f.Foreign) != "" {
				list = append(list, "~fk("+normIdent(f.Name)+"): "+foreignSignature(f))
			}
		}
		list = append(list, constraintFacts(db.Constraints, tableByStruct, tableKey)...)
		list = append(list, indexFacts(db.Indexes, tableByStruct, tableKey)...)
		sort.Strings(list)
		out[tableKey] = list
	}
	return out
}

func globalFacts(db *schemamodel.Database) []string {
	var facts []string
	for _, schema := range db.Schemas {
		name := normSchema(schema.Name)
		if name == "" {
			continue
		}
		sig := "name=" + name
		if comment := normComment(schema.Comment); comment != "" {
			sig += " comment=" + comment
		}
		facts = append(facts, "~schema("+name+"): "+sig)
	}
	for _, enum := range db.Enums {
		name := normIdent(enum.Name)
		values := normEnumValues(strings.Join(enum.Values, ","))
		facts = append(facts, "~enum("+name+"): values="+values)
	}
	return facts
}

func tableMetaFact(t schemamodel.Table) string {
	parts := []string{}
	if schema := normSchema(t.Schema); schema != "" {
		parts = append(parts, "schema="+schema)
	}
	if engine := normIdent(t.Engine); engine != "" {
		parts = append(parts, "engine="+engine)
	}
	if value := strings.TrimSpace(t.AutoIncrement); value != "" {
		parts = append(parts, "auto_increment="+value)
	}
	if charset := normIdent(t.Charset); charset != "" {
		parts = append(parts, "charset="+charset)
	}
	if collate := normIdent(t.Collate); collate != "" {
		parts = append(parts, "collate="+collate)
	}
	if t.Strict {
		parts = append(parts, "strict=true")
	}
	if t.WithoutRowID {
		parts = append(parts, "without_rowid=true")
	}
	if comment := normComment(t.Comment); comment != "" {
		parts = append(parts, "comment="+comment)
	}
	if len(parts) == 0 {
		return ""
	}
	return "~table: " + strings.Join(parts, " ")
}

func primaryKeyFact(t schemamodel.Table, fields []schemamodel.Field) string {
	parts := primaryKeyParts(t, fields)
	if len(parts) == 0 {
		return ""
	}
	sig := "columns=" + strings.Join(parts, ",")
	if include := normColumns(t.PrimaryKeyInclude); include != "" {
		sig += " include=" + include
	}
	return "~primary_key: " + sig
}

func primaryKeyParts(t schemamodel.Table, fields []schemamodel.Field) []string {
	if len(t.PrimaryKeyParts) > 0 {
		parts := make([]string, 0, len(t.PrimaryKeyParts))
		for _, part := range t.PrimaryKeyParts {
			parts = append(parts, primaryKeyPartSignature(part))
		}
		return parts
	}
	if len(t.PrimaryKey) > 0 {
		return normColumnList(t.PrimaryKey)
	}
	parts := []string{}
	for _, field := range fields {
		if field.Primary {
			parts = append(parts, normIdent(field.Name))
		}
	}
	return parts
}

func primaryKeyPartSignature(part schemamodel.PrimaryKeyPart) string {
	sig := normIdent(part.Name)
	if prefix := strings.TrimSpace(part.Prefix); prefix != "" {
		sig += " prefix=" + prefix
	}
	if part.Desc {
		sig += " desc"
	}
	return sig
}

// fieldSignature canonicalizes one column: type, nullability, default presence,
// primary-key membership, generated-column expression, and generated-column
// kind. Default constants are compared by normalized value rather than mere
// presence, while non-constant expressions remain visible as expressions.
func fieldSignature(f schemamodel.Field, isPK bool) string {
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
	if identity := identitySignature(f); identity != "" {
		sig += " identity=" + identity
	}
	if update := normExpr(f.UpdateExpression); update != "" {
		sig += " on_update=" + update
	}
	if charset := normIdent(f.Charset); charset != "" {
		sig += " charset=" + charset
	}
	if collate := normIdent(f.Collate); collate != "" {
		sig += " collate=" + collate
	}
	if enum := normEnumValues(strings.Join(f.Enum, ",")); enum != "" {
		sig += " enum=" + enum
	}
	if comment := normComment(f.Comment); comment != "" {
		sig += " comment=" + comment
	}
	return sig
}

func identitySignature(f schemamodel.Field) string {
	generation := normGeneratedKind(f.IdentityGeneration)
	if generation == "" && strings.TrimSpace(f.IdentityStart) == "" &&
		strings.TrimSpace(f.IdentityIncrement) == "" && strings.TrimSpace(f.IdentityOptions) == "" {
		return ""
	}
	if generation == "" {
		generation = "by default"
	}
	parts := []string{generation}
	if start := strings.TrimSpace(f.IdentityStart); start != "" {
		parts = append(parts, "start="+start)
	}
	if increment := strings.TrimSpace(f.IdentityIncrement); increment != "" {
		parts = append(parts, "increment="+increment)
	}
	if options := strings.TrimSpace(f.IdentityOptions); options != "" {
		parts = append(parts, "options="+normExpr(options))
	}
	return strings.Join(parts, " ")
}

// foreignSignature canonicalizes a field's foreign key: referenced target plus
// referential actions, so action ordering and an omitted default do not read as
// a difference.
func foreignSignature(f schemamodel.Field) string {
	return "-> " + normRef(f.Foreign) + " del=" + normAction(f.OnDelete) + " upd=" + normAction(f.OnUpdate)
}

func checkFact(expr string) string {
	normalized := normExpr(expr)
	// SQLite does not preserve stable CHECK names through Atlas/Ptah inspect, so
	// the expression is the semantic identity for this differential tier.
	return "~check(" + normalized + "): expr=" + normalized
}

func constraintFacts(constraints []schemamodel.Constraint, tableByStruct map[string]schemamodel.Table, tableName string) []string {
	var facts []string
	for _, c := range constraints {
		if constraintTableKey(c, tableByStruct) != tableName {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(c.Type)) {
		case "UNIQUE":
			cols := normColumns(c.Columns)
			facts = append(facts, uniqueFact(cols, "columns="+cols, c.IncludeColumns, c.NullsDistinct, c.Comment))
		case "CHECK":
			facts = append(facts, checkFact(c.CheckExpression))
		case "FOREIGN KEY":
			facts = append(facts, "~fk("+normColumns(c.Columns)+"): -> "+normRef(c.ForeignTable+"("+strings.Join(c.ForeignColumnsOrDefault(), ",")+")")+
				" del="+normAction(c.OnDelete)+" upd="+normAction(c.OnUpdate))
		}
	}
	return facts
}

func indexFacts(indexes []schemamodel.Index, tableByStruct map[string]schemamodel.Table, tableName string) []string {
	var facts []string
	for _, idx := range indexes {
		if indexTableKey(idx, tableByStruct) != tableName {
			continue
		}
		cols := normIndexParts(idx)
		if idx.Unique {
			facts = append(facts, uniqueFact(cols, indexFactCoreSignature(idx, cols), idx.IncludeColumns, idx.NullsDistinct, idx.Comment))
			continue
		}
		facts = append(facts, "~index("+normIdent(idx.Name)+"): "+indexFactSignature(idx, cols))
	}
	return facts
}

func uniqueFact(cols, sig string, include []string, nullsDistinct *bool, comment string) string {
	if include := normColumns(include); include != "" {
		sig += " include=" + include
	}
	if nulls := nullsDistinctSignature(nullsDistinct); nulls != "" {
		sig += " nulls=" + nulls
	}
	if comment := normComment(comment); comment != "" {
		sig += " comment=" + comment
	}
	return "~unique(" + cols + "): " + sig
}

func indexFactSignature(idx schemamodel.Index, cols string) string {
	sig := indexFactCoreSignature(idx, cols)
	if include := normColumns(idx.IncludeColumns); include != "" {
		sig += " include=" + include
	}
	if storage := storageParamsSignature(idx.StorageParams); storage != "" {
		sig += " storage=" + storage
	}
	if idx.Granularity != 0 {
		sig += " granularity=" + strconv.Itoa(idx.Granularity)
	}
	if comment := normComment(idx.Comment); comment != "" {
		sig += " comment=" + comment
	}
	return sig
}

func indexFactCoreSignature(idx schemamodel.Index, cols string) string {
	sig := "columns=" + cols
	if typ := normIndexType(idx.Type); typ != "" {
		sig += " type=" + typ
	}
	if parser := normIdent(idx.Parser); parser != "" {
		sig += " parser=" + parser
	}
	if operator := normIdent(idx.Operator); operator != "" {
		sig += " operator=" + operator
	}
	if condition := normExpr(idx.Condition); condition != "" {
		sig += " where=" + condition
	}
	return sig
}

// normIndexType folds the default B-tree access method into the omitted form.
// Schema inspectors may either preserve the catalog's explicit `btree` value
// or omit it as the engine default; those representations describe the same
// index. Non-default methods remain part of the differential fact signature.
func normIndexType(raw string) string {
	typ := normIdent(raw)
	if typ == "btree" {
		return ""
	}
	return typ
}

func nullsDistinctSignature(value *bool) string {
	if value == nil {
		return ""
	}
	if *value {
		return "distinct"
	}
	return "not_distinct"
}

func storageParamsSignature(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	type pair struct {
		key   string
		value string
	}
	pairs := make([]pair, 0, len(params))
	for key, value := range params {
		pairs = append(pairs, pair{key: normIdent(key), value: normLiteral(value)})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair.key+"="+pair.value)
	}
	return strings.Join(parts, ",")
}

// isSerial folds the two spellings of an auto-incrementing integer: Atlas's
// `serial` type and Ptah's introspected `integer` + `nextval(...)` default.
func isSerial(f schemamodel.Field) bool {
	return f.AutoInc ||
		strings.Contains(strings.ToLower(f.Type), "serial") ||
		strings.Contains(strings.ToLower(f.DefaultExpr), "nextval")
}

func defaultSignature(f schemamodel.Field) string {
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
	ref = strings.ReplaceAll(ref, "`", "")
	table, cols := ref, ""
	if i := strings.IndexRune(ref, '('); i >= 0 {
		table, cols = ref[:i], ref[i:]
	}
	return normTableRef(table) + normRefColumns(cols)
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

func constraintTableKey(c schemamodel.Constraint, tableByStruct map[string]schemamodel.Table) string {
	if strings.TrimSpace(c.Table) != "" {
		return normTableRef(c.Table)
	}
	if t, ok := tableByStruct[c.StructName]; ok {
		return tableFactKey(t)
	}
	return normTableRef(c.StructName)
}

func indexTableKey(idx schemamodel.Index, tableByStruct map[string]schemamodel.Table) string {
	if strings.TrimSpace(idx.TableName) != "" {
		return normTableRef(idx.TableName)
	}
	if t, ok := tableByStruct[idx.StructName]; ok {
		return tableFactKey(t)
	}
	return normTableRef(idx.StructName)
}

func normIndexParts(idx schemamodel.Index) string {
	return strings.Join(indexParts(idx), ",")
}

func indexParts(idx schemamodel.Index) []string {
	if len(idx.Parts) > 0 {
		cols := make([]string, 0, len(idx.Parts))
		for _, part := range idx.Parts {
			switch {
			case strings.TrimSpace(part.Name) != "":
				cols = append(cols, indexPartSignature(part))
			case strings.TrimSpace(part.Expr) != "":
				cols = append(cols, indexPartSignature(part))
			}
		}
		return cols
	}
	if len(idx.Fields) > 0 {
		return normColumnList(idx.Fields)
	}
	return nil
}

func indexPartSignature(part schemamodel.IndexPart) string {
	sig := ""
	if name := strings.TrimSpace(part.Name); name != "" {
		sig = normIdent(name)
	}
	if expr := strings.TrimSpace(part.Expr); expr != "" {
		sig = "expr(" + normExpr(expr) + ")"
	}
	if operator := normIdent(part.Operator); operator != "" {
		sig += " op=" + operator
	}
	if prefix := strings.TrimSpace(part.Prefix); prefix != "" {
		sig += " prefix=" + prefix
	}
	if part.Desc {
		sig += " desc"
	}
	return sig
}

func normColumns(cols []string) string {
	return strings.Join(normColumnList(cols), ",")
}

func normColumnList(cols []string) []string {
	normalized := make([]string, 0, len(cols))
	for _, c := range cols {
		normalized = append(normalized, normColumnOrExpr(c))
	}
	return normalized
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
	s = strings.Trim(s, "`")
	s = strings.Trim(s, `"`)
	if i := strings.LastIndex(s, "."); i >= 0 { // strip schema qualifier
		s = s[i+1:]
	}
	return strings.ToLower(strings.Trim(strings.Trim(s, "`"), `"`))
}

func normSchema(s string) string {
	s = normIdent(s)
	switch s {
	case "", "public", "main":
		return ""
	default:
		return s
	}
}

func tableFactKey(t schemamodel.Table) string {
	schema := normSchema(t.Schema)
	name := normIdent(t.Name)
	if schema == "" {
		return name
	}
	return schema + "." + name
}

func normTableRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.ReplaceAll(ref, "`", "")
	ref = strings.ReplaceAll(ref, `"`, "")
	parts := strings.Split(ref, ".")
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return normIdent(parts[0])
	default:
		schema := normSchema(parts[len(parts)-2])
		name := normIdent(parts[len(parts)-1])
		if schema == "" {
			return name
		}
		return schema + "." + name
	}
}

func normRefColumns(cols string) string {
	cols = strings.TrimSpace(cols)
	if cols == "" {
		return ""
	}
	cols = strings.TrimPrefix(cols, "(")
	cols = strings.TrimSuffix(cols, ")")
	return "(" + normColumns(strings.Split(cols, ",")) + ")"
}

func normComment(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
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
		label := factGroupLabel(t)
		switch {
		case aok && !pok:
			diffs = append(diffs, "Ptah is missing "+label)
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

func factGroupLabel(key string) string {
	if key == globalFactsKey {
		return "global schema facts"
	}
	return "table " + key
}

func diffLists(table string, atlas, ptah []string) []string {
	table = factGroupDiffPrefix(table)
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

func factGroupDiffPrefix(key string) string {
	if key == globalFactsKey {
		return "global"
	}
	return key
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
