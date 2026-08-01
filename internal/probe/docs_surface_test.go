package probe_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestParseDocsSitemap_SyntheticFixture(t *testing.T) {
	c := qt.New(t)

	data, err := os.ReadFile(filepath.Join("testdata", "docs-surface", "sitemap.xml"))
	c.Assert(err, qt.IsNil)

	urls, err := probe.ParseDocsSitemap(data)
	c.Assert(err, qt.IsNil)
	c.Assert(urls, qt.DeepEquals, []string{
		"https://atlasgo.io/",
		"https://atlasgo.io/versioned/apply",
		"https://atlasgo.io/declarative/apply/",
		"https://atlasgo.io/cloud/agents",
		"https://atlasgo.io/blog/2026/01/01/announcing-something",
		"https://atlasgo.io/pricing",
		"https://atlasgo.io/versioned/apply",
	})
}

func TestParseDocsSitemap_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "malformed XML", input: `<urlset><url><loc>https://atlasgo.io/x</loc>`},
		{name: "wrong root element", input: `<sitemapindex><sitemap><loc>https://atlasgo.io/x</loc></sitemap></sitemapindex>`},
		{name: "empty urlset", input: `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"></urlset>`},
		{name: "urls without locs", input: `<urlset><url></url><url><loc>  </loc></url></urlset>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := qt.New(t)
			_, err := probe.ParseDocsSitemap([]byte(tc.input))
			c.Assert(err, qt.IsNotNil)
		})
	}
}

func TestDocsSurfaceUniverse(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want []string // nil means excluded
	}{
		{name: "plain docs page included", url: "https://atlasgo.io/versioned/apply", want: []string{"/versioned/apply"}},
		{name: "trailing slash stripped", url: "https://atlasgo.io/declarative/apply/", want: []string{"/declarative/apply"}},
		{name: "root URL excluded", url: "https://atlasgo.io/", want: nil},
		{name: "origin without slash excluded as root", url: "https://atlasgo.io", want: nil},
		{name: "foreign host excluded", url: "https://example.com/versioned/apply", want: nil},
		{name: "origin-prefixed foreign host excluded", url: "https://atlasgo.io.evil.example/versioned/apply", want: nil},

		{name: "/blog subtree excluded", url: "https://atlasgo.io/blog/2026/01/01/post", want: nil},
		{name: "/blog itself excluded", url: "https://atlasgo.io/blog", want: nil},
		{name: "/changelog excluded", url: "https://atlasgo.io/changelog", want: nil},
		{name: "/faq excluded", url: "https://atlasgo.io/faq", want: nil},
		{name: "/tags subtree excluded", url: "https://atlasgo.io/tags/migrations", want: nil},
		{name: "/search excluded", url: "https://atlasgo.io/search", want: nil},
		{name: "/components subtree excluded", url: "https://atlasgo.io/components/x", want: nil},
		{name: "/use-cases excluded", url: "https://atlasgo.io/use-cases", want: nil},
		{name: "prefix match is segment-aware", url: "https://atlasgo.io/blogging-guide", want: []string{"/blogging-guide"}},
		{name: "faq lookalike segment included", url: "https://atlasgo.io/faqs", want: []string{"/faqs"}},

		{name: "/pricing excluded exactly", url: "https://atlasgo.io/pricing", want: nil},
		{name: "/about excluded exactly", url: "https://atlasgo.io/about", want: nil},
		{name: "/trust excluded exactly", url: "https://atlasgo.io/trust", want: nil},
		{name: "/support excluded exactly", url: "https://atlasgo.io/support", want: nil},
		{name: "/case-studies excluded exactly", url: "https://atlasgo.io/case-studies", want: nil},
		{name: "/atlas-vs-others excluded exactly", url: "https://atlasgo.io/atlas-vs-others", want: nil},
		{name: "exact exclusion does not cover subtree", url: "https://atlasgo.io/pricing/enterprise", want: []string{"/pricing/enterprise"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := qt.New(t)
			got := probe.DocsSurfaceUniverse([]string{tc.url})
			if tc.want == nil {
				c.Assert(got, qt.HasLen, 0)
				return
			}
			c.Assert(got, qt.DeepEquals, tc.want)
		})
	}
}

func TestDocsSurfaceUniverse_SortsAndDeduplicates(t *testing.T) {
	c := qt.New(t)

	got := probe.DocsSurfaceUniverse([]string{
		"https://atlasgo.io/versioned/apply",
		"https://atlasgo.io/cloud/agents",
		"https://atlasgo.io/versioned/apply/",
		"https://atlasgo.io/versioned/apply",
	})
	c.Assert(got, qt.DeepEquals, []string{"/cloud/agents", "/versioned/apply"})
}

func TestParseDocsSurfaceRegistry(t *testing.T) {
	c := qt.New(t)

	entries, err := probe.ParseDocsSurfaceRegistry([]byte(`[
  {"path": "/cloud/agents", "status": "cloud", "note": ""},
  {"path": "/versioned/apply", "status": "open", "note": "ptah migrations up"}
]`))
	c.Assert(err, qt.IsNil)
	c.Assert(entries, qt.DeepEquals, []probe.DocsSurfaceEntry{
		{Path: "/cloud/agents", Status: probe.DocsSurfaceCloud, Note: ""},
		{Path: "/versioned/apply", Status: probe.DocsSurfaceOpen, Note: "ptah migrations up"},
	})
}

func TestParseDocsSurfaceRegistry_AcceptsEveryValidStatus(t *testing.T) {
	c := qt.New(t)

	for i, status := range probe.DocsSurfaceStatuses() {
		entry := fmt.Sprintf(`[{"path": "/p", "status": %q, "note": ""}]`, status)
		_, err := probe.ParseDocsSurfaceRegistry([]byte(entry))
		c.Assert(err, qt.IsNil, qt.Commentf("status %d %q", i, status))
	}
}

func TestParseDocsSurfaceRegistry_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "unknown status", input: `[{"path": "/p", "status": "supported", "note": ""}]`},
		{name: "empty status", input: `[{"path": "/p", "status": "", "note": ""}]`},
		{name: "unknown field", input: `[{"path": "/p", "status": "open", "note": "", "extra": true}]`},
		{name: "not an array", input: `{"path": "/p", "status": "open", "note": ""}`},
		{name: "trailing data", input: `[] []`},
		{name: "path without leading slash", input: `[{"path": "p", "status": "open", "note": ""}]`},
		{name: "path with trailing slash", input: `[{"path": "/p/", "status": "open", "note": ""}]`},
		{name: "duplicate path", input: `[{"path": "/p", "status": "open", "note": ""}, {"path": "/p", "status": "gap", "note": ""}]`},
		{name: "unsorted paths", input: `[{"path": "/z", "status": "open", "note": ""}, {"path": "/a", "status": "open", "note": ""}]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := qt.New(t)
			_, err := probe.ParseDocsSurfaceRegistry([]byte(tc.input))
			c.Assert(err, qt.IsNotNil)
		})
	}
}

func TestDocsSurfaceSnapshotRoundTrip(t *testing.T) {
	c := qt.New(t)

	universe := []string{"/cloud/agents", "/versioned/apply"}
	data := probe.FormatDocsSurfaceSnapshot([]string{"/versioned/apply", "/cloud/agents", "/cloud/agents"})

	parsed, err := probe.ParseDocsSurfaceSnapshot(data)
	c.Assert(err, qt.IsNil)
	c.Assert(parsed, qt.DeepEquals, universe)
}

func TestParseDocsSurfaceSnapshot_IgnoresCommentsAndBlanks(t *testing.T) {
	c := qt.New(t)

	parsed, err := probe.ParseDocsSurfaceSnapshot([]byte("# header\n\n/cloud/agents\n/versioned/apply\n"))
	c.Assert(err, qt.IsNil)
	c.Assert(parsed, qt.DeepEquals, []string{"/cloud/agents", "/versioned/apply"})
}

func TestParseDocsSurfaceSnapshot_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty file", input: ""},
		{name: "comments only", input: "# nothing here\n"},
		{name: "path without leading slash", input: "cloud/agents\n"},
		{name: "duplicate path", input: "/a\n/a\n"},
		{name: "unsorted paths", input: "/z\n/a\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := qt.New(t)
			_, err := probe.ParseDocsSurfaceSnapshot([]byte(tc.input))
			c.Assert(err, qt.IsNotNil)
		})
	}
}

func TestProbeDocsSurface_Outcomes(t *testing.T) {
	c := qt.New(t)

	universe := []string{"/a-new", "/b-open", "/c-noted", "/d-untriaged"}
	registry := []probe.DocsSurfaceEntry{
		{Path: "/b-open", Status: probe.DocsSurfaceOpen},
		{Path: "/c-noted", Status: probe.DocsSurfaceGapStatus, Note: "tracked in stokaro/ptah#999"},
		{Path: "/d-untriaged", Status: probe.DocsSurfaceUntriaged},
		{Path: "/e-vanished", Status: probe.DocsSurfaceCloud},
	}

	results := probe.ProbeDocsSurface(universe, registry)
	c.Assert(results, qt.DeepEquals, []probe.Result{
		{Probe: "docs-surface", Fixture: "/a-new", Stage: "new-page", Outcome: probe.Gap,
			Detail: "sitemap page missing from docs-surface-registry.json — triage it"},
		{Probe: "docs-surface", Fixture: "/b-open", Stage: "triage", Outcome: probe.OK,
			Detail: "open"},
		{Probe: "docs-surface", Fixture: "/c-noted", Stage: "triage", Outcome: probe.OK,
			Detail: "gap — tracked in stokaro/ptah#999"},
		{Probe: "docs-surface", Fixture: "/d-untriaged", Stage: "untriaged", Outcome: probe.Gap,
			Detail: "docs page awaits triage in docs-surface-registry.json"},
		{Probe: "docs-surface", Fixture: "/e-vanished", Stage: "vanished", Outcome: probe.Gap,
			Detail: "registry path no longer in the sitemap universe — retire or remap the entry"},
	})
}

func TestProbeDocsSurface_FullTriageHasNoNonOK(t *testing.T) {
	c := qt.New(t)

	universe := []string{"/a", "/b"}
	registry := []probe.DocsSurfaceEntry{
		{Path: "/a", Status: probe.DocsSurfaceOpen},
		{Path: "/b", Status: probe.DocsSurfacePro},
	}

	results := probe.ProbeDocsSurface(universe, registry)
	c.Assert(results, qt.HasLen, 2)
	c.Assert(probe.NonOK(results), qt.HasLen, 0)
}

func TestRenderDocsSurfaceMarkdown_RedReport(t *testing.T) {
	c := qt.New(t)

	universe := []string{"/cloud/agents", "/versioned/apply"}
	registry := []probe.DocsSurfaceEntry{
		{Path: "/cloud/agents", Status: probe.DocsSurfaceCloud},
		{Path: "/versioned/apply", Status: probe.DocsSurfaceUntriaged},
	}
	results := probe.ProbeDocsSurface(universe, registry)

	report := probe.RenderDocsSurfaceMarkdown(results, &probe.Waivers{}, universe, registry,
		"ptah-version", "make probe-docs-surface")

	c.Assert(report, qt.Contains, "## Status: NOT DONE — 1 non-OK observation(s)")
	c.Assert(report, qt.Contains, "**2 page(s)** from the atlasgo.io sitemap")
	c.Assert(report, qt.Contains, "| cloud | 1 |")
	c.Assert(report, qt.Contains, "| untriaged | 1 |")
	c.Assert(report, qt.Contains, "| open | 0 |")
	c.Assert(report, qt.Contains, "| `cloud` | 1 |")
	c.Assert(report, qt.Contains, "| `versioned` | 1 |")
	c.Assert(report, qt.Contains, "No atlasgo.io documentation\ncontent is stored in this repository.")
	c.Assert(report, qt.Contains, "This file is generated by `make probe-docs-surface`. Do not edit by hand.")
	c.Assert(report, qt.Contains, "| **RED** | **gap** | docs-surface | `/versioned/apply` | untriaged |")
	c.Assert(report, qt.Contains, "| — | ok | docs-surface | `/cloud/agents` | triage | cloud |")
}

func TestRenderDocsSurfaceMarkdown_GreenReport(t *testing.T) {
	c := qt.New(t)

	universe := []string{"/versioned/apply"}
	registry := []probe.DocsSurfaceEntry{
		{Path: "/versioned/apply", Status: probe.DocsSurfaceOpen},
	}
	results := probe.ProbeDocsSurface(universe, registry)

	report := probe.RenderDocsSurfaceMarkdown(results, &probe.Waivers{}, universe, registry,
		"ptah-version", "make probe-docs-surface")

	c.Assert(report, qt.Contains, "## Status: DOCS SURFACE TRIAGED")
	c.Assert(report, qt.Not(qt.Contains), "NOT DONE")
	c.Assert(report, qt.Contains, "- Full gate: **0 non-OK** (passes CI)")
}

func TestFetchDocsSitemap(t *testing.T) {
	c := qt.New(t)

	const body = `<urlset><url><loc>https://atlasgo.io/versioned/apply</loc></url></urlset>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	got, err := probe.FetchDocsSitemap(context.Background(), srv.URL)
	c.Assert(err, qt.IsNil)
	c.Assert(string(got), qt.Equals, body)
}

func TestFetchDocsSitemap_NonOKStatus(t *testing.T) {
	c := qt.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := probe.FetchDocsSitemap(context.Background(), srv.URL)
	c.Assert(err, qt.ErrorMatches, `.*unexpected status.*404.*`)
}
