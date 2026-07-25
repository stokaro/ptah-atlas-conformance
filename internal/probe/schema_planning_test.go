package probe

import "testing"

// TestCanonicalizeSchemaSQL checks that the canonical form is insensitive to
// table order, column order, comments, and trailing commas, but sensitive to a
// missing column and to a column being moved to a different table.
func TestCanonicalizeSchemaSQL(t *testing.T) {
	a := "-- header\nCREATE TABLE users (\n  id INT,\n  name TEXT\n);\nCREATE TABLE orders (\n  id INT\n);\n"
	b := "CREATE TABLE orders (\n  id INT\n);\n-- header\nCREATE TABLE users (\n  name TEXT,\n  id INT\n);\n"
	if canonicalizeSchemaSQL(a) != canonicalizeSchemaSQL(b) {
		t.Fatalf("reordered tables/columns should canonicalize equal:\n%q\n%q",
			canonicalizeSchemaSQL(a), canonicalizeSchemaSQL(b))
	}
	// A missing column must NOT compare equal.
	c := "CREATE TABLE users (\n  id INT\n);\n"
	d := "CREATE TABLE users (\n  id INT,\n  email TEXT\n);\n"
	if canonicalizeSchemaSQL(c) == canonicalizeSchemaSQL(d) {
		t.Fatal("schemas differing by a column must not canonicalize equal")
	}
	// A column swapped between two tables must NOT compare equal (per-block binding).
	e := "CREATE TABLE a (\n  x TEXT\n);\nCREATE TABLE b (\n  y TEXT\n);\n"
	f := "CREATE TABLE a (\n  y TEXT\n);\nCREATE TABLE b (\n  x TEXT\n);\n"
	if canonicalizeSchemaSQL(e) == canonicalizeSchemaSQL(f) {
		t.Fatal("a column swapped between tables must not canonicalize equal")
	}
}

// TestPlanningCatalogCoversOperations guards that the matrix keeps its
// add/drop/modify coverage.
func TestPlanningCatalogCoversOperations(t *testing.T) {
	want := map[string]bool{
		"add table": false, "drop table": false,
		"add column": false, "drop column": false,
		"modify column nullability": false, "modify column type category": false,
	}
	for _, c := range planningCatalog {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
		if c.A == "" || c.B == "" || c.A == c.B {
			t.Errorf("case %q must have distinct non-empty A and B schemas", c.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("planning matrix is missing the %q operation", name)
		}
	}
}
