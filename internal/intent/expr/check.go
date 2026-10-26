package expr

import (
	"fmt"
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
