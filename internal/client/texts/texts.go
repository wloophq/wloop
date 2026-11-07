// Package texts is the Go clients' catalog of the client strings the daemon serves.
package texts

import (
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// Catalog reads client strings by key, in the locale the daemon served them in.
type Catalog struct{}

// New is the catalog of s.
func New(s api.Strings) *Catalog {
	panic("not implemented: D11")
}

// English is the empty catalog: every key reads as itself.
func English() *Catalog {
	panic("not implemented: D11")
}

// T is key's text with params interpolated; key itself when the catalog lacks it.
func (c *Catalog) T(key string, params map[string]any) string {
	panic("not implemented: D11")
}

// TN is key's text in n's plural form, with n and params interpolated.
func (c *Catalog) TN(key string, n int, params map[string]any) string {
	panic("not implemented: D11")
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
