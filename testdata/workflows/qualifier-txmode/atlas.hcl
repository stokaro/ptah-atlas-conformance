env "local" {
  dev = "sqlite://ci-dev.db"
  schema {
    src = "file://schema.sql"
  }
  migration {
    dir = "file://generated"
  }
  diff {
    concurrent_index {
      create = true
    }
  }
}
