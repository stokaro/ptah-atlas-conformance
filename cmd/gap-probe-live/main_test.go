package main

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp"
)

func TestConfiguredLiveTargetsAlwaysIncludesSQLite(t *testing.T) {
	c := qt.New(t)

	targets := configuredLiveTargets("/tmp/live", func(string) string { return "" })

	c.Assert(targets, qt.CmpEquals(cmp.AllowUnexported(liveTarget{})), []liveTarget{
		{label: "sqlite", url: "sqlite:///tmp/live/conformance.sqlite"},
	})
}

func TestConfiguredLiveTargetsIncludesConfiguredNetworkDialects(t *testing.T) {
	c := qt.New(t)

	env := map[string]string{
		"CONFORMANCE_POSTGRES_URL": "postgres://postgres:pw@localhost:5432/conf?sslmode=disable",
		"CONFORMANCE_MYSQL_URL":    "mysql://root:pw@tcp(localhost:3306)/conf",
		"CONFORMANCE_MARIADB_URL":  "mariadb://root:pw@tcp(localhost:3307)/conf",
		"CONFORMANCE_SQLITE_URL":   "sqlite:///custom/live.sqlite",
	}
	targets := configuredLiveTargets("/tmp/ignored", func(key string) string { return env[key] })

	c.Assert(targets, qt.CmpEquals(cmp.AllowUnexported(liveTarget{})), []liveTarget{
		{label: "postgres", url: env["CONFORMANCE_POSTGRES_URL"]},
		{label: "mysql", url: env["CONFORMANCE_MYSQL_URL"]},
		{label: "mariadb", url: env["CONFORMANCE_MARIADB_URL"]},
		{label: "sqlite", url: env["CONFORMANCE_SQLITE_URL"]},
	})
}
