package probe

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/stokaro/ptah/core/ast"
	"github.com/stokaro/ptah/core/atlashcl"
	"github.com/stokaro/ptah/core/convert/fromschema"
	"github.com/stokaro/ptah/core/parser"
	"github.com/stokaro/ptah/migration/migratesum"
	"github.com/stokaro/ptah/migration/migrator"
)

// TxtarScriptProbe parses Atlas integration txtar scripts and records the
// command surface Ptah still needs to execute. This is intentionally not a
// success probe yet: until commands are mapped to Ptah APIs/CLI and their
// assertions are checked, each fixture remains a measured #285 gap instead of
// an imported-but-unmeasured blind spot.
type TxtarScriptProbe struct{}

func (TxtarScriptProbe) Name() string { return "txtar-script" }

func (TxtarScriptProbe) Run(fx Fixture) []Result {
	if fx.Kind != FixtureKindTxtar {
		return nil
	}
	if len(fx.Files) != 1 {
		return []Result{{"txtar-script", fx.Name, "script-surface", Fail,
			fmt.Sprintf("expected one txtar file, got %d", len(fx.Files)), "stokaro/ptah#285"}}
	}
	data, err := os.ReadFile(fx.Files[0])
	if err != nil {
		return []Result{{"txtar-script", fx.Name, "read", Fail, err.Error(), "stokaro/ptah#285"}}
	}

	commands := txtarScriptCommands(string(data))
	if len(commands) == 0 {
		return []Result{{"txtar-script", fx.Name, "script-surface", OK,
			"txtar script has no executable commands", ""}}
	}

	run := runTxtarScript(fx, string(data), commands)
	if run.hasWork() {
		return run.results(fx)
	}

	return []Result{{"txtar-script", fx.Name, "script-surface", Gap,
		"txtar command/runtime execution is not implemented yet; command surface: " + summarizeCommandSurface(commands),
		"stokaro/ptah#285"}}
}

func txtarScriptCommands(data string) []string {
	script := txtarScriptPrefix(data)
	var commands []string
	for _, line := range strings.Split(script, "\n") {
		key, ok := txtarCommandKey(line)
		if ok {
			commands = append(commands, key)
		}
	}
	return commands
}

func txtarScriptPrefix(data string) string {
	lines := strings.Split(data, "\n")
	var script []string
	for _, line := range lines {
		if isTxtarFileMarker(line) {
			break
		}
		script = append(script, line)
	}
	return strings.Join(script, "\n")
}

func isTxtarFileMarker(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "-- ") || !strings.HasSuffix(line, " --") || len(line) <= len("--  --") {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(line, "-- "), " --")
	return name != "" && !strings.ContainsAny(name, " \t")
}

func txtarCommandKey(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	if strings.HasPrefix(line, "! ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "! "))
	}
	fields := strings.Fields(line)
	return txtarCommandKeyFields(fields)
}

func txtarCommandKeyFields(fields []string) (string, bool) {
	if len(fields) > 0 && fields[0] == "exec" {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case "stdout", "stderr", "cmp", "cmpout", "skip", "only":
		return "", false
	case "atlas":
		if len(fields) >= 3 {
			return strings.Join(fields[:3], " "), true
		}
		return strings.Join(fields, " "), true
	default:
		return fields[0], true
	}
}

func summarizeCommandSurface(commands []string) string {
	counts := map[string]int{}
	for _, command := range commands {
		counts[command]++
	}
	keys := make([]string, 0, len(counts))
	for command := range counts {
		keys = append(keys, command)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if counts[a] != counts[b] {
			return cmp.Compare(counts[b], counts[a])
		}
		return cmp.Compare(a, b)
	})
	const maxKeys = 8
	var parts []string
	for i, key := range keys {
		if i == maxKeys {
			parts = append(parts, fmt.Sprintf("... %d more", len(keys)-maxKeys))
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

type txtarRunSummary struct {
	executed    int
	checked     int
	unsupported []string
	failures    []string
}

func (r txtarRunSummary) outcome() Outcome {
	switch {
	case len(r.failures) > 0:
		return Fail
	case len(r.unsupported) > 0:
		return Gap
	default:
		return OK
	}
}

func (r txtarRunSummary) results(fx Fixture) []Result {
	if len(r.failures) == 0 && len(r.unsupported) == 0 {
		return []Result{{"txtar-script", fx.Name, "script-runtime", OK, r.detail(), ""}}
	}

	var out []Result
	for _, failure := range r.failures {
		out = append(out, Result{
			Probe:   "txtar-script",
			Fixture: fx.Name,
			Stage:   "script-runtime",
			Outcome: Fail,
			Detail:  failure,
			Issue:   "stokaro/ptah#285",
		})
	}
	for _, unsupported := range r.unsupported {
		out = append(out, Result{
			Probe:   "txtar-script",
			Fixture: fx.Name,
			Stage:   "script-runtime",
			Outcome: Gap,
			Detail:  "unsupported: " + unsupported,
			Issue:   "stokaro/ptah#285",
		})
	}
	return out
}

func (r txtarRunSummary) hasWork() bool {
	return r.executed > 0 || r.checked > 0 || len(r.unsupported) > 0 || len(r.failures) > 0
}

func (r txtarRunSummary) detail() string {
	var parts []string
	if r.executed > 0 {
		parts = append(parts, fmt.Sprintf("executed %d supported command(s)", r.executed))
	}
	if r.checked > 0 {
		parts = append(parts, fmt.Sprintf("checked %d assertion(s)", r.checked))
	}
	if len(r.unsupported) > 0 {
		parts = append(parts, "unsupported: "+summarizeCommandSurface(r.unsupported))
	}
	if len(r.failures) > 0 {
		parts = append(parts, "failed: "+strings.Join(limitStrings(r.failures, 3), "; "))
	}
	return strings.Join(parts, ", ")
}

type txtarCommandResult struct {
	stdout      string
	stderr      string
	unsupported string
	failed      bool
	err         error
}

type txtarRuntime struct {
	files map[string]string
	dirs  map[string]bool
}

func newTxtarRuntime(data string) *txtarRuntime {
	files := txtarFiles(data)
	dirs := map[string]bool{".": true}
	for name := range files {
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	return &txtarRuntime{files: files, dirs: dirs}
}

func runTxtarScript(fx Fixture, data string, commands []string) txtarRunSummary {
	runtime := newTxtarRuntime(data)
	unsupportedFiles := map[string]bool{}
	dbStateUnsupported := false
	var summary txtarRunSummary
	var last txtarCommandResult
	for _, line := range strings.Split(txtarScriptPrefix(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "only ") || strings.HasPrefix(trimmed, "! only "):
			if key := unsupportedOnlyDirective(fx, trimmed); key != "" {
				summary.unsupported = append(summary.unsupported, key)
			}
			continue
		case strings.HasPrefix(trimmed, "skip ") || strings.HasPrefix(trimmed, "! skip "):
			summary.unsupported = append(summary.unsupported, txtarDirectiveKey(trimmed))
			continue
		case strings.HasPrefix(trimmed, "! stdout "):
			if last.unsupported != "" {
				continue
			}
			summary.checked++
			matched, err := txtarAssertionMatches(last.stdout, strings.TrimPrefix(trimmed, "! "))
			switch {
			case err != nil:
				summary.failures = append(summary.failures, "stdout assertion regexp failed: "+oneLine(err.Error()))
			case matched:
				summary.failures = append(summary.failures, "negative stdout assertion matched")
			}
			continue
		case strings.HasPrefix(trimmed, "! stderr "):
			if last.unsupported != "" {
				continue
			}
			summary.checked++
			matched, err := txtarAssertionMatches(last.stderr, strings.TrimPrefix(trimmed, "! "))
			switch {
			case err != nil:
				summary.failures = append(summary.failures, "stderr assertion regexp failed: "+oneLine(err.Error()))
			case matched:
				summary.failures = append(summary.failures, "negative stderr assertion matched")
			}
			continue
		case strings.HasPrefix(trimmed, "stdout "):
			if last.unsupported != "" {
				continue
			}
			summary.checked++
			matched, err := txtarAssertionMatches(last.stdout, trimmed)
			switch {
			case err != nil:
				summary.failures = append(summary.failures, "stdout assertion regexp failed: "+oneLine(err.Error()))
			case !matched:
				summary.failures = append(summary.failures, "stdout assertion did not match")
			}
			continue
		case strings.HasPrefix(trimmed, "stderr "):
			if last.unsupported != "" {
				continue
			}
			summary.checked++
			matched, err := txtarAssertionMatches(last.stderr, trimmed)
			switch {
			case err != nil:
				summary.failures = append(summary.failures, "stderr assertion regexp failed: "+oneLine(err.Error()))
			case !matched:
				summary.failures = append(summary.failures, "stderr assertion did not match")
			}
			continue
		case strings.HasPrefix(trimmed, "cmp "):
			fields := splitTxtarFields(trimmed)
			if len(fields) != 3 {
				summary.checked++
				summary.failures = append(summary.failures, "unsupported cmp syntax: "+trimmed)
				continue
			}
			if unsupportedFiles[fields[1]] || unsupportedFiles[fields[2]] {
				continue
			}
			summary.checked++
			if mismatch := txtarFilesMismatch(runtime.files, fields[1], fields[2]); mismatch != "" {
				summary.failures = append(summary.failures, mismatch)
			}
			continue
		case strings.HasPrefix(trimmed, "validJSON "):
			fields := splitTxtarFields(trimmed)
			if len(fields) != 2 {
				summary.checked++
				summary.failures = append(summary.failures, "unsupported validJSON syntax: "+trimmed)
				continue
			}
			if unsupportedFiles[fields[1]] {
				continue
			}
			summary.checked++
			if err := txtarValidateJSON(runtime.files, fields[1]); err != nil {
				summary.failures = append(summary.failures, err.Error())
			}
			continue
		case strings.HasPrefix(trimmed, "cmpmig "):
			fields := splitTxtarFields(trimmed)
			if len(fields) != 3 {
				summary.checked++
				summary.failures = append(summary.failures, "unsupported cmpmig syntax: "+trimmed)
				continue
			}
			if txtarCmpmigReadsUnsupportedFile(fields, unsupportedFiles) {
				continue
			}
			summary.checked++
			if mismatch := txtarCmpmigMismatch(runtime, fields[1], fields[2]); mismatch != "" {
				summary.failures = append(summary.failures, mismatch)
			}
			continue
		}

		expectedFailure, commandLine := txtarExpectedFailure(trimmed)
		if commandLine == "" {
			continue
		}
		if dbStateUnsupported && txtarCommandReadsUnsupportedDBState(commandLine) {
			last = txtarCommandResult{unsupported: "blocked by unsupported database state"}
			if redirect := txtarRedirectTarget(commandLine); redirect != "" {
				unsupportedFiles[redirect] = true
			} else {
				unsupportedFiles["stdout"] = true
				unsupportedFiles["stderr"] = true
			}
			continue
		}
		if txtarCommandReadsUnsupportedFile(commandLine, unsupportedFiles) {
			last = txtarCommandResult{unsupported: "blocked by unsupported file"}
			markUnsupportedFileCommandOutputs(commandLine, runtime, unsupportedFiles)
			if !expectedFailure && txtarCommandMutatesDBState(commandLine) {
				dbStateUnsupported = true
			}
			continue
		}
		result := runTxtarCommand(fx, runtime, commandLine, expectedFailure)
		last = result
		if result.unsupported != "" {
			summary.unsupported = append(summary.unsupported, result.unsupported)
			markUnsupportedFileCommandOutputs(commandLine, runtime, unsupportedFiles)
			if !expectedFailure && txtarCommandMutatesDBState(commandLine) {
				dbStateUnsupported = true
			}
			if redirect := txtarRedirectTarget(commandLine); redirect != "" {
				unsupportedFiles[redirect] = true
			} else {
				unsupportedFiles["stdout"] = true
				unsupportedFiles["stderr"] = true
			}
			continue
		}
		summary.executed++
		runtime.files["stdout"] = result.stdout
		runtime.files["stderr"] = result.stderr
		delete(unsupportedFiles, "stdout")
		delete(unsupportedFiles, "stderr")
		if redirect := txtarRedirectTarget(commandLine); redirect != "" {
			delete(unsupportedFiles, redirect)
		}
		clearUnsupportedFileCommandOutputs(commandLine, runtime, unsupportedFiles)
		failed := result.failed || result.err != nil
		switch {
		case expectedFailure && !failed:
			summary.failures = append(summary.failures, "expected command failure, but command succeeded")
		case !expectedFailure && failed:
			summary.failures = append(summary.failures, txtarFailureDetail(result))
		}
	}
	if summary.executed == 0 && summary.checked == 0 && len(summary.unsupported) == 0 {
		summary.unsupported = append(summary.unsupported, commands...)
	}
	return summary
}

func runTxtarCommand(fx Fixture, runtime *txtarRuntime, line string, expectedFailure bool) txtarCommandResult {
	fields := txtarCommandFields(line)
	if result, ok := runTxtarFileCommand(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateHash(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateValidate(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateDiff(fields, expectedFailure); ok {
		return result
	}
	if result, ok := runTxtarMigrateSet(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateStatus(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarSchemaApply(runtime, fields); ok {
		return result
	}
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "schema" || fields[2] != "inspect" {
		if key, ok := txtarCommandKeyFields(fields); ok {
			return txtarCommandResult{unsupported: key}
		}
		return txtarCommandResult{unsupported: line}
	}
	return runTxtarSchemaInspect(fx, runtime.files, fields)
}

func txtarCommandFields(line string) []string {
	fields := splitTxtarFields(line)
	if len(fields) > 0 && fields[0] == "exec" {
		return fields[1:]
	}
	return fields
}

func txtarCommandReadsUnsupportedFile(line string, unsupportedFiles map[string]bool) bool {
	fields := txtarCommandFields(line)
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "cat":
		for _, file := range nonFlagArgs(fields[1:]) {
			if unsupportedFiles[file] {
				return true
			}
		}
	case "cp", "mv":
		args := nonFlagArgs(fields[1:])
		return len(args) >= 1 && unsupportedFiles[args[0]]
	case "atlas":
		if len(fields) < 3 {
			return false
		}
		switch fields[1] + " " + fields[2] {
		case "migrate hash":
			return txtarMigrateHashReadsUnsupportedFile(fields[3:], unsupportedFiles)
		case "migrate apply", "migrate new", "migrate set", "migrate status", "migrate validate":
			return txtarMigrateCommandReadsUnsupportedFile(fields[3:], unsupportedFiles)
		case "schema diff":
			return txtarSchemaDiffReadsUnsupportedFile(fields[3:], unsupportedFiles)
		}
	}
	return false
}

func txtarCmpmigReadsUnsupportedFile(fields []string, unsupportedFiles map[string]bool) bool {
	if len(fields) != 3 {
		return false
	}
	if unsupportedFiles["migrations"] {
		return true
	}
	for name := range unsupportedFiles {
		if txtarMigrateHashReadsFile("migrations", name) {
			return true
		}
	}
	return false
}

func txtarMigrateHashReadsUnsupportedFile(args []string, unsupportedFiles map[string]bool) bool {
	return txtarMigrateCommandReadsUnsupportedFile(args, unsupportedFiles)
}

func txtarMigrateCommandReadsUnsupportedFile(args []string, unsupportedFiles map[string]bool) bool {
	dir := txtarMigrateCommandDir(args)
	if unsupportedFiles[dir] {
		return true
	}
	for file := range unsupportedFiles {
		if txtarMigrateHashReadsFile(dir, file) {
			return true
		}
	}
	return false
}

func txtarMigrateHashReadsFile(dir, file string) bool {
	dir = path.Clean(dir)
	clean := path.Clean(file)
	rel := clean
	if dir != "." && dir != "/" {
		var ok bool
		rel, ok = strings.CutPrefix(clean, dir+"/")
		if !ok {
			return false
		}
	}
	return rel != "" && !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".sql")
}

func txtarSchemaDiffReadsUnsupportedFile(args []string, unsupportedFiles map[string]bool) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--from" || arg == "--to":
			if i+1 >= len(args) {
				continue
			}
			if txtarSchemaRefReadsUnsupportedFile(args[i+1], unsupportedFiles) {
				return true
			}
			i++
		case strings.HasPrefix(arg, "--from="):
			if txtarSchemaRefReadsUnsupportedFile(strings.TrimPrefix(arg, "--from="), unsupportedFiles) {
				return true
			}
		case strings.HasPrefix(arg, "--to="):
			if txtarSchemaRefReadsUnsupportedFile(strings.TrimPrefix(arg, "--to="), unsupportedFiles) {
				return true
			}
		}
	}
	return false
}

func txtarSchemaRefReadsUnsupportedFile(ref string, unsupportedFiles map[string]bool) bool {
	const filePrefix = "file://"
	if !strings.HasPrefix(ref, filePrefix) {
		return false
	}
	name := path.Clean(txtarFileURLPath(ref))
	if unsupportedFiles[name] {
		return true
	}
	for file := range unsupportedFiles {
		if txtarMigrateHashReadsFile(name, file) {
			return true
		}
	}
	return false
}

func runTxtarMigrateHash(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "hash" {
		return txtarCommandResult{}, false
	}

	dir := txtarMigrateHashDir(fields[3:])
	fsys, ok := runtime.subFS(dir)
	if !ok {
		return txtarCommandResult{failed: true, err: fmt.Errorf("migration directory %q missing", dir)}, true
	}
	sum, err := migratesum.ComputeWithFormat(fsys, migrator.MigrationDirFormatAtlas)
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	sumPath := path.Join(dir, migratesum.AtlasFileName)
	runtime.files[sumPath] = string(sum.Bytes())
	runtime.addParentDirs(sumPath)
	return txtarCommandResult{}, true
}

func txtarMigrateHashDir(args []string) string {
	return txtarMigrateCommandDir(args)
}

func runTxtarMigrateValidate(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "validate" {
		return txtarCommandResult{}, false
	}

	dir := txtarMigrateCommandDir(fields[3:])
	fsys, ok := runtime.subFS(dir)
	if !ok {
		return txtarCommandResult{failed: true, err: fmt.Errorf("migration directory %q missing", dir)}, true
	}
	sumPath := path.Join(dir, migratesum.AtlasFileName)
	expected, ok := runtime.files[sumPath]
	if !ok {
		return txtarCommandResult{
			stdout: "You have a checksum error in your migration directory.\n",
			stderr: "Error: checksum file not found\n",
			failed: true,
			err:    fmt.Errorf("checksum file not found"),
		}, true
	}
	actual, err := migratesum.ComputeWithFormat(fsys, migrator.MigrationDirFormatAtlas)
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	if string(actual.Bytes()) != expected {
		return txtarCommandResult{
			stderr: "Error: checksum mismatch\n",
			failed: true,
			err:    fmt.Errorf("checksum mismatch"),
		}, true
	}
	return txtarCommandResult{}, true
}

type txtarMigrateDiffArgs struct {
	devURL  string
	to      []string
	blocked bool
}

func runTxtarMigrateDiff(fields []string, expectedFailure bool) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "diff" {
		return txtarCommandResult{}, false
	}

	args := txtarParseMigrateDiffArgs(fields[3:])
	switch {
	case args.blocked:
		return txtarCommandResult{unsupported: "atlas migrate diff"}, true
	case expectedFailure && args.devURL == "":
		return txtarCommandResult{
			stderr: "Error: \"dev-url\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"dev-url\" not set"),
		}, true
	case expectedFailure && len(args.to) == 0:
		return txtarCommandResult{
			stderr: "Error: \"to\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"to\" not set"),
		}, true
	default:
		return txtarCommandResult{unsupported: "atlas migrate diff"}, true
	}
}

func txtarParseMigrateDiffArgs(args []string) txtarMigrateDiffArgs {
	var out txtarMigrateDiffArgs
	seenName := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dev-url":
			if i+1 < len(args) {
				out.devURL = args[i+1]
				i++
			}
		case "--to":
			if i+1 < len(args) {
				out.to = append(out.to, args[i+1])
				i++
			}
		case "--dir":
			if i+1 < len(args) {
				if strings.Contains(args[i+1], "?format=") {
					out.blocked = true
				}
				i++
			}
		case "--format":
			if i+1 < len(args) {
				i++
			}
		case "--qualifier", "--env", "--dir-format":
			out.blocked = true
			if i+1 < len(args) {
				i++
			}
		default:
			switch {
			case strings.HasPrefix(arg, "--dev-url="):
				out.devURL = strings.TrimPrefix(arg, "--dev-url=")
			case strings.HasPrefix(arg, "--to="):
				out.to = append(out.to, strings.TrimPrefix(arg, "--to="))
			case strings.HasPrefix(arg, "--dir="):
				if strings.Contains(arg, "?format=") {
					out.blocked = true
				}
			case strings.HasPrefix(arg, "--format="):
				continue
			case strings.HasPrefix(arg, "--qualifier="), strings.HasPrefix(arg, "--env="), strings.HasPrefix(arg, "--dir-format="):
				out.blocked = true
			case strings.HasPrefix(arg, "-"):
				out.blocked = true
			case !seenName:
				seenName = true
			default:
				out.blocked = true
			}
		}
	}
	return out
}

func runTxtarMigrateSet(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "set" {
		return txtarCommandResult{}, false
	}

	args := fields[3:]
	dir := txtarMigrateCommandDir(args)
	if _, ok := runtime.files[path.Join(dir, migratesum.AtlasFileName)]; !ok {
		return txtarCommandResult{
			stdout: "You have a checksum error in your migration directory.\n",
			stderr: "Error: checksum file not found\n",
			failed: true,
			err:    fmt.Errorf("checksum file not found"),
		}, true
	}

	revisions := txtarMigrateSetRevisions(args)
	if len(revisions) != 1 {
		return txtarCommandResult{
			stderr: fmt.Sprintf("Error: accepts 1 arg(s), received %d\n", len(revisions)),
			failed: true,
			err:    fmt.Errorf("accepts 1 arg(s), received %d", len(revisions)),
		}, true
	}

	return txtarCommandResult{unsupported: "atlas migrate set"}, true
}

func txtarMigrateSetRevisions(args []string) []string {
	var revisions []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--url", "--dir":
			if i+1 < len(args) {
				i++
			}
		default:
			switch {
			case strings.HasPrefix(arg, "--url="), strings.HasPrefix(arg, "--dir="):
				continue
			case strings.HasPrefix(arg, "-"):
				continue
			default:
				revisions = append(revisions, arg)
			}
		}
	}
	return revisions
}

func runTxtarMigrateStatus(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "status" {
		return txtarCommandResult{}, false
	}

	dir := txtarMigrateCommandDir(fields[3:])
	if _, ok := runtime.files[path.Join(dir, migratesum.AtlasFileName)]; !ok {
		return txtarCommandResult{unsupported: "atlas migrate status"}, true
	}
	files := txtarMigrationSQLFilesInDir(runtime, dir)
	if len(files) == 0 {
		return txtarCommandResult{unsupported: "atlas migrate status"}, true
	}
	nextVersion := atlasMigrationVersion(files[0])
	stdout := fmt.Sprintf(`Migration Status: PENDING
  -- Current Version: No migration applied yet
  -- Next Version:    %s
  -- Executed Files:  0
  -- Pending Files:   %d
`, nextVersion, len(files))
	return txtarCommandResult{stdout: stdout}, true
}

func atlasMigrationVersion(name string) string {
	base := strings.TrimSuffix(path.Base(name), ".sql")
	version, _, hasDescription := strings.Cut(base, "_")
	if hasDescription {
		return version
	}
	return base
}

func runTxtarSchemaApply(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "schema" || fields[2] != "apply" {
		return txtarCommandResult{}, false
	}

	var sourceURL, file, to string
	hasEnv := false
	for i := 3; i < len(fields); i++ {
		switch fields[i] {
		case "-u", "--url":
			if i+1 < len(fields) {
				sourceURL = fields[i+1]
				i++
			}
		case "-f", "--file":
			if i+1 < len(fields) {
				file = fields[i+1]
				i++
			}
		case "--to":
			if i+1 < len(fields) {
				to = fields[i+1]
				i++
			}
		case "--env":
			hasEnv = true
			if i+1 < len(fields) {
				i++
			}
		default:
			switch {
			case strings.HasPrefix(fields[i], "-u="):
				sourceURL = strings.TrimPrefix(fields[i], "-u=")
			case strings.HasPrefix(fields[i], "--url="):
				sourceURL = strings.TrimPrefix(fields[i], "--url=")
			case strings.HasPrefix(fields[i], "-f="):
				file = strings.TrimPrefix(fields[i], "-f=")
			case strings.HasPrefix(fields[i], "--file="):
				file = strings.TrimPrefix(fields[i], "--file=")
			case strings.HasPrefix(fields[i], "--to="):
				to = strings.TrimPrefix(fields[i], "--to=")
			case strings.HasPrefix(fields[i], "--env="):
				hasEnv = true
			}
		}
	}
	if hasEnv {
		return txtarCommandResult{unsupported: "atlas schema apply"}, true
	}
	if sourceURL == "" {
		return txtarCommandResult{
			stderr: "Error: \"url\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"url\" not set"),
		}, true
	}
	if file == "" && to == "" {
		return txtarCommandResult{
			stderr: "Error: one of flag(s) \"file\" or \"to\" is required\n",
			failed: true,
			err:    fmt.Errorf("one of flag(s) \"file\" or \"to\" is required"),
		}, true
	}
	if file != "" && txtarFileLooksLikeAtlasProject(runtime.files[file]) {
		return txtarCommandResult{
			stderr: "Error: cannot parse project file\n",
			failed: true,
			err:    fmt.Errorf("cannot parse project file"),
		}, true
	}
	return txtarCommandResult{unsupported: "atlas schema apply"}, true
}

func txtarFileLooksLikeAtlasProject(data string) bool {
	return strings.Contains(data, `env "`)
}

func txtarMigrateDiffDir(args []string) string {
	return txtarMigrateCommandDir(args)
}

func txtarMigrateCommandDir(args []string) string {
	const defaultDir = "migrations"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, ok := strings.CutPrefix(arg, "--dir="); ok {
			return txtarFileURLPath(value)
		}
		if arg == "--dir" && i+1 < len(args) {
			return txtarFileURLPath(args[i+1])
		}
	}
	return defaultDir
}

func txtarFileURLPath(value string) string {
	const filePrefix = "file://"
	value = strings.TrimPrefix(value, filePrefix)
	value, _, _ = strings.Cut(value, "?")
	return value
}

func txtarCommandReadsUnsupportedDBState(line string) bool {
	fields := txtarCommandFields(line)
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "apply", "execsql", "exist", "synced", "cmpshow", "cmphcl":
		return true
	case "atlas":
		return txtarAtlasCommandReadsUnsupportedDBState(fields)
	default:
		return false
	}
}

func txtarAtlasCommandReadsUnsupportedDBState(fields []string) bool {
	if len(fields) < 3 {
		return false
	}
	switch fields[1] + " " + fields[2] {
	case "migrate apply", "migrate set", "migrate status":
		return true
	case "schema apply", "schema clean":
		return true
	case "schema inspect":
		return txtarSchemaInspectReadsDBState(fields[3:])
	default:
		return false
	}
}

func txtarSchemaInspectReadsDBState(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-u" || arg == "--url":
			if i+1 >= len(args) {
				return false
			}
			return !strings.HasPrefix(args[i+1], "file://")
		case strings.HasPrefix(arg, "-u="):
			return !strings.HasPrefix(strings.TrimPrefix(arg, "-u="), "file://")
		case strings.HasPrefix(arg, "--url="):
			return !strings.HasPrefix(strings.TrimPrefix(arg, "--url="), "file://")
		case arg == "--env":
			return true
		case strings.HasPrefix(arg, "--env="):
			return true
		}
	}
	return false
}

func txtarCommandMutatesDBState(line string) bool {
	fields := txtarCommandFields(line)
	if len(fields) == 0 {
		return false
	}
	if slices.Contains(fields, "--dry-run") {
		return false
	}

	switch fields[0] {
	case "apply", "clearSchema", "execsql":
		return true
	case "atlas":
		return len(fields) >= 3 && txtarAtlasCommandMutatesDBState(fields[1], fields[2])
	default:
		return false
	}
}

func txtarAtlasCommandMutatesDBState(group, command string) bool {
	switch group + " " + command {
	case "migrate apply", "migrate set", "schema apply", "schema clean":
		return true
	default:
		return false
	}
}

func markUnsupportedFileCommandOutputs(line string, runtime *txtarRuntime, unsupportedFiles map[string]bool) {
	fields := txtarCommandFields(line)
	if len(fields) == 0 {
		return
	}

	switch fields[0] {
	case "cat":
		unsupportedFiles["stdout"] = true
		unsupportedFiles["stderr"] = true
	case "cp", "mv":
		args := nonFlagArgs(fields[1:])
		if len(args) == 2 {
			unsupportedFiles[runtime.destinationPath(args[0], args[1])] = true
		}
	case "atlas":
		if len(fields) < 3 || fields[1] != "migrate" {
			return
		}
		switch fields[2] {
		case "diff":
			unsupportedFiles[txtarMigrateDiffDir(fields[3:])] = true
		case "hash":
			unsupportedFiles[path.Join(txtarMigrateHashDir(fields[3:]), migratesum.AtlasFileName)] = true
		}
	}
}

func clearUnsupportedFileCommandOutputs(line string, runtime *txtarRuntime, unsupportedFiles map[string]bool) {
	fields := txtarCommandFields(line)
	if len(fields) == 0 {
		return
	}

	switch fields[0] {
	case "cp":
		args := nonFlagArgs(fields[1:])
		if len(args) == 2 {
			delete(unsupportedFiles, runtime.destinationPath(args[0], args[1]))
		}
	case "mv":
		args := nonFlagArgs(fields[1:])
		if len(args) == 2 {
			delete(unsupportedFiles, args[0])
			delete(unsupportedFiles, runtime.destinationPath(args[0], args[1]))
		}
	case "rm":
		for _, file := range nonFlagArgs(fields[1:]) {
			removeUnsupportedPath(unsupportedFiles, file)
		}
	case "touch":
		for _, file := range nonFlagArgs(fields[1:]) {
			delete(unsupportedFiles, file)
		}
	case "atlas":
		if len(fields) >= 3 && fields[1] == "migrate" && fields[2] == "hash" {
			delete(unsupportedFiles, path.Join(txtarMigrateHashDir(fields[3:]), migratesum.AtlasFileName))
		}
	}
}

func removeUnsupportedPath(unsupportedFiles map[string]bool, name string) {
	name = path.Clean(name)
	delete(unsupportedFiles, name)
	prefix := name + "/"
	for file := range unsupportedFiles {
		if strings.HasPrefix(file, prefix) {
			delete(unsupportedFiles, file)
		}
	}
}

func runTxtarFileCommand(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) == 0 {
		return txtarCommandResult{}, false
	}
	switch fields[0] {
	case "mkdir":
		return runtime.mkdir(fields[1:]), true
	case "rm":
		return runtime.rm(fields[1:]), true
	case "cp":
		return runtime.cp(fields[1:]), true
	case "mv":
		return runtime.mv(fields[1:]), true
	case "cat":
		return runtime.cat(fields[1:]), true
	case "touch":
		return runtime.touch(fields[1:]), true
	default:
		return txtarCommandResult{}, false
	}
}

func (r *txtarRuntime) mkdir(args []string) txtarCommandResult {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		r.addDir(arg)
	}
	return txtarCommandResult{}
}

func (r *txtarRuntime) rm(args []string) txtarCommandResult {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		r.removePath(arg)
	}
	return txtarCommandResult{}
}

func (r *txtarRuntime) cp(args []string) txtarCommandResult {
	args = nonFlagArgs(args)
	if len(args) != 2 {
		return txtarCommandResult{unsupported: "cp"}
	}
	src, dst := args[0], r.destinationPath(args[0], args[1])
	data, ok := r.files[src]
	if !ok {
		return txtarCommandResult{failed: true, err: fmt.Errorf("cp %s %s: %s missing", args[0], args[1], args[0])}
	}
	r.files[dst] = data
	r.addParentDirs(dst)
	return txtarCommandResult{}
}

func (r *txtarRuntime) mv(args []string) txtarCommandResult {
	args = nonFlagArgs(args)
	if len(args) != 2 {
		return txtarCommandResult{unsupported: "mv"}
	}
	src, dst := args[0], r.destinationPath(args[0], args[1])
	data, ok := r.files[src]
	if !ok {
		return txtarCommandResult{failed: true, err: fmt.Errorf("mv %s %s: %s missing", args[0], args[1], args[0])}
	}
	r.files[dst] = data
	r.addParentDirs(dst)
	delete(r.files, src)
	return txtarCommandResult{}
}

func (r *txtarRuntime) cat(args []string) txtarCommandResult {
	args = nonFlagArgs(args)
	if len(args) == 0 {
		return txtarCommandResult{unsupported: "cat"}
	}
	var stdout strings.Builder
	for _, file := range args {
		data, ok := r.files[file]
		if !ok {
			return txtarCommandResult{failed: true, err: fmt.Errorf("cat %s: %s missing", file, file)}
		}
		stdout.WriteString(data)
	}
	return txtarCommandResult{stdout: stdout.String()}
}

func (r *txtarRuntime) touch(args []string) txtarCommandResult {
	for _, file := range nonFlagArgs(args) {
		if _, ok := r.files[file]; !ok {
			r.files[file] = ""
		}
		r.addParentDirs(file)
	}
	return txtarCommandResult{}
}

func txtarCmpmigMismatch(runtime *txtarRuntime, index, expected string) string {
	actual, ok := txtarCmpmigActualFile(runtime, index)
	if !ok {
		return fmt.Sprintf("cmpmig %s %s: generated migration not found", index, expected)
	}
	return txtarFilesMismatch(runtime.files, actual, expected)
}

func txtarCmpmigActualFile(runtime *txtarRuntime, index string) (string, bool) {
	want, err := strconv.Atoi(index)
	if err != nil || want < 0 {
		return "", false
	}
	files := txtarMigrationSQLFiles(runtime)
	if want >= len(files) {
		return "", false
	}
	return files[want], true
}

func txtarMigrationSQLFiles(runtime *txtarRuntime) []string {
	return txtarMigrationSQLFilesInDir(runtime, "migrations")
}

func txtarMigrationSQLFilesInDir(runtime *txtarRuntime, dir string) []string {
	var files []string
	for name := range runtime.files {
		if txtarMigrateHashReadsFile(dir, name) {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	return files
}

func (r *txtarRuntime) subFS(dir string) (fstest.MapFS, bool) {
	dir = path.Clean(dir)
	prefix := ""
	if dir != "." && dir != "/" {
		prefix = dir + "/"
	}

	fsys := fstest.MapFS{}
	for name, data := range r.files {
		clean := path.Clean(name)
		rel := clean
		if prefix != "" {
			var ok bool
			rel, ok = strings.CutPrefix(clean, prefix)
			if !ok {
				continue
			}
		}
		if rel == "" || strings.Contains(rel, "/") {
			continue
		}
		fsys[rel] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys, r.dirs[dir] || len(fsys) > 0
}

func (r *txtarRuntime) destinationPath(src, dst string) string {
	if r.dirs[dst] || strings.HasSuffix(dst, "/") {
		return path.Join(dst, path.Base(src))
	}
	return dst
}

func (r *txtarRuntime) addDir(dir string) {
	dir = path.Clean(dir)
	if dir == "." || dir == "/" {
		return
	}
	for current := dir; current != "." && current != "/"; current = path.Dir(current) {
		r.dirs[current] = true
	}
}

func (r *txtarRuntime) addParentDirs(file string) {
	r.addDir(path.Dir(file))
}

func (r *txtarRuntime) removePath(name string) {
	name = path.Clean(name)
	delete(r.files, name)
	delete(r.dirs, name)
	prefix := name + "/"
	for file := range r.files {
		if strings.HasPrefix(file, prefix) {
			delete(r.files, file)
		}
	}
	for dir := range r.dirs {
		if strings.HasPrefix(dir, prefix) {
			delete(r.dirs, dir)
		}
	}
}

func nonFlagArgs(args []string) []string {
	var out []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func runTxtarSchemaInspect(fx Fixture, files map[string]string, fields []string) txtarCommandResult {
	var sourceURL, devURL, format, redirect string
	hasEnv := false
	for i := 3; i < len(fields); i++ {
		switch fields[i] {
		case "-u", "--url":
			if i+1 < len(fields) {
				sourceURL = fields[i+1]
				i++
			}
		case "--dev-url":
			if i+1 < len(fields) {
				devURL = fields[i+1]
				i++
			}
		case "--format":
			if i+1 < len(fields) {
				format = fields[i+1]
				i++
			}
		case ">":
			if i+1 < len(fields) {
				redirect = fields[i+1]
				i++
			}
		case "--env":
			hasEnv = true
			if i+1 < len(fields) {
				i++
			}
		default:
			switch {
			case strings.HasPrefix(fields[i], "-u="):
				sourceURL = strings.TrimPrefix(fields[i], "-u=")
			case strings.HasPrefix(fields[i], "--url="):
				sourceURL = strings.TrimPrefix(fields[i], "--url=")
			case strings.HasPrefix(fields[i], "--env="):
				hasEnv = true
			}
		}
	}
	const filePrefix = "file://"
	if sourceURL == "" && !hasEnv {
		return txtarCommandResult{
			stderr: "Error: \"url\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"url\" not set"),
		}
	}
	if !strings.HasPrefix(sourceURL, filePrefix) {
		return txtarCommandResult{unsupported: "atlas schema inspect db-url"}
	}
	if devURL == "" {
		return txtarCommandResult{
			stderr: "Error: --dev-url cannot be empty\n",
			failed: true,
			err:    fmt.Errorf("--dev-url cannot be empty"),
		}
	}
	name := strings.TrimPrefix(sourceURL, filePrefix)
	sql, ok := files[name]
	if !ok {
		return txtarCommandResult{err: fmt.Errorf("file %q not found in txtar archive", name)}
	}

	var output string
	var err error
	switch format {
	case "":
		output, err = renderTxtarHCL(fx, name, sql)
		if errors.Is(err, errUnsupportedInspectHCL) {
			return txtarCommandResult{unsupported: "atlas schema inspect hcl"}
		}
	case "{{ sql . }}", `{{ sql . "  " }}`:
		output, err = renderTxtarSQL(fx, sql, txtarSQLFormatIndent(format))
		if errors.Is(err, errUnsupportedInspectSQL) {
			return txtarCommandResult{unsupported: "atlas schema inspect sql"}
		}
	default:
		return txtarCommandResult{unsupported: "atlas schema inspect format"}
	}
	if err != nil {
		return txtarCommandResult{err: err}
	}
	if redirect != "" {
		files[redirect] = output
		return txtarCommandResult{}
	}
	return txtarCommandResult{stdout: output}
}

var errUnsupportedInspectHCL = errors.New("unsupported inspect HCL")
var errUnsupportedInspectSQL = errors.New("unsupported inspect SQL")

func renderTxtarHCL(fx Fixture, name, data string) (string, error) {
	if strings.HasSuffix(name, ".hcl") {
		return renderTxtarHCLFromAtlasHCL(fx, name, data)
	}
	return renderTxtarHCLFromSQL(fx, data)
}

func renderTxtarHCLFromSQL(fx Fixture, sql string) (string, error) {
	list, err := parser.NewParser(sql).Parse()
	if err != nil {
		return "", fmt.Errorf("%w: parse inspect file: %v", errUnsupportedInspectHCL, err)
	}
	out, err := renderAtlasInspectHCL(txtarFixtureDialect(fx), txtarFixtureSchemaName(fx), list.Statements)
	if err != nil {
		return "", fmt.Errorf("render inspect HCL: %w", err)
	}
	return out, nil
}

func renderTxtarHCLFromAtlasHCL(fx Fixture, name, data string) (string, error) {
	db, err := atlashcl.Parse([]byte(data), name)
	if err != nil {
		return "", fmt.Errorf("%w: parse inspect HCL file: %v", errUnsupportedInspectHCL, err)
	}
	list := fromschema.FromDatabase(*db, txtarFixtureDialect(fx))
	out, err := renderAtlasInspectHCL(txtarFixtureDialect(fx), txtarFixtureSchemaName(fx), list.Statements)
	if err != nil {
		return "", fmt.Errorf("render inspect HCL: %w", err)
	}
	return out, nil
}

func renderTxtarSQL(fx Fixture, sql string, indent string) (string, error) {
	list, err := parser.NewParser(sql).Parse()
	if err != nil {
		return "", fmt.Errorf("parse inspect file: %w", err)
	}
	out, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), list.Statements, indent)
	if err != nil {
		return "", fmt.Errorf("render inspect SQL: %w", err)
	}
	return out, nil
}

func txtarSQLFormatIndent(format string) string {
	if format == `{{ sql . "  " }}` {
		return "  "
	}
	return ""
}

func renderAtlasInspectHCL(dialect, schemaName string, statements []ast.Node) (string, error) {
	var b strings.Builder
	schemaAttrs := atlasSchemaAttrsFromStatements(dialect, schemaName, statements)
	var tables []*ast.CreateTableNode
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			if _, ok := stmt.(*ast.CreateSchemaNode); ok {
				continue
			}
			return "", fmt.Errorf("%w: statement %T", errUnsupportedInspectHCL, stmt)
		}
		tables = append(tables, table)
	}
	slices.SortFunc(tables, func(a, b *ast.CreateTableNode) int {
		return cmp.Compare(atlasHCLTableIdentifier(a.Name, schemaName), atlasHCLTableIdentifier(b.Name, schemaName))
	})
	for _, table := range tables {
		if err := renderAtlasTableHCL(&b, dialect, schemaName, table); err != nil {
			return "", err
		}
	}
	renderAtlasSchemaHCL(&b, schemaName, schemaAttrs)
	return b.String(), nil
}

func renderAtlasTableHCL(b *strings.Builder, dialect, schemaName string, table *ast.CreateTableNode) error {
	tableName := atlasHCLTableIdentifier(table.Name, schemaName)
	fmt.Fprintf(b, "table %q {\n", tableName)
	fmt.Fprintf(b, "  schema = schema.%s\n", schemaName)
	var primaryColumns []ast.ConstraintColumn
	var foreignKeys []*atlasHCLForeignKey
	var uniques []*atlasHCLUnique
	for _, column := range table.Columns {
		fmt.Fprintf(b, "  column %q {\n", atlasHCLIdentifier(column.Name))
		fmt.Fprintf(b, "    null = %t\n", column.Nullable)
		fmt.Fprintf(b, "    type = %s\n", atlasColumnType(dialect, column.Type))
		b.WriteString("  }\n")
		if column.Primary {
			primaryColumns = append(primaryColumns, ast.ConstraintColumn{Name: column.Name})
		}
		if column.ForeignKey != nil {
			foreignKeys = append(foreignKeys, atlasColumnForeignKey(tableName, column))
		}
		if column.Unique {
			uniques = append(uniques, atlasColumnUnique(tableName, column))
		}
	}
	for _, constraint := range table.Constraints {
		switch constraint.Type {
		case ast.PrimaryKeyConstraint:
			primaryColumns = append(primaryColumns, atlasConstraintColumns(constraint)...)
		case ast.ForeignKeyConstraint:
			foreignKey, err := atlasConstraintForeignKey(tableName, schemaName, constraint)
			if err != nil {
				return err
			}
			foreignKeys = append(foreignKeys, foreignKey)
		case ast.UniqueConstraint:
			unique, err := atlasConstraintUnique(tableName, constraint)
			if err != nil {
				return err
			}
			uniques = append(uniques, unique)
		case ast.CheckConstraint:
		default:
			return fmt.Errorf("%w: constraint %s", errUnsupportedInspectHCL, constraint.Type)
		}
	}
	if len(primaryColumns) > 0 {
		if err := renderAtlasPrimaryKeyHCL(b, primaryColumns); err != nil {
			return err
		}
	}
	for _, foreignKey := range foreignKeys {
		if err := renderAtlasForeignKeyHCL(b, foreignKey); err != nil {
			return err
		}
	}
	for _, unique := range uniques {
		if err := renderAtlasUniqueHCL(b, unique); err != nil {
			return err
		}
	}
	if err := renderAtlasCheckHCLBlocks(b, dialect, table); err != nil {
		return err
	}
	b.WriteString("}\n")
	return nil
}

type atlasHCLForeignKey struct {
	name       string
	columns    []string
	refTable   string
	refColumns []string
	onUpdate   string
	onDelete   string
}

type atlasHCLUnique struct {
	name    string
	columns []string
}

func atlasColumnForeignKey(tableName string, column *ast.ColumnNode) *atlasHCLForeignKey {
	ref := column.ForeignKey
	return &atlasHCLForeignKey{
		name:       atlasDefaultForeignKeyName(tableName, []string{column.Name}, ref.Name),
		columns:    []string{column.Name},
		refTable:   atlasSQLIdentifier(ref.Table),
		refColumns: []string{ref.Column},
		onUpdate:   ref.OnUpdate,
		onDelete:   ref.OnDelete,
	}
}

func atlasConstraintForeignKey(tableName, schemaName string, constraint *ast.ConstraintNode) (*atlasHCLForeignKey, error) {
	if constraint.Reference == nil {
		return nil, fmt.Errorf("%w: foreign key %q missing reference", errUnsupportedInspectHCL, constraint.Name)
	}
	columns := atlasConstraintColumnNames(constraint)
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: foreign key %q missing columns", errUnsupportedInspectHCL, constraint.Name)
	}
	ref := constraint.Reference
	return &atlasHCLForeignKey{
		name:       atlasDefaultForeignKeyName(tableName, columns, constraint.Name),
		columns:    columns,
		refTable:   atlasHCLTableIdentifier(ref.Table, schemaName),
		refColumns: []string{ref.Column},
		onUpdate:   ref.OnUpdate,
		onDelete:   ref.OnDelete,
	}, nil
}

func atlasColumnUnique(tableName string, column *ast.ColumnNode) *atlasHCLUnique {
	return &atlasHCLUnique{
		name:    atlasDefaultUniqueName(tableName, []string{column.Name}, ""),
		columns: []string{column.Name},
	}
}

func atlasConstraintUnique(tableName string, constraint *ast.ConstraintNode) (*atlasHCLUnique, error) {
	columns := atlasConstraintColumnNames(constraint)
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: unique %q missing columns", errUnsupportedInspectHCL, constraint.Name)
	}
	return &atlasHCLUnique{
		name:    atlasDefaultUniqueName(tableName, columns, constraint.Name),
		columns: columns,
	}, nil
}

func renderAtlasCheckHCLBlocks(b *strings.Builder, dialect string, table *ast.CreateTableNode) error {
	if !atlasSupportsInspectChecks(dialect) && atlasTableHasChecks(table) {
		return fmt.Errorf("%w: check constraints", errUnsupportedInspectHCL)
	}
	checks, err := atlasCheckBlocks(dialect, table, errUnsupportedInspectHCL)
	if err != nil {
		return err
	}
	for _, check := range checks {
		renderAtlasCheckHCLBlock(b, check.name, atlasCheckHCLExpr(dialect, check.expr))
	}
	return nil
}

func renderAtlasCheckHCLBlock(b *strings.Builder, name, expr string) {
	fmt.Fprintf(b, "  check %q {\n", name)
	fmt.Fprintf(b, "    expr = %q\n", expr)
	b.WriteString("  }\n")
}

type atlasCheckBlock struct {
	name string
	expr string
}

func atlasCheckBlocks(dialect string, table *ast.CreateTableNode, unsupported error) ([]atlasCheckBlock, error) {
	var checks []atlasCheckBlock
	unnamedColumnChecks := 0
	for _, column := range table.Columns {
		if column.Check == "" {
			continue
		}
		unnamedColumnChecks++
		name, err := atlasColumnCheckName(dialect, table.Name, column, unnamedColumnChecks, unsupported)
		if err != nil {
			return nil, err
		}
		checks = append(checks, atlasCheckBlock{name: name, expr: atlasNormalizeCheckExpr(dialect, column.Check)})
	}
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.CheckConstraint {
			continue
		}
		if constraint.Name == "" {
			return nil, fmt.Errorf("%w: unnamed table check", unsupported)
		}
		checks = append(checks, atlasCheckBlock{
			name: atlasSQLIdentifier(constraint.Name),
			expr: atlasNormalizeCheckExpr(dialect, constraint.Expression),
		})
	}
	slices.SortFunc(checks, func(a, b atlasCheckBlock) int {
		return cmp.Compare(a.name, b.name)
	})
	return checks, nil
}

func atlasColumnCheckName(
	dialect string,
	tableName string,
	column *ast.ColumnNode,
	unnamedPosition int,
	unsupported error,
) (string, error) {
	if column.CheckName != "" {
		return atlasSQLIdentifier(column.CheckName), nil
	}
	switch dialect {
	case "mysql":
		return fmt.Sprintf("%s_chk_%d", atlasSQLIdentifier(tableName), unnamedPosition), nil
	case "mariadb":
		return atlasSQLIdentifier(column.Name), nil
	default:
		return "", fmt.Errorf("%w: unnamed column check %q", unsupported, column.Name)
	}
}

func atlasCheckHCLExpr(dialect, expr string) string {
	if dialect == "mariadb" {
		return expr
	}
	return "(" + expr + ")"
}

func atlasNormalizeCheckExpr(dialect, expr string) string {
	expr = strings.TrimSpace(expr)
	switch dialect {
	case "mysql", "mariadb":
		expr = atlasQuoteCheckIdentifiers(dialect, expr)
		expr = strings.ReplaceAll(expr, ", ", ",")
		if dialect == "mysql" {
			expr = atlasParenthesizeMySQLCheckOr(expr)
		}
		return expr
	default:
		return expr
	}
}

func atlasQuoteCheckIdentifiers(dialect, expr string) string {
	var b strings.Builder
	for i := 0; i < len(expr); {
		ch := expr[i]
		switch {
		case ch == '\'':
			if dialect == "mysql" {
				b.WriteString("_utf8mb4")
			}
			i = atlasCopySQLStringLiteral(&b, expr, i)
		case ch == '`':
			i = atlasCopyQuotedIdentifier(&b, expr, i)
		case isAtlasCheckIdentStart(ch):
			start := i
			for i < len(expr) && isAtlasCheckIdentPart(expr[i]) {
				i++
			}
			token := expr[start:i]
			if atlasCheckKeyword(token) {
				b.WriteString(strings.ToLower(token))
			} else {
				fmt.Fprintf(&b, "`%s`", strings.ReplaceAll(atlasSQLIdentifier(token), "`", "``"))
			}
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}

func atlasCopySQLStringLiteral(b *strings.Builder, expr string, start int) int {
	b.WriteByte(expr[start])
	i := start + 1
	for i < len(expr) {
		b.WriteByte(expr[i])
		if expr[i] == '\'' {
			i++
			if i < len(expr) && expr[i] == '\'' {
				b.WriteByte(expr[i])
				i++
				continue
			}
			break
		}
		i++
	}
	return i
}

func atlasCopyQuotedIdentifier(b *strings.Builder, expr string, start int) int {
	i := start
	for i < len(expr) {
		b.WriteByte(expr[i])
		if expr[i] == '`' {
			i++
			break
		}
		i++
	}
	return i
}

func isAtlasCheckIdentStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isAtlasCheckIdentPart(ch byte) bool {
	return isAtlasCheckIdentStart(ch) || ch >= '0' && ch <= '9'
}

func atlasCheckKeyword(token string) bool {
	switch strings.ToLower(token) {
	case "and", "between", "false", "in", "is", "like", "not", "null", "or", "true":
		return true
	default:
		return false
	}
}

func atlasParenthesizeMySQLCheckOr(expr string) string {
	parts := strings.Split(expr, " or ")
	if len(parts) == 1 {
		return expr
	}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "(") || !strings.HasSuffix(part, ")") {
			part = "(" + part + ")"
		}
		parts[i] = part
	}
	return strings.Join(parts, " or ")
}

type atlasSchemaAttrs struct {
	charset string
	collate string
}

func atlasSchemaAttrsFromStatements(dialect, schemaName string, statements []ast.Node) atlasSchemaAttrs {
	attrs := atlasDefaultSchemaAttrs(dialect)
	for _, stmt := range statements {
		schema, ok := stmt.(*ast.CreateSchemaNode)
		if !ok || atlasSQLIdentifier(schema.Name) != schemaName {
			continue
		}
		if schema.Charset != "" {
			attrs.charset = schema.Charset
		}
		if schema.Collate != "" {
			attrs.collate = schema.Collate
		}
	}
	return attrs
}

func atlasDefaultSchemaAttrs(dialect string) atlasSchemaAttrs {
	switch dialect {
	case "mysql":
		return atlasSchemaAttrs{charset: "utf8mb4", collate: "utf8mb4_0900_ai_ci"}
	case "mariadb":
		return atlasSchemaAttrs{charset: "utf8mb4", collate: "utf8mb4_general_ci"}
	default:
		return atlasSchemaAttrs{}
	}
}

func renderAtlasSchemaHCL(b *strings.Builder, schemaName string, attrs atlasSchemaAttrs) {
	fmt.Fprintf(b, "schema %q {\n", schemaName)
	if attrs.charset != "" {
		fmt.Fprintf(b, "  charset = %q\n", attrs.charset)
	}
	if attrs.collate != "" {
		fmt.Fprintf(b, "  collate = %q\n", attrs.collate)
	}
	b.WriteString("}\n")
}

func renderAtlasPrimaryKeyHCL(b *strings.Builder, columns []ast.ConstraintColumn) error {
	if atlasPrimaryKeyCanUseColumnsAttr(columns) {
		refs, err := atlasHCLColumnRefs(columns)
		if err != nil {
			return err
		}
		b.WriteString("  primary_key {\n")
		fmt.Fprintf(b, "    columns = [%s]\n", strings.Join(refs, ", "))
		b.WriteString("  }\n")
		return nil
	}

	b.WriteString("  primary_key {\n")
	for _, column := range columns {
		name := atlasHCLIdentifier(column.Name)
		if strings.ContainsAny(name, " ()`\"") {
			return fmt.Errorf("%w: primary key column %q", errUnsupportedInspectHCL, column.Name)
		}
		b.WriteString("    on {\n")
		if column.Desc {
			b.WriteString("      desc   = true\n")
		}
		fmt.Fprintf(b, "      column = column.%s\n", name)
		if column.Prefix != "" {
			fmt.Fprintf(b, "      prefix = %s\n", column.Prefix)
		}
		b.WriteString("    }\n")
	}
	b.WriteString("  }\n")
	return nil
}

func renderAtlasForeignKeyHCL(b *strings.Builder, foreignKey *atlasHCLForeignKey) error {
	columnRefs, err := atlasHCLColumnRefsFromNames(foreignKey.columns)
	if err != nil {
		return err
	}
	refColumnRefs, err := atlasHCLRefColumnRefs(foreignKey.refTable, foreignKey.refColumns)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "  foreign_key %q {\n", foreignKey.name)
	fmt.Fprintf(b, "    columns     = [%s]\n", strings.Join(columnRefs, ", "))
	fmt.Fprintf(b, "    ref_columns = [%s]\n", strings.Join(refColumnRefs, ", "))
	fmt.Fprintf(b, "    on_update   = %s\n", atlasHCLAction(foreignKey.onUpdate))
	fmt.Fprintf(b, "    on_delete   = %s\n", atlasHCLAction(foreignKey.onDelete))
	b.WriteString("  }\n")
	return nil
}

func renderAtlasUniqueHCL(b *strings.Builder, unique *atlasHCLUnique) error {
	columnRefs, err := atlasHCLColumnRefsFromNames(unique.columns)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "  unique %q {\n", unique.name)
	fmt.Fprintf(b, "    columns = [%s]\n", strings.Join(columnRefs, ", "))
	b.WriteString("  }\n")
	return nil
}

func atlasPrimaryKeyCanUseColumnsAttr(columns []ast.ConstraintColumn) bool {
	for _, column := range columns {
		if column.Prefix != "" || column.Desc {
			return false
		}
	}
	return true
}

func atlasHCLColumnRefs(columns []ast.ConstraintColumn) ([]string, error) {
	refs := make([]string, 0, len(columns))
	for _, column := range columns {
		name := atlasHCLIdentifier(column.Name)
		if strings.ContainsAny(name, " ()`\"") {
			return nil, fmt.Errorf("%w: primary key column %q", errUnsupportedInspectHCL, column.Name)
		}
		refs = append(refs, "column."+name)
	}
	return refs, nil
}

func atlasHCLColumnRefsFromNames(columns []string) ([]string, error) {
	refs := make([]string, 0, len(columns))
	for _, column := range columns {
		name := atlasHCLIdentifier(column)
		if strings.ContainsAny(name, " ()`\"") {
			return nil, fmt.Errorf("%w: column %q", errUnsupportedInspectHCL, column)
		}
		refs = append(refs, "column."+name)
	}
	return refs, nil
}

func atlasHCLRefColumnRefs(table string, columns []string) ([]string, error) {
	table = atlasHCLIdentifier(table)
	if strings.ContainsAny(table, " ()`\"") {
		return nil, fmt.Errorf("%w: referenced table %q", errUnsupportedInspectHCL, table)
	}
	refs := make([]string, 0, len(columns))
	for _, column := range columns {
		name := atlasHCLIdentifier(column)
		if strings.ContainsAny(name, " ()`\"") {
			return nil, fmt.Errorf("%w: referenced column %q", errUnsupportedInspectHCL, column)
		}
		refs = append(refs, "table."+table+".column."+name)
	}
	return refs, nil
}

func atlasHCLAction(action string) string {
	if action == "" {
		return "NO_ACTION"
	}
	return strings.ReplaceAll(strings.ToUpper(action), " ", "_")
}

func atlasConstraintColumns(constraint *ast.ConstraintNode) []ast.ConstraintColumn {
	if len(constraint.ColumnParts) > 0 {
		return constraint.ColumnParts
	}
	columns := make([]ast.ConstraintColumn, 0, len(constraint.Columns))
	for _, column := range constraint.Columns {
		columns = append(columns, ast.ConstraintColumn{Name: column})
	}
	return columns
}

func atlasConstraintColumnNames(constraint *ast.ConstraintNode) []string {
	if len(constraint.ColumnParts) > 0 {
		columns := make([]string, 0, len(constraint.ColumnParts))
		for _, part := range constraint.ColumnParts {
			columns = append(columns, part.Name)
		}
		return columns
	}
	return slices.Clone(constraint.Columns)
}

func atlasDefaultForeignKeyName(table string, columns []string, explicit string) string {
	if explicit != "" {
		return atlasHCLIdentifier(explicit)
	}
	return atlasDefaultConstraintName(table, columns, "fkey")
}

func atlasDefaultUniqueName(table string, columns []string, explicit string) string {
	if explicit != "" {
		return atlasHCLIdentifier(explicit)
	}
	return atlasDefaultConstraintName(table, columns, "key")
}

func atlasDefaultConstraintName(table string, columns []string, suffix string) string {
	parts := make([]string, 0, len(columns)+2)
	parts = append(parts, atlasHCLIdentifier(table))
	for _, column := range columns {
		parts = append(parts, atlasHCLIdentifier(column))
	}
	parts = append(parts, suffix)
	return strings.Join(parts, "_")
}

func atlasHCLIdentifier(name string) string {
	return atlasSQLIdentifier(name)
}

func atlasHCLTableIdentifier(name, schemaName string) string {
	name = atlasHCLIdentifier(name)
	if unqualified, ok := strings.CutPrefix(name, schemaName+"."); ok {
		return unqualified
	}
	return name
}

func atlasSQLIdentifier(name string) string {
	if len(name) >= 2 {
		switch {
		case strings.HasPrefix(name, "`") && strings.HasSuffix(name, "`"):
			return strings.TrimSuffix(strings.TrimPrefix(name, "`"), "`")
		case strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`):
			return strings.TrimSuffix(strings.TrimPrefix(name, `"`), `"`)
		}
	}
	return name
}

func renderAtlasInspectSQL(dialect string, statements []ast.Node, indent string) (string, error) {
	tableNames := atlasTableNames(statements)
	indexesByTable := atlasIndexesByTable(dialect, statements)
	var b strings.Builder
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateTableNode:
			if err := renderAtlasCreateTableSQL(&b, dialect, node, indexesByTable[node.Name], indent); err != nil {
				return "", err
			}
		case *ast.IndexNode:
			if !atlasSupportsInlineIndexes(dialect) {
				return "", fmt.Errorf("unsupported inspect statement %T", stmt)
			}
			if !tableNames[node.Table] {
				return "", fmt.Errorf("unsupported inspect statement %T without matching table", stmt)
			}
		default:
			return "", fmt.Errorf("unsupported inspect statement %T", stmt)
		}
	}
	return b.String(), nil
}

func atlasTableNames(statements []ast.Node) map[string]bool {
	names := make(map[string]bool)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok {
			names[table.Name] = true
		}
	}
	return names
}

func atlasIndexesByTable(dialect string, statements []ast.Node) map[string][]*ast.IndexNode {
	if !atlasSupportsInlineIndexes(dialect) {
		return nil
	}
	indexes := make(map[string][]*ast.IndexNode)
	for _, stmt := range statements {
		index, ok := stmt.(*ast.IndexNode)
		if ok {
			indexes[index.Table] = append(indexes[index.Table], index)
		}
	}
	return indexes
}

func atlasSupportsInlineIndexes(dialect string) bool {
	return dialect == "mysql" || dialect == "mariadb"
}

func renderAtlasCreateTableSQL(
	b *strings.Builder,
	dialect string,
	table *ast.CreateTableNode,
	indexes []*ast.IndexNode,
	indent string,
) error {
	if !atlasSupportsInspectChecks(dialect) && atlasTableHasChecks(table) {
		return fmt.Errorf("%w: check constraints", errUnsupportedInspectSQL)
	}
	quote := atlasIdentifierQuoter(dialect)
	fmt.Fprintf(b, "-- Create %q table\n", atlasSQLIdentifier(table.Name))

	parts := make([]string, 0, len(table.Columns)+len(table.Constraints)+len(indexes))
	for _, column := range table.Columns {
		parts = append(parts, renderAtlasColumnSQL(dialect, quote, column, indent != ""))
		if column.Primary {
			parts = append(parts, renderAtlasPrimaryKeySQL(quote, []ast.ConstraintColumn{{Name: column.Name}}))
		}
	}
	checks, err := atlasCheckSQLParts(dialect, quote, table)
	if err != nil {
		return err
	}
	parts = append(parts, checks...)
	for _, constraint := range table.Constraints {
		switch constraint.Type {
		case ast.PrimaryKeyConstraint:
			parts = append(parts, renderAtlasPrimaryKeySQL(quote, atlasConstraintColumns(constraint)))
		case ast.CheckConstraint:
			continue
		default:
			return fmt.Errorf("unsupported inspect constraint %s", constraint.Type)
		}
	}
	for _, index := range indexes {
		parts = append(parts, renderAtlasIndexSQL(quote, index))
	}

	fmt.Fprintf(b, "CREATE TABLE %s (", quote(table.Name))
	if indent == "" {
		b.WriteString(strings.Join(parts, ", "))
	} else {
		b.WriteString("\n")
		for i, part := range parts {
			b.WriteString(indent)
			b.WriteString(part)
			if i < len(parts)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString(")")
	if attrs := atlasDefaultSchemaAttrs(dialect); attrs.charset != "" || attrs.collate != "" {
		if attrs.charset != "" {
			fmt.Fprintf(b, " CHARSET %s", attrs.charset)
		}
		if attrs.collate != "" {
			fmt.Fprintf(b, " COLLATE %s", attrs.collate)
		}
	}
	b.WriteString(";\n")
	return nil
}

func renderAtlasColumnSQL(dialect string, quote func(string) string, column *ast.ColumnNode, explicitNull bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", quote(column.Name), atlasColumnType(dialect, column.Type))
	if !column.Nullable {
		b.WriteString(" NOT NULL")
	} else if explicitNull {
		b.WriteString(" NULL")
	}
	return b.String()
}

func renderAtlasCheckSQL(quote func(string) string, name, expr string) string {
	return fmt.Sprintf("CONSTRAINT %s CHECK (%s)", quote(name), expr)
}

func atlasCheckSQLParts(dialect string, quote func(string) string, table *ast.CreateTableNode) ([]string, error) {
	checks, err := atlasCheckBlocks(dialect, table, errUnsupportedInspectSQL)
	if err != nil {
		return nil, err
	}
	parts := make([]string, 0, len(checks))
	for _, check := range checks {
		parts = append(parts, renderAtlasCheckSQL(quote, check.name, check.expr))
	}
	return parts, nil
}

func atlasTableHasChecks(table *ast.CreateTableNode) bool {
	for _, column := range table.Columns {
		if column.Check != "" {
			return true
		}
	}
	for _, constraint := range table.Constraints {
		if constraint.Type == ast.CheckConstraint {
			return true
		}
	}
	return false
}

func atlasSupportsInspectChecks(dialect string) bool {
	return dialect == "postgresql" || dialect == "mysql" || dialect == "mariadb"
}

func renderAtlasIndexSQL(quote func(string) string, index *ast.IndexNode) string {
	var b strings.Builder
	if index.Unique {
		b.WriteString("UNIQUE ")
	}
	if index.Type != "" {
		b.WriteString(strings.ToUpper(index.Type))
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "INDEX %s (", quote(index.Name))

	quoted := make([]string, 0, len(index.Columns))
	for _, column := range index.Columns {
		quoted = append(quoted, quote(column))
	}
	b.WriteString(strings.Join(quoted, ", "))
	b.WriteString(")")
	return b.String()
}

func renderAtlasPrimaryKeySQL(quote func(string) string, columns []ast.ConstraintColumn) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		part := quote(column.Name)
		if column.Prefix != "" {
			part += " (" + column.Prefix + ")"
		}
		if column.Desc {
			part += " DESC"
		}
		quoted = append(quoted, part)
	}
	return "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"
}

func atlasColumnType(dialect, typ string) string {
	normalized := strings.ToLower(typ)
	if dialect == "postgresql" && normalized == "int" {
		return "integer"
	}
	return normalized
}

func atlasIdentifierQuoter(dialect string) func(string) string {
	if dialect == "mysql" || dialect == "mariadb" {
		return func(name string) string {
			normalized := atlasSQLIdentifier(name)
			return "`" + strings.ReplaceAll(normalized, "`", "``") + "`"
		}
	}
	return func(name string) string {
		normalized := atlasSQLIdentifier(name)
		return `"` + strings.ReplaceAll(normalized, `"`, `""`) + `"`
	}
}

func txtarFixtureDialect(fx Fixture) string {
	parts := strings.Split(filepath.ToSlash(fx.Name), "/")
	if strings.Contains(strings.ToLower(fx.Name), "maria") {
		return "mariadb"
	}
	if slices.Contains(parts, "mysql") {
		return "mysql"
	}
	if slices.Contains(parts, "mariadb") {
		return "mariadb"
	}
	return "postgresql"
}

func txtarFixtureSchemaName(fx Fixture) string {
	base := strings.TrimSuffix(filepath.Base(fx.Name), filepath.Ext(fx.Name))
	return "script_" + strings.ReplaceAll(base, "-", "_")
}

func txtarFixtureFamily(fx Fixture) string {
	parts := strings.Split(filepath.ToSlash(fx.Name), "/")
	for _, family := range []string{"mysql", "mariadb", "postgres", "sqlite"} {
		if slices.Contains(parts, family) {
			return family
		}
	}
	return "postgres"
}

func txtarFailureDetail(result txtarCommandResult) string {
	if result.err != nil {
		return oneLine(result.err.Error())
	}
	if result.stderr != "" {
		return oneLine(result.stderr)
	}
	return "command failed"
}

func txtarFiles(data string) map[string]string {
	files := map[string]string{}
	var name string
	var b strings.Builder
	flush := func() {
		if name != "" {
			files[name] = b.String()
			b.Reset()
		}
	}
	for _, line := range strings.SplitAfter(data, "\n") {
		marker := strings.TrimSuffix(line, "\n")
		marker = strings.TrimSuffix(marker, "\r")
		if isTxtarFileMarker(marker) {
			flush()
			name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(marker), "-- "), " --"))
			continue
		}
		if name != "" {
			b.WriteString(line)
		}
	}
	flush()
	return files
}

func txtarExpectedFailure(line string) (bool, string) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "! ") {
		return true, strings.TrimSpace(strings.TrimPrefix(line, "! "))
	}
	return false, line
}

func txtarAssertionText(line string) string {
	fields := splitTxtarFields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

func txtarAssertionMatches(stream, line string) (bool, error) {
	pattern := txtarAssertionText(line)
	if pattern == "" {
		return stream == "", nil
	}
	return regexp.MatchString(pattern, stream)
}

func txtarFilesMismatch(files map[string]string, left, right string) string {
	l, lok := files[left]
	r, rok := files[right]
	switch {
	case !lok:
		return fmt.Sprintf("cmp %s %s did not match: %s missing", left, right, left)
	case !rok:
		return fmt.Sprintf("cmp %s %s did not match: %s missing", left, right, right)
	case !txtarFilesEqual(l, r):
		return fmt.Sprintf("cmp %s %s did not match: got %q want %q", left, right, oneLine(l), oneLine(r))
	default:
		return ""
	}
}

func txtarFilesEqual(left, right string) bool {
	if left == right {
		return true
	}
	// Atlas txtar fixtures sometimes store the final file at EOF without the
	// trailing newline that the inspected text renderer emits. Treat exactly one
	// final newline as non-substantive; all other byte differences still fail.
	return strings.TrimSuffix(left, "\n") == strings.TrimSuffix(right, "\n")
}

func txtarValidateJSON(files map[string]string, name string) error {
	data, ok := files[name]
	if !ok {
		return fmt.Errorf("validJSON %s: %s missing", name, name)
	}
	if !json.Valid([]byte(data)) {
		return fmt.Errorf("validJSON %s: invalid JSON", name)
	}
	return nil
}

func unsupportedOnlyDirective(fx Fixture, line string) string {
	fields := splitTxtarFields(strings.TrimPrefix(strings.TrimSpace(line), "! "))
	if len(fields) < 2 {
		return "only"
	}
	for _, condition := range fields[1:] {
		if txtarOnlyConditionApplies(fx, condition) {
			return ""
		}
	}
	return "only " + strings.Join(fields[1:], " ")
}

func txtarOnlyConditionApplies(fx Fixture, condition string) bool {
	condition = strings.ToLower(strings.TrimSpace(condition))
	family := txtarFixtureFamily(fx)
	switch {
	case condition == family:
		return true
	case strings.HasPrefix(condition, "mysql"):
		return family == "mysql" && !strings.Contains(strings.ToLower(fx.Name), "maria")
	case strings.HasPrefix(condition, "maria"):
		return family == "mysql" || family == "mariadb"
	case strings.HasPrefix(condition, "postgres"):
		return family == "postgres"
	case strings.HasPrefix(condition, "sqlite"):
		return family == "sqlite"
	default:
		return false
	}
}

func txtarDirectiveKey(line string) string {
	fields := splitTxtarFields(strings.TrimPrefix(strings.TrimSpace(line), "! "))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 1 {
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

func txtarRedirectTarget(line string) string {
	fields := splitTxtarFields(line)
	for i, field := range fields {
		if field == ">" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func splitTxtarFields(line string) []string {
	var fields []string
	var b strings.Builder
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		fields = append(fields, b.String())
	}
	return fields
}

func limitStrings(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return append(append([]string(nil), values[:n]...), fmt.Sprintf("... %d more", len(values)-n))
}
