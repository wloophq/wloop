package brief

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/github"
)

const (
	bodyMax  = 8000
	issueMax = 4000
)

// Source reads a brief's input through the GitHub client: one GraphQL call
// for the facts under 100 files, and one REST call for the diff.
type Source struct{ GitHub *github.Client }

func (s Source) Facts(ctx context.Context, ref github.Ref) (Input, error) {
	f, err := s.GitHub.BriefFacts(ctx, ref)
	if err != nil {
		return Input{}, err
	}
	in := Input{Title: f.Title, Body: cut(f.Body, bodyMax)}
	for _, file := range f.Files {
		in.Files = append(in.Files, fmt.Sprintf("%s (+%d -%d)", file.Path, file.Additions, file.Deletions))
		in.Paths = append(in.Paths, file.Path)
	}
	if f.MoreFiles > 0 {
		in.Files = append(in.Files, fmt.Sprintf("and %d more files", f.MoreFiles))
	}
	if f.Issue != nil {
		in.Issue = f.Issue.Title + "\n\n" + cut(f.Issue.Body, issueMax)
	}
	return in, nil
}

// Diff is ref's diff, at most limit bytes ending on a rune boundary, and
// whether it was cut.
func (s Source) Diff(ctx context.Context, ref github.Ref, limit int) (string, bool, error) {
	b, cut, err := s.GitHub.Diff(ctx, ref, limit)
	if err != nil {
		return "", false, err
	}
	if cut {
		b = wholeRunes(b)
	}
	return string(b), cut, nil
}

// wholeRunes drops a rune the read cut in two at the end of b.
func wholeRunes(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}

// cut keeps at most n bytes of s, ending on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

var checkbox = regexp.MustCompile(`(?m)^(\s*(?:[-*+]|\d+[.)])\s+)\[[xX ]\]`)

// InputSHA hashes what a brief is made from, the head included: the facts
// with every task-list box read as unticked, so ticking one changes nothing.
// settings are the other inputs of the call that the version does not cover.
func InputSHA(head, settings string, in Input) string {
	h := sha256.Sum256([]byte(strings.Join([]string{head, settings, in.Title, checkbox.ReplaceAllString(in.Body, "$1[ ]"), in.Issue, strings.Join(in.Files, "\n")}, "\x00")))
	return hex.EncodeToString(h[:])
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
