package probe

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/stokaro/ptah/core/goschema"
)

func TestFoldDefaultSchema_RemovesConnectionDefaultQualification(t *testing.T) {
	c := qt.New(t)
	db := &goschema.Database{
		Schemas: []goschema.Schema{{Name: "conf", Charset: "utf8mb4", Collate: "utf8mb4_0900_ai_ci"}, {Name: "audit"}},
		Tables: []goschema.Table{
			{StructName: "User", Schema: "conf", Name: "users", Charset: "utf8mb4", Collate: "utf8mb4_0900_ai_ci"},
			{StructName: "Log", Schema: "audit", Name: "logs", Charset: "latin1", Collate: "latin1_swedish_ci"},
		},
		Fields: []goschema.Field{
			{StructName: "User", Name: "id", Type: "integer", Foreign: "conf.accounts(id)", Charset: "utf8mb4", Collate: "utf8mb4_0900_ai_ci"},
			{StructName: "Log", Name: "user_id", Type: "integer", Foreign: "conf.users(id)"},
		},
		Indexes: []goschema.Index{{Name: "idx_users_id", TableName: "conf.users", Fields: []string{"id"}}},
		Constraints: []goschema.Constraint{{
			Type: "FOREIGN KEY", Table: "audit.logs", Columns: []string{"user_id"}, ForeignTable: "conf.users", ForeignColumn: "id",
		}},
	}

	got := foldDefaultSchema(db, "conf", schemaDefaults(db, "conf"))

	c.Assert(got.Schemas, qt.DeepEquals, []goschema.Schema{{Name: "audit"}})
	c.Assert(got.Tables[0].Schema, qt.Equals, "")
	c.Assert(got.Tables[0].Charset, qt.Equals, "")
	c.Assert(got.Tables[0].Collate, qt.Equals, "")
	c.Assert(got.Tables[1].Schema, qt.Equals, "audit")
	c.Assert(got.Tables[1].Charset, qt.Equals, "latin1")
	c.Assert(got.Tables[1].Collate, qt.Equals, "latin1_swedish_ci")
	c.Assert(got.Fields[0].Foreign, qt.Equals, "accounts(id)")
	c.Assert(got.Fields[0].Charset, qt.Equals, "")
	c.Assert(got.Fields[0].Collate, qt.Equals, "")
	c.Assert(got.Fields[1].Foreign, qt.Equals, "users(id)")
	c.Assert(got.Indexes[0].TableName, qt.Equals, "users")
	c.Assert(got.Constraints[0].Table, qt.Equals, "audit.logs")
	c.Assert(got.Constraints[0].ForeignTable, qt.Equals, "users")
}

func TestCountTablesIgnoresGlobalFactBucket(t *testing.T) {
	c := qt.New(t)

	got := countTables(tableFacts{
		globalFactsKey: {"~schema(auth): name=auth"},
		"auth.users":   {"id: integer notnull pk"},
	})

	c.Assert(got, qt.Equals, "1 table")
}
