package models

//migrator:schema:table name="products"
type Product struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="name" type="VARCHAR(255)" not_null="true"
	Name string
	//migrator:schema:field name="archived" type="BOOLEAN" not_null="true" default="false"
	Archived bool
}

//migrator:schema:view name="live_products" body="SELECT id, name FROM products WHERE archived = false"
type LiveProductsView struct{}
