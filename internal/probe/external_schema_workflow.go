package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	externalSchemaWorkflowSentinel = "_capability/external-schema-workflow/SENTINEL"
	externalSchemaWorkflowIssue    = "stokaro/ptah#669"
	externalSchemaTrustError       = "ptah.yaml external_schema is disabled by default; pass --allow-external-schema to execute it"
)

// ExternalSchemaWorkflowProbe exercises static SQL and external-program desired
// schemas through Ptah's native CLI, including the config trust boundary and a
// complete SQLite generate/apply/convergence workflow.
type ExternalSchemaWorkflowProbe struct {
	// FixtureRoot contains the SQL, HCL, YAML, expected-render, and provider
	// fixtures. Relative paths are resolved from the probe process directory.
	FixtureRoot string
	// Binary overrides the pinned Ptah binary build for focused tests and local
	// development. The zero value builds the go.mod-pinned CLI.
	Binary string
}

func (ExternalSchemaWorkflowProbe) Name() string { return "external-schema-workflow" }

func (p ExternalSchemaWorkflowProbe) Run(fx Fixture) []Result {
	if fx.Name != externalSchemaWorkflowSentinel {
		return nil
	}

	root, err := externalSchemaFixturePath(p.FixtureRoot)
	if err != nil {
		return []Result{externalSchemaHarnessFailure("fixture setup", err)}
	}
	bin, err := p.binary()
	if err != nil {
		return []Result{externalSchemaHarnessFailure("binary build", err)}
	}
	runRoot, err := os.MkdirTemp("", "ptah-external-schema-*")
	if err != nil {
		return []Result{externalSchemaHarnessFailure("runtime setup", err)}
	}
	defer func() { _ = os.RemoveAll(runRoot) }()

	expected, err := os.ReadFile(filepath.Join(root, "expected.sqlite.sql"))
	if err != nil {
		return []Result{externalSchemaHarnessFailure("fixture setup", err)}
	}
	provider, err := buildExternalSchemaProvider(root, runRoot)
	if err != nil {
		return []Result{externalSchemaHarnessFailure("provider build", err)}
	}

	workflow := &externalSchemaWorkflow{
		bin:              bin,
		root:             root,
		runRoot:          runRoot,
		provider:         provider,
		expectedSQL:      strings.TrimSpace(string(expected)),
		databasePath:     filepath.Join(runRoot, "external-schema.db"),
		migrationsDir:    filepath.Join(runRoot, "migrations"),
		convergenceDir:   filepath.Join(runRoot, "convergence-migrations"),
		configPath:       filepath.Join(runRoot, "ptah.yaml"),
		configMarker:     filepath.Join(runRoot, "config-executed"),
		commandMarkers:   filepath.Join(runRoot, "command-markers"),
		deniedMigrations: filepath.Join(runRoot, "denied-migrations"),
	}
	if err := workflow.setup(); err != nil {
		return []Result{externalSchemaHarnessFailure("runtime setup", err)}
	}
	return workflow.run()
}

func (p ExternalSchemaWorkflowProbe) binary() (string, error) {
	if strings.TrimSpace(p.Binary) != "" {
		return p.Binary, nil
	}
	return ptahBinary()
}

func externalSchemaFixturePath(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("fixture root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve fixture root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat fixture root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("fixture root is not a directory: %s", absolute)
	}
	return absolute, nil
}

func buildExternalSchemaProvider(root, runRoot string) (string, error) {
	provider := filepath.Join(runRoot, "schema-provider")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", provider, filepath.Join("provider", "main.go"))
	cmd.Dir = root
	cmd.Env = append(ptahCommandEnvironment(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build fixture provider: %w: %s", err, oneLine(string(output)))
	}
	return provider, nil
}

type externalSchemaWorkflow struct {
	bin              string
	root             string
	runRoot          string
	provider         string
	expectedSQL      string
	databasePath     string
	migrationsDir    string
	convergenceDir   string
	configPath       string
	configMarker     string
	commandMarkers   string
	deniedMigrations string
}

func (w *externalSchemaWorkflow) setup() error {
	for _, dir := range []string{
		w.migrationsDir,
		w.convergenceDir,
		w.commandMarkers,
		w.deniedMigrations,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	program, err := json.Marshal([]string{
		w.provider,
		"--schema", filepath.Join(w.root, "schema.sql"),
		"--marker", w.configMarker,
	})
	if err != nil {
		return fmt.Errorf("encode provider command: %w", err)
	}
	config := fmt.Sprintf("external_schema:\n  program: %s\n  format: sql\n", program)
	if err := os.WriteFile(w.configPath, []byte(config), 0o600); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}

func (w *externalSchemaWorkflow) run() []Result {
	results := []Result{
		w.staticSQLRender(),
		w.explicitRender("sql"),
		w.explicitRender("hcl"),
		w.explicitRender("yaml"),
	}
	results = append(results, w.trustDenialResults()...)

	steps := []func() Result{
		w.configRender,
		w.initialCompare,
		w.initialDrift,
		w.initialPlan,
		w.generate,
		w.apply,
		w.liveFacts,
		w.convergedCompare,
		w.convergedDrift,
		w.convergedPlan,
		w.convergedGenerate,
	}
	for _, step := range steps {
		result := step()
		results = append(results, result)
		if result.Outcome != OK {
			break
		}
	}
	return results
}

func (w *externalSchemaWorkflow) staticSQLRender() Result {
	result := w.runCommand([]string{
		"schema", "render",
		"--schema-file", filepath.Join(w.root, "schema.sql"),
		"--schema-cmd=",
		"--dialect", "sqlite",
	})
	return w.renderResult(
		result,
		"static SQL schema",
		"offline render",
		w.expectedSQL,
		"",
	)
}

func (w *externalSchemaWorkflow) explicitRender(format string) Result {
	marker := filepath.Join(w.commandMarkers, format)
	schema := filepath.Join(w.root, "schema."+format)
	command := strings.Join([]string{
		w.provider,
		"--schema", schema,
		"--marker", marker,
	}, " ")
	result := w.runCommand([]string{
		"schema", "render",
		"--schema-cmd", command,
		"--schema-format", format,
		"--dialect", "sqlite",
	})
	expected := ""
	if format == "sql" {
		expected = w.expectedSQL
	}
	return w.renderResult(
		result,
		"external "+format+" schema",
		"explicit command render",
		expected,
		marker,
	)
}

func (w *externalSchemaWorkflow) trustDenialResults() []Result {
	cases := []struct {
		fixture string
		stage   string
		args    []string
	}{
		{
			fixture: "schema render config trust gate",
			stage:   "config trust denial",
			args: []string{
				"schema", "render",
				"--config", w.configPath,
				"--dialect", "sqlite",
			},
		},
		{
			fixture: "schema compare config trust gate",
			stage:   "config trust denial",
			args: []string{
				"schema", "compare",
				"--config", w.configPath,
				"--db-url", sqliteURL(w.databasePath),
				"--exit-code",
			},
		},
		{
			fixture: "schema drift config trust gate",
			stage:   "config trust denial",
			args: []string{
				"schema", "drift",
				"--config", w.configPath,
				"--db-url", sqliteURL(w.databasePath),
			},
		},
		{
			fixture: "migrations plan config trust gate",
			stage:   "config trust denial",
			args: []string{
				"migrations", "plan",
				"--config", w.configPath,
				"--db-url", sqliteURL(w.databasePath),
			},
		},
		{
			fixture: "migrations generate config trust gate",
			stage:   "config trust denial",
			args: []string{
				"migrations", "generate",
				"--config", w.configPath,
				"--db-url", sqliteURL(w.databasePath),
				"--migrations-dir", w.deniedMigrations,
				"--name", "must_not_exist",
			},
		},
	}

	results := make([]Result, 0, len(cases))
	for _, test := range cases {
		_ = os.Remove(w.configMarker)
		result := w.runCommand(test.args)
		results = append(results, w.trustDenialResult(test.fixture, test.stage, result))
	}
	return results
}

func (w *externalSchemaWorkflow) trustDenialResult(
	fixture string,
	stage string,
	result compositeCommandResult,
) Result {
	if result.err != nil {
		return externalSchemaHarnessFailure(stage, result.err)
	}
	if result.command.exitCode != 2 {
		return externalSchemaGap(fixture, stage, fmt.Sprintf(
			"exit code = %d, want 2: %s",
			result.command.exitCode,
			result.command.diagnostic(),
		))
	}
	if !strings.Contains(result.command.stderr, externalSchemaTrustError) {
		return externalSchemaGap(fixture, stage,
			"stderr does not contain the exact trust-gate diagnostic: "+result.command.diagnostic())
	}
	if _, err := os.Stat(w.configMarker); err == nil {
		return externalSchemaGap(fixture, stage, "provider executed before trust was granted")
	} else if !os.IsNotExist(err) {
		return externalSchemaHarnessFailure(stage, err)
	}
	files, err := filepath.Glob(filepath.Join(w.deniedMigrations, "*.sql"))
	if err != nil {
		return externalSchemaHarnessFailure(stage, err)
	}
	if len(files) != 0 {
		return externalSchemaGap(fixture, stage,
			fmt.Sprintf("trust denial wrote %d migration file(s)", len(files)))
	}
	return externalSchemaOK(
		fixture,
		stage,
		"command rejected config-sourced execution before the provider ran or wrote migrations",
	)
}

func (w *externalSchemaWorkflow) configRender() Result {
	result := w.runConfigCommand([]string{
		"schema", "render",
		"--config", w.configPath,
		"--allow-external-schema",
		"--dialect", "sqlite",
	})
	return w.renderResult(
		result,
		"configured external schema",
		"allowed config render",
		w.expectedSQL,
		w.configMarker,
	)
}

func (w *externalSchemaWorkflow) initialCompare() Result {
	result := w.runConfigCommand([]string{
		"schema", "compare",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
		"--exit-code",
	})
	return w.commandResult(
		result,
		"external schema versus empty database",
		"initial compare",
		1,
		[]string{"users", "posts"},
		"compare detected the desired external schema against an empty SQLite database",
	)
}

func (w *externalSchemaWorkflow) initialDrift() Result {
	result := w.runConfigCommand([]string{
		"schema", "drift",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
	})
	return w.commandResult(
		result,
		"external schema drift from empty database",
		"initial drift",
		1,
		[]string{"tables_added", "constraints_added", "indexes_added"},
		"drift detected the desired external schema against an empty SQLite database",
	)
}

func (w *externalSchemaWorkflow) initialPlan() Result {
	result := w.runConfigCommand([]string{
		"migrations", "plan",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
	})
	return w.commandResult(
		result,
		"external schema migration plan",
		"initial plan",
		0,
		[]string{`CREATE TABLE "users"`, `CREATE TABLE "posts"`, "idx_users_email", "idx_posts_user_id"},
		"plan emitted SQL for every external-schema table and index",
	)
}

func (w *externalSchemaWorkflow) generate() Result {
	result := w.runConfigCommand([]string{
		"migrations", "generate",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
		"--migrations-dir", w.migrationsDir,
		"--name", "external_schema",
	})
	if commandResult := w.commandResult(
		result,
		"external schema migration generation",
		"migration generation",
		0,
		nil,
		"",
	); commandResult.Outcome != OK {
		return commandResult
	}
	up, err := readOneGeneratedMigration(w.migrationsDir, "*.up.sql")
	if err != nil {
		return externalSchemaGap("external schema migration generation", "migration generation", err.Error())
	}
	down, err := readOneGeneratedMigration(w.migrationsDir, "*.down.sql")
	if err != nil {
		return externalSchemaGap("external schema migration generation", "migration generation", err.Error())
	}
	for _, fragment := range []string{
		`CREATE TABLE "users"`,
		`CREATE TABLE "posts"`,
		"idx_users_email",
		"idx_posts_user_id",
	} {
		if !strings.Contains(up, fragment) {
			return externalSchemaGap(
				"external schema migration generation",
				"migration generation",
				fmt.Sprintf("up migration is missing %q", fragment),
			)
		}
	}
	for _, fragment := range []string{`DROP TABLE IF EXISTS "posts"`, `DROP TABLE IF EXISTS "users"`} {
		if !strings.Contains(down, fragment) {
			return externalSchemaGap(
				"external schema migration generation",
				"migration generation",
				fmt.Sprintf("down migration is missing %q", fragment),
			)
		}
	}
	return externalSchemaOK(
		"external schema migration generation",
		"migration generation",
		"generate wrote one reversible Ptah migration pair for the external schema",
	)
}

func (w *externalSchemaWorkflow) apply() Result {
	result := w.runCommand([]string{
		"migrations", "up",
		"--db-url", sqliteURL(w.databasePath),
		"--migrations-dir", w.migrationsDir,
		"--dir-format", "ptah",
	})
	return w.commandResult(
		result,
		"external schema migration application",
		"migration application",
		0,
		nil,
		"the generated external-schema migration applied to SQLite",
	)
}

func (w *externalSchemaWorkflow) liveFacts() Result {
	db, err := openSQLiteRuntimeDB(w.databasePath)
	if err != nil {
		return externalSchemaHarnessFailure("live schema facts", err)
	}
	defer func() { _ = db.Close() }()

	tables, err := sqliteTableNames(db)
	if err != nil {
		return externalSchemaHarnessFailure("live schema facts", err)
	}
	wantTables := []string{"posts", "schema_migrations", "users"}
	if !slices.Equal(tables, wantTables) {
		return externalSchemaGap(
			"SQLite external schema facts",
			"live schema facts",
			fmt.Sprintf("SQLite tables = %v, want %v", tables, wantTables),
		)
	}
	checks := []struct {
		objectType string
		name       string
		fragments  []string
	}{
		{
			objectType: "table",
			name:       "users",
			fragments:  []string{`"id" INTEGER PRIMARY KEY`, `"email" TEXT NOT NULL`},
		},
		{
			objectType: "table",
			name:       "posts",
			fragments: []string{
				`"user_id" INTEGER NOT NULL`,
				`CONSTRAINT "fk_posts_user" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE CASCADE`,
			},
		},
		{
			objectType: "index",
			name:       "idx_users_email",
			fragments:  []string{`CREATE UNIQUE INDEX`, `ON "users" ("email")`},
		},
		{
			objectType: "index",
			name:       "idx_posts_user_id",
			fragments:  []string{`ON "posts" ("user_id")`},
		},
	}
	for _, check := range checks {
		definition, err := sqliteObjectDefinition(db, check.objectType, check.name)
		if err != nil {
			return externalSchemaGap(
				"SQLite external schema facts",
				"live schema facts",
				fmt.Sprintf("inspect %s %q: %v", check.objectType, check.name, err),
			)
		}
		for _, fragment := range check.fragments {
			if !strings.Contains(definition, fragment) {
				return externalSchemaGap(
					"SQLite external schema facts",
					"live schema facts",
					fmt.Sprintf("%s %q is missing %q", check.objectType, check.name, fragment),
				)
			}
		}
	}
	return externalSchemaOK(
		"SQLite external schema facts",
		"live schema facts",
		"SQLite preserved tables, columns, primary keys, unique/index facts, and the cascading foreign key",
	)
}

func (w *externalSchemaWorkflow) convergedCompare() Result {
	result := w.runConfigCommand([]string{
		"schema", "compare",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
		"--exit-code",
	})
	return w.commandResult(
		result,
		"external schema compare convergence",
		"converged compare",
		0,
		nil,
		"compare reported no difference after applying the external schema",
	)
}

func (w *externalSchemaWorkflow) convergedDrift() Result {
	result := w.runConfigCommand([]string{
		"schema", "drift",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
	})
	return w.commandResult(
		result,
		"external schema drift convergence",
		"converged drift",
		0,
		nil,
		"drift reported a clean database after applying the external schema",
	)
}

func (w *externalSchemaWorkflow) convergedPlan() Result {
	result := w.runConfigCommand([]string{
		"migrations", "plan",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
	})
	commandResult := w.commandResult(
		result,
		"external schema plan convergence",
		"converged plan",
		0,
		nil,
		"",
	)
	if commandResult.Outcome != OK {
		return commandResult
	}
	for _, keyword := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "CREATE INDEX", "DROP INDEX"} {
		if strings.Contains(result.command.stdout, keyword) {
			return externalSchemaGap(
				"external schema plan convergence",
				"converged plan",
				fmt.Sprintf("converged plan still contains %q: %s", keyword, result.command.diagnostic()),
			)
		}
	}
	return externalSchemaOK(
		"external schema plan convergence",
		"converged plan",
		"plan emitted no schema-changing SQL after applying the external schema",
	)
}

func (w *externalSchemaWorkflow) convergedGenerate() Result {
	result := w.runConfigCommand([]string{
		"migrations", "generate",
		"--config", w.configPath,
		"--allow-external-schema",
		"--db-url", sqliteURL(w.databasePath),
		"--migrations-dir", w.convergenceDir,
		"--name", "must_not_exist",
	})
	commandResult := w.commandResult(
		result,
		"external schema generate convergence",
		"converged generate",
		0,
		nil,
		"",
	)
	if commandResult.Outcome != OK {
		return commandResult
	}
	files, err := filepath.Glob(filepath.Join(w.convergenceDir, "*.sql"))
	if err != nil {
		return externalSchemaHarnessFailure("converged generate", err)
	}
	if len(files) != 0 {
		return externalSchemaGap(
			"external schema generate convergence",
			"converged generate",
			fmt.Sprintf("converged generate wrote %d migration file(s)", len(files)),
		)
	}
	return externalSchemaOK(
		"external schema generate convergence",
		"converged generate",
		"generate wrote no migration files after applying the external schema",
	)
}

func (w *externalSchemaWorkflow) runCommand(args []string) compositeCommandResult {
	replacements := []compositePathReplacement{
		{path: w.root, marker: "<fixture>"},
		{path: w.runRoot, marker: "<run>"},
	}
	return runPtahCommandResult(w.bin, args, w.runRoot, replacements)
}

func (w *externalSchemaWorkflow) runConfigCommand(args []string) compositeCommandResult {
	if err := os.Remove(w.configMarker); err != nil && !os.IsNotExist(err) {
		return compositeCommandResult{err: err, args: args}
	}
	return w.runCommand(args)
}

func (w *externalSchemaWorkflow) renderResult(
	result compositeCommandResult,
	fixture string,
	stage string,
	expected string,
	marker string,
) Result {
	commandResult := w.commandResult(result, fixture, stage, 0, nil, "")
	if commandResult.Outcome != OK {
		return commandResult
	}
	rendered, err := renderedSQLSection(result.command.stdout)
	if err != nil {
		return externalSchemaGap(fixture, stage, err.Error()+": "+result.command.diagnostic())
	}
	if expected != "" && rendered != expected {
		return externalSchemaGap(fixture, stage, "rendered SQL differs from the expected SQLite snapshot")
	}
	for _, fragment := range []string{
		`CREATE TABLE "users"`,
		`CREATE TABLE "posts"`,
		"idx_users_email",
		"idx_posts_user_id",
		"ON DELETE CASCADE",
	} {
		if !strings.Contains(rendered, fragment) {
			return externalSchemaGap(fixture, stage, fmt.Sprintf("rendered SQL is missing %q", fragment))
		}
	}
	if marker != "" {
		if _, err := os.Stat(marker); err != nil {
			if os.IsNotExist(err) {
				return externalSchemaGap(fixture, stage, "external provider did not write its execution marker")
			}
			return externalSchemaHarnessFailure(stage, err)
		}
	}
	return externalSchemaOK(fixture, stage, "desired schema rendered with all expected SQLite facts")
}

func (w *externalSchemaWorkflow) commandResult(
	result compositeCommandResult,
	fixture string,
	stage string,
	exitCode int,
	outputFragments []string,
	detail string,
) Result {
	if result.err != nil {
		return externalSchemaHarnessFailure(stage, fmt.Errorf(
			"execute `ptah %s`: %w; %s",
			strings.Join(result.args, " "),
			result.err,
			result.command.diagnostic(),
		))
	}
	if result.command.exitCode != exitCode {
		return externalSchemaGap(fixture, stage, fmt.Sprintf(
			"exit code = %d, want %d: %s",
			result.command.exitCode,
			exitCode,
			result.command.diagnostic(),
		))
	}
	output := result.command.stdout + "\n" + result.command.stderr
	for _, fragment := range outputFragments {
		if !strings.Contains(output, fragment) {
			return externalSchemaGap(
				fixture,
				stage,
				fmt.Sprintf("command output is missing %q: %s", fragment, result.command.diagnostic()),
			)
		}
	}
	if slices.Contains(result.args, "--allow-external-schema") {
		if _, err := os.Stat(w.configMarker); err != nil {
			if os.IsNotExist(err) {
				return externalSchemaGap(fixture, stage, "allowed config command did not execute the provider")
			}
			return externalSchemaHarnessFailure(stage, err)
		}
	}
	return externalSchemaOK(fixture, stage, detail)
}

func externalSchemaOK(fixture, stage, detail string) Result {
	return Result{
		Probe:   "external-schema-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: OK,
		Detail:  detail,
	}
}

func externalSchemaGap(fixture, stage, detail string) Result {
	return Result{
		Probe:   "external-schema-workflow",
		Fixture: fixture,
		Stage:   stage,
		Outcome: Gap,
		Detail:  detail,
		Issue:   externalSchemaWorkflowIssue,
	}
}

func externalSchemaHarnessFailure(stage string, err error) Result {
	return Result{
		Probe:   "external-schema-workflow",
		Fixture: externalSchemaWorkflowSentinel,
		Stage:   stage,
		Outcome: Fail,
		Detail:  err.Error(),
	}
}
