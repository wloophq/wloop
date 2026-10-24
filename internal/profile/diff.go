package profile

import (
	"fmt"
	"strings"
)

const diffContext = 3

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type lineEdit struct {
	op   byte
	line string
}

func lineEdits(a, b []string) []lineEdit {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []lineEdit
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, lineEdit{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, lineEdit{'-', a[i]})
			i++
		default:
			out = append(out, lineEdit{'+', b[j]})
			j++
		}
	}
	return out
}

func hunkRange(start, n int) string {
	if n == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if n == 1 {
		return fmt.Sprint(start + 1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

// unified writes the unified diff of before and after, "" when they are equal.
func unified(from, to, before, after string) string {
	edits := lineEdits(splitLines(before), splitLines(after))
	var changed []int
	for i, e := range edits {
		if e.op != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- " + from + "\n+++ " + to + "\n")
	for k := 0; k < len(changed); {
		lo := max(changed[k]-diffContext, 0)
		end := k
		for end+1 < len(changed) && changed[end+1]-changed[end] <= 2*diffContext {
			end++
		}
		hi := min(changed[end]+diffContext+1, len(edits))
		aStart, bStart := 0, 0
		for _, e := range edits[:lo] {
			if e.op != '+' {
				aStart++
			}
			if e.op != '-' {
				bStart++
			}
		}
		aN, bN := 0, 0
		var body strings.Builder
		for _, e := range edits[lo:hi] {
			if e.op != '+' {
				aN++
			}
			if e.op != '-' {
				bN++
			}
			body.WriteString(string(e.op) + e.line + "\n")
		}
		fmt.Fprintf(&b, "@@ -%s +%s @@\n%s", hunkRange(aStart, aN), hunkRange(bStart, bN), body.String())
		k = end + 1
	}
	return b.String()
}
