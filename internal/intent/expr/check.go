package expr

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

func (t Type) String() string {
	switch t {
	case Bool:
		return "bool"
	case Int:
		return "int"
	case String:
		return "string"
	case List:
		return "list"
	case Duration:
		return "duration"
	case Time:
		return "timestamp"
	}
	return "unknown"
}

func newError(src string, pos int, format string, args ...any) *Error {
	col := utf8.RuneCountInString(src[:pos]) + 1
	lineStart := strings.LastIndexByte(src[:pos], '\n') + 1
	lineEnd := len(src)
	if i := strings.IndexByte(src[pos:], '\n'); i >= 0 {
		lineEnd = pos + i
	}
	pad := []rune(src[lineStart:pos])
	for i, r := range pad {
		if r != '\t' {
			pad[i] = ' '
		}
	}
	return &Error{
		Col:     col,
		Msg:     fmt.Sprintf(format, args...),
		Snippet: src[lineStart:lineEnd] + "\n" + string(pad) + "^",
	}
}

func suggest(name string, env Env) string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	best, bestD := "", 3
	for _, k := range names {
		if d := levenshtein(name, k); d < bestD {
			best, bestD = k, d
		}
	}
	if best == "" {
		return ""
	}
	return "; did you mean " + best + "?"
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}
