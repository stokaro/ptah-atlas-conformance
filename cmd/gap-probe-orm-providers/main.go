// Command gap-probe-orm-providers runs pinned ORM provider smoke workflows and
// writes gaps-orm-providers.md / gaps-orm-providers.json. This tier is separate
// from deterministic external-schema probes because it creates language
// environments and executes external provider toolchains.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const reportCommand = "go run ./cmd/gap-probe-orm-providers"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gap-probe-orm-providers", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fixtureRoot := flags.String(
		"fixtures",
		"testdata/workflows/external-schema/orm",
		"root of pinned ORM provider fixtures",
	)
	mdOut := flags.String("md", "gaps-orm-providers.md", "markdown report output path")
	jsonOut := flags.String("json", "gaps-orm-providers.json", "JSON report output path")
	gate := flags.Bool("gate", false, "exit non-zero if any non-OK provider observation remains")
	providerTimeout := flags.Duration(
		"provider-timeout",
		10*time.Minute,
		"timeout for provider setup and direct execution",
	)
	ptahTimeout := flags.Duration(
		"ptah-timeout",
		5*time.Minute,
		"timeout for each Ptah external-schema execution",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}

	results := probe.ORMProviderSmokeProbe{
		FixtureRoot:            *fixtureRoot,
		ProviderCommandTimeout: *providerTimeout,
		PtahCommandTimeout:     *ptahTimeout,
	}.Run()
	markdown := probe.RenderORMProviderMarkdown(results, probe.PtahVersion(), reportCommand)
	if err := os.WriteFile(*mdOut, []byte(markdown), 0o644); err != nil {
		fmt.Fprintln(stderr, "write markdown report:", err)
		return 2
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "encode JSON report:", err)
		return 2
	}
	if err := os.WriteFile(*jsonOut, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(stderr, "write JSON report:", err)
		return 2
	}

	nonOK := probe.NonOK(results)
	fmt.Fprintf(stdout, "%d ORM provider observations, %d non-OK -> %s\n",
		len(results), len(nonOK), *mdOut)
	if *gate && len(nonOK) > 0 {
		fmt.Fprintf(stderr, "\nORM PROVIDER CONFORMANCE GATE: RED - %d non-OK observation(s):\n", len(nonOK))
		for _, result := range nonOK {
			fmt.Fprintf(stderr, "  [%s] %s / %s: %s\n",
				result.Outcome, result.Fixture, result.Stage, result.Detail)
		}
		return 1
	}
	if *gate {
		fmt.Fprintln(stdout, "ORM PROVIDER CONFORMANCE GATE: GREEN - every pinned provider passed.")
	}
	return 0
}
