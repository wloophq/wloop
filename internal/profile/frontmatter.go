package profile

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/wloophq/wloop/internal/frontmatter"
)

// FrontMatterError carries Line and Col (1-based) when the problem has a position in the file.
type FrontMatterError struct {
	File, Key, Problem string
	Line, Col          int
}

func (e FrontMatterError) Error() string {
	where := e.File
	if e.Line > 0 {
		where = fmt.Sprintf("%s:%d:%d", e.File, e.Line, e.Col)
	}
	if e.Key == "" {
		return where + ": front matter: " + e.Problem
	}
	return where + ": front matter " + e.Key + ": " + e.Problem
}

type FrontMatter struct {
	Override bool
	Title    string
	// Include lists files relative to the block's file.
	Include []string
	// Paths are globs, ** allowed, one of which a changed file must match.
	Paths     []string
	Delegates map[string][]string
}

func validGlob(g string) bool {
	if g == "" {
		return false
	}
	for _, seg := range strings.Split(g, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return false
		}
	}
	return true
}

type rawFrontMatter struct {
	Override  bool                `toml:"override"`
	Title     *string             `toml:"title"`
	Include   []string            `toml:"include"`
	Paths     []string            `toml:"paths"`
	Delegates map[string][]string `toml:"delegates"`
}

var keyHints = map[string]string{
	"override":  "must be true or false",
	"title":     "must be text",
	"include":   `must be a list of paths, such as ["policy.md"]`,
	"paths":     `must be a list of globs, such as ["src/**/*.go"]`,
	"delegates": `must be a table of <agent> = ["<skill>", ...], one list per agent`,
}

// splitFrontMatter separates a leading "+++" TOML block from the text. Keys:
// override (bool), title (text), include and paths (lists of strings),
// delegates (a table of agent to list of strings).
func splitFrontMatter(file, raw string) (FrontMatter, string, error) {
	var fm FrontMatter
	head, text, _, err := frontmatter.Split(file, raw)
	if err != nil {
		return fm, "", asFrontMatterError(file, err)
	}
	if head == "" {
		return fm, text, nil
	}
	var r rawFrontMatter
	if err := frontmatter.Decode(file, []byte(head), 2, &r); err != nil {
		return fm, "", asFrontMatterError(file, err)
	}
	bad := func(key, at, problem string) (FrontMatter, string, error) {
		pos := frontmatter.Positions([]byte(head), 2)[at]
		return fm, "", FrontMatterError{File: file, Key: key, Problem: problem, Line: pos.Line, Col: pos.Col}
	}
	fm = FrontMatter{Override: r.Override, Include: r.Include, Paths: r.Paths, Delegates: r.Delegates}
	if r.Title != nil {
		if fm.Title = strings.TrimSpace(*r.Title); fm.Title == "" {
			return bad("title", "title", "is empty")
		}
	}
	if r.Include != nil && len(r.Include) == 0 {
		return bad("include", "include", keyHints["include"])
	}
	if r.Paths != nil && len(r.Paths) == 0 {
		return bad("paths", "paths", keyHints["paths"])
	}
	for i, g := range r.Paths {
		if !validGlob(g) {
			return bad("paths", fmt.Sprintf("paths[%d]", i), g+" is not a valid glob")
		}
	}
	return fm, text, nil
}

func asFrontMatterError(file string, err error) error {
	var fe *frontmatter.Error
	if !errors.As(err, &fe) {
		return FrontMatterError{File: file, Problem: err.Error()}
	}
	out := FrontMatterError{File: file, Key: fe.Key, Problem: fe.Msg, Line: fe.Line, Col: fe.Col}
	top, _, _ := strings.Cut(fe.Key, ".")
	switch hint, known := keyHints[top]; {
	case fe.Msg == "unknown key":
		out.Problem = "unknown key; the keys are override, title, include, paths and delegates"
	case known:
		out.Key, out.Problem = top, hint
	}
	return out
}
