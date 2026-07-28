package probe_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const ormProviderDDL = `CREATE TABLE users (
	id integer PRIMARY KEY,
	email text NOT NULL
);
CREATE UNIQUE INDEX idx_users_email ON users(email);
CREATE TABLE pets (
	id integer PRIMARY KEY,
	user_id integer NOT NULL,
	CONSTRAINT fk_pets_user FOREIGN KEY (user_id) REFERENCES users(id)
);`

const ormProviderRenderedDDL = `Found 2 tables, 4 fields, 1 indexes, 0 enums, 0 embedded fields

=== SQLITE SCHEMA ===

CREATE TABLE "users" (
	"id" INTEGER PRIMARY KEY,
	"email" TEXT NOT NULL
);
CREATE UNIQUE INDEX "idx_users_email" ON "users" ("email");
CREATE TABLE "pets" (
	"id" INTEGER PRIMARY KEY,
	"user_id" INTEGER NOT NULL
);
ALTER TABLE "pets" ADD CONSTRAINT "fk_pets_user" FOREIGN KEY ("user_id") REFERENCES "users"("id");`

func TestORMProviderSmokeProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(c, t, ormProviderRenderedDDL)
	gormModule := filepath.Join(fixtureRoot, "gorm", "go.mod")
	before, err := os.ReadFile(gormModule)
	c.Assert(err, qt.IsNil)

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:            fixtureRoot,
		Binary:                 bin,
		GORMCommand:            []string{"sh", "provider.sh"},
		SQLAlchemyCommand:      []string{"sh", "provider.sh"},
		ProviderCommandTimeout: 5 * time.Second,
		PtahCommandTimeout:     5 * time.Second,
	}.Run()

	c.Assert(results, qt.HasLen, 4)
	c.Assert(results, qt.DeepEquals, []probe.Result{
		{
			Probe:   "orm-provider-smoke",
			Fixture: "gorm",
			Stage:   "provider output",
			Outcome: probe.OK,
			Detail:  "GORM provider v0.6.1 provider output preserved two tables, primary keys, a unique index, and a foreign key",
		},
		{
			Probe:   "orm-provider-smoke",
			Fixture: "gorm",
			Stage:   "ptah schema render",
			Outcome: probe.OK,
			Detail:  "GORM provider v0.6.1 ptah schema render preserved two tables, primary keys, a unique index, and a foreign key",
		},
		{
			Probe:   "orm-provider-smoke",
			Fixture: "sqlalchemy",
			Stage:   "provider output",
			Outcome: probe.OK,
			Detail:  "SQLAlchemy provider 0.4.1 with SQLAlchemy 2.0.50 provider output preserved two tables, primary keys, a unique index, and a foreign key",
		},
		{
			Probe:   "orm-provider-smoke",
			Fixture: "sqlalchemy",
			Stage:   "ptah schema render",
			Outcome: probe.OK,
			Detail:  "SQLAlchemy provider 0.4.1 with SQLAlchemy 2.0.50 ptah schema render preserved two tables, primary keys, a unique index, and a foreign key",
		},
	})
	after, err := os.ReadFile(gormModule)
	c.Assert(err, qt.IsNil)
	c.Assert(after, qt.DeepEquals, before)
}

func TestORMProviderSmokeProbe_BehavioralMismatchIsGap(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(c, t, `Found 1 tables
CREATE TABLE "users" ("id" INTEGER PRIMARY KEY);`)

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:       fixtureRoot,
		Binary:            bin,
		GORMCommand:       []string{"sh", "provider.sh"},
		SQLAlchemyCommand: []string{"sh", "provider.sh"},
	}.Run()

	c.Assert(results, qt.HasLen, 4)
	c.Check(results[0].Outcome, qt.Equals, probe.OK)
	c.Check(results[1].Outcome, qt.Equals, probe.Gap)
	c.Check(results[1].Issue, qt.Equals, "stokaro/ptah#669")
	c.Check(results[1].Detail, qt.Contains, "missing expected schema facts")
	c.Check(results[2].Outcome, qt.Equals, probe.OK)
	c.Check(results[3].Outcome, qt.Equals, probe.Gap)
	c.Check(results[3].Issue, qt.Equals, "stokaro/ptah#669")
	c.Check(results[3].Detail, qt.Contains, "missing expected schema facts")
}

func TestORMProviderSmokeProbe_HarnessFailureIsFail(t *testing.T) {
	c := qt.New(t)
	fixtureRoot := filepath.Join(t.TempDir(), "missing")

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot: fixtureRoot,
		Binary:      "unused",
	}.Run()

	c.Assert(results, qt.HasLen, 1)
	c.Check(results[0].Probe, qt.Equals, "orm-provider-smoke")
	c.Check(results[0].Fixture, qt.Equals, "orm providers")
	c.Check(results[0].Stage, qt.Equals, "fixture setup")
	c.Check(results[0].Outcome, qt.Equals, probe.Fail)
	c.Check(results[0].Detail, qt.Contains, "stat fixture root")
	c.Check(results[0].Issue, qt.Equals, "")
}

func TestRenderORMProviderMarkdown(t *testing.T) {
	c := qt.New(t)
	results := []probe.Result{
		{
			Probe:   "orm-provider-smoke",
			Fixture: "gorm",
			Stage:   "ptah schema render",
			Outcome: probe.Gap,
			Detail:  "missing expected schema facts: pets-to-users foreign key",
			Issue:   "stokaro/ptah#669",
		},
	}

	report := probe.RenderORMProviderMarkdown(results, "v0.0.0-test", "go run ./cmd/gap-probe-orm-providers")

	c.Check(report, qt.Contains, "# Ptah ORM provider conformance report")
	c.Check(report, qt.Contains, "ariga.io/atlas-provider-gorm@v0.6.1")
	c.Check(report, qt.Contains, "atlas-provider-sqlalchemy==0.4.1")
	c.Check(report, qt.Contains, "SQLAlchemy==2.0.50")
	c.Check(report, qt.Contains, "Ptah at `v0.0.0-test`")
	c.Check(report, qt.Contains, "| **RED** | **gap** | gorm | ptah schema render |")
	c.Check(report, qt.Contains, "| #669 |")
}

func makeORMProviderTestFixtures(c *qt.C, t *testing.T, ptahOutput string) (string, string) {
	fixtureRoot := t.TempDir()
	for _, provider := range []string{"gorm", "sqlalchemy"} {
		dir := filepath.Join(fixtureRoot, provider)
		c.Assert(os.MkdirAll(dir, 0o700), qt.IsNil)
		providerScript := "#!/bin/sh\ncat <<'SQL'\n" + ormProviderDDL + "\nSQL\n"
		c.Assert(os.WriteFile(filepath.Join(dir, "provider.sh"), []byte(providerScript), 0o700), qt.IsNil)
	}
	c.Assert(os.WriteFile(
		filepath.Join(fixtureRoot, "gorm", "go.mod"),
		[]byte("module example.test/orm-provider\n\ngo 1.26.5\n"),
		0o600,
	), qt.IsNil)

	bin := filepath.Join(fixtureRoot, "ptah")
	ptahScript := "#!/bin/sh\ncat <<'SQL'\n" + ptahOutput + "\nSQL\n"
	c.Assert(os.WriteFile(bin, []byte(ptahScript), 0o700), qt.IsNil)
	return fixtureRoot, bin
}
