package probe

// White-box testing required: roundTripObjectSummary is report evidence for
// schema classes that Atlas CE cannot introspect, so its exact output cannot be
// exercised through the public probe API without a live database.

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"ptah.run/core/schemamodel"
)

func TestRoundTripObjectSummary(t *testing.T) {
	c := qt.New(t)

	db := &schemamodel.Database{
		Tables:            []schemamodel.Table{{Name: "accounts"}},
		Fields:            []schemamodel.Field{{Name: "id"}},
		Indexes:           []schemamodel.Index{{Name: "accounts_email_idx"}},
		Constraints:       []schemamodel.Constraint{{Name: "accounts_email_check"}},
		Enums:             []schemamodel.Enum{{Name: "account_status"}},
		Extensions:        []schemamodel.Extension{{Name: "citext"}},
		Functions:         []schemamodel.Function{{Name: "normalize_email"}},
		Sequences:         []schemamodel.Sequence{{Name: "account_id_seq"}},
		Domains:           []schemamodel.Domain{{Name: "email"}, {Name: "pincode"}},
		CompositeTypes:    []schemamodel.CompositeType{{Name: "money_amount"}},
		Ranges:            []schemamodel.Range{{Name: "floatrange"}},
		Views:             []schemamodel.View{{Name: "active_accounts"}},
		MaterializedViews: []schemamodel.MaterializedView{{Name: "account_totals"}},
		Triggers:          []schemamodel.Trigger{{Name: "accounts_updated_at"}},
		RLSPolicies:       []schemamodel.RLSPolicy{{Name: "account_access"}},
		RLSEnabledTables:  []schemamodel.RLSEnabledTable{{Table: "accounts"}},
		Roles:             []schemamodel.Role{{Name: "app"}},
		Grants:            []schemamodel.Grant{{Role: "app"}},
	}

	got := roundTripObjectSummary(db)

	c.Assert(got, qt.Equals,
		"tables=1, fields=1, indexes=1, constraints=1, enums=1, extensions=1, functions=1, "+
			"sequences=1, domains=2, composite_types=1, ranges=1, views=1, materialized_views=1, "+
			"triggers=1, rls_policies=1, rls_enabled_tables=1, roles=1, grants=1")
}

func TestRoundTripObjectSummary_EmptySchema(t *testing.T) {
	c := qt.New(t)

	got := roundTripObjectSummary(&schemamodel.Database{})

	c.Assert(got, qt.Equals, "no objects")
}
