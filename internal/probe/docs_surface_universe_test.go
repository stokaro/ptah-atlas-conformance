package probe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const testSitemap = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://atlasgo.io/docs</loc></url>
  <url><loc>https://atlasgo.io/cloud/agents</loc></url>
</urlset>`

// refusingServer fails the test if it is ever asked for anything. It is the
// assertion, not a stub: "the snapshot source stays offline" cannot be checked
// by looking at the returned universe.
//
// The caller must point SitemapURL at this server. A loader aimed elsewhere
// fails to connect instead of reaching the handler, and the test would then
// pass whether or not the loader fetched.
func refusingServer(c *qt.C) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		c.Errorf("the snapshot source issued an HTTP request")
	}))
	c.Cleanup(server.Close)
	return server
}

// TestLoadDocsUniverse_SnapshotSourceIsOfflineAndReadOnly pins the property
// every pull-request run of this tier depends on.
//
// A PR run reads the committed snapshot. If that path could reach the network
// it would report drift on a PR that changed nothing, and if it could rewrite
// the snapshot the tier would silently absorb upstream changes instead of
// failing on them -- which is the whole point of the scheduled drift job being
// separate.
//
// Both halves are asserted against effects, not against the return value: a
// client that fails the test when used, and the snapshot's bytes and modification
// time compared before and after.
func TestLoadDocsUniverse_SnapshotSourceIsOfflineAndReadOnly(t *testing.T) {
	c := qt.New(t)
	dir := c.TempDir()
	snapshot := filepath.Join(dir, "snapshot.txt")
	original := probe.FormatDocsSurfaceSnapshot([]string{"/docs", "/cloud/agents"})
	c.Assert(os.WriteFile(snapshot, original, 0o600), qt.IsNil)
	// The modification time, because neither the bytes nor the permission can
	// see this rewrite. The snapshot is already canonical, so rewriting it
	// produces the same file; and a loader that ignores the write error -- the
	// shape a careless rewrite takes -- succeeds against a read-only file too.
	// A write updates mtime whatever it writes and whatever it does with the
	// error.
	before, statErr := os.Stat(snapshot)
	c.Assert(statErr, qt.IsNil)
	// The URL points AT the refusing server. Pointing it anywhere else makes
	// the handler unreachable, so a loader that did fetch would fail to connect
	// and the assertion would pass for the wrong reason.
	refusing := refusingServer(c)

	universe, err := probe.LoadDocsUniverse(context.Background(), probe.DocsUniverseOptions{
		Source:       probe.DocsUniverseSnapshot,
		SnapshotFile: snapshot,
		SitemapURL:   refusing.URL,
		HTTP:         refusing.Client(),
	})

	c.Assert(err, qt.IsNil)
	c.Assert(universe, qt.DeepEquals, []string{"/cloud/agents", "/docs"})
	after, readErr := os.ReadFile(snapshot)
	c.Assert(readErr, qt.IsNil)
	c.Assert(string(after), qt.Equals, string(original))
	afterStat, statErr := os.Stat(snapshot)
	c.Assert(statErr, qt.IsNil)
	c.Assert(afterStat.ModTime(), qt.Equals, before.ModTime())
}

// TestLoadDocsUniverse_SitemapSourcesRewriteTheSnapshot is the control. Without
// it, a loader that never wrote anything would satisfy the offline test above
// and leave the drift job unable to record what it fetched.
func TestLoadDocsUniverse_SitemapSourcesRewriteTheSnapshot(t *testing.T) {
	tests := []struct {
		name  string
		fetch bool
	}{
		{name: "from a sitemap file"},
		{name: "from the live sitemap", fetch: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)
			dir := c.TempDir()
			snapshot := filepath.Join(dir, "snapshot.txt")
			c.Assert(os.WriteFile(snapshot, []byte("/stale\n"), 0o600), qt.IsNil)
			sitemapFile := filepath.Join(dir, "sitemap.xml")
			c.Assert(os.WriteFile(sitemapFile, []byte(testSitemap), 0o600), qt.IsNil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(testSitemap))
			}))
			c.Cleanup(server.Close)
			options := map[bool]probe.DocsUniverseOptions{
				false: {Source: probe.DocsUniverseFile, SnapshotFile: snapshot, SitemapFile: sitemapFile},
				true:  {Source: probe.DocsUniverseFetch, SnapshotFile: snapshot, SitemapURL: server.URL, HTTP: server.Client()},
			}

			universe, err := probe.LoadDocsUniverse(context.Background(), options[test.fetch])

			c.Assert(err, qt.IsNil)
			c.Assert(universe, qt.DeepEquals, []string{"/cloud/agents", "/docs"})
			after, readErr := os.ReadFile(snapshot)
			c.Assert(readErr, qt.IsNil)
			c.Assert(string(after), qt.Equals, string(probe.FormatDocsSurfaceSnapshot(universe)))
		})
	}
}

// TestLoadDocsUniverse_RefusesAnEmptyUniverse pins the fail-closed path. A
// sitemap that stopped parsing would otherwise retire every tracked page at
// once, and the diff would read as a very large, very clean change rather than
// as a broken read.
func TestLoadDocsUniverse_RefusesAnEmptyUniverse(t *testing.T) {
	c := qt.New(t)
	dir := c.TempDir()
	snapshot := filepath.Join(dir, "snapshot.txt")
	original := probe.FormatDocsSurfaceSnapshot([]string{"/docs"})
	c.Assert(os.WriteFile(snapshot, original, 0o600), qt.IsNil)
	sitemapFile := filepath.Join(dir, "sitemap.xml")
	// Parseable, and nothing in it belongs to the docs surface. An empty
	// <urlset> would fail in the parser instead and never reach the check this
	// row is about.
	empty := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` +
		`<url><loc>https://example.com/not-a-docs-page</loc></url>` +
		`</urlset>`
	c.Assert(os.WriteFile(sitemapFile, []byte(empty), 0o600), qt.IsNil)

	universe, err := probe.LoadDocsUniverse(context.Background(), probe.DocsUniverseOptions{
		Source:       probe.DocsUniverseFile,
		SnapshotFile: snapshot,
		SitemapFile:  sitemapFile,
	})

	c.Assert(err, qt.ErrorMatches, "sitemap produced an empty docs universe")
	c.Assert(universe, qt.IsNil)
	// The committed snapshot survives the refusal, so a broken read cannot
	// leave the repository with an emptied universe to commit.
	after, readErr := os.ReadFile(snapshot)
	c.Assert(readErr, qt.IsNil)
	c.Assert(string(after), qt.Equals, string(original))
}
