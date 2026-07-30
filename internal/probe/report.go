package probe

import (
	"fmt"
	"sort"
	"strings"
)

// Summary counts outcomes across a run.
type Summary struct {
	OK, Gap, Fail, Panic int
}

func summarize(results []Result) Summary {
	var s Summary
	for _, r := range results {
		switch r.Outcome {
		case OK:
			s.OK++
		case Gap:
			s.Gap++
		case Fail:
			s.Fail++
		case Panic:
			s.Panic++
		}
	}
	return s
}

// Unwaived returns the gaps/fails/panics not covered by waivers. This is the
// regression budget input; full conformance uses NonOK.
func Unwaived(results []Result, w *Waivers) []Result {
	var out []Result
	for _, r := range results {
		if r.Outcome == OK {
			continue
		}
		if _, ok := w.Reason(r); ok {
			continue
		}
		out = append(out, r)
	}
	return out
}

// RenderMarkdown produces gaps.md: a ranked, grouped report. atlasSHA and
// ptahVersion are recorded so the report is reproducible.
func RenderMarkdown(results []Result, w *Waivers, atlasSHA, ptahVersion string) string {
	return RenderMarkdownWithCommand(results, w, atlasSHA, ptahVersion, "make probe")
}

// RenderMarkdownWithCommand produces a ranked, grouped report and records the
// command that regenerates that specific report file.
func RenderMarkdownWithCommand(results []Result, w *Waivers, atlasSHA, ptahVersion, command string) string {
	return renderMarkdownWithOptions(results, w, markdownReportOptions{
		Title:   "# Ptah vs Atlas — coverage gap report",
		Command: command,
		Intro: "It combines structural coverage over Atlas-authored fixtures with targeted\n" +
			"first-party capability workflows executed through Ptah's public API and CLI.\n" +
			"It is not a quality score: a `gap` records either an Atlas construct Ptah does\n" +
			"not yet support or a first-party workflow contract Ptah failed to preserve.\n\n",
		SourceLine: fmt.Sprintf(
			"Atlas fixtures pinned at `ariga/atlas@%s`; first-party capability sentinels under `testdata/atlas/_capability`",
			atlasSHA,
		),
		PtahVersion: ptahVersion,
	})
}

// RenderLiveMarkdownWithCommand produces a live database self-consistency report
// and records the command that regenerates that specific report file.
func RenderLiveMarkdownWithCommand(results []Result, ptahVersion, command string) string {
	return renderMarkdownWithOptions(results, &Waivers{}, markdownReportOptions{
		Title:   "# Ptah live database conformance report",
		Command: command,
		Intro: "It records whether first-party Ptah schema fixtures survive Ptah's\n" +
			"generate -> apply -> introspect -> diff loop on live databases. It is a\n" +
			"behavioral self-consistency probe, not an Atlas-authored fixture coverage score.\n\n",
		SourceLine:  "Live fixtures: `testdata/live` first-party Ptah schema fixtures",
		PtahVersion: ptahVersion,
	})
}

// RenderDifferentialMarkdownWithCommand produces a live Ptah-vs-Atlas CE
// differential report and records the command that regenerates that report file.
func RenderDifferentialMarkdownWithCommand(results []Result, atlasSHA, ptahVersion, command string) string {
	return renderMarkdownWithOptions(results, &Waivers{}, markdownReportOptions{
		Title:   "# Ptah vs Atlas CE live differential report",
		Command: command,
		Intro: "It records whether first-party Ptah schema fixtures produce the same\n" +
			"introspected schema facts when inspected by Ptah and Atlas CE on live\n" +
			"databases. It is a behavioral equivalence probe, not an Atlas-authored\n" +
			"fixture coverage score.\n\n",
		SourceLine:  fmt.Sprintf("Live fixtures: `testdata/live` first-party Ptah schema fixtures; Atlas CE binary pinned at `ariga/atlas@%s`", atlasSHA),
		PtahVersion: ptahVersion,
		FactCategories: []string{
			"Global schemas and enum definitions.",
			"Schema-qualified table identity and table metadata.",
			"Columns: type, nullability, defaults, primary-key membership, identity, generated expressions, enum values, comments, charset, collation, and ON UPDATE expressions.",
			"Primary keys: ordered columns, prefix and descending parts, and include columns.",
			"Foreign keys: schema-qualified targets, column order, referenced columns, and referential actions.",
			"Unique and check constraints: columns, include columns, NULLS DISTINCT state, comments, and CHECK expressions.",
			"Indexes: ordered columns and expressions, uniqueness, type, parser, operator classes, prefix length, descending parts, partial predicates, include columns, storage params, granularity, and comments.",
		},
	})
}

// RenderMigrateRuntimeMarkdownWithCommand produces a live migration-runtime
// report and records the command that regenerates that specific report file.
func RenderMigrateRuntimeMarkdownWithCommand(results []Result, ptahVersion, command string) string {
	return renderMarkdownWithOptions(results, &Waivers{}, markdownReportOptions{
		Title:   "# Ptah Atlas migrate runtime conformance report",
		Command: command,
		Intro: "It records whether Atlas-form `migrate ...` commands on the ptah-compat binary\n" +
			"preserve Atlas-compatible runtime behavior against real databases. Unlike the\n" +
			"offline txtar-script simulator, this tier executes the real drop-in CLI and\n" +
			"inspects revision rows and end database state directly. Project configuration\n" +
			"apply uses pinned Atlas CE as an independent runtime oracle.\n\n",
		SourceLine:  "Runtime checks: first-party Atlas migration command scenarios against live SQLite, PostgreSQL, and MySQL databases; Atlas CE apply oracle pinned by atlas.version",
		PtahVersion: ptahVersion,
		FactCategories: []string{
			"Migration apply: applied schema objects, Atlas revision rows, and post-apply status.",
			"Migration set: repair-state rows and subsequent application of only remaining migrations.",
			"Atlas project configuration: cloned Atlas CE brownfield state, independent remainder apply, end schema, full revision metadata, and status facts.",
			"Transaction modes: rollback/partial-apply semantics after failed SQLite migrations for `all`, `file`, and `none`.",
			"PostgreSQL runtime behavior: custom revision schemas and `atlas:txmode none` for `CREATE INDEX CONCURRENTLY`.",
			"MySQL runtime behavior: applied schema objects and Atlas revision rows.",
		},
	})
}

type markdownReportOptions struct {
	Title          string
	Command        string
	Intro          string
	SourceLine     string
	PtahVersion    string
	FactCategories []string
}

func renderMarkdownWithOptions(results []Result, w *Waivers, opts markdownReportOptions) string {
	s := summarize(results)
	nonOK := NonOK(results)
	unwaived := Unwaived(results, w)
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n\n", opts.Title)
	fmt.Fprintf(&b, "This file is generated by `%s`. Do not edit by hand.\n\n", opts.Command)
	b.WriteString(opts.Intro)

	if len(nonOK) == 0 {
		b.WriteString("## Status: PARITY on the current corpus\n\n")
		b.WriteString("Every fixture is covered. The conformance gate is green.\n\n")
	} else {
		fmt.Fprintf(&b, "## Status: NOT DONE — %d non-OK observation(s)\n\n", len(nonOK))
		b.WriteString("The conformance gate is **red** and stays red until these close. This is by\n")
		b.WriteString("design: the report is a spec Ptah has not met yet, not a passing test log.\n\n")
	}

	fmt.Fprintf(&b, "- %s\n", opts.SourceLine)
	fmt.Fprintf(&b, "- Ptah at `%s`\n", opts.PtahVersion)
	fmt.Fprintf(&b, "- Outcomes: **%d ok**, **%d gap**, **%d fail**, **%d panic**\n", s.OK, s.Gap, s.Fail, s.Panic)
	fmt.Fprintf(&b, "- Full gate: **%d non-OK** (%s)\n",
		len(nonOK),
		conformanceGateStatus(len(nonOK)),
	)
	fmt.Fprintf(&b, "- Regression budget input: **%d unwaived non-OK**, %d waived\n",
		len(unwaived),
		s.Gap+s.Fail+s.Panic-len(unwaived),
	)
	writeCorpusSummary(&b, results)
	b.WriteString("\n")
	writeFactCategories(&b, opts.FactCategories)

	// Gaps/fails/panics first — the actionable part — most severe first.
	order := map[Outcome]int{Panic: 0, Fail: 1, Gap: 2, OK: 3}
	sorted := append([]Result(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if order[sorted[i].Outcome] != order[sorted[j].Outcome] {
			return order[sorted[i].Outcome] < order[sorted[j].Outcome]
		}
		if sorted[i].Probe != sorted[j].Probe {
			return sorted[i].Probe < sorted[j].Probe
		}
		return sorted[i].Fixture < sorted[j].Fixture
	})

	b.WriteString("## Findings\n\n")
	b.WriteString("| Gate | Outcome | Probe | Fixture | Stage | Detail | Related |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range sorted {
		issue := ""
		if r.Issue != "" {
			issue = "#" + strings.TrimPrefix(r.Issue, "stokaro/ptah#")
		}
		gate := ""
		switch {
		case r.Outcome == OK:
			gate = "—"
		default:
			if _, ok := w.Reason(r); ok {
				gate = "waived"
			} else {
				gate = "**RED**"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | `%s` | %s | %s | %s |\n",
			gate, badge(r.Outcome), r.Probe, r.Fixture, r.Stage, escapePipe(r.Detail), issue)
	}

	// Per-issue rollup so the report doubles as a backlog for ptah.
	byIssue := map[string]int{}
	for _, r := range results {
		if r.Outcome != OK && r.Issue != "" {
			byIssue[r.Issue]++
		}
	}
	if len(byIssue) > 0 {
		b.WriteString("\n## Gaps by related issue\n\n")
		keys := make([]string, 0, len(byIssue))
		for k := range byIssue {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- **%s** — %d finding(s)\n", k, byIssue[k])
		}
	}

	return b.String()
}

func writeFactCategories(b *strings.Builder, categories []string) {
	if len(categories) == 0 {
		return
	}
	b.WriteString("## Compared Schema Fact Categories\n\n")
	for _, category := range categories {
		fmt.Fprintf(b, "- %s\n", category)
	}
	b.WriteString("\n")
}

func conformanceGateStatus(nonOK int) string {
	if nonOK == 0 {
		return "passes CI"
	}
	return "fails CI"
}

func writeCorpusSummary(b *strings.Builder, results []Result) {
	var imported, measured, unmeasured, capabilitySentinels int
	for _, r := range results {
		if r.Probe != "corpus-inventory" {
			continue
		}
		if r.Stage == "capability" {
			capabilitySentinels++
			continue
		}
		imported++
		if r.Outcome == OK {
			measured++
		} else {
			unmeasured++
		}
	}
	if imported == 0 {
		return
	}
	fmt.Fprintf(b, "- Corpus inventory: **%d imported Atlas fixture(s)**, **%d measured**, **%d imported-but-unmeasured**",
		imported, measured, unmeasured)
	if capabilitySentinels > 0 {
		fmt.Fprintf(b, "; **%d first-party capability sentinel(s)**", capabilitySentinels)
	}
	b.WriteString("\n")
}

func badge(o Outcome) string {
	switch o {
	case OK:
		return "ok"
	case Gap:
		return "**gap**"
	case Fail:
		return "**fail**"
	case Panic:
		return "**PANIC**"
	}
	return string(o)
}

func escapePipe(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
