// Command cli-surface-probe compares the pinned Atlas CE help surface against
// Ptah's Atlas compatibility command surfaces.
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

func main() {
	atlasBin := flag.String("atlas-bin", probe.DefaultAtlasBinary(), "Atlas CE binary to inspect")
	mdOut := flag.String("md", "cli-surface.md", "markdown report output path")
	jsonOut := flag.String("json", "cli-surface.json", "json report output path")
	waiverFile := flag.String("waivers", "waivers.txt", "path to the waivers file")
	gate := flag.Bool("gate", false, "exit non-zero if any CLI surface gap/fail/panic remains")
	flag.Parse()

	waivers, err := probe.LoadWaivers(*waiverFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load waivers:", err)
		os.Exit(2)
	}

	results, inventory, err := probe.ProbeCLISurface(*atlasBin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe CLI surface:", err)
		os.Exit(2)
	}

	md := probe.RenderCLISurfaceMarkdown(results, waivers, inventory, ptahVersion(), "make probe-cli-surface")
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
	fmt.Printf("%d commands, %d observations, %d non-OK observation(s), %d unwaived -> %s\n",
		len(inventory.Commands), len(results), len(nonOK), len(unwaived), *mdOut)

	stale := waivers.Unused(results)
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "stale waiver (matches no finding, delete it): %s\n", s)
	}

	if *gate {
		if len(nonOK) > 0 {
			fmt.Fprintf(os.Stderr, "\nCLI SURFACE GATE: RED — %d non-OK observation(s):\n", len(nonOK))
			for _, r := range nonOK {
				fmt.Fprintf(os.Stderr, "  [%s] %s / %s / %s: %s\n", r.Outcome, r.Probe, r.Fixture, r.Stage, r.Detail)
			}
			fmt.Fprintln(os.Stderr, "\nThis is expected until Ptah matches the pinned Atlas CE CLI surface.")
			os.Exit(1)
		}
		if len(stale) > 0 {
			os.Exit(1)
		}
		fmt.Println("CLI SURFACE GATE: GREEN — every OSS Atlas command surface matches.")
	}
}

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
	for _, line := range strings.Split(string(data), "\n") {
		const mod = "github.com/stokaro/ptah "
		if _, rest, ok := strings.Cut(line, mod); ok {
			fields := strings.Fields(rest)
			if len(fields) != 0 {
				return fields[0]
			}
		}
	}
	return ""
}
