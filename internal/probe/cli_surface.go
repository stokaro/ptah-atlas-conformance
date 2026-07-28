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
	cliSurfaceGapIssue    = "stokaro/ptah#510"
	cliSurfaceCompatIssue = "stokaro/ptah#514"
)

// CLISurfaceCommand is one command discovered from the pinned Atlas CE binary.
type CLISurfaceCommand struct {
	Path                 []string
	Summary              string
	Usage                string
	Flags                []string
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
// OSS command against both Ptah's namespaced CLI and the ptah-compat binary
// named atlas.
func ProbeCLISurface(atlasBin string) ([]Result, CLISurfaceInventory, error) {
	atlasBin = strings.TrimSpace(atlasBin)
	if atlasBin == "" {
		return nil, CLISurfaceInventory{}, fmt.Errorf("atlas binary path is empty")
	}

	inventory, err := DiscoverCLISurface(atlasBin)
	if err != nil {
		return nil, CLISurfaceInventory{}, err
	}

	nativeBin, nativeErr := ptahBinary()
	compatBin, compatErr := ptahCompatAtlasBinary()

	results := inventoryResults(inventory)
	results = append(results, buildComparisonResults("atlas-cli-surface-ptah-namespace", inventory, nativeBin, nativeErr, true)...)
	results = append(results, buildComparisonResults("atlas-cli-surface-ptah-compat", inventory, compatBin, compatErr, false)...)
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

func buildComparisonResults(probeName string, inventory CLISurfaceInventory, bin string, buildErr error, native bool) []Result {
	var out []Result
	if buildErr != nil {
		return []Result{{probeName, "atlas", "build", Fail, "could not build Ptah CLI: " + oneLine(buildErr.Error()), ""}}
	}

	for _, atlasCmd := range inventory.Commands {
		path := atlasCmd.Path
		prefix := "ptah atlas"
		issue := cliSurfaceGapIssue
		if !native {
			prefix = "atlas"
			issue = cliSurfaceCompatIssue
		} else {
			path = append([]string{"atlas"}, atlasCmd.Path...)
		}
		display := displayCommand(prefix, atlasCmd.Path)
		if atlasCmd.Classification == CLISurfaceOutOfScope {
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
	target, helpErr := readCommandSurface(bin, path)
	if helpErr == nil && (target.Usage == display || strings.HasPrefix(target.Usage, display+" ")) {
		return Result{probeName, display, "out-of-scope-runtime", OK,
			"`" + display + "` resolves as an open Ptah capability beyond Atlas CE; this surface check does not claim behavioral coverage", ""}
	}
	return Result{probeName, display, "out-of-scope-runtime", Gap,
		"`" + display + "` neither reports Atlas CE's community-version unsupported boundary nor resolves as an explicit Ptah capability; got `" + oneLine(out) + "`",
		issue}
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

func compareFlags(probeName, display string, atlasCmd CLISurfaceCommand, target helpDetails, issue string) Result {
	missing := missingStrings(atlasCmd.Flags, target.Flags)
	extra := missingStrings(target.Flags, atlasCmd.Flags)
	if len(missing) == 0 && len(extra) == 0 {
		detail := "long flags match Atlas: " + strings.Join(atlasCmd.Flags, " ")
		if len(atlasCmd.Flags) == 0 {
			detail = "long flags match Atlas: no long flags"
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

func knownOutOfScopeAtlasCommands() []outOfScopeCommand {
	return []outOfScopeCommand{
		{[]string{"schema", "test"}, "Test schemas through Atlas Cloud", "Atlas Cloud / commercial workflow, not present in the pinned CE binary"},
		{[]string{"schema", "plan"}, "Plan schema changes through Atlas Cloud", "Atlas Cloud / commercial workflow, not present in the pinned CE binary"},
		{[]string{"schema", "push"}, "Push schema state to Atlas Cloud", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"migrate", "checkpoint"}, "Create migration checkpoint files", "Atlas feature intentionally tracked outside the current OSS drop-in target"},
		{[]string{"migrate", "edit"}, "Edit migration files", "Atlas convenience command intentionally tracked outside the current OSS drop-in target"},
		{[]string{"migrate", "push"}, "Push migration directory to Atlas Cloud", "Atlas Cloud / registry workflow, not present in the pinned CE binary"},
		{[]string{"migrate", "rebase"}, "Rebase migration files", "Atlas convenience command intentionally tracked outside the current OSS drop-in target"},
		{[]string{"migrate", "rm"}, "Remove migration files", "Atlas convenience command intentionally tracked outside the current OSS drop-in target"},
		{[]string{"migrate", "test"}, "Test migration files through Atlas Cloud", "Atlas Cloud / commercial workflow, not present in the pinned CE binary"},
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
