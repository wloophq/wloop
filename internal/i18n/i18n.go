// Package i18n matches locales, picks a count's CLDR plural form and interpolates {name} placeholders.
package i18n

import (
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// Default is the language every text is written in first, and the fallback.
const Default = "en"

// Normalize turns a POSIX or loosely written locale (pt_BR.UTF-8, pt-br) into a BCP 47 tag (pt-BR); C, POSIX
// and "" are "".
func Normalize(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	if s == "" || s == "C" || s == "POSIX" {
		return ""
	}
	tag, err := language.Parse(strings.ReplaceAll(s, "_", "-"))
	if err != nil || tag == language.Und {
		return ""
	}
	return tag.String()
}

// Match is the best of available for prefs, most preferred first; Default when none fits.
func Match(prefs []string, available []string) string {
	tags := []language.Tag{language.Make(Default)}
	names := []string{Default}
	for _, a := range available {
		if t, err := language.Parse(a); err == nil && a != Default {
			tags = append(tags, t)
			names = append(names, a)
		}
	}
	var want []language.Tag
	for _, p := range prefs {
		if n := Normalize(p); n != "" {
			want = append(want, language.Make(n))
		}
	}
	if len(want) == 0 {
		return Default
	}
	_, i, conf := language.NewMatcher(tags).Match(want...)
	if conf == language.No {
		return Default
	}
	return names[i]
}

// Pick is the configured locale when set, normalized even when unavailable so formats follow it; else
// Match(detected, available).
func Pick(configured string, detected []string, available []string) string {
	if n := Normalize(configured); n != "" {
		return n
	}
	return Match(detected, available)
}

// Interpolate replaces each {name} of text with params[name]; an unknown {name} stays as written.
func Interpolate(text string, params map[string]any) string {
	if len(params) == 0 || !strings.Contains(text, "{") {
		return text
	}
	var b strings.Builder
	for {
		open := strings.IndexByte(text, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(text[open:], '}')
		if end < 0 {
			break
		}
		name := text[open+1 : open+end]
		v, ok := params[name]
		if !ok || name == "" || strings.ContainsAny(name, " {") {
			b.WriteString(text[:open+1])
			text = text[open+1:]
			continue
		}
		b.WriteString(text[:open])
		fmt.Fprint(&b, v)
		text = text[open+end+1:]
	}
	b.WriteString(text)
	return b.String()
}
