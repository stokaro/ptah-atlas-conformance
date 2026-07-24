package probe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const liveFixtureManifestName = "fixture.json"

type liveFixtureManifest struct {
	Dialects []string `json:"dialects"`
}

// LiveFixtureDirs returns sorted first-party live fixture directories.
func LiveFixtureDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// LiveFixtureDirsForDialect filters live fixtures by their optional manifest.
// Fixtures without a manifest run everywhere.
func LiveFixtureDirsForDialect(dirs []string, dialect string) ([]string, error) {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		supported, err := liveFixtureSupportsDialect(dir, dialect)
		if err != nil {
			return nil, err
		}
		if supported {
			out = append(out, dir)
		}
	}
	return out, nil
}

func liveFixtureSupportsDialect(dir, dialect string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, liveFixtureManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var manifest liveFixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false, err
	}
	if len(manifest.Dialects) == 0 {
		return true, nil
	}
	dialect = normalizeLiveFixtureDialect(dialect)
	for _, allowed := range manifest.Dialects {
		if normalizeLiveFixtureDialect(allowed) == dialect {
			return true, nil
		}
	}
	return false, nil
}

func normalizeLiveFixtureDialect(dialect string) string {
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "postgresql":
		return "postgres"
	default:
		return strings.ToLower(strings.TrimSpace(dialect))
	}
}
