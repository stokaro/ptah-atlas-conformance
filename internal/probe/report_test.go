package probe

import (
	"strings"
	"testing"
)

func TestRenderMarkdownShowsPassingGateWhenNoUnwaivedResults(t *testing.T) {
	report := RenderMarkdown([]Result{
		{Outcome: OK, Probe: "probe", Fixture: "fixture", Stage: "stage", Detail: "ok"},
	}, &Waivers{byKey: map[string]string{}}, "atlas-sha", "ptah-version")

	if !strings.Contains(report, "- Full gate: **0 non-OK** (passes CI)") {
		t.Fatalf("report does not show passing gate:\n%s", report)
	}
}

func TestRenderMarkdownShowsFailingGateWhenUnwaivedResultsRemain(t *testing.T) {
	report := RenderMarkdown([]Result{
		{Outcome: Gap, Probe: "probe", Fixture: "fixture", Stage: "stage", Detail: "gap"},
	}, &Waivers{byKey: map[string]string{}}, "atlas-sha", "ptah-version")

	if !strings.Contains(report, "- Full gate: **1 non-OK** (fails CI)") {
		t.Fatalf("report does not show failing gate:\n%s", report)
	}
}

func TestRenderMarkdownKeepsFullGateRedForWaivedResults(t *testing.T) {
	report := RenderMarkdown([]Result{
		{Outcome: Gap, Probe: "probe", Fixture: "fixture", Stage: "stage", Detail: "gap"},
	}, &Waivers{byKey: map[string]string{
		waiverKey("probe", "fixture", "stage"): "tracked",
	}}, "atlas-sha", "ptah-version")

	if !strings.Contains(report, "- Full gate: **1 non-OK** (fails CI)") {
		t.Fatalf("report does not keep full gate red for waived finding:\n%s", report)
	}
	if !strings.Contains(report, "- Regression budget input: **0 unwaived non-OK**, 1 waived") {
		t.Fatalf("report does not keep waiver scoped to regression budget:\n%s", report)
	}
}
