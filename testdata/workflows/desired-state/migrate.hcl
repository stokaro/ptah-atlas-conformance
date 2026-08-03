env "dev" {
  url = "sqlite://source.db"
  dev = "sqlite://migrate-env-dev.db"

  migration {
    dir = "file://migrate-env"
  }
}
