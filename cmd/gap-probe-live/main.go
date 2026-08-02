// Command gap-probe-live runs the behavioral self-consistency probe against a
// live database and writes gaps-live.md / gaps-live.json. It is the live tier of
// the conformance suite, kept separate from the offline probes so the offline
// report stays deterministic and DB-free. SQLite runs against a fresh local
// database when CONFORMANCE_SQLITE_URL is not set; networked dialects run when
// their CONFORMANCE_*_URL variables are configured.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"

	"go.5x5.cz/ptah/dbschema"
)

func main() {
	corpus := flag.String("corpus", "testdata/live", "root of first-party round-trip schema fixtures")
	mdOut := flag.String("md", "gaps-live.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps-live.json", "json report output path")
	gate := flag.Bool("gate", false, "exit non-zero if any non-OK observation remains")
	flag.Parse()

	sqliteDir, err := os.MkdirTemp("", "ptah-conformance-live-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create sqlite temp dir:", err)
		os.Exit(2)
	}
	defer os.RemoveAll(sqliteDir) //nolint:errcheck
	configured := configuredLiveTargets(sqliteDir, os.Getenv)

	dirs, err := probe.LiveFixtureDirs(*corpus)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover fixtures:", err)
		os.Exit(2)
	}
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "no round-trip fixtures under", *corpus)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var results []probe.Result
	for _, tgt := range configured {
		conn, err := dbschema.ConnectToDatabase(ctx, tgt.url)
		if err != nil {
			fmt.Fprintln(os.Stderr, "connect", tgt.label, ":", err)
			os.Exit(2)
		}
		targetDirs, err := probe.LiveFixtureDirsForDialect(dirs, tgt.label)
		if err != nil {
			fmt.Fprintln(os.Stderr, "filter fixtures", tgt.label, ":", err)
			os.Exit(2)
		}
		for _, d := range targetDirs {
			name := tgt.label + "/" + filepath.Base(d)
			results = append(results, probe.RunRoundTrip(ctx, conn, name, d)...)
		}
		conn.Close()
	}

	md := probe.RenderLiveMarkdownWithCommand(results, probe.PtahVersion(), "make probe-live")
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
	fmt.Printf("%d observations across %d dialect(s), %d non-OK -> %s\n", len(results), len(configured), len(nonOK), *mdOut)
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

type liveTarget struct {
	label string
	url   string
}

func configuredLiveTargets(sqliteDir string, getenv func(string) string) []liveTarget {
	targets := []struct{ label, env string }{
		{"postgres", "CONFORMANCE_POSTGRES_URL"},
		{"mysql", "CONFORMANCE_MYSQL_URL"},
		{"mariadb", "CONFORMANCE_MARIADB_URL"},
	}
	configured := make([]liveTarget, 0, len(targets)+1)
	for _, t := range targets {
		if u := getenv(t.env); u != "" {
			configured = append(configured, liveTarget{label: t.label, url: u})
		}
	}

	sqliteURL := getenv("CONFORMANCE_SQLITE_URL")
	if sqliteURL == "" {
		sqliteURL = "sqlite://" + filepath.Join(sqliteDir, "conformance.sqlite")
	}
	return append(configured, liveTarget{label: "sqlite", url: sqliteURL})
}
