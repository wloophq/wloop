package slackrows

import (
	"cmp"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

// TitleRunes is the longest title a row takes from a message.
const TitleRunes = 80

var markup = regexp.MustCompile(`<([@#!]?)([^<>|]*)(?:\|([^<>]*))?>`)

var unescape = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// plain is Slack's mrkdwn text as a person reads it: mentions by name,
// links by their label.
func plain(text string, users map[string]sqlite.UserRow) string {
	text = markup.ReplaceAllStringFunc(text, func(s string) string {
		m := markup.FindStringSubmatch(s)
		sigil, target, label := m[1], m[2], m[3]
		switch sigil {
		case "@":
			return "@" + cmp.Or(nameOf(target, users), label, target)
		case "#":
			return "#" + cmp.Or(label, target)
		case "!":
			if label != "" {
				return label
			}
			name, _, _ := strings.Cut(target, "^")
			return "@" + name
		}
		return cmp.Or(label, target)
	})
	return unescape.Replace(text)
}

// firstLine is text's first non-blank line, cut to n runes.
func firstLine(text string, n int) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return cut(line, n)
		}
	}
	return ""
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// nameOf is how a person reads a user: the display name, else the name.
func nameOf(id string, users map[string]sqlite.UserRow) string {
	u := users[id]
	return cmp.Or(u.DisplayName, u.Name)
}
