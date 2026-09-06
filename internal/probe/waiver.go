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
		probe, fixture, stage, reason, ok := splitWaiver(line)
		if !ok {
			continue
		}
		w.byKey[waiverKey(probe, fixture, stage)] = reason
	}
	return w, sc.Err()
}

// splitWaiver reads the three key fields and the reason from one line.
//
// A bare `strings.Fields` cannot address most of the corpus: a fixture is
// `atlas schema inspect -s` and a stage is `HTML report`, so the key's own
// separator occurs inside the key. Taking the first three whitespace-separated
// tokens silently matched something else, and the entry was then reported as a
// stale waiver -- a wrong key and a missing key are indistinguishable that way.
//
// So a field may be double-quoted, and an unquoted field is still a single
// token, which is what every existing entry relies on.
func splitWaiver(line string) (probe, fixture, stage, reason string, ok bool) {
	rest := strings.TrimSpace(line)
	fields := make([]string, 0, 3)
	for len(fields) < 3 {
		if rest == "" {
			return "", "", "", "", false
		}
		var field string
		if rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return "", "", "", "", false
			}
			field, rest = rest[1:1+end], rest[end+2:]
		} else {
			field, rest, _ = strings.Cut(rest, " ")
		}
		fields = append(fields, field)
		rest = strings.TrimLeft(rest, " \t")
	}
	return fields[0], fields[1], fields[2], strings.TrimSpace(rest), true
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
