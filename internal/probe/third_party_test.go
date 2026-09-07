package probe_test

import (
	"os"
	"path/filepath"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func TestParseThirdPartyRepos_HappyPath(t *testing.T) {
	c := qt.New(t)

	repos, err := probe.ParseThirdPartyRepos([]byte(`[
	  {
	    "name": "owner/repo",
	    "clone_url": "https://github.com/owner/repo",
	    "commit": "e7b8821a04d82318804a83416cc9d07db382ca0b",
	    "license": "Apache-2.0",
	    "config": "atlas.hcl",
	    "migrations": "migrations",
	    "note": "why this repository is here"
	  }
	]`))

	c.Assert(err, qt.IsNil)
	c.Assert(repos, qt.DeepEquals, []probe.ThirdPartyRepo{{
		Name:       "owner/repo",
		CloneURL:   "https://github.com/owner/repo",
		Commit:     "e7b8821a04d82318804a83416cc9d07db382ca0b",
		License:    "Apache-2.0",
		Config:     "atlas.hcl",
		Migrations: "migrations",
		Note:       "why this repository is here",
	}})
}

func TestParseThirdPartyRepos_FailurePath(t *testing.T) {
	tests := []struct {
		name    string
		ledger  string
		wantErr string
	}{
		{
			name:    "not an array",
			ledger:  `{"name": "owner/repo"}`,
			wantErr: `parse third-party repository ledger: json: cannot unmarshal object .*`,
		},
		{
			name:    "unknown field",
			ledger:  `[{"name": "owner/repo", "branch": "main"}]`,
			wantErr: `parse third-party repository ledger: json: unknown field "branch"`,
		},
		{
			name:    "empty ledger",
			ledger:  `[]`,
			wantErr: `parse third-party repository ledger: no repositories listed`,
		},
		{
			name: "missing note",
			ledger: `[{"name": "owner/repo", "clone_url": "https://github.com/owner/repo",
			  "commit": "e7b8821a04d82318804a83416cc9d07db382ca0b", "license": "MIT",
			  "config": "atlas.hcl", "migrations": "migrations"}]`,
			wantErr: `parse third-party repository ledger: entry 0: note is empty`,
		},
		{
			// The pin is the tier's whole determinism story: an abbreviated
			// revision would let the measured tree move without the ledger.
			name: "abbreviated commit",
			ledger: `[{"name": "owner/repo", "clone_url": "https://github.com/owner/repo",
			  "commit": "e7b8821", "license": "MIT", "config": "atlas.hcl",
			  "migrations": "migrations", "note": "n"}]`,
			wantErr: `parse third-party repository ledger: owner/repo: commit "e7b8821" is not a full lowercase SHA`,
		},
		{
			name: "branch name where a commit belongs",
			ledger: `[{"name": "owner/repo", "clone_url": "https://github.com/owner/repo",
			  "commit": "main", "license": "MIT", "config": "atlas.hcl",
			  "migrations": "migrations", "note": "n"}]`,
			wantErr: `parse third-party repository ledger: owner/repo: commit "main" is not a full lowercase SHA`,
		},
		{
			name: "uppercase commit",
			ledger: `[{"name": "owner/repo", "clone_url": "https://github.com/owner/repo",
			  "commit": "E7B8821A04D82318804A83416CC9D07DB382CA0B", "license": "MIT",
			  "config": "atlas.hcl", "migrations": "migrations", "note": "n"}]`,
			wantErr: `parse third-party repository ledger: owner/repo: commit "E7B8821A04D82318804A83416CC9D07DB382CA0B" is not a full lowercase SHA`,
		},
		{
			name: "same repository twice",
			ledger: `[{"name": "owner/repo", "clone_url": "https://github.com/owner/repo",
			  "commit": "e7b8821a04d82318804a83416cc9d07db382ca0b", "license": "MIT",
			  "config": "atlas.hcl", "migrations": "migrations", "note": "n"},
			  {"name": "owner/repo", "clone_url": "https://github.com/owner/repo",
			  "commit": "0000000000000000000000000000000000000000", "license": "MIT",
			  "config": "atlas.hcl", "migrations": "migrations", "note": "n"}]`,
			wantErr: `parse third-party repository ledger: owner/repo listed twice`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			repos, err := probe.ParseThirdPartyRepos([]byte(test.ledger))

			c.Assert(err, qt.ErrorMatches, test.wantErr)
			c.Assert(repos, qt.IsNil)
		})
	}
}

// TestThirdPartyLedger_IsWhatTheTierWillRead reads the committed ledger rather
// than a fixture. The parser's rules are only worth anything if the file the
// tier actually opens obeys them, and that file is edited by hand.
func TestThirdPartyLedger_IsWhatTheTierWillRead(t *testing.T) {
	c := qt.New(t)

	data, err := os.ReadFile(filepath.Join("..", "..", "third-party-repos.json"))
	c.Assert(err, qt.IsNil)

	repos, err := probe.ParseThirdPartyRepos(data)

	c.Assert(err, qt.IsNil)
	// A floor, not an exact count: the corpus is meant to grow, but a ledger
	// that lost its last entry would make the tier report zero findings and
	// read as success.
	c.Assert(len(repos) >= 1, qt.IsTrue)
}
