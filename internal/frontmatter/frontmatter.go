// Package frontmatter splits a leading TOML block off a Markdown file and
// decodes it strictly, with positions in the file.
package frontmatter

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Error struct {
	File      string
	Line, Col int
	Key, Msg  string
}

// Error is "file:line:col: key: msg"; the key is left out when empty.
func (e *Error) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s", e.File, e.Line, e.Col, e.Key, e.Msg)
}

// Split separates a leading "+++" TOML block; bodyLine is the 1-based line where the body starts.
func Split(file, raw string) (head string, body string, bodyLine int, err error) {
	raw = strings.ReplaceAll(strings.TrimPrefix(raw, "\ufeff"), "\r\n", "\n")
	fail := func(msg string) (string, string, int, error) {
		return "", "", 0, &Error{File: file, Line: 1, Col: 1, Msg: msg}
	}
	if !strings.HasPrefix(raw, "+++\n") {
		switch lead := strings.TrimLeft(raw, " \t\n"); {
		case strings.HasPrefix(lead, "+++\n"):
			return fail("the +++ line must be the file's first line")
		case strings.HasPrefix(lead, "---\n"):
			return fail("front matter is TOML between +++ lines")
		}
		return "", raw, 1, nil
	}
	rest := raw[len("+++\n"):]
	n := 0
	for {
		line, after, more := strings.Cut(rest, "\n")
		if line == "+++" {
			head = strings.Join(strings.Split(raw[len("+++\n"):], "\n")[:n], "\n")
			return head, after, n + 3, nil
		}
		if !more {
			return fail("no closing +++")
		}
		rest = after
		n++
	}
}

// Decode decodes TOML into v with unknown keys rejected; positions are offset by firstLine-1.
func Decode(file string, src []byte, firstLine int, v any) error {
	dec := toml.NewDecoder(bytes.NewReader(src))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		return nil
	}
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) && len(strict.Errors) > 0 {
		de := strict.Errors[0]
		return positioned(file, firstLine, &de, "unknown key")
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		return positioned(file, firstLine, de, strings.TrimPrefix(de.Error(), "toml: "))
	}
	return &Error{File: file, Line: firstLine, Col: 1, Msg: err.Error()}
}

func positioned(file string, firstLine int, de *toml.DecodeError, msg string) *Error {
	row, col := de.Position()
	return &Error{File: file, Line: row + firstLine - 1, Col: col, Key: strings.Join(de.Key(), "."), Msg: msg}
}
