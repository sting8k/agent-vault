package vault

import (
	"fmt"
	"sort"
	"strings"
)

// NotFoundError reports an unknown entry name and the closest existing names.
// It carries names only, never values. Callers add their own advice (for example,
// asking the user to run agv set) around it.
type NotFoundError struct {
	Name        string
	Suggestions []string // up to 3 existing names, closest first
}

func (e *NotFoundError) Error() string {
	if len(e.Suggestions) == 0 {
		return fmt.Sprintf("unknown secret %q", e.Name)
	}
	return fmt.Sprintf("unknown secret %q; closest: %s", e.Name, strings.Join(e.Suggestions, ", "))
}

func notFound(name string, f *file) *NotFoundError {
	names := make([]string, 0, len(f.Entries))
	for n := range f.Entries {
		names = append(names, n)
	}
	return &NotFoundError{Name: name, Suggestions: closest(name, names)}
}

// closest picks up to 3 names that are a typo away from name or contain it, nearest first.
func closest(name string, names []string) []string {
	name = strings.ToUpper(name)
	limit := max(2, len(name)/3)
	type cand struct {
		name string
		dist int
	}
	var cands []cand
	for _, n := range names {
		d := distance(name, n)
		related := len(name) >= 3 && len(n) >= 3 && (strings.Contains(n, name) || strings.Contains(name, n))
		if d <= limit || related {
			cands = append(cands, cand{n, d})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].name < cands[j].name
	})
	var out []string
	for _, c := range cands[:min(len(cands), 3)] {
		out = append(out, c.name)
	}
	return out
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
