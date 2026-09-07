schema "main" {
}

table "users" {
  schema = schema.main
  column "id" {
    null = false
    type = integer
  }
  primary_key {
    columns = [column.id]
  }
}
