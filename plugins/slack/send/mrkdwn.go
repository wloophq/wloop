package send

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	headingRE = regexp.MustCompile(`^ {0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	quoteRE   = regexp.MustCompile(`^(\s*>+ ?)(.*)$`)
	bulletRE  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)

	slackTokenRE = regexp.MustCompile(`<(?:[@#!][^<>\s]*|(?:https?|mailto):[^<>\s|]+(?:\|[^<>]*)?)>`)
	// linkTargetRE are the URLs a Markdown link may point at; any other target, a Slack token such as
	// !here or @U123 included, stays text.
	linkTargetRE = regexp.MustCompile(`^(?i:https?://|mailto:)`)
	linkRE       = regexp.MustCompile(`\[([^\]\n]+)\]\(([^()\s]+)\)`)
	placeholdRE  = regexp.MustCompile(placeOpen + `(\d+)` + placeClose)

	boldStarRE   = regexp.MustCompile(`\*\*([^\s*](?:[^*\n]*[^\s*])?)\*\*`)
	boldUnderRE  = regexp.MustCompile(`__([^\s_](?:[^_\n]*[^\s_])?)__`)
	italicStarRE = regexp.MustCompile(`\*([^\s*](?:[^*\n]*[^\s*])?)\*`)
	strikeRE     = regexp.MustCompile(`~~([^\s~](?:[^~\n]*[^\s~])?)~~`)

	escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

// placeOpen and placeClose delimit a set-aside piece of a line; boldMark stands for Slack's bold "*"
// until italics, which Markdown also writes with "*", are converted. Mrkdwn strips them from its input.
const (
	placeOpen  = "\uE000"
	placeClose = "\uE001"
	boldMark   = "\uE002"
)

var stripMarks = strings.NewReplacer(placeOpen, "", placeClose, "", boldMark, "")

// Mrkdwn converts Markdown to Slack mrkdwn. Slack mentions and links written as <...> pass through;
// &, < and > are escaped everywhere else, code included, since Slack parses <...> inside code too.
func Mrkdwn(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(stripMarks.Replace(markdown), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+1)
	inCode := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			out = append(out, "```")
			continue
		}
		if inCode {
			out = append(out, escaper.Replace(line))
			continue
		}
		out = append(out, mrkdwnLine(line))
	}
	if inCode {
		out = append(out, "```")
	}
	return strings.Join(out, "\n")
}

func mrkdwnLine(line string) string {
	if m := headingRE.FindStringSubmatch(line); m != nil {
		return "*" + inline(m[1]) + "*"
	}
	if m := quoteRE.FindStringSubmatch(line); m != nil {
		return m[1] + inline(m[2])
	}
	if m := bulletRE.FindStringSubmatch(line); m != nil {
		return m[1] + "• " + inline(m[2])
	}
	return inline(line)
}

// inline converts one line outside code blocks: code spans, Slack tokens and links are set aside as
// placeholders, the rest is escaped and its emphasis converted, then the placeholders come back.
func inline(s string) string {
	var kept []string
	keep := func(v string) string {
		kept = append(kept, v)
		return placeOpen + strconv.Itoa(len(kept)-1) + placeClose
	}

	var b strings.Builder
	for {
		open := strings.IndexByte(s, '`')
		if open < 0 {
			break
		}
		end := strings.IndexByte(s[open+1:], '`')
		if end < 0 {
			break
		}
		b.WriteString(s[:open])
		b.WriteString(keep("`" + escaper.Replace(s[open+1:open+1+end]) + "`"))
		s = s[open+end+2:]
	}
	b.WriteString(s)
	s = b.String()

	s = slackTokenRE.ReplaceAllStringFunc(s, keep)
	s = linkRE.ReplaceAllStringFunc(s, func(m string) string {
		parts := linkRE.FindStringSubmatch(m)
		if !linkTargetRE.MatchString(parts[2]) {
			return keep(escaper.Replace(m))
		}
		return keep("<" + escaper.Replace(parts[2]) + "|" + emphasis(escaper.Replace(parts[1])) + ">")
	})
	s = emphasis(escaper.Replace(s))

	for range len(kept) {
		s = placeholdRE.ReplaceAllStringFunc(s, func(m string) string {
			i, err := strconv.Atoi(placeholdRE.FindStringSubmatch(m)[1])
			if err != nil || i >= len(kept) {
				return m
			}
			return kept[i]
		})
	}
	return s
}

func emphasis(s string) string {
	s = boldStarRE.ReplaceAllString(s, boldMark+"$1"+boldMark)
	s = boldUnderRE.ReplaceAllString(s, boldMark+"$1"+boldMark)
	s = italicStarRE.ReplaceAllString(s, "_${1}_")
	s = strikeRE.ReplaceAllString(s, "~${1}~")
	return strings.ReplaceAll(s, boldMark, "*")
}
