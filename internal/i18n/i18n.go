// Package i18n matches locales, picks a count's CLDR plural form and interpolates {name} placeholders.
package i18n

// Default is the language every text is written in first, and the fallback.
const Default = "en"

// Normalize turns a POSIX or loosely written locale (pt_BR.UTF-8, pt-br) into a BCP 47 tag (pt-BR); C, POSIX
// and "" are "".
func Normalize(raw string) string {
	panic("not implemented: D1")
}

// Match is the best of available for prefs, most preferred first; Default when none fits.
func Match(prefs []string, available []string) string {
	panic("not implemented: D1")
}

// Pick is the configured locale when set, normalized even when unavailable so formats follow it; else
// Match(detected, available).
func Pick(configured string, detected []string, available []string) string {
	panic("not implemented: D1")
}

// Interpolate replaces each {name} of text with params[name]; an unknown {name} stays as written.
func Interpolate(text string, params map[string]any) string {
	panic("not implemented: D1")
}
