package probe

// White-box testing required: the project-config status decoder is an
// unexported probe boundary whose malformed-output behavior cannot be observed
// through the public harness without launching both Atlas CE and Ptah.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

func TestParseProjectConfigStatusFacts_HappyPath(t *testing.T) {
	c := qt.New(t)
	input := `{
		"Available": [
			{
				"Name": "20260719010000_create_users.sql",
				"Version": "20260719010000",
				"Description": "create_users",
				"Type": ""
			}
		],
		"Applied": [
			{
				"Name": "",
				"Version": "20260719010000",
				"Description": "create_users",
				"Type": "manually set"
			}
		],
		"Pending": [],
		"Current": "20260719010000",
		"Next": "",
		"Status": "OK"
	}`

	got, err := parseProjectConfigStatusFacts(input)

	c.Assert(err, qt.IsNil)
	c.Assert(got, qt.DeepEquals, projectConfigStatusFacts{
		Available: []projectConfigStatusFile{
			{
				Name:        "20260719010000_create_users.sql",
				Version:     "20260719010000",
				Description: "create_users",
			},
		},
		Applied: []projectConfigStatusFile{
			{
				Version:     "20260719010000",
				Description: "create_users",
				Type:        "manually set",
			},
		},
		Pending: []projectConfigStatusFile{},
		Current: "20260719010000",
		Status:  "OK",
	})
}

func TestParseProjectConfigStatusFacts_FailurePath(t *testing.T) {
	c := qt.New(t)

	got, err := parseProjectConfigStatusFacts(`{"Available":`)

	c.Assert(err, qt.ErrorMatches, `decode migrate status JSON: unexpected end of JSON input: .*`)
	c.Assert(got, qt.DeepEquals, projectConfigStatusFacts{})
}
