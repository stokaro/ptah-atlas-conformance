// Command gap-probe-third-party runs the third-party repository tier and
// writes third-party.md / third-party.json. It replaces the Atlas binary with
// ptah-compat inside real repositories that use Atlas, pinned by commit in
// third-party-repos.json, and compares every command against the pinned Atlas
// CE oracle.
//
// Anything that prevents the comparison -- no oracle, no compatibility binary,
// an upstream tree that would not fetch -- exits 2 and writes no report. A red
// row has to mean Ptah; a row that could also mean "GitHub was down" would
// spend that meaning, and this tier is the one place in the repository where
// the corpus lives on somebody else's server.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func main() {
	mdOut := flag.String("md", "third-party.md", "markdown report output path")
	jsonOut := flag.String("json", "third-party.json", "json report output path")
	gate := flag.Bool("gate", false, "exit non-zero if any non-OK observation remains")
	flag.Parse()

	run := probe.RunThirdParty()
	if run.Infrastructure != nil {
		fmt.Fprintln(os.Stderr, "cannot measure the third-party tier:", run.Infrastructure)
		fmt.Fprintln(os.Stderr, "The Atlas oracle is built from the pinned tag, e.g.:")
		fmt.Fprintln(os.Stderr, "  make atlas && export ATLAS_BIN=$PWD/bin/atlas")
		os.Exit(2)
	}
	fmt.Printf("third-party against %s\n", run.AtlasVersion)

	md := probe.RenderThirdPartyMarkdownWithCommand(
		run.Results, run.AtlasVersion, probe.PtahVersion(), "make probe-third-party")
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
	fmt.Printf("%d observation(s), %d non-OK -> %s\n", len(run.Results), len(nonOK), *mdOut)
	if *gate {
		if len(nonOK) > 0 {
			fmt.Fprintf(os.Stderr, "\nTHIRD PARTY GATE: RED - %d non-OK observation(s):\n", len(nonOK))
			for _, r := range nonOK {
				fmt.Fprintf(os.Stderr, "  [%s] %s / %s: %s\n", r.Outcome, r.Fixture, r.Stage, r.Detail)
			}
			os.Exit(1)
		}
		fmt.Println("THIRD PARTY GATE: GREEN - every pinned repository works with ptah-compat in place of Atlas.")
	}
}
