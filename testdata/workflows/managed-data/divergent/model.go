// Package models declares the same countries table but a divergent desired data
// set (countries.yaml here drops a row). Reconciling it against the seeded
// database would delete an existing row, so `ptah migrations data` must refuse
// it at generation time unless --allow-destructive is passed. The
// managed-data-workflow probe uses this to prove the destructive gate
// (stokaro/ptah#663).
package models

//migrator:schema:table name="countries"
//migrator:schema:data table="countries" key="code" file="countries.yaml"
type Country struct {
	//migrator:schema:field name="code" type="TEXT" primary="true"
	Code string

	//migrator:schema:field name="name" type="TEXT" not_null="true"
	Name string
}
