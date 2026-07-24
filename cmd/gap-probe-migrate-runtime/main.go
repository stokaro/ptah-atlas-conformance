// Command gap-probe-migrate-runtime runs live Atlas migrate runtime checks and
// writes gaps-migrate-runtime.md and gaps-migrate-runtime.json. It exits
// non-zero only on an internal error unless -gate is set.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func main() {
	mdOut := flag.String("md", "gaps-migrate-runtime.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps-migrate-runtime.json", "json report output path")
	gate := flag.Bool("gate", false, "exit non-zero if any gap/fail/panic remains")
	flag.Parse()

	results := probe.RunMigrateRuntime()
	md := probe.RenderMigrateRuntimeMarkdownWithCommand(results, probe.PtahVersion(), "make probe-migrate-runtime")
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
	fmt.Printf("%d observations, %d non-OK observation(s) -> %s\n", len(results), len(nonOK), *mdOut)
	if *gate {
		if len(nonOK) > 0 {
			fmt.Fprintf(os.Stderr, "\nMIGRATE RUNTIME GATE: RED - %d non-OK observation(s):\n", len(nonOK))
			for _, r := range nonOK {
				fmt.Fprintf(os.Stderr, "  [%s] %s / %s / %s: %s\n", r.Outcome, r.Probe, r.Fixture, r.Stage, r.Detail)
			}
			os.Exit(1)
		}
		fmt.Println("MIGRATE RUNTIME GATE: GREEN - every runtime check passed.")
	}
}
