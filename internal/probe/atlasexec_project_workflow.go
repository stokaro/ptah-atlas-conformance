package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

const (
	atlasExecProjectProbeName = "atlasexec-project-workflow"

	atlasExecVersionedBasicFixture = "atlasexec/internal/e2e/testdata/versioned-basic/atlas.hcl"
	atlasExecMultiTenantsFixture   = "atlasexec/internal/e2e/testdata/multi-tenants/atlas.hcl"

	atlasExecVersionedBasicIssue = "stokaro/ptah#276"
	atlasExecMultiTenantsIssue   = "stokaro/ptah#1333"

	atlasExecFirstVersion  = "20240112070806"
	atlasExecSecondVersion = "20240116003831"
	atlasExecReportFormat  = "{{ json . }}"
)

// AtlasExecProjectWorkflowProbe executes the vendored Atlas v1.3.0 atlasexec
// SQLite project fixtures. Each fixture is copied to scratch before the real
// ptah-compat binary runs, preserving its atlas.hcl, migration files, and
// checksums byte-for-byte in the corpus.
type AtlasExecProjectWorkflowProbe struct {
	// Binary overrides the go.mod-pinned ptah-compat build for focused tests.
	Binary string
}

func (AtlasExecProjectWorkflowProbe) Name() string { return atlasExecProjectProbeName }

func (p AtlasExecProjectWorkflowProbe) Run(fx Fixture) []Result {
	switch fx.Name {
	case atlasExecVersionedBasicFixture:
		return p.runVersionedBasic(fx)
	case atlasExecMultiTenantsFixture:
		return p.runMultiTenants(fx)
	default:
		return nil
	}
}

func (p AtlasExecProjectWorkflowProbe) runVersionedBasic(fx Fixture) []Result {
	w, failure := newProWorkflowRuntime(
		atlasExecProjectProbeName,
		fx.Name,
		fx.Dir,
		p.Binary,
		atlasExecVersionedBasicIssue,
	)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	workflow := atlasExecVersionedBasicWorkflow{proWorkflowRuntime: w, fixture: fx.Name}
	return []Result{workflow.run()}
}

func (p AtlasExecProjectWorkflowProbe) runMultiTenants(fx Fixture) []Result {
	w, failure := newProWorkflowRuntime(
		atlasExecProjectProbeName,
		fx.Name,
		fx.Dir,
		p.Binary,
		atlasExecMultiTenantsIssue,
	)
	if failure != nil {
		return []Result{*failure}
	}
	defer w.cleanup()

	workflow := atlasExecMultiTenantsWorkflow{proWorkflowRuntime: w, fixture: fx.Name}
	return []Result{workflow.run()}
}

func atlasExecProjectConfigFixture(name string) bool {
	return name == atlasExecVersionedBasicFixture || name == atlasExecMultiTenantsFixture
}

type atlasExecVersionedBasicWorkflow struct {
	*proWorkflowRuntime
	fixture string
}

func (w *atlasExecVersionedBasicWorkflow) run() Result {
	if failure := w.pendingStatus(); failure != nil {
		return *failure
	}
	if failure := w.applyOnce(); failure != nil {
		return *failure
	}
	if failure := w.applyNoop(); failure != nil {
		return *failure
	}
	return w.ok(
		w.fixture,
		"workflow",
		"the untouched Atlas v1.3.0 versioned-basic project reported one pending migration, applied version 20240112070806 once, and returned an empty Applied result on the second apply; live SQLite state and Atlas revision state remained correct",
	)
}

func (w *atlasExecVersionedBasicWorkflow) pendingStatus() *Result {
	const stage = "status pending"
	result, harness := w.runCLI(
		stage,
		"migrate", "status",
		"--format", atlasExecReportFormat,
		"--env", "local",
		"--url", "sqlite://file.db?_fk=1",
	)
	if harness != nil {
		return harness
	}
	if gap := w.expectExit(w.fixture, stage, result, 0); gap != nil {
		return gap
	}
	if gap := w.expectStreams(stage, result, 1); gap != nil {
		return gap
	}
	reports, err := decodeAtlasExecJSONStream[atlasExecStatusReport](result.stdout)
	if err != nil {
		gap := w.gap(w.fixture, stage, "status did not emit one atlasexec JSON report: "+oneLine(err.Error()))
		return &gap
	}
	if err := validateAtlasExecPendingStatus(reports); err != nil {
		gap := w.gap(w.fixture, stage, err.Error())
		return &gap
	}
	return nil
}

func (w *atlasExecVersionedBasicWorkflow) applyOnce() *Result {
	const stage = "apply once"
	result, harness := w.runCLI(
		stage,
		"migrate", "apply",
		"--format", atlasExecReportFormat,
		"--env", "local",
		"--url", "sqlite://file.db?_fk=1",
	)
	if harness != nil {
		return harness
	}
	if gap := w.expectExit(w.fixture, stage, result, 0); gap != nil {
		return gap
	}
	if gap := w.expectStreams(stage, result, 1); gap != nil {
		return gap
	}
	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](result.stdout)
	if err != nil {
		gap := w.gap(w.fixture, stage, "apply did not emit one atlasexec JSON report: "+oneLine(err.Error()))
		return &gap
	}
	if err := validateAtlasExecVersionedApply(reports, []string{atlasExecFirstVersion}); err != nil {
		gap := w.gap(w.fixture, stage, err.Error())
		return &gap
	}
	if err := validateAtlasExecTenantState(
		filepath.Join(w.runRoot, "file.db"),
		atlasExecTenantState{
			tables:      []string{"atlas_schema_revisions", "t1"},
			rows:        0,
			uniqueIndex: false,
			revisions: []atlasExecRevisionProgress{
				{version: atlasExecFirstVersion, applied: 1, total: 1},
			},
		},
	); err != nil {
		gap := w.gap(w.fixture, stage, "versioned-basic SQLite state: "+err.Error())
		return &gap
	}
	return nil
}

func (w *atlasExecVersionedBasicWorkflow) applyNoop() *Result {
	const stage = "second apply no-op"
	result, harness := w.runCLI(
		stage,
		"migrate", "apply",
		"--format", atlasExecReportFormat,
		"--env", "local",
		"--url", "sqlite://file.db?_fk=1",
	)
	if harness != nil {
		return harness
	}
	if gap := w.expectExit(w.fixture, stage, result, 0); gap != nil {
		return gap
	}
	if gap := w.expectStreams(stage, result, 1); gap != nil {
		return gap
	}
	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](result.stdout)
	if err != nil {
		gap := w.gap(w.fixture, stage, "second apply did not emit one atlasexec JSON report: "+oneLine(err.Error()))
		return &gap
	}
	if err := validateAtlasExecVersionedApply(reports, nil); err != nil {
		gap := w.gap(w.fixture, stage, err.Error())
		return &gap
	}
	if err := validateAtlasExecTenantState(
		filepath.Join(w.runRoot, "file.db"),
		atlasExecTenantState{
			tables:      []string{"atlas_schema_revisions", "t1"},
			rows:        0,
			uniqueIndex: false,
			revisions: []atlasExecRevisionProgress{
				{version: atlasExecFirstVersion, applied: 1, total: 1},
			},
		},
	); err != nil {
		gap := w.gap(w.fixture, stage, "versioned-basic SQLite state after no-op: "+err.Error())
		return &gap
	}
	return nil
}

type atlasExecMultiTenantsWorkflow struct {
	*proWorkflowRuntime
	fixture string
}

func (w *atlasExecMultiTenantsWorkflow) run() Result {
	if failure := w.applyFirstMigration(); failure != nil {
		return *failure
	}
	if failure := w.insertDuplicateData(); failure != nil {
		return *failure
	}
	if failure := w.applyWithOneTenantFailure(); failure != nil {
		return *failure
	}
	if failure := w.retryFailedTenant(); failure != nil {
		return *failure
	}
	return w.ok(
		w.fixture,
		"workflow",
		"the untouched Atlas v1.3.0 multi-tenants project produced two ordered reports per apply: amount 1 migrated both databases, the UNIQUE migration completed only for bar, and retry left bar a no-op while retrying foo; both live SQLite end states matched the atlasexec fixture",
	)
}

func (w *atlasExecMultiTenantsWorkflow) applyFirstMigration() *Result {
	const stage = "amount-one apply"
	result, harness := w.runCLI(
		stage,
		"migrate", "apply",
		"--format", atlasExecReportFormat,
		"--env", "local",
		"1",
	)
	if harness != nil {
		return harness
	}
	if result.exitCode != 0 {
		gap := w.gap(
			w.fixture,
			stage,
			fmt.Sprintf("dynamic env expansion did not produce the two atlasexec tenant reports (exit code %d): %s", result.exitCode, result.diagnostic()),
		)
		return &gap
	}
	if gap := w.expectStreams(stage, result, 2); gap != nil {
		return gap
	}
	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](result.stdout)
	if err != nil {
		gap := w.gap(w.fixture, stage, "amount-one apply did not emit two atlasexec JSON reports: "+oneLine(err.Error()))
		return &gap
	}
	if err := validateAtlasExecTenantReports(reports, []atlasExecTenantReportExpectation{
		{host: "bar.db", appliedVersions: []string{atlasExecFirstVersion}},
		{host: "foo.db", appliedVersions: []string{atlasExecFirstVersion}},
	}); err != nil {
		gap := w.gap(w.fixture, stage, err.Error())
		return &gap
	}
	if err := w.validateTenantStates(stage, atlasExecInitialTenantStates()); err != nil {
		return err
	}
	return nil
}

func (w *atlasExecMultiTenantsWorkflow) insertDuplicateData() *Result {
	const stage = "duplicate data setup"
	db, err := openSQLiteRuntimeDB(filepath.Join(w.runRoot, "foo.db"))
	if err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("open foo tenant: %w", err))
		return &failure
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "INSERT INTO t1(c1) VALUES (1),(1),(1)"); err != nil {
		failure := w.harnessFailure(stage, fmt.Errorf("insert duplicate foo rows: %w", err))
		return &failure
	}
	return nil
}

func (w *atlasExecMultiTenantsWorkflow) applyWithOneTenantFailure() *Result {
	const stage = "one completed and one UNIQUE failure"
	result, harness := w.runCLI(
		stage,
		"migrate", "apply",
		"--format", atlasExecReportFormat,
		"--env", "local",
	)
	if harness != nil {
		return harness
	}
	if gap := w.expectExit(w.fixture, stage, result, 1); gap != nil {
		return gap
	}
	if gap := w.expectStreams(stage, result, 2); gap != nil {
		return gap
	}
	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](result.stdout)
	if err != nil {
		gap := w.gap(w.fixture, stage, "partial apply did not emit two atlasexec JSON reports: "+oneLine(err.Error()))
		return &gap
	}
	if err := validateAtlasExecTenantReports(reports, []atlasExecTenantReportExpectation{
		{host: "bar.db", appliedVersions: []string{atlasExecSecondVersion}},
		{host: "foo.db", appliedVersions: []string{atlasExecSecondVersion}, errorFragment: "UNIQUE constraint failed"},
	}); err != nil {
		gap := w.gap(w.fixture, stage, err.Error())
		return &gap
	}
	if err := w.validateTenantStates(stage, atlasExecFinalTenantStates()); err != nil {
		return err
	}
	return nil
}

func (w *atlasExecMultiTenantsWorkflow) retryFailedTenant() *Result {
	const stage = "retry successful no-op and failed tenant"
	result, harness := w.runCLI(
		stage,
		"migrate", "apply",
		"--format", atlasExecReportFormat,
		"--env", "local",
	)
	if harness != nil {
		return harness
	}
	if gap := w.expectExit(w.fixture, stage, result, 1); gap != nil {
		return gap
	}
	if gap := w.expectStreams(stage, result, 2); gap != nil {
		return gap
	}
	reports, err := decodeAtlasExecJSONStream[atlasExecApplyReport](result.stdout)
	if err != nil {
		gap := w.gap(w.fixture, stage, "retry did not emit two atlasexec JSON reports: "+oneLine(err.Error()))
		return &gap
	}
	if err := validateAtlasExecTenantReports(reports, []atlasExecTenantReportExpectation{
		{host: "bar.db"},
		{host: "foo.db", appliedVersions: []string{atlasExecSecondVersion}, errorFragment: "UNIQUE constraint failed"},
	}); err != nil {
		gap := w.gap(w.fixture, stage, err.Error())
		return &gap
	}
	if err := w.validateTenantStates(stage, atlasExecFinalTenantStates()); err != nil {
		return err
	}
	return nil
}

func (w *atlasExecMultiTenantsWorkflow) validateTenantStates(
	stage string,
	expectations map[string]atlasExecTenantState,
) *Result {
	for _, tenant := range []string{"bar.db", "foo.db"} {
		if err := validateAtlasExecTenantState(filepath.Join(w.runRoot, tenant), expectations[tenant]); err != nil {
			gap := w.gap(w.fixture, stage, tenant+" SQLite state: "+err.Error())
			return &gap
		}
	}
	return nil
}

func (w *atlasExecVersionedBasicWorkflow) expectStreams(
	stage string,
	result ptahCommandResult,
	reportCount int,
) *Result {
	return expectAtlasExecStreams(w.proWorkflowRuntime, w.fixture, stage, result, reportCount)
}

func (w *atlasExecMultiTenantsWorkflow) expectStreams(
	stage string,
	result ptahCommandResult,
	reportCount int,
) *Result {
	return expectAtlasExecStreams(w.proWorkflowRuntime, w.fixture, stage, result, reportCount)
}

func expectAtlasExecStreams(
	w *proWorkflowRuntime,
	fixture string,
	stage string,
	result ptahCommandResult,
	reportCount int,
) *Result {
	if err := validateAtlasExecStreams(result, reportCount); err != nil {
		gap := w.gap(fixture, stage, err.Error())
		return &gap
	}
	return nil
}

func validateAtlasExecStreams(result ptahCommandResult, reportCount int) error {
	if result.stderr != "" {
		return fmt.Errorf("stderr is not empty: %s", oneLine(result.stderr))
	}
	wantNewlines := reportCount - 1
	if got := strings.Count(result.stdout, "\n"); got != wantNewlines {
		return fmt.Errorf("stdout newline count = %d, want %d between %d JSON reports", got, wantNewlines, reportCount)
	}
	if strings.TrimSpace(result.stdout) != result.stdout {
		return fmt.Errorf("stdout has leading or trailing whitespace")
	}
	for i, report := range strings.Split(result.stdout, "\n") {
		if strings.TrimSpace(report) != report {
			return fmt.Errorf("stdout JSON report %d has separator-adjacent whitespace", i+1)
		}
	}
	return nil
}

func atlasExecInitialTenantStates() map[string]atlasExecTenantState {
	state := atlasExecTenantState{
		tables:      []string{"atlas_schema_revisions", "t1"},
		rows:        0,
		uniqueIndex: false,
		revisions: []atlasExecRevisionProgress{
			{version: atlasExecFirstVersion, applied: 1, total: 1},
		},
	}
	return map[string]atlasExecTenantState{"bar.db": state, "foo.db": state}
}

func atlasExecFinalTenantStates() map[string]atlasExecTenantState {
	return map[string]atlasExecTenantState{
		"bar.db": {
			tables:      []string{"atlas_schema_revisions", "t1"},
			rows:        0,
			uniqueIndex: true,
			revisions: []atlasExecRevisionProgress{
				{version: atlasExecFirstVersion, applied: 1, total: 1},
				{version: atlasExecSecondVersion, applied: 1, total: 1},
			},
		},
		"foo.db": {
			tables:      []string{"atlas_schema_revisions", "t1"},
			rows:        3,
			uniqueIndex: false,
			revisions: []atlasExecRevisionProgress{
				{version: atlasExecFirstVersion, applied: 1, total: 1},
			},
		},
	}
}

type atlasExecMigrationFile struct {
	Version string
}

type atlasExecStatusReport struct {
	Pending []atlasExecMigrationFile
}

type atlasExecApplyReport struct {
	URL     atlasExecURL
	Applied []atlasExecAppliedFile
	Error   string
}

type atlasExecURL struct {
	Host string
}

type atlasExecAppliedFile struct {
	Version string
}

type atlasExecTenantReportExpectation struct {
	host            string
	appliedVersions []string
	errorFragment   string
}

func decodeAtlasExecJSONStream[T any](output string) ([]T, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	var reports []T
	for {
		var report T
		err := decoder.Decode(&report)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode report %d: %w", len(reports)+1, err)
		}
		reports = append(reports, report)
	}
	if len(reports) == 0 {
		return nil, errors.New("no JSON reports")
	}
	return reports, nil
}

func validateAtlasExecPendingStatus(reports []atlasExecStatusReport) error {
	switch {
	case len(reports) != 1:
		return fmt.Errorf("status report count = %d, want 1", len(reports))
	case len(reports[0].Pending) != 1:
		return fmt.Errorf("status Pending length = %d, want 1", len(reports[0].Pending))
	case reports[0].Pending[0].Version != atlasExecFirstVersion:
		return fmt.Errorf("status pending version = %q, want %q", reports[0].Pending[0].Version, atlasExecFirstVersion)
	default:
		return nil
	}
}

func validateAtlasExecVersionedApply(reports []atlasExecApplyReport, wantVersions []string) error {
	if len(reports) != 1 {
		return fmt.Errorf("apply report count = %d, want 1", len(reports))
	}
	gotVersions := atlasExecAppliedVersions(reports[0])
	if !slices.Equal(gotVersions, wantVersions) {
		return fmt.Errorf("apply Applied versions = %v, want %v", gotVersions, wantVersions)
	}
	if reports[0].Error != "" {
		return fmt.Errorf("apply report Error = %q, want empty", reports[0].Error)
	}
	return nil
}

func validateAtlasExecTenantReports(
	reports []atlasExecApplyReport,
	want []atlasExecTenantReportExpectation,
) error {
	if len(reports) != len(want) {
		return fmt.Errorf("tenant report count = %d, want %d", len(reports), len(want))
	}
	for i, expected := range want {
		actual := reports[i]
		if actual.URL.Host != expected.host {
			return fmt.Errorf("tenant report %d URL host = %q, want %q", i, actual.URL.Host, expected.host)
		}
		if versions := atlasExecAppliedVersions(actual); !slices.Equal(versions, expected.appliedVersions) {
			return fmt.Errorf("tenant report %d Applied versions = %v, want %v", i, versions, expected.appliedVersions)
		}
		switch {
		case expected.errorFragment == "" && actual.Error != "":
			return fmt.Errorf("tenant report %d Error = %q, want empty", i, actual.Error)
		case expected.errorFragment != "" && !strings.Contains(actual.Error, expected.errorFragment):
			return fmt.Errorf("tenant report %d Error = %q, want fragment %q", i, actual.Error, expected.errorFragment)
		}
	}
	return nil
}

func atlasExecAppliedVersions(report atlasExecApplyReport) []string {
	versions := make([]string, 0, len(report.Applied))
	for _, applied := range report.Applied {
		versions = append(versions, applied.Version)
	}
	return versions
}

type atlasExecTenantState struct {
	tables      []string
	rows        int
	uniqueIndex bool
	revisions   []atlasExecRevisionProgress
}

type atlasExecRevisionProgress struct {
	version string
	applied int
	total   int
}

func validateAtlasExecTenantState(path string, want atlasExecTenantState) error {
	db, err := openSQLiteRuntimeDB(path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	tables, err := sqliteTableNames(db)
	if err != nil {
		return fmt.Errorf("inspect tables: %w", err)
	}
	if !slices.Equal(tables, want.tables) {
		return fmt.Errorf("tables = %v, want %v", tables, want.tables)
	}

	var rows int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM t1").Scan(&rows); err != nil {
		return fmt.Errorf("count t1 rows: %w", err)
	}
	if rows != want.rows {
		return fmt.Errorf("t1 row count = %d, want %d", rows, want.rows)
	}

	var indexes int
	if err := db.QueryRowContext(
		context.Background(),
		"SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'c1_unique'",
	).Scan(&indexes); err != nil {
		return fmt.Errorf("inspect c1_unique: %w", err)
	}
	if got := indexes == 1; got != want.uniqueIndex {
		return fmt.Errorf("c1_unique present = %t, want %t", got, want.uniqueIndex)
	}

	revisions, err := sqliteRevisionFacts(db)
	if err != nil {
		return fmt.Errorf("inspect Atlas revisions: %w", err)
	}
	progress := make([]atlasExecRevisionProgress, 0, len(revisions))
	for _, revision := range revisions {
		progress = append(progress, atlasExecRevisionProgress{
			version: revision.Version,
			applied: revision.Applied,
			total:   revision.Total,
		})
	}
	if !slices.Equal(progress, want.revisions) {
		return fmt.Errorf("revision progress = %v, want %v", progress, want.revisions)
	}
	return nil
}
