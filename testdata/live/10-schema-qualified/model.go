package models

//migrator:schema:schema name="auth"
type AuthSchema struct{}

//migrator:schema:schema name="billing"
type BillingSchema struct{}

//migrator:schema:table schema="auth" name="users"
type AuthUser struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="email" type="VARCHAR(255)" not_null="true" unique="true"
	Email string
}

//migrator:schema:table schema="billing" name="users"
type BillingUser struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="external_id" type="VARCHAR(64)" not_null="true" unique="true"
	ExternalID string
}

//migrator:schema:table schema="billing" name="invoices"
type Invoice struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="user_id" type="INTEGER" not_null="true" foreign="auth.users(id)" foreign_key_name="fk_invoices_auth_user" on_delete="CASCADE"
	UserID int64
	//migrator:schema:field name="number" type="VARCHAR(32)" not_null="true"
	Number string
	//migrator:schema:index table="billing.invoices" name="idx_invoices_user_number" fields="user_id,number" unique="true"
	_ struct{}
}
