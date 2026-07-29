package models

// Standalone PostgreSQL sequence with non-default START/INCREMENT/CACHE/CYCLE
// options. Proves Ptah round-trips a first-class CREATE SEQUENCE object that
// Atlas CE cannot introspect, that a SERIAL column's implicit backing sequence
// is not surfaced as a spurious standalone sequence, and that a column which
// consumes the sequence via DEFAULT nextval(...) round-trips cleanly
// (stokaro/ptah#675 normalizes the nextval('...'::regclass) read-back form).

//ptah:schema:sequence name="order_number_seq" as="bigint" start="1000" increment="5" cache="20" cycle="true"
type OrderNumberSeq struct{}

//ptah:schema:table name="orders"
type Order struct {
	// A separate SERIAL column: its implicit backing sequence must NOT surface
	// as a spurious standalone sequence in the round-trip diff.
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64

	// Consumes the standalone sequence via DEFAULT nextval(...).
	//ptah:schema:field name="order_number" type="BIGINT" not_null="true" default_expr="nextval('order_number_seq')"
	OrderNumber int64

	//ptah:schema:field name="total" type="INTEGER" not_null="true"
	Total int64
}
