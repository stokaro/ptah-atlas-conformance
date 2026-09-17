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

const ormProviderRenderedDDL = `CREATE TABLE "users" (
	"id" INTEGER PRIMARY KEY,
	"email" TEXT NOT NULL
);
CREATE UNIQUE INDEX "idx_users_email" ON "users" ("email");
CREATE TABLE "pets" (
	"id" INTEGER PRIMARY KEY,
	"user_id" INTEGER NOT NULL
);
ALTER TABLE "pets" ADD CONSTRAINT "fk_pets_user" FOREIGN KEY ("user_id") REFERENCES "users"("id");`

const ormProviderRenderProgress = `Found 2 tables, 4 fields, 1 indexes, 0 enums, 0 embedded fields`

func TestORMProviderSmokeProbe_HappyPath(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(c, t, ormProviderRenderedDDL, ormProviderRenderProgress)
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
			Detail:  "SQLAlchemy provider provider output preserved two tables, primary keys, a unique index, and a foreign key",
		},
		{
			Probe:   "orm-provider-smoke",
			Fixture: "sqlalchemy",
			Stage:   "ptah schema render",
			Outcome: probe.OK,
			Detail:  "SQLAlchemy provider ptah schema render preserved two tables, primary keys, a unique index, and a foreign key",
		},
	})
	after, err := os.ReadFile(gormModule)
	c.Assert(err, qt.IsNil)
	c.Assert(after, qt.DeepEquals, before)
}

func TestORMProviderSmokeProbe_BehavioralMismatchIsGap(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(
		c,
		t,
		`CREATE TABLE "users" ("id" INTEGER PRIMARY KEY);`,
		"Found 1 tables",
	)

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

func TestORMProviderSmokeProbe_RenderProgressOnStdoutIsGap(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(
		c,
		t,
		ormProviderRenderProgress+"\n\n=== SQLITE SCHEMA ===\n\n"+ormProviderRenderedDDL,
		ormProviderRenderProgress,
	)

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:       fixtureRoot,
		Binary:            bin,
		GORMCommand:       []string{"sh", "provider.sh"},
		SQLAlchemyCommand: []string{"sh", "provider.sh"},
	}.Run()

	c.Assert(results, qt.HasLen, 4)
	c.Assert(results[1].Outcome, qt.Equals, probe.Gap)
	c.Assert(results[1].Detail, qt.Equals, "render stdout contains non-SQL progress text")
	c.Assert(results[3].Outcome, qt.Equals, probe.Gap)
	c.Assert(results[3].Detail, qt.Equals, "render stdout contains non-SQL progress text")
}

func TestORMProviderSmokeProbe_MissingRenderProgressIsGap(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(c, t, ormProviderRenderedDDL, "")

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:       fixtureRoot,
		Binary:            bin,
		GORMCommand:       []string{"sh", "provider.sh"},
		SQLAlchemyCommand: []string{"sh", "provider.sh"},
	}.Run()

	c.Assert(results, qt.HasLen, 4)
	c.Assert(results[1].Outcome, qt.Equals, probe.Gap)
	c.Assert(results[1].Detail, qt.Equals, "render stderr is missing the Ptah two-table progress summary")
	c.Assert(results[3].Outcome, qt.Equals, probe.Gap)
	c.Assert(results[3].Detail, qt.Equals, "render stderr is missing the Ptah two-table progress summary")
}

func TestORMProviderSmokeProbe_NonSQLRenderStdoutIsGap(t *testing.T) {
	c := qt.New(t)
	fixtureRoot, bin := makeORMProviderTestFixtures(
		c,
		t,
		"provider warning that is not SQL\n"+ormProviderRenderedDDL,
		ormProviderRenderProgress,
	)

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:       fixtureRoot,
		Binary:            bin,
		GORMCommand:       []string{"sh", "provider.sh"},
		SQLAlchemyCommand: []string{"sh", "provider.sh"},
	}.Run()

	c.Assert(results, qt.HasLen, 4)
	c.Assert(results[1].Outcome, qt.Equals, probe.Gap)
	c.Assert(results[1].Detail, qt.Contains, "render stdout is not valid SQL")
	c.Assert(results[3].Outcome, qt.Equals, probe.Gap)
	c.Assert(results[3].Detail, qt.Contains, "render stdout is not valid SQL")
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

	// The versions are the argument's, and no version in this test is one the
	// repository pins, so a header that printed a pin of its own would show.
	pins := probe.SQLAlchemyPins{Provider: "9.9.9", ORM: "8.8.8"}
	report := probe.RenderORMProviderMarkdown(results, pins, "v0.0.0-test", "go run ./cmd/gap-probe-orm-providers")

	c.Check(report, qt.Contains, "# Ptah ORM provider conformance report")
	c.Check(report, qt.Contains, "ariga.io/atlas-provider-gorm@v0.6.1")
	c.Check(report, qt.Contains, "atlas-provider-sqlalchemy==9.9.9")
	c.Check(report, qt.Contains, "SQLAlchemy==8.8.8")
	c.Check(report, qt.Contains, "Ptah at `v0.0.0-test`")
	c.Check(report, qt.Contains, "| **RED** | **gap** | gorm | ptah schema render |")
	c.Check(report, qt.Contains, "| #669 |")
}

func makeORMProviderTestFixtures(
	c *qt.C,
	t *testing.T,
	ptahStdout, ptahStderr string,
) (fixtureRoot, bin string) {
	fixtureRoot = t.TempDir()
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

	bin = filepath.Join(fixtureRoot, "ptah")
	ptahScript := "#!/bin/sh\ncat <<'SQL'\n" + ptahStdout + "\nSQL\n" +
		"cat >&2 <<'LOG'\n" + ptahStderr + "\nLOG\n"
	c.Assert(os.WriteFile(bin, []byte(ptahScript), 0o700), qt.IsNil)
	return fixtureRoot, bin
}

// writeSQLAlchemyPins writes the two files the SQLAlchemy fixture is read from.
// The lock is written in the shape uv produces, with a trailing backslash and a
// hash line, so the reader is exercised on the format it actually meets.
func writeSQLAlchemyPins(c *qt.C, dir, intentORM, lockORM string) {
	c.Helper()

	c.Assert(os.MkdirAll(dir, 0o700), qt.IsNil)
	c.Assert(os.WriteFile(
		filepath.Join(dir, "requirements.in"),
		[]byte("atlas-provider-sqlalchemy==0.5.2\nSQLAlchemy=="+intentORM+"\n"),
		0o600,
	), qt.IsNil)
	c.Assert(os.WriteFile(
		filepath.Join(dir, "requirements.txt"),
		[]byte("# generated\natlas-provider-sqlalchemy==0.5.2 \\\n    --hash=sha256:aa\n"+
			"SQLAlchemy=="+lockORM+" \\\n    --hash=sha256:bb\n"),
		0o600,
	), qt.IsNil)
}

// TestSQLAlchemyPinsForFixtures_HappyPath reads the versions out of the fixture
// rather than out of this repository's own pins, which is the whole point: a
// dependency bump moves the fixture and the report follows it.
func TestSQLAlchemyPinsForFixtures_HappyPath(t *testing.T) {
	c := qt.New(t)

	root := t.TempDir()
	writeSQLAlchemyPins(c, filepath.Join(root, "sqlalchemy"), "2.0.99", "2.0.99")

	c.Assert(probe.SQLAlchemyPinsForFixtures(root), qt.Equals, probe.SQLAlchemyPins{
		Provider: "0.5.2",
		ORM:      "2.0.99",
	})
}

// TestSQLAlchemyPinsForFixtures_FailurePath covers the two fixtures that name
// no single version: one where the source and the lock disagree, and one where
// the pin is a range rather than an exact version.
func TestSQLAlchemyPinsForFixtures_FailurePath(t *testing.T) {
	t.Run("the source and the lock disagree", func(t *testing.T) {
		c := qt.New(t)

		root := t.TempDir()
		writeSQLAlchemyPins(c, filepath.Join(root, "sqlalchemy"), "2.0.52", "2.0.54")

		c.Assert(probe.SQLAlchemyPinsForFixtures(root), qt.Equals, probe.SQLAlchemyPins{})
	})

	// Both files carry the range, so the two cannot disagree and the refusal can
	// only come from the requirement that a pin is exact. Written in one file
	// alone, this case passes on a reader that never checks exactness at all.
	t.Run("neither file pins an exact version", func(t *testing.T) {
		c := qt.New(t)

		root := t.TempDir()
		dir := filepath.Join(root, "sqlalchemy")
		c.Assert(os.MkdirAll(dir, 0o700), qt.IsNil)
		for _, name := range []string{"requirements.in", "requirements.txt"} {
			c.Assert(os.WriteFile(
				filepath.Join(dir, name),
				[]byte("atlas-provider-sqlalchemy==0.5.2\nSQLAlchemy>=2.0.54\n"),
				0o600,
			), qt.IsNil)
		}

		c.Assert(probe.SQLAlchemyPinsForFixtures(root), qt.Equals, probe.SQLAlchemyPins{})
	})
}

// TestORMProviderSmokeProbe_RefusesALockTheSourceDidNotChoose drives the
// refusal through the probe, on the path a real run takes: the pins are read
// before the virtual environment is built, so a fixture whose lock nobody chose
// stops there and names both files. The GORM half is overridden so this test
// needs no network.
func TestORMProviderSmokeProbe_RefusesALockTheSourceDidNotChoose(t *testing.T) {
	c := qt.New(t)

	fixtureRoot, bin := makeORMProviderTestFixtures(c, t, ormProviderDDL, "")
	writeSQLAlchemyPins(c, filepath.Join(fixtureRoot, "sqlalchemy"), "2.0.52", "2.0.54")

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:            fixtureRoot,
		Binary:                 bin,
		GORMCommand:            []string{"sh", "provider.sh"},
		ProviderCommandTimeout: 5 * time.Second,
		PtahCommandTimeout:     5 * time.Second,
	}.Run()

	c.Assert(results, qt.HasLen, 3)
	setup := results[2]
	c.Check(setup.Fixture, qt.Equals, "sqlalchemy")
	c.Check(setup.Stage, qt.Equals, "provider setup")
	c.Check(setup.Outcome, qt.Equals, probe.Fail)
	c.Check(setup.Detail, qt.Contains, "requirements.in asks for atlas-provider-sqlalchemy==0.5.2 and SQLAlchemy==2.0.52")
	c.Check(setup.Detail, qt.Contains, "requirements.txt installs atlas-provider-sqlalchemy==0.5.2 and SQLAlchemy==2.0.54")
}
