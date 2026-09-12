package main

// White-box testing required: the probe's entry point and its target
// resolution are unexported -- `main`, `resolveAtlas`,
// `configuredDifferentialTargets` and the `differentialTarget` it builds.
// A command package publishes no import path, so there is no exported
// surface to reach them through.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp"
)

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

// writeStubAtlas writes an executable that prints marker, so a resolved path
// can be identified by running it.
func writeStubAtlas(c *qt.C, path, marker string) {
	c.Helper()
	c.Assert(os.MkdirAll(filepath.Dir(path), 0o750), qt.IsNil)
	c.Assert(os.WriteFile(path, []byte("#!/bin/sh\necho "+marker+"\n"), 0o600), qt.IsNil)
	c.Assert(os.Chmod(path, 0o755), qt.IsNil)
}

// TestResolveAtlas_PrefersTheRepositoryLocalBuild pins the resolution order this
// command lost by keeping one of its own.
//
// The rows are the three answers in order, and the first is the regression: a
// machine with an unpinned Atlas installed measured that binary, because this
// command fell from ATLAS_BIN straight to PATH and never looked at ./bin/atlas
// — the build `make atlas` produces from the pinned tag. The report header
// names whatever answered, so a wrong oracle read as a differential finding
// about Ptah.
//
// Each row is decided by running the resolved path: comparing paths would pass
// on a resolver that returned a plausible string it never checked was runnable.
func TestResolveAtlas_PrefersTheRepositoryLocalBuild(t *testing.T) {
	tests := []struct {
		name     string
		setEnv   bool
		wantEcho string
	}{
		{name: "the repository-local build wins over PATH", wantEcho: "local"},
		{name: "ATLAS_BIN wins over both", setEnv: true, wantEcho: "explicit"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			root := c.TempDir()
			pathDir := filepath.Join(root, "onpath")
			writeStubAtlas(c, filepath.Join(pathDir, "atlas"), "onpath")
			writeStubAtlas(c, filepath.Join(root, "bin", "atlas"), "local")
			explicit := filepath.Join(root, "explicit-atlas")
			writeStubAtlas(c, explicit, "explicit")
			t.Chdir(root)
			t.Setenv("PATH", pathDir)
			t.Setenv("ATLAS_BIN", map[bool]string{true: explicit, false: ""}[test.setEnv])

			resolved, err := resolveAtlas()

			c.Assert(err, qt.IsNil)
			out, runErr := exec.Command(resolved).Output()
			c.Assert(runErr, qt.IsNil)
			c.Assert(strings.TrimSpace(string(out)), qt.Equals, test.wantEcho)
		})
	}
}

// TestResolveAtlas_FallsBackToPATH is the control for the table above. Without
// it, a resolver that always answered ./bin/atlas would pass every row there
// and break every machine that installs Atlas rather than building it.
func TestResolveAtlas_FallsBackToPATH(t *testing.T) {
	c := qt.New(t)
	root := c.TempDir()
	pathDir := filepath.Join(root, "onpath")
	writeStubAtlas(c, filepath.Join(pathDir, "atlas"), "onpath")
	t.Chdir(root)
	t.Setenv("PATH", pathDir)
	t.Setenv("ATLAS_BIN", "")

	resolved, err := resolveAtlas()

	c.Assert(err, qt.IsNil)
	out, runErr := exec.Command(resolved).Output()
	c.Assert(runErr, qt.IsNil)
	c.Assert(strings.TrimSpace(string(out)), qt.Equals, "onpath")
}

// TestResolveAtlas_RefusesWhenNothingIsUsable pins that an unresolvable name is
// an error rather than a path the caller discovers is missing later.
func TestResolveAtlas_RefusesWhenNothingIsUsable(t *testing.T) {
	c := qt.New(t)
	root := c.TempDir()
	t.Chdir(root)
	t.Setenv("PATH", filepath.Join(root, "empty"))
	t.Setenv("ATLAS_BIN", "")

	resolved, err := resolveAtlas()

	c.Assert(err, qt.ErrorMatches, `no usable Atlas binary "atlas": set ATLAS_BIN, run .make atlas., or put .atlas. on PATH: .*`)
	c.Assert(resolved, qt.Equals, "")
}
