package probe

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestLiveFixtureDirsForDialect_FixturesWithoutManifestRunEverywhere(t *testing.T) {
	c := qt.New(t)
	root := t.TempDir()
	common := filepath.Join(root, "01-common")
	c.Assert(os.Mkdir(common, 0o755), qt.IsNil)

	dirs, err := LiveFixtureDirs(root)
	c.Assert(err, qt.IsNil)

	got, err := LiveFixtureDirsForDialect(dirs, "sqlite")
	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.DeepEquals, []string{common})
}

func TestLiveFixtureDirsForDialect_FiltersDialectSpecificFixtures(t *testing.T) {
	c := qt.New(t)
	root := t.TempDir()
	common := filepath.Join(root, "01-common")
	postgresOnly := filepath.Join(root, "02-postgres-only")
	c.Assert(os.Mkdir(common, 0o755), qt.IsNil)
	c.Assert(os.Mkdir(postgresOnly, 0o755), qt.IsNil)
	c.Assert(os.WriteFile(
		filepath.Join(postgresOnly, liveFixtureManifestName),
		[]byte(`{"dialects":["postgresql"]}`),
		0o644,
	), qt.IsNil)

	dirs, err := LiveFixtureDirs(root)
	c.Assert(err, qt.IsNil)

	postgresDirs, err := LiveFixtureDirsForDialect(dirs, "postgres")
	c.Assert(err, qt.IsNil)
	c.Assert(postgresDirs, qt.DeepEquals, []string{common, postgresOnly})

	sqliteDirs, err := LiveFixtureDirsForDialect(dirs, "sqlite")
	c.Assert(err, qt.IsNil)
	c.Assert(sqliteDirs, qt.DeepEquals, []string{common})
}

func TestLiveFixtureDirsForDialect_InvalidManifestFails(t *testing.T) {
	c := qt.New(t)
	root := t.TempDir()
	broken := filepath.Join(root, "01-broken")
	c.Assert(os.Mkdir(broken, 0o755), qt.IsNil)
	c.Assert(os.WriteFile(
		filepath.Join(broken, liveFixtureManifestName),
		[]byte(`{"dialects":`),
		0o644,
	), qt.IsNil)

	dirs, err := LiveFixtureDirs(root)
	c.Assert(err, qt.IsNil)

	got, err := LiveFixtureDirsForDialect(dirs, "postgres")
	c.Assert(err, qt.ErrorMatches, "unexpected end of JSON input")
	c.Assert(got, qt.IsNil)
}
