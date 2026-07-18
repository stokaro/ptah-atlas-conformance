// Command gap-probe-live runs the behavioral self-consistency probe against a
// live database and writes gaps-live.md / gaps-live.json. It is the live tier of
// the conformance suite, kept separate from the offline probes so the offline
// report stays deterministic and DB-free. It requires CONFORMANCE_DB_URL.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/stokaro/ptah/dbschema"
	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const atlasSHA = "a5e0aecc2bb64143bf522734f8ad88e04885fca6"

func main() {
	corpus := flag.String("corpus", "testdata/live", "root of first-party round-trip schema fixtures")
	mdOut := flag.String("md", "gaps-live.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps-live.json", "json report output path")
	dialect := flag.String("dialect", "postgres", "dialect of the target database")
	gate := flag.Bool("gate", false, "exit non-zero if any non-OK observation remains")
	flag.Parse()

	url := os.Getenv("CONFORMANCE_DB_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "CONFORMANCE_DB_URL is not set; the live round-trip tier needs a database.")
		fmt.Fprintln(os.Stderr, "Run a throwaway Postgres and export it, e.g.:")
		fmt.Fprintln(os.Stderr, "  export CONFORMANCE_DB_URL='postgres://postgres:pw@localhost:5432/conf?sslmode=disable'")
		os.Exit(2)
	}

	dirs, err := fixtureDirs(*corpus)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover fixtures:", err)
		os.Exit(2)
	}
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "no round-trip fixtures under", *corpus)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn, err := dbschema.ConnectToDatabase(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(2)
	}
	defer conn.Close()

	var results []probe.Result
	for _, d := range dirs {
		name := filepath.Base(d)
		results = append(results, probe.RunRoundTrip(ctx, conn, name, d, *dialect)...)
	}

	md := probe.RenderMarkdown(results, &probe.Waivers{}, atlasSHA, ptahVersion())
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
	fmt.Printf("%d fixtures, %d observations, %d non-OK -> %s\n", len(dirs), len(results), len(nonOK), *mdOut)
	if *gate && len(nonOK) > 0 {
		fmt.Fprintf(os.Stderr, "\nLIVE CONFORMANCE GATE: RED - %d non-OK observation(s):\n", len(nonOK))
		for _, r := range nonOK {
			fmt.Fprintf(os.Stderr, "  [%s] %s / %s: %s\n", r.Outcome, r.Fixture, r.Stage, r.Detail)
		}
		fmt.Fprintln(os.Stderr, "\nExpected until Ptah's generate -> apply -> introspect loop is lossless.")
		os.Exit(1)
	}
	if *gate {
		fmt.Println("LIVE CONFORMANCE GATE: GREEN - every fixture round-trips cleanly.")
	}
}

func fixtureDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func ptahVersion() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "github.com/stokaro/ptah (version unknown)"
	}
	for _, line := range splitLines(string(data)) {
		const mod = "github.com/stokaro/ptah "
		if i := indexOf(line, mod); i >= 0 {
			return "github.com/stokaro/ptah " + firstField(line[i+len(mod):])
		}
	}
	return "github.com/stokaro/ptah (version unknown)"
}

func splitLines(s string) []string {
	var out []string
	cur := ""
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
