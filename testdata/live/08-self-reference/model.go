package models

//ptah:schema:table name="categories"
type Category struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="parent_id" type="INTEGER" foreign="categories(id)" foreign_key_name="fk_categories_parent" on_delete="SET NULL"
	ParentID *int64
	//ptah:schema:field name="name" type="VARCHAR(128)" not_null="true"
	Name string
}
