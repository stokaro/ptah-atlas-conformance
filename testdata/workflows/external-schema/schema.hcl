table "users" {
  column "id" {
    type = integer
  }
  column "email" {
    type = text
    null = false
  }
  primary_key {
    columns = [column.id]
  }
  index "idx_users_email" {
    unique  = true
    columns = [column.email]
  }
}

table "posts" {
  column "id" {
    type = integer
  }
  column "user_id" {
    type = integer
    null = false
  }
  column "title" {
    type = text
    null = false
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "fk_posts_user" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_delete   = CASCADE
  }
  index "idx_posts_user_id" {
    columns = [column.user_id]
  }
}
