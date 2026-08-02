package probe

import (
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"

	"go.5x5.cz/ptah/core/goschema"
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

func boolPtr(value bool) *bool {
	return &value
}

func assertOneDiffContains(c *qt.C, d []string, want string) {
	c.Assert(d, qt.HasLen, 1)
	c.Assert(d[0], qt.Contains, want)
}

func TestFacts_SerialTimestampAndPKAreEquivalent(t *testing.T) {
	c := qt.New(t)

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

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_DialectTypeSpellingsAreEquivalent(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("users",
		goschema.Field{Name: "active", Type: "bool"},
		goschema.Field{Name: "created_at", Type: "sql(timestamp)", DefaultExpr: "CURRENT_TIMESTAMP"},
		goschema.Field{Name: "status", Type: "enum(active,suspended,deleted)", Default: "active", DefaultSet: true},
	)
	ptah := oneTable("users",
		goschema.Field{Name: "active", Type: "tinyint(1)"},
		goschema.Field{Name: "created_at", Type: "timestamp", DefaultExpr: "CURRENT_TIMESTAMP"},
		goschema.Field{Name: "status", Type: "enum('active','suspended','deleted')", Default: "active", DefaultSet: true},
	)

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_DefaultSchemaQualificationFolds(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables: []goschema.Table{{StructName: "User", Schema: "public", Name: "users"}},
		Fields: []goschema.Field{{StructName: "User", Name: "id", Type: "integer"}},
	}
	ptah := &goschema.Database{
		Tables: []goschema.Table{{StructName: "User", Name: "users"}},
		Fields: []goschema.Field{{StructName: "User", Name: "id", Type: "integer"}},
	}

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_SchemaQualifiedTablesDoNotCollide(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Schemas: []goschema.Schema{{Name: "auth"}, {Name: "billing"}},
		Tables: []goschema.Table{
			{StructName: "AuthUser", Schema: "auth", Name: "users"},
			{StructName: "BillingUser", Schema: "billing", Name: "users"},
		},
		Fields: []goschema.Field{
			{StructName: "AuthUser", Name: "id", Type: "integer"},
			{StructName: "BillingUser", Name: "id", Type: "integer"},
		},
	}
	ptah := &goschema.Database{
		Schemas: []goschema.Schema{{Name: "auth"}},
		Tables:  []goschema.Table{{StructName: "AuthUser", Schema: "auth", Name: "users"}},
		Fields:  []goschema.Field{{StructName: "AuthUser", Name: "id", Type: "integer"}},
	}

	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, "billing.users")
}

func TestFacts_ForeignKeyTargetSchemaMismatchIsGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("orders", goschema.Field{Name: "user_id", Type: "integer", Foreign: "auth.users(id)"})
	ptah := oneTable("orders", goschema.Field{Name: "user_id", Type: "integer", Foreign: "users(id)"})

	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, "auth.users(id)")
}

func TestFacts_DroppedLengthIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "name", Type: "character_varying(255)"})
	ptah := oneTable("t", goschema.Field{Name: "name", Type: "character varying"})

	assertOneDiffContains(c, diff(atlas, ptah), "varchar(255)")
}

func TestFacts_ForeignKeyFoldsActionOrderAndDefault(t *testing.T) {
	c := qt.New(t)

	// Atlas reports both actions explicitly; Ptah leaves them empty (the SQL
	// default is NO ACTION). They must fold to equal.
	atlas := oneTable("books", goschema.Field{Name: "author_id", Type: "integer",
		Foreign: "authors(id)", OnUpdate: "NO_ACTION", OnDelete: "NO_ACTION"})
	ptah := oneTable("books", goschema.Field{Name: "author_id", Type: "integer",
		Foreign: "authors(id)"})

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_DifferentReferentialActionIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "a", Type: "integer",
		Foreign: "u(id)", OnDelete: "CASCADE"})
	ptah := oneTable("t", goschema.Field{Name: "a", Type: "integer",
		Foreign: "u(id)", OnDelete: "NO ACTION"})

	assertOneDiffContains(c, diff(atlas, ptah), "del=cascade")
}

func TestFacts_CompositePrimaryKeyMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

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

	got := strings.Join(diff(atlas, ptah), "; ")
	c.Assert(got, qt.Contains, "group_id")
	c.Assert(got, qt.Contains, "~primary_key")
}

func TestFacts_CompositePrimaryKeyAgreesWhenBothComplete(t *testing.T) {
	c := qt.New(t)

	both := func() *goschema.Database {
		return &goschema.Database{
			Tables: []goschema.Table{{StructName: "m", Name: "m", PrimaryKey: []string{"user_id", "group_id"}}},
			Fields: []goschema.Field{
				{StructName: "m", Name: "user_id", Type: "integer"},
				{StructName: "m", Name: "group_id", Type: "integer"},
			},
		}
	}

	c.Assert(diff(both(), both()), qt.IsNil)
}

func TestFacts_MissingColumnIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t",
		goschema.Field{Name: "a", Type: "integer"},
		goschema.Field{Name: "b", Type: "integer"},
	)
	ptah := oneTable("t", goschema.Field{Name: "a", Type: "integer"})

	assertOneDiffContains(c, diff(atlas, ptah), "b")
}

func TestFacts_DefaultValueMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "status", Type: "text", Default: "active", DefaultSet: true})
	ptah := oneTable("t", goschema.Field{Name: "status", Type: "text", Default: "archived", DefaultSet: true})

	assertOneDiffContains(c, diff(atlas, ptah), "value(active)")
}

func TestFacts_StringDefaultCaseMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "status", Type: "text", Default: "Active", DefaultSet: true})
	ptah := oneTable("t", goschema.Field{Name: "status", Type: "text", DefaultExpr: "'active'::text"})

	assertOneDiffContains(c, diff(atlas, ptah), "value(Active)")
}

func TestFacts_DefaultCastInsideStringLiteralIsNotStripped(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "code", Type: "text", Default: "a::b", DefaultSet: true})
	ptah := oneTable("t", goschema.Field{Name: "code", Type: "text", DefaultExpr: "'a::b'::text"})

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_DefaultConstantSpellingsAreEquivalent(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t",
		goschema.Field{Name: "status", Type: "text", Default: "active", DefaultSet: true},
		goschema.Field{Name: "paid", Type: "boolean", Default: "false", DefaultSet: true},
		goschema.Field{Name: "subtotal", Type: "decimal(12,2)", Default: "0", DefaultSet: true},
	)
	ptah := oneTable("t",
		goschema.Field{Name: "status", Type: "text", DefaultExpr: "'active'::character varying"},
		goschema.Field{Name: "paid", Type: "boolean", DefaultExpr: "false"},
		goschema.Field{Name: "subtotal", Type: "decimal(12,2)", DefaultExpr: "0.00"},
	)

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_NumericDefaultScaleSpellingsAreEquivalent(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "subtotal", Type: "decimal(12,2)", Default: "0", DefaultSet: true})
	ptah := oneTable("t", goschema.Field{Name: "subtotal", Type: "decimal(12,2)", Default: "0.00", DefaultSet: true})

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_DefaultBareExpressionMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "created_at", Type: "timestamp", DefaultExpr: "CURRENT_TIMESTAMP"})
	ptah := oneTable("t", goschema.Field{Name: "created_at", Type: "timestamp", DefaultExpr: "NOW()"})

	assertOneDiffContains(c, diff(atlas, ptah), "current_timestamp")
}

func TestFacts_GeneratedColumnAgreesWhenExpressionAndKindMatch(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "email_normalized", Type: "text", GeneratedExpression: "lower(email)", GeneratedKind: "STORED"})
	ptah := oneTable("t", goschema.Field{Name: "email_normalized", Type: "text", GeneratedExpression: "( lower(email) )", GeneratedKind: "stored"})

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_GeneratedColumnKindMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{Name: "email_normalized", Type: "text", GeneratedExpression: "lower(email)", GeneratedKind: "STORED"})
	ptah := oneTable("t", goschema.Field{Name: "email_normalized", Type: "text", GeneratedExpression: "lower(email)", GeneratedKind: "VIRTUAL"})

	assertOneDiffContains(c, diff(atlas, ptah), "kind=stored")
}

func TestFacts_IdentityMetadataMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := oneTable("t", goschema.Field{
		Name: "id", Type: "bigint", IdentityGeneration: "ALWAYS", IdentityStart: "10", IdentityIncrement: "5",
	})
	ptah := oneTable("t", goschema.Field{
		Name: "id", Type: "bigint", IdentityGeneration: "BY_DEFAULT", IdentityStart: "10", IdentityIncrement: "5",
	})

	assertOneDiffContains(c, diff(atlas, ptah), "identity=always start=10 increment=5")
}

func TestFacts_UniqueConstraintAndUniqueIndexFoldTogether(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields:      []goschema.Field{{StructName: "Account", Name: "tenant_id", Type: "integer"}, {StructName: "Account", Name: "email", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Account", Name: "accounts_identity_unique", Type: "UNIQUE", Columns: []string{"tenant_id", "email"}}},
	}
	ptah := &goschema.Database{
		Tables:  []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields:  []goschema.Field{{StructName: "Account", Name: "tenant_id", Type: "integer"}, {StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{StructName: "Account", Name: "accounts_identity_unique", Unique: true, Fields: []string{"tenant_id", "email"}}},
	}

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_UniqueNullsDistinctMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Account", Name: "accounts_email_key", Unique: true,
			Fields: []string{"email"}, NullsDistinct: boolPtr(false),
		}},
	}
	ptah := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Account", Name: "accounts_email_key", Unique: true,
			Fields: []string{"email"}, NullsDistinct: boolPtr(true),
		}},
	}

	assertOneDiffContains(c, diff(atlas, ptah), "nulls=not_distinct")
}

func TestFacts_CheckExpressionMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "budget_cents", Type: "integer"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_budget_check", Type: "CHECK", CheckExpression: "budget_cents >= 0"}},
	}
	ptah := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "budget_cents", Type: "integer"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_budget_check", Type: "CHECK", CheckExpression: "budget_cents > 0"}},
	}

	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, "budget_cents >= 0")
}

func TestFacts_CheckStringLiteralCaseMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "status", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_status_check", Type: "CHECK", CheckExpression: "status IN ('ACTIVE')"}},
	}
	ptah := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "status", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_status_check", Type: "CHECK", CheckExpression: "status IN ('active')"}},
	}

	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, "'ACTIVE'")
}

func TestFacts_CheckStringLiteralDoubleQuotesArePreserved(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "label", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_label_check", Type: "CHECK", CheckExpression: `"label" = 'A "quoted" value'`}},
	}
	ptah := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "label", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_label_check", Type: "CHECK", CheckExpression: `label = 'A quoted value'`}},
	}

	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, `'A "quoted" value'`)
}

func TestFacts_MySQLCheckLiteralSpellingsAreEquivalent(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "status", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_status_check", Type: "CHECK", CheckExpression: "`status` in (_utf8mb4'active',_utf8mb4'archived')"}},
	}
	ptah := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Project", Name: "projects"}},
		Fields:      []goschema.Field{{StructName: "Project", Name: "status", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Project", Name: "projects_status_check", Type: "CHECK", CheckExpression: "`status` in (_utf8mb4\\'active\\',_utf8mb4\\'archived\\')"}},
	}

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_CheckGeneratedNameDifferenceIsEquivalent(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields:      []goschema.Field{{StructName: "Account", Name: "status", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Account", Name: "accounts_check", Type: "CHECK", CheckExpression: "status IN ('active', 'suspended')"}},
	}
	ptah := &goschema.Database{
		Tables:      []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields:      []goschema.Field{{StructName: "Account", Name: "status", Type: "text"}},
		Constraints: []goschema.Constraint{{StructName: "Account", Name: "accounts_status_check", Type: "CHECK", CheckExpression: "status IN ('active', 'suspended')"}},
	}

	c.Assert(diff(atlas, ptah), qt.IsNil)
}

func TestFacts_IndexMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables:  []goschema.Table{{StructName: "Contact", Name: "contacts"}},
		Fields:  []goschema.Field{{StructName: "Contact", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{StructName: "Contact", Name: "idx_contacts_email", Fields: []string{"email"}}},
	}
	ptah := &goschema.Database{
		Tables:  []goschema.Table{{StructName: "Contact", Name: "contacts"}},
		Fields:  []goschema.Field{{StructName: "Contact", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{StructName: "Contact", Name: "idx_contacts_email", Fields: []string{"email_normalized"}}},
	}

	assertOneDiffContains(c, diff(atlas, ptah), "email_normalized")
}

func TestFacts_ExpressionIndexKeepsQualifiedExpression(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Account",
			Name:       "idx_accounts_email_expr",
			Parts:      []goschema.IndexPart{{Expr: "lower(account.email)"}},
		}},
	}
	ptah := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Account",
			Name:       "idx_accounts_email_expr",
			Parts:      []goschema.IndexPart{{Expr: "upper(account.email)"}},
		}},
	}

	assertOneDiffContains(c, diff(atlas, ptah), "expr(lower(account.email))")
}

func TestFacts_IndexPartMetadataMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Account",
			Name:       "idx_accounts_email",
			Parts:      []goschema.IndexPart{{Name: "email", Operator: "text_pattern_ops", Prefix: "16", Desc: true}},
		}},
	}
	ptah := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
		Indexes: []goschema.Index{{
			StructName: "Account",
			Name:       "idx_accounts_email",
			Parts:      []goschema.IndexPart{{Name: "email"}},
		}},
	}

	assertOneDiffContains(c, diff(atlas, ptah), "email op=text_pattern_ops prefix=16 desc")
}

func TestFacts_EnumDefinitionsMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Enums: []goschema.Enum{{Name: "enum_account_status", Values: []string{"active", "suspended"}}},
	}
	ptah := &goschema.Database{
		Enums: []goschema.Enum{{Name: "enum_account_status", Values: []string{"active"}}},
	}

	assertOneDiffContains(c, diff(atlas, ptah), "values=active,suspended")
}

func TestFacts_CommentsMismatchIsAGap(t *testing.T) {
	c := qt.New(t)

	atlas := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts", Comment: "Customer accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text", Comment: "Login email"}},
	}
	ptah := &goschema.Database{
		Tables: []goschema.Table{{StructName: "Account", Name: "accounts"}},
		Fields: []goschema.Field{{StructName: "Account", Name: "email", Type: "text"}},
	}

	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, "comment=Customer accounts")
	c.Assert(strings.Join(diff(atlas, ptah), "; "), qt.Contains, "comment=Login email")
}
