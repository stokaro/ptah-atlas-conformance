package models

// Standalone PostgreSQL sequence with non-default START/INCREMENT/CACHE/CYCLE
// options. Proves Ptah round-trips a first-class CREATE SEQUENCE object that
// Atlas CE cannot introspect, and that a SERIAL column's implicit backing
// sequence is not surfaced as a spurious standalone sequence.
//
// A column that consumes the sequence via DEFAULT nextval(...) is intentionally
// omitted here: PostgreSQL reads such a default back as
// nextval('...'::regclass), which Ptah's compare does not yet normalize
// (stokaro/ptah#675). This fixture isolates the sequence-object round-trip.
//
//migrator:schema:sequence name="order_number_seq" as="bigint" start="1000" increment="5" cache="20" cycle="true"
type OrderNumberSeq struct{}

//migrator:schema:table name="orders"
type Order struct {
	// A separate SERIAL column: its implicit backing sequence must NOT surface
	// as a spurious standalone sequence in the round-trip diff.
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64

	//migrator:schema:field name="total" type="INTEGER" not_null="true"
	Total int64
}
