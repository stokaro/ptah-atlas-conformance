package models

//ptah:schema:table name="invoices"
type Invoice struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="invoice_number" type="VARCHAR(32)" not_null="true" unique="true"
	InvoiceNumber string
	//ptah:schema:field name="subtotal" type="DECIMAL(12,2)" not_null="true" default="0.00"
	Subtotal string
	//ptah:schema:field name="tax_rate" type="DECIMAL(5,4)" not_null="true" default_expr="0"
	TaxRate string
	//ptah:schema:field name="issued_at" type="TIMESTAMP" not_null="true" default_expr="CURRENT_TIMESTAMP"
	IssuedAt string
	//ptah:schema:field name="paid" type="BOOLEAN" not_null="true" default="false"
	Paid bool
}
