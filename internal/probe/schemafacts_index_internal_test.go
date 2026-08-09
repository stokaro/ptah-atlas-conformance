package probe

// White-box testing required: these tests pin the internal semantic
// canonicalization used only by the Atlas-versus-Ptah differential fact model.

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"go.5x5.cz/ptah/core/goschema"
)

func TestFacts_ImplicitAndExplicitBTreeIndexesAreEquivalent(t *testing.T) {
	c := qt.New(t)
	atlas := databaseWithIndexType("")
	ptah := databaseWithIndexType("btree")

	c.Check(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_ImplicitBTreeAndHashIndexesDiffer(t *testing.T) {
	c := qt.New(t)
	atlas := databaseWithIndexType("")
	ptah := databaseWithIndexType("hash")

	assertOneDiffContains(c, diff(atlas, ptah), "type=hash")
}

func databaseWithIndexType(indexType string) *goschema.Database {
	return &goschema.Database{
		Tables: []goschema.Table{{StructName: "Contact", Name: "contacts"}},
		Fields: []goschema.Field{{StructName: "Contact", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Contact",
			Name:       "idx_contacts_email",
			Fields:     []string{"email"},
			Type:       indexType,
		}},
	}
}
