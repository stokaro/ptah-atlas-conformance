package probe

// White-box testing required: the comparison folds a live schema through
// unexported helpers -- `foldDefaultSchema`, `schemaDefaults`, `tableFacts`,
// `countTables` and the `globalFactsKey` they are recorded under. The folded
// form is what a comparison matches on and no exported result carries it.

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"ptah.run/core/schemaext"
	"ptah.run/core/schemamodel"
	"ptah.run/dialect/mysql/mysqlschema"
)

func TestFoldDefaultSchema_RemovesConnectionDefaultQualification(t *testing.T) {
	c := qt.New(t)
	facet := func(value schemaext.Value) schemaext.Facets {
		facets, err := schemaext.NewFacets(value)
		c.Assert(err, qt.IsNil)
		return facets
	}
	db := &schemamodel.Database{
		Schemas: []schemamodel.Schema{{Name: "conf", Charset: "utf8mb4", Collate: "utf8mb4_0900_ai_ci"}, {Name: "audit"}},
		Tables: []schemamodel.Table{
			{StructName: "User", Schema: "conf", Name: "users", Facets: facet(&mysqlschema.DesiredTable{Charset: "utf8mb4"}), Collate: "utf8mb4_0900_ai_ci"},
			{StructName: "Log", Schema: "audit", Name: "logs", Facets: facet(&mysqlschema.DesiredTable{Charset: "latin1"}), Collate: "latin1_swedish_ci"},
		},
		Fields: []schemamodel.Field{
			{StructName: "User", Name: "id", Type: "integer", Foreign: "conf.accounts(id)", Facets: facet(&mysqlschema.DesiredColumnSettings{Charset: "utf8mb4"}), Collate: "utf8mb4_0900_ai_ci"},
			{StructName: "Log", Name: "user_id", Type: "integer", Foreign: "conf.users(id)"},
		},
		Indexes: []schemamodel.Index{{Name: "idx_users_id", TableName: "conf.users", Fields: []string{"id"}}},
		Constraints: []schemamodel.Constraint{{
			Type: "FOREIGN KEY", Table: "audit.logs", Columns: []string{"user_id"}, ForeignTable: "conf.users", ForeignColumn: "id",
		}},
	}

	got := foldDefaultSchema(db, "conf", schemaDefaults(db, "conf"))

	c.Assert(got.Schemas, qt.DeepEquals, []schemamodel.Schema{{Name: "audit"}})
	c.Assert(got.Tables[0].Schema, qt.Equals, "")
	c.Assert(mysqlTableOptions(got.Tables[0].Facets).Charset, qt.Equals, "")
	c.Assert(got.Tables[0].Collate, qt.Equals, "")
	c.Assert(got.Tables[1].Schema, qt.Equals, "audit")
	c.Assert(mysqlTableOptions(got.Tables[1].Facets).Charset, qt.Equals, "latin1")
	c.Assert(got.Tables[1].Collate, qt.Equals, "latin1_swedish_ci")
	c.Assert(got.Fields[0].Foreign, qt.Equals, "accounts(id)")
	c.Assert(mysqlColumnOptions(got.Fields[0].Facets).Charset, qt.Equals, "")
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
