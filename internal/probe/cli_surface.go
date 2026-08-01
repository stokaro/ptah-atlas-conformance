package probe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	cliSurfaceIssue       = "stokaro/ptah#632"
	cliSurfaceCompatIssue = "stokaro/ptah#514"
)

// CLISurfaceCommand is one command discovered from the pinned Atlas CE binary.
type CLISurfaceCommand struct {
	Path    []string
	Summary string
	Usage   string
	// Flags are the long flags the pinned Atlas CE binary registers on this
	// command, discovered from its own help output.
	Flags []string
	// ProFlags are the long flags a licensed Atlas build registers on this same
	// command while the pinned CE binary does not. They are NOT discovered —
	// the CE binary answers "unknown flag" for every one of them — so they come
	// from the static proSurfaceFlags table; see its provenance comment.
	ProFlags             []string
	Classification       CLISurfaceClassification
	ClassificationReason string
}

// CLISurfaceClassification describes whether a discovered Atlas command is an
// OSS parity target for Ptah.
type CLISurfaceClassification string

const (
	CLISurfaceOSS          CLISurfaceClassification = "oss"
	CLISurfaceOutOfScope   CLISurfaceClassification = "out-of-scope"
	CLISurfaceUnclassified CLISurfaceClassification = "unclassified"
)

// CLISurfaceInventory captures Atlas CE help output after recursive discovery.
type CLISurfaceInventory struct {
	AtlasVersion string
	Commands     []CLISurfaceCommand
}

type atlasHelpCommand struct {
	Name    string
	Summary string
}

type helpDetails struct {
	Usage       string
	Flags       []string
	Subcommands []atlasHelpCommand
}

// ProbeCLISurface discovers the command tree from atlasBin and compares every
// OSS command against the ptah-compat binary named atlas — the single
// Atlas-shaped surface Ptah ships since stokaro/ptah#850 removed the `ptah
// atlas ...` namespace (the cli-exit-behavior probe pins that removal).
func ProbeCLISurface(atlasBin string) ([]Result, CLISurfaceInventory, error) {
	atlasBin = strings.TrimSpace(atlasBin)
	if atlasBin == "" {
		return nil, CLISurfaceInventory{}, fmt.Errorf("atlas binary path is empty")
	}

	inventory, err := DiscoverCLISurface(atlasBin)
	if err != nil {
		return nil, CLISurfaceInventory{}, err
	}

	compatBin, compatErr := ptahCompatAtlasBinary()

	results := inventoryResults(inventory)
	results = append(results, buildComparisonResults("atlas-cli-surface-ptah-compat", inventory, compatBin, compatErr)...)
	return results, inventory, nil
}

// DiscoverCLISurface recursively reads Cobra help from the pinned Atlas CE
// binary. It intentionally uses the binary rather than a static table so Atlas
// command/flag drift is measured as soon as atlas.version changes.
func DiscoverCLISurface(atlasBin string) (CLISurfaceInventory, error) {
	version, err := atlasVersionLine(atlasBin)
	if err != nil {
		return CLISurfaceInventory{}, err
	}

	visited := map[string]bool{}
	proFlags := proSurfaceFlags()
	var commands []CLISurfaceCommand
	var walk func(path []string, summary string) error
	walk = func(path []string, summary string) error {
		key := strings.Join(path, " ")
		if visited[key] {
			return nil
		}
		visited[key] = true

		out, err := commandHelp(atlasBin, path)
		if err != nil {
			return fmt.Errorf("read Atlas help for %q: %w", displayCommand("atlas", path), err)
		}
		details := parseHelpDetails(out)
		classification, reason := classifyAtlasCommand(path)
		commands = append(commands, CLISurfaceCommand{
			Path:                 append([]string(nil), path...),
			Summary:              summary,
			Usage:                details.Usage,
			Flags:                details.Flags,
			ProFlags:             append([]string(nil), proFlags[key]...),
			Classification:       classification,
			ClassificationReason: reason,
		})

		for _, sub := range details.Subcommands {
			if skipAtlasHelpSubcommand(sub.Name) {
				continue
			}
			if err := walk(append(append([]string(nil), path...), sub.Name), sub.Summary); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(nil, ""); err != nil {
		return CLISurfaceInventory{}, err
	}

	for _, known := range knownOutOfScopeAtlasCommands() {
		if visited[strings.Join(known.path, " ")] {
			continue
		}
		commands = append(commands, CLISurfaceCommand{
			Path:                 known.path,
			Summary:              known.summary,
			Usage:                displayCommand("atlas", known.path),
			Classification:       CLISurfaceOutOfScope,
			ClassificationReason: known.reason,
		})
	}

	sort.Slice(commands, func(i, j int) bool {
		return strings.Join(commands[i].Path, " ") < strings.Join(commands[j].Path, " ")
	})
	return CLISurfaceInventory{AtlasVersion: version, Commands: commands}, nil
}

// proSurfaceFlags is a closed, per-command allow-list of long flags that a
// licensed Atlas build registers on a command the pinned CE binary also ships,
// but which CE itself does not register. Implementing one of these on
// ptah-compat is deliberate — stokaro/ptah#951 wants Ptah to be a drop-in even
// for Atlas Pro — so it must not be reported as a non-Atlas flag.
//
// It cannot be discovered. DiscoverCLISurface reads the pinned CE binary, and
// that binary answers `Error: unknown flag: --include` for these flags, so the
// data has to be static.
//
// PROVENANCE: captured 2026-08-01 from a licensed Atlas build reporting
// `atlas version v1.2.4-e282f76-canary`, recorded as the `help-surface`
// capture `trial-surface.json` in the atlas-pro-trial-observations working
// notes. Each entry is that build's long-flag set for the command minus the
// pinned CE binary's long-flag set for the same command (`--help` excluded, as
// everywhere else in this file).
//
// Why an allow-list and not a waivers.txt line: a waiver key is (probe,
// fixture, stage), so waiving `atlas schema inspect`/`flags` would hide EVERY
// extra flag on that command from then on, including a genuinely non-Atlas one.
// The single property this tier protects is that ptah-compat exposes no flag
// Atlas does not have, so the allowance has to name the exact flags. waivers.txt
// also stays empty as standing policy: a red tier stays red rather than waived.
//
// Adding an entry never relaxes the missing-flag direction — compareFlags still
// requires every CE flag — and a listed flag that ptah-compat does not
// implement stays invisible until it does, at which point it is named in the
// report detail.
func proSurfaceFlags() map[string][]string {
	return map[string][]string{
		// CE v1.2.0 registers --config --dev-url --env --exclude --format
		// --schema --url --var on `atlas schema inspect`; the licensed build
		// registers these four in addition. ptah-compat implements --include
		// (stokaro/ptah#977); --export, --output, and --web are listed because
		// they are part of the same measured licensed-only delta, and listing
		// them keeps an unimplemented Pro flag from ever reading as a Ptah gap.
		"schema inspect": {"--export", "--include", "--output", "--web"},
	}
}

func inventoryResults(inventory CLISurfaceInventory) []Result {
	out := make([]Result, 0, len(inventory.Commands))
	for _, cmd := range inventory.Commands {
		fixture := displayCommand("atlas", cmd.Path)
		switch cmd.Classification {
		case CLISurfaceOSS:
			out = append(out, Result{"atlas-cli-surface-inventory", fixture, "classify", OK,
				"Atlas CE command is an OSS parity target: " + cmd.ClassificationReason, ""})
		case CLISurfaceOutOfScope:
			out = append(out, Result{"atlas-cli-surface-inventory", fixture, "classify", OK,
				"not an OSS drop-in target: " + cmd.ClassificationReason, ""})
		default:
			out = append(out, Result{"atlas-cli-surface-inventory", fixture, "classify", Gap,
				"Atlas CE exposes an unclassified command; decide whether Ptah must implement it or explicitly waive it",
				cliSurfaceIssue})
		}
	}
	return out
}

func buildComparisonResults(probeName string, inventory CLISurfaceInventory, bin string, buildErr error) []Result {
	var out []Result
	if buildErr != nil {
		return []Result{{probeName, "atlas", "build", Fail, "could not build Ptah CLI: " + oneLine(buildErr.Error()), ""}}
	}

	for _, atlasCmd := range inventory.Commands {
		path := atlasCmd.Path
		const prefix = "atlas"
		issue := cliSurfaceCompatIssue
		display := displayCommand(prefix, atlasCmd.Path)
		if atlasCmd.Classification == CLISurfaceOutOfScope {
			if surface, ok := implementedProVerbSurfaces()[strings.Join(atlasCmd.Path, " ")]; ok {
				out = append(out, compareImplementedProCommand(probeName, display, bin, path, atlasCmd, surface, issue)...)
				continue
			}
			out = append(out, compareOutOfScopeCommand(probeName, display, bin, path, atlasCmd, issue))
			continue
		}
		if atlasCmd.Classification != CLISurfaceOSS {
			continue
		}
		target, err := readCommandSurface(bin, path)
		if err != nil {
			out = append(out, Result{probeName, display, "help", Fail,
				"reading `" + display + " --help` failed: " + oneLine(err.Error()), ""})
			continue
		}
		if !usageMatchesPrefix(target.Usage, prefix, atlasCmd.Path) {
			out = append(out, Result{probeName, display, "resolve", Gap,
				"`" + display + "` did not resolve to the Atlas-shaped command usage; got `" + target.Usage + "`",
				issue})
			continue
		}
		out = append(out, compareUsage(probeName, display, atlasCmd, target, issue))
		out = append(out, compareFlags(probeName, display, atlasCmd, target, issue))
	}
	return out
}

// compareOutOfScopeCommand checks an out-of-scope Atlas command that Ptah
// deliberately keeps as a CE-boundary stub — the Cloud/registry verbs such as
// `migrate push`, `schema push`, and the `schema plan` registry sub-verbs.
// The expectation is exact in the closed direction: the command must report
// Atlas CE's community-version abort. A stub that starts resolving as an open
// capability is a Gap, not a silent upgrade — an implemented verb must be
// listed in implementedProVerbSurfaces so its usage/flag surface and workflow
// behavior are measured instead of merely observed.
func compareOutOfScopeCommand(probeName, display, bin string, path []string, atlasCmd CLISurfaceCommand, issue string) Result {
	out, err := commandOutput(bin, path)
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return Result{probeName, display, "out-of-scope-runtime", Fail,
				"executing `" + display + "` failed: " + oneLine(err.Error()), ""}
		}
	}
	want := "'" + displayCommand("atlas", atlasCmd.Path) + "' is not supported by the community version"
	if strings.Contains(out, want) {
		return Result{probeName, display, "out-of-scope-runtime", OK,
			"`" + display + "` reports the same community-version unsupported boundary as Atlas CE", ""}
	}
	return Result{probeName, display, "out-of-scope-runtime", Gap,
		"`" + display + "` did not report Atlas CE's community-version unsupported boundary; still-stubbed Cloud/registry verbs must keep the CE abort until they are implemented and added to the open-capability expectations; got `" + oneLine(out) + "`",
		issue}
}

// implementedProVerbSurface is the first-party usage/flag contract for an
// Atlas Pro/Cloud verb that Ptah implements as an open capability. The pinned
// Atlas CE binary aborts on these verbs and has no help for them, so — unlike
// the OSS rows — there is no CE oracle: this repository owns the expected
// surface, and Ptah exposing extra flags beyond the required set is allowed.
type implementedProVerbSurface struct {
	// usage is the Atlas-shaped usage line `--help` must report.
	usage string
	// flags are the long flags every Ptah surface must expose at minimum.
	flags []string
}

// implementedProVerbSurfaces lists the out-of-scope Atlas commands Ptah has
// implemented as open capabilities: `migrate test` / `schema test`
// (stokaro/ptah#805), `migrate edit` / `rebase` / `rm` (stokaro/ptah#807),
// the local half of `schema plan` (stokaro/ptah#809), and the earlier
// `migrate checkpoint` (stokaro/ptah#660). For these verbs the out-of-scope
// expectation is tight in the open direction: they must resolve with a real
// command surface, and regressing to Atlas CE's community-version abort stub
// is a conformance gap. Out-of-scope verbs absent from this map must keep the
// CE abort boundary instead.
func implementedProVerbSurfaces() map[string]implementedProVerbSurface {
	return map[string]implementedProVerbSurface{
		"migrate checkpoint": {
			usage: "atlas migrate checkpoint [flags] [name]",
			flags: []string{"--dev-url", "--dir", "--dir-format"},
		},
		"migrate edit": {
			usage: "atlas migrate edit [flags] {name | version}",
			flags: []string{"--dir", "--dir-format"},
		},
		"migrate rebase": {
			usage: "atlas migrate rebase [flags] {name | version}...",
			flags: []string{"--dir", "--dir-format"},
		},
		"migrate rm": {
			usage: "atlas migrate rm [flags] {name | version}",
			flags: []string{"--dir", "--dir-format"},
		},
		"migrate test": {
			usage: "atlas migrate test [flags] [paths]",
			flags: []string{"--dev-url", "--dir", "--dir-format", "--run"},
		},
		"schema test": {
			usage: "atlas schema test [flags] [paths]",
			flags: []string{"--dev-url", "--run", "--url"},
		},
		"schema plan": {
			usage: "atlas schema plan [flags]",
			flags: []string{"--dev-url", "--dry-run", "--edit", "--from", "--name", "--output", "--save", "--to"},
		},
	}
}

// compareImplementedProCommand checks an out-of-scope Atlas command that Ptah
// implements as an open capability. Three observations are emitted per
// surface: the runtime boundary (the CE community-version abort must be gone),
// the help usage line, and the minimum long-flag set. The usage/flag oracle is
// first-party (see implementedProVerbSurfaces) because the CE binary cannot
// supply help for these verbs; behavioral evidence is owned by the matching
// workflow probes, not by help output. A regression to the CE abort stub
// short-circuits so the gate points at the real problem.
func compareImplementedProCommand(probeName, display, bin string, path []string, atlasCmd CLISurfaceCommand, surface implementedProVerbSurface, issue string) []Result {
	bare, err := commandOutput(bin, path)
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return []Result{{probeName, display, "capability-runtime", Fail,
				"executing `" + display + "` failed: " + oneLine(err.Error()), ""}}
		}
	}
	boundary := "'" + displayCommand("atlas", atlasCmd.Path) + "' is not supported by the community version"
	if strings.Contains(bare, boundary) {
		return []Result{{probeName, display, "capability-runtime", Gap,
			"`" + display + "` regressed to Atlas CE's community-version abort stub; Ptah implements this Pro verb as an open capability and must keep it resolving", issue}}
	}
	out := []Result{{probeName, display, "capability-runtime", OK,
		"`" + display + "` executes as an open Ptah capability instead of Atlas CE's community-version abort; behavioral coverage is owned by the matching workflow probe", ""}}

	target, err := readCommandSurface(bin, path)
	if err != nil {
		return append(out, Result{probeName, display, "help", Fail,
			"reading `" + display + " --help` failed: " + oneLine(err.Error()), ""})
	}
	out = append(out, compareImplementedProUsage(probeName, display, atlasCmd, surface, target, issue))
	out = append(out, compareImplementedProFlags(probeName, display, surface, target, issue))
	return out
}

func compareImplementedProUsage(probeName, display string, atlasCmd CLISurfaceCommand, surface implementedProVerbSurface, target helpDetails, issue string) Result {
	want := normalizeUsage(surface.usage)
	got := normalizeUsage(remapUsageBinary(target.Usage, displayCommand("atlas", atlasCmd.Path), display))
	if got == want {
		return Result{probeName, display, "usage", OK,
			"usage matches the first-party open-capability contract: `" + surface.usage + "` (Atlas CE has no help for this verb)", ""}
	}
	return Result{probeName, display, "usage", Gap,
		"usage mismatch; the first-party open-capability contract expects `" + surface.usage + "`, Ptah has `" + target.Usage + "`", issue}
}

func compareImplementedProFlags(probeName, display string, surface implementedProVerbSurface, target helpDetails, issue string) Result {
	missing := missingStrings(surface.flags, target.Flags)
	if len(missing) == 0 {
		return Result{probeName, display, "flags", OK,
			"exposes the first-party required long flags: " + strings.Join(surface.flags, " ") + " (extra flags are allowed; Atlas CE has no flag oracle for this verb)", ""}
	}
	return Result{probeName, display, "flags", Gap,
		"flag mismatch against the first-party open-capability contract: missing " + strings.Join(missing, ", "), issue}
}

func compareUsage(probeName, display string, atlasCmd CLISurfaceCommand, target helpDetails, issue string) Result {
	want := normalizeUsage(atlasCmd.Usage)
	got := normalizeUsage(remapUsageBinary(target.Usage, displayCommand("atlas", atlasCmd.Path), display))
	if got == want {
		return Result{probeName, display, "usage", OK, "usage matches Atlas: `" + atlasCmd.Usage + "`", ""}
	}
	return Result{probeName, display, "usage", Gap,
		"usage mismatch; Atlas has `" + atlasCmd.Usage + "`, Ptah has `" + target.Usage + "`", issue}
}

// compareFlags compares ptah-compat's long-flag surface for an OSS Atlas
// command against the pinned CE binary. The two directions are deliberately
// asymmetric:
//
//   - missing is measured against the CE flag set ONLY. A Pro-surface flag
//     ptah-compat has not implemented is not a gap: CE does not expose it
//     either, so a drop-in replacement for CE is complete without it.
//   - extra is measured against the CE flag set PLUS this command's
//     proSurfaceFlags allowance, so a flag a licensed Atlas build registers on
//     this same command is not reported as a non-Atlas flag. Anything outside
//     both sets is still a gap — the allowance is closed and per-command, so an
//     arbitrary extra flag stays red.
//
// A command that adopted part of the licensed surface does NOT collapse to a
// bare OK: the adopted flags are named in the detail so the committed report
// keeps showing which Pro surface ptah-compat has taken on, and so a mistaken
// allow-list entry is visible instead of silently permanent.
func compareFlags(probeName, display string, atlasCmd CLISurfaceCommand, target helpDetails, issue string) Result {
	missing := missingStrings(atlasCmd.Flags, target.Flags)
	allowed := append(append([]string(nil), atlasCmd.Flags...), atlasCmd.ProFlags...)
	extra := missingStrings(target.Flags, allowed)
	if len(missing) == 0 && len(extra) == 0 {
		detail := "long flags match Atlas: " + strings.Join(atlasCmd.Flags, " ")
		if len(atlasCmd.Flags) == 0 {
			detail = "long flags match Atlas: no long flags"
		}
		// Only flags the pinned CE binary does NOT register count as adopted Pro
		// surface: once an atlas.version bump moves a listed flag into CE's own
		// help, it is ordinary CE parity and the allowance entry is dead weight.
		proOnly := missingStrings(atlasCmd.ProFlags, atlasCmd.Flags)
		if adopted := commonStrings(proOnly, target.Flags); len(adopted) > 0 {
			detail += "; plus Pro-surface flags implemented openly: " + strings.Join(adopted, " ")
		}
		return Result{probeName, display, "flags", OK, detail, ""}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "extra "+strings.Join(extra, ", "))
	}
	return Result{probeName, display, "flags", Gap, "flag mismatch: " + strings.Join(parts, "; "), issue}
}

func readCommandSurface(bin string, path []string) (helpDetails, error) {
	out, err := commandHelp(bin, path)
	if err != nil {
		return helpDetails{}, err
	}
	return parseHelpDetails(out), nil
}

func commandHelp(bin string, path []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(append([]string(nil), path...), "--help")
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "", err
		}
	}
	return string(out), nil
}

func atlasVersionLine(atlasBin string) (string, error) {
	out, err := commandOutput(atlasBin, []string{"version"})
	if err != nil {
		return "", fmt.Errorf("read Atlas version: %w", err)
	}
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if line == "" {
		return "", fmt.Errorf("atlas version output was empty")
	}
	return line, nil
}

func parseHelpDetails(help string) helpDetails {
	lines := strings.Split(help, "\n")
	return helpDetails{
		Usage:       extractUsage(lines),
		Flags:       sortedFlags(lines),
		Subcommands: parseAvailableCommands(lines),
	}
}

func extractUsage(lines []string) string {
	for i, line := range lines {
		if strings.TrimSpace(line) != "Usage:" {
			continue
		}
		for _, candidate := range lines[i+1:] {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func parseAvailableCommands(lines []string) []atlasHelpCommand {
	var out []atlasHelpCommand
	inCommands := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Available Commands:":
			inCommands = true
			continue
		case inCommands && trimmed == "":
			return out
		case !inCommands:
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		out = append(out, atlasHelpCommand{Name: fields[0], Summary: strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))})
	}
	return out
}

func sortedFlags(lines []string) []string {
	set := map[string]bool{}
	inFlagSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "Flags:", "Global Flags:", "Inherited Flags:":
			inFlagSection = true
			continue
		case "":
			inFlagSection = false
			continue
		}
		if !inFlagSection {
			continue
		}
		for _, flag := range flagPattern.FindAllString(line, -1) {
			if flag == "--help" {
				continue
			}
			set[flag] = true
		}
	}
	out := make([]string, 0, len(set))
	for flag := range set {
		out = append(out, flag)
	}
	sort.Strings(out)
	return out
}

func classifyAtlasCommand(path []string) (CLISurfaceClassification, string) {
	key := strings.Join(path, " ")
	switch key {
	case "", "license", "version",
		"schema", "schema apply", "schema clean", "schema diff", "schema fmt", "schema inspect",
		"migrate", "migrate apply", "migrate diff", "migrate hash", "migrate import",
		"migrate lint", "migrate new", "migrate set", "migrate status", "migrate validate":
		return CLISurfaceOSS, "present in Atlas CE and not cloud-gated"
	}
	for _, known := range knownOutOfScopeAtlasCommands() {
		if key == strings.Join(known.path, " ") {
			return CLISurfaceOutOfScope, known.reason
		}
	}
	return CLISurfaceUnclassified, "new command discovered in Atlas CE"
}

func skipAtlasHelpSubcommand(name string) bool {
	switch name {
	case "completion", "help":
		return true
	default:
		return false
	}
}

type outOfScopeCommand struct {
	path    []string
	summary string
	reason  string
}

// knownOutOfScopeAtlasCommands lists Atlas commands that are not OSS drop-in
// parity targets because the pinned CE binary aborts on them. Two directions
// exist within this set: verbs listed in implementedProVerbSurfaces must
// resolve as open Ptah capabilities, while the rest (the Cloud/registry verbs,
// including the `schema plan` registry sub-verbs) must keep Atlas CE's
// community-version abort boundary.
func knownOutOfScopeAtlasCommands() []outOfScopeCommand {
	return []outOfScopeCommand{
		{[]string{"schema", "test"}, "Test schemas through Atlas Cloud", "Atlas Pro/Cloud test workflow not present in the pinned CE binary; Ptah implements it as an open capability"},
		{[]string{"schema", "plan"}, "Plan schema changes through Atlas Cloud", "Atlas Pro/Cloud plan workflow not present in the pinned CE binary; Ptah implements the local plan-file half as an open capability"},
		{[]string{"schema", "plan", "approve"}, "Approve a plan in the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "lint"}, "Lint a plan against the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "list"}, "List plans in the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "new"}, "Create a new plan in the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "pull"}, "Pull a plan from the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "push"}, "Push a plan to the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "rm"}, "Remove a plan from the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "test"}, "Test a plan through the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan", "validate"}, "Validate a plan through the Atlas Registry", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"schema", "push"}, "Push schema state to Atlas Cloud", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"migrate", "checkpoint"}, "Create migration checkpoint files", "Atlas Pro feature not present in the pinned CE binary; Ptah implements it as an open capability"},
		{[]string{"migrate", "edit"}, "Edit migration files", "Atlas Pro directory-maintenance verb not present in the pinned CE binary; Ptah implements it as an open capability"},
		{[]string{"migrate", "push"}, "Push migration directory to Atlas Cloud", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"migrate", "rebase"}, "Rebase migration files", "Atlas Pro directory-maintenance verb not present in the pinned CE binary; Ptah implements it as an open capability"},
		{[]string{"migrate", "rm"}, "Remove migration files", "Atlas Pro directory-maintenance verb not present in the pinned CE binary; Ptah implements it as an open capability"},
		{[]string{"migrate", "test"}, "Test migration files through Atlas Cloud", "Atlas Pro/Cloud test workflow not present in the pinned CE binary; Ptah implements it as an open capability"},
	}
}

func displayCommand(prefix string, path []string) string {
	tokens := []string{prefix}
	tokens = append(tokens, path...)
	return strings.Join(tokens, " ")
}

func usageMatchesPrefix(usage, prefix string, path []string) bool {
	want := displayCommand(prefix, path)
	return usage == want || strings.HasPrefix(usage, want+" ")
}

func remapUsageBinary(usage, atlasUsagePrefix, ptahUsagePrefix string) string {
	if rest, ok := strings.CutPrefix(usage, ptahUsagePrefix); ok {
		return atlasUsagePrefix + rest
	}
	return usage
}

func normalizeUsage(usage string) string {
	return strings.Join(strings.Fields(usage), " ")
}

// commonStrings returns the members of want that also appear in got, keeping
// want's order so the rendered detail is deterministic.
func commonStrings(want, got []string) []string {
	have := make(map[string]bool, len(got))
	for _, value := range got {
		have[value] = true
	}
	var common []string
	for _, value := range want {
		if have[value] {
			common = append(common, value)
		}
	}
	return common
}

func missingStrings(want, got []string) []string {
	have := make(map[string]bool, len(got))
	for _, value := range got {
		have[value] = true
	}
	var missing []string
	for _, value := range want {
		if !have[value] {
			missing = append(missing, value)
		}
	}
	sort.Strings(missing)
	return missing
}

// DefaultAtlasBinary returns the Atlas CE binary path used by CLI-surface
// probes. ATLAS_BIN wins; otherwise the repository-local build is preferred.
func DefaultAtlasBinary() string {
	if env := strings.TrimSpace(os.Getenv("ATLAS_BIN")); env != "" {
		return env
	}
	local := filepath.Join("bin", "atlas")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return "atlas"
}
