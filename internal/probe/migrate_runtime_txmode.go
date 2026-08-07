package probe

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	fileTxModeIssue            = "stokaro/ptah#998"
	fileTxModeBookkeepingIssue = "stokaro/ptah#887"
	fileTxModeDiagnosticIssue  = "stokaro/ptah#1076"
	fileTxModeNoSeparatorIssue = "stokaro/ptah#1081"
	fileTxModeWhitespaceIssue  = "stokaro/ptah#1077"
	fileTxModeFilename         = "1_case.sql"
	fileTxModeBodyTable        = "txmode_body"
	fileTxModeMissingTable     = "txmode_missing"
	fileTxModePtahBetterStage  = "ptah-better"
)

const fileTxModeFailingBody = `CREATE TABLE txmode_body (id INTEGER PRIMARY KEY);
INSERT INTO txmode_missing (id) VALUES (1);
`

type fileTxModeMatrixCase struct {
	Name                  string
	GlobalMode            string
	Directive             string
	DirectiveLabel        string
	AtlasExpected         fileTxModeExpectedState
	PtahExpected          fileTxModeExpectedState
	WantError             string
	IntentionalDivergence string
	Issue                 string
}

type fileTxModeExpectedState struct {
	BodyTable bool
	Tables    []string
	Revisions []fileTxModeRevisionFact
}

type fileTxModeRevisionFact struct {
	Version              string
	Description          string
	Type                 int64
	Applied              int64
	Total                int64
	ErrorIsNull          bool
	ErrorStatementIsNull bool
	ErrorStatement       string
	OperatorVersion      string
}

type fileTxModeObservation struct {
	Process   integrityProcessResult
	Tables    []string
	Revisions []projectConfigRevisionMetadata
}

type fileTxModeRuntime struct {
	bin        string
	migrations string
	dbPath     string
	env        []string
	dirFormat  string
}

type fileTxModePair struct {
	Atlas fileTxModeObservation
	Ptah  fileTxModeObservation
}

func sqliteMigrateFileTxModeOracle(ptahBin, nativeBin, atlasBin string) []Result {
	atlasRolledBack := fileTxModeExpectedState{
		Tables:    []string{"atlas_schema_revisions"},
		Revisions: []fileTxModeRevisionFact{},
	}
	atlasNoTransaction := fileTxModeExpectedState{
		BodyTable: true,
		Tables:    []string{"atlas_schema_revisions", fileTxModeBodyTable},
		Revisions: []fileTxModeRevisionFact{
			{
				Version:         "1",
				Description:     "case",
				Type:            2,
				Applied:         1,
				Total:           2,
				ErrorStatement:  "INSERT INTO txmode_missing (id) VALUES (1);",
				OperatorVersion: "Atlas CLI v1.3.0",
			},
		},
	}
	ptahNoTransaction := fileTxModeExpectedState{
		BodyTable: true,
		Tables:    []string{"atlas_schema_revisions", fileTxModeBodyTable},
		Revisions: []fileTxModeRevisionFact{
			{
				Version:         "1",
				Description:     "case",
				Type:            2,
				Applied:         1,
				Total:           2,
				ErrorStatement:  "INSERT INTO txmode_missing (id) VALUES (1);",
				OperatorVersion: "Ptah",
			},
		},
	}
	cases := []fileTxModeMatrixCase{
		{Name: "global-file-directive-absent", GlobalMode: "file"},
		{Name: "global-file-directive-file", GlobalMode: "file", Directive: "-- atlas:txmode file\n\n"},
		{
			Name:          "global-file-directive-none",
			GlobalMode:    "file",
			Directive:     "-- atlas:txmode none\n\n",
			AtlasExpected: fileTxModeExpectedState{BodyTable: true},
			PtahExpected:  fileTxModeExpectedState{BodyTable: true},
		},
		{
			Name:       "global-file-directive-all",
			GlobalMode: "file",
			Directive:  "-- atlas:txmode all\n\n",
			WantError:  `txmode "all" is not allowed in file directive "1_case.sql". Use "file" instead`,
		},
		{Name: "global-all-directive-absent", GlobalMode: "all"},
		{
			Name:       "global-all-directive-file",
			GlobalMode: "all",
			Directive:  "-- atlas:txmode file\n\n",
			WantError:  `cannot set txmode directive to "file" in "1_case.sql" when txmode "all" is set globally`,
		},
		{
			Name:       "global-all-directive-none",
			GlobalMode: "all",
			Directive:  "-- atlas:txmode none\n\n",
			WantError:  `cannot set txmode directive to "none" in "1_case.sql" when txmode "all" is set globally`,
		},
		{
			Name:       "global-all-directive-all",
			GlobalMode: "all",
			Directive:  "-- atlas:txmode all\n\n",
			WantError:  `txmode "all" is not allowed in file directive "1_case.sql". Use "file" instead`,
		},
		{
			Name:          "global-none-directive-absent",
			GlobalMode:    "none",
			AtlasExpected: fileTxModeExpectedState{BodyTable: true},
			PtahExpected:  fileTxModeExpectedState{BodyTable: true},
		},
		{Name: "global-none-directive-file", GlobalMode: "none", Directive: "-- atlas:txmode file\n\n"},
		{
			Name:          "global-none-directive-none",
			GlobalMode:    "none",
			Directive:     "-- atlas:txmode none\n\n",
			AtlasExpected: fileTxModeExpectedState{BodyTable: true},
			PtahExpected:  fileTxModeExpectedState{BodyTable: true},
		},
		{
			Name:       "global-none-directive-all",
			GlobalMode: "none",
			Directive:  "-- atlas:txmode all\n\n",
			WantError:  `txmode "all" is not allowed in file directive "1_case.sql". Use "file" instead`,
		},
		{
			Name:       "unknown-directive",
			GlobalMode: "file",
			Directive:  "-- atlas:txmode statement\n\n",
			WantError:  `unknown txmode "statement" found in file directive "1_case.sql"`,
		},
		{
			Name:       "duplicate-directive",
			GlobalMode: "file",
			Directive:  "-- atlas:txmode none\n-- atlas:txmode file\n\n",
			WantError:  `multiple txmode values found in file "1_case.sql": ["none" "file"]`,
		},
		{
			Name:           "misplaced-directive-is-ignored",
			GlobalMode:     "file",
			Directive:      "-- generated migration\n\n-- atlas:txmode none\n",
			DirectiveLabel: "none (misplaced; ignored)",
		},
		{
			Name:           "separator-empty-lf",
			GlobalMode:     "file",
			Directive:      "-- atlas:txmode none\n\n",
			DirectiveLabel: `none (empty separator with LF)`,
			AtlasExpected:  atlasNoTransaction,
			PtahExpected:   ptahNoTransaction,
		},
		{
			Name:           "separator-spaces-lf",
			GlobalMode:     "file",
			Directive:      "-- atlas:txmode none\n   \n",
			DirectiveLabel: `none (spaces with LF)`,
			AtlasExpected:  atlasNoTransaction,
			PtahExpected:   ptahNoTransaction,
		},
		{
			Name:           "separator-tabs-lf",
			GlobalMode:     "file",
			Directive:      "-- atlas:txmode none\n\t\t\n",
			DirectiveLabel: `none (tabs with LF)`,
			AtlasExpected:  atlasNoTransaction,
			PtahExpected:   ptahNoTransaction,
		},
		{
			Name:           "separator-mixed-whitespace-lf",
			GlobalMode:     "file",
			Directive:      "-- atlas:txmode none\n \t \n",
			DirectiveLabel: `none (mixed whitespace with LF)`,
			AtlasExpected:  atlasNoTransaction,
			PtahExpected:   ptahNoTransaction,
		},
		{
			Name:                  "separator-empty-crlf-intentional-divergence",
			GlobalMode:            "file",
			Directive:             "-- atlas:txmode none\r\n\r\n",
			DirectiveLabel:        `none (empty separator with CRLF)`,
			AtlasExpected:         atlasRolledBack,
			PtahExpected:          ptahNoTransaction,
			IntentionalDivergence: "Atlas CE v1.3 drops the explicit txmode directive when the empty separator uses CRLF; Ptah honors it instead of copying line-ending-sensitive behavior that discards user intent",
			Issue:                 fileTxModeWhitespaceIssue,
		},
		{
			Name:                  "separator-mixed-whitespace-crlf-intentional-divergence",
			GlobalMode:            "file",
			Directive:             "-- atlas:txmode none\n \t\r\n",
			DirectiveLabel:        `none (mixed whitespace with CRLF)`,
			AtlasExpected:         atlasRolledBack,
			PtahExpected:          ptahNoTransaction,
			IntentionalDivergence: "Atlas CE v1.3 drops the explicit txmode directive when the whitespace separator contains a carriage return; Ptah honors it instead of copying line-ending-sensitive behavior that discards user intent",
			Issue:                 fileTxModeWhitespaceIssue,
		},
		{
			Name:                  "missing-separator-intentional-divergence",
			GlobalMode:            "file",
			Directive:             "-- atlas:txmode none\n",
			DirectiveLabel:        "none (no separator)",
			AtlasExpected:         atlasRolledBack,
			PtahExpected:          ptahNoTransaction,
			IntentionalDivergence: "Atlas CE v1.3 drops the explicit txmode directive when the statement immediately follows it; Ptah honors it instead of copying behavior that discards user intent",
			Issue:                 fileTxModeNoSeparatorIssue,
		},
	}

	results := make([]Result, 0, len(cases)+9)
	observations := make(map[string]fileTxModePair, len(cases))
	for _, tc := range cases {
		fixture := "sqlite/per-file-txmode/matrix/" + tc.Name
		pair, err := runFileTxModePair(ptahBin, atlasBin, map[string]string{
			fileTxModeFilename: tc.Directive + fileTxModeFailingBody,
		}, []string{"--tx-mode", tc.GlobalMode})
		if err != nil {
			results = append(results, fileTxModeFailure(fixture, "execute", err))
			continue
		}
		observations[tc.Name] = pair
		results = append(results, compareFileTxModeMatrixCase(fixture, tc, pair))
	}

	results = append(results,
		compareFileTxModeBookkeeping(observations),
	)
	results = append(results, sqliteMigrateFileTxModeSelectionOracle(ptahBin, atlasBin)...)
	results = append(results, sqliteMigrateSplitFileTxModeOracle(ptahBin, atlasBin)...)
	results = append(results, sqliteMigrateTxtarFileTxModeOracle(ptahBin, atlasBin)...)
	results = append(results, sqliteNativeNoTransactionOracle(nativeBin))
	return results
}

func runFileTxModePair(
	ptahBin, atlasBin string,
	files map[string]string,
	applyArgs []string,
) (fileTxModePair, error) {
	return runFileTxModePairWithFormat(ptahBin, atlasBin, files, applyArgs, "")
}

func runFileTxModePairWithFormat(
	ptahBin, atlasBin string,
	files map[string]string,
	applyArgs []string,
	dirFormat string,
) (fileTxModePair, error) {
	root, err := os.MkdirTemp("", "migrate-runtime-file-txmode-*")
	if err != nil {
		return fileTxModePair{}, err
	}
	defer func() { _ = os.RemoveAll(root) }()

	atlas, err := prepareFileTxModeRuntimeWithFormat(
		atlasBin,
		filepath.Join(root, "atlas"),
		files,
		[]string{"HOME=" + filepath.Join(root, "atlas-home")},
		dirFormat,
	)
	if err != nil {
		return fileTxModePair{}, fmt.Errorf("prepare Atlas fixture: %w", err)
	}
	ptah, err := prepareFileTxModeRuntimeWithFormat(ptahBin, filepath.Join(root, "ptah"), files, nil, dirFormat)
	if err != nil {
		return fileTxModePair{}, fmt.Errorf("prepare Ptah fixture: %w", err)
	}

	atlasObservation, err := atlas.apply(applyArgs...)
	if err != nil {
		return fileTxModePair{}, fmt.Errorf("run Atlas apply: %w", err)
	}
	ptahObservation, err := ptah.apply(applyArgs...)
	if err != nil {
		return fileTxModePair{}, fmt.Errorf("run Ptah apply: %w", err)
	}
	return fileTxModePair{Atlas: atlasObservation, Ptah: ptahObservation}, nil
}

func prepareFileTxModeRuntime(
	bin, root string,
	files map[string]string,
	env []string,
) (fileTxModeRuntime, error) {
	return prepareFileTxModeRuntimeWithFormat(bin, root, files, env, "")
}

func prepareFileTxModeRuntimeWithFormat(
	bin, root string,
	files map[string]string,
	env []string,
	dirFormat string,
) (fileTxModeRuntime, error) {
	migrations := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrations, 0o750); err != nil {
		return fileTxModeRuntime{}, err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(migrations, name), []byte(content), 0o600); err != nil {
			return fileTxModeRuntime{}, err
		}
	}
	runtime := fileTxModeRuntime{
		bin:        bin,
		migrations: migrations,
		dbPath:     filepath.Join(root, "runtime.db"),
		env:        env,
		dirFormat:  dirFormat,
	}
	hash, err := runFileTxModeProcess(bin, []string{"migrate", "hash", "--dir", runtime.directoryURL()}, env)
	if err != nil {
		return fileTxModeRuntime{}, err
	}
	if hash.exitCode != 0 {
		return fileTxModeRuntime{}, fmt.Errorf("hash exited %d: %s", hash.exitCode, fileTxModeProcessDetail(hash))
	}
	return runtime, nil
}

func (runtime fileTxModeRuntime) directoryURL() string {
	value := fileURL(runtime.migrations)
	if runtime.dirFormat != "" {
		value += "?format=" + runtime.dirFormat
	}
	return value
}

func (runtime fileTxModeRuntime) apply(extraArgs ...string) (fileTxModeObservation, error) {
	args := []string{"migrate", "apply"}
	args = append(args, extraArgs...)
	args = append(args,
		"--url", sqliteURL(runtime.dbPath),
		"--dir", runtime.directoryURL(),
		"--revisions-schema", "main",
	)
	process, err := runFileTxModeProcess(runtime.bin, args, runtime.env)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	tables, revisions, err := inspectFileTxModeState(runtime.dbPath)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	return fileTxModeObservation{Process: process, Tables: tables, Revisions: revisions}, nil
}

func (runtime fileTxModeRuntime) down(toVersion string) (fileTxModeObservation, error) {
	process, err := runFileTxModeProcess(runtime.bin, []string{
		"migrate", "down",
		"--url", sqliteURL(runtime.dbPath),
		"--dir", runtime.directoryURL(),
		"--revisions-schema", "main",
		"--to-version", toVersion,
	}, runtime.env)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	tables, revisions, err := inspectFileTxModeState(runtime.dbPath)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	return fileTxModeObservation{Process: process, Tables: tables, Revisions: revisions}, nil
}

func runFileTxModeProcess(bin string, args, env []string) (integrityProcessResult, error) {
	stdout, stderr, err := commandStreamsWithEnv(bin, args, "", env)
	result := integrityProcessResult{stdout: stdout, stderr: stderr}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result, nil
	}
	return integrityProcessResult{}, err
}

func inspectFileTxModeState(dbPath string) ([]string, []projectConfigRevisionMetadata, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	db, err := openSQLiteRuntimeDB(dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = db.Close() }()
	tables, err := sqliteTableNames(db)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Contains(tables, "atlas_schema_revisions") {
		return tables, nil, nil
	}
	revisions, err := projectConfigRevisionMetadataFacts(db)
	if err != nil {
		return nil, nil, err
	}
	return tables, revisions, nil
}

func compareFileTxModeMatrixCase(
	fixture string,
	tc fileTxModeMatrixCase,
	pair fileTxModePair,
) Result {
	if detail, issue := compareFileTxModeMatrixObservation(
		"Atlas CE",
		tc,
		pair.Atlas,
		tc.AtlasExpected,
	); detail != "" {
		return fileTxModeGapForIssue(fixture, "atlas-oracle", detail, issue)
	}
	if detail, issue := compareFileTxModeMatrixObservation(
		"Ptah",
		tc,
		pair.Ptah,
		tc.PtahExpected,
	); detail != "" {
		return fileTxModeGapForIssue(fixture, "ptah", detail, issue)
	}
	if tc.IntentionalDivergence != "" {
		return Result{
			migrateRuntimeProbeName,
			fixture,
			fileTxModePtahBetterStage,
			OK,
			fmt.Sprintf(
				"%s; measured state: Atlas body=%t/revisions=%d, Ptah body=%t/revisions=%d",
				tc.IntentionalDivergence,
				tableExists(pair.Atlas.Tables, fileTxModeBodyTable),
				len(pair.Atlas.Revisions),
				tableExists(pair.Ptah.Tables, fileTxModeBodyTable),
				len(pair.Ptah.Revisions),
			),
			tc.Issue,
		}
	}
	if tableExists(pair.Atlas.Tables, fileTxModeBodyTable) != tableExists(pair.Ptah.Tables, fileTxModeBodyTable) {
		return fileTxModeGapForIssue(fixture, "compare", "Atlas CE and Ptah left different body table state", fileTxModeMatrixIssue(tc))
	}
	directive := tc.DirectiveLabel
	if directive == "" {
		directive = strings.TrimSpace(strings.TrimPrefix(tc.Directive, "-- atlas:txmode"))
	}
	if directive == "" {
		directive = "absent"
	}
	return Result{migrateRuntimeProbeName, fixture, "compare", OK,
		fmt.Sprintf("global %s plus file directive %s matched Atlas CE v1.3 on exit, validation, and SQLite body state", tc.GlobalMode, directive), ""}
}

func compareFileTxModeMatrixObservation(
	label string,
	tc fileTxModeMatrixCase,
	observation fileTxModeObservation,
	expected fileTxModeExpectedState,
) (string, string) {
	issue := fileTxModeMatrixIssue(tc)
	if observation.Process.exitCode != 1 {
		return fmt.Sprintf("%s exit = %d, want 1: %s", label, observation.Process.exitCode, fileTxModeProcessDetail(observation.Process)), issue
	}
	if tc.WantError != "" {
		wantTables := []string{"atlas_schema_revisions"}
		if !slices.Equal(observation.Tables, wantTables) {
			return fmt.Sprintf("%s tables after rejecting the directive = %v, want %v", label, observation.Tables, wantTables), issue
		}
		if len(observation.Revisions) != 0 {
			return fmt.Sprintf("%s wrote %d revision row(s) before rejecting the directive", label, len(observation.Revisions)), issue
		}
		if got := fileTxModeStableError(observation.Process.stderr); got != tc.WantError {
			if label == "Ptah" && got == "error applying migrations: "+tc.WantError {
				issue = fileTxModeDiagnosticIssue
			}
			return fmt.Sprintf("%s diagnostic = %q, want %q", label, got, tc.WantError), issue
		}
		return "", ""
	}
	if !strings.Contains(observation.Process.stdout+observation.Process.stderr, fileTxModeMissingTable) {
		return label + " did not reach the deliberately failing body statement", issue
	}
	if got := tableExists(observation.Tables, fileTxModeBodyTable); got != expected.BodyTable {
		return fmt.Sprintf("%s body table exists = %t, want %t", label, got, expected.BodyTable), issue
	}
	if expected.Tables != nil && !slices.Equal(observation.Tables, expected.Tables) {
		return fmt.Sprintf(
			"%s tables = %v, want %v",
			label,
			observation.Tables,
			expected.Tables,
		), issue
	}
	if expected.Revisions != nil {
		got := fileTxModeRevisionFacts(observation.Revisions)
		if !slices.Equal(got, expected.Revisions) {
			return fmt.Sprintf("%s revision facts = %v, want %v", label, got, expected.Revisions), issue
		}
	}
	return "", ""
}

func fileTxModeRevisionFacts(revisions []projectConfigRevisionMetadata) []fileTxModeRevisionFact {
	facts := make([]fileTxModeRevisionFact, len(revisions))
	for i, revision := range revisions {
		facts[i] = fileTxModeRevisionFact{
			Version:              revision.Version,
			Description:          revision.Description,
			Type:                 revision.Type,
			Applied:              revision.Applied,
			Total:                revision.Total,
			ErrorIsNull:          revision.ErrorIsNull,
			ErrorStatementIsNull: revision.ErrorStatementIsNull,
			ErrorStatement:       revision.ErrorStatement,
			OperatorVersion:      revision.OperatorVersion,
		}
	}
	return facts
}

func fileTxModeMatrixIssue(tc fileTxModeMatrixCase) string {
	if tc.Issue != "" {
		return tc.Issue
	}
	return fileTxModeIssue
}

func compareFileTxModeBookkeeping(observations map[string]fileTxModePair) Result {
	const fixture = "sqlite/per-file-txmode/revision-bookkeeping"
	var differences []string
	differingCases := 0
	for _, name := range fileTxModeBookkeepingCaseNames() {
		pair, ok := observations[name]
		if !ok {
			differingCases++
			differences = append(differences, name+": observation unavailable")
			continue
		}
		atlas := stableProjectConfigRevisions(pair.Atlas.Revisions)
		ptah := stableProjectConfigRevisions(pair.Ptah.Revisions)
		caseDifferences := projectConfigRevisionDifferences(name, atlas, ptah)
		if len(caseDifferences) == 0 {
			continue
		}
		differingCases++
		differences = append(differences, caseDifferences...)
		if len(atlas) == 0 && len(ptah) != 0 {
			differences = append(differences, name+" Ptah extra row "+fileTxModeRevisionSummary(ptah[0]))
		}
	}
	if len(differences) == 0 {
		return Result{migrateRuntimeProbeName, fixture, "compare", OK,
			"all seven body-execution cells match Atlas CE full stable revision metadata", ""}
	}
	return Result{migrateRuntimeProbeName, fixture, "compare", Gap,
		fmt.Sprintf("full stable revision metadata differs in %d/7 body-execution cells: %s", differingCases, strings.Join(differences, "; ")),
		fileTxModeBookkeepingIssue}
}

func fileTxModeBookkeepingCaseNames() []string {
	return []string{
		"global-file-directive-absent",
		"global-file-directive-file",
		"global-file-directive-none",
		"global-all-directive-absent",
		"global-none-directive-absent",
		"global-none-directive-file",
		"global-none-directive-none",
	}
}

func fileTxModeRevisionSummary(revision projectConfigStableRevisionMetadata) string {
	return fmt.Sprintf(
		"{version=%q description=%q type=%d applied=%d total=%d executed_at_type=%q error_null=%t error=%q error_type=%q error_stmt_null=%t error_stmt=%q error_stmt_type=%q hash=%q partial_hashes_null=%t partial_hashes=%q partial_hashes_type=%q}",
		revision.Version,
		revision.Description,
		revision.Type,
		revision.Applied,
		revision.Total,
		revision.ExecutedAtStorageClass,
		revision.ErrorIsNull,
		revision.Error,
		revision.ErrorStorageClass,
		revision.ErrorStatementIsNull,
		revision.ErrorStatement,
		revision.ErrorStatementStorageClass,
		revision.Hash,
		revision.PartialHashesIsNull,
		revision.PartialHashes,
		revision.PartialHashesStorageClass,
	)
}

func sqliteMigrateFileTxModeSelectionOracle(ptahBin, atlasBin string) []Result {
	return []Result{
		fileTxModeAmountSelectionOracle(ptahBin, atlasBin, "file", true),
		fileTxModeAmountSelectionOracle(ptahBin, atlasBin, "all", false),
		fileTxModeBaselineSelectionOracle(ptahBin, atlasBin),
	}
}

func fileTxModeAmountSelectionOracle(ptahBin, atlasBin, globalMode string, retry bool) Result {
	fixture := "sqlite/per-file-txmode/selection/amount-one-global-" + globalMode
	root, err := os.MkdirTemp("", "migrate-runtime-file-txmode-selection-*")
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	files := map[string]string{
		"1_valid.sql":   "CREATE TABLE first_valid (id INTEGER PRIMARY KEY);\n",
		"2_invalid.sql": "-- atlas:txmode bogus\n\nCREATE TABLE second_invalid (id INTEGER PRIMARY KEY);\n",
	}
	atlas, ptah, err := prepareFileTxModePair(ptahBin, atlasBin, root, files)
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	atlasFirst, err := atlas.apply("1", "--tx-mode", globalMode)
	if err != nil {
		return fileTxModeFailure(fixture, "atlas-apply", err)
	}
	ptahFirst, err := ptah.apply("1", "--tx-mode", globalMode)
	if err != nil {
		return fileTxModeFailure(fixture, "ptah-apply", err)
	}
	if detail := compareSuccessfulFileTxModeSelection(atlasFirst, []string{"atlas_schema_revisions", "first_valid"}, []string{"1"}); detail != "" {
		return fileTxModeGap(fixture, "atlas-oracle", detail)
	}
	if detail := compareSuccessfulFileTxModeSelection(ptahFirst, []string{"atlas_schema_revisions", "first_valid"}, []string{"1"}); detail != "" {
		return fileTxModeGap(fixture, "ptah", detail)
	}
	if retry {
		atlasRetry, atlasErr := atlas.apply("--tx-mode", globalMode)
		if atlasErr != nil {
			return fileTxModeFailure(fixture, "atlas-retry", atlasErr)
		}
		ptahRetry, ptahErr := ptah.apply("--tx-mode", globalMode)
		if ptahErr != nil {
			return fileTxModeFailure(fixture, "ptah-retry", ptahErr)
		}
		const want = `unknown txmode "bogus" found in file directive "2_invalid.sql"`
		if detail, issue := compareRejectedFileTxModeSelection("Atlas CE", atlasRetry, atlasFirst, want); detail != "" {
			return fileTxModeGapForIssue(fixture, "atlas-retry", detail, issue)
		}
		if detail, issue := compareRejectedFileTxModeSelection("Ptah", ptahRetry, ptahFirst, want); detail != "" {
			return fileTxModeGapForIssue(fixture, "ptah-retry", detail, issue)
		}
	}
	return Result{migrateRuntimeProbeName, fixture, "compare", OK,
		"amount 1 validated only the selected first migration under global " + globalMode + "; the later invalid directive remained untouched", ""}
}

func fileTxModeBaselineSelectionOracle(ptahBin, atlasBin string) Result {
	const fixture = "sqlite/per-file-txmode/selection/baseline-two"
	root, err := os.MkdirTemp("", "migrate-runtime-file-txmode-baseline-*")
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	files := map[string]string{
		"1_first.sql":   "CREATE TABLE first_skipped (id INTEGER PRIMARY KEY);\n",
		"2_invalid.sql": "-- atlas:txmode bogus\n\nCREATE TABLE second_skipped (id INTEGER PRIMARY KEY);\n",
		"3_third.sql":   "CREATE TABLE third_applied (id INTEGER PRIMARY KEY);\n",
	}
	atlas, ptah, err := prepareFileTxModePair(ptahBin, atlasBin, root, files)
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	atlasObservation, err := atlas.apply("--baseline", "2", "--tx-mode", "file")
	if err != nil {
		return fileTxModeFailure(fixture, "atlas-apply", err)
	}
	ptahObservation, err := ptah.apply("--baseline", "2", "--tx-mode", "file")
	if err != nil {
		return fileTxModeFailure(fixture, "ptah-apply", err)
	}
	wantTables := []string{"atlas_schema_revisions", "third_applied"}
	wantVersions := []string{"2", "3"}
	if detail := compareSuccessfulFileTxModeSelection(atlasObservation, wantTables, wantVersions); detail != "" {
		return fileTxModeGap(fixture, "atlas-oracle", detail)
	}
	if detail := compareSuccessfulFileTxModeSelection(ptahObservation, wantTables, wantVersions); detail != "" {
		return fileTxModeGap(fixture, "ptah", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "compare", OK,
		"baseline 2 skipped validation and execution of the invalid baseline migration while both binaries applied version 3", ""}
}

func prepareFileTxModePair(
	ptahBin, atlasBin, root string,
	files map[string]string,
) (fileTxModeRuntime, fileTxModeRuntime, error) {
	return prepareFileTxModePairWithFormat(ptahBin, atlasBin, root, files, "")
}

func prepareFileTxModePairWithFormat(
	ptahBin, atlasBin, root string,
	files map[string]string,
	dirFormat string,
) (fileTxModeRuntime, fileTxModeRuntime, error) {
	atlas, err := prepareFileTxModeRuntimeWithFormat(
		atlasBin,
		filepath.Join(root, "atlas"),
		files,
		[]string{"HOME=" + filepath.Join(root, "atlas-home")},
		dirFormat,
	)
	if err != nil {
		return fileTxModeRuntime{}, fileTxModeRuntime{}, fmt.Errorf("prepare Atlas fixture: %w", err)
	}
	ptah, err := prepareFileTxModeRuntimeWithFormat(ptahBin, filepath.Join(root, "ptah"), files, nil, dirFormat)
	if err != nil {
		return fileTxModeRuntime{}, fileTxModeRuntime{}, fmt.Errorf("prepare Ptah fixture: %w", err)
	}
	return atlas, ptah, nil
}

func compareSuccessfulFileTxModeSelection(
	observation fileTxModeObservation,
	wantTables, wantVersions []string,
) string {
	if observation.Process.exitCode != 0 {
		return "apply failed: " + fileTxModeProcessDetail(observation.Process)
	}
	if !slices.Equal(observation.Tables, wantTables) {
		return fmt.Sprintf("SQLite tables = %v, want %v", observation.Tables, wantTables)
	}
	if got := fileTxModeRevisionVersions(observation.Revisions); !slices.Equal(got, wantVersions) {
		return fmt.Sprintf("revision versions = %v, want %v", got, wantVersions)
	}
	return ""
}

func sqliteMigrateSplitFileTxModeOracle(ptahBin, atlasBin string) []Result {
	return []Result{
		fileTxModeSplitFileUpOracle(ptahBin, atlasBin),
		fileTxModeSplitFileDownControl(ptahBin, atlasBin),
	}
}

func fileTxModeSplitFileUpOracle(ptahBin, atlasBin string) Result {
	const fixture = "sqlite/per-file-txmode/plain-split-up"
	pair, err := runFileTxModePairWithFormat(ptahBin, atlasBin, map[string]string{
		"1_case.up.sql":   "-- atlas:txmode none\n\n" + fileTxModeFailingBody,
		"1_case.down.sql": "DROP TABLE " + fileTxModeBodyTable + ";\n",
	}, []string{"--tx-mode", "file"}, "golang-migrate")
	if err != nil {
		return fileTxModeFailure(fixture, "execute", err)
	}
	if pair.Atlas.Process.exitCode != 1 || pair.Ptah.Process.exitCode != 1 {
		return fileTxModeGap(fixture, "apply", "both binaries must reach the deliberately failing split up body")
	}
	if !strings.Contains(pair.Atlas.Process.stdout+pair.Atlas.Process.stderr, fileTxModeMissingTable) {
		return fileTxModeGap(fixture, "atlas-oracle", "Atlas CE did not reach the deliberately failing split up statement")
	}
	if !strings.Contains(pair.Ptah.Process.stdout+pair.Ptah.Process.stderr, fileTxModeMissingTable) {
		return fileTxModeGap(fixture, "ptah", "Ptah did not reach the deliberately failing split up statement")
	}
	if !slices.Equal(pair.Atlas.Tables, []string{"atlas_schema_revisions"}) {
		return fileTxModeGap(fixture, "atlas-oracle", fmt.Sprintf(
			"Atlas CE tables = %v, want only the revision table after rolling back the converted .up.sql migration",
			pair.Atlas.Tables,
		))
	}
	if len(pair.Atlas.Revisions) != 0 {
		return fileTxModeGap(fixture, "atlas-oracle", fmt.Sprintf(
			"Atlas CE left %d revision row(s), want 0 after rolling back the converted .up.sql migration",
			len(pair.Atlas.Revisions),
		))
	}
	wantPtahTables := []string{"atlas_schema_revisions", fileTxModeBodyTable}
	if !slices.Equal(pair.Ptah.Tables, wantPtahTables) {
		return fileTxModeGap(fixture, "ptah", fmt.Sprintf(
			"Ptah tables = %v, want %v after honoring the converted .up.sql txmode directive",
			pair.Ptah.Tables,
			wantPtahTables,
		))
	}
	wantPtahRevisions := []fileTxModeRevisionFact{
		{
			Version:         "1",
			Description:     "case",
			Type:            2,
			Applied:         1,
			Total:           2,
			ErrorStatement:  "INSERT INTO txmode_missing (id) VALUES (1);",
			OperatorVersion: "Ptah",
		},
	}
	if got := fileTxModeRevisionFacts(pair.Ptah.Revisions); !slices.Equal(got, wantPtahRevisions) {
		return fileTxModeGap(fixture, "ptah", fmt.Sprintf(
			"Ptah revision facts = %v, want %v after honoring the converted .up.sql txmode directive",
			got,
			wantPtahRevisions,
		))
	}
	return Result{migrateRuntimeProbeName, fixture, fileTxModePtahBetterStage, OK,
		"Atlas CE v1.3 discarded the explicit source .up.sql txmode directive during golang-migrate format conversion and rolled back the body (0 revision rows); Ptah preserved the directive, kept the successful statement, and recorded the failed nontransactional migration (1 revision row)",
		"stokaro/ptah#1082"}
}

func fileTxModeSplitFileDownControl(ptahBin, atlasBin string) Result {
	const fixture = "sqlite/per-file-txmode/plain-split-down"
	root, err := os.MkdirTemp("", "migrate-runtime-file-txmode-split-down-*")
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	files := map[string]string{
		"1_case.up.sql": "CREATE TABLE split_down_first (id INTEGER PRIMARY KEY);\n" +
			"CREATE TABLE split_down_second (id INTEGER PRIMARY KEY);\n",
		"1_case.down.sql": "-- atlas:txmode none\n\n" +
			"DROP TABLE split_down_second;\nTHIS IS A SPLIT DOWN FAILURE;\n",
	}
	atlas, ptah, err := prepareFileTxModePairWithFormat(ptahBin, atlasBin, root, files, "golang-migrate")
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	atlasUp, err := atlas.apply("--tx-mode", "file")
	if err != nil {
		return fileTxModeFailure(fixture, "atlas-up", err)
	}
	ptahUp, err := ptah.apply("--tx-mode", "file")
	if err != nil {
		return fileTxModeFailure(fixture, "ptah-up", err)
	}
	wantUpTables := []string{"atlas_schema_revisions", "split_down_first", "split_down_second"}
	if detail := compareSuccessfulFileTxModeSelection(atlasUp, wantUpTables, []string{"1"}); detail != "" {
		return fileTxModeGap(fixture, "atlas-up", detail)
	}
	if detail := compareSuccessfulFileTxModeSelection(ptahUp, wantUpTables, []string{"1"}); detail != "" {
		return fileTxModeGap(fixture, "ptah-up", detail)
	}
	atlasDown, err := runFileTxModeCEDownBoundary(atlasBin)
	if err != nil {
		return fileTxModeFailure(fixture, "atlas-down-boundary", err)
	}
	if detail := requireFileTxModeCEDownBoundary(atlasDown); detail != "" {
		return fileTxModeGap(fixture, "atlas-down-boundary", detail)
	}
	ptahDown, err := ptah.down("0")
	if err != nil {
		return fileTxModeFailure(fixture, "ptah-down", err)
	}
	if ptahDown.Process.exitCode != 1 {
		return fileTxModeGap(fixture, "ptah-down", "Ptah did not reach the deliberately failing down body")
	}
	if detail := comparePreservedFileTxModeState(ptahUp, ptahDown); detail != "" {
		return fileTxModeGap(fixture, "ptah-down-state", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "ptah-control", OK,
		"Atlas CE v1.3 applied the split up migration, but its community migrate down command rejects execution before runtime flags can be supplied; the Ptah-side live control discarded the source .down.sql directive during golang-migrate conversion and rolled back the failing down without changing tables or stable revision metadata", ""}
}

func sqliteNativeNoTransactionOracle(nativeBin string) Result {
	const fixture = "sqlite/per-file-txmode/native-no-transaction"
	root, err := os.MkdirTemp("", "migrate-runtime-native-no-transaction-*")
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	nonTransactional, err := runNativeNoTransactionCase(nativeBin, filepath.Join(root, "none"), "-- +ptah no_transaction\n\n")
	if err != nil {
		return fileTxModeFailure(fixture, "none", err)
	}
	transactional, err := runNativeNoTransactionCase(nativeBin, filepath.Join(root, "control"), "")
	if err != nil {
		return fileTxModeFailure(fixture, "control", err)
	}
	if nonTransactional.Process.exitCode != 2 || transactional.Process.exitCode != 2 ||
		!strings.Contains(nonTransactional.Process.stdout+nonTransactional.Process.stderr, fileTxModeMissingTable) ||
		!strings.Contains(transactional.Process.stdout+transactional.Process.stderr, fileTxModeMissingTable) {
		return fileTxModeGap(fixture, "execute", "both native files must reach the deliberately failing statement: no_transaction="+
			fileTxModeProcessDetail(nonTransactional.Process)+" control="+fileTxModeProcessDetail(transactional.Process))
	}
	if !tableExists(nonTransactional.Tables, fileTxModeBodyTable) {
		return fileTxModeGap(fixture, "none", "native +ptah no_transaction did not preserve the successful first statement")
	}
	if tableExists(transactional.Tables, fileTxModeBodyTable) {
		return fileTxModeGap(fixture, "control", "native transactional control did not roll back the successful first statement")
	}
	return Result{migrateRuntimeProbeName, fixture, "ptah-control", OK,
		"native -- +ptah no_transaction preserved the successful statement before failure while the identical transactional control rolled it back", ""}
}

func runNativeNoTransactionCase(nativeBin, root, directive string) (fileTxModeObservation, error) {
	migrations := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrations, 0o750); err != nil {
		return fileTxModeObservation{}, err
	}
	if err := os.WriteFile(filepath.Join(migrations, "0000000001_case.up.sql"), []byte(directive+fileTxModeFailingBody), 0o600); err != nil {
		return fileTxModeObservation{}, err
	}
	if err := os.WriteFile(filepath.Join(migrations, "0000000001_case.down.sql"), []byte("DROP TABLE "+fileTxModeBodyTable+";\n"), 0o600); err != nil {
		return fileTxModeObservation{}, err
	}
	hash, err := runFileTxModeProcess(nativeBin, []string{
		"migrations", "hash", "--dir", migrations, "--dir-format", "ptah",
	}, nil)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	if hash.exitCode != 0 {
		return fileTxModeObservation{}, fmt.Errorf("native hash exited %d: %s", hash.exitCode, fileTxModeProcessDetail(hash))
	}
	dbPath := filepath.Join(root, "runtime.db")
	process, err := runFileTxModeProcess(nativeBin, []string{
		"migrations", "up",
		"--db-url", sqliteURL(dbPath),
		"--migrations-dir", migrations,
		"--dir-format", "ptah",
	}, nil)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	tables, revisions, err := inspectFileTxModeState(dbPath)
	if err != nil {
		return fileTxModeObservation{}, err
	}
	return fileTxModeObservation{Process: process, Tables: tables, Revisions: revisions}, nil
}

func compareRejectedFileTxModeSelection(
	label string,
	observation fileTxModeObservation,
	before fileTxModeObservation,
	wantError string,
) (string, string) {
	if observation.Process.exitCode != 1 {
		return fmt.Sprintf("%s exit = %d, want 1", label, observation.Process.exitCode), fileTxModeIssue
	}
	if detail := comparePreservedFileTxModeState(before, observation); detail != "" {
		return label + " " + detail, fileTxModeIssue
	}
	if got := fileTxModeStableError(observation.Process.stderr); got != wantError {
		issue := fileTxModeIssue
		if label == "Ptah" && got == "error applying migrations: "+wantError {
			issue = fileTxModeDiagnosticIssue
		}
		return fmt.Sprintf("%s diagnostic = %q, want %q", label, got, wantError), issue
	}
	return "", ""
}

func sqliteMigrateTxtarFileTxModeOracle(ptahBin, atlasBin string) []Result {
	return []Result{
		fileTxModeTxtarUpOracle(ptahBin, atlasBin),
		fileTxModeTxtarDownOracle(ptahBin),
	}
}

func fileTxModeTxtarUpOracle(ptahBin, atlasBin string) Result {
	const fixture = "sqlite/per-file-txmode/txtar-up-extension"
	const migration = `-- atlas:txtar

-- migration.sql --
-- atlas:txmode none

CREATE TABLE txtar_up_partial (id INTEGER PRIMARY KEY);
INSERT INTO txtar_up_missing (id) VALUES (1);

-- down.sql --
DROP TABLE txtar_up_partial;
`
	pair, err := runFileTxModePair(ptahBin, atlasBin, map[string]string{"1_txtar.sql": migration}, []string{"--tx-mode", "file"})
	if err != nil {
		return fileTxModeFailure(fixture, "execute", err)
	}
	if pair.Atlas.Process.exitCode != 1 || pair.Ptah.Process.exitCode != 1 {
		return fileTxModeGap(fixture, "apply", "both binaries must reach the deliberately failing txtar up body")
	}
	if tableExists(pair.Atlas.Tables, "txtar_up_partial") {
		return fileTxModeGap(fixture, "atlas-oracle", "Atlas CE unexpectedly interpreted the section-local txmode directive")
	}
	if !tableExists(pair.Ptah.Tables, "txtar_up_partial") {
		return fileTxModeGap(fixture, "ptah", "Ptah did not apply the migration.sql section-local none mode")
	}
	return Result{migrateRuntimeProbeName, fixture, "classify", OK,
		"Atlas CE v1.3 ignored section-local txtar txmode and rolled back the up body; Ptah intentionally extends txtar by applying migration.sql mode independently", ""}
}

func fileTxModeTxtarDownOracle(ptahBin string) Result {
	const fixture = "sqlite/per-file-txmode/txtar-down-extension"
	const migration = `-- atlas:txtar

-- migration.sql --
-- atlas:txmode file

CREATE TABLE txtar_down_first (id INTEGER PRIMARY KEY);
CREATE TABLE txtar_down_second (id INTEGER PRIMARY KEY);

-- down.sql --
-- atlas:txmode none

DROP TABLE txtar_down_second;
THIS IS A TXMODE DOWN FAILURE;
`
	root, err := os.MkdirTemp("", "migrate-runtime-file-txmode-txtar-down-*")
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	ptah, err := prepareFileTxModeRuntime(ptahBin, filepath.Join(root, "ptah"), map[string]string{"1_txtar.sql": migration}, nil)
	if err != nil {
		return fileTxModeFailure(fixture, "setup", err)
	}
	ptahUp, err := ptah.apply("--tx-mode", "file")
	if err != nil {
		return fileTxModeFailure(fixture, "ptah-up", err)
	}
	wantUpTables := []string{"atlas_schema_revisions", "txtar_down_first", "txtar_down_second"}
	if detail := compareSuccessfulFileTxModeSelection(ptahUp, wantUpTables, []string{"1"}); detail != "" {
		return fileTxModeGap(fixture, "ptah-up", detail)
	}
	ptahDown, err := ptah.down("0")
	if err != nil {
		return fileTxModeFailure(fixture, "ptah-down", err)
	}
	if ptahDown.Process.exitCode != 1 {
		return fileTxModeGap(fixture, "ptah-down", "Ptah did not reach the deliberately failing txtar down body")
	}
	wantDownTables := []string{"atlas_schema_revisions", "txtar_down_first"}
	if !slices.Equal(ptahDown.Tables, wantDownTables) {
		return fileTxModeGap(fixture, "ptah-down-state", fmt.Sprintf("Ptah tables = %v, want %v", ptahDown.Tables, wantDownTables))
	}
	if detail := comparePreservedFileTxModeRevisions(ptahUp, ptahDown); detail != "" {
		return fileTxModeGap(fixture, "ptah-down-state", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "ptah-control", OK,
		"Ptah-side live evidence proved migration.sql=file and down.sql=none remain independent: the successful first down statement persisted, the failing statement stopped execution, and stable revision metadata remained unchanged", ""}
}

func requireFileTxModeCEDownBoundary(observation fileTxModeObservation) string {
	if observation.Process.exitCode != 1 {
		return fmt.Sprintf("Atlas CE migrate down exit = %d, want community abort exit 1", observation.Process.exitCode)
	}
	if !ceCommunityAbortPattern.MatchString(observation.Process.stderr) {
		return "Atlas CE migrate down did not preserve the measured community-version boundary: " +
			fileTxModeProcessDetail(observation.Process)
	}
	return ""
}

func runFileTxModeCEDownBoundary(atlasBin string) (fileTxModeObservation, error) {
	home, err := os.MkdirTemp("", "migrate-runtime-atlas-down-boundary-*")
	if err != nil {
		return fileTxModeObservation{}, err
	}
	defer func() { _ = os.RemoveAll(home) }()
	process, err := runFileTxModeProcess(atlasBin, []string{"migrate", "down"}, []string{"HOME=" + home})
	if err != nil {
		return fileTxModeObservation{}, err
	}
	return fileTxModeObservation{Process: process}, nil
}

func fileTxModeRevisionVersions(revisions []projectConfigRevisionMetadata) []string {
	versions := make([]string, len(revisions))
	for i, revision := range revisions {
		versions[i] = revision.Version
	}
	return versions
}

func comparePreservedFileTxModeState(before, after fileTxModeObservation) string {
	if !slices.Equal(after.Tables, before.Tables) {
		return fmt.Sprintf("changed SQLite tables: before=%v after=%v", before.Tables, after.Tables)
	}
	return comparePreservedFileTxModeRevisions(before, after)
}

func comparePreservedFileTxModeRevisions(before, after fileTxModeObservation) string {
	want := stableProjectConfigRevisions(before.Revisions)
	got := stableProjectConfigRevisions(after.Revisions)
	if !slices.Equal(got, want) {
		return fmt.Sprintf("changed stable revision metadata: before=%v after=%v", want, got)
	}
	return ""
}

func fileTxModeStableError(stderr string) string {
	for line := range strings.SplitSeq(stderr, "\n") {
		if value, ok := strings.CutPrefix(line, "Error: "); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func fileTxModeProcessDetail(result integrityProcessResult) string {
	return fmt.Sprintf("{exit=%d stdout=%q stderr=%q}", result.exitCode, oneLine(result.stdout), oneLine(result.stderr))
}

func tableExists(tables []string, name string) bool {
	return slices.Contains(tables, name)
}

func fileTxModeGap(fixture, stage, detail string) Result {
	return fileTxModeGapForIssue(fixture, stage, detail, fileTxModeIssue)
}

func fileTxModeGapForIssue(fixture, stage, detail, issue string) Result {
	return Result{migrateRuntimeProbeName, fixture, stage, Gap, detail, issue}
}

func fileTxModeFailure(fixture, stage string, err error) Result {
	return Result{migrateRuntimeProbeName, fixture, stage, Fail, err.Error(), fileTxModeIssue}
}
