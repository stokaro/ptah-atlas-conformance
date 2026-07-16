// Command gap-budget checks the generated conformance report against the
// repository's current allowed non-OK observation budget.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/stokaro/ptah-atlas-conformance/internal/probe"
)

func main() {
	reportFile := flag.String("report", "gaps.json", "generated JSON report")
	budgetFile := flag.String("budget", "gap-budget.txt", "allowed unwaived non-OK observation budget")
	waiverFile := flag.String("waivers", "waivers.txt", "waivers file")
	flag.Parse()

	results, err := loadResults(*reportFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	budget, err := loadBudget(*budgetFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	waivers, err := probe.LoadWaivers(*waiverFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load waivers:", err)
		os.Exit(2)
	}

	status := probe.CheckGapBudget(results, waivers, budget)
	for _, stale := range status.StaleWaivers {
		fmt.Fprintf(os.Stderr, "stale waiver (matches no finding, delete it): %s\n", stale)
	}
	if len(status.StaleWaivers) > 0 {
		os.Exit(1)
	}
	if status.OverBudget() {
		fmt.Fprintf(os.Stderr, "CONFORMANCE BUDGET: RED — %d unwaived non-OK observation(s), budget is %d\n", status.Unwaived, status.Budget)
		os.Exit(1)
	}
	fmt.Printf("CONFORMANCE BUDGET: GREEN — %d unwaived non-OK observation(s), budget is %d\n", status.Unwaived, status.Budget)
}

func loadResults(path string) ([]probe.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var results []probe.Result
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return results, nil
}

func loadBudget(path string) (probe.GapBudget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read budget: %w", err)
	}
	budget, err := probe.ParseGapBudget(data)
	if err != nil {
		return 0, err
	}
	return budget, nil
}
