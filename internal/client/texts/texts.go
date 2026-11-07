// Package texts is the Go clients' catalog of the client strings the daemon serves.
package texts

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// Catalog reads client strings by key, in the locale the daemon served them in.
type Catalog struct {
	strings map[string]api.Text
}

// New is the catalog of s.
func New(s api.Strings) *Catalog {
	panic("not implemented: D11")
}

// English is the empty catalog: every key reads as itself.
func English() *Catalog { return &Catalog{} }

// T is key's text with params interpolated; key itself when the catalog lacks it.
func (c *Catalog) T(key string, params map[string]any) string {
	return interpolate(c.form(key, "other"), params)
}

// TN is key's text in n's plural form, with n and params interpolated; a key the catalog lacks reads as its
// other form, the key itself.
func (c *Catalog) TN(key string, n int, params map[string]any) string {
	form := "other"
	if n == 1 {
		form = "one"
	}
	all := map[string]any{"n": strconv.Itoa(n), "count": n}
	for k, v := range params {
		all[k] = v
	}
	return interpolate(c.form(key, form), all)
}

func (c *Catalog) form(key, form string) string {
	forms := c.strings[key]
	if s, ok := forms[form]; ok {
		return s
	}
	if s, ok := forms["other"]; ok {
		return s
	}
	return key
}

var placeholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func interpolate(text string, params map[string]any) string {
	return placeholder.ReplaceAllStringFunc(text, func(m string) string {
		if v, ok := params[m[1:len(m)-1]]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}

// Time is t formatted by the served format style (time, date, datetime).
func (c *Catalog) Time(t time.Time, style string) string {
	panic("not implemented: D11")
}

// Number is n with the served group separator.
func (c *Catalog) Number(n int) string {
	panic("not implemented: D11")
}

// Locale is the locale the strings were served in.
func (c *Catalog) Locale() string {
	panic("not implemented: D11")
}
