// Command gap-probe runs the Atlas coverage probes over the vendored corpus and
// writes gaps.md and gaps.json. It exits non-zero only on an internal error, not
// on finding gaps — gaps are the expected, useful output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// atlasSHA is the ariga/atlas commit the vendored fixtures were taken from.
// Keep this in sync with third_party/atlas/PROVENANCE.md.
const atlasSHA = "a5e0aecc2bb64143bf522734f8ad88e04885fca6"

func main() {
	corpus := flag.String("corpus", "third_party/atlas/upstream,testdata/atlas", "comma-separated roots of Atlas-compatible fixtures")
	mdOut := flag.String("md", "gaps.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps.json", "json report output path")
	waiverFile := flag.String("waivers", "waivers.txt", "path to the waivers file")
	gate := flag.Bool("gate", false, "exit non-zero if any unwaived gap/fail/panic remains (the conformance gate)")
	flag.Parse()

	var fixtures []probe.Fixture
	for _, root := range strings.Split(*corpus, ",") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		loaded, err := probe.LoadCorpus(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "load corpus:", err)
			os.Exit(2)
		}
		fixtures = append(fixtures, loaded...)
	}
	if len(fixtures) == 0 {
		fmt.Fprintln(os.Stderr, "no fixtures found under", *corpus)
		os.Exit(2)
	}
	waivers, err := probe.LoadWaivers(*waiverFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load waivers:", err)
		os.Exit(2)
	}

	var results []probe.Result
	for _, p := range probe.AllProbes() {
		for _, fx := range fixtures {
			results = append(results, p.Run(fx)...)
		}
	}

	md := probe.RenderMarkdown(results, waivers, atlasSHA, ptahVersion())
	if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write md:", err)
		os.Exit(2)
	}
	j, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(*jsonOut, append(j, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write json:", err)
		os.Exit(2)
	}

	unwaived := probe.Unwaived(results, waivers)
	fmt.Printf("%d fixtures, %d observations, %d unwaived gap(s) → %s\n",
		len(fixtures), len(results), len(unwaived), *mdOut)

	// A stale waiver (matching nothing) means a gap closed; force cleanup.
	stale := waivers.Unused(results)
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "stale waiver (matches no finding, delete it): %s\n", s)
	}

	if *gate {
		if len(unwaived) > 0 {
			fmt.Fprintf(os.Stderr, "\nCONFORMANCE GATE: RED — %d unwaived gap(s):\n", len(unwaived))
			for _, r := range unwaived {
				fmt.Fprintf(os.Stderr, "  [%s] %s / %s / %s: %s\n", r.Outcome, r.Probe, r.Fixture, r.Stage, r.Detail)
			}
			fmt.Fprintln(os.Stderr, "\nThis is expected until Ptah reaches Atlas coverage on the corpus.")
			os.Exit(1)
		}
		if len(stale) > 0 {
			os.Exit(1) // stale waivers are also a gate failure
		}
		fmt.Println("CONFORMANCE GATE: GREEN — every fixture covered or waived.")
	}
}

// ptahVersion reads the resolved github.com/stokaro/ptah version from the build
// info so the report pins exactly what it ran against.
func ptahVersion() string {
	if v := readModVersion(); v != "" {
		return "github.com/stokaro/ptah " + v
	}
	return "github.com/stokaro/ptah (version unknown)"
}

func readModVersion() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		if wd, e := os.Getwd(); e == nil {
			data, _ = os.ReadFile(filepath.Join(wd, "go.mod"))
		}
	}
	for _, line := range splitLines(string(data)) {
		const mod = "github.com/stokaro/ptah "
		if i := indexOf(line, mod); i >= 0 {
			return firstField(line[i+len(mod):])
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out, cur = []string{}, ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
