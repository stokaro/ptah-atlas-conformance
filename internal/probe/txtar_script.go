package probe

import (
	"cmp"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/stokaro/ptah/core/ast"
	"github.com/stokaro/ptah/core/parser"
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
		}

		expectedFailure, commandLine := txtarExpectedFailure(trimmed)
		if commandLine == "" {
			continue
		}
		if txtarCommandReadsUnsupportedFile(commandLine, unsupportedFiles) {
			last = txtarCommandResult{unsupported: "blocked by unsupported file"}
			markUnsupportedFileCommandOutputs(commandLine, runtime, unsupportedFiles)
			continue
		}
		result := runTxtarCommand(fx, runtime, commandLine)
		last = result
		if result.unsupported != "" {
			summary.unsupported = append(summary.unsupported, result.unsupported)
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

func runTxtarCommand(fx Fixture, runtime *txtarRuntime, line string) txtarCommandResult {
	fields := txtarCommandFields(line)
	if result, ok := runTxtarFileCommand(runtime, fields); ok {
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
	}
	return false
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
		}
	}
	const filePrefix = "file://"
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
	if format == "" {
		return txtarCommandResult{unsupported: "atlas schema inspect hcl"}
	}
	if format != "{{ sql . }}" {
		return txtarCommandResult{unsupported: "atlas schema inspect format"}
	}
	name := strings.TrimPrefix(sourceURL, filePrefix)
	sql, ok := files[name]
	if !ok {
		return txtarCommandResult{err: fmt.Errorf("file %q not found in txtar archive", name)}
	}
	output, err := renderTxtarSQL(fx, sql)
	if err != nil {
		return txtarCommandResult{err: err}
	}
	if redirect != "" {
		files[redirect] = output
		return txtarCommandResult{}
	}
	return txtarCommandResult{stdout: output}
}

func renderTxtarSQL(fx Fixture, sql string) (string, error) {
	list, err := parser.NewParser(sql).Parse()
	if err != nil {
		return "", fmt.Errorf("parse inspect file: %w", err)
	}
	out, err := renderAtlasInspectSQL(txtarFixtureDialect(fx), list.Statements)
	if err != nil {
		return "", fmt.Errorf("render inspect SQL: %w", err)
	}
	return out, nil
}

func renderAtlasInspectSQL(dialect string, statements []ast.Node) (string, error) {
	tableNames := atlasTableNames(statements)
	indexesByTable := atlasIndexesByTable(dialect, statements)
	var b strings.Builder
	for _, stmt := range statements {
		switch node := stmt.(type) {
		case *ast.CreateTableNode:
			if err := renderAtlasCreateTableSQL(&b, dialect, node, indexesByTable[node.Name]); err != nil {
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
) error {
	quote := atlasIdentifierQuoter(dialect)
	fmt.Fprintf(b, "-- Create %q table\n", table.Name)
	fmt.Fprintf(b, "CREATE TABLE %s (", quote(table.Name))

	parts := make([]string, 0, len(table.Columns)+len(table.Constraints)+len(indexes))
	for _, column := range table.Columns {
		parts = append(parts, renderAtlasColumnSQL(dialect, quote, column))
		if column.Primary {
			parts = append(parts, renderAtlasPrimaryKeySQL(quote, []string{column.Name}))
		}
	}
	for _, constraint := range table.Constraints {
		if constraint.Type != ast.PrimaryKeyConstraint {
			return fmt.Errorf("unsupported inspect constraint %s", constraint.Type)
		}
		parts = append(parts, renderAtlasPrimaryKeySQL(quote, constraint.Columns))
	}
	for _, index := range indexes {
		parts = append(parts, renderAtlasIndexSQL(quote, index))
	}

	b.WriteString(strings.Join(parts, ", "))
	b.WriteString(")")
	if dialect == "mysql" {
		b.WriteString(" CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci")
	}
	b.WriteString(";\n")
	return nil
}

func renderAtlasColumnSQL(dialect string, quote func(string) string, column *ast.ColumnNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", quote(column.Name), atlasColumnType(dialect, column.Type))
	if !column.Nullable {
		b.WriteString(" NOT NULL")
	}
	return b.String()
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

func renderAtlasPrimaryKeySQL(quote func(string) string, columns []string) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quote(column))
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
		return func(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }
	}
	return func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
}

func txtarFixtureDialect(fx Fixture) string {
	parts := strings.Split(filepath.ToSlash(fx.Name), "/")
	if slices.Contains(parts, "mysql") {
		return "mysql"
	}
	if slices.Contains(parts, "mariadb") {
		return "mariadb"
	}
	return "postgresql"
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
	case l != r:
		return fmt.Sprintf("cmp %s %s did not match: got %q want %q", left, right, oneLine(l), oneLine(r))
	default:
		return ""
	}
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
