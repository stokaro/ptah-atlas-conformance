package models

//ptah:schema:table name="contacts"
type Contact struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="email" type="VARCHAR(255)" not_null="true"
	Email string
	//ptah:schema:field name="email_normalized" type="VARCHAR(255)" generated="lower(email)" generated_kind="stored"
	EmailNormalized string
	//ptah:schema:index table="contacts" name="idx_contacts_email_normalized" fields="email_normalized"
	_ struct{}
}
