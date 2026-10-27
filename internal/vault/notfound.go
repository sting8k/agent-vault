package vault

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// NotFoundError reports an unknown entry name and the existing names worth trying instead.
// Suggestions are stored names, never values. Callers add their own advice (for example, asking the user to run agv set) around it.
type NotFoundError struct {
	Name        string
	Suggestions []string // existing names, best first; see Suggest
	// With no suggestion: the deepest stored group of Name and what it holds one level down
	// ("DB", "PROD/"). Other secrets, shown so a missing environment is visible, never as a fix.
	Group   string
	InGroup []string
}

func (e *NotFoundError) Error() string {
	switch {
	case len(e.Suggestions) == 0 && e.Group != "":
		return fmt.Sprintf("unknown secret %q; %s/ has: %s (other secrets, not a fix for this name)", e.Name, e.Group, strings.Join(e.InGroup, ", "))
	case len(e.Suggestions) == 0:
		return fmt.Sprintf("unknown secret %q", e.Name)
	case len(e.Suggestions) <= 3:
		return fmt.Sprintf("unknown secret %q; closest: %s", e.Name, strings.Join(e.Suggestions, ", "))
	}
	return fmt.Sprintf("unknown secret %q; closest:\n  %s", e.Name, strings.Join(e.Suggestions, "\n  "))
}

func notFound(name string, f *file) *NotFoundError {
	names := make([]string, 0, len(f.Entries))
	for n := range f.Entries {
		names = append(names, n)
	}
	nf := &NotFoundError{Name: name, Suggestions: Suggest(name, names)}
	if len(nf.Suggestions) == 0 {
		nf.Group, nf.InGroup = groupContents(name, names)
	}
	return nf
}

// groupContents returns the deepest stored group that typed lies in (SHOP for SHOP/DEV/DB) and
// what it holds one level down, as "DB" for an entry and "PROD/" for a group. Both are stored
// strings only.
func groupContents(typed string, names []string) (string, []string) {
	segs := strings.Split(strings.ToUpper(strings.Trim(typed, "/")), "/")
	have := Groups(names)
	for i := len(segs) - 1; i > 0; i-- {
		group := strings.Join(segs[:i], "/")
		if !slices.Contains(have, group) {
			continue
		}
		held := map[string]bool{}
		for _, n := range names {
			rest, ok := strings.CutPrefix(n, group+"/")
			if !ok {
				continue
			}
			if head, _, deeper := strings.Cut(rest, "/"); deeper {
				rest = head + "/"
			}
			held[rest] = true
		}
		return group, slices.Sorted(maps.Keys(held))
	}
	return "", nil
}

// Suggest returns the names among names that typed was most likely meant to be, best first. The
// result holds only strings from names. The first rule that finds anything decides:
//
//  1. the same letters in another case or with another separator (shop/prod/db, SHOP_PROD_DB);
//  2. every name that has the typed parts in order, those that end with them first: DB gives
//     every */DB, SHOP/PROD gives everything in that group. Not cut: the right one must be there;
//  3. up to 3 names a typo away in one part (so PROD and DEV, or PROD and PRE, are never taken
//     for each other), or that contain or are contained in typed.
func Suggest(typed string, names []string) []string {
	up := strings.ToUpper(strings.Trim(typed, "/"))
	if up == "" {
		return nil
	}
	segs := strings.Split(up, "/")

	key := looseKey(up)
	var same []string
	for _, n := range names {
		if looseKey(n) == key {
			same = append(same, n)
		}
	}
	if len(same) > 0 {
		sort.Strings(same)
		return same
	}

	var tail, rest []string
	for _, n := range names {
		ns := strings.Split(n, "/")
		switch {
		case len(ns) <= len(segs) || !isSubsequence(segs, ns):
		case slices.Equal(segs, ns[len(ns)-len(segs):]):
			tail = append(tail, n)
		default:
			rest = append(rest, n)
		}
	}
	if len(tail)+len(rest) > 0 {
		sort.Strings(tail)
		sort.Strings(rest)
		return append(tail, rest...)
	}

	type cand struct {
		name string
		dist int
	}
	var cands []cand
	for _, n := range names {
		d, ok := typoDistance(segs, strings.Split(n, "/"))
		if !ok && len(up) >= 3 && len(n) >= 3 && (strings.Contains(n, up) || strings.Contains(up, n)) {
			d, ok = distance(up, n), true
		}
		if ok {
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

// looseKey makes names that differ only in case or in the separator written (/ . - : \ space)
// equal. It is the "I typed it the way I would write a flat name" rule.
func looseKey(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("/.-:\\ ", r) {
			return '_'
		}
		return r
	}, strings.ToUpper(s))
}

// isSubsequence reports whether all of want appear in have, whole and in order.
func isSubsequence(want, have []string) bool {
	i := 0
	for _, h := range have {
		if i < len(want) && h == want[i] {
			i++
		}
	}
	return i == len(want)
}

// typoDistance compares two names part by part. They need the same number of parts and at most
// one part may differ, by a typo. SHOP/PRD/DB is PROD mistyped; it is not also SHOP mistyped as SHIP.
func typoDistance(a, b []string) (int, bool) {
	if len(a) != len(b) {
		return 0, false
	}
	total, differing := 0, 0
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		d, ok := partTypo(a[i], b[i])
		if !ok {
			return 0, false
		}
		total += d
		differing++
	}
	return total, differing == 1
}

// partTypo is the edit distance of two name parts and whether it is small enough to be a typo.
// Short parts allow fewer edits, because one letter changes PRE into PRO and DEV into DEB.
func partTypo(a, b string) (int, bool) {
	d := distance(a, b)
	limit := 2
	switch n := min(len(a), len(b)); {
	case n < 3:
		limit = 0
		if len(a) == 2 && len(b) == 2 && a[0] == b[1] && a[1] == b[0] { // DB typed BD
			limit = 1
		}
	case n < 6:
		limit = 1
	}
	return d, d <= limit
}

// distance is the optimal-string-alignment distance between a and b: insertions, deletions,
// substitutions and swaps of two neighbouring letters (SHPO for SHOP) each cost 1.
func distance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

// Groups returns every group that holds an entry, however deep, sorted and without a trailing
// "/": SHOP and SHOP/PROD for SHOP/PROD/DB. Groups are not stored; they are the names' prefixes.
func Groups(names []string) []string {
	set := map[string]bool{}
	for _, n := range names {
		for i := 0; i < len(n); i++ {
			if n[i] == '/' {
				set[n[:i]] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// NewGroupNear is for a name about to be created. It finds the first group of name that does
// not exist yet (SHPO for SHPO/PROD/DB) and returns it with the existing groups beside it that it
// is a typo of (SHOP). With no such group, or none close, it returns "" and nil: a new group
// that resembles nothing needs no question.
func NewGroupNear(name string, names []string) (group string, near []string) {
	have := Groups(names)
	segs := strings.Split(name, "/")
	for i := 1; i < len(segs); i++ {
		group = strings.Join(segs[:i], "/")
		if slices.Contains(have, group) {
			continue
		}
		parent := strings.Join(segs[:i-1], "/")
		for _, h := range have {
			hs := strings.Split(h, "/")
			if _, ok := partTypo(segs[i-1], hs[len(hs)-1]); ok && len(hs) == i && strings.Join(hs[:i-1], "/") == parent {
				near = append(near, h)
			}
		}
		if len(near) == 0 {
			return "", nil
		}
		return group, near
	}
	return "", nil
}
