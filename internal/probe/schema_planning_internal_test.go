package probe

// White-box testing required: the target/dev URL topology is an internal live
// harness invariant and is not exposed by the public conformance Probe API.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestPlanningPostgresURLs_HappyPath(t *testing.T) {
	c := qt.New(t)

	target, dev, err := planningPostgresURLs(
		"postgres://user:pass@localhost:5432/conf?sslmode=disable",
		"ptah_dev_123",
	)

	c.Assert(err, qt.IsNil)
	c.Check(target, qt.Equals, "postgres://user:pass@localhost:5432/conf?search_path=public&sslmode=disable")
	c.Check(dev, qt.Equals, "postgres://user:pass@localhost:5432/ptah_dev_123?search_path=public&sslmode=disable")
}

func TestPlanningPostgresURLs_ReplacesExistingSearchPath(t *testing.T) {
	c := qt.New(t)

	target, dev, err := planningPostgresURLs("postgres://localhost/conf?search_path=legacy", "ptah_dev_456")

	c.Assert(err, qt.IsNil)
	c.Check(target, qt.Equals, "postgres://localhost/conf?search_path=public")
	c.Check(dev, qt.Equals, "postgres://localhost/ptah_dev_456?search_path=public")
}

func TestPlanningPostgresURLs_FailurePathDoesNotExposeCredentials(t *testing.T) {
	c := qt.New(t)

	target, dev, err := planningPostgresURLs(
		"postgres://user:do-not-leak%zz@localhost/conf",
		"ptah_dev_789",
	)

	c.Check(target, qt.Equals, "")
	c.Check(dev, qt.Equals, "")
	c.Check(err, qt.ErrorMatches, `parse PostgreSQL URL: invalid URL escape "%zz"`)
}
