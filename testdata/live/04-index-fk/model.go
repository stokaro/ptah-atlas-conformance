package models

//migrator:schema:table name="authors"
type Author struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="name" type="VARCHAR(255)" not_null="true"
	Name string
}

//migrator:schema:table name="books"
type Book struct {
	//migrator:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//migrator:schema:field name="title" type="VARCHAR(255)" not_null="true"
	Title string
	//migrator:schema:field name="author_id" type="INTEGER" not_null="true" foreign="authors(id)"
	AuthorID int64
}

//migrator:schema:index table="books" name="idx_books_title" columns="title"
