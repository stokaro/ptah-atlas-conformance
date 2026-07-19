// Command gap-probe-diff runs the differential-vs-Atlas tier and writes
// gaps-diff.md / gaps-diff.json. It applies each first-party Ptah schema to a
// live Postgres, then compares what a real Atlas CE binary and Ptah each report
// about that schema. It is kept separate from the offline and round-trip tiers
// because it needs both a database (CONFORMANCE_POSTGRES_URL) and an Atlas binary
// (ATLAS_BIN), built from the tag pinned in atlas.version so release parity is
// explicit and auditable.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/stokaro/ptah/dbschema"
	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

const atlasSHA = "a5e0aecc2bb64143bf522734f8ad88e04885fca6"

func main() {
	corpus := flag.String("corpus", "testdata/live", "root of first-party schema fixtures")
	mdOut := flag.String("md", "gaps-diff.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps-diff.json", "json report output path")
	gate := flag.Bool("gate", false, "exit non-zero if any non-OK observation remains")
	flag.Parse()

	url := os.Getenv("CONFORMANCE_POSTGRES_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "the differential tier needs a Postgres target.")
		fmt.Fprintln(os.Stderr, "  export CONFORMANCE_POSTGRES_URL='postgres://postgres:pw@localhost:5432/conf?sslmode=disable'")
		os.Exit(2)
	}
	atlasBin, err := resolveAtlas()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Build it from the pinned tag, e.g.:")
		fmt.Fprintln(os.Stderr, "  git clone --depth 1 --branch $(cat atlas.version) https://github.com/ariga/atlas /tmp/atlas")
		fmt.Fprintln(os.Stderr, "  (cd /tmp/atlas && GOWORK=off go build -o atlas ./cmd/atlas) && export ATLAS_BIN=/tmp/atlas/atlas")
		os.Exit(2)
	}
	fmt.Printf("differential against %s (atlas.version=%s)\n", atlasReported(atlasBin), pinnedVersion())

	dirs, err := fixtureDirs(*corpus)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover fixtures:", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	conn, err := dbschema.ConnectToDatabase(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect postgres:", err)
		os.Exit(2)
	}
	defer conn.Close()

	var results []probe.Result
	for _, d := range dirs {
		results = append(results, probe.RunSchemaDiff(ctx, conn, atlasBin, url, filepath.Base(d), d)...)
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
	fmt.Printf("%d observations, %d non-OK -> %s\n", len(results), len(nonOK), *mdOut)
	if *gate && len(nonOK) > 0 {
		fmt.Fprintf(os.Stderr, "\nDIFFERENTIAL GATE: RED - %d non-OK observation(s):\n", len(nonOK))
		for _, r := range nonOK {
			fmt.Fprintf(os.Stderr, "  [%s] %s / %s: %s\n", r.Outcome, r.Fixture, r.Stage, r.Detail)
		}
		fmt.Fprintln(os.Stderr, "\nExpected until Ptah and Atlas CE agree on every CE-visible construct.")
		os.Exit(1)
	}
	if *gate {
		fmt.Println("DIFFERENTIAL GATE: GREEN - Ptah agrees with Atlas CE on every fixture.")
	}
}

// resolveAtlas finds the Atlas binary: ATLAS_BIN if set, else `atlas` on PATH.
func resolveAtlas() (string, error) {
	if b := os.Getenv("ATLAS_BIN"); b != "" {
		if _, err := os.Stat(b); err != nil {
			return "", fmt.Errorf("ATLAS_BIN=%q is not usable: %w", b, err)
		}
		return b, nil
	}
	if p, err := exec.LookPath("atlas"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("no Atlas binary: set ATLAS_BIN or put `atlas` on PATH")
}

func atlasReported(bin string) string {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "atlas (version unknown)"
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

func pinnedVersion() string {
	b, err := os.ReadFile("atlas.version")
	if err != nil {
		return "unpinned"
	}
	return strings.TrimSpace(string(b))
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
	for _, line := range strings.Split(string(data), "\n") {
		const mod = "github.com/stokaro/ptah "
		if i := strings.Index(line, mod); i >= 0 {
			return "github.com/stokaro/ptah " + strings.Fields(line[i+len(mod):])[0]
		}
	}
	return "github.com/stokaro/ptah (version unknown)"
}
