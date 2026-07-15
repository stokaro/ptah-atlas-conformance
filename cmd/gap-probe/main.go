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

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

// atlasSHA is the ariga/atlas commit the vendored fixtures were taken from.
// Keep this in sync with third_party/atlas/PROVENANCE.md.
const atlasSHA = "a5e0aecc2bb64143bf522734f8ad88e04885fca6"

func main() {
	corpus := flag.String("corpus", "third_party/atlas", "root of the vendored Atlas fixtures")
	mdOut := flag.String("md", "gaps.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps.json", "json report output path")
	flag.Parse()

	fixtures, err := probe.LoadCorpus(*corpus)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load corpus:", err)
		os.Exit(1)
	}
	if len(fixtures) == 0 {
		fmt.Fprintln(os.Stderr, "no fixtures found under", *corpus)
		os.Exit(1)
	}

	var results []probe.Result
	for _, p := range probe.AllProbes() {
		for _, fx := range fixtures {
			results = append(results, p.Run(fx)...)
		}
	}

	md := probe.RenderMarkdown(results, atlasSHA, ptahVersion())
	if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write md:", err)
		os.Exit(1)
	}
	j, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(*jsonOut, append(j, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write json:", err)
		os.Exit(1)
	}

	var gaps, fails, panics int
	for _, r := range results {
		switch r.Outcome {
		case probe.Gap:
			gaps++
		case probe.Fail:
			fails++
		case probe.Panic:
			panics++
		}
	}
	fmt.Printf("%d fixtures, %d observations: %d gap, %d fail, %d panic → %s\n",
		len(fixtures), len(results), gaps, fails, panics, *mdOut)
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
