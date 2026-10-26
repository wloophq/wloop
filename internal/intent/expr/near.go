package expr

import (
	"strconv"
	"strings"
)

// Closest is the option a typo of s plausibly meant, "" when none is that
// close: the same word in another case, or one at most max(1, len(s)/4)
// edits away, a swap of two neighbors counting as one. A tie is no
// suggestion, since either guess may be wrong.
func Closest(s string, options []string) string {
	for _, o := range options {
		if o != s && strings.EqualFold(o, s) {
			return o
		}
	}
	best, bestD, tie := "", max(1, len(s)/4)+1, false
	for _, o := range options {
		switch d := distance(s, o); {
		case o == "" || o == s:
		case d < bestD:
			best, bestD, tie = o, d, false
		case d == bestD && o != best:
			tie = true
		}
	}
	if tie {
		return ""
	}
	return best
}

// DidYouMean is "; did you mean \"x\"?" for Closest's x, "" when none.
func DidYouMean(s string, options []string) string {
	if c := Closest(s, options); c != "" {
		return "; did you mean " + strconv.Quote(c) + "?"
	}
	return ""
}

// distance is the optimal string alignment distance of a and b: inserts,
// deletes, substitutions and swaps of two neighbors.
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
