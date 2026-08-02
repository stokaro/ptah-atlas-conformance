package probe_test

import (
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"go.5x5.cz/ptah/core/goschema"
)

func TestLiveFixtures_ParseIndexes(t *testing.T) {
	c := qt.New(t)

	tests := []struct {
		name      string
		fixture   string
		indexName string
		tableName string
		fields    []string
		unique    bool
	}{
		{
			name:      "index and foreign key",
			fixture:   "04-index-fk",
			indexName: "idx_books_title",
			tableName: "books",
			fields:    []string{"title"},
		},
		{
			name:      "generated column",
			fixture:   "07-generated-column",
			indexName: "idx_contacts_email_normalized",
			tableName: "contacts",
			fields:    []string{"email_normalized"},
		},
		{
			name:      "schema qualified",
			fixture:   "10-schema-qualified",
			indexName: "idx_invoices_user_number",
			tableName: "billing.invoices",
			fields:    []string{"user_id", "number"},
			unique:    true,
		},
	}

	for _, tt := range tests {
		c.Run(tt.name, func(c *qt.C) {
			dir := filepath.Join("..", "..", "testdata", "live", tt.fixture)

			db, err := goschema.ParseDir(dir)

			c.Assert(err, qt.IsNil)
			c.Assert(db.Indexes, qt.HasLen, 1)
			c.Assert(db.Indexes[0].Name, qt.Equals, tt.indexName)
			c.Assert(db.Indexes[0].TableName, qt.Equals, tt.tableName)
			c.Assert(db.Indexes[0].Fields, qt.DeepEquals, tt.fields)
			c.Assert(db.Indexes[0].Unique, qt.Equals, tt.unique)
		})
	}
}
