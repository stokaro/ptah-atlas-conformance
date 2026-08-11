package main

import (
	"os"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp"
)

func TestEnvironmentWithoutPtahVariables(t *testing.T) {
	t.Setenv("PTAH_ATLAS_STRICT_COMPAT", "1")
	t.Setenv("ptah_token", "secret")
	t.Setenv("CONFORMANCE_KEEP", "present")
	c := qt.New(t)

	environment := environmentWithoutPtahVariables()

	c.Assert(environment, qt.Not(qt.Contains), "PTAH_ATLAS_STRICT_COMPAT=1")
	c.Assert(environment, qt.Not(qt.Contains), "ptah_token=secret")
	c.Assert(environment, qt.Contains, "CONFORMANCE_KEEP=present")
	_, ok := os.LookupEnv("PTAH_ATLAS_STRICT_COMPAT")
	c.Assert(ok, qt.IsTrue)
}

func TestConfiguredDifferentialTargetsAlwaysIncludesSQLite(t *testing.T) {
	c := qt.New(t)

	targets := configuredDifferentialTargets("/tmp/diff", func(string) string { return "" })

	c.Assert(targets, qt.CmpEquals(cmp.AllowUnexported(differentialTarget{})), []differentialTarget{
		{label: "sqlite", ptahURL: "sqlite:///tmp/diff/conformance.sqlite", atlasURL: "sqlite:///tmp/diff/conformance.sqlite"},
	})
}

func TestConfiguredDifferentialTargetsIncludesConfiguredNetworkDialects(t *testing.T) {
	c := qt.New(t)

	env := map[string]string{
		"CONFORMANCE_POSTGRES_URL":     "postgres://postgres:pw@localhost:5432/conf?sslmode=disable",
		"CONFORMANCE_MYSQL_URL":        "mysql://root:pw@tcp(localhost:3306)/conf",
		"CONFORMANCE_SQLITE_URL":       "sqlite:///custom/diff.sqlite",
		"CONFORMANCE_MYSQL_ATLAS_URL":  "mysql://root:pw@localhost:3306/conf",
		"CONFORMANCE_SQLITE_ATLAS_URL": "sqlite:///custom/atlas-diff.sqlite",
	}
	targets := configuredDifferentialTargets("/tmp/ignored", func(key string) string { return env[key] })

	c.Assert(targets, qt.CmpEquals(cmp.AllowUnexported(differentialTarget{})), []differentialTarget{
		{
			label:    "postgres",
			ptahURL:  env["CONFORMANCE_POSTGRES_URL"],
			atlasURL: env["CONFORMANCE_POSTGRES_URL"],
		},
		{
			label:    "mysql",
			ptahURL:  env["CONFORMANCE_MYSQL_URL"],
			atlasURL: env["CONFORMANCE_MYSQL_ATLAS_URL"],
		},
		{
			label:    "sqlite",
			ptahURL:  env["CONFORMANCE_SQLITE_URL"],
			atlasURL: env["CONFORMANCE_SQLITE_ATLAS_URL"],
		},
	})
}

func TestAtlasMySQLURLConvertsGoDriverTCPAuthority(t *testing.T) {
	c := qt.New(t)

	got := atlasMySQLURL("mysql://root:pw@tcp(localhost:3306)/conf?parseTime=true")

	c.Assert(got, qt.Equals, "mysql://root:pw@localhost:3306/conf?parseTime=true")
}
