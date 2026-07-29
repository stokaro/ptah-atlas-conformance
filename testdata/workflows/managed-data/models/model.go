// Package models declares a table and its managed reference/seed rows for the
// managed-data-workflow conformance probe. The //ptah:schema:data
// annotation points at countries.yaml, whose rows Ptah manages as desired-state
// data for the countries table (stokaro/ptah#663).
package models

//ptah:schema:table name="countries"
//ptah:schema:data table="countries" key="code" file="countries.yaml"
type Country struct {
	//ptah:schema:field name="code" type="TEXT" primary="true"
	Code string

	//ptah:schema:field name="name" type="TEXT" not_null="true"
	Name string
}
