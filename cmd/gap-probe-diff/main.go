// Command gap-probe-diff runs the differential-vs-Atlas tier and writes
// gaps-diff.md / gaps-diff.json. It applies each first-party Ptah schema to a
// live databases, then compares what a real Atlas CE binary and Ptah each
// report about that schema. It is kept separate from the offline and round-trip
// tiers because it needs configured databases and an Atlas binary (ATLAS_BIN),
// built from the tag pinned in atlas.version so release parity is explicit and
// auditable. SQLite runs against a fresh local database when
// CONFORMANCE_SQLITE_URL is not set; networked dialects run when their
// CONFORMANCE_*_URL variables are configured.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
	"go.5x5.cz/ptah/dbschema"
)

const atlasSHA = "a5e0aecc2bb64143bf522734f8ad88e04885fca6"

func main() {
	corpus := flag.String("corpus", "testdata/live", "root of first-party schema fixtures")
	mdOut := flag.String("md", "gaps-diff.md", "markdown report output path")
	jsonOut := flag.String("json", "gaps-diff.json", "json report output path")
	gate := flag.Bool("gate", false, "exit non-zero if any non-OK observation remains")
	flag.Parse()

	atlasBin, err := resolveAtlas()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Build it from the pinned tag, e.g.:")
		fmt.Fprintln(os.Stderr, "  git clone --depth 1 --branch $(cat atlas.version) https://github.com/ariga/atlas /tmp/atlas")
		fmt.Fprintln(os.Stderr, "  (cd /tmp/atlas && GOWORK=off go build -o atlas ./cmd/atlas) && export ATLAS_BIN=/tmp/atlas/atlas")
		os.Exit(2)
	}
	fmt.Printf("differential against %s (atlas.version=%s)\n", atlasReported(atlasBin), pinnedVersion())

	sqliteDir, err := os.MkdirTemp("", "ptah-conformance-diff-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create sqlite temp dir:", err)
		os.Exit(2)
	}
	defer os.RemoveAll(sqliteDir) //nolint:errcheck
	configured := configuredDifferentialTargets(sqliteDir, os.Getenv)

	dirs, err := probe.LiveFixtureDirs(*corpus)
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover fixtures:", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var results []probe.Result
	for _, tgt := range configured {
		conn, err := dbschema.ConnectToDatabase(ctx, tgt.ptahURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "connect", tgt.label+":", err)
			os.Exit(2)
		}
		targetDirs, err := probe.LiveFixtureDirsForDialect(dirs, tgt.label)
		if err != nil {
			fmt.Fprintln(os.Stderr, "filter fixtures", tgt.label+":", err)
			os.Exit(2)
		}
		for _, d := range targetDirs {
			name := tgt.label + "/" + filepath.Base(d)
			results = append(results, probe.RunSchemaDiff(ctx, conn, atlasBin, tgt.atlasURL, name, d)...)
		}
		if err := conn.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close", tgt.label+":", err)
			os.Exit(2)
		}
	}

	md := probe.RenderDifferentialMarkdownWithCommand(results, atlasSHA, probe.PtahVersion(), "make probe-diff")
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

type differentialTarget struct {
	label    string
	ptahURL  string
	atlasURL string
}

func configuredDifferentialTargets(sqliteDir string, getenv func(string) string) []differentialTarget {
	var configured []differentialTarget
	if postgresURL := getenv("CONFORMANCE_POSTGRES_URL"); postgresURL != "" {
		configured = append(configured, differentialTarget{
			label:    "postgres",
			ptahURL:  postgresURL,
			atlasURL: atlasURLOrDefault(getenv, "CONFORMANCE_POSTGRES_ATLAS_URL", postgresURL),
		})
	}
	if mysqlURL := getenv("CONFORMANCE_MYSQL_URL"); mysqlURL != "" {
		configured = append(configured, differentialTarget{
			label:    "mysql",
			ptahURL:  mysqlURL,
			atlasURL: atlasURLOrDefault(getenv, "CONFORMANCE_MYSQL_ATLAS_URL", atlasMySQLURL(mysqlURL)),
		})
	}

	sqliteURL := getenv("CONFORMANCE_SQLITE_URL")
	if sqliteURL == "" {
		sqliteURL = "sqlite://" + filepath.Join(sqliteDir, "conformance.sqlite")
	}
	configured = append(configured, differentialTarget{
		label:    "sqlite",
		ptahURL:  sqliteURL,
		atlasURL: atlasURLOrDefault(getenv, "CONFORMANCE_SQLITE_ATLAS_URL", sqliteURL),
	})
	return configured
}

func atlasURLOrDefault(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}

func atlasMySQLURL(raw string) string {
	const prefix = "mysql://"
	rest, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return raw
	}
	userInfo, afterTCP, ok := strings.Cut(rest, "@tcp(")
	if !ok {
		return raw
	}
	host, suffix, ok := strings.Cut(afterTCP, ")")
	if !ok {
		return raw
	}
	return prefix + userInfo + "@" + host + suffix
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
