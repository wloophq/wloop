// Package texts is the Go clients' catalog of the client strings the daemon serves.
package texts

import (
	"maps"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/i18n"
)

// Catalog reads client strings by key, in the locale the daemon served them in.
type Catalog struct {
	locale  string
	strings map[string]api.Text
	formats map[string]string
}

// englishFormats are the formats of a catalog the daemon served none of.
var englishFormats = map[string]string{"time": "3:04 PM", "date": "Jan 2", "datetime": "Jan 2 3:04 PM", "decimal": ".", "group": ","}

// New is the catalog of s.
func New(s api.Strings) *Catalog {
	return &Catalog{locale: s.Locale, strings: s.Strings, formats: s.Formats}
}

// English is the empty catalog: every key reads as itself.
func English() *Catalog { return &Catalog{} }

// T is key's text with params interpolated; key itself when the catalog lacks it.
func (c *Catalog) T(key string, params map[string]any) string {
	return i18n.Interpolate(c.forms(key)["other"], params)
}

// TN is key's text in n's plural form, with n and params interpolated; a key the catalog lacks reads as its
// other form, the key itself.
func (c *Catalog) TN(key string, n int, params map[string]any) string {
	all := map[string]any{"n": c.Number(n), "count": n}
	maps.Copy(all, params)
	return i18n.Interpolate(c.forms(key).For(c.Locale(), float64(n)), all)
}

var (
	pluralWord = regexp.MustCompile(`\{\{\s*plural\s+\.(\w+)((?:\s+"[^"]*")+)\s*\}\}`)
	pluralPair = regexp.MustCompile(`"([^"]*)"`)
	valueWord  = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)
)

// Fill is a plugin's word with each {{.name}} replaced by vars[name], and each
// {{plural .name "one" "…" "other" "…"}} by the form of vars[name] in the
// catalog's locale, as the cockpit fills a word.
func (c *Catalog) Fill(word string, vars map[string]string) string {
	word = pluralWord.ReplaceAllStringFunc(word, func(m string) string {
		sub := pluralWord.FindStringSubmatch(m)
		pairs := pluralPair.FindAllStringSubmatch(sub[2], -1)
		forms := i18n.Forms{}
		for i := 0; i+1 < len(pairs); i += 2 {
			forms[pairs[i][1]] = pairs[i+1][1]
		}
		n, _ := strconv.ParseFloat(vars[sub[1]], 64)
		return forms.For(c.Locale(), n)
	})
	return valueWord.ReplaceAllStringFunc(word, func(m string) string { return vars[valueWord.FindStringSubmatch(m)[1]] })
}

func (c *Catalog) forms(key string) i18n.Forms {
	if f := c.strings[key]; f["other"] != "" {
		return i18n.Forms(f)
	}
	return i18n.Forms{"other": key}
}

func (c *Catalog) format(name string) string {
	if f := c.formats[name]; f != "" {
		return f
	}
	return englishFormats[name]
}

// Time is t formatted by the served format style (time, date, datetime).
func (c *Catalog) Time(t time.Time, style string) string {
	layout := c.format(style)
	if layout == "" {
		layout = c.format("datetime")
	}
	return t.Format(layout)
}

// Number is n with the served group separator.
func (c *Catalog) Number(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if n < 0 {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(c.format("group"))
		}
		b.WriteRune(d)
	}
	return sign + b.String()
}

// Locale is the locale the strings were served in.
func (c *Catalog) Locale() string {
	if c.locale == "" {
		return i18n.Default
	}
	return c.locale
}
