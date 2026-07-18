package models

//migrator:schema:table name="accounts"
type Account struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="status" type="ENUM" enum="active,suspended,deleted" not_null="true" default="active"
	Status string
}
