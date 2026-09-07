package probe

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

// DocsUniverseSource names where the docs universe comes from.
//
// It is an enum rather than the three-parameter mix of a bool and two paths it
// replaces, because that shape made the impossible combinations representable:
// fetch with a sitemap file, or neither with both. The one property this
// package has to keep -- a snapshot run touches no network and writes no file --
// cannot be stated about a function whose mode is implied by which of four
// arguments happen to be non-zero.
type DocsUniverseSource int

const (
	// DocsUniverseSnapshot reads the committed snapshot. It is the only source
	// a pull request uses, and it must be offline and side-effect free.
	DocsUniverseSnapshot DocsUniverseSource = iota
	// DocsUniverseFile parses a sitemap already on disk and rewrites the
	// snapshot from it.
	DocsUniverseFile
	// DocsUniverseFetch reads the live sitemap and rewrites the snapshot from
	// it. Only the scheduled drift job selects this.
	DocsUniverseFetch
)

// DocsUniverseOptions is the input to [LoadDocsUniverse].
type DocsUniverseOptions struct {
	// Source selects where the universe is read from.
	Source DocsUniverseSource
	// SnapshotFile is read under DocsUniverseSnapshot and written under the
	// other two.
	SnapshotFile string
	// SitemapFile is read under DocsUniverseFile.
	SitemapFile string
	// SitemapURL is fetched under DocsUniverseFetch.
	SitemapURL string
	// HTTP fetches the sitemap. A nil client selects [http.DefaultClient],
	// which is what the command uses; a test supplies its own so a run that
	// must not reach the network can prove it did not.
	HTTP *http.Client
}

// LoadDocsUniverse returns the documentation universe the docs-surface tier
// measures against.
//
// Under [DocsUniverseSnapshot] it reads the committed snapshot and returns:
// no request is built and the snapshot is not rewritten. That is the property
// every pull-request run depends on -- the tier is offline and deterministic
// there, and drift is reported only by the scheduled job that asks for it.
//
// The other two sources parse a sitemap and rewrite the snapshot, and refuse an
// empty universe rather than committing one: a sitemap that stopped parsing
// would otherwise retire every page at once and read as a very large,
// very clean diff.
func LoadDocsUniverse(ctx context.Context, opts DocsUniverseOptions) ([]string, error) {
	var sitemap []byte
	switch opts.Source {
	case DocsUniverseSnapshot:
		data, err := os.ReadFile(opts.SnapshotFile)
		if err != nil {
			return nil, fmt.Errorf("read snapshot (use -fetch or -sitemap-file to build one): %w", err)
		}
		return ParseDocsSurfaceSnapshot(data)
	case DocsUniverseFetch:
		body, err := fetchDocsSitemapWith(ctx, opts.HTTP, opts.SitemapURL)
		if err != nil {
			return nil, err
		}
		sitemap = body
	case DocsUniverseFile:
		body, err := os.ReadFile(opts.SitemapFile)
		if err != nil {
			return nil, fmt.Errorf("read sitemap file: %w", err)
		}
		sitemap = body
	default:
		return nil, fmt.Errorf("unknown docs universe source %d", opts.Source)
	}

	urls, err := ParseDocsSitemap(sitemap)
	if err != nil {
		return nil, err
	}
	universe := DocsSurfaceUniverse(urls)
	if len(universe) == 0 {
		return nil, fmt.Errorf("sitemap produced an empty docs universe")
	}
	if err := os.WriteFile(opts.SnapshotFile, FormatDocsSurfaceSnapshot(universe), 0o600); err != nil {
		return nil, fmt.Errorf("write snapshot: %w", err)
	}
	return universe, nil
}
