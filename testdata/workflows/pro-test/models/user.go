// Package models declares the desired schema for the pro-test workflow's
// `atlas schema test` cases via Ptah's Go schema annotations.
package models

//ptah:schema:table name="users"
type User struct {
	//ptah:schema:field name="id" type="INTEGER" primary="true"
	ID int64

	//ptah:schema:field name="name" type="TEXT" not_null="true"
	Name string
}
