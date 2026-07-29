package models

//ptah:schema:table name="products"
type Product struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="name" type="VARCHAR(255)" not_null="true"
	Name string
	//ptah:schema:field name="archived" type="BOOLEAN" not_null="true" default="false"
	Archived bool
}

//ptah:schema:view name="live_products" body="SELECT id, name FROM products WHERE archived = false"
type LiveProductsView struct{}
