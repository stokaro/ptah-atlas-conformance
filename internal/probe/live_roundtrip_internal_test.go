package probe

// White-box testing required: roundTripObjectSummary is report evidence for
// schema classes that Atlas CE cannot introspect, so its exact output cannot be
// exercised through the public probe API without a live database.

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"go.5x5.cz/ptah/core/goschema"
)

func TestRoundTripObjectSummary(t *testing.T) {
	c := qt.New(t)

	db := &goschema.Database{
		Tables:            []goschema.Table{{Name: "accounts"}},
		Fields:            []goschema.Field{{Name: "id"}},
		Indexes:           []goschema.Index{{Name: "accounts_email_idx"}},
		Constraints:       []goschema.Constraint{{Name: "accounts_email_check"}},
		Enums:             []goschema.Enum{{Name: "account_status"}},
		Extensions:        []goschema.Extension{{Name: "citext"}},
		Functions:         []goschema.Function{{Name: "normalize_email"}},
		Sequences:         []goschema.Sequence{{Name: "account_id_seq"}},
		Domains:           []goschema.Domain{{Name: "email"}, {Name: "pincode"}},
		CompositeTypes:    []goschema.CompositeType{{Name: "money_amount"}},
		Ranges:            []goschema.Range{{Name: "floatrange"}},
		Views:             []goschema.View{{Name: "active_accounts"}},
		MaterializedViews: []goschema.MaterializedView{{Name: "account_totals"}},
		Triggers:          []goschema.Trigger{{Name: "accounts_updated_at"}},
		RLSPolicies:       []goschema.RLSPolicy{{Name: "account_access"}},
		RLSEnabledTables:  []goschema.RLSEnabledTable{{Table: "accounts"}},
		Roles:             []goschema.Role{{Name: "app"}},
		Grants:            []goschema.Grant{{Role: "app"}},
	}

	got := roundTripObjectSummary(db)

	c.Assert(got, qt.Equals,
		"tables=1, fields=1, indexes=1, constraints=1, enums=1, extensions=1, functions=1, "+
			"sequences=1, domains=2, composite_types=1, ranges=1, views=1, materialized_views=1, "+
			"triggers=1, rls_policies=1, rls_enabled_tables=1, roles=1, grants=1")
}

func TestRoundTripObjectSummary_EmptySchema(t *testing.T) {
	c := qt.New(t)

	got := roundTripObjectSummary(&goschema.Database{})

	c.Assert(got, qt.Equals, "no objects")
}
