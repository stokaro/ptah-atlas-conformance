// Command gap-probe-ce-gating runs the CE gating tier and writes ce-gating.md
// / ce-gating.json. It executes the pinned Atlas CE binary — logged out, under
// a scratch HOME per scenario — through the fixed capability scenarios Ptah's
// feature matrix asserts about the CE column, and classifies each observed
// outcome (works / community-abort / absent / unregistered-command /
// unknown-flag / named-error / silent-unenforced). Expected classes encode the
// measured 2026-08-01 baseline for Atlas CE v1.2.0, re-confirmed unchanged
// against v1.3.0 on 2026-08-02, so a renovate bump of atlas.version that
// changes gating turns the gate red. SQLite only; no external databases. Needs ATLAS_BIN or
// ./bin/atlas, built from the tag pinned in atlas.version so release parity is
// explicit and auditable.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func main() {
	mdOut := flag.String("md", "ce-gating.md", "markdown report output path")
	jsonOut := flag.String("json", "ce-gating.json", "json report output path")
	gate := flag.Bool("gate", false, "exit non-zero if any scenario diverges from the measured baseline")
	flag.Parse()

	atlasBin := probe.DefaultAtlasBinary()
	// The version line lands in the committed report header, so it is
	// measured under the same scrubbed logged-out environment as every
	// scenario — never with the developer's ambient env.
	versionLine, err := probe.CEGatingAtlasVersion(atlasBin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no usable Atlas binary (%q): %v\n", atlasBin, err)
		fmt.Fprintln(os.Stderr, "Build it from the pinned tag, e.g.:")
		fmt.Fprintln(os.Stderr, "  make atlas && export ATLAS_BIN=$PWD/bin/atlas")
		os.Exit(2)
	}
	fmt.Printf("ce-gating against %s (atlas.version=%s)\n", versionLine, pinnedVersion())

	run := probe.RunCEGating(atlasBin)

	md := probe.RenderCEGatingMarkdownWithCommand(run.Results, versionLine, "make probe-ce-gating")
	if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write md:", err)
		os.Exit(2)
	}
	j, _ := json.MarshalIndent(run.Results, "", "  ")
	if err := os.WriteFile(*jsonOut, append(j, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write json:", err)
		os.Exit(2)
	}

	nonOK := probe.NonOK(run.Results)
	fmt.Printf("%d scenario(s): %s; %d non-OK -> %s\n",
		len(run.Results), formatClassCounts(run.Observed), len(nonOK), *mdOut)
	if *gate && len(nonOK) > 0 {
		fmt.Fprintf(os.Stderr, "\nCE GATING GATE: RED - %d non-OK observation(s):\n", len(nonOK))
		for _, r := range nonOK {
			fmt.Fprintf(os.Stderr, "  [%s] %s / %s: %s\n", r.Outcome, r.Fixture, r.Stage, r.Detail)
		}
		fmt.Fprintln(os.Stderr, "\nThe pinned Atlas CE binary no longer matches the measured gating baseline;")
		fmt.Fprintln(os.Stderr, "re-measure by hand and update the scenario table before trusting the CE column.")
		os.Exit(1)
	}
	if *gate {
		fmt.Println("CE GATING GATE: GREEN - every scenario matches the measured Atlas CE gating baseline.")
	}
}

// formatClassCounts renders per-class scenario counts in the fixed class
// order, e.g. "7 works, 11 community-abort, ...".
func formatClassCounts(observed map[probe.CEGatingClass]int) string {
	var parts []string
	for _, class := range probe.CEGatingClassOrder() {
		if n := observed[class]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, class))
		}
	}
	if len(parts) == 0 {
		return "no classified observations"
	}
	return strings.Join(parts, ", ")
}

func pinnedVersion() string {
	b, err := os.ReadFile("atlas.version")
	if err != nil {
		return "unpinned"
	}
	return strings.TrimSpace(string(b))
}
