package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
)

// The docs surface tier indexes every documentation page on atlasgo.io and
// requires each page to carry an explicit Ptah triage stance. It mirrors the
// CLI surface tier, but the inventory source is the atlasgo.io sitemap instead
// of the Atlas binary's help tree, so parity is measured against the full Atlas
// documentation surface.
//
// Licensing posture: the registry, the snapshot, and the generated report
// record only atlasgo.io URL paths and this repository's own triage labels and
// notes. No atlasgo.io documentation content is stored in this repository.

const docsSurfaceProbeName = "docs-surface"

// DocsSurfaceOrigin is the docs site origin the sitemap describes. Sitemap URLs
// outside this origin are not part of the docs universe.
const DocsSurfaceOrigin = "https://atlasgo.io"

// DefaultDocsSitemapURL is the live sitemap fetched by the weekly refresh.
const DefaultDocsSitemapURL = DocsSurfaceOrigin + "/sitemap.xml"

// maxDocsSitemapBytes bounds the fetched sitemap body. The real sitemap is
// ~72KB; anything near this limit is an infrastructure error, not docs growth.
const maxDocsSitemapBytes = 32 << 20

// DocsSurfaceStatus is Ptah's triage stance for one atlasgo.io docs page.
type DocsSurfaceStatus string

const (
	// DocsSurfaceOpen — Ptah covers what the page documents as an open capability.
	DocsSurfaceOpen DocsSurfaceStatus = "open"
	// DocsSurfacePartial — Ptah covers part of what the page documents.
	DocsSurfacePartial DocsSurfaceStatus = "partial"
	// DocsSurfaceGapStatus — Ptah lacks what the page documents.
	DocsSurfaceGapStatus DocsSurfaceStatus = "gap"
	// DocsSurfacePro — the page documents an Atlas paid (Pro) feature.
	DocsSurfacePro DocsSurfaceStatus = "pro"
	// DocsSurfaceCloud — the page documents the Atlas hosted (Cloud) service.
	DocsSurfaceCloud DocsSurfaceStatus = "cloud"
	// DocsSurfaceOutOfScope — the page is consciously outside Ptah's scope.
	DocsSurfaceOutOfScope DocsSurfaceStatus = "out-of-scope"
	// DocsSurfaceUntriaged — the page has not been triaged yet; this is the only
	// registry status that keeps the docs surface gate red.
	DocsSurfaceUntriaged DocsSurfaceStatus = "untriaged"
)

// DocsSurfaceStatuses returns every valid triage status in canonical order.
func DocsSurfaceStatuses() []DocsSurfaceStatus {
	return []DocsSurfaceStatus{
		DocsSurfaceOpen,
		DocsSurfacePartial,
		DocsSurfaceGapStatus,
		DocsSurfacePro,
		DocsSurfaceCloud,
		DocsSurfaceOutOfScope,
		DocsSurfaceUntriaged,
	}
}

// DocsSurfaceEntry is one triaged page in docs-surface-registry.json.
type DocsSurfaceEntry struct {
	Path   string            `json:"path"`
	Status DocsSurfaceStatus `json:"status"`
	Note   string            `json:"note"`
}

// docsUniverseExcludedPrefixes are path classes outside the docs universe.
// Matching is segment-aware: "/blog" and "/blog/x" are excluded, "/blogging"
// is not.
var docsUniverseExcludedPrefixes = []string{
	"/blog",
	"/changelog",
	"/faq",
	"/tags",
	"/search",
	"/components",
	"/use-cases",
}

// docsUniverseExcludedExact are individual marketing/legal pages outside the
// docs universe. Only the exact path is excluded, not its subtree.
var docsUniverseExcludedExact = map[string]bool{
	"/":                true,
	"/pricing":         true,
	"/about":           true,
	"/trust":           true,
	"/support":         true,
	"/case-studies":    true,
	"/atlas-vs-others": true,
}

// ParseDocsSitemap extracts the URL list from a sitemap.xml document. A
// sitemap that parses but lists no URLs is an error: an empty universe from a
// fetch means broken infrastructure, never "all docs vanished".
func ParseDocsSitemap(data []byte) ([]string, error) {
	var doc struct {
		XMLName xml.Name `xml:"urlset"`
		URLs    []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse sitemap XML: %w", err)
	}
	urls := make([]string, 0, len(doc.URLs))
	for _, u := range doc.URLs {
		if loc := strings.TrimSpace(u.Loc); loc != "" {
			urls = append(urls, loc)
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("sitemap contains no <url><loc> entries")
	}
	return urls, nil
}

// DocsSurfaceUniverse reduces a sitemap URL list to the sorted, deduplicated
// docs universe: the atlasgo.io URL paths that must carry a triage stance.
// Non-atlasgo.io URLs, blog/changelog/marketing classes, and the root are
// excluded.
func DocsSurfaceUniverse(urls []string) []string {
	var out []string
	for _, u := range urls {
		p, ok := strings.CutPrefix(u, DocsSurfaceOrigin)
		if !ok {
			continue
		}
		if p != "/" {
			p = strings.TrimSuffix(p, "/")
		}
		if p == "" {
			p = "/"
		}
		if !strings.HasPrefix(p, "/") {
			continue
		}
		if docsUniverseExcluded(p) {
			continue
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func docsUniverseExcluded(path string) bool {
	if docsUniverseExcludedExact[path] {
		return true
	}
	for _, prefix := range docsUniverseExcludedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// FetchDocsSitemap downloads the sitemap document from url. The caller owns
// the timeout via ctx; any transport or status failure is an infrastructure
// error, not a docs gap.
func FetchDocsSitemap(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build sitemap request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch sitemap: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch sitemap %s: unexpected status %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocsSitemapBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read sitemap body: %w", err)
	}
	if len(body) > maxDocsSitemapBytes {
		return nil, fmt.Errorf("sitemap body exceeds %d bytes", maxDocsSitemapBytes)
	}
	return body, nil
}

// ParseDocsSurfaceRegistry parses and validates docs-surface-registry.json.
// The file must be a JSON array of {path, status, note} objects, strictly
// sorted by path, with no duplicates and no unknown statuses or fields — an
// invalid registry is an infrastructure error (exit 2), never a gap.
func ParseDocsSurfaceRegistry(data []byte) ([]DocsSurfaceEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var entries []DocsSurfaceEntry
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse docs-surface registry: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse docs-surface registry: trailing data after the JSON array")
	}
	valid := DocsSurfaceStatuses()
	for i, e := range entries {
		if !strings.HasPrefix(e.Path, "/") {
			return nil, fmt.Errorf("docs-surface registry entry %d: path %q must start with /", i, e.Path)
		}
		if e.Path != "/" && strings.HasSuffix(e.Path, "/") {
			return nil, fmt.Errorf("docs-surface registry entry %q: path must not end with /", e.Path)
		}
		if !slices.Contains(valid, e.Status) {
			return nil, fmt.Errorf("docs-surface registry entry %q: unknown status %q (valid: %s)",
				e.Path, e.Status, joinDocsSurfaceStatuses(valid))
		}
		if docsUniverseExcluded(e.Path) {
			return nil, fmt.Errorf("docs-surface registry entry %q: path is excluded from the docs universe by rule; it can never match a universe page", e.Path)
		}
		if i == 0 {
			continue
		}
		switch prev := entries[i-1].Path; {
		case prev == e.Path:
			return nil, fmt.Errorf("docs-surface registry entry %q: duplicate path", e.Path)
		case prev > e.Path:
			return nil, fmt.Errorf("docs-surface registry is not sorted by path: %q after %q", e.Path, prev)
		}
	}
	return entries, nil
}

// LoadDocsSurfaceRegistry reads and validates the registry file at path.
func LoadDocsSurfaceRegistry(path string) ([]DocsSurfaceEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read docs-surface registry: %w", err)
	}
	return ParseDocsSurfaceRegistry(data)
}

func joinDocsSurfaceStatuses(statuses []DocsSurfaceStatus) string {
	parts := make([]string, len(statuses))
	for i, s := range statuses {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}

// FormatDocsSurfaceSnapshot renders the committed docs universe snapshot:
// one path per line, sorted, behind a generated-file header. The snapshot is
// the offline, deterministic universe source for PR CI and local runs.
func FormatDocsSurfaceSnapshot(universe []string) []byte {
	var b strings.Builder
	b.WriteString("# Atlas docs universe snapshot: the atlasgo.io sitemap URL paths that form\n")
	b.WriteString("# the docs-surface tier's inventory, one per line, sorted. Regenerated by\n")
	b.WriteString("# `FETCH=1 make probe-docs-surface` (or -sitemap-file); do not edit by hand.\n")
	sorted := slices.Clone(universe)
	slices.Sort(sorted)
	for _, p := range slices.Compact(sorted) {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// ParseDocsSurfaceSnapshot reads a committed docs universe snapshot. Blank
// lines and # comments are ignored; paths must start with / and be strictly
// sorted with no duplicates, so a hand-mangled snapshot fails loudly.
func ParseDocsSurfaceSnapshot(data []byte) ([]string, error) {
	var paths []string
	for lineno, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "/") {
			return nil, fmt.Errorf("docs-surface snapshot line %d: path %q must start with /", lineno+1, line)
		}
		if len(paths) > 0 {
			switch prev := paths[len(paths)-1]; {
			case prev == line:
				return nil, fmt.Errorf("docs-surface snapshot: duplicate path %q", line)
			case prev > line:
				return nil, fmt.Errorf("docs-surface snapshot is not sorted: %q after %q", line, prev)
			}
		}
		paths = append(paths, line)
	}
	if len(paths) == 0 {
		return nil, errors.New("docs-surface snapshot contains no paths")
	}
	return paths, nil
}

// ProbeDocsSurface compares the docs universe against the triage registry and
// returns one Result per page. A page is ok only when it is in both the
// universe and the registry with an explicit non-untriaged stance; untriaged,
// unregistered (new-page), and registry-only (vanished) pages are gaps.
func ProbeDocsSurface(universe []string, registry []DocsSurfaceEntry) []Result {
	inUniverse := make(map[string]bool, len(universe))
	for _, p := range universe {
		inUniverse[p] = true
	}
	byPath := make(map[string]DocsSurfaceEntry, len(registry))
	union := maps.Clone(inUniverse)
	for _, e := range registry {
		byPath[e.Path] = e
		union[e.Path] = true
	}

	results := make([]Result, 0, len(union))
	for _, path := range slices.Sorted(maps.Keys(union)) {
		entry, inRegistry := byPath[path]
		switch {
		case inUniverse[path] && inRegistry && entry.Status != DocsSurfaceUntriaged:
			detail := string(entry.Status)
			if entry.Note != "" {
				detail += " — " + entry.Note
			}
			results = append(results, Result{
				Probe:   docsSurfaceProbeName,
				Fixture: path,
				Stage:   "triage",
				Outcome: OK,
				Detail:  detail,
			})
		case inUniverse[path] && inRegistry:
			results = append(results, Result{
				Probe:   docsSurfaceProbeName,
				Fixture: path,
				Stage:   "untriaged",
				Outcome: Gap,
				Detail:  "docs page awaits triage in docs-surface-registry.json",
			})
		case inUniverse[path]:
			results = append(results, Result{
				Probe:   docsSurfaceProbeName,
				Fixture: path,
				Stage:   "new-page",
				Outcome: Gap,
				Detail:  "sitemap page missing from docs-surface-registry.json — triage it",
			})
		default:
			results = append(results, Result{
				Probe:   docsSurfaceProbeName,
				Fixture: path,
				Stage:   "vanished",
				Outcome: Gap,
				Detail:  "registry path no longer in the sitemap universe — retire or remap the entry",
			})
		}
	}
	return results
}

// RenderDocsSurfaceMarkdown renders the dedicated Atlas docs surface report.
// It is separate from gaps.md because it measures the atlasgo.io documentation
// page inventory, not the vendored fixture corpus. The output depends only on
// the universe and registry, so snapshot-sourced and sitemap-sourced runs of
// the same universe render byte-identical reports.
func RenderDocsSurfaceMarkdown(results []Result, w *Waivers, universe []string, registry []DocsSurfaceEntry, ptahVersion, command string) string {
	s := summarize(results)
	nonOK := NonOK(results)
	unwaived := Unwaived(results, w)

	var b strings.Builder
	b.WriteString("# Ptah vs Atlas — docs surface report\n\n")
	fmt.Fprintf(&b, "This file is generated by `%s`. Do not edit by hand.\n\n", command)
	b.WriteString("It indexes every documentation page on atlasgo.io (from the committed sitemap\n")
	b.WriteString("snapshot `docs-surface-snapshot.txt`) and records Ptah's triage stance for each\n")
	b.WriteString("page from `docs-surface-registry.json`, so parity is measured against the full\n")
	b.WriteString("Atlas documentation surface: an untriaged, unregistered, or vanished page is a\n")
	b.WriteString("gap until the registry catches up.\n\n")

	if len(nonOK) == 0 {
		b.WriteString("## Status: DOCS SURFACE TRIAGED\n\n")
		b.WriteString("Every atlasgo.io docs page carries an explicit Ptah stance.\n\n")
	} else {
		fmt.Fprintf(&b, "## Status: NOT DONE — %d non-OK observation(s)\n\n", len(nonOK))
		b.WriteString("The full docs-surface gate is red until every atlasgo.io docs page is triaged\n")
		b.WriteString("and the registry matches the sitemap universe.\n\n")
	}

	fmt.Fprintf(&b, "- Docs universe: **%d page(s)** from the atlasgo.io sitemap (committed snapshot `docs-surface-snapshot.txt`)\n", len(universe))
	fmt.Fprintf(&b, "- Ptah at `%s`\n", ptahVersion)
	fmt.Fprintf(&b, "- Outcomes: **%d ok**, **%d gap**, **%d fail**, **%d panic**\n", s.OK, s.Gap, s.Fail, s.Panic)
	fmt.Fprintf(&b, "- Full gate: **%d non-OK** (%s)\n", len(nonOK), conformanceGateStatus(len(nonOK)))
	fmt.Fprintf(&b, "- Regression budget input: **%d unwaived non-OK**, %d waived\n",
		len(unwaived),
		s.Gap+s.Fail+s.Panic-len(unwaived),
	)

	writeDocsSurfaceSummary(&b, universe, registry)
	writeDocsSurfaceFindings(&b, results, w)
	return b.String()
}

func writeDocsSurfaceSummary(b *strings.Builder, universe []string, registry []DocsSurfaceEntry) {
	b.WriteString("\n## Docs Universe Summary\n\n")
	fmt.Fprintf(b, "Universe: **%d page(s)** after the docs-universe filter.\n", len(universe))

	statusCounts := map[DocsSurfaceStatus]int{}
	for _, e := range registry {
		statusCounts[e.Status]++
	}
	b.WriteString("\n### Pages per triage status\n\n")
	b.WriteString("| Status | Pages |\n")
	b.WriteString("| --- | --- |\n")
	for _, status := range DocsSurfaceStatuses() {
		fmt.Fprintf(b, "| %s | %d |\n", status, statusCounts[status])
	}

	segmentCounts := map[string]int{}
	for _, p := range universe {
		segmentCounts[docsSurfaceFirstSegment(p)]++
	}
	b.WriteString("\n### Pages per docs section\n\n")
	b.WriteString("| Section | Pages |\n")
	b.WriteString("| --- | --- |\n")
	for _, segment := range slices.Sorted(maps.Keys(segmentCounts)) {
		fmt.Fprintf(b, "| `%s` | %d |\n", segment, segmentCounts[segment])
	}

	b.WriteString("\n### Licensing\n\n")
	b.WriteString("The registry, the snapshot, and this report record only atlasgo.io URL paths\n")
	b.WriteString("and this repository's own triage labels and notes. No atlasgo.io documentation\n")
	b.WriteString("content is stored in this repository.\n")
}

// docsSurfaceFirstSegment returns the first path segment of a docs page path,
// e.g. "/versioned/apply" -> "versioned". The root path reports "(root)".
func docsSurfaceFirstSegment(path string) string {
	segment, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if segment == "" {
		return "(root)"
	}
	return segment
}

func writeDocsSurfaceFindings(b *strings.Builder, results []Result, w *Waivers) {
	order := map[Outcome]int{Panic: 0, Fail: 1, Gap: 2, OK: 3}
	sorted := slices.Clone(results)
	slices.SortStableFunc(sorted, func(a, z Result) int {
		if c := order[a.Outcome] - order[z.Outcome]; c != 0 {
			return c
		}
		if c := strings.Compare(a.Probe, z.Probe); c != 0 {
			return c
		}
		if c := strings.Compare(a.Fixture, z.Fixture); c != 0 {
			return c
		}
		return strings.Compare(a.Stage, z.Stage)
	})

	b.WriteString("\n## Findings\n\n")
	b.WriteString("| Gate | Outcome | Probe | Page | Stage | Detail | Related |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range sorted {
		issue := ""
		if r.Issue != "" {
			issue = "#" + strings.TrimPrefix(r.Issue, "stokaro/ptah#")
		}
		gate := "—"
		if r.Outcome != OK {
			gate = "**RED**"
			if _, ok := w.Reason(r); ok {
				gate = "waived"
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | `%s` | %s | %s | %s |\n",
			gate, badge(r.Outcome), r.Probe, r.Fixture, r.Stage, escapePipe(r.Detail), issue)
	}
}
