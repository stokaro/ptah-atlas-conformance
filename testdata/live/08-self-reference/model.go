package models

//migrator:schema:table name="categories"
type Category struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="parent_id" type="INTEGER" foreign="categories(id)" foreign_key_name="fk_categories_parent" on_delete="SET NULL"
	ParentID *int64
	//migrator:schema:field name="name" type="VARCHAR(128)" not_null="true"
	Name string
}
