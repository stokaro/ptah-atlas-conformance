package probe

import (
	"bufio"
	"os"
	"strings"
)

// Waivers records gaps that are consciously tracked and must not fail the
// conformance gate yet. A waiver is keyed on (probe, fixture, stage).
type Waivers struct {
	byKey map[string]string // key -> reason
}

func waiverKey(probe, fixture, stage string) string {
	return probe + "\x00" + fixture + "\x00" + stage
}

// LoadWaivers reads the line-based waivers file. A missing file means no
// waivers, which is the strict default: every gap is red.
func LoadWaivers(path string) (*Waivers, error) {
	w := &Waivers{byKey: map[string]string{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return w, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		reason := strings.TrimSpace(strings.TrimPrefix(line,
			fields[0]+" "+fields[1]+" "+fields[2]))
		w.byKey[waiverKey(fields[0], fields[1], fields[2])] = reason
	}
	return w, sc.Err()
}

// Reason returns the waiver reason for a result and whether it is waived.
func (w *Waivers) Reason(r Result) (string, bool) {
	reason, ok := w.byKey[waiverKey(r.Probe, r.Fixture, r.Stage)]
	return reason, ok
}

// Unused reports waiver entries that no longer match any result — a stale
// waiver means a gap closed and the waiver should be deleted.
func (w *Waivers) Unused(results []Result) []string {
	live := map[string]bool{}
	for _, r := range results {
		live[waiverKey(r.Probe, r.Fixture, r.Stage)] = true
	}
	var stale []string
	for k := range w.byKey {
		if !live[k] {
			parts := strings.Split(k, "\x00")
			stale = append(stale, strings.Join(parts, " "))
		}
	}
	return stale
}
