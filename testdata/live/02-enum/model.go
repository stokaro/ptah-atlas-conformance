package models

//ptah:schema:table name="accounts"
type Account struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="status" type="ENUM" enum="active,suspended,deleted" not_null="true" default="active"
	Status string
}
