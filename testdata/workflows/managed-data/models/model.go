// Package models declares a table and its managed reference/seed rows for the
// managed-data-workflow conformance probe. The //migrator:schema:data
// annotation points at countries.yaml, whose rows Ptah manages as desired-state
// data for the countries table (stokaro/ptah#663).
package models

//migrator:schema:table name="countries"
//migrator:schema:data table="countries" key="code" file="countries.yaml"
type Country struct {
	//migrator:schema:field name="code" type="TEXT" primary="true"
	Code string

	//migrator:schema:field name="name" type="TEXT" not_null="true"
	Name string
}
