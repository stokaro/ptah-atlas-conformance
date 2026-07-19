package probe

import (
	"testing"

	"github.com/stokaro/ptah/core/goschema"
)

// These lock the differential comparison: the point of the tier is that
// semantically-equivalent schemas from Atlas and Ptah compare equal, while
// genuine differences (a dropped length, a lost primary key, a different
// referential action, a missing column) stay visible. Inputs model the two
// representations observed on the live fixtures: Atlas (via core/atlashcl) spells
// an auto-increment column `serial` and uses `character_varying`; Ptah (via
// introspection) spells it `integer` + a `nextval(...)` default and
// `character varying`.

func oneTable(name string, fields ...goschema.Field) *goschema.Database {
	for i := range fields {
		fields[i].StructName = name
	}
	return &goschema.Database{
		Tables: []goschema.Table{{StructName: name, Name: name}},
		Fields: fields,
	}
}

func diff(atlas, ptah *goschema.Database) []string {
	return diffTableFacts(factsFromDatabase(atlas), factsFromDatabase(ptah))
}

func TestFacts_SerialTimestampAndPKAreEquivalent(t *testing.T) {
	atlas := oneTable("users",
		goschema.Field{Name: "id", Type: "serial", Primary: true},
		goschema.Field{Name: "email", Type: "character_varying(255)"},
		goschema.Field{Name: "created_at", Type: "timestamp", DefaultExpr: "CURRENT_TIMESTAMP"},
	)
	ptah := oneTable("users",
		goschema.Field{Name: "id", Type: "integer", Primary: true, AutoInc: true, DefaultExpr: "nextval('users_id_seq'::regclass)"},
		goschema.Field{Name: "email", Type: "character varying(255)"},
		goschema.Field{Name: "created_at", Type: "timestamp without time zone", DefaultExpr: "CURRENT_TIMESTAMP"},
	)
	if d := diff(atlas, ptah); d != nil {
		t.Fatalf("expected equivalence (serial==int+nextval, character_varying==character varying, timestamp==timestamp without time zone), got: %v", d)
	}
}

func TestFacts_DroppedLengthIsAGap(t *testing.T) {
	atlas := oneTable("t", goschema.Field{Name: "name", Type: "character_varying(255)"})
	ptah := oneTable("t", goschema.Field{Name: "name", Type: "character varying"})
	if d := diff(atlas, ptah); len(d) != 1 {
		t.Fatalf("expected exactly one difference for the dropped length, got: %v", d)
	}
}

func TestFacts_ForeignKeyFoldsActionOrderAndDefault(t *testing.T) {
	// Atlas reports both actions explicitly; Ptah leaves them empty (the SQL
	// default is NO ACTION). They must fold to equal.
	atlas := oneTable("books", goschema.Field{Name: "author_id", Type: "integer",
		Foreign: "authors(id)", OnUpdate: "NO_ACTION", OnDelete: "NO_ACTION"})
	ptah := oneTable("books", goschema.Field{Name: "author_id", Type: "integer",
		Foreign: "authors(id)"})
	if d := diff(atlas, ptah); d != nil {
		t.Fatalf("expected the foreign key to fold to equal, got: %v", d)
	}
}

func TestFacts_DifferentReferentialActionIsAGap(t *testing.T) {
	atlas := oneTable("t", goschema.Field{Name: "a", Type: "integer",
		Foreign: "u(id)", OnDelete: "CASCADE"})
	ptah := oneTable("t", goschema.Field{Name: "a", Type: "integer",
		Foreign: "u(id)", OnDelete: "NO ACTION"})
	if d := diff(atlas, ptah); len(d) != 1 {
		t.Fatalf("expected ON DELETE CASCADE vs NO ACTION to be exactly one gap, got: %v", d)
	}
}

func TestFacts_CompositePrimaryKeyMismatchIsAGap(t *testing.T) {
	// Atlas records the composite key at table level; Ptah round-trips only the
	// first column as primary (the real defect the live tier found).
	atlas := &goschema.Database{
		Tables: []goschema.Table{{StructName: "m", Name: "m", PrimaryKey: []string{"user_id", "group_id"}}},
		Fields: []goschema.Field{
			{StructName: "m", Name: "user_id", Type: "integer"},
			{StructName: "m", Name: "group_id", Type: "integer"},
		},
	}
	ptah := &goschema.Database{
		Tables: []goschema.Table{{StructName: "m", Name: "m"}},
		Fields: []goschema.Field{
			{StructName: "m", Name: "user_id", Type: "integer", Primary: true},
			{StructName: "m", Name: "group_id", Type: "integer"},
		},
	}
	d := diff(atlas, ptah)
	if len(d) != 1 {
		t.Fatalf("expected exactly one gap for the lost composite-PK membership, got: %v", d)
	}
}

func TestFacts_CompositePrimaryKeyAgreesWhenBothComplete(t *testing.T) {
	both := func() *goschema.Database {
		return &goschema.Database{
			Tables: []goschema.Table{{StructName: "m", Name: "m", PrimaryKey: []string{"user_id", "group_id"}}},
			Fields: []goschema.Field{
				{StructName: "m", Name: "user_id", Type: "integer"},
				{StructName: "m", Name: "group_id", Type: "integer"},
			},
		}
	}
	if d := diff(both(), both()); d != nil {
		t.Fatalf("expected a complete composite key to compare equal, got: %v", d)
	}
}

func TestFacts_MissingColumnIsAGap(t *testing.T) {
	atlas := oneTable("t",
		goschema.Field{Name: "a", Type: "integer"},
		goschema.Field{Name: "b", Type: "integer"},
	)
	ptah := oneTable("t", goschema.Field{Name: "a", Type: "integer"})
	if d := diff(atlas, ptah); len(d) != 1 {
		t.Fatalf("expected exactly one difference for the missing column, got: %v", d)
	}
}
