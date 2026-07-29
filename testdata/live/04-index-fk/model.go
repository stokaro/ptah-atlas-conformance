package models

//ptah:schema:table name="authors"
type Author struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="name" type="VARCHAR(255)" not_null="true"
	Name string
}

//ptah:schema:table name="books"
type Book struct {
	//ptah:schema:field name="id" type="SERIAL" primary="true"
	ID int64
	//ptah:schema:field name="title" type="VARCHAR(255)" not_null="true"
	Title string
	//ptah:schema:field name="author_id" type="INTEGER" not_null="true" foreign="authors(id)"
	AuthorID int64
	//ptah:schema:index table="books" name="idx_books_title" fields="title"
	_ struct{}
}
