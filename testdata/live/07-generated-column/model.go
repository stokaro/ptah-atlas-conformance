package models

//migrator:schema:table name="contacts"
type Contact struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="email" type="VARCHAR(255)" not_null="true"
	Email string
	//migrator:schema:field name="email_normalized" type="VARCHAR(255)" generated="lower(email)" generated_kind="stored"
	EmailNormalized string
}

//migrator:schema:index table="contacts" name="idx_contacts_email_normalized" columns="email_normalized"
