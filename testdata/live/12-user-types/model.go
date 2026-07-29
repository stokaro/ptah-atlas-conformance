package models

// PostgreSQL user-defined types: a domain with a CHECK, a domain over a
// non-canonical base type (VARCHAR), a composite type with a parameterized
// field type (NUMERIC), and a range type. Proves Ptah round-trips first-class
// user-defined types that Atlas CE cannot introspect, exercising the readback
// type-canonicalization and comma-in-type parsing paths.

//ptah:schema:domain name="email" type="TEXT" check="VALUE ~ '@'"
type EmailDomain struct{}

//ptah:schema:domain name="pincode" type="VARCHAR(255)" not_null="true"
type PinDomain struct{}

//ptah:schema:composite name="money_amount" fields="amount:NUMERIC(10,2),cur:VARCHAR(3)"
type MoneyType struct{}

//ptah:schema:range name="floatrange" subtype="float8" subtype_diff="float8mi"
type FloatRange struct{}

//ptah:schema:table name="accounts"
type Account struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64

	//ptah:schema:field name="label" type="TEXT" not_null="true"
	Label string
}
