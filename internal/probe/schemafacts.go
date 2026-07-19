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

// tableFacts maps table name -> sorted list of "column: signature" strings.
type tableFacts map[string][]string

// factsFromDatabase reads canonical column facts off a typed schema.
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
			if strings.TrimSpace(f.Foreign) != "" {
				list = append(list, "~fk("+normIdent(f.Name)+"): "+foreignSignature(f))
			}
		}
		sort.Strings(list)
		out[t.Name] = list
	}
	return out
}

// fieldSignature canonicalizes one column: type, nullability, default presence,
// primary-key membership.
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
	if hasDefault(f) && !serial {
		sig += " hasdefault"
	}
	if isPK {
		sig += " pk"
	}
	return sig
}

// foreignSignature canonicalizes a field's foreign key: referenced target plus
// referential actions, so action ordering and an omitted default do not read as
// a difference.
func foreignSignature(f goschema.Field) string {
	return "-> " + normRef(f.Foreign) + " del=" + normAction(f.OnDelete) + " upd=" + normAction(f.OnUpdate)
}

// isSerial folds the two spellings of an auto-incrementing integer: Atlas's
// `serial` type and Ptah's introspected `integer` + `nextval(...)` default.
func isSerial(f goschema.Field) bool {
	return f.AutoInc ||
		strings.Contains(strings.ToLower(f.Type), "serial") ||
		strings.Contains(strings.ToLower(f.DefaultExpr), "nextval")
}

func hasDefault(f goschema.Field) bool {
	return f.DefaultSet || strings.TrimSpace(f.Default) != "" || strings.TrimSpace(f.DefaultExpr) != ""
}

// canonType folds equivalent type spellings, including the underscore forms Atlas
// HCL uses (`character_varying`) and the spaced forms Ptah introspects
// (`character varying`), while preserving a length/precision suffix so a dropped
// length stays visible.
func canonType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.ReplaceAll(t, `"`, "")

	base, suffix := t, ""
	if i := strings.IndexRune(t, '('); i >= 0 {
		base, suffix = t[:i], t[i:]
	}
	base = strings.TrimSpace(base)
	if i := strings.LastIndex(base, "."); i >= 0 { // strip schema qualifier on user types
		base = base[i+1:]
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
		{"boolean", "bool"},
		{"numeric", "decimal"},
	}
	for _, a := range aliases {
		if base == a.from {
			base = a.to
			break
		}
	}
	return strings.TrimSpace(base + suffix)
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

func normIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if i := strings.LastIndex(s, "."); i >= 0 { // strip schema qualifier
		s = s[i+1:]
	}
	return strings.ToLower(strings.Trim(s, `"`))
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
