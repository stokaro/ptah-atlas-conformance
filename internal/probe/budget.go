package probe

import (
	"fmt"
	"strconv"
	"strings"
)

// GapBudget is the maximum allowed count of unwaived non-OK observations.
type GapBudget int

// ParseGapBudget reads a line-oriented budget file. Blank lines and comments
// are ignored; the first remaining line must be a non-negative integer.
func ParseGapBudget(data []byte) (GapBudget, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return 0, fmt.Errorf("parse gap budget %q: %w", line, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("gap budget must be non-negative, got %d", n)
		}
		return GapBudget(n), nil
	}
	return 0, fmt.Errorf("gap budget file contains no budget")
}

// BudgetStatus is the result of comparing the current report with a budget.
type BudgetStatus struct {
	Unwaived     int
	Budget       GapBudget
	StaleWaivers []string
}

// CheckGapBudget counts unwaived results and stale waivers for CI progress
// gating. Full parity still requires CheckGapBudget(...).Unwaived == 0.
func CheckGapBudget(results []Result, w *Waivers, budget GapBudget) BudgetStatus {
	return BudgetStatus{
		Unwaived:     len(Unwaived(results, w)),
		Budget:       budget,
		StaleWaivers: w.Unused(results),
	}
}

// OverBudget reports whether the current report regressed beyond the allowed
// unwaived gap budget.
func (s BudgetStatus) OverBudget() bool {
	return s.Unwaived > int(s.Budget)
}
