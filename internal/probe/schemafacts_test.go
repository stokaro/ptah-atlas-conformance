package probe

import "testing"

// These lock the differential normalizer: the point of the tier is that
// semantically-equivalent DDL from Atlas and Ptah compares equal, while genuine
// differences (a dropped length, a lost primary key, a missing column) stay
// visible. The inputs are shaped like the real `schema inspect` / introspect
// output the two tools produced on the live fixtures.

func TestFacts_SerialAndTimestampAndPKAreEquivalent(t *testing.T) {
	atlas := `-- Add new schema named "public"
CREATE SCHEMA IF NOT EXISTS "public";
-- Create "users" table
CREATE TABLE "public"."users" ("id" serial NOT NULL, "email" character varying(255) NOT NULL, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("id"));`

	ptah := `-- POSTGRES TABLE: users --
CREATE TABLE "users" (
  "id" integer PRIMARY KEY NOT NULL DEFAULT nextval('users_id_seq'::regclass),
  "email" character varying(255) NOT NULL,
  "created_at" timestamp without time zone NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE EXTENSION IF NOT EXISTS "plpgsql" VERSION '1.0';`

	if diffs := diffTableFacts(extractTableFacts(atlas), extractTableFacts(ptah)); diffs != nil {
		t.Fatalf("expected equivalence (serial==int+nextval, timestamp==timestamp without time zone, inline==table-level PK), got: %v", diffs)
	}
}

func TestFacts_DroppedLengthIsAGap(t *testing.T) {
	atlas := `CREATE TABLE "public"."t" ("name" character varying(255) NOT NULL);`
	ptah := `CREATE TABLE "t" ("name" character varying NOT NULL);`
	diffs := diffTableFacts(extractTableFacts(atlas), extractTableFacts(ptah))
	if len(diffs) != 1 {
		t.Fatalf("expected exactly one difference for the dropped length, got: %v", diffs)
	}
}

func TestFacts_QuotedSchemaQualifiedEnumTypeFolds(t *testing.T) {
	atlas := `CREATE TABLE "public"."accounts" ("status" "public"."enum_account_status" NOT NULL DEFAULT 'active');`
	ptah := `CREATE TABLE "accounts" ("status" enum_account_status NOT NULL DEFAULT 'active');`
	if diffs := diffTableFacts(extractTableFacts(atlas), extractTableFacts(ptah)); diffs != nil {
		t.Fatalf("expected quoted schema-qualified enum type to fold to bare name, got: %v", diffs)
	}
}

func TestFacts_MultipleTablesDoNotBleed(t *testing.T) {
	// Regression: a greedy CREATE TABLE match once mashed both tables' columns
	// into one body, mislabeling authors.name as the serial PK.
	sql := `CREATE TABLE "public"."authors" ("id" serial NOT NULL, "name" character varying(255) NOT NULL, PRIMARY KEY ("id"));
CREATE TABLE "public"."books" ("id" serial NOT NULL, "title" character varying(255) NOT NULL, PRIMARY KEY ("id"));`
	facts := extractTableFacts(sql)
	if len(facts) != 2 {
		t.Fatalf("expected 2 tables, got %d: %v", len(facts), facts)
	}
	for _, want := range []string{"authors", "books"} {
		if facts[want] == nil {
			t.Fatalf("missing table %q in %v", want, facts)
		}
	}
	// authors.name must be a plain varchar, never the serial PK.
	for _, f := range facts["authors"] {
		if f == "name: serial notnull pk" {
			t.Fatalf("authors.name bled the id column's signature: %v", facts["authors"])
		}
	}
}

func TestFacts_CompositePrimaryKeyIsCaptured(t *testing.T) {
	sql := `CREATE TABLE "public"."memberships" ("user_id" integer NOT NULL, "group_id" integer NOT NULL, "role" character varying(50) NOT NULL, PRIMARY KEY ("user_id", "group_id"));`
	facts := extractTableFacts(sql)["memberships"]
	m := factMap(facts)
	for _, col := range []string{"user_id", "group_id"} {
		if got := m[col]; got == "" || got[len(got)-2:] != "pk" {
			t.Fatalf("expected %s to be part of the composite primary key, got %q", col, got)
		}
	}
	if got := m["role"]; got == "" || got[len(got)-2:] == "pk" {
		t.Fatalf("role should not be a primary key, got %q", got)
	}
}

func TestFacts_MissingColumnIsAGap(t *testing.T) {
	atlas := `CREATE TABLE "public"."t" ("a" integer NOT NULL, "b" integer NOT NULL);`
	ptah := `CREATE TABLE "t" ("a" integer NOT NULL);`
	diffs := diffTableFacts(extractTableFacts(atlas), extractTableFacts(ptah))
	if len(diffs) != 1 {
		t.Fatalf("expected exactly one difference for the missing column, got: %v", diffs)
	}
}
