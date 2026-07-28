env "local" {
  url = getenv("PTAH_ATLAS_PROJECT_CONFIG_E2E_URL")

  migration {
    dir              = "file://project-migrations"
    format           = atlas
    revisions_schema = "main"
  }
}
