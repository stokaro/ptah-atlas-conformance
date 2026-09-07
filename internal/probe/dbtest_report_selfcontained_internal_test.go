package probe

// White-box testing required: the HTML report expectations and the fragment
// evaluator are unexported, and the property under test is what the forbidden
// list admits rather than anything a probe run reports. Driving the probe
// itself could only ever show that today's Ptah passes, which is exactly the
// evidence that cannot tell a rule that discriminates from one that stopped.

import (
	"testing"

	qt "github.com/frankban/quicktest"
)

// TestHTMLReportForbiddenList_ForbidsAFetchAndNotAScheme pins the narrowing.
// The rule used to forbid the strings "http://" and "https://", which caught
// Ptah's own report footer -- an href to ptah.run that a reader follows by
// clicking and a browser never loads on its own. The property the probe wants
// is that opening the report fetches nothing, so each row here is a document
// that either does or does not fetch on open, and the plain href must pass
// while every real fetch vector must not.
func TestHTMLReportForbiddenList_ForbidsAFetchAndNotAScheme(t *testing.T) {
	// The real footer Ptah renders, so the allowed case is the measured one
	// rather than a simplified stand-in.
	const footer = `<div class="footer"><span>Rendered by Ptah from the test run. ` +
		`This file is self-contained: opening it fetches nothing.</span>` +
		`<a class="footer-mark" href="https://ptah.run" target="_blank" rel="noopener noreferrer">` +
		`<svg viewBox="0 0 64 64"><rect width="64" height="64"/></svg>ptah dev</a></div>`

	tests := []struct {
		name      string
		body      string
		wantFetch bool
	}{
		{name: "the report footer's own brand href", body: footer, wantFetch: false},
		{name: "an inline svg", body: `<svg viewBox="0 0 8 8"><path d="M0 0"/></svg>`, wantFetch: false},
		{name: "an in-document anchor", body: `<a class="ref" href="#authors">authors</a>`, wantFetch: false},
		{name: "a remote stylesheet", body: `<link rel="stylesheet" href="https://cdn/x.css">`, wantFetch: true},
		{name: "a remote script", body: `<script src="https://cdn/x.js"></script>`, wantFetch: true},
		{name: "a remote image, double quoted", body: `<img src="https://cdn/x.png">`, wantFetch: true},
		{name: "a remote image, single quoted", body: `<img src='http://cdn/x.png'>`, wantFetch: true},
		{name: "a remote image, unquoted", body: `<img src=https://cdn/x.png>`, wantFetch: true},
		{name: "a css background", body: `<style>body{background:url(https://cdn/x.png)}</style>`, wantFetch: true},
		{name: "a css import", body: `<style>@import "https://cdn/x.css";</style>`, wantFetch: true},
		{name: "an embedded frame", body: `<iframe src="https://cdn/x.html"></iframe>`, wantFetch: true},
	}

	// Both HTML report checks share one forbidden list, so measuring either
	// measures the rule. The migration one is taken here.
	expectation := htmlReportExpectationUnderTest(qt.New(t))

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := qt.New(t)

			err := fragmentExpectation{forbidden: expectation.forbidden}.validate(test.body)

			c.Assert(err != nil, qt.Equals, test.wantFetch,
				qt.Commentf("body %q gave %v", test.body, err))
		})
	}
}

// htmlReportExpectationUnderTest returns the stdout expectation of the
// migration HTML report check, read out of the production catalog rather than
// restated here: a copy would keep passing after the real list changed.
func htmlReportExpectationUnderTest(c *qt.C) fragmentExpectation {
	byFixture := map[string]dbTestOutputValidator{}
	for _, check := range dbTestMigrationReportChecks(nil) {
		byFixture[check.fixture] = check.stdout
	}
	expectation, ok := byFixture["ptah migrations test/html"].(fragmentExpectation)
	c.Assert(ok, qt.IsTrue)
	return expectation
}
