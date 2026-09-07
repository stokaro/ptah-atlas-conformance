// Command docs-surface-probe indexes the atlasgo.io documentation surface and
// compares it against the committed Ptah triage registry. By default the
// universe comes from the committed docs-surface-snapshot.txt so PR CI and
// local runs are offline and deterministic; -sitemap-file parses a local
// sitemap XML and -fetch downloads the live one, both rewriting the snapshot
// so docs drift shows up as a git diff.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const fetchTimeout = 30 * time.Second

func main() {
	sitemapFile := flag.String("sitemap-file", "", "parse this local sitemap XML instead of the committed snapshot")
	fetch := flag.Bool("fetch", false, "fetch the live sitemap from -sitemap-url (weekly refresh mode)")
	sitemapURL := flag.String("sitemap-url", probe.DefaultDocsSitemapURL, "sitemap URL used by -fetch")
	snapshotFile := flag.String("snapshot", "docs-surface-snapshot.txt", "committed docs universe snapshot (default universe source; rewritten by -sitemap-file/-fetch)")
	registryFile := flag.String("registry", "docs-surface-registry.json", "committed docs triage registry")
	mdOut := flag.String("md", "docs-surface.md", "markdown report output path")
	jsonOut := flag.String("json", "docs-surface.json", "json report output path")
	waiverFile := flag.String("waivers", "waivers.txt", "path to the waivers file")
	gate := flag.Bool("gate", false, "exit non-zero if any docs page is untriaged, unregistered, or vanished")
	flag.Parse()

	if *fetch && *sitemapFile != "" {
		fmt.Fprintln(os.Stderr, "use either -fetch or -sitemap-file, not both")
		os.Exit(2)
	}

	// The flags stay here and the enum goes below: refusing both -fetch and
	// -sitemap-file is flag validation, while the mode the loader acts on is a
	// single value it cannot receive in an impossible combination.
	source := probe.DocsUniverseSnapshot
	switch {
	case *fetch:
		source = probe.DocsUniverseFetch
	case *sitemapFile != "":
		source = probe.DocsUniverseFile
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	universe, err := probe.LoadDocsUniverse(ctx, probe.DocsUniverseOptions{
		Source:       source,
		SnapshotFile: *snapshotFile,
		SitemapFile:  *sitemapFile,
		SitemapURL:   *sitemapURL,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "load docs universe:", err)
		os.Exit(2)
	}

	registry, err := probe.LoadDocsSurfaceRegistry(*registryFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load docs-surface registry:", err)
		os.Exit(2)
	}

	waivers, err := probe.LoadWaivers(*waiverFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load waivers:", err)
		os.Exit(2)
	}

	results := probe.ProbeDocsSurface(universe, registry)

	md := probe.RenderDocsSurfaceMarkdown(results, waivers, universe, registry, probe.PtahVersion(), "make probe-docs-surface")
	if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write md:", err)
		os.Exit(2)
	}
	j, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(*jsonOut, append(j, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write json:", err)
		os.Exit(2)
	}

	nonOK := probe.NonOK(results)
	unwaived := probe.Unwaived(results, waivers)
	fmt.Printf("%d docs page(s), %d observations, %d non-OK observation(s), %d unwaived -> %s\n",
		len(universe), len(results), len(nonOK), len(unwaived), *mdOut)

	stale := waivers.Unused(results)
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "stale waiver (matches no finding, delete it): %s\n", s)
	}

	if *gate {
		if len(nonOK) > 0 {
			fmt.Fprintf(os.Stderr, "\nDOCS SURFACE GATE: RED — %d non-OK observation(s):\n", len(nonOK))
			for _, r := range nonOK {
				fmt.Fprintf(os.Stderr, "  [%s] %s / %s / %s: %s\n", r.Outcome, r.Probe, r.Fixture, r.Stage, r.Detail)
			}
			fmt.Fprintln(os.Stderr, "\nThis is expected until every atlasgo.io docs page is triaged in docs-surface-registry.json.")
			os.Exit(1)
		}
		if len(stale) > 0 {
			os.Exit(1)
		}
		fmt.Println("DOCS SURFACE GATE: GREEN — every atlasgo.io docs page carries an explicit Ptah stance.")
	}
}
