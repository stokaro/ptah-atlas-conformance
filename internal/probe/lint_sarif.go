package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// sarifReport is the subset of the SARIF 2.1.0 shape the fidelity matrix asserts
// Ptah's `migrations lint --format sarif` output conforms to. CI systems ingest
// this document, so its shape is part of analyzer fidelity.
type sarifReport struct {
	Version string `json:"version"`
	Runs    []struct {
		Tool struct {
			Driver struct {
				Name  string `json:"name"`
				Rules []struct {
					ID string `json:"id"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID    string `json:"ruleId"`
			Level     string `json:"level"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine int `json:"startLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

// lintSarifShape asserts that `ptah migrations lint --format sarif` emits a
// well-formed SARIF 2.1.0 document: a named driver, and a result carrying a
// ruleId, a level, and a physical location (file + line). A regression in any of
// these — a dropped ruleId, a missing region, malformed JSON — turns this red,
// which is the "deliberately removed SARIF behavior makes the gate red" guarantee.
func lintSarifShape(bin string) Result {
	const fixture = "fidelity: sarif output shape"
	dir, err := os.MkdirTemp("", "lint-sarif-*")
	if err != nil {
		return migrateRuntimeFail(fixture, "setup", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	files := map[string]string{
		"0000000001_drop.up.sql":   "DROP TABLE users;\n",
		"0000000001_drop.down.sql": "-- irreversible for the probe\n",
	}
	for name, sql := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600); err != nil {
			return migrateRuntimeFail(fixture, "setup", err)
		}
	}

	// --fail-on none keeps the exit code 0 so CombinedOutput is just the report.
	output, err := commandOutput(bin, []string{
		"migrations", "lint", "--dir", dir, "--dialect", "postgres", "--format", "sarif", "--fail-on", "none",
	})
	if err != nil {
		return migrateRuntimeExit(fixture, "lint", output, err)
	}

	report, perr := parseSarif(output)
	if perr != nil {
		return migrateRuntimeGap(fixture, "parse", "SARIF output is not valid JSON: "+oneLine(perr.Error()))
	}
	if detail := validateSarifShape(report); detail != "" {
		return migrateRuntimeGap(fixture, "shape", detail)
	}
	return Result{migrateRuntimeProbeName, fixture, "shape", OK,
		"lint --format sarif emits SARIF 2.1.0 with a named driver and a result carrying ruleId, level, and a file:line location", ""}
}

// parseSarif extracts the SARIF object from the command output, tolerating any
// leading/trailing non-JSON noise by slicing to the outermost braces.
func parseSarif(output string) (*sarifReport, error) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start >= 0 && end > start {
		output = output[start : end+1]
	}
	var report sarifReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func validateSarifShape(report *sarifReport) string {
	if report.Version != "2.1.0" {
		return "expected SARIF version 2.1.0, got " + oneLine(report.Version)
	}
	if len(report.Runs) == 0 {
		return "SARIF document has no runs"
	}
	run := report.Runs[0]
	if strings.TrimSpace(run.Tool.Driver.Name) == "" {
		return "SARIF run is missing tool.driver.name"
	}
	if len(run.Results) == 0 {
		return "SARIF run reported no results for a migration that drops a table"
	}
	res := run.Results[0]
	switch {
	case strings.TrimSpace(res.RuleID) == "":
		return "SARIF result is missing ruleId"
	case strings.TrimSpace(res.Level) == "":
		return "SARIF result is missing level"
	case len(res.Locations) == 0:
		return "SARIF result is missing locations"
	case strings.TrimSpace(res.Locations[0].PhysicalLocation.ArtifactLocation.URI) == "":
		return "SARIF result location is missing the artifact URI"
	case res.Locations[0].PhysicalLocation.Region.StartLine == 0:
		return "SARIF result location is missing region.startLine"
	}
	return ""
}
