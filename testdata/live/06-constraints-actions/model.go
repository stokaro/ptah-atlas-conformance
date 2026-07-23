package models

//migrator:schema:table name="organizations"
type Organization struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="slug" type="VARCHAR(64)" not_null="true" unique="true"
	Slug string
}

//migrator:schema:table name="projects"
//migrator:schema:constraint name="projects_org_slug_unique" type="UNIQUE" table="projects" columns="organization_id,slug"
//migrator:schema:constraint name="projects_status_check" type="CHECK" table="projects" check="status IN ('active', 'archived')"
type Project struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="organization_id" type="INTEGER" not_null="true" foreign="organizations(id)" foreign_key_name="fk_projects_organization" on_delete="CASCADE" on_update="RESTRICT"
	OrganizationID int64
	//migrator:schema:field name="slug" type="VARCHAR(64)" not_null="true"
	Slug string
	//migrator:schema:field name="status" type="VARCHAR(16)" not_null="true" default="active"
	Status string
	//migrator:schema:field name="budget_cents" type="INTEGER" not_null="true" default_expr="0" check="budget_cents >= 0" check_name="projects_budget_nonnegative"
	BudgetCents int64
}
