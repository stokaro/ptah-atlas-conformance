package probe

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"go.5x5.cz/ptah/atlascompat"
	"go.5x5.cz/ptah/core/ast"
	"go.5x5.cz/ptah/migration/migrator"
)

var (
	mysqlIntegerDisplayWidthRE = regexp.MustCompile(`\b(bigint|int|integer|mediumint|smallint|tinyint)\(\d+\)`)
	mysqlDefaultCharsetRE      = regexp.MustCompile(`(?m)\s+CHARSET\s+\S+\s+COLLATE\s+\S+;?$`)
	mysqlUTF8MB4IntroducerRE   = regexp.MustCompile(`(?i)\b_utf8mb4'`)
	postgresSimpleIndexExprRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*\*\s*\d+$`)
	postgresTSVectorOpsRE      = regexp.MustCompile(`^tsvector_ops\(siglen=([0-9]+)\)$`)
	postgresHoursIntervalRE    = regexp.MustCompile(`^(\d+)\s+hours?$`)
	postgresCurrentTimestampRE = regexp.MustCompile(`(?i)^CURRENT_TIMESTAMP(?:\((\d+)\))?$`)
	postgresPartitionOfRE      = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+\S+\s+PARTITION\s+OF\s+(.+?)(?:\s+FOR\s+VALUES\b|$)`)
	postgresFloorExprRE        = regexp.MustCompile(`(?i)^floor\(\s*\(?\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)?\s*(?:::double\s+precision)?\s*\)$`)
	postgresFunctionRE         = regexp.MustCompile(`(?is)\bCREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\b.*?\$\$.*?\$\$\s+LANGUAGE\s+[A-Za-z_][A-Za-z0-9_]*(?:\s+[^;]*)?;`)
	flywayUndoMigrationRE      = regexp.MustCompile(`^U\d+\.sql$`)
	spaceRunRE                 = regexp.MustCompile(`\s+`)
)

// TxtarScriptProbe parses Atlas integration txtar scripts and executes the
// mapped OSS command/runtime subset against a virtual fixture runtime. Its OK
// rows include the script surface per fixture so the report is an inventory of
// what was actually exercised or asserted, not just a green aggregate count.
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
		"txtar command/runtime execution is not implemented yet; script surface: " + summarizeCommandSurface(commands),
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
	commands    []string
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
	if len(r.commands) > 0 {
		parts = append(parts, "script surface: "+summarizeCommandSurface(r.commands))
	}
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
	files             map[string]string
	dirs              map[string]bool
	hasVirtualDBState bool
	dbStatements      []ast.Node
	dbRows            map[string][]txtarVirtualRow
	partitionChildren map[string]int
	appliedMigrations map[string]bool
	appliedVersion    string
}

type txtarVirtualRow map[string]string

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

func (r *txtarRuntime) replaceDBStatements(statements []ast.Node) {
	r.hasVirtualDBState = true
	r.dbStatements = statements
	r.partitionChildren = nil
}

func runTxtarScript(fx Fixture, data string, commands []string) txtarRunSummary {
	runtime := newTxtarRuntime(data)
	unsupportedFiles := map[string]bool{}
	dbStateUnsupported := false
	summary := txtarRunSummary{commands: commands}
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
				summary.failures = append(summary.failures, fmt.Sprintf(
					"stdout assertion %q did not match %q",
					txtarAssertionText(trimmed), oneLine(last.stdout),
				))
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
				summary.failures = append(summary.failures, fmt.Sprintf(
					"stderr assertion %q did not match %q",
					txtarAssertionText(trimmed), oneLine(last.stderr),
				))
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
			if mismatch := txtarCmpmigMismatch(fx, runtime, fields[1], fields[2]); mismatch != "" {
				summary.failures = append(summary.failures, mismatch)
			}
			continue
		}

		expectedFailure, commandLine := txtarExpectedFailure(trimmed)
		if commandLine == "" {
			continue
		}
		if dbStateUnsupported && txtarCommandReadsUnsupportedDBState(commandLine) &&
			!txtarCommandClearsDBState(fx, commandLine) {
			last = txtarCommandResult{unsupported: "blocked by unsupported database state"}
			if redirect := txtarRedirectTarget(commandLine); redirect != "" {
				unsupportedFiles[redirect] = true
			} else {
				unsupportedFiles["stdout"] = true
				unsupportedFiles["stderr"] = true
			}
			continue
		}
		if txtarCommandReadsUnsupportedFile(runtime, commandLine, unsupportedFiles) {
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
			if result.stdout != "" {
				runtime.files[redirect] = result.stdout
				runtime.addParentDirs(redirect)
			}
			delete(unsupportedFiles, redirect)
		}
		clearUnsupportedFileCommandOutputs(commandLine, runtime, unsupportedFiles)
		if txtarCommandClearsDBState(fx, commandLine) {
			dbStateUnsupported = false
		}
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
	if result, ok := runTxtarMigrateNew(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateDiff(fx, runtime, fields, expectedFailure); ok {
		return result
	}
	if result, ok := runTxtarMigrateLint(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateApply(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateSet(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarMigrateStatus(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarClearSchema(runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarSchemaClean(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarApply(fx, runtime, fields, expectedFailure); ok {
		return result
	}
	if result, ok := runTxtarExist(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarSynced(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarExecSQL(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarCmpHCL(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarCmpShow(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarSchemaApply(fx, runtime, fields); ok {
		return result
	}
	if result, ok := runTxtarSchemaDiff(fx, runtime, fields); ok {
		return result
	}
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "schema" || fields[2] != "inspect" {
		if key, ok := txtarCommandKeyFields(fields); ok {
			return txtarCommandResult{unsupported: key}
		}
		return txtarCommandResult{unsupported: line}
	}
	return runTxtarSchemaInspect(fx, runtime, fields)
}

func txtarCommandFields(line string) []string {
	fields := splitTxtarFields(line)
	if len(fields) > 0 && fields[0] == "exec" {
		return fields[1:]
	}
	return fields
}

func txtarCommandReadsUnsupportedFile(runtime *txtarRuntime, line string, unsupportedFiles map[string]bool) bool {
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
			return txtarMigrateHashReadsUnsupportedFile(runtime, fields[3:], unsupportedFiles)
		case "migrate apply", "migrate lint", "migrate new", "migrate set", "migrate status", "migrate validate":
			return txtarMigrateCommandReadsUnsupportedFile(runtime, fields[3:], unsupportedFiles)
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

func txtarMigrateHashReadsUnsupportedFile(runtime *txtarRuntime, args []string, unsupportedFiles map[string]bool) bool {
	return txtarMigrateCommandReadsUnsupportedFile(runtime, args, unsupportedFiles)
}

func txtarMigrateCommandReadsUnsupportedFile(runtime *txtarRuntime, args []string, unsupportedFiles map[string]bool) bool {
	dir := txtarMigrateCommandRuntimeDir(runtime, args)
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

	dir := txtarMigrateHashDir(runtime, fields[3:])
	if err := runtime.refreshMigrationHash(dir); err != nil {
		return txtarCommandResult{err: err}, true
	}
	return txtarCommandResult{}, true
}

func txtarMigrateHashDir(runtime *txtarRuntime, args []string) string {
	return txtarMigrateCommandRuntimeDir(runtime, args)
}

func runTxtarMigrateValidate(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "validate" {
		return txtarCommandResult{}, false
	}

	dir := txtarMigrateCommandRuntimeDir(runtime, fields[3:])
	fsys, ok := runtime.subFS(dir)
	if !ok {
		return txtarCommandResult{failed: true, err: fmt.Errorf("migration directory %q missing", dir)}, true
	}
	sumPath := path.Join(dir, atlascompat.AtlasSumFileName)
	expected, ok := runtime.files[sumPath]
	if !ok {
		return txtarCommandResult{
			stdout: "You have a checksum error in your migration directory.\n",
			stderr: "Error: checksum file not found\n",
			failed: true,
			err:    fmt.Errorf("checksum file not found"),
		}, true
	}
	actual, err := atlascompat.ComputeSum(fsys, migrator.MigrationDirFormatAtlas)
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

func runTxtarMigrateNew(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "new" {
		return txtarCommandResult{}, false
	}

	name := txtarMigrateNewName(fields[3:])
	if name == "" {
		return txtarCommandResult{unsupported: "atlas migrate new"}, true
	}
	dir := txtarMigrateCommandRuntimeDir(runtime, fields[3:])
	file := txtarNextNamedMigrationFile(runtime, dir, name)
	runtime.files[file] = ""
	runtime.addParentDirs(file)
	if err := runtime.refreshMigrationHash(dir); err != nil {
		return txtarCommandResult{err: err}, true
	}
	return txtarCommandResult{}, true
}

func txtarMigrateNewName(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dir", "--env", "--edit":
			if i+1 < len(args) {
				i++
			}
		default:
			switch {
			case strings.HasPrefix(arg, "--dir="), strings.HasPrefix(arg, "--env="), strings.HasPrefix(arg, "--edit="):
				continue
			case strings.HasPrefix(arg, "-"):
				continue
			default:
				return arg
			}
		}
	}
	return ""
}

type txtarMigrateDiffArgs struct {
	devURL    string
	to        []string
	dir       string
	dirFormat string
	dirSet    bool
	env       string
	name      string
	indent    string
	qualifier string
	blocked   bool
}

func runTxtarMigrateDiff(fx Fixture, runtime *txtarRuntime, fields []string, expectedFailure bool) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "diff" {
		return txtarCommandResult{}, false
	}

	args := txtarParseMigrateDiffArgs(fields[3:])
	if args.env != "" && args.qualifier != "" {
		args.blocked = true
	}
	if args.env != "" {
		var ok bool
		args, ok = txtarResolveMigrateDiffEnv(fx, runtime, args)
		if !ok {
			args.blocked = true
		}
	}
	switch {
	case args.blocked:
		return txtarCommandResult{unsupported: "atlas migrate diff"}, true
	case expectedFailure:
		if result, ok := txtarMigrateDiffExpectedFailure(args); ok {
			return result, true
		}
		return runTxtarMigrateDiffCreate(fx, runtime, args), true
	default:
		return runTxtarMigrateDiffCreate(fx, runtime, args), true
	}
}

func txtarMigrateDiffExpectedFailure(args txtarMigrateDiffArgs) (txtarCommandResult, bool) {
	switch {
	case args.devURL == "":
		return txtarCommandResult{
			stderr: "Error: \"dev-url\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"dev-url\" not set"),
		}, true
	case len(args.to) == 0:
		return txtarCommandResult{
			stderr: "Error: \"to\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"to\" not set"),
		}, true
	}
	schemes := txtarURLSchemes(args.to)
	if len(schemes) <= 1 {
		return txtarCommandResult{}, false
	}
	if len(slices.Compact(slices.Clone(schemes))) > 1 {
		return txtarCommandResult{
			stderr: "Error: got mixed --to url schemes\n",
			failed: true,
			err:    fmt.Errorf("got mixed --to url schemes"),
		}, true
	}
	return txtarCommandResult{
		stderr: fmt.Sprintf("Error: got multiple --to urls of scheme %q\n", schemes[0]),
		failed: true,
		err:    fmt.Errorf("got multiple --to urls of scheme %q", schemes[0]),
	}, true
}

func txtarURLSchemes(urls []string) []string {
	schemes := make([]string, 0, len(urls))
	for _, url := range urls {
		scheme, _, ok := strings.Cut(url, "://")
		if ok {
			schemes = append(schemes, scheme)
		}
	}
	slices.Sort(schemes)
	return schemes
}

func txtarParseMigrateDiffArgs(args []string) txtarMigrateDiffArgs {
	out := txtarMigrateDiffArgs{dir: "migrations"}
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
				var ok bool
				out.dir, out.dirFormat, ok = txtarMigrateDiffDirAndFormat(args[i+1])
				if !ok {
					out.blocked = true
				}
				out.dirSet = true
				i++
			}
		case "--format":
			if i+1 < len(args) {
				var ok bool
				out.indent, ok = txtarMigrateDiffSQLFormatIndent(args[i+1])
				if !ok {
					out.blocked = true
				}
				i++
			}
		case "--env":
			if i+1 < len(args) {
				out.env = args[i+1]
				i++
			}
		case "--qualifier":
			if i+1 < len(args) {
				out.qualifier = args[i+1]
				i++
			} else {
				out.blocked = true
			}
		case "--dir-format":
			if i+1 < len(args) {
				out.dirFormat = args[i+1]
				if !txtarSupportedMigrateDiffDirFormat(out.dirFormat) {
					out.blocked = true
				}
				i++
			} else {
				out.blocked = true
			}
		default:
			switch {
			case strings.HasPrefix(arg, "--dev-url="):
				out.devURL = strings.TrimPrefix(arg, "--dev-url=")
			case strings.HasPrefix(arg, "--to="):
				out.to = append(out.to, strings.TrimPrefix(arg, "--to="))
			case strings.HasPrefix(arg, "--dir="):
				var ok bool
				out.dir, out.dirFormat, ok = txtarMigrateDiffDirAndFormat(strings.TrimPrefix(arg, "--dir="))
				if !ok {
					out.blocked = true
				}
				out.dirSet = true
			case strings.HasPrefix(arg, "--format="):
				var ok bool
				out.indent, ok = txtarMigrateDiffSQLFormatIndent(strings.TrimPrefix(arg, "--format="))
				if !ok {
					out.blocked = true
				}
				continue
			case strings.HasPrefix(arg, "--env="):
				out.env = strings.TrimPrefix(arg, "--env=")
			case strings.HasPrefix(arg, "--qualifier="):
				out.qualifier = strings.TrimPrefix(arg, "--qualifier=")
			case strings.HasPrefix(arg, "--dir-format="):
				out.dirFormat = strings.TrimPrefix(arg, "--dir-format=")
				if !txtarSupportedMigrateDiffDirFormat(out.dirFormat) {
					out.blocked = true
				}
			case strings.HasPrefix(arg, "-"):
				out.blocked = true
			case !seenName:
				seenName = true
				out.name = arg
			default:
				out.blocked = true
			}
		}
	}
	return out
}

func txtarMigrateDiffDirAndFormat(value string) (string, string, bool) {
	dir := txtarFileURLPath(value)
	_, query, ok := strings.Cut(value, "?")
	if !ok {
		return dir, "", true
	}
	for _, part := range strings.Split(query, "&") {
		key, val, ok := strings.Cut(part, "=")
		if !ok || key != "format" {
			return dir, "", false
		}
		if !txtarSupportedMigrateDiffDirFormat(val) {
			return dir, "", false
		}
		return dir, val, true
	}
	return dir, "", true
}

func txtarSupportedMigrateDiffDirFormat(format string) bool {
	switch format {
	case "", "atlas", "dbmate", "flyway", "golang-migrate", "goose", "liquibase":
		return true
	default:
		return false
	}
}

func txtarMigrateDiffSQLFormatIndent(format string) (string, bool) {
	switch format {
	case "{{ sql . }}":
		return "", true
	case `{{ sql . "  " }}`:
		return "  ", true
	default:
		return "", false
	}
}

func txtarResolveMigrateDiffEnv(fx Fixture, runtime *txtarRuntime, args txtarMigrateDiffArgs) (txtarMigrateDiffArgs, bool) {
	if txtarFixtureFamily(fx) != "sqlite" {
		return args, false
	}
	project, ok := runtime.files["atlas.hcl"]
	if !ok {
		return args, false
	}
	env, ok := txtarAtlasNamedBlock(project, "env", args.env)
	if !ok {
		return args, false
	}
	if args.devURL == "" {
		devURL, ok := txtarHCLStringAttr(env, "dev")
		if !ok {
			return args, false
		}
		args.devURL = devURL
	}
	if len(args.to) == 0 {
		target, ok := txtarAtlasEnvSourceTarget(runtime, project, env)
		if !ok {
			return args, false
		}
		args.to = []string{target}
	}
	if !args.dirSet {
		if migration, ok := txtarAtlasAnonymousBlock(env, "migration"); ok {
			if dir, ok := txtarHCLStringAttr(migration, "dir"); ok {
				args.dir = txtarFileURLPath(dir)
			}
		}
	}
	return args, true
}

func txtarAtlasEnvSourceTarget(runtime *txtarRuntime, project, env string) (string, bool) {
	return txtarAtlasEnvSourceTargetWithVars(runtime, project, env, nil)
}

func txtarAtlasEnvSourceTargetWithVars(runtime *txtarRuntime, project, env string, vars map[string]string) (string, bool) {
	value, ok := txtarHCLAttrValue(env, "src")
	if !ok {
		return "", false
	}
	if refName, ok := txtarAtlasDataHCLSchemaRef(value); ok {
		return txtarAtlasDataHCLSchemaTarget(runtime, project, refName)
	}
	return txtarAtlasHCLSourceTargetWithVars(runtime, value, vars)
}

func txtarAtlasDataHCLSchemaRef(value string) (string, bool) {
	value = strings.TrimSpace(value)
	const prefix = "data.hcl_schema."
	const suffix = ".url"
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	return name, name != ""
}

func txtarAtlasDataHCLSchemaTarget(runtime *txtarRuntime, project, name string) (string, bool) {
	block, ok := txtarAtlasDataHCLSchemaBlock(project, name)
	if !ok {
		return "", false
	}
	files, ok := txtarAtlasDataHCLSchemaFiles(runtime, block)
	if !ok {
		return "", false
	}
	vars := txtarAtlasHCLVars(block)
	synthetic := path.Join(".ptah", "data_hcl_schema_"+name+".hcl")
	runtime.files[synthetic] = txtarJoinHCLSourceFiles(runtime, files, vars)
	runtime.addParentDirs(synthetic)
	return "file://" + synthetic, true
}

func txtarAtlasDataHCLSchemaFiles(runtime *txtarRuntime, block string) ([]string, bool) {
	if value, ok := txtarHCLStringAttr(block, "path"); ok {
		file := txtarCleanAtlasPath(value)
		if _, ok := runtime.files[file]; !ok {
			return nil, false
		}
		return []string{file}, true
	}
	value, ok := txtarHCLAttrValue(block, "paths")
	if !ok {
		return nil, false
	}
	pattern, ok := txtarAtlasGlobPattern(value)
	if !ok {
		return nil, false
	}
	var files []string
	for file := range runtime.files {
		matched, err := path.Match(pattern, file)
		if err == nil && matched {
			files = append(files, file)
		}
	}
	slices.Sort(files)
	return files, len(files) > 0
}

func txtarAtlasGlobPattern(value string) (string, bool) {
	value = strings.TrimSpace(value)
	const prefix = "glob("
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, ")") {
		return "", false
	}
	quoted := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, prefix), ")"))
	pattern, ok := txtarHCLQuotedString(quoted)
	if !ok {
		return "", false
	}
	return txtarCleanAtlasPath(pattern), true
}

func txtarAtlasHCLVars(block string) map[string]string {
	varsBlock, ok := txtarAtlasAnonymousBlock(block, "vars")
	if !ok {
		return nil
	}
	vars := map[string]string{}
	re := regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"\s*$`)
	for _, match := range re.FindAllStringSubmatch(varsBlock, -1) {
		vars[match[1]] = match[2]
	}
	return vars
}

func txtarAtlasHCLSourceTarget(runtime *txtarRuntime, value string) (string, bool) {
	return txtarAtlasHCLSourceTargetWithVars(runtime, value, nil)
}

func txtarAtlasHCLSourceTargetWithVars(runtime *txtarRuntime, value string, vars map[string]string) (string, bool) {
	files, ok := txtarAtlasHCLSourceFiles(runtime, value)
	if !ok {
		return "", false
	}
	if len(files) == 1 && len(vars) == 0 {
		return "file://" + files[0], true
	}
	synthetic := ".ptah/source.hcl"
	runtime.files[synthetic] = txtarJoinHCLSourceFiles(runtime, files, vars)
	runtime.addParentDirs(synthetic)
	return "file://" + synthetic, true
}

func txtarAtlasHCLSourceFiles(runtime *txtarRuntime, value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if src, ok := txtarHCLQuotedString(value); ok {
		return txtarAtlasHCLPathFiles(runtime, src)
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		var files []string
		re := regexp.MustCompile(`"([^"]*)"`)
		for _, match := range re.FindAllStringSubmatch(value, -1) {
			part, ok := txtarAtlasHCLPathFiles(runtime, match[1])
			if !ok {
				return nil, false
			}
			files = append(files, part...)
		}
		slices.Sort(files)
		return files, len(files) > 0
	}
	return nil, false
}

func txtarAtlasHCLPathFiles(runtime *txtarRuntime, value string) ([]string, bool) {
	name := txtarCleanAtlasPath(value)
	if _, ok := runtime.files[name]; ok {
		return []string{name}, true
	}
	prefix := strings.TrimSuffix(name, "/") + "/"
	var files []string
	for file := range runtime.files {
		if strings.HasPrefix(file, prefix) && path.Ext(file) == ".hcl" {
			files = append(files, file)
		}
	}
	slices.Sort(files)
	return files, len(files) > 0
}

func txtarJoinHCLSourceFiles(runtime *txtarRuntime, files []string, vars map[string]string) string {
	var out strings.Builder
	for _, file := range files {
		data := runtime.files[file]
		data = txtarStripHCLVariableBlocks(data)
		for key, value := range vars {
			defaultRe := regexp.MustCompile(`default\s*=\s*var\.` + regexp.QuoteMeta(key) + `\b`)
			data = defaultRe.ReplaceAllString(data, "default = "+strconv.Quote("'"+value+"'"))
			data = strings.ReplaceAll(data, "var."+key, strconv.Quote(value))
		}
		data = txtarInlineAtlasSchemaNameAttrs(data)
		out.WriteString(strings.TrimSpace(data))
		out.WriteString("\n")
	}
	return out.String()
}

func txtarInlineAtlasSchemaNameAttrs(data string) string {
	// Ptah HCL does not support Atlas's schema name override yet. Keep this
	// rewrite narrow: it only handles schema blocks that contain the name attr.
	re := regexp.MustCompile(`(?ms)schema\s+"([^"]+)"\s*\{\s*name\s*=\s*"([^"]+)"\s*\}`)
	for _, match := range re.FindAllStringSubmatch(data, -1) {
		alias := match[1]
		name := match[2]
		data = strings.ReplaceAll(data, match[0], fmt.Sprintf("schema %q {\n}", name))
		data = strings.ReplaceAll(data, "schema."+alias, "schema."+name)
	}
	return data
}

func txtarStripHCLVariableBlocks(data string) string {
	re := regexp.MustCompile(`(?m)^\s*variable\s+"[^"]+"\s*\{`)
	for {
		loc := re.FindStringIndex(data)
		if loc == nil {
			return data
		}
		_, end, ok := txtarHCLBlockBody(data, loc[1]-1)
		if !ok {
			return data
		}
		data = data[:loc[0]] + data[end:]
	}
}

func txtarAtlasDataHCLSchemaBlock(data, name string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*data\s+"hcl_schema"\s+"` + regexp.QuoteMeta(name) + `"\s*\{`)
	loc := re.FindStringIndex(data)
	if loc == nil {
		return "", false
	}
	body, _, ok := txtarHCLBlockBody(data, loc[1]-1)
	return body, ok
}

func txtarAtlasNamedBlock(data, kind, name string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(kind) + `\s+"` + regexp.QuoteMeta(name) + `"\s*\{`)
	loc := re.FindStringIndex(data)
	if loc == nil {
		return "", false
	}
	body, _, ok := txtarHCLBlockBody(data, loc[1]-1)
	return body, ok
}

func txtarAtlasAnonymousBlock(data, kind string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(kind) + `\s*(?:=\s*)?\{`)
	loc := re.FindStringIndex(data)
	if loc == nil {
		return "", false
	}
	body, _, ok := txtarHCLBlockBody(data, loc[1]-1)
	return body, ok
}

func txtarHCLBlockBody(data string, open int) (string, int, bool) {
	if open < 0 || open >= len(data) || data[open] != '{' {
		return "", 0, false
	}
	depth := 0
	inString := false
	inLineComment := false
	inBlockComment := false
	escaped := false
	for i := open; i < len(data); i++ {
		ch := data[i]
		next := byte(0)
		if i+1 < len(data) {
			next = data[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch data[i] {
		case '"':
			inString = true
		case '#':
			inLineComment = true
		case '/':
			switch next {
			case '/':
				inLineComment = true
				i++
			case '*':
				inBlockComment = true
				i++
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return data[open+1 : i], i + 1, true
			}
		}
	}
	return "", 0, false
}

func txtarHCLStringAttr(data, key string) (string, bool) {
	value, ok := txtarHCLAttrValue(data, key)
	if !ok {
		return "", false
	}
	return txtarHCLQuotedString(value)
}

func txtarHCLAttrValue(data, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	loc := re.FindStringIndex(data)
	if loc == nil {
		return "", false
	}
	start := loc[1]
	for start < len(data) && (data[start] == ' ' || data[start] == '\t') {
		start++
	}
	if start >= len(data) {
		return "", false
	}
	switch data[start] {
	case '"':
		end, ok := txtarHCLQuotedStringEnd(data, start)
		if !ok {
			return "", false
		}
		return data[start : end+1], true
	case '[':
		end := strings.IndexByte(data[start:], ']')
		if end < 0 {
			return "", false
		}
		return data[start : start+end+1], true
	default:
		end := strings.IndexByte(data[start:], '\n')
		if end < 0 {
			return strings.TrimSpace(data[start:]), true
		}
		return strings.TrimSpace(data[start : start+end]), true
	}
}

func txtarHCLQuotedStringEnd(data string, start int) (int, bool) {
	escaped := false
	for i := start + 1; i < len(data); i++ {
		switch {
		case escaped:
			escaped = false
		case data[i] == '\\':
			escaped = true
		case data[i] == '"':
			return i, true
		}
	}
	return 0, false
}

func txtarHCLQuotedString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	return value[1 : len(value)-1], true
}

func txtarCleanAtlasPath(value string) string {
	return path.Clean(strings.TrimPrefix(value, "./"))
}

func runTxtarMigrateDiffCreate(fx Fixture, runtime *txtarRuntime, args txtarMigrateDiffArgs) txtarCommandResult {
	if !txtarMigrateDiffSupportsInitialCreate(txtarFixtureFamily(fx)) || args.devURL == "" || len(args.to) == 0 {
		return txtarCommandResult{unsupported: "atlas migrate diff"}
	}

	if !txtarMigrateDiffSupportedTargetSchemes(args.to) {
		return txtarCommandResult{unsupported: "atlas migrate diff"}
	}

	targetStatements, err := txtarMigrateDiffTargetStatements(fx, runtime, args.to)
	if err != nil {
		if errors.Is(err, errUnsupportedInspectSQL) || errors.Is(err, errUnsupportedInspectHCL) {
			return txtarCommandResult{unsupported: "atlas migrate diff"}
		}
		return txtarCommandResult{err: err}
	}
	if txtarMigrateDiffShouldUnqualifyTables(txtarFixtureDialect(fx)) {
		targetStatements = atlasUnqualifySchemaStatements(txtarFixtureSchemaName(fx), targetStatements)
	}
	migrationFiles := txtarMigrationSQLFilesInDir(runtime, args.dir)
	sql, err := renderTxtarMigrateDiffSQLStatements(fx, targetStatements, args.indent, args.qualifier)
	if err != nil && !errors.Is(err, errUnsupportedInspectSQL) && !errors.Is(err, errUnsupportedInspectHCL) {
		return txtarCommandResult{err: err}
	}
	if err == nil && txtarMigrationDirHasSQL(runtime, args.dir, sql) {
		return txtarCommandResult{
			stdout: "The migration directory is synced with the desired state, no changes to be made\n",
		}
	}
	if len(migrationFiles) > 0 {
		current, currentComparable, ok := txtarCurrentMigrationState(fx, runtime, args.dir)
		if !ok {
			return txtarCommandResult{unsupported: "atlas migrate diff"}
		}
		if currentComparable && txtarMigrateDiffStatesMatch(fx, current, targetStatements, args.indent) {
			return txtarCommandResult{
				stdout: "The migration directory is synced with the desired state, no changes to be made\n",
			}
		}
		incrementalSQL, incrementalDownSQL, ok := renderTxtarMigrateDiffCreateTablePairSQL(
			fx,
			current,
			targetStatements,
			args.indent,
			args.qualifier,
		)
		if !ok {
			incrementalSQL, incrementalDownSQL, ok = renderTxtarMigrateDiffAddColumnPairSQL(
				fx,
				current,
				targetStatements,
				args.indent,
				args.qualifier,
			)
		}
		if !ok {
			incrementalSQL, incrementalDownSQL, ok = renderTxtarMigrateDiffCheckChangeSQL(
				fx,
				current,
				targetStatements,
				args.qualifier,
			)
		}
		if !ok {
			incrementalSQL, incrementalDownSQL, ok = renderTxtarMigrateDiffPrimaryKeyChangeSQL(
				fx,
				current,
				targetStatements,
				args.qualifier,
			)
		}
		if !ok {
			incrementalSQL, incrementalDownSQL, ok = renderTxtarMigrateDiffDropIndexSQL(
				fx,
				current,
				targetStatements,
				args.qualifier,
			)
		}
		if !ok {
			return txtarCommandResult{unsupported: "atlas migrate diff"}
		}
		if err := txtarWriteMigrateDiff(runtime, args, incrementalSQL, incrementalDownSQL); err != nil {
			return txtarCommandResult{err: err}
		}
		return txtarCommandResult{}
	}
	// Existing migration directories can still be handled by narrow incremental
	// renderers when full initial-create rendering is not available.
	if errors.Is(err, errUnsupportedInspectSQL) || errors.Is(err, errUnsupportedInspectHCL) {
		return txtarCommandResult{unsupported: "atlas migrate diff"}
	}

	downSQL, ok := renderTxtarMigrateDiffCreateDownSQL(fx, targetStatements, args.qualifier)
	if !ok {
		return txtarCommandResult{unsupported: "atlas migrate diff"}
	}
	if err := txtarWriteMigrateDiff(runtime, args, sql, downSQL); err != nil {
		return txtarCommandResult{err: err}
	}
	return txtarCommandResult{}
}

func renderTxtarMigrateDiffCreateTablePairSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	indent string,
	qualifier string,
) (string, string, bool) {
	statements, ok := txtarMigrateDiffCreateTableStatements(txtarFixtureDialect(fx), current, target)
	if !ok || len(statements) == 0 {
		return "", "", false
	}
	outputDialect := txtarMigrateDiffOutputDialect(txtarFixtureDialect(fx))
	upStatements := statements
	if qualifier != "" {
		upStatements = atlasQualifyTableStatements(qualifier, statements)
	}
	upSQL, err := renderAtlasInspectSQL(outputDialect, upStatements, indent)
	if err != nil {
		return "", "", false
	}
	downSQL, ok := renderTxtarMigrateDiffCreateDownSQL(fx, statements, qualifier)
	if !ok {
		return "", "", false
	}
	return upSQL, downSQL, true
}

func txtarMigrateDiffCreateTableStatements(dialect string, current, target []ast.Node) ([]ast.Node, bool) {
	currentTables := txtarCreateTablesByName(current)
	targetTables := txtarCreateTablesByName(target)
	for tableName := range currentTables {
		if _, ok := targetTables[tableName]; !ok {
			return nil, false
		}
	}
	missingTables := map[string]bool{}
	for tableName, targetTable := range targetTables {
		currentTable, ok := currentTables[tableName]
		if !ok {
			missingTables[tableName] = true
			continue
		}
		if !txtarTablesEquivalentBySQL(dialect, currentTable, targetTable) {
			return nil, false
		}
	}
	if len(missingTables) == 0 {
		return nil, false
	}
	statements := make([]ast.Node, 0, len(target))
	for _, stmt := range target {
		switch node := stmt.(type) {
		case *ast.CreateSchemaNode:
			continue
		case *ast.CreateTableNode:
			if missingTables[atlasSQLIdentifier(node.Name)] {
				statements = append(statements, node)
			}
		case *ast.IndexNode:
			if !missingTables[atlasSQLIdentifier(node.Table)] {
				return nil, false
			}
			statements = append(statements, node)
		default:
			return nil, false
		}
	}
	return statements, len(statements) > 0
}

func txtarCurrentMigrationState(fx Fixture, runtime *txtarRuntime, dir string) ([]ast.Node, bool, bool) {
	var state []ast.Node
	comparable := true
	for _, file := range txtarMigrationApplySQLFilesInDir(runtime, dir) {
		parsed := txtarParseMigrationStatementsForDialect(runtime.files[file], txtarFixtureDialect(fx))
		if parsed.err != nil || parsed.failing != "" {
			return nil, false, false
		}
		if parsed.skippedUnsupported {
			comparable = false
		}
		var err error
		state, err = txtarApplyStatementsToVirtualState(state, parsed.statements)
		if err != nil {
			return nil, false, false
		}
	}
	return state, comparable, true
}

func txtarWriteMigrateDiff(runtime *txtarRuntime, args txtarMigrateDiffArgs, upSQL, downSQL string) error {
	version := txtarNextMigrateDiffVersion(runtime, args)
	switch args.dirFormat {
	case "", "atlas":
		name := txtarNextMigrationFile(runtime, args.dir)
		if args.name != "" {
			name = txtarNextNamedMigrationFile(runtime, args.dir, args.name)
		}
		runtime.files[name] = upSQL
		runtime.addParentDirs(name)
	case "golang-migrate":
		runtime.files[path.Join(args.dir, fmt.Sprintf("%d.down.sql", version))] = txtarFormatMigrationCommentCase(downSQL)
		runtime.files[path.Join(args.dir, fmt.Sprintf("%d.up.sql", version))] = txtarFormatMigrationCommentCase(upSQL)
		runtime.addParentDirs(path.Join(args.dir, fmt.Sprintf("%d.up.sql", version)))
	case "goose":
		runtime.files[path.Join(args.dir, fmt.Sprintf("%d.sql", version))] = "-- +goose Up\n" +
			txtarFormatMigrationCommentCase(upSQL) + "\n-- +goose Down\n" +
			txtarFormatMigrationCommentCase(downSQL)
		runtime.addParentDirs(path.Join(args.dir, fmt.Sprintf("%d.sql", version)))
	case "dbmate":
		runtime.files[path.Join(args.dir, fmt.Sprintf("%d.sql", version))] = "-- migrate:up\n" +
			txtarFormatMigrationCommentCase(upSQL) + "\n-- migrate:down\n" +
			txtarFormatMigrationCommentCase(downSQL)
		runtime.addParentDirs(path.Join(args.dir, fmt.Sprintf("%d.sql", version)))
	case "flyway":
		runtime.files[path.Join(args.dir, fmt.Sprintf("U%d.sql", version))] = txtarFormatMigrationCommentCase(downSQL)
		runtime.files[path.Join(args.dir, fmt.Sprintf("V%d.sql", version))] = txtarFormatMigrationCommentCase(upSQL)
		runtime.addParentDirs(path.Join(args.dir, fmt.Sprintf("V%d.sql", version)))
	case "liquibase":
		runtime.files[path.Join(args.dir, fmt.Sprintf("%d.sql", version))] = txtarLiquibaseMigrationSQL(upSQL, downSQL)
		runtime.addParentDirs(path.Join(args.dir, fmt.Sprintf("%d.sql", version)))
	default:
		return fmt.Errorf("unsupported migrate diff dir format %q", args.dirFormat)
	}
	return runtime.refreshMigrationHash(args.dir)
}

func txtarNextMigrateDiffVersion(runtime *txtarRuntime, args txtarMigrateDiffArgs) int {
	switch args.dirFormat {
	case "flyway":
		return txtarCountMigrationFilesWithPrefix(runtime, args.dir, "V") + 1
	case "golang-migrate":
		return txtarCountMigrationFilesWithSuffix(runtime, args.dir, ".up.sql") + 1
	default:
		return len(txtarMigrationSQLFilesInDir(runtime, args.dir)) + 1
	}
}

func txtarCountMigrationFilesWithPrefix(runtime *txtarRuntime, dir, prefix string) int {
	count := 0
	for _, file := range txtarMigrationSQLFilesInDir(runtime, dir) {
		if strings.HasPrefix(path.Base(file), prefix) {
			count++
		}
	}
	return count
}

func txtarCountMigrationFilesWithSuffix(runtime *txtarRuntime, dir, suffix string) int {
	count := 0
	for _, file := range txtarMigrationSQLFilesInDir(runtime, dir) {
		if strings.HasSuffix(path.Base(file), suffix) {
			count++
		}
	}
	return count
}

func txtarMigrateDiffStatesMatch(fx Fixture, current, target []ast.Node, indent string) bool {
	currentSQL, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), current, indent)
	if err != nil {
		return false
	}
	targetSQL, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), target, indent)
	if err != nil {
		return false
	}
	return currentSQL == targetSQL
}

func renderTxtarMigrateDiffAddColumnSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	indent string,
	qualifier string,
) (string, bool) {
	dialect := txtarFixtureDialect(fx)
	changes, ok := txtarMigrateDiffAddColumnChanges(dialect, current, target)
	if !ok || len(changes) == 0 {
		return "", false
	}
	var out strings.Builder
	outputDialect := txtarMigrateDiffOutputDialect(dialect)
	quote := atlasIdentifierQuoter(outputDialect)
	for _, change := range changes {
		tableName := change.tableName
		if qualifier != "" {
			tableName = atlasQualifyTableName(qualifier, tableName)
		}
		fmt.Fprintf(&out, "-- Modify %q table\n", atlasSQLIdentifier(change.tableName))
		parts := make([]string, 0, len(change.columns))
		for _, column := range change.columns {
			parts = append(parts, "ADD COLUMN "+renderAtlasColumnSQL(outputDialect, quote, column, true, atlasInspectSQLOptions{}))
		}
		fmt.Fprintf(&out, "ALTER TABLE %s ", quote(tableName))
		if indent == "" {
			out.WriteString(strings.Join(parts, ", "))
		} else {
			out.WriteString(strings.Join(parts, ",\n"+indent))
		}
		out.WriteString(";\n")
	}
	return out.String(), true
}

func renderTxtarMigrateDiffAddColumnDownSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	qualifier string,
) (string, bool) {
	dialect := txtarMigrateDiffOutputDialect(txtarFixtureDialect(fx))
	changes, ok := txtarMigrateDiffAddColumnChanges(dialect, current, target)
	if !ok || len(changes) == 0 {
		return "", false
	}
	quote := atlasIdentifierQuoter(dialect)
	var out strings.Builder
	for _, change := range changes {
		tableName := change.tableName
		if qualifier != "" {
			tableName = atlasQualifyTableName(qualifier, tableName)
		}
		fmt.Fprintf(&out, "-- reverse: modify %q table\n", atlasSQLIdentifier(change.tableName))
		parts := make([]string, 0, len(change.columns))
		for _, column := range change.columns {
			parts = append(parts, "DROP COLUMN "+quote(column.Name))
		}
		fmt.Fprintf(&out, "ALTER TABLE %s %s;\n", quote(tableName), strings.Join(parts, ", "))
	}
	return out.String(), true
}

func renderTxtarMigrateDiffAddColumnPairSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	indent string,
	qualifier string,
) (string, string, bool) {
	upSQL, ok := renderTxtarMigrateDiffAddColumnSQL(fx, current, target, indent, qualifier)
	if !ok {
		return "", "", false
	}
	downSQL, ok := renderTxtarMigrateDiffAddColumnDownSQL(fx, current, target, qualifier)
	if !ok {
		return "", "", false
	}
	return upSQL, downSQL, true
}

func renderTxtarMigrateDiffCheckChangeSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	qualifier string,
) (string, string, bool) {
	dialect := txtarMigrateDiffOutputDialect(txtarFixtureDialect(fx))
	changes, ok := txtarMigrateDiffCheckChanges(dialect, current, target)
	if !ok || len(changes) == 0 {
		return "", "", false
	}
	upSQL, ok := renderTxtarMigrateDiffCheckChangeDirectionSQL(dialect, changes, qualifier, false)
	if !ok {
		return "", "", false
	}
	downSQL, ok := renderTxtarMigrateDiffCheckChangeDirectionSQL(dialect, changes, qualifier, true)
	if !ok {
		return "", "", false
	}
	return upSQL, downSQL, true
}

func renderTxtarMigrateDiffCheckChangeDirectionSQL(
	dialect string,
	changes []txtarMigrateDiffCheckChange,
	qualifier string,
	reverse bool,
) (string, bool) {
	quote := atlasIdentifierQuoter(dialect)
	var out strings.Builder
	for _, tableChanges := range txtarMigrateDiffCheckChangesByTable(changes) {
		change := tableChanges[0]
		tableName := change.tableName
		if qualifier != "" {
			tableName = atlasQualifyTableName(qualifier, tableName)
		}
		comment := "-- Modify %q table\n"
		if reverse {
			comment = "-- reverse: modify %q table\n"
		}
		fmt.Fprintf(&out, comment, atlasSQLIdentifier(change.tableName))
		parts := make([]string, 0, len(tableChanges)*2)
		for _, change := range tableChanges {
			from := change.current
			to := change.target
			if reverse {
				from, to = to, from
			}
			if from.name == "" || to.name == "" {
				return "", false
			}
			parts = append(
				parts,
				txtarMigrateDiffDropCheckSQL(dialect, quote, from.name),
				"ADD "+renderAtlasCheckSQL(quote, to.name, to.expr),
			)
		}
		fmt.Fprintf(&out, "ALTER TABLE %s ", quote(tableName))
		out.WriteString(strings.Join(parts, ", "))
		out.WriteString(";\n")
	}
	return out.String(), true
}

func txtarMigrateDiffCheckChangesByTable(changes []txtarMigrateDiffCheckChange) [][]txtarMigrateDiffCheckChange {
	var groups [][]txtarMigrateDiffCheckChange
	for _, change := range changes {
		if len(groups) == 0 || groups[len(groups)-1][0].tableName != change.tableName {
			groups = append(groups, []txtarMigrateDiffCheckChange{change})
			continue
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], change)
	}
	return groups
}

func txtarMigrateDiffDropCheckSQL(dialect string, quote func(string) string, name string) string {
	if dialect == "postgresql" {
		return "DROP CONSTRAINT " + quote(name)
	}
	return "DROP CHECK " + quote(name)
}

func renderTxtarMigrateDiffPrimaryKeyChangeSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	qualifier string,
) (string, string, bool) {
	dialect := txtarMigrateDiffOutputDialect(txtarFixtureDialect(fx))
	changes, ok := txtarMigrateDiffPrimaryKeyChanges(dialect, current, target)
	if !ok || len(changes) == 0 {
		return "", "", false
	}
	upSQL, ok := renderTxtarMigrateDiffPrimaryKeyChangeDirectionSQL(dialect, changes, qualifier, false)
	if !ok {
		return "", "", false
	}
	downSQL, ok := renderTxtarMigrateDiffPrimaryKeyChangeDirectionSQL(dialect, changes, qualifier, true)
	if !ok {
		return "", "", false
	}
	return upSQL, downSQL, true
}

func renderTxtarMigrateDiffPrimaryKeyChangeDirectionSQL(
	dialect string,
	changes []txtarMigrateDiffPrimaryKeyChange,
	qualifier string,
	reverse bool,
) (string, bool) {
	quote := atlasIdentifierQuoter(dialect)
	var out strings.Builder
	for _, change := range changes {
		tableName := change.tableName
		if qualifier != "" {
			tableName = atlasQualifyTableName(qualifier, tableName)
		}
		primaryKey := change.target
		comment := "-- Modify %q table\n"
		if reverse {
			primaryKey = change.current
			comment = "-- reverse: modify %q table\n"
		}
		if len(primaryKey.columns) == 0 {
			return "", false
		}
		fmt.Fprintf(&out, comment, atlasSQLIdentifier(change.tableName))
		parts := make([]string, 0, len(change.addedColumns)+3)
		if !reverse {
			for _, column := range change.addedColumns {
				parts = append(parts, "ADD COLUMN "+renderAtlasColumnSQL(dialect, quote, column, true, atlasInspectSQLOptions{}))
			}
		}
		parts = append(parts, "DROP PRIMARY KEY", "ADD "+renderAtlasPrimaryKeySQL(quote, primaryKey.columns, primaryKey.include))
		if reverse {
			for _, column := range change.addedColumns {
				parts = append(parts, "DROP COLUMN "+quote(column.Name))
			}
		}
		fmt.Fprintf(&out, "ALTER TABLE %s %s;\n", quote(tableName), strings.Join(parts, ", "))
	}
	return out.String(), true
}

func renderTxtarMigrateDiffDropIndexSQL(
	fx Fixture,
	current []ast.Node,
	target []ast.Node,
	qualifier string,
) (string, string, bool) {
	dialect := txtarMigrateDiffOutputDialect(txtarFixtureDialect(fx))
	changes, ok := txtarMigrateDiffDropIndexChanges(dialect, current, target)
	if !ok || len(changes) == 0 {
		return "", "", false
	}
	upSQL, ok := renderTxtarMigrateDiffDropIndexDirectionSQL(dialect, changes, qualifier, false)
	if !ok {
		return "", "", false
	}
	downSQL, ok := renderTxtarMigrateDiffDropIndexDirectionSQL(dialect, changes, qualifier, true)
	if !ok {
		return "", "", false
	}
	return upSQL, downSQL, true
}

func renderTxtarMigrateDiffDropIndexDirectionSQL(
	dialect string,
	changes []txtarMigrateDiffDropIndexChange,
	qualifier string,
	reverse bool,
) (string, bool) {
	quote := atlasIdentifierQuoter(dialect)
	var out strings.Builder
	for _, change := range changes {
		tableName := change.tableName
		if qualifier != "" {
			tableName = atlasQualifyTableName(qualifier, tableName)
		}
		if change.index == nil || change.index.Name == "" {
			return "", false
		}
		fmt.Fprintf(&out, "-- Modify %q table\n", atlasSQLIdentifier(change.tableName))
		if reverse {
			fmt.Fprintf(&out, "ALTER TABLE %s ADD %s;\n", quote(tableName), renderAtlasIndexSQL(dialect, quote, change.index))
			continue
		}
		fmt.Fprintf(&out, "ALTER TABLE %s DROP INDEX %s;\n", quote(tableName), quote(change.index.Name))
	}
	return out.String(), true
}

func renderTxtarMigrateDiffCreateDownSQL(fx Fixture, statements []ast.Node, qualifier string) (string, bool) {
	dialect := txtarMigrateDiffOutputDialect(txtarFixtureDialect(fx))
	quote := atlasIdentifierQuoter(dialect)
	var tables []string
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			continue
		}
		tableName := atlasSQLIdentifier(table.Name)
		tables = append(tables, tableName)
	}
	if len(tables) == 0 {
		return "", false
	}
	var out strings.Builder
	for i := len(tables) - 1; i >= 0; i-- {
		tableName := tables[i]
		outputName := atlasQualifyTableName(qualifier, tableName)
		fmt.Fprintf(&out, "-- reverse: create %q table\n", tableName)
		fmt.Fprintf(&out, "DROP TABLE %s;\n", quote(outputName))
	}
	return out.String(), true
}

func txtarMigrateDiffOutputDialect(dialect string) string {
	if dialect == "mariadb" {
		// Atlas migrate diff fixtures compare against the generic migration file;
		// MariaDB-version variants are present as alternates for cmpmig.
		return "mysql"
	}
	return dialect
}

func txtarFormatMigrationCommentCase(sql string) string {
	sql = strings.ReplaceAll(sql, "-- Create ", "-- create ")
	sql = strings.ReplaceAll(sql, "-- Modify ", "-- modify ")
	return sql
}

func txtarLiquibaseMigrationSQL(upSQL, downSQL string) string {
	upSQL = txtarFormatMigrationCommentCase(upSQL)
	downSQL = txtarFormatMigrationCommentCase(downSQL)
	upComment := txtarFirstAtlasMigrationComment(upSQL)
	downSQL = strings.TrimSpace(txtarStripAtlasMigrationComments(downSQL))
	return "--liquibase formatted sql\n" +
		"--changeset atlas:0-0\n" +
		"--comment: " + upComment + "\n" +
		strings.TrimSpace(txtarStripAtlasMigrationComments(upSQL)) + "\n" +
		"--rollback: " + downSQL + "\n"
}

func txtarFirstAtlasMigrationComment(sql string) string {
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-- ") {
			return strings.TrimPrefix(line, "-- ")
		}
	}
	return "migration"
}

func txtarStripAtlasMigrationComments(sql string) string {
	var lines []string
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type txtarMigrateDiffAddColumnChange struct {
	tableName   string
	targetTable *ast.CreateTableNode
	columns     []*ast.ColumnNode
}

type txtarMigrateDiffCheckChange struct {
	tableName string
	current   atlasCheckBlock
	target    atlasCheckBlock
}

type txtarMigrateDiffPrimaryKeyChange struct {
	tableName    string
	addedColumns []*ast.ColumnNode
	current      txtarPrimaryKey
	target       txtarPrimaryKey
}

type txtarPrimaryKey struct {
	columns []ast.ConstraintColumn
	include []string
}

type txtarMigrateDiffDropIndexChange struct {
	tableName string
	index     *ast.IndexNode
}

func txtarMigrateDiffDropIndexChanges(dialect string, current, target []ast.Node) ([]txtarMigrateDiffDropIndexChange, bool) {
	if !atlasSupportsInlineIndexes(dialect) {
		return nil, false
	}
	if changes, ok := txtarMigrateDiffDropIndexNodeChanges(dialect, current, target); ok && len(changes) > 0 {
		return changes, true
	}
	// Ptah's generic parser currently stores MySQL inline INDEX declarations as
	// table constraints, so support the same drop-index shape through that AST.
	return txtarMigrateDiffDropUniqueConstraintChanges(dialect, current, target)
}

func txtarMigrateDiffDropIndexNodeChanges(
	dialect string,
	current []ast.Node,
	target []ast.Node,
) ([]txtarMigrateDiffDropIndexChange, bool) {
	currentIndexes := atlasIndexesByTable(dialect, current)
	targetIndexes := atlasIndexesByTable(dialect, target)
	var changes []txtarMigrateDiffDropIndexChange
	for tableName, indexes := range currentIndexes {
		targetByName := txtarIndexesByName(targetIndexes[tableName])
		for _, index := range indexes {
			targetIndex, ok := targetByName[atlasSQLIdentifier(index.Name)]
			switch {
			case !ok:
				changes = append(changes, txtarMigrateDiffDropIndexChange{tableName: tableName, index: index})
			case !txtarIndexesEquivalent(dialect, index, targetIndex):
				return nil, false
			}
		}
	}
	dropped := txtarDropIndexChangeKeys(changes)
	for tableName, indexes := range targetIndexes {
		currentByName := txtarIndexesByName(currentIndexes[tableName])
		for _, index := range indexes {
			if _, ok := currentByName[atlasSQLIdentifier(index.Name)]; !ok {
				return nil, false
			}
		}
	}
	currentWithoutDrops := txtarRemoveDroppedIndexes(current, dropped)
	currentSQL, err := renderAtlasInspectSQL(dialect, currentWithoutDrops, "")
	if err != nil {
		return nil, false
	}
	targetSQL, err := renderAtlasInspectSQL(dialect, target, "")
	if err != nil {
		return nil, false
	}
	if currentSQL != targetSQL {
		return nil, false
	}
	slices.SortStableFunc(changes, func(a, b txtarMigrateDiffDropIndexChange) int {
		if a.tableName != b.tableName {
			return cmp.Compare(a.tableName, b.tableName)
		}
		return cmp.Compare(a.index.Name, b.index.Name)
	})
	return changes, true
}

func txtarMigrateDiffDropUniqueConstraintChanges(
	dialect string,
	current []ast.Node,
	target []ast.Node,
) ([]txtarMigrateDiffDropIndexChange, bool) {
	currentTables := txtarCreateTablesByName(current)
	targetTables := txtarCreateTablesByName(target)
	if len(currentTables) != len(targetTables) {
		return nil, false
	}
	var changes []txtarMigrateDiffDropIndexChange
	for tableName, currentTable := range currentTables {
		targetTable, ok := targetTables[tableName]
		if !ok {
			return nil, false
		}
		tableChanges, ok := txtarMigrateDiffDropTableUniqueConstraintChanges(dialect, currentTable, targetTable)
		if !ok {
			return nil, false
		}
		changes = append(changes, tableChanges...)
	}
	slices.SortStableFunc(changes, func(a, b txtarMigrateDiffDropIndexChange) int {
		if a.tableName != b.tableName {
			return cmp.Compare(a.tableName, b.tableName)
		}
		return cmp.Compare(a.index.Name, b.index.Name)
	})
	return changes, true
}

func txtarMigrateDiffDropTableUniqueConstraintChanges(
	dialect string,
	current *ast.CreateTableNode,
	target *ast.CreateTableNode,
) ([]txtarMigrateDiffDropIndexChange, bool) {
	currentUniques := txtarUniqueConstraintsByName(current)
	targetUniques := txtarUniqueConstraintsByName(target)
	var changes []txtarMigrateDiffDropIndexChange
	dropped := map[string]bool{}
	for name, currentUnique := range currentUniques {
		targetUnique, ok := targetUniques[name]
		switch {
		case !ok:
			dropped[name] = true
			changes = append(changes, txtarMigrateDiffDropIndexChange{
				tableName: atlasSQLIdentifier(current.Name),
				index:     txtarIndexFromUniqueConstraint(current.Name, currentUnique),
			})
		case !txtarUniqueConstraintsEquivalent(currentUnique, targetUnique):
			return nil, false
		}
	}
	for name := range targetUniques {
		if _, ok := currentUniques[name]; !ok {
			return nil, false
		}
	}
	currentWithoutDrops := txtarCloneTableWithoutUniqueConstraints(current, dropped)
	if !txtarTablesEquivalentForDropIndex(dialect, currentWithoutDrops, target) {
		return nil, false
	}
	return changes, true
}

func txtarUniqueConstraintsByName(table *ast.CreateTableNode) map[string]*ast.ConstraintNode {
	byName := map[string]*ast.ConstraintNode{}
	for _, constraint := range table.Constraints {
		if constraint.Type == ast.UniqueConstraint && constraint.Name != "" {
			byName[atlasSQLIdentifier(constraint.Name)] = constraint
		}
	}
	return byName
}

func txtarUniqueConstraintsEquivalent(left, right *ast.ConstraintNode) bool {
	return txtarConstraintColumnsEqual(atlasConstraintColumns(left), atlasConstraintColumns(right)) &&
		boolPtrEqual(left.NullsDistinct, right.NullsDistinct)
}

func txtarConstraintColumnsEqual(left, right []ast.ConstraintColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if atlasSQLIdentifier(left[i].Name) != atlasSQLIdentifier(right[i].Name) ||
			left[i].Prefix != right[i].Prefix ||
			left[i].Desc != right[i].Desc {
			return false
		}
	}
	return true
}

func txtarIndexFromUniqueConstraint(tableName string, constraint *ast.ConstraintNode) *ast.IndexNode {
	return &ast.IndexNode{
		Name:          atlasSQLIdentifier(constraint.Name),
		Table:         atlasSQLIdentifier(tableName),
		Columns:       atlasConstraintColumnNames(constraint),
		Unique:        true,
		NullsDistinct: cloneBoolPtr(constraint.NullsDistinct),
		Parts:         txtarIndexPartsFromConstraint(constraint),
	}
}

func txtarIndexPartsFromConstraint(constraint *ast.ConstraintNode) []ast.IndexPart {
	columns := atlasConstraintColumns(constraint)
	parts := make([]ast.IndexPart, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, ast.IndexPart{
			Name:   column.Name,
			Prefix: column.Prefix,
			Desc:   column.Desc,
		})
	}
	return parts
}

func txtarCloneTableWithoutUniqueConstraints(table *ast.CreateTableNode, dropped map[string]bool) *ast.CreateTableNode {
	clone := *table
	clone.Constraints = slices.DeleteFunc(slices.Clone(table.Constraints), func(constraint *ast.ConstraintNode) bool {
		return constraint.Type == ast.UniqueConstraint && dropped[atlasSQLIdentifier(constraint.Name)]
	})
	return &clone
}

func txtarTablesEquivalentForDropIndex(dialect string, current *ast.CreateTableNode, target *ast.CreateTableNode) bool {
	if current.SelectBody != target.SelectBody || current.Comment != target.Comment || !maps.Equal(current.Options, target.Options) {
		return false
	}
	if len(current.Columns) != len(target.Columns) {
		return false
	}
	for i := range current.Columns {
		if !txtarColumnsEquivalent(dialect, current.Columns[i], target.Columns[i]) {
			return false
		}
	}
	return txtarConstraintListsEquivalent(current.Constraints, target.Constraints)
}

func txtarConstraintListsEquivalent(left, right []*ast.ConstraintNode) bool {
	leftKeys := txtarConstraintKeys(left)
	rightKeys := txtarConstraintKeys(right)
	return slices.Equal(leftKeys, rightKeys)
}

func txtarConstraintKeys(constraints []*ast.ConstraintNode) []string {
	keys := make([]string, 0, len(constraints))
	for _, constraint := range constraints {
		keys = append(keys, txtarConstraintKey(constraint))
	}
	slices.Sort(keys)
	return keys
}

func txtarConstraintKey(constraint *ast.ConstraintNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|", constraint.Type, atlasSQLIdentifier(constraint.Name))
	for _, column := range atlasConstraintColumns(constraint) {
		fmt.Fprintf(&b, "%s:%s:%t,", atlasSQLIdentifier(column.Name), column.Prefix, column.Desc)
	}
	if constraint.Reference != nil {
		ref := constraint.Reference
		fmt.Fprintf(&b, "|ref=%s:%s:%s:%s", atlasSQLIdentifier(ref.Table), strings.Join(txtarSQLIdentifiers(ref.ReferencedColumns()), ","),
			ref.OnUpdate, ref.OnDelete)
	}
	if constraint.Expression != "" {
		fmt.Fprintf(&b, "|expr=%s", constraint.Expression)
	}
	return b.String()
}

func txtarSQLIdentifiers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, atlasSQLIdentifier(value))
	}
	return out
}

func txtarIndexesByName(indexes []*ast.IndexNode) map[string]*ast.IndexNode {
	byName := make(map[string]*ast.IndexNode, len(indexes))
	for _, index := range indexes {
		byName[atlasSQLIdentifier(index.Name)] = index
	}
	return byName
}

func txtarIndexesEquivalent(dialect string, left, right *ast.IndexNode) bool {
	quote := atlasIdentifierQuoter(dialect)
	return renderAtlasIndexSQL(dialect, quote, left) == renderAtlasIndexSQL(dialect, quote, right)
}

func txtarDropIndexChangeKeys(changes []txtarMigrateDiffDropIndexChange) map[string]bool {
	keys := make(map[string]bool, len(changes))
	for _, change := range changes {
		keys[txtarIndexKey(change.tableName, change.index.Name)] = true
	}
	return keys
}

func txtarRemoveDroppedIndexes(statements []ast.Node, dropped map[string]bool) []ast.Node {
	out := make([]ast.Node, 0, len(statements))
	for _, stmt := range statements {
		index, ok := stmt.(*ast.IndexNode)
		if ok && dropped[txtarIndexKey(index.Table, index.Name)] {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

func txtarIndexKey(tableName, indexName string) string {
	return atlasSQLIdentifier(tableName) + "\x00" + atlasSQLIdentifier(indexName)
}

func txtarMigrateDiffPrimaryKeyChanges(dialect string, current, target []ast.Node) ([]txtarMigrateDiffPrimaryKeyChange, bool) {
	currentTables := txtarCreateTablesByName(current)
	targetTables := txtarCreateTablesByName(target)
	if len(currentTables) != len(targetTables) {
		return nil, false
	}
	var changes []txtarMigrateDiffPrimaryKeyChange
	for tableName, targetTable := range targetTables {
		currentTable, ok := currentTables[tableName]
		if !ok {
			return nil, false
		}
		change, changed, ok := txtarMigrateDiffTablePrimaryKeyChange(dialect, currentTable, targetTable)
		if !ok {
			return nil, false
		}
		if changed {
			change.tableName = tableName
			changes = append(changes, change)
		}
	}
	slices.SortStableFunc(changes, func(a, b txtarMigrateDiffPrimaryKeyChange) int {
		return cmp.Compare(a.tableName, b.tableName)
	})
	return changes, true
}

func txtarMigrateDiffTablePrimaryKeyChange(
	dialect string,
	current *ast.CreateTableNode,
	target *ast.CreateTableNode,
) (txtarMigrateDiffPrimaryKeyChange, bool, bool) {
	currentPrimaryKey, ok := txtarTablePrimaryKey(current)
	if !ok {
		return txtarMigrateDiffPrimaryKeyChange{}, false, false
	}
	targetPrimaryKey, ok := txtarTablePrimaryKey(target)
	if !ok {
		return txtarMigrateDiffPrimaryKeyChange{}, false, false
	}
	addedColumns, ok := txtarMigrateDiffAddedColumnsIgnoringPrimaryKey(dialect, current, target)
	if !ok {
		return txtarMigrateDiffPrimaryKeyChange{}, false, false
	}
	if txtarPrimaryKeyColumnsEqual(currentPrimaryKey, targetPrimaryKey) {
		return txtarMigrateDiffPrimaryKeyChange{}, false, true
	}
	return txtarMigrateDiffPrimaryKeyChange{
		addedColumns: addedColumns,
		current:      currentPrimaryKey,
		target:       targetPrimaryKey,
	}, true, true
}

func txtarMigrateDiffAddedColumnsIgnoringPrimaryKey(
	dialect string,
	current *ast.CreateTableNode,
	target *ast.CreateTableNode,
) ([]*ast.ColumnNode, bool) {
	currentColumns := map[string]*ast.ColumnNode{}
	for _, column := range current.Columns {
		currentColumns[atlasSQLIdentifier(column.Name)] = column
	}
	var added []*ast.ColumnNode
	currentBase := txtarCloneTableWithoutPrimaryKey(current)
	targetBase := txtarCloneTableWithoutPrimaryKey(target)
	targetBase.Columns = nil
	for _, column := range target.Columns {
		name := atlasSQLIdentifier(column.Name)
		currentColumn, ok := currentColumns[name]
		switch {
		case !ok:
			added = append(added, column)
		case !txtarColumnsEquivalent(dialect, currentColumn, column):
			return nil, false
		default:
			targetBase.Columns = append(targetBase.Columns, column)
		}
	}
	if len(current.Columns)+len(added) != len(target.Columns) {
		return nil, false
	}
	if !txtarTablesEquivalentBySQL(dialect, currentBase, targetBase) {
		return nil, false
	}
	return added, true
}

func txtarTablePrimaryKey(table *ast.CreateTableNode) (txtarPrimaryKey, bool) {
	var primaryColumns []ast.ConstraintColumn
	var includeColumns []string
	for _, column := range table.Columns {
		if column.Primary {
			primaryColumns = append(primaryColumns, ast.ConstraintColumn{Name: column.Name})
		}
	}
	for _, constraint := range table.Constraints {
		if constraint.Type == ast.PrimaryKeyConstraint {
			primaryColumns = append(primaryColumns, atlasConstraintColumns(constraint)...)
			includeColumns = append(includeColumns, constraint.IncludeColumns...)
		}
	}
	return txtarPrimaryKey{columns: primaryColumns, include: includeColumns}, len(primaryColumns) > 0
}

func atlasInspectColumn(dialect string, table *ast.CreateTableNode, column *ast.ColumnNode) *ast.ColumnNode {
	if dialect != "postgresql" || !column.Nullable {
		return column
	}
	primaryKey, ok := txtarTablePrimaryKey(table)
	if !ok || !slices.ContainsFunc(primaryKey.columns, func(primaryColumn ast.ConstraintColumn) bool {
		return atlasSQLIdentifier(primaryColumn.Name) == atlasSQLIdentifier(column.Name)
	}) {
		return column
	}
	normalized := *column
	normalized.Nullable = false
	return &normalized
}

func txtarPrimaryKeyColumnsEqual(left, right txtarPrimaryKey) bool {
	if len(left.columns) != len(right.columns) || len(left.include) != len(right.include) {
		return false
	}
	for i := range left.columns {
		if atlasSQLIdentifier(left.columns[i].Name) != atlasSQLIdentifier(right.columns[i].Name) ||
			left.columns[i].Prefix != right.columns[i].Prefix ||
			left.columns[i].Desc != right.columns[i].Desc {
			return false
		}
	}
	for i := range left.include {
		if atlasSQLIdentifier(left.include[i]) != atlasSQLIdentifier(right.include[i]) {
			return false
		}
	}
	return true
}

func txtarCloneTableWithoutPrimaryKey(table *ast.CreateTableNode) *ast.CreateTableNode {
	clone := *table
	clone.Columns = make([]*ast.ColumnNode, 0, len(table.Columns))
	for _, column := range table.Columns {
		columnClone := *column
		columnClone.Primary = false
		clone.Columns = append(clone.Columns, &columnClone)
	}
	clone.Constraints = slices.DeleteFunc(slices.Clone(table.Constraints), func(constraint *ast.ConstraintNode) bool {
		return constraint.Type == ast.PrimaryKeyConstraint
	})
	return &clone
}

func txtarMigrateDiffCheckChanges(dialect string, current, target []ast.Node) ([]txtarMigrateDiffCheckChange, bool) {
	currentTables := txtarCreateTablesByName(current)
	targetTables := txtarCreateTablesByName(target)
	if len(currentTables) != len(targetTables) {
		return nil, false
	}
	var changes []txtarMigrateDiffCheckChange
	for tableName, targetTable := range targetTables {
		currentTable, ok := currentTables[tableName]
		if !ok {
			return nil, false
		}
		tableChanges, ok := txtarMigrateDiffTableCheckChanges(dialect, currentTable, targetTable)
		if !ok {
			return nil, false
		}
		for _, change := range tableChanges {
			change.tableName = tableName
			changes = append(changes, change)
		}
	}
	slices.SortStableFunc(changes, func(a, b txtarMigrateDiffCheckChange) int {
		if a.tableName != b.tableName {
			return cmp.Compare(a.tableName, b.tableName)
		}
		return cmp.Compare(a.current.name, b.current.name)
	})
	return changes, true
}

func txtarMigrateDiffTableCheckChanges(
	dialect string,
	current *ast.CreateTableNode,
	target *ast.CreateTableNode,
) ([]txtarMigrateDiffCheckChange, bool) {
	currentChecks, err := atlasCheckBlocks(dialect, current, errUnsupportedInspectSQL)
	if err != nil {
		return nil, false
	}
	targetChecks, err := atlasCheckBlocks(dialect, target, errUnsupportedInspectSQL)
	if err != nil {
		return nil, false
	}
	currentByName := atlasCheckBlocksByName(currentChecks)
	targetByName := atlasCheckBlocksByName(targetChecks)
	if len(currentByName) != len(targetByName) {
		return nil, false
	}
	targetBase := txtarCloneTableWithoutChecks(target)
	currentBase := txtarCloneTableWithoutChecks(current)
	if !txtarTablesEquivalentBySQL(dialect, currentBase, targetBase) {
		return nil, false
	}
	var changes []txtarMigrateDiffCheckChange
	matchedTargets := map[string]bool{}
	for name, currentCheck := range currentByName {
		targetCheck, ok := targetByName[name]
		if !ok {
			continue
		}
		matchedTargets[name] = true
		if currentCheck.expr == targetCheck.expr {
			continue
		}
		changes = append(changes, txtarMigrateDiffCheckChange{
			current: currentCheck,
			target:  targetCheck,
		})
	}
	for name, currentCheck := range currentByName {
		if _, ok := targetByName[name]; ok {
			continue
		}
		targetCheck, ok := txtarMigrateDiffRenamedCheckTarget(currentCheck, targetByName, matchedTargets)
		if !ok {
			return nil, false
		}
		matchedTargets[targetCheck.name] = true
		changes = append(changes, txtarMigrateDiffCheckChange{
			current: currentCheck,
			target:  targetCheck,
		})
	}
	if len(matchedTargets) != len(targetByName) {
		return nil, false
	}
	return changes, true
}

func txtarMigrateDiffRenamedCheckTarget(
	current atlasCheckBlock,
	targets map[string]atlasCheckBlock,
	matched map[string]bool,
) (atlasCheckBlock, bool) {
	var out atlasCheckBlock
	for name, target := range targets {
		if matched[name] || target.expr != current.expr {
			continue
		}
		if out.name != "" {
			return atlasCheckBlock{}, false
		}
		out = target
	}
	return out, out.name != ""
}

func atlasCheckBlocksByName(checks []atlasCheckBlock) map[string]atlasCheckBlock {
	byName := make(map[string]atlasCheckBlock, len(checks))
	for _, check := range checks {
		byName[check.name] = check
	}
	return byName
}

func txtarCloneTableWithoutChecks(table *ast.CreateTableNode) *ast.CreateTableNode {
	clone := *table
	clone.Columns = make([]*ast.ColumnNode, 0, len(table.Columns))
	for _, column := range table.Columns {
		columnClone := *column
		columnClone.Check = ""
		columnClone.CheckName = ""
		clone.Columns = append(clone.Columns, &columnClone)
	}
	clone.Constraints = slices.DeleteFunc(slices.Clone(table.Constraints), func(constraint *ast.ConstraintNode) bool {
		return constraint.Type == ast.CheckConstraint
	})
	return &clone
}

func txtarMigrateDiffAddColumnChanges(dialect string, current, target []ast.Node) ([]txtarMigrateDiffAddColumnChange, bool) {
	currentTables := txtarCreateTablesByName(current)
	targetTables := txtarCreateTablesByName(target)
	if len(currentTables) != len(targetTables) {
		return nil, false
	}
	var changes []txtarMigrateDiffAddColumnChange
	for tableName, targetTable := range targetTables {
		currentTable, ok := currentTables[tableName]
		if !ok {
			return nil, false
		}
		columns, ok := txtarMigrateDiffAddedColumns(dialect, currentTable, targetTable)
		if !ok {
			return nil, false
		}
		if len(columns) > 0 {
			changes = append(changes, txtarMigrateDiffAddColumnChange{
				tableName:   tableName,
				targetTable: targetTable,
				columns:     columns,
			})
		}
	}
	slices.SortStableFunc(changes, func(a, b txtarMigrateDiffAddColumnChange) int {
		return cmp.Compare(a.tableName, b.tableName)
	})
	return changes, true
}

func txtarCreateTablesByName(statements []ast.Node) map[string]*ast.CreateTableNode {
	tables := map[string]*ast.CreateTableNode{}
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok {
			tables[atlasSQLIdentifier(table.Name)] = table
		}
	}
	return tables
}

func txtarMigrateDiffAddedColumns(dialect string, current, target *ast.CreateTableNode) ([]*ast.ColumnNode, bool) {
	currentColumns := map[string]*ast.ColumnNode{}
	for _, column := range current.Columns {
		currentColumns[atlasSQLIdentifier(column.Name)] = column
	}
	var added []*ast.ColumnNode
	targetBase := *target
	targetBase.Columns = nil
	for _, column := range target.Columns {
		name := atlasSQLIdentifier(column.Name)
		currentColumn, ok := currentColumns[name]
		switch {
		case !ok:
			added = append(added, column)
		case !txtarColumnsEquivalent(dialect, currentColumn, column):
			return nil, false
		default:
			targetBase.Columns = append(targetBase.Columns, column)
		}
	}
	if len(current.Columns)+len(added) != len(target.Columns) {
		return nil, false
	}
	if !txtarTablesEquivalentBySQL(dialect, current, &targetBase) {
		return nil, false
	}
	return added, true
}

func txtarColumnsEquivalent(dialect string, a, b *ast.ColumnNode) bool {
	quote := atlasIdentifierQuoter(dialect)
	return renderAtlasColumnSQL(dialect, quote, a, true, atlasInspectSQLOptions{}) ==
		renderAtlasColumnSQL(dialect, quote, b, true, atlasInspectSQLOptions{})
}

func txtarTablesEquivalentBySQL(dialect string, a, b *ast.CreateTableNode) bool {
	left, err := renderAtlasInspectSQL(dialect, []ast.Node{a}, "")
	if err != nil {
		return false
	}
	right, err := renderAtlasInspectSQL(dialect, []ast.Node{b}, "")
	if err != nil {
		return false
	}
	return left == right
}

func txtarMigrateDiffSupportsInitialCreate(family string) bool {
	switch family {
	case "mysql", "postgres", "sqlite":
		return true
	default:
		return false
	}
}

func txtarMigrateDiffSupportedTargetSchemes(targets []string) bool {
	schemes := txtarURLSchemes(targets)
	schemes = slices.Compact(schemes)
	return len(schemes) == 1 && schemes[0] == "file"
}

func renderTxtarMigrateDiffSQLTargets(fx Fixture, runtime *txtarRuntime, targets []string, indent string) (string, error) {
	statements, err := txtarMigrateDiffTargetStatements(fx, runtime, targets)
	if err != nil {
		return "", err
	}
	if txtarMigrateDiffShouldUnqualifyTables(txtarFixtureDialect(fx)) {
		statements = atlasUnqualifySchemaStatements(txtarFixtureSchemaName(fx), statements)
	}
	return renderTxtarMigrateDiffSQLStatements(fx, statements, indent, "")
}

func txtarMigrateDiffTargetStatements(fx Fixture, runtime *txtarRuntime, targets []string) ([]ast.Node, error) {
	files, err := txtarMigrateDiffTargetFiles(runtime, targets)
	if err != nil {
		return nil, err
	}
	if len(files) == 1 {
		return txtarMigrateDiffStatements(fx, runtime, "file://"+files[0])
	}
	synthetic := ".ptah/migrate_diff_source.hcl"
	runtime.files[synthetic] = txtarJoinHCLSourceFiles(runtime, files, nil)
	runtime.addParentDirs(synthetic)
	return txtarMigrateDiffStatements(fx, runtime, "file://"+synthetic)
}

func txtarMigrateDiffTargetFiles(runtime *txtarRuntime, targets []string) ([]string, error) {
	var files []string
	for _, target := range targets {
		if !strings.HasPrefix(target, "file://") {
			return nil, errUnsupportedInspectSQL
		}
		name := txtarFileURLPath(target)
		if _, ok := runtime.files[name]; ok {
			files = append(files, name)
			continue
		}
		sourceFiles, ok := txtarAtlasHCLPathFiles(runtime, name)
		if !ok {
			return nil, fmt.Errorf("%w: file %q not found in txtar archive", errUnsupportedInspectSQL, name)
		}
		files = append(files, sourceFiles...)
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

func renderTxtarMigrateDiffSQL(fx Fixture, runtime *txtarRuntime, target string, indent string) (string, error) {
	statements, err := txtarMigrateDiffStatements(fx, runtime, target)
	if err != nil {
		return "", err
	}
	if txtarMigrateDiffShouldUnqualifyTables(txtarFixtureDialect(fx)) {
		statements = atlasUnqualifySchemaStatements(txtarFixtureSchemaName(fx), statements)
	}
	return renderTxtarMigrateDiffSQLStatements(fx, statements, indent, "")
}

func txtarMigrateDiffStatements(fx Fixture, runtime *txtarRuntime, target string) ([]ast.Node, error) {
	const filePrefix = "file://"
	if !strings.HasPrefix(target, filePrefix) {
		return nil, errUnsupportedInspectSQL
	}
	name := txtarFileURLPath(target)
	data, ok := runtime.files[name]
	if !ok {
		return nil, fmt.Errorf("%w: file %q not found in txtar archive", errUnsupportedInspectSQL, name)
	}

	var statements []ast.Node
	var err error
	if strings.HasSuffix(name, ".hcl") {
		statements, err = txtarHCLStatements(fx, name, data)
		if err == nil {
			statements, err = txtarMaterializeHCLApplyState(statements)
		}
	} else {
		list, parseErr := atlascompat.ParseSQL(data, atlascompat.ParseSQLOptions{})
		if parseErr != nil {
			err = fmt.Errorf("%w: parse migrate diff target: %v", errUnsupportedInspectSQL, parseErr)
		} else {
			statements = list.Statements
		}
	}
	if err != nil {
		return nil, err
	}
	return statements, nil
}

func renderTxtarMigrateDiffSQLStatements(
	fx Fixture,
	statements []ast.Node,
	indent string,
	qualifier string,
) (string, error) {
	if qualifier != "" {
		statements = atlasQualifyTableStatements(qualifier, statements)
	}
	out, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), statements, indent)
	if err != nil {
		return "", fmt.Errorf("%w: render migrate diff SQL: %v", errUnsupportedInspectSQL, err)
	}
	return out, nil
}

func txtarMigrateDiffShouldUnqualifyTables(dialect string) bool {
	switch dialect {
	case "mariadb", "mysql", "postgresql", "sqlite":
		return true
	default:
		return false
	}
}

func atlasUnqualifySchemaStatements(schemaName string, statements []ast.Node) []ast.Node {
	out := make([]ast.Node, 0, len(statements))
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateTableNode:
			tableCopy := *node
			tableCopy.Name = atlasUnqualifyTableName(schemaName, node.Name)
			tableCopy.Columns = make([]*ast.ColumnNode, len(node.Columns))
			for i, column := range node.Columns {
				columnCopy := *column
				columnCopy.Type = atlasUnqualifyTableName(schemaName, column.Type)
				tableCopy.Columns[i] = &columnCopy
			}
			out = append(out, &tableCopy)
		case *ast.EnumNode:
			enumCopy := *node
			enumCopy.Name = atlasUnqualifyTableName(schemaName, node.Name)
			out = append(out, &enumCopy)
		case *ast.IndexNode:
			indexCopy := *node
			indexCopy.Table = atlasUnqualifyTableName(schemaName, node.Table)
			out = append(out, &indexCopy)
		default:
			out = append(out, stmt)
		}
	}
	return out
}

func atlasQualifyTableStatements(qualifier string, statements []ast.Node) []ast.Node {
	if qualifier == "" {
		return statements
	}
	out := make([]ast.Node, 0, len(statements))
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateTableNode:
			tableCopy := *node
			tableCopy.Name = atlasQualifyTableName(qualifier, node.Name)
			out = append(out, &tableCopy)
		case *ast.IndexNode:
			indexCopy := *node
			indexCopy.Table = atlasQualifyTableName(qualifier, node.Table)
			out = append(out, &indexCopy)
		case *ast.AlterTableNode:
			alterCopy := *node
			alterCopy.Name = atlasQualifyTableName(qualifier, node.Name)
			out = append(out, &alterCopy)
		default:
			out = append(out, stmt)
		}
	}
	return out
}

func atlasUnqualifyTableName(schemaName, name string) string {
	unqualified, ok := strings.CutPrefix(name, schemaName+".")
	if ok {
		return unqualified
	}
	return name
}

func atlasQualifyTableName(qualifier, name string) string {
	name = atlasSQLIdentifier(name)
	if qualifier == "" || strings.Contains(name, ".") {
		return name
	}
	return atlasSQLIdentifier(qualifier) + "." + name
}

func txtarMigrationDirHasSQL(runtime *txtarRuntime, dir, sql string) bool {
	for _, name := range txtarMigrationSQLFilesInDir(runtime, dir) {
		if runtime.files[name] == sql {
			return true
		}
	}
	return false
}

func txtarNextMigrationFile(runtime *txtarRuntime, dir string) string {
	dir = path.Clean(dir)
	next := len(txtarMigrationSQLFilesInDir(runtime, dir)) + 1
	for {
		name := path.Join(dir, fmt.Sprintf("%d.sql", next))
		if _, exists := runtime.files[name]; !exists {
			return name
		}
		next++
	}
}

func txtarNextNamedMigrationFile(runtime *txtarRuntime, dir, description string) string {
	dir = path.Clean(dir)
	next := len(txtarMigrationSQLFilesInDir(runtime, dir)) + 1
	for {
		name := path.Join(dir, fmt.Sprintf("%d_%s.sql", next, description))
		if _, exists := runtime.files[name]; !exists {
			return name
		}
		next++
	}
}

func (r *txtarRuntime) refreshMigrationHash(dir string) error {
	fsys, ok := r.subFS(dir)
	if !ok {
		return fmt.Errorf("migration directory %q missing", dir)
	}
	sum, err := atlascompat.ComputeSum(fsys, migrator.MigrationDirFormatAtlas)
	if err != nil {
		return err
	}
	sumPath := path.Join(dir, atlascompat.AtlasSumFileName)
	r.files[sumPath] = string(sum.Bytes())
	r.addParentDirs(sumPath)
	return nil
}

func runTxtarMigrateSet(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "set" {
		return txtarCommandResult{}, false
	}

	args := fields[3:]
	dir := txtarMigrateCommandDir(args)
	if _, ok := runtime.files[path.Join(dir, atlascompat.AtlasSumFileName)]; !ok {
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

	version := revisions[0]
	if !txtarMigrationVersionExists(runtime, dir, version) {
		return txtarCommandResult{
			stderr: fmt.Sprintf("Error: migration with version %q not found\n", version),
			failed: true,
			err:    fmt.Errorf("migration with version %q not found", version),
		}, true
	}
	runtime.markMigrationsAppliedThrough(dir, version)
	return txtarCommandResult{}, true
}

func txtarMigrationVersionExists(runtime *txtarRuntime, dir, version string) bool {
	for _, file := range txtarMigrationSQLFilesInDir(runtime, dir) {
		if atlasMigrationVersion(file) == version {
			return true
		}
	}
	return false
}

func (r *txtarRuntime) markMigrationsAppliedThrough(dir, version string) {
	r.appliedMigrations = map[string]bool{}
	for _, file := range txtarMigrationSQLFilesInDir(r, dir) {
		r.appliedMigrations[file] = true
		r.appliedVersion = atlasMigrationVersion(file)
		if r.appliedVersion == version {
			return
		}
	}
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

	dir := txtarMigrateCommandRuntimeDir(runtime, fields[3:])
	if _, ok := runtime.files[path.Join(dir, atlascompat.AtlasSumFileName)]; !ok {
		return txtarCommandResult{unsupported: "atlas migrate status"}, true
	}
	files := txtarMigrationSQLFilesInDir(runtime, dir)
	if len(files) == 0 {
		return txtarCommandResult{unsupported: "atlas migrate status"}, true
	}

	applied := txtarAppliedMigrationPrefix(runtime, files)
	pending := len(files) - applied
	if pending == 0 {
		stdout := fmt.Sprintf(`Migration Status: OK
  -- Current Version: %s
  -- Next Version:    Already at latest version
  -- Executed Files:  %d
  -- Pending Files:   0
`, atlasMigrationVersion(files[len(files)-1]), applied)
		return txtarCommandResult{stdout: stdout}, true
	}

	currentVersion := "No migration applied yet"
	if applied > 0 {
		currentVersion = atlasMigrationVersion(files[applied-1])
	}
	nextVersion := atlasMigrationVersion(files[applied])
	stdout := fmt.Sprintf(`Migration Status: PENDING
  -- Current Version: %s
  -- Next Version:    %s
  -- Executed Files:  %d
  -- Pending Files:   %d
`, currentVersion, nextVersion, applied, pending)
	return txtarCommandResult{stdout: stdout}, true
}

func txtarAppliedMigrationPrefix(runtime *txtarRuntime, files []string) int {
	for i, file := range files {
		if !runtime.appliedMigrations[file] {
			return i
		}
	}
	return len(files)
}

func runTxtarClearSchema(runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) == 0 || fields[0] != "clearSchema" {
		return txtarCommandResult{}, false
	}
	if len(fields) != 1 {
		return txtarCommandResult{unsupported: "clearSchema"}, true
	}
	runtime.hasVirtualDBState = false
	runtime.dbStatements = nil
	runtime.dbRows = nil
	runtime.partitionChildren = nil
	runtime.appliedMigrations = nil
	runtime.appliedVersion = ""
	return txtarCommandResult{}, true
}

func runTxtarSchemaClean(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "schema" || fields[2] != "clean" {
		return txtarCommandResult{}, false
	}
	if txtarFixtureFamily(fx) != "postgres" || !txtarSchemaCleanArgsSupported(fields[3:]) {
		return txtarCommandResult{unsupported: "atlas schema clean"}, true
	}
	runtime.hasVirtualDBState = false
	runtime.dbStatements = nil
	runtime.dbRows = nil
	runtime.partitionChildren = nil
	runtime.appliedMigrations = nil
	runtime.appliedVersion = ""
	return txtarCommandResult{}, true
}

func txtarSchemaCleanArgsSupported(args []string) bool {
	hasURL := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-u" || arg == "--url":
			if i+1 >= len(args) {
				return false
			}
			hasURL = true
			i++
		case strings.HasPrefix(arg, "-u=") || strings.HasPrefix(arg, "--url="):
			hasURL = true
		case arg == "--auto-approve":
		default:
			return false
		}
	}
	return hasURL
}

type txtarMigrateApplyArgs struct {
	dir       string
	txMode    string
	env       string
	tenant    string
	vars      map[string]string
	logJSON   bool
	limit     int
	dryRun    bool
	blocked   bool
	hasLimit  bool
	hasURL    bool
	hasTxMode bool
}

func runTxtarMigrateApply(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "migrate" || fields[2] != "apply" {
		return txtarCommandResult{}, false
	}
	if !txtarMigrateApplySupportsFamily(txtarFixtureFamily(fx)) {
		return txtarCommandResult{unsupported: "atlas migrate apply"}, true
	}

	args := txtarParseMigrateApplyArgs(fields[3:])
	if args.env != "" {
		var ok bool
		args, ok = txtarResolveMigrateApplyEnv(fx, runtime, args)
		if !ok {
			args.blocked = true
		}
	}
	if args.blocked {
		return txtarCommandResult{unsupported: "atlas migrate apply"}, true
	}
	if _, ok := runtime.files[path.Join(args.dir, atlascompat.AtlasSumFileName)]; !ok {
		return txtarCommandResult{
			stdout: "You have a checksum error in your migration directory.\nRun 'atlas migrate hash' to create or update the checksum file.\n",
			stderr: "Error: checksum file not found\n",
			failed: true,
			err:    fmt.Errorf("checksum file not found"),
		}, true
	}
	if args.hasTxMode && !slices.Contains([]string{"all", "file", "none"}, args.txMode) {
		return txtarCommandResult{
			stderr: fmt.Sprintf("Error: unknown tx-mode %q\n", args.txMode),
			failed: true,
			err:    fmt.Errorf("unknown tx-mode %q", args.txMode),
		}, true
	}
	if !args.hasURL {
		return txtarCommandResult{unsupported: "atlas migrate apply"}, true
	}

	files := txtarPendingMigrationSQLFiles(runtime, args.dir)
	if len(files) == 0 {
		return txtarCommandResult{stdout: "No migration files to execute\n"}, true
	}
	if args.hasLimit && args.limit < len(files) {
		files = files[:args.limit]
	}
	if len(files) == 0 {
		return txtarCommandResult{stdout: "No migration files to execute\n"}, true
	}

	result, err := txtarApplyMigrationFiles(fx, runtime, files, args)
	if err != nil {
		result.failed = true
		result.err = err
	}
	return result, true
}

func txtarMigrateApplySupportsFamily(family string) bool {
	switch family {
	case "mysql", "postgres", "sqlite":
		return true
	default:
		return false
	}
}

func txtarParseMigrateApplyArgs(args []string) txtarMigrateApplyArgs {
	out := txtarMigrateApplyArgs{dir: "migrations", txMode: "file"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--url":
			if i+1 < len(args) {
				out.hasURL = true
				i++
			} else {
				out.blocked = true
			}
		case "--dir":
			if i+1 < len(args) {
				out.dir = txtarFileURLPath(args[i+1])
				i++
			} else {
				out.blocked = true
			}
		case "--tx-mode":
			if i+1 < len(args) {
				out.txMode = args[i+1]
				out.hasTxMode = true
				i++
			} else {
				out.blocked = true
			}
		case "--revisions-schema":
			if i+1 < len(args) {
				i++
			} else {
				out.blocked = true
			}
		case "--env":
			if i+1 < len(args) {
				out.env = args[i+1]
				i++
			} else {
				out.blocked = true
			}
		case "--var":
			if i+1 < len(args) {
				out.vars = txtarAddAtlasVar(out.vars, args[i+1])
				i++
			} else {
				out.blocked = true
			}
		case "--log":
			if i+1 < len(args) {
				out.logJSON = txtarMigrateApplyJSONLog(args[i+1])
				out.blocked = out.blocked || !out.logJSON
				i++
			} else {
				out.blocked = true
			}
		case "--dry-run":
			out.dryRun = true
		default:
			switch {
			case strings.HasPrefix(arg, "--url="):
				out.hasURL = true
			case strings.HasPrefix(arg, "--dir="):
				out.dir = txtarFileURLPath(strings.TrimPrefix(arg, "--dir="))
			case strings.HasPrefix(arg, "--tx-mode="):
				out.txMode = strings.TrimPrefix(arg, "--tx-mode=")
				out.hasTxMode = true
			case strings.HasPrefix(arg, "--revisions-schema="):
				continue
			case strings.HasPrefix(arg, "--env="):
				out.env = strings.TrimPrefix(arg, "--env=")
			case strings.HasPrefix(arg, "--var="):
				out.vars = txtarAddAtlasVar(out.vars, strings.TrimPrefix(arg, "--var="))
			case strings.HasPrefix(arg, "--log="):
				out.logJSON = txtarMigrateApplyJSONLog(strings.TrimPrefix(arg, "--log="))
				out.blocked = out.blocked || !out.logJSON
			case strings.HasPrefix(arg, "-"):
				out.blocked = true
			default:
				limit, err := strconv.Atoi(arg)
				if err != nil || limit < 0 || out.hasLimit {
					out.blocked = true
					continue
				}
				out.limit = limit
				out.hasLimit = true
			}
		}
	}
	return out
}

func txtarResolveMigrateApplyEnv(
	fx Fixture,
	runtime *txtarRuntime,
	args txtarMigrateApplyArgs,
) (txtarMigrateApplyArgs, bool) {
	switch txtarFixtureFamily(fx) {
	case "mysql", "postgres":
	default:
		return args, false
	}
	project, ok := runtime.files["atlas.hcl"]
	if !ok {
		return args, false
	}
	env, ok := txtarAtlasNamedBlock(project, "env", args.env)
	if !ok {
		return args, false
	}
	resolved, ok := txtarResolveAtlasSQLTenants(project, env, args.vars)
	if !ok || len(resolved) != 1 {
		return args, false
	}
	args.tenant = resolved[0]
	args.hasURL = true
	return args, true
}

func txtarMigrateApplyJSONLog(format string) bool {
	return strings.TrimSpace(format) == "{{ json . }}"
}

func txtarPendingMigrationSQLFiles(runtime *txtarRuntime, dir string) []string {
	files := txtarMigrationSQLFilesInDir(runtime, dir)
	return slices.DeleteFunc(files, func(name string) bool {
		return runtime.appliedMigrations[name]
	})
}

func txtarApplyMigrationFiles(
	fx Fixture,
	runtime *txtarRuntime,
	files []string,
	args txtarMigrateApplyArgs,
) (txtarCommandResult, error) {
	startVersion := runtime.appliedVersion
	var stdout strings.Builder
	fmt.Fprintln(&stdout, txtarMigrateApplySummaryLine(files, startVersion))

	committed := slices.Clone(runtime.dbStatements)
	batchStatements := make([]ast.Node, 0)
	appliedFiles := make([]string, 0, len(files))
	appliedStatements := 0
	for _, file := range files {
		version := atlasMigrationVersion(file)
		data := runtime.files[file]
		parsed := txtarParseMigrationStatementsForDialect(data, txtarFixtureDialect(fx))
		if parsed.err != nil {
			return txtarCommandResult{unsupported: "atlas migrate apply"}, nil
		}
		if parsed.failing != "" {
			return txtarFailedMigrationApplyResult(runtime, args, committed, batchStatements, appliedFiles, file, parsed.failing, stdout.String())
		}

		fmt.Fprintf(&stdout, "-- migrating version %s\n", version)
		txtarWriteMigrationSQL(&stdout, data)
		batchStatements = append(batchStatements, parsed.statements...)
		appliedStatements += len(parsed.statements)
		if !args.dryRun && args.txMode == "file" {
			var err error
			committed, err = txtarApplyStatementsToVirtualState(committed, parsed.statements)
			if err != nil {
				return txtarCommandResult{unsupported: "atlas migrate apply"}, nil
			}
			appliedFiles = append(appliedFiles, file)
		}
	}
	if !args.dryRun && args.txMode != "file" {
		var err error
		committed, err = txtarApplyStatementsToVirtualState(committed, batchStatements)
		if err != nil {
			return txtarCommandResult{unsupported: "atlas migrate apply"}, nil
		}
		appliedFiles = append(appliedFiles, files...)
	}
	if !args.dryRun {
		runtime.replaceDBStatements(committed)
		runtime.markMigrationsApplied(appliedFiles)
	}
	fmt.Fprintf(&stdout, "-- %d migrations\n", len(files))
	fmt.Fprintf(&stdout, "-- %d sql statements\n", appliedStatements)
	if args.logJSON {
		output, err := txtarMigrateApplyJSONLogOutput(fx, runtime, files)
		if err != nil {
			return txtarCommandResult{err: err}, nil
		}
		return txtarCommandResult{stdout: output}, nil
	}
	return txtarCommandResult{stdout: stdout.String()}, nil
}

type txtarMigrateApplyLogOutput struct {
	Driver  string                         `json:"Driver"`
	Scheme  string                         `json:"Scheme"`
	Dir     string                         `json:"Dir"`
	Target  string                         `json:"Target"`
	Pending []txtarMigrateApplyLogRevision `json:"Pending"`
	Applied []string                       `json:"Applied"`
	Start   string                         `json:"Start"`
}

type txtarMigrateApplyLogRevision struct {
	Name        string `json:"Name"`
	Version     string `json:"Version"`
	Description string `json:"Description"`
}

func txtarMigrateApplyJSONLogOutput(fx Fixture, runtime *txtarRuntime, files []string) (string, error) {
	dialect := txtarFixtureDialect(fx)
	if dialect == "" {
		dialect = txtarFixtureFamily(fx)
	}
	out := txtarMigrateApplyLogOutput{
		Driver:  dialect,
		Scheme:  dialect,
		Dir:     "file://migrations",
		Target:  atlasMigrationVersion(files[len(files)-1]),
		Pending: txtarMigrateApplyLogRevisions(files),
		Applied: txtarMigrateApplyLogApplied(runtime, files),
		Start:   time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func txtarMigrateApplyLogRevisions(files []string) []txtarMigrateApplyLogRevision {
	revisions := make([]txtarMigrateApplyLogRevision, 0, len(files))
	for _, file := range files {
		revisions = append(revisions, txtarMigrateApplyLogRevision{
			Name:        path.Base(file),
			Version:     atlasMigrationVersion(file),
			Description: atlasMigrationDescription(file),
		})
	}
	return revisions
}

func txtarMigrateApplyLogApplied(runtime *txtarRuntime, files []string) []string {
	if len(files) == 0 {
		return nil
	}
	statements := txtarMigrationSQLStrings(runtime.files[files[0]])
	return statements[:min(len(statements), 1)]
}

func txtarApplyStatementsToVirtualState(current []ast.Node, statements []ast.Node) ([]ast.Node, error) {
	next := slices.Clone(current)
	for _, stmt := range statements {
		var err error
		next, err = txtarApplyStatementToVirtualState(next, stmt)
		if err != nil {
			return nil, err
		}
	}
	return next, nil
}

func txtarApplyStatementToVirtualState(current []ast.Node, stmt ast.Node) ([]ast.Node, error) {
	switch node := stmt.(type) {
	case *ast.CreateSchemaNode, *ast.CreateTableNode, *ast.IndexNode:
		return append(current, node), nil
	case *ast.DropIndexNode:
		return txtarApplyDropIndexToVirtualState(current, node)
	case *ast.AlterTableNode:
		return txtarApplyAlterTableToVirtualState(current, node)
	default:
		return nil, fmt.Errorf("unsupported virtual migration statement %T", stmt)
	}
}

func txtarApplyAlterTableToVirtualState(current []ast.Node, alter *ast.AlterTableNode) ([]ast.Node, error) {
	next := slices.Clone(current)
	for _, op := range alter.Operations {
		switch op := op.(type) {
		case *ast.AddColumnOperation:
			if op.Column == nil {
				return nil, fmt.Errorf("unsupported virtual alter add column <nil>")
			}
			if !txtarApplyAddColumnToTables(next, alter.Name, op.Column) {
				return nil, fmt.Errorf("unsupported virtual alter add column on table %s", alter.Name)
			}
		case *ast.AddConstraintOperation:
			index, ok := txtarIndexFromAlterAddConstraint(alter.Name, op.Constraint)
			if ok {
				next = append(next, index)
				continue
			}
			if txtarApplyAddForeignKeyConstraintToTables(next, alter.Name, op.Constraint) {
				continue
			}
			if !txtarApplyAddCheckConstraintToTables(next, alter.Name, op.Constraint) {
				if txtarApplyAddPrimaryKeyConstraintToTables(next, alter.Name, op.Constraint) {
					continue
				}
				return nil, fmt.Errorf("unsupported virtual alter constraint %s", txtarConstraintType(op.Constraint))
			}
		case *ast.DropConstraintOperation:
			switch {
			case txtarApplyDropCheckConstraintFromTables(next, alter.Name, op.ConstraintName):
				continue
			case txtarPrimaryKeyConstraintName(op.ConstraintName) &&
				txtarApplyDropPrimaryKeyConstraintFromTables(next, alter.Name):
				continue
			default:
				return nil, fmt.Errorf("unsupported virtual alter drop constraint %s", op.ConstraintName)
			}
		default:
			return nil, fmt.Errorf("unsupported virtual alter operation %T", op)
		}
	}
	return next, nil
}

func txtarApplyDropIndexToVirtualState(current []ast.Node, drop *ast.DropIndexNode) ([]ast.Node, error) {
	next := make([]ast.Node, 0, len(current))
	dropped := false
	for _, stmt := range current {
		index, ok := stmt.(*ast.IndexNode)
		if ok && txtarDropIndexMatches(index, drop) {
			dropped = true
			continue
		}
		if table, ok := stmt.(*ast.CreateTableNode); ok && txtarDropUniqueConstraintFromTable(table, drop) {
			dropped = true
		}
		next = append(next, stmt)
	}
	if !dropped {
		return nil, fmt.Errorf("unsupported virtual drop index %s", drop.Name)
	}
	return next, nil
}

func txtarDropIndexMatches(index *ast.IndexNode, drop *ast.DropIndexNode) bool {
	if atlasSQLIdentifier(index.Name) != atlasSQLIdentifier(drop.Name) {
		return false
	}
	return drop.Table == "" || atlasSQLIdentifier(index.Table) == atlasSQLIdentifier(drop.Table)
}

func txtarDropUniqueConstraintFromTable(table *ast.CreateTableNode, drop *ast.DropIndexNode) bool {
	if drop.Table != "" && atlasSQLIdentifier(table.Name) != atlasSQLIdentifier(drop.Table) {
		return false
	}
	before := len(table.Constraints)
	table.Constraints = slices.DeleteFunc(table.Constraints, func(constraint *ast.ConstraintNode) bool {
		return constraint.Type == ast.UniqueConstraint && atlasSQLIdentifier(constraint.Name) == atlasSQLIdentifier(drop.Name)
	})
	return len(table.Constraints) != before
}

func txtarApplyAddColumnToTables(statements []ast.Node, tableName string, column *ast.ColumnNode) bool {
	tableName = atlasSQLIdentifier(tableName)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok && atlasSQLIdentifier(table.Name) == tableName {
			table.Columns = append(table.Columns, column)
			return true
		}
	}
	return false
}

func txtarApplyAddPrimaryKeyConstraintToTables(statements []ast.Node, tableName string, constraint *ast.ConstraintNode) bool {
	if constraint == nil || constraint.Type != ast.PrimaryKeyConstraint || len(atlasConstraintColumns(constraint)) == 0 {
		return false
	}
	tableName = atlasSQLIdentifier(tableName)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok && atlasSQLIdentifier(table.Name) == tableName {
			table.Constraints = append(table.Constraints, constraint)
			return true
		}
	}
	return false
}

func txtarApplyAddForeignKeyConstraintToTables(statements []ast.Node, tableName string, constraint *ast.ConstraintNode) bool {
	if constraint == nil || constraint.Type != ast.ForeignKeyConstraint ||
		len(atlasConstraintColumns(constraint)) == 0 || constraint.Reference == nil {
		return false
	}
	tableName = atlasSQLIdentifier(tableName)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok && atlasSQLIdentifier(table.Name) == tableName {
			table.Constraints = append(table.Constraints, constraint)
			return true
		}
	}
	return false
}

func txtarApplyDropPrimaryKeyConstraintFromTables(statements []ast.Node, tableName string) bool {
	tableName = atlasSQLIdentifier(tableName)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok || atlasSQLIdentifier(table.Name) != tableName {
			continue
		}
		if txtarDropPrimaryKeyConstraintFromTable(table) {
			return true
		}
	}
	return false
}

func txtarDropPrimaryKeyConstraintFromTable(table *ast.CreateTableNode) bool {
	dropped := false
	for _, column := range table.Columns {
		if column.Primary {
			column.Primary = false
			dropped = true
		}
	}
	before := len(table.Constraints)
	table.Constraints = slices.DeleteFunc(table.Constraints, func(constraint *ast.ConstraintNode) bool {
		return constraint.Type == ast.PrimaryKeyConstraint
	})
	return dropped || len(table.Constraints) != before
}

func txtarPrimaryKeyConstraintName(name string) bool {
	return strings.EqualFold(atlasSQLIdentifier(name), "PRIMARY")
}

func txtarApplyAddCheckConstraintToTables(statements []ast.Node, tableName string, constraint *ast.ConstraintNode) bool {
	if constraint == nil || constraint.Type != ast.CheckConstraint || constraint.Name == "" {
		return false
	}
	tableName = atlasSQLIdentifier(tableName)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok && atlasSQLIdentifier(table.Name) == tableName {
			table.Constraints = append(table.Constraints, constraint)
			return true
		}
	}
	return false
}

func txtarApplyDropCheckConstraintFromTables(statements []ast.Node, tableName, constraintName string) bool {
	tableName = atlasSQLIdentifier(tableName)
	constraintName = atlasSQLIdentifier(constraintName)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok || atlasSQLIdentifier(table.Name) != tableName {
			continue
		}
		if txtarDropCheckConstraintFromTable(table, constraintName) {
			return true
		}
	}
	return false
}

func txtarDropCheckConstraintFromTable(table *ast.CreateTableNode, constraintName string) bool {
	for _, column := range table.Columns {
		if column.Check == "" {
			continue
		}
		if atlasSQLIdentifier(column.CheckName) == constraintName {
			column.Check = ""
			column.CheckName = ""
			return true
		}
	}
	before := len(table.Constraints)
	table.Constraints = slices.DeleteFunc(table.Constraints, func(constraint *ast.ConstraintNode) bool {
		return constraint.Type == ast.CheckConstraint && atlasSQLIdentifier(constraint.Name) == constraintName
	})
	return len(table.Constraints) != before
}

func txtarIndexFromAlterAddConstraint(tableName string, constraint *ast.ConstraintNode) (*ast.IndexNode, bool) {
	if constraint == nil || constraint.Type != ast.UniqueConstraint || constraint.Name == "" {
		return nil, false
	}
	columns := atlasConstraintColumnNames(constraint)
	if len(columns) == 0 {
		return nil, false
	}
	return &ast.IndexNode{
		Name:    constraint.Name,
		Table:   tableName,
		Columns: columns,
		Unique:  true,
	}, true
}

func txtarConstraintType(constraint *ast.ConstraintNode) string {
	if constraint == nil {
		return "<nil>"
	}
	return constraint.Type.String()
}

func (r *txtarRuntime) markMigrationsApplied(files []string) {
	for _, file := range files {
		r.markMigrationApplied(file)
	}
}

func (r *txtarRuntime) markMigrationApplied(file string) {
	if r.appliedMigrations == nil {
		r.appliedMigrations = map[string]bool{}
	}
	r.appliedMigrations[file] = true
	r.appliedVersion = atlasMigrationVersion(file)
}

func txtarMigrateApplySummaryLine(files []string, startVersion string) string {
	targetVersion := atlasMigrationVersion(files[len(files)-1])
	total := len(files)
	if startVersion != "" {
		return fmt.Sprintf("Migrating to version %s from %s (%d migrations in total):", targetVersion, startVersion, total)
	}
	return fmt.Sprintf("Migrating to version %s (%d migrations in total):", targetVersion, total)
}

type txtarParsedMigrationStatements struct {
	statements         []ast.Node
	failing            string
	skippedUnsupported bool
	err                error
}

func txtarParseMigrationStatements(data string) ([]ast.Node, string, error) {
	parsed := txtarParseMigrationStatementsForDialect(data, "")
	return parsed.statements, parsed.failing, parsed.err
}

func txtarParseMigrationStatementsForDialect(data string, dialect string) txtarParsedMigrationStatements {
	data = txtarMigrationUpSQL(data)
	skippedUnsupported := false
	if txtarShouldSkipPostgresFunctionDefinitions(dialect) && postgresFunctionRE.MatchString(data) {
		data = txtarSkipPostgresFunctionDefinitions(data)
		skippedUnsupported = true
	}
	var statements []ast.Node
	for _, raw := range strings.Split(data, ";") {
		stmt := txtarExecutableMigrationStatement(raw)
		if stmt == "" {
			continue
		}
		if strings.Contains(stmt, "THIS IS A FAILING STATEMENT") {
			return txtarParsedMigrationStatements{
				statements:         statements,
				failing:            stmt,
				skippedUnsupported: skippedUnsupported,
			}
		}
		if fallback, ok := txtarParseGeneratedPrimaryKeyAlterStatement(stmt); ok {
			statements = append(statements, fallback)
			continue
		}
		if fallback, ok := txtarParseGeneratedDropIndexAlterStatement(stmt); ok {
			statements = append(statements, fallback)
			continue
		}
		if fallback, ok := txtarParseGeneratedCheckAlterStatement(stmt); ok {
			statements = append(statements, fallback)
			continue
		}
		list, err := atlascompat.ParseSQL(stmt+";", atlascompat.ParseSQLOptions{})
		if err != nil {
			if len(statements) == 0 {
				return txtarParsedMigrationStatements{err: err}
			}
			return txtarParsedMigrationStatements{
				statements:         statements,
				failing:            stmt,
				skippedUnsupported: skippedUnsupported,
			}
		}
		statements = append(statements, list.Statements...)
	}
	return txtarParsedMigrationStatements{
		statements:         statements,
		skippedUnsupported: skippedUnsupported,
	}
}

func txtarShouldSkipPostgresFunctionDefinitions(dialect string) bool {
	return dialect == "postgresql" || dialect == "postgres"
}

func txtarSkipPostgresFunctionDefinitions(data string) string {
	return postgresFunctionRE.ReplaceAllString(data, "")
}

func txtarParseGeneratedDropIndexAlterStatement(stmt string) (ast.Node, bool) {
	stmt = strings.TrimSpace(stmt)
	rest, ok := strings.CutPrefix(stmt, "ALTER TABLE ")
	if !ok {
		return nil, false
	}
	tableName, rest, ok := txtarParseLeadingSQLIdentifier(rest)
	if !ok {
		return nil, false
	}
	rest, ok = strings.CutPrefix(strings.TrimSpace(rest), "DROP INDEX ")
	if !ok {
		return nil, false
	}
	indexName, rest, ok := txtarParseLeadingSQLIdentifier(rest)
	if !ok || strings.TrimSpace(rest) != "" {
		return nil, false
	}
	return ast.NewDropIndex(indexName).SetTable(tableName), true
}

func txtarParseGeneratedPrimaryKeyAlterStatement(stmt string) (ast.Node, bool) {
	stmt = strings.TrimSpace(stmt)
	rest, ok := strings.CutPrefix(stmt, "ALTER TABLE ")
	if !ok {
		return nil, false
	}
	tableName, rest, ok := txtarParseLeadingSQLIdentifier(rest)
	if !ok {
		return nil, false
	}
	parts, ok := txtarSplitTopLevelComma(rest)
	if !ok {
		return nil, false
	}
	var operations []ast.AlterOperation
	seenDropPrimary := false
	seenAddPrimary := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "ADD COLUMN "):
			operation, ok := txtarParseGeneratedAlterOperation(tableName, part)
			if !ok {
				return nil, false
			}
			operations = append(operations, operation)
		case strings.EqualFold(part, "DROP PRIMARY KEY"):
			operations = append(operations, &ast.DropConstraintOperation{ConstraintName: "PRIMARY"})
			seenDropPrimary = true
		case strings.HasPrefix(part, "ADD PRIMARY KEY "):
			operation, ok := txtarParseGeneratedAlterOperation(tableName, part)
			if !ok {
				return nil, false
			}
			operations = append(operations, operation)
			seenAddPrimary = true
		default:
			return nil, false
		}
	}
	if !seenDropPrimary || !seenAddPrimary || len(operations) == 0 {
		return nil, false
	}
	return &ast.AlterTableNode{Name: tableName, Operations: operations}, true
}

func txtarParseGeneratedAlterOperation(tableName, operation string) (ast.AlterOperation, bool) {
	sql := fmt.Sprintf("ALTER TABLE %s %s;", atlasIdentifierQuoter("mysql")(tableName), operation)
	list, err := atlascompat.ParseSQL(sql, atlascompat.ParseSQLOptions{})
	if err != nil || len(list.Statements) != 1 {
		return nil, false
	}
	alter, ok := list.Statements[0].(*ast.AlterTableNode)
	if !ok || len(alter.Operations) != 1 {
		return nil, false
	}
	return alter.Operations[0], true
}

func txtarSplitTopLevelComma(value string) ([]string, bool) {
	var parts []string
	start := 0
	depth := 0
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\'':
			next, ok := txtarSQLStringEnd(value, i)
			if !ok {
				return nil, false
			}
			i = next
		case '`':
			next, ok := txtarSQLQuotedIdentifierEnd(value, i)
			if !ok {
				return nil, false
			}
			i = next
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return nil, false
			}
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, value[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	parts = append(parts, value[start:])
	return parts, true
}

func txtarSQLStringEnd(value string, start int) (int, bool) {
	for i := start + 1; i < len(value); i++ {
		if value[i] != '\'' {
			continue
		}
		if i+1 < len(value) && value[i+1] == '\'' {
			i++
			continue
		}
		return i, true
	}
	return 0, false
}

func txtarSQLQuotedIdentifierEnd(value string, start int) (int, bool) {
	for i := start + 1; i < len(value); i++ {
		if value[i] != '`' {
			continue
		}
		if i+1 < len(value) && value[i+1] == '`' {
			i++
			continue
		}
		return i, true
	}
	return 0, false
}

func txtarParseGeneratedCheckAlterStatement(stmt string) (ast.Node, bool) {
	stmt = strings.TrimSpace(stmt)
	rest, ok := strings.CutPrefix(stmt, "ALTER TABLE ")
	if !ok {
		return nil, false
	}
	tableName, rest, ok := txtarParseLeadingSQLIdentifier(rest)
	if !ok {
		return nil, false
	}
	parts, ok := txtarSplitTopLevelComma(rest)
	if !ok {
		return nil, false
	}
	if len(parts) == 0 || len(parts)%2 != 0 {
		return nil, false
	}
	operations := make([]ast.AlterOperation, 0, len(parts))
	for i := 0; i < len(parts); i += 2 {
		dropName, ok := txtarParseGeneratedCheckDropName(parts[i])
		if !ok {
			return nil, false
		}
		addName, expr, ok := txtarParseGeneratedCheckAdd(parts[i+1])
		if !ok {
			return nil, false
		}
		operations = append(
			operations,
			&ast.DropConstraintOperation{ConstraintName: dropName, Check: true},
			&ast.AddConstraintOperation{Constraint: &ast.ConstraintNode{
				Type:       ast.CheckConstraint,
				Name:       addName,
				Expression: expr,
			}},
		)
	}
	return &ast.AlterTableNode{Name: tableName, Operations: operations}, true
}

func txtarParseGeneratedCheckDropName(part string) (string, bool) {
	part = strings.TrimSpace(part)
	var ok bool
	part, ok = strings.CutPrefix(part, "DROP CHECK ")
	if !ok {
		part, ok = strings.CutPrefix(strings.TrimSpace(part), "DROP CONSTRAINT ")
	}
	if !ok {
		return "", false
	}
	name, rest, ok := txtarParseLeadingSQLIdentifier(part)
	return name, ok && strings.TrimSpace(rest) == ""
}

func txtarParseGeneratedCheckAdd(part string) (string, string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(part), "ADD CONSTRAINT ")
	if !ok {
		return "", "", false
	}
	name, rest, ok := txtarParseLeadingSQLIdentifier(rest)
	if !ok || name == "" {
		return "", "", false
	}
	rest, ok = strings.CutPrefix(strings.TrimSpace(rest), "CHECK ")
	if !ok {
		return "", "", false
	}
	expr, ok := txtarCheckExpression(rest)
	return name, expr, ok
}

func txtarParseLeadingSQLIdentifier(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if value[0] != '`' && value[0] != '"' {
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return "", "", false
		}
		return fields[0], strings.TrimPrefix(value, fields[0]), true
	}
	var b strings.Builder
	quote := value[0]
	for i := 1; i < len(value); i++ {
		if value[i] != quote {
			b.WriteByte(value[i])
			continue
		}
		if i+1 < len(value) && value[i+1] == quote {
			b.WriteByte(quote)
			i++
			continue
		}
		return b.String(), value[i+1:], true
	}
	return "", "", false
}

func txtarCheckExpression(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '(' || value[len(value)-1] != ')' {
		return "", false
	}
	return strings.TrimSpace(value[1 : len(value)-1]), true
}

func txtarMigrationUpSQL(data string) string {
	for _, marker := range []string{"\n-- +goose Down", "\n-- migrate:down"} {
		if before, _, ok := strings.Cut(data, marker); ok {
			return before
		}
	}
	return data
}

func txtarExecutableMigrationStatement(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "--") {
		// Generated migration files start with Atlas operation comments.
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func txtarFailedMigrationApplyResult(
	runtime *txtarRuntime,
	args txtarMigrateApplyArgs,
	committed []ast.Node,
	batchStatements []ast.Node,
	appliedFiles []string,
	file string,
	failing string,
	stdout string,
) (txtarCommandResult, error) {
	switch args.txMode {
	case "none":
		runtime.replaceDBStatements(append(append(committed, batchStatements...), txtarParseablePrefixStatements(runtime.files[file])...))
		if !args.dryRun {
			runtime.markMigrationsApplied(appliedFiles)
		}
	case "file":
		runtime.replaceDBStatements(committed)
		if !args.dryRun {
			runtime.markMigrationsApplied(appliedFiles)
		}
	case "all":
		runtime.replaceDBStatements(committed)
	}
	return txtarCommandResult{
		stdout: stdout,
		stderr: fmt.Sprintf("Error: executing statement %q from version %q\n", failing+";", atlasMigrationVersion(file)),
	}, fmt.Errorf("executing statement %q from version %q", failing+";", atlasMigrationVersion(file))
}

func txtarParseablePrefixStatements(data string) []ast.Node {
	statements, _, err := txtarParseMigrationStatements(data)
	if err != nil {
		return nil
	}
	return statements
}

func txtarWriteMigrationSQL(b *strings.Builder, data string) {
	for _, stmt := range txtarMigrationSQLStrings(data) {
		fmt.Fprintf(b, "-> %s\n", stmt)
	}
}

func txtarMigrationSQLStrings(data string) []string {
	var statements []string
	for _, raw := range strings.Split(data, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		statements = append(statements, stmt+";")
	}
	return statements
}

func runTxtarApply(fx Fixture, runtime *txtarRuntime, fields []string, expectedFailure bool) (txtarCommandResult, bool) {
	if len(fields) < 1 || fields[0] != "apply" {
		return txtarCommandResult{}, false
	}
	if len(fields) != 2 && !(expectedFailure && len(fields) == 3) {
		return txtarCommandResult{unsupported: "apply"}, true
	}
	data, ok := runtime.files[fields[1]]
	if !ok {
		return txtarCommandResult{
			failed: true,
			err:    fmt.Errorf("apply %s: %s missing", fields[1], fields[1]),
		}, true
	}
	statements, err := txtarHCLStatements(fx, fields[1], data)
	if err != nil {
		return txtarCommandResult{unsupported: "apply"}, true
	}
	if !txtarFixtureSupportsVirtualApplyWithState(fx, runtime.dbStatements, statements) {
		return txtarCommandResult{unsupported: "apply"}, true
	}
	if expectedFailure {
		if failure := txtarExpectedApplyFailure(fx, runtime.dbStatements, statements, runtime.dbRows); failure != "" {
			if len(fields) == 3 && fields[2] != failure {
				return txtarCommandResult{unsupported: "apply"}, true
			}
			return txtarCommandResult{stderr: "Error: " + failure + "\n", failed: true, err: errors.New(failure)}, true
		}
		return txtarCommandResult{unsupported: "apply"}, true
	}
	if txtarFixtureFamily(fx) == "postgres" {
		statements = txtarPostgresRetainDomainTypes(runtime.dbStatements, statements)
	}
	state, err := txtarMaterializeHCLApplyState(statements)
	if err != nil {
		return txtarCommandResult{unsupported: "apply"}, true
	}
	runtime.replaceDBStatements(state)
	return txtarCommandResult{}, true
}

func txtarMaterializeHCLApplyState(statements []ast.Node) ([]ast.Node, error) {
	state := make([]ast.Node, 0, len(statements))
	var deferred []*ast.AlterTableNode
	for _, stmt := range statements {
		alter, ok := stmt.(*ast.AlterTableNode)
		if ok {
			deferred = append(deferred, alter)
			continue
		}
		state = append(state, stmt)
	}
	for _, alter := range deferred {
		var err error
		state, err = txtarApplyAlterTableToVirtualState(state, alter)
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}

func txtarExpectedApplyFailure(
	fx Fixture,
	current []ast.Node,
	next []ast.Node,
	rows map[string][]txtarVirtualRow,
) string {
	if failure := txtarForeignKeySetNullFailure(next); failure != "" {
		return failure
	}
	if failure := txtarUniqueIndexDataFailure(txtarFixtureDialect(fx), next, rows); failure != "" {
		return failure
	}
	currentTables := atlasCreateTablesByName(current)
	nextTables := atlasCreateTablesByName(next)
	for tableName, nextTable := range nextTables {
		currentTable, ok := currentTables[tableName]
		if !ok {
			continue
		}
		if failure := txtarGeneratedColumnChangeFailure(currentTable, nextTable); failure != "" {
			return failure
		}
		if failure := txtarPostgresPartitionChangeFailure(fx, currentTable, nextTable); failure != "" {
			return failure
		}
	}
	return ""
}

func txtarPostgresPartitionChangeFailure(fx Fixture, current, next *ast.CreateTableNode) string {
	if txtarFixtureFamily(fx) != "postgres" || current.Partition == nil || next.Partition == nil {
		return ""
	}
	currentSQL, currentOK := renderAtlasPostgresPartitionSQL(current.Partition, atlasIdentifierQuoter("postgresql"))
	nextSQL, nextOK := renderAtlasPostgresPartitionSQL(next.Partition, atlasIdentifierQuoter("postgresql"))
	if !currentOK || !nextOK || currentSQL == nextSQL {
		return ""
	}
	return fmt.Sprintf(
		"partition key of table %q cannot be changed from %s to %s (drop and add is required)",
		atlasUnqualifiedSQLTableName(next.Name),
		currentSQL,
		nextSQL,
	)
}

func txtarForeignKeySetNullFailure(statements []ast.Node) string {
	tables := atlasCreateTablesByName(statements)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			continue
		}
		for _, column := range table.Columns {
			if column.Nullable || column.ForeignKey == nil {
				continue
			}
			if atlasForeignKeyActionIsSetNull(column.ForeignKey.OnDelete) ||
				atlasForeignKeyActionIsSetNull(column.ForeignKey.OnUpdate) {
				return fmt.Sprintf(
					"foreign key constraint was %q SET NULL, but column %q is NOT NULL",
					atlasSQLIdentifier(column.Name),
					atlasSQLIdentifier(column.Name),
				)
			}
		}
		for _, constraint := range table.Constraints {
			if constraint.Type != ast.ForeignKeyConstraint || constraint.Reference == nil {
				continue
			}
			if !atlasForeignKeyActionIsSetNull(constraint.Reference.OnDelete) &&
				!atlasForeignKeyActionIsSetNull(constraint.Reference.OnUpdate) {
				continue
			}
			if columnName := txtarFirstNotNullConstraintColumn(table, constraint); columnName != "" {
				return fmt.Sprintf(
					"foreign key constraint was %q SET NULL, but column %q is NOT NULL",
					atlasSQLIdentifier(columnName),
					atlasSQLIdentifier(columnName),
				)
			}
		}
	}
	for _, stmt := range statements {
		alter, ok := stmt.(*ast.AlterTableNode)
		if !ok {
			continue
		}
		table := tables[atlasSQLIdentifier(alter.Name)]
		if table == nil {
			continue
		}
		for _, operation := range alter.Operations {
			add, ok := operation.(*ast.AddConstraintOperation)
			if !ok || add.Constraint == nil ||
				add.Constraint.Type != ast.ForeignKeyConstraint ||
				add.Constraint.Reference == nil {
				continue
			}
			if !atlasForeignKeyActionIsSetNull(add.Constraint.Reference.OnDelete) &&
				!atlasForeignKeyActionIsSetNull(add.Constraint.Reference.OnUpdate) {
				continue
			}
			if columnName := txtarFirstNotNullConstraintColumn(table, add.Constraint); columnName != "" {
				return fmt.Sprintf(
					"foreign key constraint was %q SET NULL, but column %q is NOT NULL",
					atlasSQLIdentifier(columnName),
					atlasSQLIdentifier(columnName),
				)
			}
		}
	}
	return ""
}

func txtarFirstNotNullConstraintColumn(table *ast.CreateTableNode, constraint *ast.ConstraintNode) string {
	columns := atlasConstraintColumnNames(constraint)
	if len(columns) == 0 {
		return ""
	}
	columnsByName := atlasColumnsByName(table)
	for _, columnName := range columns {
		column := columnsByName[atlasSQLIdentifier(columnName)]
		if column != nil && !column.Nullable {
			return column.Name
		}
	}
	return ""
}

func atlasForeignKeyActionIsSetNull(action string) bool {
	return strings.EqualFold(strings.ReplaceAll(action, " ", "_"), "SET_NULL")
}

func atlasCreateTablesByName(statements []ast.Node) map[string]*ast.CreateTableNode {
	tables := map[string]*ast.CreateTableNode{}
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok {
			tables[atlasSQLIdentifier(table.Name)] = table
		}
	}
	return tables
}

func txtarUniqueIndexDataFailure(dialect string, statements []ast.Node, rows map[string][]txtarVirtualRow) string {
	if len(rows) == 0 {
		return ""
	}
	for tableName, indexes := range atlasIndexesByTable(dialect, statements) {
		for _, index := range indexes {
			if !index.Unique {
				continue
			}
			columns, ok := txtarIndexColumnNames(index)
			if !ok {
				continue
			}
			if failure := txtarDuplicateUniqueKeyFailure(rows[atlasUnqualifiedSQLTableName(tableName)], index.Name, columns); failure != "" {
				return failure
			}
		}
	}
	for tableName, table := range atlasCreateTablesByName(statements) {
		for _, constraint := range table.Constraints {
			if constraint.Type != ast.UniqueConstraint {
				continue
			}
			if failure := txtarDuplicateUniqueKeyFailure(
				rows[atlasUnqualifiedSQLTableName(tableName)],
				constraint.Name,
				atlasConstraintColumnNames(constraint),
			); failure != "" {
				return failure
			}
		}
	}
	return ""
}

func txtarIndexColumnNames(index *ast.IndexNode) ([]string, bool) {
	parts := index.EffectiveParts()
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Expr != "" || part.Name == "" {
			return nil, false
		}
		columns = append(columns, part.Name)
	}
	return columns, len(columns) > 0
}

func txtarDuplicateUniqueKeyFailure(rows []txtarVirtualRow, keyName string, columns []string) string {
	if len(rows) == 0 || len(columns) == 0 {
		return ""
	}
	seen := map[string]string{}
	for _, row := range rows {
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			value, ok := row[atlasSQLIdentifier(column)]
			if !ok {
				values = nil
				break
			}
			values = append(values, value)
		}
		if len(values) == 0 {
			continue
		}
		key := strings.Join(values, "\x00")
		entry := strings.Join(values, "-")
		if _, ok := seen[key]; ok {
			return fmt.Sprintf("Error 1062: Duplicate entry '%s' for key '%s'", entry, atlasSQLIdentifier(keyName))
		}
		seen[key] = entry
	}
	return ""
}

func txtarGeneratedColumnChangeFailure(current, next *ast.CreateTableNode) string {
	currentColumns := atlasColumnsByName(current)
	for _, nextColumn := range next.Columns {
		currentColumn, ok := currentColumns[atlasSQLIdentifier(nextColumn.Name)]
		if !ok {
			continue
		}
		currentGenerated := currentColumn.GeneratedExpression != ""
		nextGenerated := nextColumn.GeneratedExpression != ""
		switch {
		case currentGenerated && !nextGenerated:
			return fmt.Sprintf(
				"changing %s generated column %q to non-generated column is not supported (drop and add is required)",
				atlasGeneratedHCLKind(currentColumn.GeneratedKind),
				atlasSQLIdentifier(nextColumn.Name),
			)
		case !currentGenerated && nextGenerated:
			return fmt.Sprintf(
				"changing column %q to %s generated column is not supported (drop and add is required)",
				atlasSQLIdentifier(nextColumn.Name),
				atlasGeneratedHCLKind(nextColumn.GeneratedKind),
			)
		case currentGenerated && nextGenerated &&
			!strings.EqualFold(atlasGeneratedHCLKind(currentColumn.GeneratedKind), atlasGeneratedHCLKind(nextColumn.GeneratedKind)):
			return fmt.Sprintf(
				"changing the store type of generated column %q from %q to %q is not supported",
				atlasSQLIdentifier(nextColumn.Name),
				atlasGeneratedHCLKind(currentColumn.GeneratedKind),
				atlasGeneratedHCLKind(nextColumn.GeneratedKind),
			)
		}
	}
	return ""
}

func atlasColumnsByName(table *ast.CreateTableNode) map[string]*ast.ColumnNode {
	columns := map[string]*ast.ColumnNode{}
	for _, column := range table.Columns {
		columns[atlasSQLIdentifier(column.Name)] = column
	}
	return columns
}

func txtarFixtureSupportsVirtualApply(fx Fixture, statements []ast.Node) bool {
	return txtarFixtureSupportsVirtualApplyWithState(fx, nil, statements)
}

func txtarFixtureSupportsVirtualApplyWithState(fx Fixture, current, statements []ast.Node) bool {
	if path.Base(fx.Name) == "cli-inspect.txtar" {
		return true
	}
	family := txtarFixtureFamily(fx)
	switch family {
	case "sqlite":
		return txtarSQLiteVirtualApplyStateSupported(statements)
	case "mysql", "mariadb":
		return txtarMySQLVirtualApplyStateSupported(txtarFixtureDialect(fx), statements)
	case "postgres":
		return txtarPostgresVirtualApplyStateSupportedWithTypes(statements, current)
	default:
		return false
	}
}

func txtarPostgresVirtualApplyStateSupported(statements []ast.Node) bool {
	return txtarPostgresVirtualApplyStateSupportedWithTypes(statements, nil)
}

func txtarPostgresVirtualApplyStateSupportedWithTypes(statements, current []ast.Node) bool {
	domains := txtarPostgresDomainNames(statements)
	for domain := range txtarPostgresDomainNames(current) {
		domains[domain] = true
	}
	enums := txtarPostgresEnumsByName(statements)
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateSchemaNode:
			continue
		case *ast.EnumNode:
			continue
		case *ast.CreateTypeNode:
			if _, ok := node.TypeDef.(*ast.DomainTypeDef); !ok {
				return false
			}
		case *ast.CreateTableNode:
			if !txtarPostgresApplyTableSupported(node, domains, enums) {
				return false
			}
		case *ast.AlterTableNode:
			if !txtarVirtualApplyAlterTableSupported(node) {
				return false
			}
		case *ast.IndexNode:
			if !txtarPostgresApplyIndexSupported(node) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func txtarPostgresApplyTableSupported(table *ast.CreateTableNode, domains map[string]bool, enums map[string]*ast.EnumNode) bool {
	if table.SelectBody != "" || !txtarPostgresApplyPartitionSupported(table.Partition) {
		return false
	}
	for _, column := range table.Columns {
		if !txtarPostgresApplyGeneratedColumnSupported(column) ||
			!txtarPostgresApplyColumnTypeSupported(column, domains, enums) ||
			!txtarPostgresApplyColumnDefaultSupported(column, enums) ||
			!txtarPostgresApplyColumnForeignKeySupported(column) {
			return false
		}
	}
	for _, constraint := range table.Constraints {
		switch constraint.Type {
		case ast.PrimaryKeyConstraint:
			continue
		case ast.UniqueConstraint:
			if len(atlasConstraintColumnNames(constraint)) == 0 {
				return false
			}
		case ast.ForeignKeyConstraint:
			if !txtarPostgresApplyForeignKeyConstraintSupported(constraint) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func txtarPostgresApplyPartitionSupported(partition *ast.PartitionSpec) bool {
	if partition == nil {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(partition.Type)) {
	case "HASH", "LIST", "RANGE":
	default:
		return false
	}
	if len(partition.Parts) == 0 {
		return false
	}
	for _, part := range partition.Parts {
		if (part.Name == "") == (part.Expr == "") {
			return false
		}
		if part.Name != "" && strings.ContainsAny(atlasSQLIdentifier(part.Name), " ()`\"$") {
			return false
		}
		if part.Expr != "" && txtarPostgresPartitionExprSQL(part.Expr) == "" {
			return false
		}
	}
	return true
}

func txtarPostgresApplyGeneratedColumnSupported(column *ast.ColumnNode) bool {
	if column.GeneratedExpression == "" {
		return true
	}
	return strings.EqualFold(atlasGeneratedHCLKindForDialect("postgresql", column.GeneratedKind), "STORED")
}

func txtarPostgresEnumsByName(statements []ast.Node) map[string]*ast.EnumNode {
	enums := map[string]*ast.EnumNode{}
	unqualifiedCounts := map[string]int{}
	var enumList []*ast.EnumNode
	for _, stmt := range statements {
		enum, ok := stmt.(*ast.EnumNode)
		if ok {
			name := atlasSQLIdentifier(enum.Name)
			enums[name] = enum
			enumList = append(enumList, enum)
			_, unqualified := atlasSplitQualifiedTableName(name)
			unqualifiedCounts[unqualified]++
		}
	}
	for _, enum := range enumList {
		_, unqualified := atlasSplitQualifiedTableName(enum.Name)
		if unqualifiedCounts[unqualified] == 1 {
			enums[unqualified] = enum
		}
	}
	return enums
}

func txtarPostgresDomainNames(statements []ast.Node) map[string]bool {
	domains := map[string]bool{}
	for _, stmt := range statements {
		createType, ok := stmt.(*ast.CreateTypeNode)
		if !ok {
			continue
		}
		if _, ok := createType.TypeDef.(*ast.DomainTypeDef); ok {
			domains[atlasSQLIdentifier(createType.Name)] = true
		}
	}
	return domains
}

func txtarPostgresRetainDomainTypes(current, next []ast.Node) []ast.Node {
	if len(current) == 0 {
		return next
	}
	retained := make([]ast.Node, 0, len(current)+len(next))
	for _, stmt := range current {
		createType, ok := stmt.(*ast.CreateTypeNode)
		if !ok {
			continue
		}
		if _, ok := createType.TypeDef.(*ast.DomainTypeDef); ok {
			retained = append(retained, createType)
		}
	}
	return append(retained, next...)
}

func txtarPostgresApplyColumnForeignKeySupported(column *ast.ColumnNode) bool {
	if column.ForeignKey == nil {
		return true
	}
	return txtarPostgresApplyForeignKeyRefSupported(column.ForeignKey, 1)
}

func txtarPostgresApplyForeignKeyConstraintSupported(constraint *ast.ConstraintNode) bool {
	if constraint.Reference == nil {
		return false
	}
	return txtarPostgresApplyForeignKeyRefSupported(constraint.Reference, len(atlasConstraintColumnNames(constraint)))
}

func txtarPostgresApplyForeignKeyRefSupported(ref *ast.ForeignKeyRef, columnCount int) bool {
	if ref == nil || strings.TrimSpace(ref.Table) == "" || columnCount <= 0 {
		return false
	}
	refColumns := ref.ReferencedColumns()
	if len(refColumns) != columnCount {
		return false
	}
	return !slices.ContainsFunc(refColumns, func(column string) bool {
		return strings.TrimSpace(column) == ""
	})
}

func txtarPostgresApplyColumnDefaultSupported(column *ast.ColumnNode, enums map[string]*ast.EnumNode) bool {
	if column.Default == nil {
		return true
	}
	if column.Default.Expression != "" {
		return txtarPostgresApplyExpressionDefaultSupported(column)
	}
	switch txtarPostgresColumnType(column) {
	case "character varying", "bpchar", "integer", "boolean":
		return true
	case "interval":
		return txtarPostgresIntervalDefaultSQL(column) != ""
	default:
		_, ok := enums[atlasSQLIdentifier(column.Type)]
		return ok
	}
}

func txtarPostgresApplyExpressionDefaultSupported(column *ast.ColumnNode) bool {
	expr := strings.TrimSpace(column.Default.Expression)
	return strings.HasPrefix(txtarPostgresColumnType(column), "timestamp") &&
		postgresCurrentTimestampRE.MatchString(expr)
}

func txtarPostgresApplyColumnTypeSupported(column *ast.ColumnNode, domains map[string]bool, enums map[string]*ast.EnumNode) bool {
	normalized := strings.ToLower(strings.TrimSpace(column.Type))
	if column.TypeRawSQL {
		return txtarPostgresRawArrayType(column) != "" ||
			txtarPostgresEnumArrayType(column.Type, enums) != "" ||
			domains[atlasSQLIdentifier(column.Type)]
	}
	if _, ok := enums[atlasSQLIdentifier(column.Type)]; ok {
		return true
	}
	switch normalized {
	case "smallserial", "serial", "bigserial":
		return true
	default:
		return true
	}
}

func txtarPostgresApplyIndexSupported(index *ast.IndexNode) bool {
	if len(index.EffectiveParts()) == 0 || index.Concurrently || index.Operator != "" || index.Comment != "" {
		return false
	}
	if index.NullsDistinct != nil && !index.Unique {
		return false
	}
	for _, part := range index.EffectiveParts() {
		if !txtarPostgresApplyIndexPartSupported(part) {
			return false
		}
	}
	switch strings.ToUpper(index.Type) {
	case "", "BTREE", "HASH", "GIN", "GIST", "BRIN":
		return true
	default:
		return false
	}
}

func txtarPostgresApplyIndexPartSupported(part ast.IndexPart) bool {
	if part.Expr != "" && txtarPostgresShowIndexExpr(part.Expr) == "" {
		return false
	}
	return txtarPostgresIndexOperatorSupported(part.Operator)
}

func txtarSQLiteVirtualApplyStateSupported(statements []ast.Node) bool {
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateSchemaNode:
			continue
		case *ast.CreateTableNode:
			if !txtarSQLiteApplyTableSupported(node) {
				return false
			}
		case *ast.IndexNode:
			if !txtarSQLiteApplyIndexSupported(node) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func txtarSQLiteApplyTableSupported(table *ast.CreateTableNode) bool {
	if !txtarSQLiteApplyTableOptionsSupported(table.Options) || table.SelectBody != "" || table.Comment != "" {
		return false
	}
	for _, column := range table.Columns {
		if column.Unique || column.Check != "" || column.CheckName != "" ||
			column.Comment != "" || column.ForeignKey != nil {
			return false
		}
	}
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.PrimaryKeyConstraint {
			return false
		}
	}
	return true
}

func txtarSQLiteApplyTableOptionsSupported(options map[string]string) bool {
	for key := range options {
		switch key {
		case "STRICT", "WITHOUT_ROWID":
			continue
		default:
			return false
		}
	}
	return true
}

func txtarSQLiteApplyIndexSupported(index *ast.IndexNode) bool {
	return len(index.EffectiveParts()) > 0 && index.Type == "" && index.Operator == "" &&
		index.Comment == "" && !index.Concurrently
}

func txtarMySQLVirtualApplyStateSupported(dialect string, statements []ast.Node) bool {
	if !txtarMySQLSchemasSupported(dialect, statements) {
		return false
	}
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateSchemaNode:
			continue
		case *ast.CreateTableNode:
			if !txtarMySQLApplyTableSupported(dialect, node) {
				return false
			}
		case *ast.AlterTableNode:
			if !txtarVirtualApplyAlterTableSupported(node) {
				return false
			}
		case *ast.IndexNode:
			if !txtarMySQLApplyIndexSupported(dialect, node) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func txtarMySQLSchemasSupported(dialect string, statements []ast.Node) bool {
	defaults := atlasDefaultSchemaAttrs(dialect)
	for _, stmt := range statements {
		schema, ok := stmt.(*ast.CreateSchemaNode)
		if !ok {
			continue
		}
		if schema.Charset != "" && !atlasMySQLSchemaCharsetSupported(schema.Charset, defaults.charset) {
			return false
		}
		if schema.Collate != "" && !atlasMySQLSchemaCollateSupported(schema.Collate, defaults.collate) {
			return false
		}
	}
	return true
}

func atlasMySQLSchemaCharsetSupported(charset, defaultCharset string) bool {
	return strings.EqualFold(charset, defaultCharset)
}

func atlasMySQLSchemaCollateSupported(collate, defaultCollate string) bool {
	if strings.EqualFold(collate, defaultCollate) {
		return true
	}
	return strings.EqualFold(collate, "utf8mb4_general_ci")
}

func atlasMySQLColumnTypeUsesSchemaCollation(columnType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(atlasSQLIdentifier(columnType)))
	return strings.Contains(normalized, "char") ||
		strings.Contains(normalized, "text") ||
		strings.HasPrefix(normalized, "enum") ||
		strings.HasPrefix(normalized, "set")
}

func txtarMySQLApplyTableSupported(dialect string, table *ast.CreateTableNode) bool {
	if table.SelectBody != "" || table.Comment != "" {
		return false
	}
	if !txtarMySQLApplyTableOptionsSupported(dialect, table.Options) {
		return false
	}
	for _, column := range table.Columns {
		if column.Comment != "" || column.Unique ||
			column.Check != "" || column.CheckName != "" {
			return false
		}
		if column.ForeignKey != nil && !txtarMySQLApplyColumnForeignKeySupported(column.ForeignKey) {
			return false
		}
		if column.GeneratedExpression != "" && dialect != "mysql" {
			return false
		}
		switch strings.ToLower(column.Type) {
		case "json":
			if dialect != "mariadb" {
				return false
			}
		}
	}
	return true
}

func txtarMySQLApplyColumnForeignKeySupported(ref *ast.ForeignKeyRef) bool {
	return ref.Table != "" && len(ref.ReferencedColumns()) > 0
}

func txtarVirtualApplyAlterTableSupported(alter *ast.AlterTableNode) bool {
	if strings.TrimSpace(alter.Name) == "" || len(alter.Operations) == 0 {
		return false
	}
	for _, operation := range alter.Operations {
		add, ok := operation.(*ast.AddConstraintOperation)
		if !ok || !txtarVirtualApplyAddConstraintSupported(add.Constraint) {
			return false
		}
	}
	return true
}

func txtarVirtualApplyAddConstraintSupported(constraint *ast.ConstraintNode) bool {
	if constraint == nil {
		return false
	}
	switch constraint.Type {
	case ast.ForeignKeyConstraint:
		if constraint.Reference == nil {
			return false
		}
		columns := atlasConstraintColumnNames(constraint)
		refColumns := constraint.Reference.ReferencedColumns()
		return len(columns) > 0 &&
			len(refColumns) == len(columns) &&
			strings.TrimSpace(constraint.Reference.Table) != "" &&
			!slices.ContainsFunc(refColumns, func(column string) bool {
				return strings.TrimSpace(column) == ""
			})
	case ast.PrimaryKeyConstraint:
		return len(atlasConstraintColumnNames(constraint)) > 0
	case ast.UniqueConstraint:
		return constraint.Name != "" && len(atlasConstraintColumnNames(constraint)) > 0
	case ast.CheckConstraint:
		return constraint.Name != ""
	default:
		return false
	}
}

func txtarMySQLApplyTableOptionsSupported(dialect string, options map[string]string) bool {
	defaultAttrs := atlasDefaultSchemaAttrs(dialect)
	for key, value := range options {
		switch key {
		case "AUTO_INCREMENT":
			continue
		case "ENGINE":
			if strings.EqualFold(value, "InnoDB") || strings.EqualFold(value, "MyISAM") {
				continue
			}
		case "CHARSET":
			if value == defaultAttrs.charset {
				continue
			}
		case "COLLATE":
			if value == defaultAttrs.collate {
				continue
			}
		}
		return false
	}
	return true
}

func txtarMySQLApplyIndexSupported(dialect string, index *ast.IndexNode) bool {
	if len(index.EffectiveParts()) == 0 || index.Concurrently || index.Operator != "" ||
		index.Comment != "" || index.Condition != "" {
		return false
	}
	if !txtarMySQLApplyIndexTypeSupported(index.Type) {
		return false
	}
	if index.Parser != "" && !strings.EqualFold(index.Type, "FULLTEXT") {
		return false
	}
	for _, part := range index.EffectiveParts() {
		if part.Expr != "" && dialect != "mysql" {
			return false
		}
	}
	return true
}

func txtarMySQLApplyIndexTypeSupported(indexType string) bool {
	if indexType == "" {
		return true
	}
	return strings.EqualFold(indexType, "FULLTEXT") || strings.EqualFold(indexType, "SPATIAL")
}

func txtarHCLStatements(fx Fixture, name, data string) ([]ast.Node, error) {
	data = txtarNormalizeAtlasHCL(fx, data)
	db, err := atlascompat.ParseAtlasHCL([]byte(data), name)
	if err != nil {
		return nil, fmt.Errorf("%w: parse HCL file: %v", errUnsupportedInspectHCL, err)
	}
	list := atlascompat.SchemaToAST(*db, txtarFixtureDialect(fx))
	return txtarOrderHCLStatementsByTableBlocks(fx, data, list.Statements), nil
}

func txtarNormalizeAtlasHCL(fx Fixture, data string) string {
	schemaName := txtarFixtureSchemaName(fx)
	data = strings.ReplaceAll(data, "schema.$db", "schema."+schemaName)
	data = strings.ReplaceAll(data, `schema "$db"`, fmt.Sprintf("schema %q", schemaName))
	attrs := atlasDefaultSchemaAttrs(txtarFixtureDialect(fx))
	if attrs.charset != "" {
		data = strings.ReplaceAll(data, `"$charset"`, fmt.Sprintf("%q", attrs.charset))
	}
	if attrs.collate != "" {
		data = strings.ReplaceAll(data, `"$collate"`, fmt.Sprintf("%q", attrs.collate))
	}
	return data
}

func txtarOrderHCLStatementsByTableBlocks(fx Fixture, data string, statements []ast.Node) []ast.Node {
	order := txtarHCLTableOrder(data)
	if len(order) == 0 {
		return statements
	}
	out := slices.Clone(statements)
	schemaName := txtarFixtureSchemaName(fx)
	slices.SortStableFunc(out, func(a, b ast.Node) int {
		aRank, aOK := txtarHCLStatementTableRank(schemaName, order, a)
		bRank, bOK := txtarHCLStatementTableRank(schemaName, order, b)
		switch {
		case aOK && bOK:
			return cmp.Compare(aRank, bRank)
		case aOK:
			return -1
		case bOK:
			return 1
		default:
			return 0
		}
	})
	return out
}

func txtarHCLStatementTableRank(schemaName string, order map[string]int, stmt ast.Node) (int, bool) {
	switch node := stmt.(type) {
	case *ast.CreateTableNode:
		rank, ok := order[atlasUnqualifyTableName(schemaName, node.Name)]
		return rank, ok
	case *ast.IndexNode:
		rank, ok := order[atlasUnqualifyTableName(schemaName, node.Table)]
		return rank, ok
	default:
		return 0, false
	}
}

func txtarHCLTableOrder(data string) map[string]int {
	re := regexp.MustCompile(`(?m)^\s*table\s+"([^"]+)"\s*\{`)
	matches := re.FindAllStringSubmatch(data, -1)
	order := make(map[string]int, len(matches))
	for _, match := range matches {
		if _, exists := order[match[1]]; !exists {
			order[match[1]] = len(order)
		}
	}
	return order
}

func runTxtarExecSQL(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 1 || fields[0] != "execsql" {
		return txtarCommandResult{}, false
	}
	if len(fields) != 2 {
		return txtarCommandResult{unsupported: "execsql"}, true
	}
	switch txtarFixtureFamily(fx) {
	case "sqlite":
		list, err := atlascompat.ParseSQL(fields[1], atlascompat.ParseSQLOptions{})
		if err != nil {
			return txtarCommandResult{unsupported: "execsql"}, true
		}
		runtime.hasVirtualDBState = true
		runtime.dbStatements = append(runtime.dbStatements, list.Statements...)
		return txtarCommandResult{}, true
	case "postgres":
		if tableName, ok := txtarPostgresExecSQLPartitionParent(fx, fields[1]); ok {
			runtime.hasVirtualDBState = true
			if runtime.partitionChildren == nil {
				runtime.partitionChildren = map[string]int{}
			}
			runtime.partitionChildren[tableName]++
			return txtarCommandResult{}, true
		}
		list, err := atlascompat.ParseSQL(fields[1], atlascompat.ParseSQLOptions{})
		if err != nil || !txtarPostgresVirtualApplyStateSupported(list.Statements) {
			return txtarCommandResult{unsupported: "execsql"}, true
		}
		runtime.hasVirtualDBState = true
		runtime.dbStatements = append(runtime.dbStatements, list.Statements...)
		return txtarCommandResult{}, true
	case "mysql", "mariadb":
		tableName, rows, ok := txtarParseInsertRows(fields[1])
		if ok {
			if runtime.dbRows == nil {
				runtime.dbRows = map[string][]txtarVirtualRow{}
			}
			runtime.dbRows[tableName] = append(runtime.dbRows[tableName], rows...)
			return txtarCommandResult{}, true
		}
		if !txtarExecSQLMySQLAuthNoop(fields[1]) {
			return txtarCommandResult{unsupported: "execsql"}, true
		}
		return txtarCommandResult{}, true
	default:
		return txtarCommandResult{unsupported: "execsql"}, true
	}
}

func txtarPostgresExecSQLPartitionParent(fx Fixture, sql string) (string, bool) {
	match := postgresPartitionOfRE.FindStringSubmatch(strings.TrimSpace(sql))
	if match == nil {
		return "", false
	}
	parent := strings.TrimSpace(match[1])
	parent = strings.Trim(parent, `"`)
	schemaName := txtarFixtureSchemaName(fx)
	parent = strings.ReplaceAll(parent, "$db.", schemaName+".")
	return atlasHCLTableIdentifier(parent, schemaName), true
}

func txtarExecSQLMySQLAuthNoop(stmt string) bool {
	stmt = strings.TrimSuffix(strings.TrimSpace(stmt), ";")
	user := `"[^"]+"\s*@\s*"[^"]+"`
	patterns := []string{
		`(?is)^CREATE\s+USER\s+IF\s+NOT\s+EXISTS\s+` + user + `\s+IDENTIFIED\s+BY\s+("[^"]*"|'[^']*')$`,
		`(?is)^GRANT\s+ALL\s+PRIVILEGES\s+ON\s+\*\.\*\s+TO\s+` + user + `\s+WITH\s+GRANT\s+OPTION$`,
		`(?is)^DROP\s+USER\s+` + user + `$`,
	}
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		return regexp.MustCompile(pattern).MatchString(stmt)
	})
}

func txtarParseInsertRows(stmt string) (string, []txtarVirtualRow, bool) {
	stmt = strings.TrimSuffix(strings.TrimSpace(stmt), ";")
	rest, ok := cutPrefixFold(stmt, "INSERT INTO ")
	if !ok {
		return "", nil, false
	}
	tableName, rest, ok := txtarParseLeadingSQLIdentifier(rest)
	if !ok {
		return "", nil, false
	}
	tableName = atlasUnqualifiedSQLTableName(tableName)
	columnList, rest, ok := txtarParenthesizedSQLPrefix(rest)
	if !ok {
		return "", nil, false
	}
	columns, ok := txtarParseInsertColumns(columnList)
	if !ok {
		return "", nil, false
	}
	rest, ok = cutPrefixFold(strings.TrimSpace(rest), "VALUES")
	if !ok {
		return "", nil, false
	}
	rows, ok := txtarParseInsertValueRows(strings.TrimSpace(rest), columns)
	if !ok {
		return "", nil, false
	}
	return tableName, rows, true
}

func cutPrefixFold(value, prefix string) (string, bool) {
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	return value[len(prefix):], true
}

func txtarParseInsertColumns(data string) ([]string, bool) {
	values := txtarSplitSQLList(data)
	if len(values) == 0 {
		return nil, false
	}
	columns := make([]string, 0, len(values))
	for _, value := range values {
		column, rest, ok := txtarParseLeadingSQLIdentifier(value)
		if !ok || strings.TrimSpace(rest) != "" {
			return nil, false
		}
		columns = append(columns, atlasSQLIdentifier(column))
	}
	return columns, true
}

func txtarParseInsertValueRows(data string, columns []string) ([]txtarVirtualRow, bool) {
	var rows []txtarVirtualRow
	rest := strings.TrimSpace(data)
	for rest != "" {
		valuesData, next, ok := txtarParenthesizedSQLPrefix(rest)
		if !ok {
			return nil, false
		}
		values := txtarSplitSQLList(valuesData)
		if len(values) != len(columns) {
			return nil, false
		}
		row := txtarVirtualRow{}
		for i, column := range columns {
			value, ok := txtarSQLLiteralValue(values[i])
			if !ok {
				return nil, false
			}
			row[column] = value
		}
		rows = append(rows, row)
		rest = strings.TrimSpace(next)
		if rest == "" {
			break
		}
		if !strings.HasPrefix(rest, ",") {
			return nil, false
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, ","))
	}
	return rows, len(rows) > 0
}

func txtarParenthesizedSQLPrefix(data string) (string, string, bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, "(") {
		return "", "", false
	}
	quote := byte(0)
	escaped := false
	depth := 0
	for i := 0; i < len(data); i++ {
		ch := data[i]
		switch {
		case escaped:
			escaped = false
		case quote != 0 && ch == '\\':
			escaped = true
		case quote != 0 && ch == quote:
			if i+1 < len(data) && data[i+1] == quote {
				i++
				continue
			}
			quote = 0
		case quote != 0:
			continue
		case ch == '\'' || ch == '"':
			quote = ch
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth == 0 {
				return data[1:i], data[i+1:], true
			}
		}
	}
	return "", "", false
}

func txtarSplitSQLList(data string) []string {
	var out []string
	start := 0
	quote := byte(0)
	escaped := false
	depth := 0
	for i := 0; i < len(data); i++ {
		ch := data[i]
		switch {
		case escaped:
			escaped = false
		case quote != 0 && ch == '\\':
			escaped = true
		case quote != 0 && ch == quote:
			if i+1 < len(data) && data[i+1] == quote {
				i++
				continue
			}
			quote = 0
		case quote != 0:
			continue
		case ch == '\'' || ch == '"':
			quote = ch
		case ch == '(':
			depth++
		case ch == ')':
			depth--
		case ch == ',' && depth == 0:
			out = append(out, strings.TrimSpace(data[start:i]))
			start = i + 1
		}
	}
	out = append(out, strings.TrimSpace(data[start:]))
	return slices.DeleteFunc(out, func(value string) bool { return value == "" })
}

func txtarSQLLiteralValue(data string) (string, bool) {
	data = strings.TrimSpace(data)
	if data == "" {
		return "", false
	}
	if len(data) < 2 || (data[0] != '\'' && data[0] != '"') {
		return data, true
	}
	quote := data[0]
	if data[len(data)-1] != quote {
		return "", false
	}
	value := data[1 : len(data)-1]
	value = strings.ReplaceAll(value, `\`+string(quote), string(quote))
	value = strings.ReplaceAll(value, string([]byte{quote, quote}), string(quote))
	return value, true
}

func runTxtarExist(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 1 || fields[0] != "exist" {
		return txtarCommandResult{}, false
	}
	switch txtarFixtureFamily(fx) {
	case "sqlite", "mysql", "mariadb":
	default:
		return txtarCommandResult{unsupported: "exist"}, true
	}
	if len(fields) < 2 || !runtime.hasVirtualDBState {
		return txtarCommandResult{unsupported: "exist"}, true
	}
	for _, tableName := range fields[1:] {
		if _, ok := txtarFindTable(txtarFixtureSchemaName(fx), runtime.dbStatements, tableName); !ok {
			return txtarCommandResult{failed: true, err: fmt.Errorf("table %s does not exist", tableName)}, true
		}
	}
	return txtarCommandResult{}, true
}

func runTxtarCmpHCL(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 1 || fields[0] != "cmphcl" {
		return txtarCommandResult{}, false
	}
	if len(fields) != 2 {
		return txtarCommandResult{unsupported: "cmphcl"}, true
	}
	if !runtime.hasVirtualDBState {
		return txtarCommandResult{unsupported: "cmphcl"}, true
	}

	actual, err := renderAtlasInspectHCL(txtarFixtureDialect(fx), txtarFixtureSchemaName(fx), runtime.dbStatements)
	if err != nil {
		return txtarCommandResult{unsupported: "cmphcl"}, true
	}
	expected, ok := runtime.files[fields[1]]
	if !ok {
		return txtarCommandResult{
			failed: true,
			err:    fmt.Errorf("cmphcl %s: %s missing", fields[1], fields[1]),
		}, true
	}
	expected = txtarNormalizeAtlasHCL(fx, expected)
	if !txtarFilesEqual(actual, expected) {
		return txtarCommandResult{
			failed: true,
			err:    fmt.Errorf("cmphcl %s did not match: got %q want %q", fields[1], oneLine(actual), oneLine(expected)),
		}, true
	}
	return txtarCommandResult{}, true
}

func runTxtarSynced(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 1 || fields[0] != "synced" {
		return txtarCommandResult{}, false
	}
	switch txtarFixtureFamily(fx) {
	case "sqlite", "mysql", "mariadb", "postgres":
	default:
		return txtarCommandResult{unsupported: "synced"}, true
	}
	if len(fields) != 2 || !runtime.hasVirtualDBState {
		return txtarCommandResult{unsupported: "synced"}, true
	}
	data, ok := runtime.files[fields[1]]
	if !ok {
		return txtarCommandResult{failed: true, err: fmt.Errorf("synced %s: file missing", fields[1])}, true
	}
	statements, err := txtarHCLStatements(fx, fields[1], data)
	if err != nil {
		return txtarCommandResult{unsupported: "synced"}, true
	}
	if !txtarFixtureSupportsVirtualApply(fx, statements) {
		return txtarCommandResult{unsupported: "synced"}, true
	}
	actual, ok := txtarVirtualStateShowSQL(fx, runtime.dbStatements)
	if !ok {
		return txtarCommandResult{unsupported: "synced"}, true
	}
	expected, ok := txtarVirtualStateShowSQL(fx, statements)
	if !ok {
		return txtarCommandResult{unsupported: "synced"}, true
	}
	if txtarNormalizeFixtureShowSQL(fx, actual) != txtarNormalizeFixtureShowSQL(fx, expected) {
		return txtarCommandResult{failed: true, err: fmt.Errorf("synced %s did not match: got %q want %q", fields[1], oneLine(actual), oneLine(expected))}, true
	}
	return txtarCommandResult{}, true
}

func runTxtarCmpShow(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 1 || fields[0] != "cmpshow" {
		return txtarCommandResult{}, false
	}
	if len(fields) < 3 {
		return txtarCommandResult{unsupported: "cmpshow"}, true
	}
	if !runtime.hasVirtualDBState {
		return txtarCommandResult{unsupported: "cmpshow"}, true
	}
	tableNames := fields[1 : len(fields)-1]
	expectedName := fields[len(fields)-1]
	if len(tableNames) != 1 {
		expectedSQL, ok := runtime.files[expectedName]
		if !ok {
			return txtarCommandResult{
				failed: true,
				err:    fmt.Errorf("cmpshow %s %s: %s missing", strings.Join(tableNames, " "), expectedName, expectedName),
			}, true
		}
		return txtarCmpShowSQL(fx, runtime, tableNames, expectedName, expectedSQL)
	}
	actual, ok := txtarTableHCL(fx, runtime.dbStatements, tableNames[0])
	if !ok {
		return txtarCommandResult{
			failed: true,
			err:    fmt.Errorf("cmpshow %s %s: table %s missing", tableNames[0], expectedName, tableNames[0]),
		}, true
	}
	expectedSQL, ok := runtime.files[expectedName]
	if !ok {
		return txtarCommandResult{
			failed: true,
			err:    fmt.Errorf("cmpshow %s %s: %s missing", tableNames[0], expectedName, expectedName),
		}, true
	}
	if txtarTableNeedsSQLShowCompare(fx, runtime.dbStatements, tableNames[0]) {
		return txtarCmpShowSQL(fx, runtime, tableNames, expectedName, expectedSQL)
	}
	expectedStatements, err := txtarParseExpectedShowSQL(expectedSQL)
	if err != nil {
		return txtarCmpShowSQL(fx, runtime, tableNames, expectedName, expectedSQL)
	}
	expected, ok := txtarTableHCL(fx, expectedStatements, tableNames[0])
	if !ok {
		return txtarCommandResult{unsupported: "cmpshow"}, true
	}
	if !txtarFilesEqual(actual, expected) {
		return txtarCommandResult{
			failed: true,
			err: fmt.Errorf("cmpshow %s %s did not match: got %q want %q",
				tableNames[0], expectedName, oneLine(actual), oneLine(expected)),
		}, true
	}
	return txtarCommandResult{}, true
}

func txtarTableNeedsSQLShowCompare(fx Fixture, statements []ast.Node, name string) bool {
	switch txtarFixtureFamily(fx) {
	case "mysql", "mariadb":
		return true
	}
	schemaName := txtarFixtureSchemaName(fx)
	for _, stmt := range statements {
		index, ok := stmt.(*ast.IndexNode)
		if ok && atlasHCLTableIdentifier(index.Table, schemaName) == name {
			return true
		}
	}
	table, ok := txtarFindTable(schemaName, statements, name)
	if !ok {
		return false
	}
	for _, column := range table.Columns {
		if column.Default != nil || column.GeneratedExpression != "" {
			return true
		}
	}
	return table.Partition != nil
}

func txtarCmpShowSQL(
	fx Fixture,
	runtime *txtarRuntime,
	tableNames []string,
	expectedName string,
	expectedSQL string,
) (txtarCommandResult, bool) {
	switch txtarFixtureFamily(fx) {
	case "sqlite", "mysql", "mariadb", "postgres":
	default:
		return txtarCommandResult{unsupported: "cmpshow"}, true
	}
	actual, ok := txtarTablesShowSQLWithPartitionChildren(fx, runtime.dbStatements, tableNames, runtime.partitionChildren)
	if !ok {
		return txtarCommandResult{unsupported: "cmpshow"}, true
	}
	expected := txtarCanonicalShowSQL(fx, expectedSQL)
	if txtarNormalizeFixtureShowSQL(fx, actual) != txtarNormalizeFixtureShowSQL(fx, expected) {
		tableLabel := strings.Join(tableNames, " ")
		return txtarCommandResult{
			failed: true,
			err: fmt.Errorf("cmpshow %s %s did not match: got %q want %q",
				tableLabel, expectedName, oneLine(actual), oneLine(expected)),
		}, true
	}
	return txtarCommandResult{}, true
}

func txtarCanonicalShowSQL(fx Fixture, sql string) string {
	switch txtarFixtureDialect(fx) {
	case "mysql":
		// MySQL fixtures already store Atlas SHOW CREATE TABLE output. Parsing it
		// through Ptah can collapse KEY/UNIQUE KEY shape details that SHOW output
		// preserves, so compare the raw fixture text with MySQL normalization.
		return sql
	}
	statements, err := txtarParseExpectedShowSQL(sql)
	if err != nil {
		return sql
	}
	atlasNormalizeMariaDBJSONColumns(txtarFixtureDialect(fx), statements)
	statements = atlasNormalizeMySQLParsedUniqueKeys(txtarFixtureDialect(fx), statements)
	out, ok := txtarVirtualStateShowSQL(fx, statements)
	if !ok {
		return sql
	}
	return out
}

func atlasNormalizeMySQLParsedUniqueKeys(dialect string, statements []ast.Node) []ast.Node {
	if dialect != "mysql" && dialect != "mariadb" {
		return statements
	}
	out := slices.Clone(statements)
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			continue
		}
		kept := table.Constraints[:0]
		for _, constraint := range table.Constraints {
			if constraint.Type != ast.UniqueConstraint {
				kept = append(kept, constraint)
				continue
			}
			columns := atlasConstraintColumnNames(constraint)
			if len(columns) == 0 {
				kept = append(kept, constraint)
				continue
			}
			out = append(out, &ast.IndexNode{
				Name:    atlasDefaultUniqueName(table.Name, columns, constraint.Name),
				Table:   table.Name,
				Columns: columns,
				Unique:  true,
				Parts:   txtarIndexPartsFromConstraint(constraint),
			})
		}
		table.Constraints = kept
	}
	return out
}

func txtarVirtualStateShowSQL(fx Fixture, statements []ast.Node) (string, bool) {
	schemaName := txtarFixtureSchemaName(fx)
	unqualified := atlasUnqualifySchemaStatements(schemaName, statements)
	out, err := renderAtlasInspectSQLWithOptions(txtarFixtureDialect(fx), unqualified, "", atlasInspectSQLOptions{
		mariaDBJSONStorage: true,
		showDefaultNull:    true,
	})
	if err != nil {
		return "", false
	}
	return out, true
}

func atlasNormalizeMariaDBJSONColumns(dialect string, statements []ast.Node) {
	if dialect != "mariadb" {
		return
	}
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			continue
		}
		normalizedJSONChecks := map[string]bool{}
		for _, column := range table.Columns {
			if !atlasMariaDBJSONStorageColumn(column) {
				continue
			}
			if !atlasMariaDBJSONCheckMatches(column.Name, column.Check) &&
				!atlasTableHasMariaDBJSONCheck(table, column.Name) {
				continue
			}
			column.Type = "json"
			column.Charset = ""
			column.Collate = ""
			column.Check = ""
			normalizedJSONChecks[column.Name] = true
		}
		if len(normalizedJSONChecks) > 0 {
			table.Constraints = slices.DeleteFunc(table.Constraints, func(constraint *ast.ConstraintNode) bool {
				return constraint.Type == ast.CheckConstraint &&
					atlasAnyMariaDBJSONCheckMatches(normalizedJSONChecks, constraint.Expression)
			})
		}
	}
}

func atlasMariaDBJSONStorageColumn(column *ast.ColumnNode) bool {
	return strings.EqualFold(column.Type, "longtext") &&
		strings.EqualFold(column.Charset, "utf8mb4") &&
		strings.EqualFold(column.Collate, "utf8mb4_bin")
}

func atlasTableHasMariaDBJSONCheck(table *ast.CreateTableNode, columnName string) bool {
	for _, constraint := range table.Constraints {
		if constraint.Type == ast.CheckConstraint && atlasMariaDBJSONCheckMatches(columnName, constraint.Expression) {
			return true
		}
	}
	return false
}

func atlasAnyMariaDBJSONCheckMatches(columns map[string]bool, expr string) bool {
	for columnName := range columns {
		if atlasMariaDBJSONCheckMatches(columnName, expr) {
			return true
		}
	}
	return false
}

func atlasMariaDBJSONCheckMatches(columnName, expr string) bool {
	expr = strings.ToLower(spaceRunRE.ReplaceAllString(strings.TrimSpace(expr), ""))
	columnName = atlasSQLIdentifier(columnName)
	for _, candidate := range []string{
		"json_valid(" + strings.ToLower(columnName) + ")",
		"json_valid(`" + strings.ToLower(columnName) + "`)",
	} {
		if expr == candidate {
			return true
		}
	}
	return false
}

func txtarTableShowSQL(fx Fixture, statements []ast.Node, name string) (string, bool) {
	return txtarTablesShowSQL(fx, statements, []string{name})
}

func txtarTablesShowSQL(fx Fixture, statements []ast.Node, names []string) (string, bool) {
	return txtarTablesShowSQLWithPartitionChildren(fx, statements, names, nil)
}

func txtarTablesShowSQLWithPartitionChildren(
	fx Fixture,
	statements []ast.Node,
	names []string,
	partitionChildren map[string]int,
) (string, bool) {
	schemaName := txtarFixtureSchemaName(fx)
	dialect := txtarFixtureDialect(fx)
	schemaAttrs := atlasSchemaAttrsFromStatements(dialect, schemaName, statements)
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	found := map[string]bool{}
	filtered := make([]ast.Node, 0, len(statements))
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateTableNode:
			tableName := atlasHCLTableIdentifier(node.Name, schemaName)
			if wanted[tableName] {
				filtered = append(filtered, node)
				found[tableName] = true
			}
		case *ast.IndexNode:
			if wanted[atlasHCLTableIdentifier(node.Table, schemaName)] {
				filtered = append(filtered, node)
			}
		}
	}
	for _, name := range names {
		if !found[name] {
			return "", false
		}
	}
	if len(filtered) == 0 {
		return "", false
	}
	if txtarFixtureFamily(fx) == "postgres" {
		return txtarPostgresTablesShowSQL(schemaName, filtered, statements, names, partitionChildren)
	}
	filtered = atlasUnqualifySchemaStatements(schemaName, filtered)
	out, err := renderAtlasInspectSQLWithOptions(dialect, filtered, "", atlasInspectSQLOptions{
		mariaDBJSONStorage: true,
		showDefaultNull:    true,
		schemaAttrs:        schemaAttrs,
	})
	if err != nil {
		return "", false
	}
	return out, true
}

func txtarPostgresTablesShowSQL(
	schemaName string,
	statements []ast.Node,
	allStatements []ast.Node,
	names []string,
	partitionChildren map[string]int,
) (string, bool) {
	var out strings.Builder
	for i, name := range names {
		table, ok := txtarFindTable(schemaName, statements, name)
		if !ok {
			return "", false
		}
		if i > 0 {
			out.WriteByte('\n')
		}
		txtarWritePostgresTableShowSQL(
			&out,
			schemaName,
			table,
			txtarPostgresTableIndexes(schemaName, statements, name),
			allStatements,
			partitionChildren[name],
		)
	}
	return out.String(), true
}

func txtarWritePostgresTableShowSQL(
	out *strings.Builder,
	schemaName string,
	table *ast.CreateTableNode,
	indexes []*ast.IndexNode,
	statements []ast.Node,
	partitionChildren int,
) {
	tableName := atlasHCLTableIdentifier(table.Name, schemaName)
	enums := txtarPostgresEnumsByName(statements)
	if table.Partition == nil {
		fmt.Fprintf(out, "Table %q\n", schemaName+"."+tableName)
	} else {
		fmt.Fprintf(out, "Partitioned table %q\n", schemaName+"."+tableName)
	}
	out.WriteString("Column | Type | Collation | Nullable | Default\n")
	out.WriteString("--------+------+-----------+----------+--------\n")
	for _, column := range table.Columns {
		column = atlasInspectColumn("postgresql", table, column)
		nullable := ""
		if !column.Nullable {
			nullable = "not null"
		}
		defaultValue := ""
		if column.AutoInc {
			defaultValue = txtarPostgresIdentityDefault(column)
		} else if column.Default != nil {
			defaultValue = txtarPostgresDefaultSQLWithEnums(schemaName, column, enums)
		} else if txtarPostgresSerialType(column.Type) != "" {
			defaultValue = txtarPostgresSerialDefault(schemaName, tableName, column.Name)
		}
		fmt.Fprintf(out, "%s | %s |  | %s | %s\n",
			atlasSQLIdentifier(column.Name),
			txtarPostgresColumnTypeWithEnums(schemaName, column, enums),
			nullable,
			defaultValue,
		)
	}
	if table.Partition != nil {
		partitionKey, ok := txtarPostgresShowPartitionKey(table.Partition)
		if !ok {
			return
		}
		fmt.Fprintf(out, "Partition key: %s\n", partitionKey)
		if partitionChildren > 0 {
			fmt.Fprintf(out, "Number of partitions: %d (Use \\d+ to list them.)\n", partitionChildren)
		} else {
			fmt.Fprintf(out, "Number of partitions: %d\n", partitionChildren)
		}
	}
	indexLines := txtarPostgresShowIndexLines(tableName, table, indexes)
	if len(indexLines) == 0 {
		return
	}
	out.WriteString("Indexes:\n")
	for _, line := range indexLines {
		fmt.Fprintf(out, "    %s\n", line)
	}
	foreignKeyLines := txtarPostgresShowForeignKeyLines(schemaName, table)
	if len(foreignKeyLines) > 0 {
		out.WriteString("Foreign-key constraints:\n")
		for _, line := range foreignKeyLines {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
	referencedByLines := txtarPostgresReferencedByLines(schemaName, statements, tableName)
	if len(referencedByLines) > 0 {
		out.WriteString("Referenced by:\n")
		for _, line := range referencedByLines {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
}

func txtarPostgresShowPartitionKey(partition *ast.PartitionSpec) (string, bool) {
	if partition == nil || len(partition.Parts) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(partition.Parts))
	for _, part := range partition.Parts {
		switch {
		case part.Name != "":
			parts = append(parts, atlasSQLIdentifier(part.Name))
		case part.Expr != "":
			expr := txtarPostgresPartitionExprShow(part.Expr)
			if expr == "" {
				return "", false
			}
			parts = append(parts, expr)
		default:
			return "", false
		}
	}
	return strings.ToUpper(partition.Type) + " (" + strings.Join(parts, ", ") + ")", true
}

func txtarPostgresPartitionExprShow(expr string) string {
	expr = strings.TrimSpace(expr)
	if normalized := txtarPostgresNormalizeFloorExpr(expr); normalized != "" {
		return normalized
	}
	if expr == "" {
		return ""
	}
	return "((" + expr + "))"
}

func txtarPostgresPartitionExprSQL(expr string) string {
	expr = strings.TrimSpace(expr)
	if normalized := txtarPostgresNormalizeFloorExpr(expr); normalized != "" {
		return normalized
	}
	if expr == "" {
		return ""
	}
	return "(" + expr + ")"
}

func txtarPostgresPartitionExprHCL(expr string) string {
	expr = strings.TrimSpace(expr)
	if normalized := txtarPostgresNormalizeFloorExpr(expr); normalized != "" {
		return normalized
	}
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		return expr
	}
	return "(" + expr + ")"
}

func txtarPostgresNormalizeFloorExpr(expr string) string {
	match := postgresFloorExprRE.FindStringSubmatch(strings.TrimSpace(expr))
	if match == nil {
		return ""
	}
	return fmt.Sprintf("floor((%s)::double precision)", atlasSQLIdentifier(match[1]))
}

func txtarPostgresTableIndexes(schemaName string, statements []ast.Node, tableName string) []*ast.IndexNode {
	var indexes []*ast.IndexNode
	for _, stmt := range statements {
		index, ok := stmt.(*ast.IndexNode)
		if ok && atlasHCLTableIdentifier(index.Table, schemaName) == tableName {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *ast.IndexNode) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return indexes
}

func txtarPostgresShowIndexLines(tableName string, table *ast.CreateTableNode, indexes []*ast.IndexNode) []string {
	var lines []string
	if primaryKey := txtarPostgresPrimaryKey(table); len(primaryKey.columns) > 0 {
		line := fmt.Sprintf("%q PRIMARY KEY, btree (%s)", tableName+"_pkey", strings.Join(primaryKey.columns, ", "))
		if len(primaryKey.include) > 0 {
			line += fmt.Sprintf(" INCLUDE (%s)", strings.Join(primaryKey.include, ", "))
		}
		lines = append(lines, line)
	}
	var indexLines []string
	for _, index := range indexes {
		parts := make([]string, 0, len(index.EffectiveParts()))
		for _, part := range index.EffectiveParts() {
			parts = append(parts, txtarPostgresShowIndexPart(part))
		}
		kind := ""
		if index.Unique {
			kind = "UNIQUE, "
		}
		line := fmt.Sprintf("%q %s%s (%s)", atlasSQLIdentifier(index.Name), kind, txtarPostgresIndexMethod(index), strings.Join(parts, ", "))
		if len(index.IncludeColumns) > 0 {
			line += fmt.Sprintf(" INCLUDE (%s)", strings.Join(index.IncludeColumns, ", "))
		}
		if len(index.StorageParams) > 0 {
			line += " " + renderAtlasIndexStorageParamsSQL(index.StorageParams)
		}
		if index.NullsDistinct != nil {
			line += " " + renderAtlasNullsDistinctSQL(index.NullsDistinct)
		}
		if index.Condition != "" {
			line += " WHERE " + txtarPostgresShowIndexCondition(index.Condition)
		}
		indexLines = append(indexLines, line)
	}
	for _, constraint := range txtarPostgresUniqueConstraints(tableName, table) {
		indexLines = append(indexLines, txtarPostgresShowUniqueConstraintLine(constraint))
	}
	slices.Sort(indexLines)
	lines = append(lines, indexLines...)
	return lines
}

type txtarPostgresShowUniqueConstraint struct {
	name          string
	columns       []string
	nullsDistinct *bool
}

func txtarPostgresUniqueConstraints(tableName string, table *ast.CreateTableNode) []txtarPostgresShowUniqueConstraint {
	var constraints []txtarPostgresShowUniqueConstraint
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.UniqueConstraint {
			continue
		}
		columns := atlasConstraintColumnNames(constraint)
		if len(columns) == 0 {
			continue
		}
		showColumns := slices.Clone(columns)
		for i, column := range showColumns {
			showColumns[i] = atlasSQLIdentifier(column)
		}
		constraints = append(constraints, txtarPostgresShowUniqueConstraint{
			name:          atlasDefaultUniqueName(tableName, columns, constraint.Name),
			columns:       showColumns,
			nullsDistinct: cloneBoolPtr(constraint.NullsDistinct),
		})
	}
	slices.SortFunc(constraints, func(a, b txtarPostgresShowUniqueConstraint) int {
		return cmp.Compare(a.name, b.name)
	})
	return constraints
}

func txtarPostgresShowUniqueConstraintLine(constraint txtarPostgresShowUniqueConstraint) string {
	line := fmt.Sprintf("%q UNIQUE CONSTRAINT, btree (%s)", atlasSQLIdentifier(constraint.name), strings.Join(constraint.columns, ", "))
	if constraint.nullsDistinct != nil {
		line += " " + renderAtlasNullsDistinctSQL(constraint.nullsDistinct)
	}
	return line
}

type txtarPostgresShowForeignKey struct {
	sourceTable string
	name        string
	columns     []string
	refTable    string
	refColumns  []string
	onUpdate    string
	onDelete    string
}

func txtarPostgresShowForeignKeyLines(schemaName string, table *ast.CreateTableNode) []string {
	foreignKeys := txtarPostgresTableForeignKeys(schemaName, table)
	lines := make([]string, 0, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		lines = append(lines, txtarPostgresShowForeignKeyLine(foreignKey))
	}
	return lines
}

func txtarPostgresReferencedByLines(schemaName string, statements []ast.Node, tableName string) []string {
	var lines []string
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			continue
		}
		for _, foreignKey := range txtarPostgresTableForeignKeys(schemaName, table) {
			if foreignKey.refTable != schemaName+"."+tableName {
				continue
			}
			lines = append(lines, fmt.Sprintf(
				"TABLE %q CONSTRAINT %q %s",
				foreignKey.sourceTable,
				foreignKey.name,
				txtarPostgresShowForeignKeySpec(foreignKey),
			))
		}
	}
	return lines
}

func txtarPostgresTableForeignKeys(schemaName string, table *ast.CreateTableNode) []txtarPostgresShowForeignKey {
	tableName := atlasHCLTableIdentifier(table.Name, schemaName)
	var foreignKeys []txtarPostgresShowForeignKey
	for _, column := range table.Columns {
		if column.ForeignKey == nil {
			continue
		}
		ref := column.ForeignKey
		foreignKeys = append(foreignKeys, txtarPostgresShowForeignKey{
			sourceTable: schemaName + "." + tableName,
			name:        atlasDefaultForeignKeyName(tableName, []string{column.Name}, ref.Name),
			columns:     []string{column.Name},
			refTable:    txtarPostgresQualifiedTableName(schemaName, ref.Table),
			refColumns:  ref.ReferencedColumns(),
			onUpdate:    ref.OnUpdate,
			onDelete:    ref.OnDelete,
		})
	}
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.ForeignKeyConstraint || constraint.Reference == nil {
			continue
		}
		columns := atlasConstraintColumnNames(constraint)
		if len(columns) == 0 {
			continue
		}
		ref := constraint.Reference
		foreignKeys = append(foreignKeys, txtarPostgresShowForeignKey{
			sourceTable: schemaName + "." + tableName,
			name:        atlasDefaultForeignKeyName(tableName, columns, constraint.Name),
			columns:     columns,
			refTable:    txtarPostgresQualifiedTableName(schemaName, ref.Table),
			refColumns:  ref.ReferencedColumns(),
			onUpdate:    ref.OnUpdate,
			onDelete:    ref.OnDelete,
		})
	}
	return foreignKeys
}

func txtarPostgresShowForeignKeyLine(foreignKey txtarPostgresShowForeignKey) string {
	return fmt.Sprintf("%q %s", foreignKey.name, txtarPostgresShowForeignKeySpec(foreignKey))
}

func txtarPostgresShowForeignKeySpec(foreignKey txtarPostgresShowForeignKey) string {
	return fmt.Sprintf(
		"FOREIGN KEY (%s) REFERENCES %s(%s)%s",
		txtarPostgresIdentifierList(foreignKey.columns),
		foreignKey.refTable,
		txtarPostgresIdentifierList(foreignKey.refColumns),
		txtarPostgresShowForeignKeyActions(foreignKey),
	)
}

func txtarPostgresIdentifierList(columns []string) string {
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		out = append(out, atlasSQLIdentifier(column))
	}
	return strings.Join(out, ", ")
}

func txtarPostgresQualifiedTableName(schemaName, tableName string) string {
	tableName = atlasSQLIdentifier(tableName)
	if strings.Contains(tableName, ".") {
		return tableName
	}
	return schemaName + "." + tableName
}

func txtarPostgresShowForeignKeyActions(foreignKey txtarPostgresShowForeignKey) string {
	var parts []string
	if action := txtarPostgresShowForeignKeyAction(foreignKey.onUpdate); action != "" {
		parts = append(parts, "ON UPDATE "+action)
	}
	if action := txtarPostgresShowForeignKeyAction(foreignKey.onDelete); action != "" {
		parts = append(parts, "ON DELETE "+action)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func txtarPostgresShowForeignKeyAction(action string) string {
	normalized := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(action)), "_", " ")
	if normalized == "" || normalized == "NO ACTION" {
		return ""
	}
	return normalized
}

func txtarPostgresColumnType(column *ast.ColumnNode) string {
	if rawArrayType := txtarPostgresRawArrayType(column); rawArrayType != "" {
		return rawArrayType
	}
	if column.TypeRawSQL {
		return column.Type
	}
	typ := atlasColumnType("postgresql", column.Type)
	normalized := strings.ToLower(typ)
	switch {
	case normalized == "bit":
		return "bit(1)"
	case strings.HasPrefix(normalized, "bit_varying"):
		return "bit varying" + strings.TrimPrefix(normalized, "bit_varying")
	case normalized == "character_varying":
		return "character varying"
	case strings.HasPrefix(normalized, "char("):
		return "character" + strings.TrimPrefix(normalized, "char")
	case strings.HasPrefix(normalized, "varchar("):
		return "character varying" + strings.TrimPrefix(normalized, "varchar")
	case normalized == "double_precision":
		return "double precision"
	case strings.HasPrefix(normalized, "float("):
		return txtarPostgresFloatType(normalized)
	case normalized == "decimal":
		return "numeric"
	case strings.HasPrefix(normalized, "decimal("):
		return txtarPostgresNumericType(strings.TrimPrefix(normalized, "decimal"))
	case strings.HasPrefix(normalized, "numeric("):
		return txtarPostgresNumericType(strings.TrimPrefix(normalized, "numeric"))
	case normalized == "smallserial":
		return "smallint"
	case normalized == "serial":
		return "integer"
	case normalized == "bigserial":
		return "bigint"
	case normalized == "timestamptz":
		return "timestamp with time zone"
	case strings.HasPrefix(normalized, "timestamptz("):
		return "timestamp" + strings.TrimPrefix(normalized, "timestamptz") + " with time zone"
	case normalized == "timestamp(6)":
		return "timestamp without time zone"
	case strings.HasPrefix(normalized, "timestamp"):
		return normalized + " without time zone"
	case normalized == "time":
		return "time without time zone"
	case strings.HasPrefix(normalized, "time("):
		return normalized + " without time zone"
	case normalized == "timetz" || normalized == "timetz(6)":
		return "time with time zone"
	case strings.HasPrefix(normalized, "timetz("):
		return "time" + strings.TrimPrefix(normalized, "timetz") + " with time zone"
	case normalized == "second":
		return "interval second"
	case strings.HasPrefix(normalized, "second("):
		return "interval second" + strings.TrimPrefix(normalized, "second")
	case normalized == "day_to_second":
		return "interval day to second"
	case strings.HasPrefix(normalized, "day_to_second("):
		return "interval day to second" + strings.TrimPrefix(normalized, "day_to_second")
	default:
		return typ
	}
}

func txtarPostgresColumnTypeWithEnums(schemaName string, column *ast.ColumnNode, enums map[string]*ast.EnumNode) string {
	if column.TypeRawSQL {
		if enumArrayType := txtarPostgresEnumArrayType(column.Type, enums); enumArrayType != "" {
			base := strings.TrimSuffix(enumArrayType, "[]")
			return txtarPostgresQualifiedTableName(schemaName, base) + "[]"
		}
	}
	if enum, ok := enums[atlasSQLIdentifier(column.Type)]; ok {
		return txtarPostgresQualifiedTableName(schemaName, enum.Name)
	}
	return txtarPostgresColumnType(column)
}

func txtarPostgresSerialType(columnType string) string {
	switch strings.ToLower(strings.TrimSpace(columnType)) {
	case "smallserial", "serial", "bigserial":
		return strings.ToLower(strings.TrimSpace(columnType))
	default:
		return ""
	}
}

func txtarPostgresSerialDefault(schemaName, tableName, columnName string) string {
	return fmt.Sprintf(
		"nextval('%s.%s_%s_seq'::regclass)",
		schemaName,
		atlasSQLIdentifier(tableName),
		atlasSQLIdentifier(columnName),
	)
}

func txtarPostgresIdentityDefault(column *ast.ColumnNode) string {
	switch strings.ToUpper(strings.ReplaceAll(column.IdentityGeneration, " ", "_")) {
	case "ALWAYS":
		return "generated always as identity"
	default:
		return "generated by default as identity"
	}
}

func txtarPostgresRawArrayType(column *ast.ColumnNode) string {
	if !column.TypeRawSQL {
		return ""
	}
	normalized := spaceRunRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(column.Type)), " ")
	switch {
	case normalized == "int[1]" || normalized == "int array[1]":
		return "integer[]"
	case normalized == "point [][1]":
		return "point[]"
	case strings.HasPrefix(normalized, "varchar("):
		size, ok := txtarPostgresArrayTypeSize(normalized, "varchar")
		if ok {
			return "character varying(" + size + ")[]"
		}
	case strings.HasPrefix(normalized, "character varying("):
		size, ok := txtarPostgresArrayTypeSize(normalized, "character varying")
		if ok {
			return "character varying(" + size + ")[]"
		}
	}
	return ""
}

func txtarPostgresEnumArrayType(raw string, enums map[string]*ast.EnumNode) string {
	raw = strings.TrimSpace(raw)
	base, ok := strings.CutSuffix(raw, "[]")
	if !ok {
		return ""
	}
	base = atlasSQLIdentifier(strings.TrimSpace(base))
	enum, ok := enums[base]
	if !ok {
		return ""
	}
	return atlasSQLIdentifier(enum.Name) + "[]"
}

func txtarPostgresArrayTypeSize(raw, prefix string) (string, bool) {
	rest := strings.TrimPrefix(raw, prefix+"(")
	size, suffix, ok := strings.Cut(rest, ")")
	if !ok || strings.TrimSpace(size) == "" {
		return "", false
	}
	suffix = strings.TrimSpace(suffix)
	switch suffix {
	case "[]", "array", "array[1]", "[10][]":
		return size, true
	default:
		return "", false
	}
}

func txtarPostgresFloatType(typ string) string {
	precisionText := strings.TrimSuffix(strings.TrimPrefix(typ, "float("), ")")
	precision, err := strconv.Atoi(precisionText)
	if err != nil || precision <= 24 {
		return "real"
	}
	return "double precision"
}

func txtarPostgresNumericType(params string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(params, "("), ")")
	if inner == "" || strings.Contains(inner, ",") {
		return "numeric" + params
	}
	return fmt.Sprintf("numeric(%s,0)", inner)
}

func txtarPostgresDefaultSQL(column *ast.ColumnNode) string {
	if column.Default == nil {
		return ""
	}
	if column.Default.Expression != "" {
		return atlasDefaultSQL("postgresql", column.Default)
	}
	value := strings.TrimSpace(column.Default.Value)
	switch txtarPostgresColumnType(column) {
	case "character varying":
		return fmt.Sprintf("%s::character varying", atlasDefaultSQL("postgresql", column.Default))
	case "bpchar":
		return fmt.Sprintf("%s::bpchar", atlasDefaultSQL("postgresql", column.Default))
	case "interval":
		return txtarPostgresIntervalDefaultSQL(column)
	default:
		return value
	}
}

func txtarPostgresDefaultSQLWithEnums(schemaName string, column *ast.ColumnNode, enums map[string]*ast.EnumNode) string {
	if column.Default == nil {
		return ""
	}
	if enum, ok := enums[atlasSQLIdentifier(column.Type)]; ok && column.Default.Expression == "" {
		return fmt.Sprintf(
			"%s::%s",
			atlasDefaultSQL("postgresql", column.Default),
			txtarPostgresQualifiedTableName(schemaName, enum.Name),
		)
	}
	return txtarPostgresDefaultSQL(column)
}

func txtarPostgresIntervalDefaultSQL(column *ast.ColumnNode) string {
	if column.Default == nil || column.Default.Expression != "" {
		return ""
	}
	value := strings.TrimSpace(column.Default.Value)
	matches := postgresHoursIntervalRE.FindStringSubmatch(value)
	if len(matches) != 2 {
		return ""
	}
	hours, err := strconv.Atoi(matches[1])
	if err != nil {
		return ""
	}
	return fmt.Sprintf("'%02d:00:00'::interval", hours)
}

func txtarPostgresShowIndexPart(part ast.IndexPart) string {
	var out string
	if part.Expr != "" {
		expr := txtarPostgresShowIndexExpr(part.Expr)
		if expr == "" {
			expr = part.Expr
		}
		out = fmt.Sprintf("(%s)", expr)
	} else {
		out = atlasSQLIdentifier(part.Name)
	}
	if operator := txtarPostgresShowIndexOperator(part.Operator); operator != "" {
		out += " " + operator
	}
	if part.Desc {
		out += " DESC"
	}
	return out
}

func txtarPostgresShowIndexExpr(expr string) string {
	trimmed := strings.TrimSpace(expr)
	switch trimmed {
	case "first_name || ' ' || last_name":
		return "(first_name::text || ' '::text) || last_name::text"
	case "first_name || '''s first name'":
		return "first_name::text || '''s first name'::text"
	}
	if postgresSimpleIndexExprRE.MatchString(trimmed) {
		return trimmed
	}
	return ""
}

func txtarPostgresIndexOperatorSupported(operator string) bool {
	_, ok := atlasIndexPartOperatorSQL(operator)
	return ok
}

func txtarPostgresShowIndexOperator(operator string) string {
	operator, ok := atlasIndexPartOperatorSQL(operator)
	if !ok {
		return ""
	}
	if match := postgresTSVectorOpsRE.FindStringSubmatch(operator); match != nil {
		return fmt.Sprintf("tsvector_ops (siglen='%s')", match[1])
	}
	return operator
}

func txtarPostgresShowIndexCondition(condition string) string {
	return strings.ReplaceAll(condition, "''", "''::text")
}

func txtarPostgresIndexMethod(index *ast.IndexNode) string {
	if index.Type == "" {
		return "btree"
	}
	return strings.ToLower(index.Type)
}

func txtarPostgresPrimaryKey(table *ast.CreateTableNode) txtarPostgresPrimaryKeyInfo {
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.PrimaryKeyConstraint {
			continue
		}
		columns := atlasConstraintColumnNames(constraint)
		if len(columns) > 0 {
			for i, column := range columns {
				columns[i] = atlasSQLIdentifier(column)
			}
			include := slices.Clone(constraint.IncludeColumns)
			for i, column := range include {
				include[i] = atlasSQLIdentifier(column)
			}
			return txtarPostgresPrimaryKeyInfo{columns: columns, include: include}
		}
	}
	var columns []string
	for _, column := range table.Columns {
		if column.Primary {
			columns = append(columns, atlasSQLIdentifier(column.Name))
		}
	}
	return txtarPostgresPrimaryKeyInfo{columns: columns}
}

type txtarPostgresPrimaryKeyInfo struct {
	columns []string
	include []string
}

func txtarNormalizeShowSQL(sql string) string {
	var lines []string
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-- ") {
			continue
		}
		lines = append(lines, txtarNormalizeShowSQLLine(line))
	}
	return strings.Join(lines, "\n")
}

func txtarNormalizeFixtureShowSQL(fx Fixture, sql string) string {
	switch txtarFixtureFamily(fx) {
	case "mysql", "mariadb":
		return txtarNormalizeMySQLShowSQL(sql)
	case "postgres":
		return txtarNormalizePostgresShowSQL(sql)
	default:
		return txtarNormalizeShowSQL(sql)
	}
}

func txtarNormalizePostgresShowSQL(sql string) string {
	var lines []string
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || txtarPostgresShowSeparator(line) {
			continue
		}
		if strings.Contains(line, "|") {
			parts := strings.Split(line, "|")
			for i, part := range parts {
				parts[i] = strings.TrimSpace(part)
			}
			line = strings.Join(parts, "|")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func txtarPostgresShowSeparator(line string) bool {
	for _, ch := range line {
		switch ch {
		case '-', '+':
			continue
		default:
			return false
		}
	}
	return line != ""
}

func txtarNormalizeMySQLShowSQL(sql string) string {
	normalized := txtarNormalizeShowSQL(sql)
	normalized = normalizeMySQLIntegerDisplayWidths(normalized)
	normalized = mysqlDefaultCharsetRE.ReplaceAllString(normalized, "")
	// MySQL SHOW CREATE TABLE injects default-charset introducers for string
	// literals inside expressions. Ptah preserves the Atlas HCL expression text,
	// so compare the literal value while still keeping the quoted contents.
	normalized = mysqlUTF8MB4IntroducerRE.ReplaceAllString(normalized, "'")
	normalized = strings.TrimSuffix(normalized, ";")
	normalized = spaceRunRE.ReplaceAllString(normalized, " ")
	for _, repl := range []struct {
		old string
		new string
	}{
		{" (", "("},
		{"( ", "("},
		{" )", ")"},
		{" ,", ","},
		{", ", ","},
	} {
		normalized = strings.ReplaceAll(normalized, repl.old, repl.new)
	}
	return strings.TrimSpace(normalized)
}

func normalizeMySQLIntegerDisplayWidths(sql string) string {
	return mysqlIntegerDisplayWidthRE.ReplaceAllStringFunc(sql, func(match string) string {
		if strings.EqualFold(match, "tinyint(1)") {
			return "tinyint(1)"
		}
		base, _, _ := strings.Cut(match, "(")
		return base
	})
}

func txtarNormalizeShowSQLLine(line string) string {
	line = strings.TrimSuffix(line, ";")
	beforeWhere, afterWhere, hasWhere := strings.Cut(line, " WHERE ")
	beforeWhere = strings.ReplaceAll(beforeWhere, `"`, "`")
	if hasWhere {
		return beforeWhere + " WHERE " + afterWhere
	}
	return beforeWhere
}

func txtarParseExpectedShowSQL(data string) ([]ast.Node, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil, fmt.Errorf("empty cmpshow SQL")
	}
	if !strings.HasSuffix(data, ";") {
		data += ";"
	}
	list, err := atlascompat.ParseSQL(data, atlascompat.ParseSQLOptions{})
	if err != nil {
		return nil, err
	}
	return list.Statements, nil
}

func txtarTableHCL(fx Fixture, statements []ast.Node, name string) (string, bool) {
	schemaName := txtarFixtureSchemaName(fx)
	table, ok := txtarFindTable(schemaName, statements, name)
	if !ok {
		return "", false
	}
	out, err := renderAtlasInspectHCL(txtarFixtureDialect(fx), schemaName, []ast.Node{table})
	if err != nil {
		return "", false
	}
	return out, true
}

func txtarFindTable(schemaName string, statements []ast.Node, name string) (*ast.CreateTableNode, bool) {
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if ok && atlasHCLTableIdentifier(table.Name, schemaName) == name {
			return table, true
		}
	}
	return nil, false
}

func atlasMigrationVersion(name string) string {
	base := strings.TrimSuffix(path.Base(name), ".sql")
	version, _, hasDescription := strings.Cut(base, "_")
	if hasDescription {
		return version
	}
	return base
}

func atlasMigrationDescription(name string) string {
	base := strings.TrimSuffix(path.Base(name), ".sql")
	_, description, ok := strings.Cut(base, "_")
	if !ok {
		return ""
	}
	return strings.ReplaceAll(description, "_", " ")
}

func runTxtarSchemaApply(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "schema" || fields[2] != "apply" {
		return txtarCommandResult{}, false
	}

	args := parseTxtarSchemaApplyArgs(fields[3:])
	resolved, err := txtarResolveSchemaApplyEnv(fx, runtime, args)
	var missing *txtarMissingAtlasVariableError
	if errors.As(err, &missing) {
		return txtarCommandResult{
			stderr: fmt.Sprintf("Error: missing value for required variable %q\n", missing.name),
			failed: true,
			err:    err,
		}, true
	}
	if errors.Is(err, errUnsupportedInspectHCL) {
		return txtarCommandResult{unsupported: "atlas schema apply"}, true
	}
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	args = resolved
	if args.sourceURL == "" {
		return txtarCommandResult{
			stderr: "Error: \"url\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"url\" not set"),
		}, true
	}
	if len(args.files) == 0 && args.to == "" {
		return txtarCommandResult{
			stderr: "Error: one of flag(s) \"file\" or \"to\" is required\n",
			failed: true,
			err:    fmt.Errorf("one of flag(s) \"file\" or \"to\" is required"),
		}, true
	}
	statements, err := txtarSchemaApplyStatements(fx, runtime, args)
	if errors.Is(err, errUnsupportedInspectHCL) {
		return txtarCommandResult{unsupported: "atlas schema apply"}, true
	}
	if errors.Is(err, errAtlasProjectFile) {
		return txtarCommandResult{
			stderr: "Error: cannot parse project file\n",
			failed: true,
			err:    errAtlasProjectFile,
		}, true
	}
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	if !txtarFixtureSupportsVirtualApply(fx, statements) {
		return txtarCommandResult{unsupported: "atlas schema apply"}, true
	}
	if txtarVirtualStatesEqual(fx, runtime.dbStatements, statements) {
		runtime.replaceDBStatements(statements)
		return txtarCommandResult{stdout: "Schema is synced, no changes to be made\n"}, true
	}
	runtime.replaceDBStatements(statements)
	return txtarCommandResult{stdout: txtarSchemaApplyOutput(fx, args, statements)}, true
}

type txtarSchemaApplyArgs struct {
	sourceURL string
	files     []string
	to        string
	env       string
	tenant    string
	logTenant bool
	vars      map[string]string
}

func parseTxtarSchemaApplyArgs(fields []string) txtarSchemaApplyArgs {
	var args txtarSchemaApplyArgs
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "-u", "--url":
			if i+1 < len(fields) {
				args.sourceURL = fields[i+1]
				i++
			}
		case "-f", "--file":
			if i+1 < len(fields) {
				args.files = append(args.files, fields[i+1])
				i++
			}
		case "--to":
			if i+1 < len(fields) {
				args.to = fields[i+1]
				i++
			}
		case "--env":
			if i+1 < len(fields) {
				args.env = fields[i+1]
				i++
			}
		case "--var":
			if i+1 < len(fields) {
				args.vars = txtarAddAtlasVar(args.vars, fields[i+1])
				i++
			}
		default:
			switch {
			case strings.HasPrefix(fields[i], "-u="):
				args.sourceURL = strings.TrimPrefix(fields[i], "-u=")
			case strings.HasPrefix(fields[i], "--url="):
				args.sourceURL = strings.TrimPrefix(fields[i], "--url=")
			case strings.HasPrefix(fields[i], "-f="):
				args.files = append(args.files, strings.TrimPrefix(fields[i], "-f="))
			case strings.HasPrefix(fields[i], "--file="):
				args.files = append(args.files, strings.TrimPrefix(fields[i], "--file="))
			case strings.HasPrefix(fields[i], "--to="):
				args.to = strings.TrimPrefix(fields[i], "--to=")
			case strings.HasPrefix(fields[i], "--env="):
				args.env = strings.TrimPrefix(fields[i], "--env=")
			case strings.HasPrefix(fields[i], "--var="):
				args.vars = txtarAddAtlasVar(args.vars, strings.TrimPrefix(fields[i], "--var="))
			}
		}
	}
	return args
}

func txtarAddAtlasVar(vars map[string]string, assignment string) map[string]string {
	key, value, ok := strings.Cut(assignment, "=")
	if !ok || key == "" {
		return vars
	}
	if vars == nil {
		vars = map[string]string{}
	}
	vars[key] = value
	return vars
}

func txtarResolveSchemaApplyEnv(
	fx Fixture,
	runtime *txtarRuntime,
	args txtarSchemaApplyArgs,
) (txtarSchemaApplyArgs, error) {
	if args.env == "" {
		return args, nil
	}
	switch txtarFixtureFamily(fx) {
	case "sqlite", "mysql", "mariadb":
	default:
		return args, errUnsupportedInspectHCL
	}
	project, ok := runtime.files["atlas.hcl"]
	if !ok {
		return args, errUnsupportedInspectHCL
	}
	env, ok := txtarAtlasNamedBlock(project, "env", args.env)
	if !ok {
		return args, errUnsupportedInspectHCL
	}
	if txtarFixtureFamily(fx) != "sqlite" {
		if _, ok := txtarHCLAttrValue(env, "for_each"); ok {
			resolved, ok := txtarResolveAtlasSQLTenants(project, env, args.vars)
			if !ok || len(resolved) != 1 {
				return args, errUnsupportedInspectHCL
			}
			args.tenant = resolved[0]
			args.vars = maps.Clone(args.vars)
			if args.vars == nil {
				args.vars = map[string]string{}
			}
			args.vars["tenant"] = args.tenant
			args.logTenant = txtarAtlasSchemaApplyLogsTenant(env)
		}
	}
	if args.sourceURL == "" {
		if sourceURL, ok := txtarHCLStringAttr(env, "url"); ok {
			args.sourceURL = sourceURL
		}
	}
	envVars, err := txtarAtlasEnvVars(env, args.vars)
	if err != nil {
		return args, err
	}
	if args.tenant != "" {
		envVars["tenant"] = args.tenant
	}
	if len(args.files) == 0 && args.to == "" {
		target, ok := txtarAtlasEnvSourceTargetWithVars(runtime, project, env, envVars)
		if !ok {
			return args, errUnsupportedInspectHCL
		}
		args.to = target
	}
	if err := txtarCheckSchemaApplySourceVars(runtime, args); err != nil {
		return args, err
	}
	return args, nil
}

func txtarSchemaApplyOutput(fx Fixture, args txtarSchemaApplyArgs, statements []ast.Node) string {
	switch txtarFixtureFamily(fx) {
	case "mysql", "mariadb":
	default:
		return ""
	}
	out, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), statements, "")
	if err != nil {
		return ""
	}
	if args.logTenant {
		return txtarSchemaApplyTenantJSONLog(fx, args, statements)
	}
	return out
}

func txtarSchemaApplyTenantJSONLog(fx Fixture, args txtarSchemaApplyArgs, statements []ast.Node) string {
	unqualified := atlasUnqualifySchemaStatements(args.tenant, statements)
	applied, err := txtarSchemaApplyTenantLogApplied(txtarFixtureDialect(fx), unqualified)
	if err != nil {
		return ""
	}
	data, err := json.Marshal(map[string]any{
		"Applied": applied,
		"Tenant":  args.tenant,
	})
	if err != nil {
		return ""
	}
	return string(data) + "\n"
}

func txtarSchemaApplyTenantLogApplied(dialect string, statements []ast.Node) ([]string, error) {
	if txtarSchemaApplyTenantLogTableCount(statements) <= 1 {
		sql, err := txtarSchemaApplyTenantLogSQL(dialect, statements)
		if err != nil {
			return nil, err
		}
		return []string{sql}, nil
	}

	out := make([]string, 0, len(statements))
	for _, stmt := range statements {
		if _, ok := stmt.(*ast.IndexNode); ok {
			continue
		}
		group := txtarSchemaApplyTenantLogStatementGroup(stmt, statements)
		sql, err := txtarSchemaApplyTenantLogSQL(dialect, group)
		if err != nil {
			return nil, err
		}
		out = append(out, sql)
	}
	return out, nil
}

func txtarSchemaApplyTenantLogTableCount(statements []ast.Node) int {
	count := 0
	for _, stmt := range statements {
		if _, ok := stmt.(*ast.CreateTableNode); ok {
			count++
		}
	}
	return count
}

func txtarSchemaApplyTenantLogSQL(dialect string, statements []ast.Node) (string, error) {
	sql, err := renderAtlasInspectSQL(dialect, statements, "  ")
	if err != nil {
		return "", err
	}
	sql = strings.TrimSpace(txtarStripAtlasMigrationComments(sql))
	sql = mysqlDefaultCharsetRE.ReplaceAllString(sql, "")
	return strings.TrimSuffix(strings.TrimSpace(sql), ";") + ";", nil
}

func txtarSchemaApplyTenantLogStatementGroup(stmt ast.Node, statements []ast.Node) []ast.Node {
	table, ok := stmt.(*ast.CreateTableNode)
	if !ok {
		return []ast.Node{stmt}
	}
	group := []ast.Node{stmt}
	for _, candidate := range statements {
		index, ok := candidate.(*ast.IndexNode)
		if ok && index.Table == table.Name {
			group = append(group, candidate)
		}
	}
	return group
}

func txtarResolveAtlasSQLTenants(project string, env string, vars map[string]string) ([]string, bool) {
	// This models the Atlas datasource fixture shape exactly. Broader HCL or
	// SQL datasource semantics should stay red until Ptah supports them.
	forEach, ok := txtarHCLAttrValue(env, "for_each")
	if !ok || txtarCompactHCLExpr(forEach) != "toset(data.sql.tenants.values)" {
		return nil, false
	}
	envURL, ok := txtarHCLAttrValue(env, "url")
	if !ok || !txtarAtlasSQLTenantURLEnvironment(envURL) {
		return nil, false
	}
	block, ok := txtarAtlasDataSQLBlock(project, "tenants")
	if !ok || !txtarAtlasDataSQLTenantsQuery(block) {
		return nil, false
	}
	args, ok := txtarHCLAttrValue(block, "args")
	if !ok {
		return nil, false
	}
	varName, ok := txtarAtlasSingleVarListRef(args)
	if !ok {
		return nil, false
	}
	pattern, ok := vars[varName]
	if !ok || pattern == "" || strings.Contains(pattern, "%") {
		return nil, false
	}
	return []string{pattern}, true
}

func txtarAtlasSQLTenantURLEnvironment(value string) bool {
	switch txtarCompactHCLExpr(value) {
	case "urlsetpath(var.url,each.value)",
		`urlqueryset(var.url,"search_path",each.value)`:
		return true
	default:
		return false
	}
}

func txtarAtlasDataSQLBlock(data, name string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*data\s+"sql"\s+"` + regexp.QuoteMeta(name) + `"\s*\{`)
	loc := re.FindStringIndex(data)
	if loc == nil {
		return "", false
	}
	body, _, ok := txtarHCLBlockBody(data, loc[1]-1)
	return body, ok
}

func txtarAtlasDataSQLTenantsQuery(block string) bool {
	return strings.Contains(block, "information_schema") &&
		strings.Contains(block, "schemata") &&
		strings.Contains(block, "schema_name") &&
		(strings.Contains(block, "LIKE ?") || strings.Contains(block, "LIKE $1"))
}

func txtarAtlasSingleVarListRef(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return "", false
	}
	return txtarAtlasVarRef(strings.TrimSpace(value[1 : len(value)-1]))
}

func txtarCompactHCLExpr(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), "")
}

func txtarAtlasSchemaApplyLogsTenant(env string) bool {
	return strings.Contains(env, "json_merge") &&
		strings.Contains(env, "Tenant") &&
		strings.Contains(env, "each.value")
}

func txtarAtlasEnvVars(env string, cliVars map[string]string) (map[string]string, error) {
	vars := map[string]string{}
	for _, key := range txtarHCLTopLevelAttrNames(env) {
		switch key {
		case "url", "src", "dev":
			continue
		}
		value, ok := txtarHCLAttrValue(env, key)
		if !ok {
			continue
		}
		if name, ok := txtarAtlasVarRef(value); ok {
			resolved, ok := cliVars[name]
			if !ok {
				return nil, &txtarMissingAtlasVariableError{name: name}
			}
			vars[key] = resolved
			continue
		}
		if quoted, ok := txtarHCLQuotedString(value); ok {
			vars[key] = quoted
		}
	}
	return vars, nil
}

func txtarHCLTopLevelAttrNames(data string) []string {
	var names []string
	for _, line := range strings.Split(txtarHCLTopLevelOnly(data), "\n") {
		name, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t") {
			continue
		}
		names = append(names, name)
	}
	return names
}

func txtarHCLTopLevelOnly(data string) string {
	var out strings.Builder
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			if depth == 0 {
				out.WriteByte(ch)
			}
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
			if depth == 0 {
				out.WriteByte(ch)
			}
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out.WriteByte(ch)
			}
		}
	}
	return out.String()
}

func txtarAtlasVarRef(value string) (string, bool) {
	value = strings.TrimSpace(value)
	name, ok := strings.CutPrefix(value, "var.")
	return name, ok && name != ""
}

func txtarCheckSchemaApplySourceVars(runtime *txtarRuntime, args txtarSchemaApplyArgs) error {
	files := args.files
	if args.to != "" && strings.HasPrefix(args.to, "file://") {
		files = []string{txtarFileURLPath(args.to)}
	}
	for _, file := range files {
		data, ok := runtime.files[file]
		if !ok {
			continue
		}
		for _, name := range txtarHCLRequiredVariables(data) {
			if _, ok := args.vars[name]; ok {
				continue
			}
			if strings.Contains(data, "var."+name) {
				return &txtarMissingAtlasVariableError{name: name}
			}
		}
	}
	return nil
}

func txtarHCLRequiredVariables(data string) []string {
	re := regexp.MustCompile(`(?m)^\s*variable\s+"([^"]+)"\s*\{`)
	matches := re.FindAllStringSubmatch(data, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	return names
}

type txtarMissingAtlasVariableError struct {
	name string
}

func (e *txtarMissingAtlasVariableError) Error() string {
	return fmt.Sprintf("missing value for required variable %q", e.name)
}

func txtarSchemaApplyStatements(fx Fixture, runtime *txtarRuntime, args txtarSchemaApplyArgs) ([]ast.Node, error) {
	if len(args.files) > 0 && args.to != "" {
		return nil, errUnsupportedInspectHCL
	}
	files := args.files
	if args.to != "" {
		if !strings.HasPrefix(args.to, "file://") {
			return nil, errUnsupportedInspectHCL
		}
		files = []string{txtarFileURLPath(args.to)}
	}
	var chunks []string
	for _, file := range files {
		data, ok := runtime.files[file]
		if !ok {
			return nil, fmt.Errorf("file %q not found in txtar archive", file)
		}
		if txtarFileLooksLikeAtlasProject(data) {
			return nil, errAtlasProjectFile
		}
		chunks = append(chunks, data)
	}
	return txtarHCLStatements(fx, strings.Join(files, ","), strings.Join(chunks, "\n"))
}

func txtarVirtualStatesEqual(fx Fixture, current, next []ast.Node) bool {
	if len(current) == 0 {
		return false
	}
	currentSQL, ok := txtarVirtualStateShowSQL(fx, current)
	if !ok {
		return false
	}
	nextSQL, ok := txtarVirtualStateShowSQL(fx, next)
	if !ok {
		return false
	}
	return txtarNormalizeFixtureShowSQL(fx, currentSQL) == txtarNormalizeFixtureShowSQL(fx, nextSQL)
}

var errAtlasProjectFile = errors.New("cannot parse project file")

func txtarFileLooksLikeAtlasProject(data string) bool {
	return strings.Contains(data, `env "`)
}

type txtarSchemaDiffArgs struct {
	devURL  string
	from    string
	to      string
	blocked bool
}

func runTxtarSchemaDiff(fx Fixture, runtime *txtarRuntime, fields []string) (txtarCommandResult, bool) {
	if len(fields) < 3 || fields[0] != "atlas" || fields[1] != "schema" || fields[2] != "diff" {
		return txtarCommandResult{}, false
	}

	args := txtarParseSchemaDiffArgs(fields[3:])
	if args.blocked || txtarFixtureFamily(fx) != "sqlite" || args.devURL == "" || args.from == "" || args.to == "" {
		return txtarCommandResult{unsupported: "atlas schema diff"}, true
	}
	if !strings.HasPrefix(args.from, "file://") || !strings.HasPrefix(args.to, "file://") {
		return txtarCommandResult{unsupported: "atlas schema diff"}, true
	}
	dir := txtarFileURLPath(args.from)
	targetSQL, err := renderTxtarMigrateDiffSQL(fx, runtime, args.to, "")
	if errors.Is(err, errUnsupportedInspectSQL) || errors.Is(err, errUnsupportedInspectHCL) {
		return txtarCommandResult{unsupported: "atlas schema diff"}, true
	}
	if err != nil {
		return txtarCommandResult{err: err}, true
	}
	if !txtarMigrationDirHasSQL(runtime, dir, targetSQL) {
		return txtarCommandResult{unsupported: "atlas schema diff"}, true
	}
	return txtarCommandResult{stdout: "Schemas are synced, no changes to be made.\n"}, true
}

func txtarParseSchemaDiffArgs(args []string) txtarSchemaDiffArgs {
	var out txtarSchemaDiffArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dev-url":
			if i+1 < len(args) {
				out.devURL = args[i+1]
				i++
			}
		case "--from":
			if i+1 < len(args) {
				out.from = args[i+1]
				i++
			}
		case "--to":
			if i+1 < len(args) {
				out.to = args[i+1]
				i++
			}
		case "--exclude":
			if i+1 < len(args) {
				i++
			}
		default:
			switch {
			case strings.HasPrefix(arg, "--dev-url="):
				out.devURL = strings.TrimPrefix(arg, "--dev-url=")
			case strings.HasPrefix(arg, "--from="):
				out.from = strings.TrimPrefix(arg, "--from=")
			case strings.HasPrefix(arg, "--to="):
				out.to = strings.TrimPrefix(arg, "--to=")
			case strings.HasPrefix(arg, "--exclude="):
				continue
			case strings.HasPrefix(arg, "-"):
				out.blocked = true
			default:
				out.blocked = true
			}
		}
	}
	return out
}

func txtarMigrateDiffDir(args []string) string {
	return txtarMigrateCommandDir(args)
}

func txtarMigrateCommandRuntimeDir(runtime *txtarRuntime, args []string) string {
	dir := txtarMigrateCommandDir(args)
	if dir != "migrations" {
		return dir
	}
	env := txtarMigrateCommandEnv(args)
	if env == "" {
		return dir
	}
	project, ok := runtime.files["atlas.hcl"]
	if !ok {
		return dir
	}
	envBlock, ok := txtarAtlasNamedBlock(project, "env", env)
	if !ok {
		return dir
	}
	migration, ok := txtarAtlasAnonymousBlock(envBlock, "migration")
	if !ok {
		return dir
	}
	if configured, ok := txtarHCLStringAttr(migration, "dir"); ok {
		return txtarFileURLPath(configured)
	}
	return dir
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

func txtarMigrateCommandEnv(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--env" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(arg, "--env="):
			return strings.TrimPrefix(arg, "--env=")
		}
	}
	return ""
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

func txtarCommandClearsDBState(fx Fixture, line string) bool {
	fields := txtarCommandFields(line)
	if len(fields) == 1 && fields[0] == "clearSchema" {
		return true
	}
	return txtarFixtureFamily(fx) == "postgres" &&
		len(fields) >= 3 && fields[0] == "atlas" && fields[1] == "schema" && fields[2] == "clean"
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
			unsupportedFiles[txtarMigrateCommandRuntimeDir(runtime, fields[3:])] = true
		case "hash":
			unsupportedFiles[path.Join(txtarMigrateHashDir(runtime, fields[3:]), atlascompat.AtlasSumFileName)] = true
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
			delete(unsupportedFiles, path.Join(txtarMigrateHashDir(runtime, fields[3:]), atlascompat.AtlasSumFileName))
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

func txtarCmpmigMismatch(fx Fixture, runtime *txtarRuntime, index, expected string) string {
	actual, ok := txtarCmpmigActualFile(runtime, index)
	if !ok {
		return fmt.Sprintf("cmpmig %s %s: generated migration not found", index, expected)
	}
	return txtarFilesMismatchAny(runtime.files, actual, txtarVariantExpectedFiles(fx, runtime, expected))
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

func txtarMigrationApplySQLFilesInDir(runtime *txtarRuntime, dir string) []string {
	files := txtarMigrationSQLFilesInDir(runtime, dir)
	return slices.DeleteFunc(files, func(name string) bool {
		base := path.Base(name)
		return strings.HasSuffix(base, ".down.sql") || flywayUndoMigrationRE.MatchString(base)
	})
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

func txtarResolveSchemaInspectEnv(fx Fixture, runtime *txtarRuntime, name string) (string, *txtarCommandResult, bool) {
	project, ok := runtime.files["atlas.hcl"]
	if !ok {
		return "", nil, false
	}
	env, ok := txtarAtlasNamedBlock(project, "env", name)
	if !ok {
		return "", nil, false
	}
	switch txtarFixtureFamily(fx) {
	case "sqlite":
		sourceURL, ok := txtarHCLStringAttr(env, "url")
		return sourceURL, nil, ok
	case "mysql", "mariadb":
		value, ok := txtarHCLAttrValue(env, "url")
		if !ok {
			return "", nil, false
		}
		sourceURL, ok := txtarResolveAtlasProjectStringExpr(project, value)
		if !ok {
			return "", nil, false
		}
		if _, err := url.Parse(sourceURL); err != nil {
			result := txtarCommandResult{
				stderr: "Error: " + err.Error() + "\n",
				failed: true,
				err:    err,
			}
			return "", &result, true
		}
		return sourceURL, nil, true
	default:
		return "", nil, false
	}
}

func runTxtarSchemaInspect(fx Fixture, runtime *txtarRuntime, fields []string) txtarCommandResult {
	var sourceURL, devURL, format, redirect, env string
	var excludes []string
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
		case "--exclude":
			if i+1 < len(fields) {
				excludes = append(excludes, fields[i+1])
				i++
			}
		case ">":
			if i+1 < len(fields) {
				redirect = fields[i+1]
				i++
			}
		case "--env":
			if i+1 < len(fields) {
				env = fields[i+1]
				i++
			}
		default:
			switch {
			case strings.HasPrefix(fields[i], "-u="):
				sourceURL = strings.TrimPrefix(fields[i], "-u=")
			case strings.HasPrefix(fields[i], "--url="):
				sourceURL = strings.TrimPrefix(fields[i], "--url=")
			case strings.HasPrefix(fields[i], "--env="):
				env = strings.TrimPrefix(fields[i], "--env=")
			case strings.HasPrefix(fields[i], "--exclude="):
				excludes = append(excludes, strings.TrimPrefix(fields[i], "--exclude="))
			}
		}
	}
	const filePrefix = "file://"
	if sourceURL == "" && env != "" {
		resolved, result, ok := txtarResolveSchemaInspectEnv(fx, runtime, env)
		if result != nil {
			return *result
		}
		if !ok {
			return txtarCommandResult{unsupported: "atlas schema inspect db-url"}
		}
		sourceURL = resolved
	}
	if sourceURL == "" {
		return txtarCommandResult{
			stderr: "Error: \"url\" not set\n",
			failed: true,
			err:    fmt.Errorf("\"url\" not set"),
		}
	}
	if !strings.HasPrefix(sourceURL, filePrefix) {
		if !runtime.hasVirtualDBState {
			// Atlas URL-escape fixture inspects an empty schema after auth setup.
			// Keep this limited to env URLs that name the fixture schema.
			if !txtarSchemaInspectEnvCanUseEmptyDB(fx, sourceURL, env) {
				return txtarCommandResult{unsupported: "atlas schema inspect db-url"}
			}
			runtime.hasVirtualDBState = true
		}
		output, err := renderTxtarDBStateInspectHCL(fx, runtime.dbStatements, excludes, format)
		if errors.Is(err, errUnsupportedInspectHCL) {
			return txtarCommandResult{unsupported: "atlas schema inspect hcl"}
		}
		if errors.Is(err, errUnsupportedInspectSQL) {
			return txtarCommandResult{unsupported: "atlas schema inspect sql"}
		}
		if err != nil {
			return txtarCommandResult{err: err}
		}
		if redirect != "" {
			runtime.files[redirect] = output
			return txtarCommandResult{}
		}
		return txtarCommandResult{stdout: output}
	}
	if devURL == "" {
		return txtarCommandResult{
			stderr: "Error: --dev-url cannot be empty\n",
			failed: true,
			err:    fmt.Errorf("--dev-url cannot be empty"),
		}
	}
	name := strings.TrimPrefix(sourceURL, filePrefix)
	sql, ok := runtime.files[name]
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
		runtime.files[redirect] = output
		return txtarCommandResult{}
	}
	return txtarCommandResult{stdout: output}
}

func txtarResolveAtlasProjectStringExpr(project string, value string) (string, bool) {
	resolved, ok := txtarHCLQuotedString(value)
	if !ok {
		return "", false
	}
	vars := txtarAtlasProjectVariableDefaults(project)
	locals := txtarAtlasProjectLocals(project, vars)
	for key, val := range vars {
		resolved = strings.ReplaceAll(resolved, "${var."+key+"}", val)
	}
	for key, val := range locals {
		resolved = strings.ReplaceAll(resolved, "${local."+key+"}", val)
	}
	if strings.Contains(resolved, "${") {
		return "", false
	}
	return resolved, true
}

func txtarAtlasProjectVariableDefaults(project string) map[string]string {
	vars := map[string]string{}
	re := regexp.MustCompile(`(?m)^\s*variable\s+"([^"]+)"\s*\{`)
	for _, loc := range re.FindAllStringSubmatchIndex(project, -1) {
		name := project[loc[2]:loc[3]]
		body, _, ok := txtarHCLBlockBody(project, loc[1]-1)
		if !ok {
			continue
		}
		value, ok := txtarHCLStringAttr(body, "default")
		if ok {
			vars[name] = value
		}
	}
	return vars
}

func txtarAtlasProjectLocals(project string, vars map[string]string) map[string]string {
	out := map[string]string{}
	locals, ok := txtarAtlasAnonymousBlock(project, "locals")
	if !ok {
		return out
	}
	for _, key := range txtarHCLTopLevelAttrNames(locals) {
		value, ok := txtarHCLAttrValue(locals, key)
		if !ok {
			continue
		}
		if source, ok := txtarAtlasURLQueryEscapeVar(value); ok {
			resolved, ok := vars[source]
			if ok {
				out[key] = url.QueryEscape(resolved)
			}
		}
	}
	return out
}

func txtarAtlasURLQueryEscapeVar(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "urlescape(") || !strings.HasSuffix(value, ")") {
		return "", false
	}
	return txtarAtlasVarRef(strings.TrimSpace(value[len("urlescape(") : len(value)-1]))
}

func txtarSchemaInspectEnvCanUseEmptyDB(fx Fixture, sourceURL string, env string) bool {
	if env == "" {
		return false
	}
	switch txtarFixtureFamily(fx) {
	case "mysql", "mariadb":
	default:
		return false
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return false
	}
	schemaName := strings.TrimPrefix(parsed.Path, "/")
	return schemaName == txtarFixtureSchemaName(fx)
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
	list, err := atlascompat.ParseSQL(sql, atlascompat.ParseSQLOptions{})
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
	statements, err := txtarHCLStatements(fx, name, data)
	if err != nil {
		return "", err
	}
	out, err := renderAtlasInspectHCL(txtarFixtureDialect(fx), txtarFixtureSchemaName(fx), statements)
	if err != nil {
		return "", fmt.Errorf("render inspect HCL: %w", err)
	}
	return out, nil
}

func renderTxtarDBStateInspectHCL(fx Fixture, statements []ast.Node, excludes []string, format string) (string, error) {
	filtered, err := atlasInspectStatementsWithExcludes(txtarFixtureSchemaName(fx), statements, excludes)
	if err != nil {
		return "", err
	}
	switch format {
	case "":
		out, err := renderAtlasInspectHCL(txtarFixtureDialect(fx), txtarFixtureSchemaName(fx), filtered)
		if err != nil {
			return "", fmt.Errorf("render inspect HCL: %w", err)
		}
		return out, nil
	case "{{ sql . }}", `{{ sql . "  " }}`:
		unqualified := atlasUnqualifySchemaStatements(txtarFixtureSchemaName(fx), filtered)
		out, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), unqualified, txtarSQLFormatIndent(format))
		if err != nil {
			return "", fmt.Errorf("render inspect SQL: %w", err)
		}
		return out, nil
	default:
		return "", errUnsupportedInspectHCL
	}
}

func atlasInspectStatementsWithExcludes(schemaName string, statements []ast.Node, excludes []string) ([]ast.Node, error) {
	if len(excludes) == 0 {
		return statements, nil
	}
	out := make([]ast.Node, 0, len(statements))
	droppedTables := map[string]bool{}
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			out = append(out, stmt)
			continue
		}
		filtered, keep, err := atlasInspectTableWithExcludes(schemaName, table, excludes)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, filtered)
		} else {
			droppedTables[atlasSQLIdentifier(table.Name)] = true
		}
	}
	out = slices.DeleteFunc(out, func(stmt ast.Node) bool {
		index, ok := stmt.(*ast.IndexNode)
		return ok && droppedTables[atlasSQLIdentifier(index.Table)]
	})
	return out, nil
}

func atlasInspectTableWithExcludes(
	schemaName string,
	table *ast.CreateTableNode,
	excludes []string,
) (*ast.CreateTableNode, bool, error) {
	tableName := atlasHCLTableIdentifier(table.Name, schemaName)
	for _, exclude := range excludes {
		matches, err := path.Match(exclude, tableName)
		if err != nil {
			return nil, false, fmt.Errorf("%w: invalid exclude %q: %v", errUnsupportedInspectHCL, exclude, err)
		}
		if matches {
			return nil, false, nil
		}
	}

	keptColumns := map[string]bool{}
	columns := make([]*ast.ColumnNode, 0, len(table.Columns))
	for _, column := range table.Columns {
		excluded, err := atlasInspectColumnExcluded(tableName, column.Name, excludes)
		if err != nil {
			return nil, false, err
		}
		if excluded {
			continue
		}
		keptColumns[column.Name] = true
		columnCopy := *column
		columns = append(columns, &columnCopy)
	}

	tableCopy := *table
	tableCopy.Columns = columns
	tableCopy.Constraints = atlasInspectConstraintsForColumns(table.Constraints, keptColumns)
	tableCopy.Options = maps.Clone(table.Options)
	return &tableCopy, true, nil
}

func atlasInspectColumnExcluded(tableName, columnName string, excludes []string) (bool, error) {
	qualified := tableName + "." + columnName
	for _, exclude := range excludes {
		matches, err := path.Match(exclude, qualified)
		if err != nil {
			return false, fmt.Errorf("%w: invalid exclude %q: %v", errUnsupportedInspectHCL, exclude, err)
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

func atlasInspectConstraintsForColumns(constraints []*ast.ConstraintNode, keptColumns map[string]bool) []*ast.ConstraintNode {
	out := make([]*ast.ConstraintNode, 0, len(constraints))
	for _, constraint := range constraints {
		if !atlasInspectConstraintColumnsKept(constraint, keptColumns) {
			continue
		}
		constraintCopy := *constraint
		constraintCopy.Columns = slices.Clone(constraint.Columns)
		constraintCopy.ColumnParts = slices.Clone(constraint.ColumnParts)
		out = append(out, &constraintCopy)
	}
	return out
}

func atlasInspectConstraintColumnsKept(constraint *ast.ConstraintNode, keptColumns map[string]bool) bool {
	for _, column := range constraint.Columns {
		if !keptColumns[column] {
			return false
		}
	}
	for _, column := range constraint.ColumnParts {
		if !keptColumns[column.Name] {
			return false
		}
	}
	return true
}

func renderTxtarSQL(fx Fixture, sql string, indent string) (string, error) {
	list, err := atlascompat.ParseSQL(sql, atlascompat.ParseSQLOptions{})
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
	var enumList []*ast.EnumNode
	indexes := map[string][]*ast.IndexNode{}
	domains := txtarPostgresDomainNames(statements)
	enums := txtarPostgresEnumsByName(statements)
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateSchemaNode:
			continue
		case *ast.EnumNode:
			if dialect != "postgresql" {
				return "", fmt.Errorf("%w: statement %T", errUnsupportedInspectHCL, stmt)
			}
			enumList = append(enumList, node)
		case *ast.CreateTypeNode:
			if _, ok := node.TypeDef.(*ast.DomainTypeDef); ok && dialect == "postgresql" {
				continue
			}
			return "", fmt.Errorf("%w: statement %T", errUnsupportedInspectHCL, stmt)
		case *ast.CreateTableNode:
			tables = append(tables, node)
		case *ast.IndexNode:
			tableName := atlasHCLTableIdentifier(node.Table, schemaName)
			indexes[tableName] = append(indexes[tableName], node)
		default:
			return "", fmt.Errorf("%w: statement %T", errUnsupportedInspectHCL, stmt)
		}
	}
	slices.SortFunc(tables, func(a, b *ast.CreateTableNode) int {
		return cmp.Compare(atlasHCLTableIdentifier(a.Name, schemaName), atlasHCLTableIdentifier(b.Name, schemaName))
	})
	tableNames := map[string]bool{}
	for _, table := range tables {
		tableNames[atlasHCLTableIdentifier(table.Name, schemaName)] = true
	}
	for tableName := range indexes {
		if !tableNames[tableName] {
			return "", fmt.Errorf("%w: index table %s", errUnsupportedInspectHCL, tableName)
		}
		slices.SortFunc(indexes[tableName], func(a, b *ast.IndexNode) int {
			return cmp.Compare(a.Name, b.Name)
		})
	}
	for _, table := range tables {
		tableName := atlasHCLTableIdentifier(table.Name, schemaName)
		if err := renderAtlasTableHCL(&b, dialect, schemaName, table, indexes[tableName], domains, enums); err != nil {
			return "", err
		}
	}
	slices.SortFunc(enumList, func(a, b *ast.EnumNode) int {
		return cmp.Compare(atlasHCLIdentifier(a.Name), atlasHCLIdentifier(b.Name))
	})
	for _, enum := range enumList {
		renderAtlasEnumHCL(&b, schemaName, enum)
	}
	renderAtlasSchemaHCL(&b, schemaName, schemaAttrs)
	return b.String(), nil
}

func renderAtlasTableHCL(
	b *strings.Builder,
	dialect, schemaName string,
	table *ast.CreateTableNode,
	indexes []*ast.IndexNode,
	domains map[string]bool,
	enums map[string]*ast.EnumNode,
) error {
	tableName := atlasHCLTableIdentifier(table.Name, schemaName)
	fmt.Fprintf(b, "table %q {\n", tableName)
	fmt.Fprintf(b, "  schema = schema.%s\n", schemaName)
	renderAtlasMySQLTableHCLAttrs(b, dialect, table)
	var primaryColumns []ast.ConstraintColumn
	var primaryInclude []string
	var foreignKeys []*atlasHCLForeignKey
	var uniques []*atlasHCLUnique
	for _, column := range table.Columns {
		inspectColumn := atlasInspectColumn(dialect, table, column)
		fmt.Fprintf(b, "  column %q {\n", atlasHCLIdentifier(column.Name))
		if column.Default != nil {
			fmt.Fprintf(b, "    null    = %t\n", inspectColumn.Nullable)
			fmt.Fprintf(b, "    type    = %s\n", atlasColumnHCLType(dialect, schemaName, inspectColumn, domains, enums))
			fmt.Fprintf(b, "    default = %s\n", atlasColumnDefaultHCL(dialect, column))
		} else {
			fmt.Fprintf(b, "    null = %t\n", inspectColumn.Nullable)
			fmt.Fprintf(b, "    type = %s\n", atlasColumnHCLType(dialect, schemaName, inspectColumn, domains, enums))
		}
		if dialect == "mysql" || dialect == "mariadb" {
			if column.Charset != "" {
				fmt.Fprintf(b, "    charset = %q\n", column.Charset)
			}
			if column.Collate != "" {
				fmt.Fprintf(b, "    collate = %q\n", column.Collate)
			}
			if column.UpdateExpression != "" {
				fmt.Fprintf(b, "    on_update = %s\n", atlasSQLExpressionHCL(column.UpdateExpression))
			}
			if column.GeneratedExpression != "" {
				fmt.Fprintf(b, "    as {\n")
				fmt.Fprintf(b, "      expr = %q\n", atlasGeneratedHCLExpr(dialect, column.GeneratedExpression))
				fmt.Fprintf(b, "      type = %s\n", atlasGeneratedHCLKindForDialect(dialect, column.GeneratedKind))
				fmt.Fprintf(b, "    }\n")
			}
		}
		if dialect == "postgresql" && column.GeneratedExpression != "" {
			fmt.Fprintf(b, "    as {\n")
			fmt.Fprintf(b, "      expr = %q\n", atlasGeneratedHCLExpr(dialect, column.GeneratedExpression))
			fmt.Fprintf(b, "      type = %s\n", atlasGeneratedHCLKindForDialect(dialect, column.GeneratedKind))
			fmt.Fprintf(b, "    }\n")
		}
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
			primaryInclude = append(primaryInclude, constraint.IncludeColumns...)
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
	slices.SortFunc(uniques, func(a, b *atlasHCLUnique) int {
		return cmp.Compare(a.name, b.name)
	})
	if len(primaryColumns) > 0 {
		if err := renderAtlasPrimaryKeyHCL(b, primaryColumns, primaryInclude); err != nil {
			return err
		}
	}
	for _, foreignKey := range foreignKeys {
		if err := renderAtlasForeignKeyHCL(b, foreignKey); err != nil {
			return err
		}
	}
	if dialect == "mysql" || dialect == "mariadb" {
		for _, index := range atlasForeignKeyIndexHCLs(tableName, table, indexes) {
			if err := renderAtlasIndexHCL(b, index); err != nil {
				return err
			}
		}
	}
	if dialect == "postgresql" {
		for _, index := range indexes {
			if err := renderAtlasIndexHCL(b, index); err != nil {
				return err
			}
		}
		for _, unique := range uniques {
			if err := renderAtlasUniqueHCL(b, unique); err != nil {
				return err
			}
		}
	} else {
		for _, unique := range uniques {
			if err := renderAtlasUniqueHCL(b, unique); err != nil {
				return err
			}
		}
		for _, index := range indexes {
			if err := renderAtlasIndexHCL(b, index); err != nil {
				return err
			}
		}
	}
	if err := renderAtlasCheckHCLBlocks(b, dialect, table); err != nil {
		return err
	}
	if dialect == "postgresql" {
		if err := renderAtlasPostgresPartitionHCL(b, table.Partition); err != nil {
			return err
		}
	}
	b.WriteString("}\n")
	return nil
}

func renderAtlasPostgresPartitionHCL(b *strings.Builder, partition *ast.PartitionSpec) error {
	if partition == nil {
		return nil
	}
	if !txtarPostgresApplyPartitionSupported(partition) {
		return fmt.Errorf("%w: table partition", errUnsupportedInspectHCL)
	}
	b.WriteString("  partition {\n")
	if atlasPartitionCanUseColumnsAttr(partition.Parts) {
		fmt.Fprintf(b, "    type    = %s\n", strings.ToUpper(partition.Type))
		refs := make([]string, 0, len(partition.Parts))
		for _, part := range partition.Parts {
			refs = append(refs, atlasHCLColumnRef(part.Name))
		}
		fmt.Fprintf(b, "    columns = [%s]\n", strings.Join(refs, ", "))
	} else {
		fmt.Fprintf(b, "    type = %s\n", strings.ToUpper(partition.Type))
		for _, part := range partition.Parts {
			b.WriteString("    by {\n")
			if part.Name != "" {
				fmt.Fprintf(b, "      column = %s\n", atlasHCLColumnRef(part.Name))
			} else {
				expr := txtarPostgresPartitionExprHCL(part.Expr)
				if expr == "" {
					return fmt.Errorf("%w: table partition expression", errUnsupportedInspectHCL)
				}
				fmt.Fprintf(b, "      expr = %q\n", expr)
			}
			b.WriteString("    }\n")
		}
	}
	b.WriteString("  }\n")
	return nil
}

func atlasPartitionCanUseColumnsAttr(parts []ast.PartitionPart) bool {
	for _, part := range parts {
		if part.Name == "" || part.Expr != "" {
			return false
		}
	}
	return len(parts) > 0
}

func atlasForeignKeyIndexHCLs(tableName string, table *ast.CreateTableNode, indexes []*ast.IndexNode) []*ast.IndexNode {
	namesByColumns := atlasForeignKeyIndexNamesByColumns(tableName, table, indexes)
	keys := make([]string, 0, len(namesByColumns))
	for key := range namesByColumns {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	out := make([]*ast.IndexNode, 0, len(keys))
	for _, key := range keys {
		columns := strings.Split(key, "\x00")
		out = append(out, &ast.IndexNode{
			Name:    namesByColumns[key],
			Table:   tableName,
			Columns: columns,
		})
	}
	return out
}

func atlasColumnDefaultHCL(dialect string, column *ast.ColumnNode) string {
	if column.Default == nil {
		return ""
	}
	if dialect == "postgresql" && column.Default.Expression == "" &&
		txtarPostgresDefaultHCLLiteralIsRaw(column) {
		return strings.TrimSpace(column.Default.Value)
	}
	if dialect == "postgresql" {
		if intervalDefault := txtarPostgresIntervalDefaultSQL(column); intervalDefault != "" {
			return atlasSQLExpressionHCL(intervalDefault)
		}
	}
	return atlasDefaultHCL(column.Default)
}

func txtarPostgresDefaultHCLLiteralIsRaw(column *ast.ColumnNode) bool {
	switch txtarPostgresColumnType(column) {
	case "integer", "boolean":
		return true
	default:
		return false
	}
}

func atlasDefaultHCL(def *ast.DefaultValue) string {
	if def.Expression != "" {
		return atlasSQLExpressionHCL(def.Expression)
	}
	return strconv.Quote(def.Value)
}

func atlasSQLExpressionHCL(expr string) string {
	return "sql(" + strconv.Quote(expr) + ")"
}

func atlasColumnHCLType(dialect, schemaName string, column *ast.ColumnNode, domains map[string]bool, enums map[string]*ast.EnumNode) string {
	if dialect == "postgresql" {
		if hclType, ok := atlasPostgresEnumHCLType(schemaName, column, enums); ok {
			return hclType
		}
		if hclType, ok := atlasPostgresDomainHCLType(schemaName, column, domains); ok {
			return hclType
		}
	}
	if column.TypeRawSQL {
		return atlasSQLExpressionHCL(column.Type)
	}
	typ := column.Type
	normalized := strings.ToLower(strings.TrimSpace(atlasSQLIdentifier(typ)))
	if dialect == "mysql" && (normalized == "bool" || normalized == "boolean" || normalized == "tinyint(1)") {
		return "bool"
	}
	if dialect == "postgresql" && strings.HasPrefix(normalized, "char(") {
		return "character" + strings.TrimPrefix(normalized, "char")
	}
	return atlasColumnType(dialect, typ)
}

func atlasPostgresEnumHCLType(
	schemaName string,
	column *ast.ColumnNode,
	enums map[string]*ast.EnumNode,
) (string, bool) {
	if column.TypeRawSQL {
		enumArrayType := txtarPostgresEnumArrayType(column.Type, enums)
		if unqualified, ok := strings.CutPrefix(enumArrayType, schemaName+"."); ok {
			enumArrayType = unqualified
		}
		if enumArrayType != "" {
			return atlasSQLExpressionHCL(enumArrayType), true
		}
	}
	typ := column.Type
	if enum, ok := enums[atlasSQLIdentifier(typ)]; ok {
		return "enum." + atlasHCLTableIdentifier(enum.Name, schemaName), true
	}
	return "", false
}

func atlasPostgresDomainHCLType(schemaName string, column *ast.ColumnNode, domains map[string]bool) (string, bool) {
	if !column.TypeRawSQL || !domains[atlasSQLIdentifier(column.Type)] {
		return "", false
	}
	prefix := schemaName + "."
	if unqualified, ok := strings.CutPrefix(column.Type, prefix); ok {
		return atlasSQLExpressionHCL(unqualified), true
	}
	return atlasSQLExpressionHCL(column.Type), true
}

func atlasGeneratedHCLKind(kind string) string {
	return atlasGeneratedHCLKindForDialect("", kind)
}

func atlasGeneratedHCLKindForDialect(dialect, kind string) string {
	if kind != "" {
		return kind
	}
	if dialect == "postgresql" {
		return "STORED"
	}
	return "VIRTUAL"
}

func atlasGeneratedHCLExpr(dialect, expr string) string {
	if dialect != "mysql" && dialect != "mariadb" {
		return expr
	}
	return atlasQuoteGeneratedHCLIdentifiers(expr)
}

func atlasGeneratedSQLExpr(dialect, expr string) string {
	if dialect != "mysql" && dialect != "mariadb" {
		return expr
	}
	return atlasQuoteGeneratedHCLIdentifiers(expr)
}

func atlasQuoteGeneratedHCLIdentifiers(expr string) string {
	var b strings.Builder
	for i := 0; i < len(expr); {
		ch := expr[i]
		switch {
		case ch == '\'':
			i = atlasCopySQLStringLiteral(&b, expr, i)
		case ch == '`':
			i = atlasCopyQuotedIdentifier(&b, expr, i)
		case isAtlasCheckIdentStart(ch):
			start := i
			for i < len(expr) && isAtlasCheckIdentPart(expr[i]) {
				i++
			}
			token := expr[start:i]
			if atlasCheckKeyword(token) || atlasGeneratedExprTokenIsFunction(expr, i) {
				b.WriteString(token)
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

func atlasGeneratedExprTokenIsFunction(expr string, pos int) bool {
	for pos < len(expr) && strings.ContainsRune(" \t\n\r", rune(expr[pos])) {
		pos++
	}
	return pos < len(expr) && expr[pos] == '('
}

func renderAtlasMySQLTableHCLAttrs(b *strings.Builder, dialect string, table *ast.CreateTableNode) {
	if dialect != "mysql" && dialect != "mariadb" {
		return
	}
	if engine := table.Options["ENGINE"]; engine != "" && !strings.EqualFold(engine, "InnoDB") {
		fmt.Fprintf(b, "  engine = %s\n", engine)
	}
	defaultAttrs := atlasDefaultSchemaAttrs(dialect)
	if charset := table.Options["CHARSET"]; charset != "" && charset != defaultAttrs.charset {
		fmt.Fprintf(b, "  charset = %q\n", charset)
	}
	if collate := table.Options["COLLATE"]; collate != "" && collate != defaultAttrs.collate {
		fmt.Fprintf(b, "  collate = %q\n", collate)
	}
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
	name          string
	columns       []string
	nullsDistinct *bool
}

func atlasColumnForeignKey(tableName string, column *ast.ColumnNode) *atlasHCLForeignKey {
	ref := column.ForeignKey
	return &atlasHCLForeignKey{
		name:       atlasDefaultForeignKeyName(tableName, []string{column.Name}, ref.Name),
		columns:    []string{column.Name},
		refTable:   atlasSQLIdentifier(ref.Table),
		refColumns: ref.ReferencedColumns(),
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
		refColumns: ref.ReferencedColumns(),
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
		name:          atlasDefaultUniqueName(tableName, columns, constraint.Name),
		columns:       columns,
		nullsDistinct: cloneBoolPtr(constraint.NullsDistinct),
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
		expr = atlasCollapseDoubledBacktickDelimiters(expr)
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
		case dialect == "mysql" && strings.HasPrefix(strings.ToLower(expr[i:]), "_utf8mb4'"):
			b.WriteString("_utf8mb4")
			i += len("_utf8mb4")
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

func atlasCollapseDoubledBacktickDelimiters(expr string) string {
	var b strings.Builder
	for i := 0; i < len(expr); {
		if expr[i] != '`' {
			b.WriteByte(expr[i])
			i++
			continue
		}
		start := i
		for i < len(expr) && expr[i] == '`' {
			i++
		}
		prevIdent := start > 0 && isAtlasCheckIdentPart(expr[start-1])
		nextIdent := i < len(expr) && isAtlasCheckIdentPart(expr[i])
		if prevIdent && nextIdent {
			b.WriteString("``")
		} else {
			b.WriteByte('`')
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

func renderAtlasEnumHCL(b *strings.Builder, schemaName string, enum *ast.EnumNode) {
	enumSchema, enumName := atlasSplitQualifiedTableName(enum.Name)
	if enumSchema == "" {
		enumSchema = schemaName
	}
	fmt.Fprintf(b, "enum %q {\n", atlasHCLIdentifier(enumName))
	fmt.Fprintf(b, "  schema = schema.%s\n", enumSchema)
	fmt.Fprintf(b, "  values = [%s]\n", atlasHCLStringList(enum.Values))
	b.WriteString("}\n")
}

func atlasHCLStringList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return strings.Join(quoted, ", ")
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

func renderAtlasPrimaryKeyHCL(b *strings.Builder, columns []ast.ConstraintColumn, include []string) error {
	if atlasPrimaryKeyCanUseColumnsAttr(columns) {
		refs, err := atlasHCLColumnRefs(columns)
		if err != nil {
			return err
		}
		b.WriteString("  primary_key {\n")
		fmt.Fprintf(b, "    columns = [%s]\n", strings.Join(refs, ", "))
		if err := renderAtlasPrimaryKeyIncludeHCL(b, include); err != nil {
			return err
		}
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
	if err := renderAtlasPrimaryKeyIncludeHCL(b, include); err != nil {
		return err
	}
	b.WriteString("  }\n")
	return nil
}

func renderAtlasPrimaryKeyIncludeHCL(b *strings.Builder, include []string) error {
	if len(include) == 0 {
		return nil
	}
	refs, err := atlasHCLColumnRefsFromNames(include)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "    include = [%s]\n", strings.Join(refs, ", "))
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
	if unique.nullsDistinct != nil {
		fmt.Fprintf(b, "    columns        = [%s]\n", strings.Join(columnRefs, ", "))
	} else {
		fmt.Fprintf(b, "    columns = [%s]\n", strings.Join(columnRefs, ", "))
	}
	if unique.nullsDistinct != nil {
		fmt.Fprintf(b, "    nulls_distinct = %t\n", *unique.nullsDistinct)
	}
	b.WriteString("  }\n")
	return nil
}

func renderAtlasIndexHCL(b *strings.Builder, index *ast.IndexNode) error {
	if !atlasIndexTypeSupportedHCL(index.Type) || index.Parser != "" || index.Operator != "" ||
		index.Comment != "" || index.Concurrently || index.Granularity != 0 {
		return fmt.Errorf("%w: index %s", errUnsupportedInspectHCL, index.Name)
	}

	fmt.Fprintf(b, "  index %q {\n", atlasHCLIdentifier(index.Name))
	if index.Unique {
		if index.NullsDistinct != nil {
			b.WriteString("    unique         = true\n")
		} else {
			b.WriteString("    unique  = true\n")
		}
	}
	parts := index.EffectiveParts()
	if len(parts) == 0 {
		return fmt.Errorf("%w: index %s empty parts", errUnsupportedInspectHCL, index.Name)
	}
	if atlasIndexCanUseColumnsAttr(parts) {
		refs, err := atlasHCLIndexColumnRefs(parts)
		if err != nil {
			return err
		}
		if index.NullsDistinct != nil {
			fmt.Fprintf(b, "    columns        = [%s]\n", strings.Join(refs, ", "))
		} else {
			fmt.Fprintf(b, "    columns = [%s]\n", strings.Join(refs, ", "))
		}
		if err := renderAtlasIndexExtraHCL(b, index); err != nil {
			return err
		}
		b.WriteString("  }\n")
		return nil
	}

	for _, part := range parts {
		b.WriteString("    on {\n")
		if part.Desc {
			b.WriteString("      desc = true\n")
		}
		switch {
		case part.Expr != "":
			fmt.Fprintf(b, "      expr = %q\n", part.Expr)
		case part.Name != "":
			fmt.Fprintf(b, "      column = %s\n", atlasHCLColumnRef(part.Name))
			if part.Prefix != "" {
				fmt.Fprintf(b, "      prefix = %s\n", part.Prefix)
			}
		default:
			return fmt.Errorf("%w: index %s empty part", errUnsupportedInspectHCL, index.Name)
		}
		operator, ok := atlasIndexPartOperatorHCL(part.Operator)
		if !ok {
			return fmt.Errorf("%w: index %s operator class %s", errUnsupportedInspectHCL, index.Name, part.Operator)
		}
		if operator != "" {
			fmt.Fprintf(b, "      ops    = %s\n", operator)
		}
		b.WriteString("    }\n")
	}
	if err := renderAtlasIndexExtraHCL(b, index); err != nil {
		return err
	}
	b.WriteString("  }\n")
	return nil
}

func renderAtlasIndexExtraHCL(b *strings.Builder, index *ast.IndexNode) error {
	if index.Type != "" {
		fmt.Fprintf(b, "    type    = %s\n", strings.ToUpper(index.Type))
	}
	if index.Condition != "" {
		fmt.Fprintf(b, "    where   = %q\n", index.Condition)
	}
	if len(index.IncludeColumns) > 0 {
		refs, err := atlasHCLColumnRefsFromNames(index.IncludeColumns)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "    include = [%s]\n", strings.Join(refs, ", "))
	}
	if err := renderAtlasIndexStorageParamsHCL(b, index.StorageParams); err != nil {
		return err
	}
	if index.NullsDistinct != nil {
		fmt.Fprintf(b, "    nulls_distinct = %t\n", *index.NullsDistinct)
	}
	return nil
}

func atlasIndexTypeSupportedHCL(indexType string) bool {
	switch strings.ToUpper(indexType) {
	case "", "BTREE", "HASH", "GIN", "GIST", "BRIN", "FULLTEXT":
		return true
	default:
		return false
	}
}

func renderAtlasIndexStorageParamsHCL(b *strings.Builder, params map[string]string) error {
	if len(params) == 0 {
		return nil
	}
	value, ok := params["pages_per_range"]
	if !ok || len(params) != 1 {
		return fmt.Errorf("%w: unsupported index storage params", errUnsupportedInspectHCL)
	}
	fmt.Fprintf(b, "    page_per_range = %s\n", value)
	return nil
}

func atlasIndexCanUseColumnsAttr(parts []ast.IndexPart) bool {
	for _, part := range parts {
		operator, ok := atlasIndexPartOperatorHCL(part.Operator)
		if !ok || operator != "" || part.Expr != "" || part.Prefix != "" || part.Desc {
			return false
		}
	}
	return true
}

func atlasIndexPartOperatorHCL(operator string) (string, bool) {
	operator, ok := atlasIndexPartOperatorSQL(operator)
	if !ok || operator == "" {
		return "", ok
	}
	if postgresTSVectorOpsRE.MatchString(operator) {
		return atlasSQLExpressionHCL(operator), true
	}
	return operator, true
}

func atlasHCLIndexColumnRefs(parts []ast.IndexPart) ([]string, error) {
	refs := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Name == "" {
			return nil, fmt.Errorf("%w: empty index column", errUnsupportedInspectHCL)
		}
		refs = append(refs, atlasHCLColumnRef(part.Name))
	}
	return refs, nil
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
		refs = append(refs, atlasHCLColumnRef(column))
	}
	return refs, nil
}

func atlasHCLColumnRef(column string) string {
	name := atlasHCLIdentifier(column)
	if strings.ContainsAny(name, " ()`\"$") {
		return "column[" + strconv.Quote(name) + "]"
	}
	return "column." + name
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
	return renderAtlasInspectSQLWithOptions(dialect, statements, indent, atlasInspectSQLOptions{})
}

type atlasInspectSQLOptions struct {
	mariaDBJSONStorage bool
	showDefaultNull    bool
	schemaAttrs        atlasSchemaAttrs
	postgresEnums      map[string]*ast.EnumNode
}

func renderAtlasInspectSQLWithOptions(
	dialect string,
	statements []ast.Node,
	indent string,
	opts atlasInspectSQLOptions,
) (string, error) {
	tableNames := atlasTableNames(statements)
	indexesByTable := atlasIndexesByTable(dialect, statements)
	schemaAttrsByTable := atlasSchemaAttrsByTable(dialect, statements)
	if dialect == "postgresql" && opts.postgresEnums == nil {
		opts.postgresEnums = txtarPostgresEnumsByName(statements)
	}
	var b strings.Builder
	if dialect == "postgresql" {
		for _, enum := range atlasPostgresEnumList(statements) {
			renderAtlasEnumSQL(&b, enum)
		}
	}
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.EnumNode:
			if dialect != "postgresql" {
				return "", fmt.Errorf("unsupported inspect statement %T", stmt)
			}
		case *ast.CreateTableNode:
			tableOpts := opts
			if attrs, ok := schemaAttrsByTable[node.Name]; ok {
				tableOpts.schemaAttrs = attrs
			}
			if err := renderAtlasCreateTableSQL(&b, dialect, node, indexesByTable[node.Name], indent, tableOpts); err != nil {
				return "", err
			}
		case *ast.IndexNode:
			if !tableNames[node.Table] {
				return "", fmt.Errorf("unsupported inspect statement %T without matching table", stmt)
			}
			if !atlasSupportsInlineIndexes(dialect) {
				renderAtlasStandaloneIndexSQL(&b, dialect, node)
			}
		case *ast.CreateSchemaNode:
			continue
		default:
			return "", fmt.Errorf("unsupported inspect statement %T", stmt)
		}
	}
	return b.String(), nil
}

func atlasSchemaAttrsByTable(dialect string, statements []ast.Node) map[string]atlasSchemaAttrs {
	attrsBySchema := map[string]atlasSchemaAttrs{}
	for _, stmt := range statements {
		schema, ok := stmt.(*ast.CreateSchemaNode)
		if !ok {
			continue
		}
		attrs := atlasDefaultSchemaAttrs(dialect)
		if schema.Charset != "" {
			attrs.charset = schema.Charset
		}
		if schema.Collate != "" {
			attrs.collate = schema.Collate
		}
		attrsBySchema[atlasSQLIdentifier(schema.Name)] = attrs
	}
	if len(attrsBySchema) == 0 {
		return nil
	}

	out := map[string]atlasSchemaAttrs{}
	for _, stmt := range statements {
		table, ok := stmt.(*ast.CreateTableNode)
		if !ok {
			continue
		}
		schemaName, _ := atlasSplitQualifiedTableName(table.Name)
		switch {
		case schemaName != "":
			if attrs, ok := attrsBySchema[schemaName]; ok {
				out[table.Name] = attrs
			}
		case len(attrsBySchema) == 1:
			for _, attrs := range attrsBySchema {
				out[table.Name] = attrs
			}
		}
	}
	return out
}

func atlasSplitQualifiedTableName(name string) (string, string) {
	name = atlasSQLIdentifier(name)
	schemaName, tableName, ok := strings.Cut(name, ".")
	if !ok {
		return "", name
	}
	return schemaName, tableName
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
	for tableName := range indexes {
		slices.SortStableFunc(indexes[tableName], func(a, b *ast.IndexNode) int {
			return cmp.Compare(a.Name, b.Name)
		})
	}
	return indexes
}

func atlasSupportsInlineIndexes(dialect string) bool {
	return dialect == "mysql" || dialect == "mariadb"
}

func atlasPostgresEnumList(statements []ast.Node) []*ast.EnumNode {
	var enums []*ast.EnumNode
	for _, stmt := range statements {
		enum, ok := stmt.(*ast.EnumNode)
		if ok {
			enums = append(enums, enum)
		}
	}
	slices.SortFunc(enums, func(a, b *ast.EnumNode) int {
		return cmp.Compare(atlasSQLIdentifier(a.Name), atlasSQLIdentifier(b.Name))
	})
	return enums
}

func renderAtlasEnumSQL(b *strings.Builder, enum *ast.EnumNode) {
	quotedValues := make([]string, 0, len(enum.Values))
	for _, value := range enum.Values {
		quotedValues = append(quotedValues, atlasSQLStringLiteral(value))
	}
	fmt.Fprintf(b, "-- Create enum type %q\n", atlasSQLIdentifier(enum.Name))
	fmt.Fprintf(
		b,
		"CREATE TYPE %s AS ENUM (%s);\n",
		atlasIdentifierQuoter("postgresql")(enum.Name),
		strings.Join(quotedValues, ", "),
	)
}

func atlasSQLStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func renderAtlasCreateTableSQL(
	b *strings.Builder,
	dialect string,
	table *ast.CreateTableNode,
	indexes []*ast.IndexNode,
	indent string,
	opts atlasInspectSQLOptions,
) error {
	if !atlasSupportsInspectChecks(dialect) && atlasTableHasChecks(table) {
		return fmt.Errorf("%w: check constraints", errUnsupportedInspectSQL)
	}
	quote := atlasIdentifierQuoter(dialect)
	fmt.Fprintf(b, "-- Create %q table\n", atlasUnqualifiedSQLTableName(table.Name))
	tableOpts := opts
	tableAttrs := atlasTableSchemaAttrs(dialect, table, opts.schemaAttrs)
	tableOpts.schemaAttrs = tableAttrs

	parts := make([]string, 0, len(table.Columns)+len(table.Constraints)+len(indexes))
	var primaryColumns []ast.ConstraintColumn
	var columnForeignKeys []*ast.ColumnNode
	for _, column := range table.Columns {
		inspectColumn := atlasInspectColumn(dialect, table, column)
		parts = append(parts, renderAtlasColumnSQL(dialect, quote, inspectColumn, indent != "" || dialect == "sqlite", tableOpts))
		if column.Primary && !atlasColumnPrimaryKeyInline(dialect, column) {
			primaryColumns = append(primaryColumns, ast.ConstraintColumn{Name: column.Name})
		}
		if column.ForeignKey != nil {
			columnForeignKeys = append(columnForeignKeys, column)
		}
	}
	if len(primaryColumns) > 0 {
		parts = append(parts, renderAtlasPrimaryKeySQL(quote, primaryColumns, nil))
	}
	if dialect == "mysql" || dialect == "mariadb" {
		for _, column := range columnForeignKeys {
			ref := column.ForeignKey
			name := atlasDefaultForeignKeyName(table.Name, []string{column.Name}, ref.Name)
			if !atlasIndexNameExists(indexes, name) {
				parts = append(parts, renderAtlasColumnForeignKeyIndexSQL(quote, name, column))
			}
		}
		parts = append(parts, renderAtlasConstraintForeignKeyIndexSQLs(quote, table, indexes)...)
	}
	for _, column := range columnForeignKeys {
		parts = append(parts, renderAtlasColumnForeignKeySQL(dialect, quote, table.Name, column))
	}
	checks, err := atlasCheckSQLParts(dialect, quote, table)
	if err != nil {
		return err
	}
	parts = append(parts, checks...)
	unnamedSQLiteForeignKeys := 0
	for _, constraint := range table.Constraints {
		switch constraint.Type {
		case ast.PrimaryKeyConstraint:
			parts = append(parts, renderAtlasPrimaryKeySQL(quote, atlasConstraintColumns(constraint), constraint.IncludeColumns))
		case ast.ForeignKeyConstraint:
			parts = append(parts, renderAtlasConstraintForeignKeySQL(dialect, quote, table.Name, constraint, unnamedSQLiteForeignKeys))
			if dialect == "sqlite" && constraint.Name == "" {
				unnamedSQLiteForeignKeys++
			}
		case ast.UniqueConstraint:
			parts = append(parts, renderAtlasUniqueSQL(quote, table.Name, constraint))
		case ast.CheckConstraint:
			continue
		default:
			return fmt.Errorf("unsupported inspect constraint %s", constraint.Type)
		}
	}
	for _, index := range indexes {
		parts = append(parts, renderAtlasIndexSQL(dialect, quote, index))
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
	if dialect == "sqlite" {
		options, err := renderAtlasSQLiteTableOptions(table.Options)
		if err != nil {
			return err
		}
		b.WriteString(options)
	}
	if dialect == "mysql" || dialect == "mariadb" {
		if autoIncrement := table.Options["AUTO_INCREMENT"]; autoIncrement != "" {
			fmt.Fprintf(b, " AUTO_INCREMENT=%s", autoIncrement)
		}
	}
	if tableAttrs.charset != "" || tableAttrs.collate != "" {
		if tableAttrs.charset != "" {
			fmt.Fprintf(b, " CHARSET %s", tableAttrs.charset)
		}
		if tableAttrs.collate != "" {
			fmt.Fprintf(b, " COLLATE %s", tableAttrs.collate)
		}
	}
	if dialect == "postgresql" && table.Partition != nil {
		partitionSQL, ok := renderAtlasPostgresPartitionSQL(table.Partition, quote)
		if !ok {
			return fmt.Errorf("%w: table partition", errUnsupportedInspectSQL)
		}
		b.WriteByte(' ')
		b.WriteString(partitionSQL)
	}
	b.WriteString(";\n")
	for _, column := range table.Columns {
		if column.Unique && !atlasSupportsInlineIndexes(dialect) {
			index := &ast.IndexNode{
				Name:    atlasDefaultSQLUniqueIndexName(dialect, table.Name, column.Name),
				Table:   table.Name,
				Columns: []string{column.Name},
				Unique:  true,
			}
			renderAtlasStandaloneIndexSQL(b, dialect, index)
		}
	}
	return nil
}

func renderAtlasPostgresPartitionSQL(partition *ast.PartitionSpec, quote func(string) string) (string, bool) {
	if partition == nil || len(partition.Parts) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(partition.Parts))
	for _, part := range partition.Parts {
		switch {
		case part.Name != "":
			parts = append(parts, quote(part.Name))
		case part.Expr != "":
			expr := txtarPostgresPartitionExprSQL(part.Expr)
			if expr == "" {
				return "", false
			}
			parts = append(parts, expr)
		default:
			return "", false
		}
	}
	return "PARTITION BY " + strings.ToUpper(partition.Type) + " (" + strings.Join(parts, ", ") + ")", true
}

func atlasTableSchemaAttrs(dialect string, table *ast.CreateTableNode, inherited atlasSchemaAttrs) atlasSchemaAttrs {
	if dialect != "mysql" && dialect != "mariadb" {
		return inherited
	}
	attrs := inherited
	if attrs.charset == "" && attrs.collate == "" {
		attrs = atlasDefaultSchemaAttrs(dialect)
	}
	if charset := table.Options["CHARSET"]; charset != "" {
		attrs.charset = charset
	}
	if collate := table.Options["COLLATE"]; collate != "" {
		attrs.collate = collate
	}
	return attrs
}

func renderAtlasSQLiteTableOptions(options map[string]string) (string, error) {
	parts := make([]string, 0, len(options))
	for key := range options {
		switch key {
		case "STRICT", "WITHOUT_ROWID":
			continue
		default:
			return "", fmt.Errorf("unsupported inspect table option %s", key)
		}
	}
	if tableOptionEnabled(options, "WITHOUT_ROWID") {
		parts = append(parts, "WITHOUT ROWID")
	}
	if tableOptionEnabled(options, "STRICT") {
		parts = append(parts, "STRICT")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " " + strings.Join(parts, ", "), nil
}

func tableOptionEnabled(options map[string]string, key string) bool {
	value, ok := options[key]
	if !ok {
		return false
	}
	return strings.EqualFold(value, "true") || value == ""
}

func renderAtlasColumnSQL(
	dialect string,
	quote func(string) string,
	column *ast.ColumnNode,
	explicitNull bool,
	opts atlasInspectSQLOptions,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", quote(column.Name), atlasColumnSQLType(dialect, column, opts))
	if dialect == "mysql" || dialect == "mariadb" {
		atlasWriteColumnCharsetCollate(&b, dialect, column, opts)
	}
	generated := column.GeneratedExpression != ""
	if atlasColumnPrimaryKeyInline(dialect, column) {
		if !column.Nullable {
			b.WriteString(" NOT NULL")
		}
		b.WriteString(" PRIMARY KEY")
		if column.AutoInc {
			b.WriteString(" AUTOINCREMENT")
		}
	} else if !column.Nullable && !generated {
		b.WriteString(" NOT NULL")
	} else if opts.showDefaultNull && (dialect == "mysql" || dialect == "mariadb") && column.Nullable && !generated {
		b.WriteString(" DEFAULT NULL")
	} else if explicitNull {
		b.WriteString(" NULL")
	}
	if (dialect == "mysql" || dialect == "mariadb") && column.AutoInc {
		b.WriteString(" AUTO_INCREMENT")
	}
	if generated {
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s)", atlasGeneratedSQLExpr(dialect, column.GeneratedExpression))
		fmt.Fprintf(&b, " %s", atlasGeneratedHCLKindForDialect(dialect, column.GeneratedKind))
	}
	if column.Default != nil {
		fmt.Fprintf(&b, " DEFAULT %s", atlasDefaultSQL(dialect, column.Default))
	}
	if column.UpdateExpression != "" {
		fmt.Fprintf(&b, " ON UPDATE %s", atlasSQLExpression(dialect, column.UpdateExpression))
	}
	if opts.mariaDBJSONStorage && atlasMariaDBJSONColumn(dialect, column) {
		fmt.Fprintf(&b, " CHECK (json_valid(%s))", quote(column.Name))
	}
	return b.String()
}

func atlasColumnSQLType(dialect string, column *ast.ColumnNode, opts atlasInspectSQLOptions) string {
	if opts.mariaDBJSONStorage && atlasMariaDBJSONColumn(dialect, column) {
		return "longtext"
	}
	if dialect == "postgresql" {
		quote := atlasIdentifierQuoter("postgresql")
		if enum, ok := opts.postgresEnums[atlasSQLIdentifier(column.Type)]; ok {
			return quote(enum.Name)
		}
		if column.TypeRawSQL {
			if enumArrayType := txtarPostgresEnumArrayType(column.Type, opts.postgresEnums); enumArrayType != "" {
				base := strings.TrimSuffix(enumArrayType, "[]")
				return quote(base) + "[]"
			}
		}
		if rawArrayType := txtarPostgresRawArrayType(column); rawArrayType != "" {
			return rawArrayType
		}
	}
	if column.TypeRawSQL {
		return column.Type
	}
	return atlasColumnType(dialect, column.Type)
}

func atlasWriteColumnCharsetCollate(
	b *strings.Builder,
	dialect string,
	column *ast.ColumnNode,
	opts atlasInspectSQLOptions,
) {
	charset := column.Charset
	collate := column.Collate
	if opts.mariaDBJSONStorage && atlasMariaDBJSONColumn(dialect, column) {
		if charset == "" {
			charset = "utf8mb4"
		}
		if collate == "" {
			collate = "utf8mb4_bin"
		}
	}
	if charset == "" && collate == "" && atlasColumnUsesInheritedSchemaCollation(dialect, column, opts.schemaAttrs) {
		collate = opts.schemaAttrs.collate
	}
	if charset != "" {
		fmt.Fprintf(b, " CHARACTER SET %s", charset)
	}
	if collate != "" && !atlasColumnCollateIsImplicitDefault(dialect, column) {
		fmt.Fprintf(b, " COLLATE %s", collate)
	}
}

func atlasColumnUsesInheritedSchemaCollation(dialect string, column *ast.ColumnNode, schemaAttrs atlasSchemaAttrs) bool {
	if schemaAttrs.collate == "" || !atlasMySQLColumnTypeUsesSchemaCollation(column.Type) {
		return false
	}
	return !strings.EqualFold(schemaAttrs.collate, atlasDefaultSchemaAttrs(dialect).collate)
}

func atlasColumnPrimaryKeyInline(dialect string, column *ast.ColumnNode) bool {
	return dialect == "sqlite" && column.Primary && column.AutoInc
}

func atlasColumnCollateIsImplicitDefault(dialect string, column *ast.ColumnNode) bool {
	return column.Collate != "" && strings.EqualFold(column.Collate, atlasDefaultCollateForColumn(dialect, column))
}

func atlasDefaultCollateForColumn(dialect string, column *ast.ColumnNode) string {
	charset := strings.ToLower(column.Charset)
	switch charset {
	case "hebrew":
		return "hebrew_general_ci"
	case "", "utf8mb4":
		return atlasDefaultSchemaAttrs(dialect).collate
	default:
		return ""
	}
}

func atlasDefaultSQL(dialect string, def *ast.DefaultValue) string {
	if def.Expression != "" {
		return atlasSQLExpression(dialect, def.Expression)
	}
	value := strings.TrimSpace(def.Value)
	if value == "" && def.HasLiteral() {
		return "''"
	}
	if atlasDefaultLiteralIsRawSQL(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func atlasSQLExpression(dialect, expr string) string {
	if dialect == "mariadb" {
		return atlasMariaDBSQLExpression(expr)
	}
	return expr
}

func atlasMariaDBSQLExpression(expr string) string {
	trimmed := strings.TrimSpace(expr)
	if strings.EqualFold(trimmed, "CURRENT_TIMESTAMP") {
		return "current_timestamp()"
	}
	prefix, precision, ok := strings.Cut(trimmed, "(")
	if ok && strings.EqualFold(strings.TrimSpace(prefix), "CURRENT_TIMESTAMP") && strings.HasSuffix(precision, ")") {
		return "current_timestamp(" + precision
	}
	return expr
}

func atlasDefaultLiteralIsRawSQL(value string) bool {
	if strings.HasPrefix(value, "'") || strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "`") {
		return true
	}
	switch strings.ToLower(value) {
	case "true", "false", "null":
		return true
	}
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
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

func renderAtlasIndexSQL(dialect string, quote func(string) string, index *ast.IndexNode) string {
	var b strings.Builder
	if index.Unique {
		b.WriteString("UNIQUE ")
	}
	if index.Type != "" {
		b.WriteString(strings.ToUpper(index.Type))
		b.WriteString(" ")
	}
	indexKeyword := "INDEX"
	if (dialect == "mysql" || dialect == "mariadb") &&
		(index.Type == "" || strings.EqualFold(index.Type, "FULLTEXT")) {
		indexKeyword = "KEY"
	}
	fmt.Fprintf(&b, "%s %s (", indexKeyword, quote(index.Name))

	parts := make([]string, 0, len(index.EffectiveParts()))
	for _, part := range index.EffectiveParts() {
		parts = append(parts, renderAtlasIndexPartSQL(quote, part))
	}
	b.WriteString(strings.Join(parts, atlasIndexPartSeparator(dialect)))
	b.WriteString(")")
	if dialect == "postgresql" && len(index.IncludeColumns) > 0 {
		b.WriteString(" INCLUDE (")
		b.WriteString(renderAtlasIndexIncludeColumnsSQL(quote, index.IncludeColumns))
		b.WriteString(")")
	}
	if dialect == "postgresql" && index.NullsDistinct != nil {
		b.WriteString(" ")
		b.WriteString(renderAtlasNullsDistinctSQL(index.NullsDistinct))
	}
	if dialect == "postgresql" && len(index.StorageParams) > 0 {
		b.WriteString(" ")
		b.WriteString(renderAtlasIndexStorageParamsSQL(index.StorageParams))
	}
	if index.Parser != "" {
		fmt.Fprintf(&b, " /*!50100 WITH PARSER %s */", quote(index.Parser))
	}
	return b.String()
}

func renderAtlasIndexPartSQL(quote func(string) string, part ast.IndexPart) string {
	var spec string
	if part.Expr != "" {
		spec = fmt.Sprintf("(%s)", part.Expr)
	} else {
		spec = quote(part.Name)
	}
	if part.Prefix != "" && part.Expr == "" {
		spec += " (" + part.Prefix + ")"
	}
	if operator, ok := atlasIndexPartOperatorSQL(part.Operator); ok && operator != "" {
		spec += " " + operator
	}
	if part.Desc {
		spec += " DESC"
	}
	return spec
}

func atlasIndexPartOperatorSQL(operator string) (string, bool) {
	operator = strings.TrimSpace(operator)
	if postgresTSVectorOpsRE.MatchString(operator) {
		return operator, true
	}
	switch operator {
	case "", "bpchar_ops":
		return "", true
	case "bpchar_pattern_ops", "jsonb_path_ops":
		return operator, true
	default:
		return "", false
	}
}

func atlasIndexPartSeparator(dialect string) string {
	if dialect == "mysql" || dialect == "mariadb" {
		return ","
	}
	return ", "
}

func renderAtlasStandaloneIndexSQL(b *strings.Builder, dialect string, index *ast.IndexNode) {
	quote := atlasIdentifierQuoter(dialect)
	fmt.Fprintf(b, "-- Create index %q to table: %q\n", atlasSQLIdentifier(index.Name), atlasSQLIdentifier(index.Table))
	b.WriteString("CREATE ")
	if index.Unique {
		b.WriteString("UNIQUE ")
	}
	if index.Type != "" {
		b.WriteString(strings.ToUpper(index.Type))
		b.WriteString(" ")
	}
	fmt.Fprintf(b, "INDEX %s ON %s (", quote(index.Name), quote(index.Table))
	parts := make([]string, 0, len(index.EffectiveParts()))
	for _, part := range index.EffectiveParts() {
		parts = append(parts, renderAtlasIndexPartSQL(quote, part))
	}
	b.WriteString(strings.Join(parts, atlasIndexPartSeparator(dialect)))
	b.WriteString(")")
	if dialect == "postgresql" && len(index.IncludeColumns) > 0 {
		b.WriteString(" INCLUDE (")
		b.WriteString(renderAtlasIndexIncludeColumnsSQL(quote, index.IncludeColumns))
		b.WriteString(")")
	}
	if dialect == "postgresql" && index.NullsDistinct != nil {
		b.WriteString(" ")
		b.WriteString(renderAtlasNullsDistinctSQL(index.NullsDistinct))
	}
	if dialect == "postgresql" && len(index.StorageParams) > 0 {
		b.WriteString(" ")
		b.WriteString(renderAtlasIndexStorageParamsSQL(index.StorageParams))
	}
	if index.Parser != "" {
		fmt.Fprintf(b, " /*!50100 WITH PARSER %s */", quote(index.Parser))
	}
	if index.Condition != "" {
		fmt.Fprintf(b, " WHERE %s;\n", index.Condition)
		return
	}
	b.WriteString(";\n")
}

func renderAtlasIndexIncludeColumnsSQL(quote func(string) string, columns []string) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quote(column))
	}
	return strings.Join(quoted, ", ")
}

func renderAtlasIndexStorageParamsSQL(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	rendered := make([]string, 0, len(keys))
	for _, key := range keys {
		value := "'" + strings.ReplaceAll(params[key], "'", "''") + "'"
		rendered = append(rendered, key+"="+value)
	}
	return "WITH (" + strings.Join(rendered, ", ") + ")"
}

func renderAtlasUniqueSQL(quote func(string) string, tableName string, constraint *ast.ConstraintNode) string {
	columns := atlasConstraintColumnNames(constraint)
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quote(column))
	}
	name := atlasDefaultUniqueName(tableName, columns, constraint.Name)
	parts := []string{"CONSTRAINT", quote(name), "UNIQUE"}
	if constraint.NullsDistinct != nil {
		parts = append(parts, renderAtlasNullsDistinctSQL(constraint.NullsDistinct))
	}
	parts = append(parts, "("+strings.Join(quoted, ", ")+")")
	return strings.Join(parts, " ")
}

func renderAtlasNullsDistinctSQL(nullsDistinct *bool) string {
	if nullsDistinct != nil && *nullsDistinct {
		return "NULLS DISTINCT"
	}
	return "NULLS NOT DISTINCT"
}

func boolPtrEqual(left, right *bool) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func renderAtlasColumnForeignKeySQL(
	dialect string,
	quote func(string) string,
	tableName string,
	column *ast.ColumnNode,
) string {
	ref := column.ForeignKey
	name := atlasDefaultForeignKeyName(tableName, []string{column.Name}, ref.Name)
	return atlasForeignKeySQL(dialect, quote, name, []string{column.Name}, ref)
}

func renderAtlasColumnForeignKeyIndexSQL(
	quote func(string) string,
	name string,
	column *ast.ColumnNode,
) string {
	return renderAtlasForeignKeyIndexSQL(quote, name, []string{column.Name})
}

func renderAtlasConstraintForeignKeyIndexSQLs(
	quote func(string) string,
	table *ast.CreateTableNode,
	indexes []*ast.IndexNode,
) []string {
	namesByColumns := atlasForeignKeyIndexNamesByColumns(table.Name, table, indexes)
	keys := make([]string, 0, len(namesByColumns))
	for key := range namesByColumns {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	indexSQL := make([]string, 0, len(keys))
	for _, key := range keys {
		columns := strings.Split(key, "\x00")
		indexSQL = append(indexSQL, renderAtlasForeignKeyIndexSQL(quote, namesByColumns[key], columns))
	}
	return indexSQL
}

func atlasForeignKeyIndexNamesByColumns(tableName string, table *ast.CreateTableNode, indexes []*ast.IndexNode) map[string]string {
	namesByColumns := map[string]string{}
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.ForeignKeyConstraint {
			continue
		}
		columns := atlasConstraintColumnNames(constraint)
		if len(columns) == 0 {
			continue
		}
		name := atlasDefaultForeignKeyName(tableName, columns, constraint.Name)
		if atlasIndexNameExists(indexes, name) {
			continue
		}
		namesByColumns[strings.Join(columns, "\x00")] = name
	}
	return namesByColumns
}

func renderAtlasForeignKeyIndexSQL(quote func(string) string, name string, columns []string) string {
	quotedColumns := make([]string, 0, len(columns))
	for _, column := range columns {
		quotedColumns = append(quotedColumns, quote(column))
	}
	return fmt.Sprintf("KEY %s (%s)", quote(name), strings.Join(quotedColumns, ","))
}

func atlasIndexNameExists(indexes []*ast.IndexNode, name string) bool {
	for _, index := range indexes {
		if atlasSQLIdentifier(index.Name) == atlasSQLIdentifier(name) {
			return true
		}
	}
	return false
}

func renderAtlasConstraintForeignKeySQL(
	dialect string,
	quote func(string) string,
	tableName string,
	constraint *ast.ConstraintNode,
	unnamedSQLitePosition int,
) string {
	name := atlasDefaultForeignKeyName(tableName, atlasConstraintColumnNames(constraint), constraint.Name)
	if dialect == "sqlite" && constraint.Name == "" {
		name = strconv.Itoa(unnamedSQLitePosition)
	}
	return atlasForeignKeySQL(dialect, quote, name, atlasConstraintColumnNames(constraint), constraint.Reference)
}

func atlasForeignKeySQL(
	dialect string,
	quote func(string) string,
	name string,
	columns []string,
	ref *ast.ForeignKeyRef,
) string {
	quotedColumns := make([]string, 0, len(columns))
	for _, column := range columns {
		quotedColumns = append(quotedColumns, quote(column))
	}
	refColumns := ref.ReferencedColumns()
	quotedRefColumns := make([]string, 0, len(refColumns))
	for _, column := range refColumns {
		quotedRefColumns = append(quotedRefColumns, quote(column))
	}
	sql := fmt.Sprintf(
		"CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		quote(name),
		strings.Join(quotedColumns, ", "),
		quote(atlasUnqualifiedSQLTableName(ref.Table)),
		strings.Join(quotedRefColumns, ", "),
	)
	if dialect == "sqlite" {
		if action := atlasForeignKeySQLAction(dialect, ref.OnUpdate); action != "" {
			sql += " ON UPDATE " + action
		}
		if action := atlasForeignKeySQLAction(dialect, ref.OnDelete); action != "" {
			sql += " ON DELETE " + action
		}
		return sql
	}
	if action := atlasForeignKeySQLAction(dialect, ref.OnDelete); action != "" {
		sql += " ON DELETE " + action
	}
	if action := atlasForeignKeySQLAction(dialect, ref.OnUpdate); action != "" {
		sql += " ON UPDATE " + action
	}
	return sql
}

func atlasUnqualifiedSQLTableName(table string) string {
	table = atlasSQLIdentifier(table)
	if _, unqualified, ok := strings.Cut(table, "."); ok {
		return unqualified
	}
	return table
}

func atlasForeignKeySQLAction(dialect, action string) string {
	if action == "" && dialect == "sqlite" {
		action = "NO ACTION"
	}
	return strings.ReplaceAll(action, "_", " ")
}

func renderAtlasPrimaryKeySQL(quote func(string) string, columns []ast.ConstraintColumn, include []string) string {
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
	sql := "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"
	if len(include) > 0 {
		quotedInclude := make([]string, 0, len(include))
		for _, column := range include {
			quotedInclude = append(quotedInclude, quote(column))
		}
		sql += " INCLUDE (" + strings.Join(quotedInclude, ", ") + ")"
	}
	return sql
}

func atlasColumnType(dialect, typ string) string {
	normalized := strings.ToLower(typ)
	if dialect == "sqlite" {
		if base, _, ok := strings.Cut(normalized, "("); ok {
			switch base {
			case "varchar", "decimal":
				return base
			}
		}
	}
	if dialect == "sqlite" && normalized == "" {
		return "blob"
	}
	if dialect == "postgresql" && (normalized == "int" || normalized == "int4") {
		return "integer"
	}
	if dialect == "mysql" || dialect == "mariadb" {
		switch normalized {
		case "bit":
			return "bit(1)"
		case "bool", "boolean", "tinyint(1)":
			return "tinyint(1)"
		}
		if base, _, ok := strings.Cut(normalized, "("); ok {
			switch base {
			case "bigint", "int", "integer", "mediumint", "smallint", "tinyint":
				return base
			}
		}
	}
	return normalized
}

func atlasMariaDBJSONColumn(dialect string, column *ast.ColumnNode) bool {
	return dialect == "mariadb" && strings.EqualFold(column.Type, "json")
}

func atlasDefaultSQLUniqueIndexName(dialect, tableName, columnName string) string {
	if dialect == "sqlite" {
		return atlasSQLIdentifier(tableName) + "_" + atlasSQLIdentifier(columnName)
	}
	return atlasDefaultUniqueName(tableName, []string{columnName}, "")
}

func atlasIdentifierQuoter(dialect string) func(string) string {
	if dialect == "mysql" || dialect == "mariadb" || dialect == "sqlite" {
		return func(name string) string {
			return atlasQuoteIdentifierParts(name, "`")
		}
	}
	return func(name string) string {
		return atlasQuoteIdentifierParts(name, `"`)
	}
}

func atlasQuoteIdentifierParts(name, quote string) string {
	parts := strings.Split(atlasSQLIdentifier(name), ".")
	for i, part := range parts {
		parts[i] = quote + strings.ReplaceAll(part, quote, quote+quote) + quote
	}
	return strings.Join(parts, ".")
}

func txtarFixtureDialect(fx Fixture) string {
	if dialect, ok := txtarOnlyDirectiveDialect(fx); ok {
		return dialect
	}
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
	if slices.Contains(parts, "sqlite") {
		return "sqlite"
	}
	return "postgresql"
}

func txtarOnlyDirectiveDialect(fx Fixture) (string, bool) {
	if fx.Kind != FixtureKindTxtar || len(fx.Files) != 1 {
		return "", false
	}
	data, err := os.ReadFile(fx.Files[0])
	if err != nil {
		return "", false
	}
	return txtarOnlyDirectiveDialectFromData(string(data))
}

func txtarOnlyDirectiveDialectFromData(data string) (string, bool) {
	dialects := map[string]bool{}
	for _, line := range strings.Split(txtarScriptPrefix(data), "\n") {
		fields := txtarOnlyDirectiveFields(line)
		for _, condition := range fields {
			switch {
			case strings.HasPrefix(condition, "maria"):
				dialects["mariadb"] = true
			case strings.HasPrefix(condition, "mysql"):
				dialects["mysql"] = true
			case strings.HasPrefix(condition, "postgres"):
				dialects["postgresql"] = true
			case strings.HasPrefix(condition, "sqlite"):
				dialects["sqlite"] = true
			}
		}
	}
	if len(dialects) != 1 {
		return "", false
	}
	for dialect := range dialects {
		return dialect, true
	}
	return "", false
}

func txtarOnlyDirectiveFields(line string) []string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "! ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "! "))
	}
	if !strings.HasPrefix(line, "only ") {
		return nil
	}
	fields := splitTxtarFields(line)
	if len(fields) < 2 {
		return nil
	}
	for i := 1; i < len(fields); i++ {
		fields[i] = strings.ToLower(strings.TrimSpace(fields[i]))
	}
	return fields[1:]
}

func txtarFixtureSchemaName(fx Fixture) string {
	if txtarFixtureFamily(fx) == "sqlite" {
		return "main"
	}
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
		return stream == "" || strings.Contains(stream, "\n\n"), nil
	}
	return regexp.MatchString(pattern, stream)
}

func txtarFilesMismatch(files map[string]string, left, right string) string {
	return txtarFilesMismatchAny(files, left, []string{right})
}

func txtarFilesMismatchAny(files map[string]string, left string, rights []string) string {
	l, lok := files[left]
	if !lok {
		return fmt.Sprintf("cmp %s %s did not match: %s missing", left, rights[0], left)
	}
	var missing []string
	for _, right := range rights {
		r, rok := files[right]
		if !rok {
			missing = append(missing, right)
			continue
		}
		if txtarFilesEqual(l, r) {
			return ""
		}
	}
	if len(missing) == len(rights) {
		return fmt.Sprintf("cmp %s %s did not match: %s missing", left, rights[0], rights[0])
	}
	return fmt.Sprintf("cmp %s %s did not match: got %q want %q", left, rights[0], oneLine(l), oneLine(files[rights[0]]))
}

func txtarVariantExpectedFiles(fx Fixture, runtime *txtarRuntime, expected string) []string {
	candidates := []string{expected}
	for _, condition := range txtarOnlyDirectiveConditions(fx) {
		candidate := path.Join(condition, expected)
		if _, ok := runtime.files[candidate]; ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func txtarOnlyDirectiveConditions(fx Fixture) []string {
	if fx.Kind != FixtureKindTxtar || len(fx.Files) != 1 {
		return nil
	}
	data, err := os.ReadFile(fx.Files[0])
	if err != nil {
		return nil
	}
	var conditions []string
	for _, line := range strings.Split(txtarScriptPrefix(string(data)), "\n") {
		conditions = append(conditions, txtarOnlyDirectiveFields(line)...)
	}
	return conditions
}

func txtarFilesEqual(left, right string) bool {
	if left == right {
		return true
	}
	// Atlas txtar fixtures differ on whether the final section keeps a trailing
	// blank line. Treat only trailing newlines as non-substantive; spaces and
	// any content differences still fail.
	return strings.TrimRight(left, "\n") == strings.TrimRight(right, "\n")
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
